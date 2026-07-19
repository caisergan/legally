"""Browser-facing signing route tests (Phase 3B).

Cover disabled behavior, CSRF/Origin, ownership 404s, upload validation, request
idempotency, fresh-auth confirm/download, immutable manifest, illegal-state
operations, the synchronous PIN relay (no PIN persisted, delivery-unknown block),
and one-use hash-bound download with private no-store headers.
"""

from __future__ import annotations

from datetime import timedelta

import pytest
import pytest_asyncio
from httpx import ASGITransport, AsyncClient
from sqlalchemy import text

from app import signing_state as st
from app.api import signing as signing_api
from app.config import settings
from app.db import get_db
from app.models import SigningDownloadAuthorization, utcnow
from app.security import get_current_user, hash_password

from .conftest import async_iter
from .factories import (
    make_approval,
    make_certificate,
    make_input_artifact,
    make_output_artifact,
    make_request,
    make_user,
)

PDF_BYTES = b"%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF"
PIN_JWE = "eyJ.PIN-ENVELOPE-SECRET-VALUE.sig"


class FakeSigner:
    def __init__(self):
        self.events: dict[str, list[dict]] = {}
        self.authorize_result = {"authorization_status": "consumed"}
        self.authorize_error: Exception | None = None
        self.challenge = {
            "challenge_id": "chal-1",
            "challenge_jws": "jws",
            "recipient_jwk": {},
            "expires_at": "2026-07-19T12:01:30Z",
        }

    async def create_job(self, command_id, payload):
        return {"id": payload["job_id"], "state": "QUEUED", "version": 1, "event_sequence": 1}

    async def get_command(self, command_id):
        return {"state": "QUEUED", "event_sequence": 1}

    async def cancel_job(self, job_id):
        return {"state": "CANCELLED"}

    async def get_job_events(self, job_id, after):
        return {"events": [e for e in self.events.get(job_id, []) if e["sequence"] > after]}

    async def get_pin_challenge(self, job_id):
        return self.challenge

    async def authorize(self, job_id, challenge_id, pin_jwe):
        if self.authorize_error:
            raise self.authorize_error
        return self.authorize_result


@pytest_asyncio.fixture
async def api(sessionmaker, store):
    settings.signing_enabled = True
    settings.signing_kill_switch = False
    settings.signing_capability_secret = "test-capability-secret"
    signing_api._upload_hits.clear()

    from app.main import create_app

    app = create_app()
    state: dict = {"user": None, "signer": FakeSigner()}

    async def _db():
        async with sessionmaker() as session:
            yield session

    def _user():
        return state["user"]

    async def _signer():
        yield state["signer"]

    app.dependency_overrides[get_db] = _db
    app.dependency_overrides[get_current_user] = _user
    app.dependency_overrides[signing_api.get_signer_client] = _signer
    app.dependency_overrides[signing_api.get_artifact_store] = lambda: store

    transport = ASGITransport(app=app)
    async with AsyncClient(
        transport=transport,
        base_url="http://testserver",
        headers={
            "Origin": "http://testserver",
            "X-CSRF-Token": "csrf-token",
        },
        cookies={"ya_csrf": "csrf-token"},
    ) as client:
        yield client, state, sessionmaker
    app.dependency_overrides.clear()
    settings.signing_enabled = False


async def _seed_user(sessionmaker, email="u@x.c", password="correct horse"):
    async with sessionmaker() as s:
        user = await make_user(s, email)
        user.password_hash = hash_password(password)
        await s.commit()
        await s.refresh(user)
        return user


# --- disabled + CSRF/origin --------------------------------------------------


async def test_disabled_feature_behavior(api):
    client, state, sm = api
    state["user"] = await _seed_user(sm)
    settings.signing_enabled = False
    try:
        caps = await client.get("/api/signing/capabilities")
        assert caps.status_code == 200 and caps.json()["enabled"] is False
        certs = await client.get("/api/signing/certificates")
        assert certs.status_code == 404
    finally:
        settings.signing_enabled = True


async def test_csrf_and_origin_required(api):
    client, state, sm = api
    state["user"] = await _seed_user(sm)
    # Missing CSRF header.
    missing = await client.post(
        "/api/signing/artifacts",
        files={"file": ("x.pdf", PDF_BYTES, "application/pdf")},
        headers={"X-CSRF-Token": ""},
    )
    assert missing.status_code == 403
    # Wrong Origin.
    wrong = await client.post(
        "/api/signing/artifacts",
        files={"file": ("x.pdf", PDF_BYTES, "application/pdf")},
        headers={"Origin": "http://evil.example"},
    )
    assert wrong.status_code == 403


# --- artifacts ---------------------------------------------------------------


