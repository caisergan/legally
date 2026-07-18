from __future__ import annotations

import json
import logging
import time
from typing import Annotated, Any

from fastapi import APIRouter, Depends, HTTPException
from fastapi.responses import JSONResponse
from pydantic import ValidationError
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from ..adapters import (
    ADAPTERS,
    Adapter,
    SearchOutcome,
    SearchParams,
    available,
    outcome_from_exception,
    tool_to_db,
)
from ..db import get_db
from ..mcp_bridge import bridge
from ..models import Search, SearchResultSnapshot, User
from ..quotas import check_and_touch
from ..security import get_current_user

logger = logging.getLogger("yargi_asistan.search")

router = APIRouter(prefix="/search", tags=["search"])

Database = Annotated[AsyncSession, Depends(get_db)]
CurrentUser = Annotated[User, Depends(get_current_user)]


def _json_dump(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, separators=(",", ":"))


def _require_adapter(source_db: str) -> Adapter:
    adapter = ADAPTERS.get(source_db)
    if adapter is None:
        raise HTTPException(status_code=404, detail="Kaynak bulunamadı")
    if tool_to_db.get(adapter.search_tool) != source_db or not available(source_db):
        raise HTTPException(
            status_code=409,
            detail="bu kaynak sunucuda yapılandırılmamış",
        )
    return adapter


def _response(search_id: str | None, outcome: SearchOutcome) -> dict[str, Any]:
    return {
        "search_id": search_id,
        **outcome.model_dump(mode="json", exclude_none=True),
    }


async def _persist_search(
    db: AsyncSession,
    user: User,
    source_db: str,
    adapter: Adapter,
    params: SearchParams,
    outcome: SearchOutcome,
    duration_ms: int,
) -> str:
    search = Search(
        user_id=user.id,
        origin="manual",
        conversation_id=None,
        source_db=source_db,
        tool_name=adapter.search_tool,
        params_json=_json_dump(params.model_dump(mode="json")),
        query_text=params.phrase,
        result_count=len(outcome.results),
        status=outcome.status,
        error_text=outcome.error,
        duration_ms=duration_ms,
    )
    db.add(search)
    await db.flush()
    for rank, decision in enumerate(outcome.results[:50], start=1):
        db.add(
            SearchResultSnapshot(
                search_id=search.id,
                rank=rank,
                normalized_json=_json_dump(decision.model_dump(mode="json")),
            )
        )
    await db.commit()
    return search.id


async def _execute_search(
    source_db: str,
    params: SearchParams,
    user: User,
    db: AsyncSession,
) -> dict[str, Any] | JSONResponse:
    adapter = _require_adapter(source_db)
    try:
        arguments = adapter.build_search_args(params)
    except (TypeError, ValueError) as error:
        raise HTTPException(
            status_code=400,
            detail="Arama parametreleri geçersiz",
        ) from error
    try:
        await check_and_touch(db, user.id, need_tool_call=True)
    except HTTPException as error:
        if error.status_code == 429 and isinstance(error.detail, dict):
            return JSONResponse(
                status_code=429,
                content=error.detail,
                headers=error.headers,
            )
        raise

    started_at = time.perf_counter()
    try:
        payload = await bridge.call_tool(adapter.search_tool, arguments)
        outcome = adapter.normalize(payload, params.page)
    except Exception as error:
        if isinstance(error, TimeoutError):
            logger.warning("%s araması zaman aşımına uğradı", source_db)
        else:
            logger.exception("%s araması başarısız", source_db)
        outcome = outcome_from_exception(error)
    duration_ms = round((time.perf_counter() - started_at) * 1000)

    search_id = None
    if outcome.status in {"ok", "empty"}:
        search_id = await _persist_search(
            db,
            user,
            source_db,
            adapter,
            params,
            outcome,
            duration_ms,
        )
    return _response(search_id, outcome)


async def _owned_search(search_id: str, user: User, db: AsyncSession) -> Search:
    search = await db.scalar(
        select(Search).where(Search.id == search_id, Search.user_id == user.id)
    )
    if search is None:
        raise HTTPException(status_code=404, detail="Arama bulunamadı")
    return search


@router.post("/{source_db}")
async def run_search(
    source_db: str,
    params: SearchParams,
    user: CurrentUser,
    db: Database,
) -> Any:
    return await _execute_search(source_db, params, user, db)


@router.post("/{search_id}/rerun")
async def rerun_search(
    search_id: str,
    user: CurrentUser,
    db: Database,
) -> Any:
    search = await _owned_search(search_id, user, db)
    registered_source = tool_to_db.get(search.tool_name)
    if registered_source != search.source_db:
        raise HTTPException(
            status_code=409,
            detail="Arama kaynağı artık kullanılamıyor",
        )
    try:
        params = SearchParams.model_validate(json.loads(search.params_json))
    except (json.JSONDecodeError, TypeError, ValidationError) as error:
        logger.warning("%s aramasının parametreleri okunamadı: %s", search.id, error)
        raise HTTPException(
            status_code=409,
            detail="Arama parametreleri yeniden çalıştırılamıyor",
        ) from error
    await db.commit()
    return await _execute_search(search.source_db, params, user, db)


@router.get("/{search_id}/snapshot")
async def get_snapshot(
    search_id: str,
    user: CurrentUser,
    db: Database,
) -> dict[str, Any]:
    search = await _owned_search(search_id, user, db)
    rows = (
        await db.scalars(
            select(SearchResultSnapshot)
            .where(SearchResultSnapshot.search_id == search.id)
            .order_by(SearchResultSnapshot.rank)
        )
    ).all()
    try:
        params = json.loads(search.params_json)
        results = [json.loads(row.normalized_json) for row in rows]
    except (json.JSONDecodeError, TypeError) as error:
        logger.error("%s anlık görüntüsü okunamadı: %s", search.id, error)
        raise HTTPException(
            status_code=500,
            detail="Arama anlık görüntüsü okunamadı",
        ) from error
    return {
        "search_id": search.id,
        "origin": search.origin,
        "conversation_id": search.conversation_id,
        "source_db": search.source_db,
        "tool_name": search.tool_name,
        "params": params,
        "query_text": search.query_text,
        "result_count": search.result_count,
        "status": search.status,
        "error": search.error_text,
        "duration_ms": search.duration_ms,
        "created_at": search.created_at,
        "results": results,
    }
