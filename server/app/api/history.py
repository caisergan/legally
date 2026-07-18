from __future__ import annotations

import json
import logging
from typing import Annotated, Any

from fastapi import APIRouter, Depends, Query
from sqlalchemy import func, select
from sqlalchemy.ext.asyncio import AsyncSession

from ..db import get_db
from ..models import DocumentView, Search, User
from ..security import get_current_user

logger = logging.getLogger("yargi_asistan.history")

router = APIRouter(prefix="/history", tags=["history"])

Database = Annotated[AsyncSession, Depends(get_db)]
CurrentUser = Annotated[User, Depends(get_current_user)]


def _load_meta(value: str | None) -> dict[str, Any]:
    if not value:
        return {}
    try:
        parsed = json.loads(value)
    except (json.JSONDecodeError, TypeError):
        logger.warning("Belge geçmişi metadatası okunamadı")
        return {}
    return parsed if isinstance(parsed, dict) else {}


@router.get("/searches")
async def list_searches(
    user: CurrentUser,
    db: Database,
    source_db: Annotated[str | None, Query(alias="db", max_length=64)] = None,
    status_filter: Annotated[str | None, Query(alias="status", max_length=32)] = None,
    query: Annotated[str | None, Query(alias="q", max_length=500)] = None,
    limit: Annotated[int, Query(ge=1, le=100)] = 50,
    offset: Annotated[int, Query(ge=0)] = 0,
) -> list[dict[str, Any]]:
    statement = select(Search).where(Search.user_id == user.id)
    if source_db:
        statement = statement.where(Search.source_db == source_db)
    if status_filter:
        statement = statement.where(Search.status == status_filter)
    if query and query.strip():
        statement = statement.where(
            Search.query_text.contains(query.strip(), autoescape=True)
        )
    searches = (
        await db.scalars(
            statement.order_by(Search.created_at.desc(), Search.id.desc())
            .limit(limit)
            .offset(offset)
        )
    ).all()
    return [
        {
            "id": search.id,
            "query_text": search.query_text,
            "source_db": search.source_db,
            "result_count": search.result_count,
            "status": search.status,
            "created_at": search.created_at,
        }
        for search in searches
    ]


@router.get("/documents")
async def list_documents(
    user: CurrentUser,
    db: Database,
    limit: Annotated[int, Query(ge=1, le=100)] = 50,
) -> list[dict[str, Any]]:
    recency_rank = func.row_number().over(
        partition_by=(DocumentView.source_db, DocumentView.doc_key),
        order_by=(DocumentView.opened_at.desc(), DocumentView.id.desc()),
    )
    ranked_views = (
        select(
            DocumentView.id.label("id"),
            DocumentView.source_db.label("source_db"),
            DocumentView.doc_key.label("doc_key"),
            DocumentView.title.label("title"),
            DocumentView.meta_json.label("meta_json"),
            DocumentView.opened_at.label("opened_at"),
            recency_rank.label("recency_rank"),
        )
        .where(DocumentView.user_id == user.id)
        .subquery()
    )
    rows = (
        await db.execute(
            select(ranked_views)
            .where(ranked_views.c.recency_rank == 1)
            .order_by(ranked_views.c.opened_at.desc(), ranked_views.c.id.desc())
            .limit(limit)
        )
    ).mappings().all()
    return [
        {
            "id": row["id"],
            "source_db": row["source_db"],
            "doc_key": row["doc_key"],
            "title": row["title"],
            "meta": _load_meta(row["meta_json"]),
            "opened_at": row["opened_at"],
        }
        for row in rows
    ]
