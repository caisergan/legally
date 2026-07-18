from __future__ import annotations

import binascii
import json
import logging
from typing import Annotated, Any

from fastapi import APIRouter, Depends, HTTPException, Query, Response, status
from pydantic import BaseModel, ConfigDict, Field, field_validator
from sqlalchemy import select
from sqlalchemy.dialects.sqlite import insert as sqlite_insert
from sqlalchemy.ext.asyncio import AsyncSession

from ..adapters import ADAPTERS, DocRef, decode_doc_key, make_doc_key
from ..db import get_db
from ..models import Bookmark, User, utcnow
from ..security import get_current_user
from .documents import validate_document_args

logger = logging.getLogger("yargi_asistan.bookmarks")

router = APIRouter(prefix="/bookmarks", tags=["bookmarks"])

Database = Annotated[AsyncSession, Depends(get_db)]
CurrentUser = Annotated[User, Depends(get_current_user)]
Tag = Annotated[str, Field(min_length=1, max_length=100)]


class RequestModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class BookmarkCreate(RequestModel):
    source_db: str = Field(min_length=1, max_length=64)
    doc_key: str = Field(min_length=1, max_length=16_000)
    doc_ref: DocRef
    title: str = Field(min_length=1, max_length=2_000)
    meta: dict[str, Any] | None = None
    note: str | None = Field(default=None, max_length=10_000)
    tags: list[Tag] | None = Field(default=None, max_length=50)

    @field_validator("title")
    @classmethod
    def validate_title(cls, value: str) -> str:
        normalized = value.strip()
        if not normalized:
            raise ValueError("başlık boş olamaz")
        return normalized


class BookmarkUpdate(RequestModel):
    note: str | None = Field(default=None, max_length=10_000)
    tags: list[Tag] | None = Field(default=None, max_length=50)


