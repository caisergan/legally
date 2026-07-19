"""Row factories for signing tests."""

from __future__ import annotations

from datetime import timedelta

from sqlalchemy.ext.asyncio import AsyncSession

from app import signing_state as st
from app.models import (
    SigningApproval,
    SigningArtifact,
    SigningCertificate,
    SigningRequest,
    User,
    utcnow,
)
from app.signing_policy import ALGORITHM, POLICY_VERSION, PROFILE


async def make_user(db: AsyncSession, email: str = "user@example.com") -> User:
    user = User(email=email, password_hash="x", display_name="Test")
    db.add(user)
    await db.flush()
    return user


async def make_certificate(
    db: AsyncSession, service_id: str = "svc-1", fingerprint: str | None = None
) -> SigningCertificate:
    fingerprint = fingerprint or ("ab" * 32)
    cert = SigningCertificate(
        service_credential_id=service_id,
        certificate_fingerprint_sha256=fingerprint,
        subject_display="E*** A***",
        issuer_display="Test CA",
        serial_suffix="A1B2",
        fingerprint_suffix=fingerprint[-8:].upper(),
        not_before=utcnow() - timedelta(days=1),
        not_after=utcnow() + timedelta(days=365),
        public_key_type="RSA",
        public_key_bits=2048,
    )
    db.add(cert)
    await db.flush()
    return cert


async def make_input_artifact(
    db: AsyncSession, owner_id: int, sha256: str, byte_count: int, storage_key: str
) -> SigningArtifact:
    artifact = SigningArtifact(
        owner_id=owner_id,
        kind="input",
        display_filename="petition.pdf",
        byte_count=byte_count,
        sha256=sha256,
        storage_key=storage_key,
        preflight_status="accepted",
        referenced=True,
    )
    db.add(artifact)
    await db.flush()
    return artifact


async def make_output_artifact(db: AsyncSession, owner_id: int) -> SigningArtifact:
    artifact = SigningArtifact(
        owner_id=owner_id,
        kind="output",
        display_filename="petition-signed.pdf",
        byte_count=0,
        sha256="",
        storage_key="",
        preflight_status="pending",
    )
    db.add(artifact)
    await db.flush()
    return artifact


async def make_request(
    db: AsyncSession,
    owner: User,
    cert: SigningCertificate,
    artifact: SigningArtifact,
    *,
    state: str = st.AWAITING_OWNER_CONFIRMATION,
    idempotency_key: str = "idem-1",
) -> SigningRequest:
    request = SigningRequest(
        owner_id=owner.id,
        idempotency_key=idempotency_key,
        artifact_id=artifact.id,
        certificate_id=cert.id,
        state=state,
        profile=PROFILE,
        algorithm=ALGORITHM,
        policy_version=POLICY_VERSION,
        input_sha256=artifact.sha256,
        input_byte_count=artifact.byte_count,
        certificate_fingerprint_sha256=cert.certificate_fingerprint_sha256,
    )
    db.add(request)
    await db.flush()
    return request


async def make_approval(db: AsyncSession, request: SigningRequest) -> SigningApproval:
    approval = SigningApproval(
        request_id=request.id,
        owner_id=request.owner_id,
        approval_nonce="nonce-" + request.id,
        input_sha256=request.input_sha256,
        certificate_fingerprint_sha256=request.certificate_fingerprint_sha256,
        policy_version=request.policy_version,
        expires_at=utcnow() + timedelta(minutes=5),
    )
    db.add(approval)
    await db.flush()
    return approval
