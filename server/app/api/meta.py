from __future__ import annotations

import asyncio
import copy
import logging
import os
import time
from typing import Annotated, Any

from fastapi import APIRouter, Depends
from sqlalchemy.ext.asyncio import AsyncSession

from ..adapters import FORM_METADATA, SOURCE_META, available
from ..config import settings
from ..db import get_db
from ..mcp_bridge import bridge
from ..models import User
from ..quotas import get_usage
from ..security import get_current_user

logger = logging.getLogger("yargi_asistan.meta")

router = APIRouter(prefix="/meta", tags=["meta"])

Database = Annotated[AsyncSession, Depends(get_db)]
CurrentUser = Annotated[User, Depends(get_current_user)]

_health_cache: dict[str, Any] | None = None
_health_cache_expires_at = 0.0
_health_cache_lock = asyncio.Lock()


def _model_name(model_id: str) -> str:
    name = model_id.replace("-", " ").title()
    if name.startswith("Gpt"):
        return f"GPT{name[3:]}"
    if name.startswith("Claude"):
        return f"Claude{name[6:]}"
    return name


@router.get("/models")
def list_models(_user: CurrentUser) -> list[dict[str, str]]:
    slots = (
        ("sonnet5", settings.model_sonnet5, "Dengeli · varsayılan"),
        ("opus", settings.model_opus, "En yetenekli · daha yavaş"),
        ("haiku", settings.model_haiku, "En hızlı · kısa görevler"),
    )
    return [
        {
            "key": key,
            "model": model,
            "name": _model_name(model),
            "description": description,
        }
        for key, model, description in slots
    ]


@router.get("/sources")
def list_sources() -> list[dict[str, Any]]:
    tavily_is_configured = bool(os.environ.get("TAVILY_API_KEY", "").strip())
    sources: list[dict[str, Any]] = []
    for source_meta in SOURCE_META:
        source = copy.deepcopy(source_meta)
        source_id = source["id"]
        source["available"] = available(source_id)
        if source.get("gated") and not tavily_is_configured:
            source["gated_reason"] = (
                "Sunucuda yapılandırılmamış (TAVILY_API_KEY yok)"
            )
        source.update(copy.deepcopy(FORM_METADATA.get(source_id, {})))
        sources.append(source)
    return sources


async def _read_health() -> dict[str, Any]:
    try:
        payload = await bridge.call_tool("check_government_servers_health", {})
    except TimeoutError:
        logger.warning("Hükümet sunucuları sağlık kontrolü zaman aşımına uğradı")
        return {"status": "down", "detail": {"error": "kaynak zaman aşımına uğradı"}}
    except Exception:
        logger.exception("Hükümet sunucuları sağlık kontrolü başarısız")
        return {"status": "down", "detail": {"error": "sağlık kontrolü başarısız"}}

    if not isinstance(payload, dict):
        return {"status": "down", "detail": {"error": "geçersiz sağlık yanıtı"}}
    status_map = {"healthy": "ok", "degraded": "degraded", "unhealthy": "down"}
    status = status_map.get(str(payload.get("overall_status", "")).lower(), "down")
    return {"status": status, "detail": payload}


@router.get("/health")
async def get_health() -> dict[str, Any]:
    global _health_cache, _health_cache_expires_at

    now = time.monotonic()
    if _health_cache is not None and now < _health_cache_expires_at:
        return copy.deepcopy(_health_cache)

    async with _health_cache_lock:
        now = time.monotonic()
        if _health_cache is not None and now < _health_cache_expires_at:
            return copy.deepcopy(_health_cache)
        _health_cache = await _read_health()
        _health_cache_expires_at = time.monotonic() + 60
        return copy.deepcopy(_health_cache)


@router.get("/usage")
async def usage(user: CurrentUser, db: Database) -> dict[str, int | str]:
    return await get_usage(db, user.id)
