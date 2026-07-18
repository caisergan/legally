"""Bridge to the yargi-mcp server.

Two modes (settings.mcp_mode):
- "embedded": import yargi-mcp's FastMCP app and drive it in-memory (no network hop).
- "http": connect to a running server's Streamable HTTP endpoint.

Every call goes through `call_tool` which applies a per-tool timeout and folds
the transport-level result into a plain dict.
"""

import asyncio
import json
import logging
from typing import Any

from fastmcp import Client

from .config import settings

logger = logging.getLogger("yargi_asistan.mcp")

# Tools hidden from both chat and the UI (ChatGPT deep-research duplicates).
HIDDEN_TOOLS = {"search", "fetch"}


class McpBridge:
    def __init__(self) -> None:
        self._client: Client | None = None
        self._tools: list[dict[str, Any]] = []
        self._lock = asyncio.Lock()

    async def start(self) -> None:
        if settings.mcp_mode == "http":
            target: Any = settings.yargi_mcp_url
        else:
            from mcp_server_main import app as yargi_app  # embedded import

            target = yargi_app
        self._client = Client(target)
        await self._client.__aenter__()
        tools = await self._client.list_tools()
        self._tools = [
            {
                "name": t.name,
                "description": t.description or "",
                "input_schema": t.inputSchema,
            }
            for t in tools
            if t.name not in HIDDEN_TOOLS
        ]
        logger.info("MCP bridge ready: %d tools", len(self._tools))

    async def stop(self) -> None:
        if self._client is not None:
            await self._client.__aexit__(None, None, None)
            self._client = None

    @property
    def tools(self) -> list[dict[str, Any]]:
        return self._tools

    def tool_names(self) -> set[str]:
        return {t["name"] for t in self._tools}

    async def call_tool(self, name: str, arguments: dict[str, Any]) -> Any:
        """Call a tool and return its payload as Python data.

        Raises TimeoutError on timeout; other tool errors propagate as exceptions.
        In-band error payloads (e.g. Bedesten rate limits) are returned as-is —
        callers classify them via `classify_result`.
        """
        if self._client is None:
            raise RuntimeError("MCP bridge not started")
        timeout = settings.tool_timeout_seconds
        if name == "search_bedesten_semantic":
            timeout = 120.0
        async with self._lock:
            result = await asyncio.wait_for(
                self._client.call_tool(name, arguments), timeout=timeout
            )
        return _extract_payload(result)


def _extract_payload(result: Any) -> Any:
    """Fold a fastmcp CallToolResult (or content list) into Python data."""
    # fastmcp >= 2.10 returns CallToolResult with .data / .content
    data = getattr(result, "data", None)
    if data is not None:
        return _maybe_json(data)
    content = getattr(result, "content", result)
    if isinstance(content, list) and content:
        first = content[0]
        text = getattr(first, "text", None)
        if text is not None:
            return _maybe_json(text)
    return content


def _maybe_json(value: Any) -> Any:
    if isinstance(value, str):
        stripped = value.strip()
        if stripped.startswith(("{", "[")):
            try:
                return json.loads(stripped)
            except json.JSONDecodeError:
                return value
    return value


def classify_result(payload: Any) -> tuple[str, str | None, int | None]:
    """Fold the repo's four error conventions into one status.

    Returns (status, error_message, retry_after) where status is one of
    ok | rate_limited | source_error.
    """
    if isinstance(payload, dict):
        if payload.get("error") == "rate_limit_exceeded" or payload.get("status_code") == 429:
            retry = payload.get("retry_after")
            return "rate_limited", payload.get("message"), int(retry) if retry else None
        for key in ("error", "error_message", "error_code"):
            val = payload.get(key)
            if val:
                return "source_error", str(val), None
        md = payload.get("markdown_content") or payload.get("markdown_chunk") or ""
        if isinstance(md, str) and md.startswith("ERROR ("):
            if "rate_limit" in md:
                return "rate_limited", md[:200], None
            return "source_error", md[:200], None
    return "ok", None, None


bridge = McpBridge()
