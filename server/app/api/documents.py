from __future__ import annotations

import asyncio
import base64
import binascii
import json
import logging
from datetime import datetime, time, timedelta
from typing import Annotated, Any
from urllib.parse import urlsplit

from fastapi import APIRouter, Depends, HTTPException, Query
from pydantic import BaseModel
from sqlalchemy import select
from sqlalchemy.dialects.sqlite import insert as sqlite_insert
from sqlalchemy.ext.asyncio import AsyncSession

from ..adapters import ADAPTERS, Adapter, DocRef, available, decode_doc_key, make_doc_key
from ..config import settings
from ..db import get_db
from ..mcp_bridge import bridge, classify_result
from ..models import DocumentCache, DocumentView, User, utcnow
from ..security import get_current_user

logger = logging.getLogger("yargi_asistan.documents")

router = APIRouter(prefix="/documents", tags=["documents"])

Database = Annotated[AsyncSession, Depends(get_db)]
CurrentUser = Annotated[User, Depends(get_current_user)]
DocumentPage = Annotated[int, Query(ge=1, le=10_000)]
EncodedMeta = Annotated[str | None, Query(max_length=50_000)]
_view_write_lock = asyncio.Lock()

_STRING_DOCUMENT_ARGUMENTS = {
    "bedesten": "documentId",
    "emsal": "id",
    "kik": "gundemMaddesiId",
    "rekabet": "karar_id",
    "bddk": "document_id",
}
_URL_DOCUMENT_ARGUMENTS = {
    "anayasa": (
        "document_url",
        {
            "normkararlarbilgibankasi.anayasa.gov.tr",
            "kararlarbilgibankasi.anayasa.gov.tr",
        },
    ),
    "uyusmazlik": ("document_url", {"kararlar.uyusmazlik.gov.tr"}),
    "kvkk": ("decision_url", {"www.kvkk.gov.tr"}),
    "btk": ("pdf_url", {"www.btk.gov.tr", "www.btk.tr"}),
}


