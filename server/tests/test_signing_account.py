"""Account export includes safe signing metadata; deletion respects retention."""

from __future__ import annotations

import pytest
from fastapi import HTTPException

from app.api.account import AccountDeleteRequest, delete_account, export_account
from app.models import (
    SigningArtifact,
    SigningRequest,
    SigningRequestEvent,
    utcnow,
)
from app.security import hash_password

from .factories import (
    make_certificate,
    make_input_artifact,
    make_request,
    make_user,
)


class _FakeURL:
    scheme = "http"


class _FakeRequest:
    url = _FakeURL()


async def test_export_includes_signing_safe_only(db):
    user = await make_user(db)
    cert = await make_certificate(db)
    artifact = await make_input_artifact(db, user.id, "cd" * 32, 200, "1" * 32)
    request = await make_request(db, user, cert, artifact)
    await db.commit()

    data = await export_account(user, db)
    assert data["signing_requests"][0]["id"] == request.id
    assert data["signing_artifacts"][0]["id"] == artifact.id
    # No secret / storage-path leakage in the export payload.
    blob = repr(data)
    assert "storage_key" not in blob
    assert "1" * 32 not in blob  # storage_key value must not appear


async def test_delete_refused_when_retained_evidence(db):
    user = await make_user(db)
    user.password_hash = hash_password("correct horse")
    cert = await make_certificate(db)
    artifact = await make_input_artifact(db, user.id, "ef" * 32, 10, "2" * 32)
    request = await make_request(db, user, cert, artifact, state="COMPLETED")
    request.retained = True
    await db.commit()

    with pytest.raises(HTTPException) as excinfo:
        await delete_account(
            AccountDeleteRequest(password="correct horse"), _FakeRequest(), user, db
        )
    assert excinfo.value.status_code == 409


async def test_delete_removes_nonretained_signing_rows(db):
    user = await make_user(db)
    user.password_hash = hash_password("correct horse")
    cert = await make_certificate(db)
    artifact = await make_input_artifact(db, user.id, "01" * 32, 10, "3" * 32)
    request = await make_request(db, user, cert, artifact, state="CANCELLED")
    db.add(
        SigningRequestEvent(
            request_id=request.id, sequence=1, type="cancelled", state="CANCELLED"
        )
    )
    await db.commit()
    user_id = user.id

    await delete_account(
        AccountDeleteRequest(password="correct horse"), _FakeRequest(), user, db
    )

    assert await db.get(type(user), user_id) is None
    remaining_requests = (
        await db.execute(
            SigningRequest.__table__.select().where(SigningRequest.owner_id == user_id)
        )
    ).all()
    remaining_artifacts = (
        await db.execute(
            SigningArtifact.__table__.select().where(SigningArtifact.owner_id == user_id)
        )
    ).all()
    assert remaining_requests == []
    assert remaining_artifacts == []
