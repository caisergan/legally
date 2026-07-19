"""Append-only safe audit writer (plan §9.3).

Only safe metadata is persisted. A forbidden-key guard drops any secret-bearing
field (PIN, PIN JWE, password, capability, raw token, vendor error) before it can
reach the audit row.
"""

from __future__ import annotations

import json
from typing import Any

from sqlalchemy.ext.asyncio import AsyncSession

from .models import SigningAuditEvent

_FORBIDDEN_SUBSTRINGS = (
    "pin",
    "jwe",
    "password",
    "passwd",
    "secret",
    "capability",
    "token",
    "cookie",
    "session",
    "private",
    "pkcs11",
    "vendor_error",
    "ckaid",
    "cka_id",
)


def sanitize_detail(detail: dict[str, Any] | None) -> dict[str, Any]:
    if not detail:
        return {}
    return {
        key: value
        for key, value in detail.items()
        if not any(bad in key.lower() for bad in _FORBIDDEN_SUBSTRINGS)
    }


async def record(
    db: AsyncSession,
    action: str,
    *,
    outcome: str = "ok",
    actor_type: str = "system",
    actor_id: int | None = None,
    request_id: str | None = None,
    signer_job_id: str | None = None,
    command_id: str | None = None,
    certificate_id: str | None = None,
    input_sha256: str | None = None,
    output_sha256: str | None = None,
    detail: dict[str, Any] | None = None,
) -> SigningAuditEvent:
    safe_detail = sanitize_detail(detail)
    event = SigningAuditEvent(
        action=action,
        outcome=outcome,
        actor_type=actor_type,
        actor_id=actor_id,
        request_id=request_id,
        signer_job_id=signer_job_id,
        command_id=command_id,
        certificate_id=certificate_id,
        input_sha256=input_sha256,
        output_sha256=output_sha256,
        detail_json=json.dumps(safe_detail, ensure_ascii=False) if safe_detail else None,
    )
    db.add(event)
    await db.flush()
    return event