async def test_upload_get_and_cross_user_404(api):
    client, state, sm = api
    owner = await _seed_user(sm, "owner@x.c")
    other = await _seed_user(sm, "other@x.c")
    state["user"] = owner
    up = await client.post(
        "/api/signing/artifacts",
        files={"file": ("Dilekçe.pdf", PDF_BYTES, "application/pdf")},
    )
    assert up.status_code == 201, up.text
    artifact_id = up.json()["id"]
    assert up.json()["sha256"] and up.json()["byte_count"] == len(PDF_BYTES)

    mine = await client.get(f"/api/signing/artifacts/{artifact_id}")
    assert mine.status_code == 200
    state["user"] = other
    theirs = await client.get(f"/api/signing/artifacts/{artifact_id}")
    assert theirs.status_code == 404


async def test_upload_rejects_non_pdf(api):
    client, state, sm = api
    state["user"] = await _seed_user(sm)
    resp = await client.post(
        "/api/signing/artifacts",
        files={"file": ("x.txt", b"not a pdf", "application/pdf")},
    )
    assert resp.status_code == 422


# --- requests ----------------------------------------------------------------


async def _make_uploaded_request(client, state, sm, owner):
    state["user"] = owner
    up = await client.post(
        "/api/signing/artifacts", files={"file": ("x.pdf", PDF_BYTES, "application/pdf")}
    )
    artifact_id = up.json()["id"]
    async with sm() as s:
        cert = await make_certificate(s)
        from app.models import SigningCertificateAssignment

        s.add(SigningCertificateAssignment(certificate_id=cert.id, user_id=owner.id, active=True))
        await s.commit()
        cert_id = cert.id
    return artifact_id, cert_id


async def test_request_idempotency(api):
    client, state, sm = api
    owner = await _seed_user(sm)
    artifact_id, cert_id = await _make_uploaded_request(client, state, sm, owner)
    body = {"artifact_id": artifact_id, "certificate_id": cert_id}
    headers = {"Idempotency-Key": "key-1"}

    missing = await client.post("/api/signing/requests", json=body)
    assert missing.status_code == 400

    first = await client.post("/api/signing/requests", json=body, headers=headers)
    assert first.status_code == 201
    request_id = first.json()["id"]
    again = await client.post("/api/signing/requests", json=body, headers=headers)
    assert again.status_code == 200 and again.json()["id"] == request_id
    # Same key, different artifact → 409.
    conflict = await client.post(
        "/api/signing/requests",
        json={"artifact_id": "different", "certificate_id": cert_id},
        headers=headers,
    )
    assert conflict.status_code == 409


async def test_request_rejects_policy_fields(api):
    client, state, sm = api
    owner = await _seed_user(sm)
    artifact_id, cert_id = await _make_uploaded_request(client, state, sm, owner)
    resp = await client.post(
        "/api/signing/requests",
        json={"artifact_id": artifact_id, "certificate_id": cert_id, "profile": "X"},
        headers={"Idempotency-Key": "k"},
    )
    assert resp.status_code == 422  # extra=forbid


# --- confirm + immutable manifest + illegal state ----------------------------


async def test_confirm_fresh_password_and_queue(api):
    client, state, sm = api
    owner = await _seed_user(sm, password="correct horse")
    artifact_id, cert_id = await _make_uploaded_request(client, state, sm, owner)
    created = await client.post(
        "/api/signing/requests",
        json={"artifact_id": artifact_id, "certificate_id": cert_id},
        headers={"Idempotency-Key": "k"},
    )
    request_id = created.json()["id"]

    bad = await client.post(
        f"/api/signing/requests/{request_id}/confirm", json={"current_password": "wrong"}
    )
    assert bad.status_code == 400
    good = await client.post(
        f"/api/signing/requests/{request_id}/confirm",
        json={"current_password": "correct horse"},
    )
    assert good.status_code == 202 and good.json()["state"] == st.QUEUED
    # Illegal state: confirming again.
    dup = await client.post(
        f"/api/signing/requests/{request_id}/confirm",
        json={"current_password": "correct horse"},
    )
    assert dup.status_code == 409

    # Manifest is immutable after QUEUED (DB trigger).
    async with sm() as s:
        with pytest.raises(Exception):
            await s.execute(
                text("UPDATE signing_requests SET input_sha256='0' WHERE id=:i"),
                {"i": request_id},
            )
            await s.commit()


# --- pin relay ---------------------------------------------------------------


async def _pin_required_request(sm, owner):
    async with sm() as s:
        cert = await make_certificate(s)
        artifact = await make_input_artifact(s, owner.id, "aa" * 32, 100, "0" * 32)
        request = await make_request(s, owner, cert, artifact, state=st.PIN_REQUIRED)
        request.signer_job_id = "job-pin"
        await s.commit()
        return request.id


async def test_pin_envelope_consumed_and_no_pin_persisted(api):
    client, state, sm = api
    owner = await _seed_user(sm)
    request_id = await _pin_required_request(sm, owner)
    state["user"] = owner

    resp = await client.post(
        f"/api/signing/requests/{request_id}/pin-envelope",
        json={"challenge_id": "chal-1", "pin_jwe": PIN_JWE},
    )
    assert resp.status_code == 202 and resp.json()["authorization_status"] == "consumed"

    # The PIN envelope appears in no signing table.
    async with sm() as s:
        for (table,) in (await s.execute(
            text("SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'signing_%'")
        )).all():
            rows = (await s.execute(text(f"SELECT * FROM {table}"))).all()
            assert "PIN-ENVELOPE-SECRET-VALUE" not in repr(rows)