def _json_dump(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def _require_adapter(source_db: str) -> Adapter:
    adapter = ADAPTERS.get(source_db)
    if adapter is None:
        raise HTTPException(status_code=404, detail="Kaynak bulunamadı")
    return adapter


def _invalid_document_key() -> HTTPException:
    return HTTPException(status_code=400, detail="Belge anahtarı geçersiz")


def _valid_string(value: Any, *, max_length: int = 4_096) -> bool:
    return isinstance(value, str) and bool(value.strip()) and len(value) <= max_length


def _valid_official_url(value: Any, allowed_hosts: set[str]) -> bool:
    if not _valid_string(value):
        return False
    try:
        parsed = urlsplit(value)
        port = parsed.port
    except ValueError:
        return False
    return (
        parsed.scheme == "https"
        and parsed.hostname in allowed_hosts
        and port in {None, 443}
        and parsed.username is None
        and parsed.password is None
    )


def validate_document_args(source_db: str, arguments: dict[str, Any]) -> None:
    if source_db in _STRING_DOCUMENT_ARGUMENTS:
        key = _STRING_DOCUMENT_ARGUMENTS[source_db]
        if set(arguments) == {key} and _valid_string(arguments.get(key), max_length=1_000):
            return
        raise _invalid_document_key()

    if source_db in _URL_DOCUMENT_ARGUMENTS:
        key, allowed_hosts = _URL_DOCUMENT_ARGUMENTS[source_db]
        if set(arguments) == {key} and _valid_official_url(arguments.get(key), allowed_hosts):
            return
        raise _invalid_document_key()

    if source_db == "sayistay":
        if (
            set(arguments) == {"decision_id", "decision_type"}
            and _valid_string(arguments.get("decision_id"), max_length=1_000)
            and arguments.get("decision_type")
            in {"genel_kurul", "temyiz_kurulu", "daire"}
        ):
            return
        raise _invalid_document_key()

    if source_db == "gib":
        value = arguments.get("ozelge_id")
        if (
            set(arguments) == {"ozelge_id"}
            and isinstance(value, int)
            and not isinstance(value, bool)
        ):
            return
        raise _invalid_document_key()

    if source_db == "sigorta":
        value = arguments.get("issue_number")
        if (
            set(arguments) == {"issue_number"}
            and isinstance(value, int)
            and not isinstance(value, bool)
            and 1 <= value <= 64
        ):
            return
        raise _invalid_document_key()

    raise _invalid_document_key()


def _decode_args(source_db: str, doc_key: str) -> dict[str, Any]:
    try:
        arguments = decode_doc_key(doc_key)
    except (
        binascii.Error,
        json.JSONDecodeError,
        TypeError,
        UnicodeDecodeError,
        ValueError,
    ) as error:
        raise _invalid_document_key() from error
    if not isinstance(arguments, dict) or make_doc_key(arguments) != doc_key:
        raise _invalid_document_key()
    validate_document_args(source_db, arguments)
    return arguments


def _decode_meta(encoded_meta: str | None) -> dict[str, Any]:
    if encoded_meta is None or not encoded_meta.strip():
        return {}
    raw = encoded_meta.strip()
    try:
        if raw.startswith("{"):
            value = json.loads(raw)
        else:
            padding = "=" * (-len(raw) % 4)
            decoded = base64.urlsafe_b64decode(raw + padding).decode("utf-8")
            value = json.loads(decoded)
    except (binascii.Error, json.JSONDecodeError, UnicodeDecodeError, ValueError) as error:
        raise HTTPException(status_code=400, detail="Belge metadatası geçersiz") from error
    if not isinstance(value, dict):
        raise HTTPException(status_code=400, detail="Belge metadatası geçersiz")
    return value


def _payload_dict(payload: Any) -> dict[str, Any]:
    if isinstance(payload, BaseModel):
        return payload.model_dump(mode="json")
    if isinstance(payload, dict):
        return payload
    if isinstance(payload, str):
        try:
            value = json.loads(payload)
        except json.JSONDecodeError as error:
            raise HTTPException(
                status_code=502,
                detail="Belge kaynağı geçersiz yanıt verdi",
            ) from error
        if isinstance(value, dict):
            return value
    raise HTTPException(status_code=502, detail="Belge kaynağı geçersiz yanıt verdi")


def _raise_source_error(payload: Any) -> None:
    try:
        source_status, error_message, retry_after = classify_result(payload)
    except (TypeError, ValueError) as error:
        raise HTTPException(
            status_code=502,
            detail="Belge kaynağı geçersiz yanıt verdi",
        ) from error
    if source_status == "ok":
        return
    if source_status == "rate_limited":
        headers = {"Retry-After": str(retry_after)} if retry_after is not None else None
        raise HTTPException(
            status_code=429,
            detail=_optional_text(error_message) or "Kaynak hız sınırına ulaştı",
            headers=headers,
        )
    raise HTTPException(
        status_code=502,
        detail=_optional_text(error_message) or "Belge kaynağı yanıt veremedi",
    )


def _is_fresh(cache: DocumentCache) -> bool:
    fetched_at = cache.fetched_at.replace(tzinfo=None)
    cutoff = (utcnow() - timedelta(days=settings.doc_cache_ttl_days)).replace(tzinfo=None)
    return fetched_at >= cutoff


def _optional_text(value: Any) -> str | None:
    if value is None:
        return None
    text_value = str(value).strip()
    return text_value or None


def _display_meta(meta: dict[str, Any]) -> dict[str, str | None]:
    return {
        "court": _optional_text(meta.get("court")),
        "esas_no": _optional_text(meta.get("esas_no")),
        "karar_no": _optional_text(meta.get("karar_no")),
        "decision_date": _optional_text(meta.get("decision_date")),
    }


def _source_url(
    payload: dict[str, Any] | None,
    meta: dict[str, Any],
    arguments: dict[str, Any],
) -> str | None:
    candidates = [
        payload.get("source_url") if payload else None,
        meta.get("source_url"),
        arguments.get("document_url"),
        arguments.get("decision_url"),
        arguments.get("pdf_url"),
    ]
    for candidate in candidates:
        if value := _optional_text(candidate):
            return value
    return None


def _title(
    payload: dict[str, Any] | None,
    meta: dict[str, Any],
    cache: DocumentCache | None,
    adapter: Adapter,
) -> str:
    document_data = payload.get("document_data") if payload else None
    nested_title = document_data.get("title") if isinstance(document_data, dict) else None
    candidates = [
        payload.get("title") if payload else None,
        payload.get("title_on_landing_page") if payload else None,
        nested_title,
        meta.get("title"),
        cache.title if cache else None,
    ]
    for candidate in candidates:
        if value := _optional_text(candidate):
            return value
    return adapter.name


def _cache_title(
    payload: dict[str, Any],
    cache: DocumentCache | None,
    adapter: Adapter,
) -> str:
    document_data = payload.get("document_data")
    nested_title = document_data.get("title") if isinstance(document_data, dict) else None
    candidates = [
        payload.get("title"),
        payload.get("title_on_landing_page"),
        nested_title,
        cache.title if cache else None,
    ]
    for candidate in candidates:
        if value := _optional_text(candidate):
            return value
    return adapter.name


def _total_pages(payload: dict[str, Any], chunked: bool) -> int:
    if not chunked:
        return 1
    try:
        return max(1, int(payload.get("total_pages", 1)))
    except (TypeError, ValueError):
        return 1


def _validate_page_result(
    payload: dict[str, Any],
    requested_page: int,
    chunked: bool,
) -> None:
    if not chunked:
        return
    current_value = payload.get("current_page", payload.get("page_number"))
    total_value = payload.get("total_pages")
    try:
        current_page = int(current_value) if current_value is not None else requested_page
        total_pages = int(total_value) if total_value is not None else None
    except (TypeError, ValueError) as error:
        raise HTTPException(
            status_code=502,
            detail="Belge kaynağı geçersiz sayfa bilgisi verdi",
        ) from error
    if current_page != requested_page or (
        total_pages is not None and requested_page > total_pages
    ):
        raise HTTPException(status_code=404, detail="Belge sayfası bulunamadı")


async def _record_view(
    db: AsyncSession,
    user: User,
    source_db: str,
    doc_key: str,
    title: str,
    meta: dict[str, Any],
) -> None:
    now = utcnow()
    start_of_day = datetime.combine(now.date(), time.min)
    end_of_day = start_of_day + timedelta(days=1)
    view = await db.scalar(
        select(DocumentView).where(
            DocumentView.user_id == user.id,
            DocumentView.source_db == source_db,
            DocumentView.doc_key == doc_key,
            DocumentView.opened_at >= start_of_day,
            DocumentView.opened_at < end_of_day,
        )
    )
    if view is None:
        db.add(
            DocumentView(
                user_id=user.id,
                source_db=source_db,
                doc_key=doc_key,
                title=title,
                meta_json=_json_dump(meta) if meta else None,
                opened_at=now,
            )
        )
        return
    view.title = title
    if meta:
        view.meta_json = _json_dump(meta)
    view.opened_at = now


def _response(
    *,
    source_db: str,
    doc_key: str,
    adapter: Adapter,
    arguments: dict[str, Any],
    cache: DocumentCache,
    meta: dict[str, Any],
    cached: bool,
    payload: dict[str, Any] | None = None,
) -> dict[str, Any]:
    return {
        "source_db": source_db,
        "doc_key": doc_key,
        "title": _title(payload, meta, cache, adapter),
        "markdown": cache.markdown,
        "page": cache.chunk_page,
        "total_pages": cache.total_pages,
        "chunked": adapter.doc_chunked,
        "cached": cached,
        "fetched_at": cache.fetched_at,
        "source_url": _source_url(payload, meta, arguments),
        "meta": _display_meta(meta),
    }


async def _fetch_document(
    *,
    source_db: str,
    doc_key: str,
    page: int,
    meta: dict[str, Any],
    user: User,
    db: AsyncSession,
    bypass_cache: bool,
) -> dict[str, Any]:
    adapter = _require_adapter(source_db)
    if not adapter.doc_chunked and page != 1:
        raise HTTPException(status_code=400, detail="Bu belge sayfalı değil")
    cache_key = (source_db, doc_key, page)
    cache = await db.get(DocumentCache, cache_key)
    await db.commit()
    arguments = _decode_args(source_db, doc_key)

    if not bypass_cache and cache is not None and _is_fresh(cache):
        title = _title(None, meta, cache, adapter)
        async with _view_write_lock:
            await _record_view(db, user, source_db, doc_key, title, meta)
            await db.commit()
        return _response(
            source_db=source_db,
            doc_key=doc_key,
            adapter=adapter,
            arguments=arguments,
            cache=cache,
            meta=meta,
            cached=True,
        )

    if not available(source_db):
        raise HTTPException(
            status_code=409,
            detail="bu kaynak sunucuda yapılandırılmamış",
        )

    call_arguments = dict(arguments)
    if adapter.doc_chunked:
        call_arguments["page_number"] = page
    try:
        raw_payload = await bridge.call_tool(adapter.doc_tool, call_arguments)
    except TimeoutError as error:
        logger.warning("%s belgesi zaman aşımına uğradı", source_db)
        raise HTTPException(
            status_code=504,
            detail="Kaynak zaman aşımına uğradı",
        ) from error
    except Exception as error:
        logger.exception("%s belgesi alınamadı", source_db)
        raise HTTPException(
            status_code=502,
            detail="Belge kaynağı yanıt veremedi",
        ) from error

    _raise_source_error(raw_payload)
    payload = _payload_dict(raw_payload)
    _raise_source_error(payload)
    _validate_page_result(payload, page, adapter.doc_chunked)
    markdown = payload.get("markdown_content")
    if markdown is None:
        markdown = payload.get("markdown_chunk")
    if not isinstance(markdown, str):
        raise HTTPException(status_code=502, detail="Belge içeriği alınamadı")

    fetched_at = utcnow()
    title = _title(payload, meta, cache, adapter)
    cache_title = _cache_title(payload, cache, adapter)
    doc_ref = DocRef(
        tool=adapter.doc_tool,
        args=arguments,
        chunked=adapter.doc_chunked,
    )
    cache_values = {
        "source_db": source_db,
        "doc_key": doc_key,
        "chunk_page": page,
        "doc_ref_json": _json_dump(doc_ref.model_dump(mode="json")),
        "title": cache_title,
        "markdown": markdown,
        "total_pages": _total_pages(payload, adapter.doc_chunked),
        "fetched_at": fetched_at,
    }
    statement = sqlite_insert(DocumentCache).values(**cache_values)
    statement = statement.on_conflict_do_update(
        index_elements=[
            DocumentCache.source_db,
            DocumentCache.doc_key,
            DocumentCache.chunk_page,
        ],
        set_={
            "doc_ref_json": cache_values["doc_ref_json"],
            "title": cache_values["title"],
            "markdown": cache_values["markdown"],
            "total_pages": cache_values["total_pages"],
            "fetched_at": cache_values["fetched_at"],
        },
    )
    async with _view_write_lock:
        await db.execute(statement)
        await _record_view(db, user, source_db, doc_key, title, meta)
        await db.commit()
    if cache is None:
        cache = await db.get(DocumentCache, cache_key)
    else:
        await db.refresh(cache)
    if cache is None:
        raise HTTPException(status_code=500, detail="Belge önbelleğe alınamadı")
    return _response(
        source_db=source_db,
        doc_key=doc_key,
        adapter=adapter,
        arguments=arguments,
        cache=cache,
        meta=meta,
        cached=False,
        payload=payload,
    )


@router.get("/{source_db}/{doc_key}")
async def get_document(
    source_db: str,
    doc_key: str,
    user: CurrentUser,
    db: Database,
    page: DocumentPage = 1,
    meta: EncodedMeta = None,
) -> dict[str, Any]:
    return await _fetch_document(
        source_db=source_db,
        doc_key=doc_key,
        page=page,
        meta=_decode_meta(meta),
        user=user,
        db=db,
        bypass_cache=False,
    )


@router.post("/{source_db}/{doc_key}/refresh")
async def refresh_document(
    source_db: str,
    doc_key: str,
    user: CurrentUser,
    db: Database,
    meta: EncodedMeta = None,
) -> dict[str, Any]:
    return await _fetch_document(
        source_db=source_db,
        doc_key=doc_key,
        page=1,
        meta=_decode_meta(meta),
        user=user,
        db=db,
        bypass_cache=True,
    )