def _json_dump(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def _json_object(value: str | None) -> dict[str, Any]:
    if not value:
        return {}
    try:
        parsed = json.loads(value)
    except (json.JSONDecodeError, TypeError):
        logger.warning("Kayıtlı karar metadatası okunamadı")
        return {}
    return parsed if isinstance(parsed, dict) else {}


def _json_list(value: str | None) -> list[str]:
    if not value:
        return []
    try:
        parsed = json.loads(value)
    except (json.JSONDecodeError, TypeError):
        logger.warning("Kayıtlı karar etiketleri okunamadı")
        return []
    if not isinstance(parsed, list):
        return []
    return [str(item) for item in parsed if str(item).strip()]


def _normalized_tags(tags: list[str] | None) -> list[str]:
    normalized: list[str] = []
    seen: set[str] = set()
    for tag in tags or []:
        value = tag.strip()
        identity = value.casefold()
        if not value or identity in seen:
            continue
        seen.add(identity)
        normalized.append(value)
    return normalized


def _validate_doc_ref(
    source_db: str,
    doc_key: str,
    doc_ref: DocRef,
) -> None:
    adapter = ADAPTERS.get(source_db)
    if adapter is None:
        raise HTTPException(status_code=404, detail="Kaynak bulunamadı")
    try:
        decoded_arguments = decode_doc_key(doc_key)
    except (
        binascii.Error,
        json.JSONDecodeError,
        TypeError,
        UnicodeDecodeError,
        ValueError,
    ) as error:
        raise HTTPException(status_code=400, detail="Belge anahtarı geçersiz") from error
    if (
        not isinstance(decoded_arguments, dict)
        or make_doc_key(decoded_arguments) != doc_key
        or decoded_arguments != doc_ref.args
        or make_doc_key(doc_ref.args) != doc_key
        or doc_ref.tool != adapter.doc_tool
        or doc_ref.chunked != adapter.doc_chunked
    ):
        raise HTTPException(status_code=400, detail="Belge referansı geçersiz")
    validate_document_args(source_db, decoded_arguments)


def _bookmark_response(bookmark: Bookmark) -> dict[str, Any]:
    return {
        "id": bookmark.id,
        "source_db": bookmark.source_db,
        "doc_key": bookmark.doc_key,
        "title": bookmark.title,
        "meta": _json_object(bookmark.meta_json),
        "note": bookmark.note,
        "tags": _json_list(bookmark.tags_json),
        "created_at": bookmark.created_at,
    }


async def _owned_bookmark(
    bookmark_id: int,
    user: User,
    db: AsyncSession,
) -> Bookmark:
    bookmark = await db.scalar(
        select(Bookmark).where(
            Bookmark.id == bookmark_id,
            Bookmark.user_id == user.id,
        )
    )
    if bookmark is None:
        raise HTTPException(status_code=404, detail="Kayıtlı karar bulunamadı")
    return bookmark


@router.get("")
async def list_bookmarks(
    user: CurrentUser,
    db: Database,
    tag: Annotated[str | None, Query(max_length=100)] = None,
) -> list[dict[str, Any]]:
    bookmarks = (
        await db.scalars(
            select(Bookmark)
            .where(Bookmark.user_id == user.id)
            .order_by(Bookmark.created_at.desc(), Bookmark.id.desc())
        )
    ).all()
    if tag and tag.strip():
        requested_tag = tag.strip().casefold()
        bookmarks = [
            bookmark
            for bookmark in bookmarks
            if requested_tag in {item.casefold() for item in _json_list(bookmark.tags_json)}
        ]
    return [_bookmark_response(bookmark) for bookmark in bookmarks]


@router.get("/tags")
async def list_tags(user: CurrentUser, db: Database) -> list[str]:
    values = (
        await db.scalars(
            select(Bookmark.tags_json).where(Bookmark.user_id == user.id)
        )
    ).all()
    tags_by_identity: dict[str, str] = {}
    for value in values:
        for tag in _json_list(value):
            tags_by_identity.setdefault(tag.casefold(), tag)
    return sorted(tags_by_identity.values(), key=str.casefold)


@router.get("/lookup")
async def lookup_bookmark(
    source_db: Annotated[str, Query(min_length=1, max_length=64)],
    doc_key: Annotated[str, Query(min_length=1, max_length=16_000)],
    user: CurrentUser,
    db: Database,
) -> dict[str, Any]:
    bookmark = await db.scalar(
        select(Bookmark).where(
            Bookmark.user_id == user.id,
            Bookmark.source_db == source_db,
            Bookmark.doc_key == doc_key,
        )
    )
    if bookmark is None:
        return {"bookmarked": False}
    return {"bookmarked": True, "id": bookmark.id}


@router.post("")
async def upsert_bookmark(
    payload: BookmarkCreate,
    user: CurrentUser,
    db: Database,
) -> dict[str, Any]:
    _validate_doc_ref(payload.source_db, payload.doc_key, payload.doc_ref)
    inserted_values = {
        "user_id": user.id,
        "source_db": payload.source_db,
        "doc_key": payload.doc_key,
        "doc_ref_json": _json_dump(payload.doc_ref.model_dump(mode="json")),
        "title": payload.title.strip(),
        "meta_json": _json_dump(payload.meta) if payload.meta is not None else None,
        "note": (payload.note or "").strip(),
        "tags_json": _json_dump(_normalized_tags(payload.tags)),
        "created_at": utcnow(),
    }
    updated_values: dict[str, Any] = {
        "doc_ref_json": inserted_values["doc_ref_json"],
        "title": inserted_values["title"],
    }
    if "meta" in payload.model_fields_set:
        updated_values["meta_json"] = inserted_values["meta_json"]
    if "note" in payload.model_fields_set:
        updated_values["note"] = inserted_values["note"]
    if "tags" in payload.model_fields_set:
        updated_values["tags_json"] = inserted_values["tags_json"]

    statement = sqlite_insert(Bookmark).values(**inserted_values)
    statement = statement.on_conflict_do_update(
        index_elements=[Bookmark.user_id, Bookmark.source_db, Bookmark.doc_key],
        set_=updated_values,
    )
    await db.execute(statement)
    await db.commit()
    bookmark = await db.scalar(
        select(Bookmark).where(
            Bookmark.user_id == user.id,
            Bookmark.source_db == payload.source_db,
            Bookmark.doc_key == payload.doc_key,
        )
    )
    if bookmark is None:
        raise HTTPException(status_code=500, detail="Kayıtlı karar oluşturulamadı")
    return _bookmark_response(bookmark)


@router.patch("/{bookmark_id}")
async def update_bookmark(
    bookmark_id: int,
    payload: BookmarkUpdate,
    user: CurrentUser,
    db: Database,
) -> dict[str, Any]:
    if not payload.model_fields_set:
        raise HTTPException(status_code=400, detail="Güncellenecek alan bulunamadı")
    bookmark = await _owned_bookmark(bookmark_id, user, db)
    if "note" in payload.model_fields_set:
        bookmark.note = (payload.note or "").strip()
    if "tags" in payload.model_fields_set:
        bookmark.tags_json = _json_dump(_normalized_tags(payload.tags))
    await db.commit()
    await db.refresh(bookmark)
    return _bookmark_response(bookmark)


@router.delete("/{bookmark_id}", status_code=status.HTTP_204_NO_CONTENT)
async def delete_bookmark(
    bookmark_id: int,
    user: CurrentUser,
    db: Database,
) -> Response:
    bookmark = await _owned_bookmark(bookmark_id, user, db)
    await db.delete(bookmark)
    await db.commit()
    return Response(status_code=status.HTTP_204_NO_CONTENT)