async def test_pin_envelope_delivery_unknown_blocks_resubmission(api):
    client, state, sm = api
    owner = await _seed_user(sm)
    request_id = await _pin_required_request(sm, owner)
    state["user"] = owner
    state["signer"].authorize_result = {"authorization_status": "delivery_unknown"}

    first = await client.post(
        f"/api/signing/requests/{request_id}/pin-envelope",
        json={"challenge_id": "chal-1", "pin_jwe": PIN_JWE},
    )
    assert first.status_code == 202 and first.json()["authorization_status"] == "delivery_unknown"
    second = await client.post(
        f"/api/signing/requests/{request_id}/pin-envelope",
        json={"challenge_id": "chal-1", "pin_jwe": PIN_JWE},
    )
    assert second.status_code == 409


async def test_pin_challenge_wrong_state_409(api):
    client, state, sm = api
    owner = await _seed_user(sm)
    async with sm() as s:
        cert = await make_certificate(s)
        artifact = await make_input_artifact(s, owner.id, "aa" * 32, 100, "0" * 32)
        request = await make_request(s, owner, cert, artifact, state=st.QUEUED)
        await s.commit()
        request_id = request.id
    state["user"] = owner
    resp = await client.get(f"/api/signing/requests/{request_id}/pin-challenge")
    assert resp.status_code == 409


# --- cancel ------------------------------------------------------------------


async def test_cancel_boundary(api):
    client, state, sm = api
    owner = await _seed_user(sm)
    async with sm() as s:
        cert = await make_certificate(s)
        artifact = await make_input_artifact(s, owner.id, "aa" * 32, 100, "0" * 32)
        cancellable = await make_request(s, owner, cert, artifact, state=st.PIN_REQUIRED)
        cancellable.signer_job_id = "job-c"
        locked = await make_request(
            s, owner, cert, artifact, state=st.AUTHORIZATION_CONSUMED, idempotency_key="idem-2"
        )
        await s.commit()
        cancellable_id, locked_id = cancellable.id, locked.id
    state["user"] = owner

    ok = await client.post(f"/api/signing/requests/{cancellable_id}/cancel")
    assert ok.status_code == 200
    refused = await client.post(f"/api/signing/requests/{locked_id}/cancel")
    assert refused.status_code == 409


# --- download ----------------------------------------------------------------


async def _completed_request(sm, store, owner):
    stored = await store.store_stream(async_iter([b"%PDF-1.4 signed output"]), max_bytes=4096)
    async with sm() as s:
        cert = await make_certificate(s)
        artifact = await make_input_artifact(s, owner.id, "aa" * 32, 100, "0" * 32)
        output = await make_output_artifact(s, owner.id)
        output.storage_key = stored.storage_key
        output.sha256 = stored.sha256
        output.byte_count = stored.byte_count
        request = await make_request(s, owner, cert, artifact, state=st.COMPLETED)
        request.output_artifact_id = output.id
        request.output_sha256 = stored.sha256
        request.output_byte_count = stored.byte_count
        request.retained = True
        await s.commit()
        return request.id, stored.sha256


async def test_download_authorization_and_one_use_download(api, store):
    client, state, sm = api
    owner = await _seed_user(sm, password="correct horse")
    request_id, sha = await _completed_request(sm, store, owner)
    state["user"] = owner

    bad = await client.post(
        f"/api/signing/requests/{request_id}/download-authorization",
        json={"current_password": "wrong"},
    )
    assert bad.status_code == 400
    auth = await client.post(
        f"/api/signing/requests/{request_id}/download-authorization",
        json={"current_password": "correct horse"},
    )
    assert auth.status_code == 201
    token = auth.json()["download_token"]

    dl = await client.get(f"/api/signing/requests/{request_id}/download?token={token}")
    assert dl.status_code == 200
    assert dl.headers["cache-control"] == "private, no-store"
    assert dl.headers["x-content-type-options"] == "nosniff"
    assert dl.content.startswith(b"%PDF-1.4 signed output")
    # One-use: second download is refused.
    again = await client.get(f"/api/signing/requests/{request_id}/download?token={token}")
    assert again.status_code == 404


async def test_download_authorization_expiry_and_hash_binding(api, store):
    client, state, sm = api
    owner = await _seed_user(sm)
    request_id, sha = await _completed_request(sm, store, owner)
    state["user"] = owner
    # Insert an expired, one-way-hashed authorization directly.
    from app.security import token_hash

    async with sm() as s:
        s.add(
            SigningDownloadAuthorization(
                request_id=request_id,
                owner_id=owner.id,
                token_hash=token_hash("expired-token"),
                output_artifact_id="whatever",
                output_sha256=sha,
                expires_at=utcnow() - timedelta(seconds=1),
            )
        )
        await s.commit()
    expired = await client.get(f"/api/signing/requests/{request_id}/download?token=expired-token")
    assert expired.status_code == 404
