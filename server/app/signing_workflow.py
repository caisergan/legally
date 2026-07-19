"""Signer orchestration: outbox, reconciliation, event projection, cert sync.

Every signer command except the synchronous PIN envelope flows through the
transactional outbox. Job and command IDs are deterministic so an ambiguous
create can be reconciled without a secret lookup token. FastAPI never invents a
signer transition; it projects durable signer events onto the local request.
"""

from __future__ import annotations

import hashlib
import json
from datetime import datetime, timedelta, timezone

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from . import signing_state as st
from .models import (
    SigningApproval,
    SigningCertificate,
    SigningOutbox,
    SigningRequest,
    SigningRequestEvent,
    new_id,
    utcnow,
)
from .signing_client import SignerClient, SignerError, SignerUnavailable

PIN_ENVELOPE_COMMAND = "pin_envelope"
_MAX_BACKOFF_SECONDS = 300

# Signer-owned states FastAPI may project from durable events.
_PROJECTABLE = frozenset(
    {
        st.QUEUED,
        st.WAITING_FOR_TOKEN,
        st.PIN_REQUIRED,
        st.AUTHORIZATION_CONSUMED,
        st.SIGNING,
        st.VERIFYING,
        st.COMPLETED,
        st.APPROVAL_EXPIRED,
        st.PIN_WINDOW_EXPIRED,
        st.PIN_REJECTED,
        st.TOKEN_LOCKED,
        st.TOKEN_UNAVAILABLE,
        st.FAILED_PRE_SIGN,
        st.FAILED_POST_SIGN,
        st.OUTCOME_UNKNOWN,
        st.QUARANTINED,
        st.CANCELLED,
    }
)


class OutboxError(Exception):
    pass


class PinEnvelopeInOutbox(OutboxError):
    """PIN envelopes must be relayed synchronously, never persisted to the outbox."""


def deterministic_command_id(request_id: str, version: int) -> str:
    return hashlib.sha256(f"cmd:{request_id}:{version}".encode()).hexdigest()


def deterministic_job_id(command_id: str) -> str:
    return hashlib.sha256(f"job:{command_id}".encode()).hexdigest()


def _guard_command_type(command_type: str) -> None:
    if command_type == PIN_ENVELOPE_COMMAND:
        raise PinEnvelopeInOutbox("PIN envelopes are never queued")


def build_create_job_payload(
    request: SigningRequest,
    *,
    credential_id: str,
    approval: SigningApproval,
    output_artifact_id: str,
    max_output_bytes: int,
    nonce: str,
    expires_at: datetime,
) -> dict:
    return {
        "command_id": request.signer_command_id,
        "job_id": request.signer_job_id,
        "request_id": request.id,
        "expected_request_version": request.version,
        "credential_id": credential_id,
        "certificate_fingerprint_sha256": request.certificate_fingerprint_sha256,
        "policy_version": request.policy_version,
        "profile": request.profile,
        "algorithm": request.algorithm,
        "input": {
            "artifact_id": request.artifact_id,
            "byte_count": request.input_byte_count,
            "sha256": request.input_sha256,
        },
        "output": {
            "artifact_id": output_artifact_id,
            "max_byte_count": max_output_bytes,
        },
        "approval": {
            "approval_id": approval.id,
            "approved_at": _iso(approval.approved_at),
            "expires_at": _iso(approval.expires_at),
        },
        "nonce": nonce,
        "expires_at": _iso(expires_at),
    }


async def enqueue_create_job(
    db: AsyncSession,
    request: SigningRequest,
    *,
    credential_id: str,
    approval: SigningApproval,
    output_artifact_id: str,
    max_output_bytes: int,
    expires_at: datetime,
) -> SigningOutbox:
    """Idempotently enqueue the create-job command and mark the request QUEUED."""
    if request.signer_command_id:
        existing = await db.scalar(
            select(SigningOutbox).where(
                SigningOutbox.command_id == request.signer_command_id
            )
        )
        if existing is not None:
            return existing

    command_id = deterministic_command_id(request.id, request.version)
    job_id = deterministic_job_id(command_id)
    request.signer_command_id = command_id
    request.signer_job_id = job_id
    payload = build_create_job_payload(
        request,
        credential_id=credential_id,
        approval=approval,
        output_artifact_id=output_artifact_id,
        max_output_bytes=max_output_bytes,
        nonce=new_id(),
        expires_at=expires_at,
    )
    outbox = SigningOutbox(
        request_id=request.id,
        command_id=command_id,
        command_type="create_job",
        payload_json=json.dumps(payload, ensure_ascii=False),
        status="pending",
    )
    _guard_command_type(outbox.command_type)
    db.add(outbox)
    request.state = st.QUEUED
    request.queued_at = utcnow()
    request.version += 1
    await db.flush()
    return outbox


async def enqueue_cancel(db: AsyncSession, request: SigningRequest) -> SigningOutbox:
    command_id = deterministic_command_id(request.id, request.version) + ":cancel"
    outbox = SigningOutbox(
        request_id=request.id,
        command_id=command_id,
        command_type="cancel",
        payload_json=json.dumps({"job_id": request.signer_job_id}),
        status="pending",
    )
    _guard_command_type(outbox.command_type)
    db.add(outbox)
    await db.flush()
    return outbox


