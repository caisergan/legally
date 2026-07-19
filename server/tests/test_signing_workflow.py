"""Outbox, reconciliation, event projection, and certificate sync."""

from __future__ import annotations

from datetime import timedelta

import pytest

from app import signing_state as st
from app import signing_workflow as wf
from app.models import SigningOutbox, utcnow
from app.signing_client import SignerError, SignerUnavailable

from .factories import (
    make_approval,
    make_certificate,
    make_input_artifact,
    make_output_artifact,
    make_request,
    make_user,
)


class FakeSigner:
    def __init__(self):
        self.credentials = {"items": []}
        self.events: dict[str, list[dict]] = {}
        self.fail_create: Exception | None = None
        self.command_result: dict | None = None

    async def create_job(self, command_id, payload):
        if self.fail_create:
            raise self.fail_create
        return {"id": payload["job_id"], "state": "QUEUED", "version": 1, "event_sequence": 1}

    async def get_command(self, command_id):
        return self.command_result or {"state": "QUEUED", "event_sequence": 1}

    async def cancel_job(self, job_id):
        return {"state": "CANCELLED"}

    async def get_job_events(self, job_id, after):
        return {"events": [e for e in self.events.get(job_id, []) if e["sequence"] > after]}

    async def get_credentials(self):
        return self.credentials


def test_deterministic_ids_are_stable():
    cmd = wf.deterministic_command_id("req-1", 3)
    assert cmd == wf.deterministic_command_id("req-1", 3)
    assert wf.deterministic_job_id(cmd) == wf.deterministic_job_id(cmd)
    assert wf.deterministic_command_id("req-1", 4) != cmd


def test_pin_envelope_never_enters_outbox():
    with pytest.raises(wf.PinEnvelopeInOutbox):
        wf._guard_command_type(wf.PIN_ENVELOPE_COMMAND)


async def _queued_request(db):
    user = await make_user(db)
    cert = await make_certificate(db)
    artifact = await make_input_artifact(db, user.id, "aa" * 32, 100, "0" * 32)
    request = await make_request(db, user, cert, artifact)
    approval = await make_approval(db, request)
    output = await make_output_artifact(db, user.id)
    return request, cert, approval, output


async def test_enqueue_create_job_queues_and_is_idempotent(db):
    request, cert, approval, output = await _queued_request(db)
    outbox = await wf.enqueue_create_job(
        db,
        request,
        credential_id=cert.service_credential_id,
        approval=approval,
        output_artifact_id=output.id,
        max_output_bytes=1024,
        expires_at=utcnow() + timedelta(minutes=5),
    )
    assert request.state == st.QUEUED
    assert request.signer_job_id and request.signer_command_id
    assert "capability" not in outbox.payload_json.lower()
    assert outbox.command_type == "create_job"

    again = await wf.enqueue_create_job(
        db,
        request,
        credential_id=cert.service_credential_id,
        approval=approval,
        output_artifact_id=output.id,
        max_output_bytes=1024,
        expires_at=utcnow() + timedelta(minutes=5),
    )
    assert again.command_id == outbox.command_id


async def test_dispatch_success_projects_state(db):
    request, cert, approval, output = await _queued_request(db)
    outbox = await wf.enqueue_create_job(
        db, request, credential_id=cert.service_credential_id, approval=approval,
        output_artifact_id=output.id, max_output_bytes=1024,
        expires_at=utcnow() + timedelta(minutes=5),
    )
    await wf.dispatch_outbox(db, FakeSigner(), outbox)
    assert outbox.status == "acknowledged"
    assert outbox.acknowledged_at is not None


async def test_dispatch_signer_unavailable_schedules_retry(db):
    request, cert, approval, output = await _queued_request(db)
    outbox = await wf.enqueue_create_job(
        db, request, credential_id=cert.service_credential_id, approval=approval,
        output_artifact_id=output.id, max_output_bytes=1024,
        expires_at=utcnow() + timedelta(minutes=5),
    )
    signer = FakeSigner()
    signer.fail_create = SignerUnavailable("socket down")
    await wf.dispatch_outbox(db, signer, outbox)
    assert outbox.status == "reconcile"
    assert outbox.attempts == 1
    assert outbox.next_attempt_at is not None
    assert request.state == st.QUEUED  # unchanged; no invented transition


async def test_dispatch_conflict_reconciles_via_command(db):
    request, cert, approval, output = await _queued_request(db)
    outbox = await wf.enqueue_create_job(
        db, request, credential_id=cert.service_credential_id, approval=approval,
        output_artifact_id=output.id, max_output_bytes=1024,
        expires_at=utcnow() + timedelta(minutes=5),
    )
    signer = FakeSigner()
    signer.fail_create = SignerError(409, "conflict", "duplicate")
    signer.command_result = {"state": "PIN_REQUIRED", "event_sequence": 3}
    await wf.dispatch_outbox(db, signer, outbox)
    assert outbox.status == "acknowledged"
    assert request.state == st.PIN_REQUIRED


async def test_project_events_is_monotonic_and_ignores_stale_cursor(db):
    request, cert, approval, output = await _queued_request(db)
    request.signer_job_id = "job-xyz"
    await db.flush()
    signer = FakeSigner()
    signer.events["job-xyz"] = [
        {"sequence": 1, "type": "queued", "state": "QUEUED"},
        {"sequence": 2, "type": "pin_required", "state": "PIN_REQUIRED"},
    ]
    applied = await wf.project_events(db, signer, request)
    assert applied == 2
    assert request.state == st.PIN_REQUIRED
    assert request.last_event_sequence == 2
    assert request.signer_event_cursor == 2
    # Re-projection with the same (stale) events applies nothing.
    assert await wf.project_events(db, signer, request) == 0


async def test_project_events_marks_completed_retained(db):
    request, cert, approval, output = await _queued_request(db)
    request.signer_job_id = "job-done"
    await db.flush()
    signer = FakeSigner()
    signer.events["job-done"] = [{"sequence": 1, "type": "completed", "state": "COMPLETED"}]
    await wf.project_events(db, signer, request)
    assert request.state == st.COMPLETED
    assert request.retained is True
    assert request.completed_at is not None


async def test_sync_certificates_upserts_and_marks_unavailable(db):
    signer = FakeSigner()
    signer.credentials = {
        "items": [
            {
                "id": "svc-A",
                "certificate_fingerprint_sha256": "ab" * 32,
                "subject_display": "E*** A***",
                "issuer_display": "Test CA",
                "serial_suffix": "A1B2",
                "not_before": "2026-01-01T00:00:00Z",
                "not_after": "2027-01-01T00:00:00Z",
                "public_key_type": "RSA",
                "public_key_bits": 2048,
                "mode": "softhsm",
                "status": "available",
            }
        ]
    }
    first = await wf.sync_certificates(db, signer)
    assert first["created"] == 1
    # Second sync with an empty inventory marks the certificate unavailable.
    signer.credentials = {"items": []}
    second = await wf.sync_certificates(db, signer)
    assert second["unavailable"] == 1
