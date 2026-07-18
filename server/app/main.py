from __future__ import annotations

import json
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from typing import Any

from fastapi import FastAPI
from starlette.exceptions import HTTPException as StarletteHTTPException
from starlette.responses import JSONResponse, Response
from starlette.staticfiles import StaticFiles
from starlette.types import Scope

from .adapters import validate_registry
from .api import api_router
from .config import settings
from .db import init_db
from .mcp_bridge import bridge


class Utf8JsonResponse(JSONResponse):
    def render(self, content: Any) -> bytes:
        return json.dumps(
            content,
            ensure_ascii=False,
            allow_nan=False,
            indent=None,
            separators=(",", ":"),
        ).encode("utf-8")


class SpaStaticFiles(StaticFiles):
    async def get_response(self, path: str, scope: Scope) -> Response:
        try:
            return await super().get_response(path, scope)
        except StarletteHTTPException as error:
            request_path = scope.get("path", "")
            is_api_path = request_path == "/api" or request_path.startswith("/api/")
            if error.status_code != 404 or scope["method"] != "GET" or is_api_path:
                raise
        return await super().get_response("index.html", scope)


@asynccontextmanager
async def lifespan(_: FastAPI) -> AsyncIterator[None]:
    await init_db()
    await bridge.start()
    try:
        validate_registry(bridge)
        yield
    finally:
        await bridge.stop()


def create_app() -> FastAPI:
    application = FastAPI(
        title="Yargı Asistan",
        lifespan=lifespan,
        default_response_class=Utf8JsonResponse,
    )
    application.include_router(api_router, prefix="/api")
    if settings.frontend_dist.is_dir():
        application.mount(
            "/",
            SpaStaticFiles(directory=settings.frontend_dist, html=True),
            name="frontend",
        )
    return application


app = create_app()