async def dispatch_outbox(
    db: AsyncSession, client: SignerClient, outbox: SigningOutbox
) -> None:
    _guard_command_type(outbox.command_type)
    payload = json.loads(outbox.payload_json)
    try:
        if outbox.command_type == "create_job":
            result = await client.create_job(outbox.command_id, payload)
        elif outbox.command_type == "cancel":
            result = await client.cancel_job(payload.get("job_id", ""))
        else:
            raise OutboxError(f"unknown command type {outbox.command_type!r}")
    except SignerUnavailable as error:
        _schedule_retry(outbox, code="signer_unavailable", detail=str(error))
        await db.flush()
        return
    except SignerError as error:
        if error.status_code == 409 and outbox.command_type == "create_job":
            result = await _reconcile_create(client, outbox)
        else:
            _schedule_retry(outbox, code=error.code, detail=error.detail)
            await db.flush()
            return
    outbox.status = "acknowledged"
    outbox.acknowledged_at = utcnow()
    outbox.dispatched_at = outbox.dispatched_at or utcnow()
    await _project_result(db, outbox, result)
    await db.flush()


async def _reconcile_create(client: SignerClient, outbox: SigningOutbox) -> dict:
    return await client.get_command(outbox.command_id)


def _schedule_retry(outbox: SigningOutbox, *, code: str, detail: str) -> None:
    outbox.attempts += 1
    outbox.status = "reconcile"
    outbox.last_error_code = code
    backoff = min(2 ** outbox.attempts, _MAX_BACKOFF_SECONDS)
    outbox.next_attempt_at = utcnow() + timedelta(seconds=backoff)


async def _project_result(db: AsyncSession, outbox: SigningOutbox, result: dict) -> None:
    request = await db.get(SigningRequest, outbox.request_id)
    if request is None:
        return
    state = str(result.get("state", ""))
    if state in _PROJECTABLE:
        request.state = state
    sequence = result.get("event_sequence")
    if isinstance(sequence, int):
        request.signer_event_cursor = max(request.signer_event_cursor, sequence)


async def project_events(
    db: AsyncSession, client: SignerClient, request: SigningRequest
) -> int:
    """Project durable signer events onto the request. Returns events applied."""
    if not request.signer_job_id:
        return 0
    response = await client.get_job_events(
        request.signer_job_id, after=request.signer_event_cursor
    )
    applied = 0
    for event in response.get("events", []):
        signer_seq = int(event.get("sequence", 0))
        if signer_seq <= request.signer_event_cursor:
            continue  # stale / already-seen cursor
        request.last_event_sequence += 1
        db.add(
            SigningRequestEvent(
                request_id=request.id,
                sequence=request.last_event_sequence,
                type=str(event.get("type", "signer_event")),
                state=_safe_state(event.get("state")),
                detail_json=_safe_detail(event),
                signer_event_sequence=signer_seq,
            )
        )
        projected = _safe_state(event.get("state"))
        if projected is not None:
            request.state = projected
            if projected == st.COMPLETED:
                request.completed_at = utcnow()
                request.retained = True
        request.signer_event_cursor = signer_seq
        applied += 1
    await db.flush()
    return applied


async def sync_certificates(db: AsyncSession, client: SignerClient) -> dict[str, int]:
    """Upsert safe certificate metadata from `GET /v1/credentials` (§5.4)."""
    response = await client.get_credentials()
    seen: set[str] = set()
    created = updated = 0
    for item in response.get("items", []):
        service_id = str(item["id"])
        seen.add(service_id)
        row = await db.scalar(
            select(SigningCertificate).where(
                SigningCertificate.service_credential_id == service_id
            )
        )
        fingerprint = str(item["certificate_fingerprint_sha256"])
        fields = dict(
            certificate_fingerprint_sha256=fingerprint,
            subject_display=str(item.get("subject_display", "")),
            issuer_display=str(item.get("issuer_display", "")),
            serial_suffix=str(item.get("serial_suffix", "")),
            fingerprint_suffix=fingerprint[-8:].upper(),
            not_before=_parse_iso(item["not_before"]),
            not_after=_parse_iso(item["not_after"]),
            public_key_type=str(item.get("public_key_type", "")),
            public_key_bits=item.get("public_key_bits"),
            mode=str(item.get("mode", "softhsm")),
            status=str(item.get("status", "available")),
            last_checked_at=utcnow(),
        )
        if row is None:
            db.add(SigningCertificate(service_credential_id=service_id, **fields))
            created += 1
        else:
            for key, value in fields.items():
                setattr(row, key, value)
            updated += 1
    # Certificates no longer advertised become unavailable (never deleted).
    stale = await db.scalars(select(SigningCertificate))
    unavailable = 0
    for cert in stale:
        if cert.service_credential_id not in seen and cert.status != "unavailable":
            cert.status = "unavailable"
            cert.last_checked_at = utcnow()
            unavailable += 1
    await db.flush()
    return {"created": created, "updated": updated, "unavailable": unavailable}


def _safe_state(value: object) -> str | None:
    text = str(value) if value is not None else ""
    return text if text in _PROJECTABLE else None


def _safe_detail(event: dict) -> str | None:
    safe = {
        key: event[key]
        for key in ("type", "state", "code", "sequence")
        if key in event
    }
    return json.dumps(safe, ensure_ascii=False) if safe else None


def _iso(value: datetime) -> str:
    normalized = value if value.tzinfo else value.replace(tzinfo=timezone.utc)
    return normalized.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def _parse_iso(value: str) -> datetime:
    parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    return parsed.astimezone(timezone.utc).replace(tzinfo=None)
