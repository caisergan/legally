"""Capability tokens and the internal artifact broker."""

from __future__ import annotations

import hashlib

import pytest
from sqlalchemy import text

from app.artifact_capabilities import (
    ArtifactBroker,
    CapabilityExpired,
    CapabilityInvalid,
    CapabilityIssuer,
    CapabilityMismatch,
    CapabilityReplay,
)
from app.artifact_store import ArtifactStore

from .conftest import async_iter
from .factories import make_input_artifact, make_output_artifact, make_user


def test_capability_signature_tamper_rejected(issuer: CapabilityIssuer):
    token, _ = issuer.mint(method="GET", artifact_id="a", job_id="j", sha256="d", size=5)
    prefix, payload, signature = token.split(".")
    forged = f"{prefix}.{payload}.{signature[:-2]}AA"
    with pytest.raises(CapabilityInvalid):
        issuer.parse(forged)


def test_capability_expiry(issuer: CapabilityIssuer):
    token, cap = issuer.mint(method="GET", artifact_id="a", job_id="j", sha256="d", size=5)
    with pytest.raises(CapabilityExpired):
        issuer.parse(token, now=cap.expires_at + 1)


async def test_broker_read_verifies_hash_and_size(
    db, broker: ArtifactBroker, store: ArtifactStore
):
    user = await make_user(db)
    data = b"%PDF-1.4 broker read"
    stored = await store.store_stream(async_iter([data]), max_bytes=1024)
    artifact = await make_input_artifact(
        db, user.id, stored.sha256, stored.byte_count, stored.storage_key
    )
    await db.commit()

    token, _ = broker.issuer().mint(
        method="GET",
        artifact_id=artifact.id,
        job_id="job-1",
        sha256=stored.sha256,
        size=stored.byte_count,
    )
    _, reader = await broker.read(db, token)
    assert b"".join(reader) == data


async def test_broker_read_rejects_mismatched_binding(
    db, broker: ArtifactBroker, store: ArtifactStore
):
    user = await make_user(db)
    data = b"abc"
    stored = await store.store_stream(async_iter([data]), max_bytes=64)
    artifact = await make_input_artifact(
        db, user.id, stored.sha256, stored.byte_count, stored.storage_key
    )
    await db.commit()
    token, _ = broker.issuer().mint(
        method="GET",
        artifact_id=artifact.id,
        job_id="job-1",
        sha256="00" * 32,  # wrong hash
        size=stored.byte_count,
    )
    with pytest.raises(CapabilityMismatch):
        await broker.read(db, token)


async def test_broker_read_is_one_use(db, broker: ArtifactBroker, store: ArtifactStore):
    user = await make_user(db)
    stored = await store.store_stream(async_iter([b"once"]), max_bytes=64)
    artifact = await make_input_artifact(
        db, user.id, stored.sha256, stored.byte_count, stored.storage_key
    )
    await db.commit()
    token, _ = broker.issuer().mint(
        method="GET",
        artifact_id=artifact.id,
        job_id="job-1",
        sha256=stored.sha256,
        size=stored.byte_count,
    )
    _, reader = await broker.read(db, token)
    b"".join(reader)
    with pytest.raises(CapabilityReplay):
        await broker.read(db, token)


async def test_broker_write_stores_and_hashes_without_db_bytes(
    db, broker: ArtifactBroker, store: ArtifactStore
):
    user = await make_user(db)
    output = await make_output_artifact(db, user.id)
    await db.commit()
    payload = b"%PDF-1.4 signed output bytes"
    token, _ = broker.issuer().mint(
        method="PUT",
        artifact_id=output.id,
        job_id="job-1",
        sha256="",
        size=1024,
    )
    _, sha, size = await broker.write(db, token, async_iter([payload]))
    assert sha == hashlib.sha256(payload).hexdigest()
    assert size == len(payload)
    await db.refresh(output)
    assert output.storage_key and output.sha256 == sha
    assert await store.read_all(output.storage_key) == payload

    # No document bytes anywhere in SQLite.
    hexes = payload.hex()
    for (table,) in (await db.execute(
        text("SELECT name FROM sqlite_master WHERE type='table'")
    )).all():
        rows = (await db.execute(text(f"SELECT * FROM {table}"))).all()
        blob = repr(rows).encode("utf-8", "ignore")
        assert payload not in blob and hexes.encode() not in blob


async def test_broker_write_rejects_reuse(db, broker: ArtifactBroker):
    user = await make_user(db)
    output = await make_output_artifact(db, user.id)
    await db.commit()
    token, _ = broker.issuer().mint(
        method="PUT", artifact_id=output.id, job_id="job-1", sha256="", size=1024
    )
    await broker.write(db, token, async_iter([b"first"]))
    token2, _ = broker.issuer().mint(
        method="PUT", artifact_id=output.id, job_id="job-1", sha256="", size=1024
    )
    with pytest.raises(CapabilityReplay):
        await broker.write(db, token2, async_iter([b"second"]))


async def test_broker_write_enforces_size_cap(db, broker: ArtifactBroker):
    from app.artifact_store import ArtifactTooLarge

    user = await make_user(db)
    output = await make_output_artifact(db, user.id)
    await db.commit()
    token, _ = broker.issuer().mint(
        method="PUT", artifact_id=output.id, job_id="job-1", sha256="", size=4
    )
    with pytest.raises(ArtifactTooLarge):
        await broker.write(db, token, async_iter([b"toolong"]))
