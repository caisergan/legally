"""Browser-facing signing routes (plan §8.1, Phase 3B).

Every state-changing route enforces session auth, CSRF, and exact Origin/Host.
Cross-user access to any artifact, certificate, or request returns 404. The PIN
envelope is relayed synchronously and never logged, journaled, audited, or placed
in the outbox. Completed output is downloadable only under a fresh-authenticated,
one-use, output-hash-bound authorization. When the feature is disabled every route
except capabilities behaves as feature-off.
"""

from __future__ import annotations

import time
from collections.abc import AsyncIterator
from datetime import timedelta
from typing import Annotated

from fastapi import (
    APIRouter,
    Depends,
    Header,
    HTTPException,
    Request,
    Response,
    UploadFile,
    status,
)
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, ConfigDict, Field
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from .. import signing_audit, signing_policy, signing_state as st, signing_workflow as wf
from ..artifact_capabilities import ArtifactBroker, CapabilityIssuer
from ..artifact_store import ArtifactStore, ArtifactTooLarge
from ..config import settings
from ..db import get_db
from ..models import (
    SigningApproval,
    SigningArtifact,
    SigningCertificate,
    SigningCertificateAssignment,
    SigningDownloadAuthorization,
    SigningRequest,
    SigningRequestEvent,
    User,
    new_id,
    utcnow,
)
from ..security import (
    ensure_csrf_cookie,
    get_current_user,
    require_state_change_guards,
    token_hash,
    verify_fresh_password,
)
from ..signing_client import SignerClient, SignerError, SignerUnreached, build_signer_client

router = APIRouter(prefix="/signing", tags=["signing"])

Database = Annotated[AsyncSession, Depends(get_db)]
CurrentUser = Annotated[User, Depends(get_current_user)]
Guards = Depends(require_state_change_guards)

_PDF_MAGIC = b"%PDF-"
_UPLOAD_CHUNK = 1 << 16


# --- providers (overridable in tests) ----------------------------------------

_store: ArtifactStore | None = None


def get_artifact_store() -> ArtifactStore:
    global _store
    if _store is None:
        _store = ArtifactStore(settings.signing_state_dir / "artifacts")
    return _store


def get_artifact_broker(
    store: Annotated[ArtifactStore, Depends(get_artifact_store)],
) -> ArtifactBroker:
    issuer = CapabilityIssuer(
        settings.signing_capability_secret.encode(),
        settings.signing_capability_key_id,
        settings.signing_capability_ttl_seconds,
    )
    return ArtifactBroker(store, issuer)


async def get_signer_client() -> AsyncIterator[SignerClient]:
    client = build_signer_client(settings)
    try:
        yield client
    finally:
        await client.aclose()


Store = Annotated[ArtifactStore, Depends(get_artifact_store)]
Signer = Annotated[SignerClient, Depends(get_signer_client)]


def require_signing_enabled() -> None:
    if not signing_policy.service_available(settings):
        raise HTTPException(status_code=404, detail="Bu özellik kullanılamıyor")


Enabled = Depends(require_signing_enabled)


# --- rate limiting -----------------------------------------------------------

_upload_hits: dict[int, list[float]] = {}


def _rate_limit_upload(user_id: int) -> None:
    now = time.monotonic()
    window = [t for t in _upload_hits.get(user_id, []) if now - t < 60]
    if len(window) >= settings.signing_upload_rate_per_minute:
        raise HTTPException(status_code=429, detail="Çok fazla yükleme, lütfen bekleyin")
    window.append(now)
    _upload_hits[user_id] = window


# --- ownership helpers -------------------------------------------------------


async def _owned_request(db: AsyncSession, request_id: str, user: User) -> SigningRequest:
    row = await db.get(SigningRequest, request_id)
    if row is None or row.owner_id != user.id:
        raise HTTPException(status_code=404, detail="Kayıt bulunamadı")
    return row


async def _owned_artifact(db: AsyncSession, artifact_id: str, user: User) -> SigningArtifact:
    row = await db.get(SigningArtifact, artifact_id)
    if row is None or row.owner_id != user.id or row.deleted_at is not None:
        raise HTTPException(status_code=404, detail="Belge bulunamadı")
    return row


# --- schemas -----------------------------------------------------------------


class CreateRequestBody(BaseModel):
    model_config = ConfigDict(extra="forbid")
    artifact_id: str = Field(min_length=1, max_length=64)
    certificate_id: str = Field(min_length=1, max_length=64)


class ConfirmBody(BaseModel):
    model_config = ConfigDict(extra="forbid")
    current_password: str = Field(min_length=1, max_length=1024)


class PinEnvelopeBody(BaseModel):
    model_config = ConfigDict(extra="forbid")
    challenge_id: str = Field(min_length=1, max_length=128)
    pin_jwe: str = Field(min_length=1, max_length=16384)


class DownloadAuthBody(BaseModel):
    model_config = ConfigDict(extra="forbid")
    current_password: str = Field(min_length=1, max_length=1024)


def _artifact_out(artifact: SigningArtifact) -> dict:
    return {
        "id": artifact.id,
        "display_filename": artifact.display_filename,
        "mime_type": artifact.mime_type,
        "byte_count": artifact.byte_count,
        "sha256": artifact.sha256,
        "preflight_status": artifact.preflight_status,
        "created_at": _iso(artifact.created_at),
        "expires_at": _iso(artifact.expires_at),
    }


def _request_out(request: SigningRequest) -> dict:
    return {
        "id": request.id,
        "state": request.state,
        "state_label": st.label_tr(request.state),
        "profile": request.profile,
        "algorithm": request.algorithm,
        "policy_version": request.policy_version,
        "certificate_id": request.certificate_id,
        "certificate_fingerprint_sha256": request.certificate_fingerprint_sha256,
        "input_sha256": request.input_sha256,
        "input_byte_count": request.input_byte_count,
        "output_sha256": request.output_sha256,
        "output_byte_count": request.output_byte_count,
        "failure_code": request.failure_code,
        "cancellable": st.cancellation_allowed(request.state),
        "created_at": _iso(request.created_at),
        "queued_at": _iso(request.queued_at),
        "completed_at": _iso(request.completed_at),
    }


# --- capability + certificate discovery --------------------------------------


@router.get("/capabilities")
async def capabilities(request: Request, response: Response, _: CurrentUser) -> dict:
    ensure_csrf_cookie(request, response)
    return signing_policy.capabilities(settings)


@router.get("/challenge-keyset", dependencies=[Enabled])
async def challenge_keyset(_: CurrentUser) -> dict:
    import json

    return json.loads(settings.signing_challenge_jwks or '{"keys": []}')


@router.get("/certificates", dependencies=[Enabled])
async def list_certificates(user: CurrentUser, db: Database) -> dict:
    rows = (
        await db.execute(
            select(SigningCertificate)
            .join(
                SigningCertificateAssignment,
                SigningCertificateAssignment.certificate_id == SigningCertificate.id,
            )
            .where(
                SigningCertificateAssignment.user_id == user.id,
                SigningCertificateAssignment.active == True,  # noqa: E712
                SigningCertificate.status == "available",
            )
        )
    ).scalars().all()
    return {
        "items": [
            {
                "id": cert.id,
                "subject_display": cert.subject_display,
                "issuer_display": cert.issuer_display,
                "serial_suffix": cert.serial_suffix,
                "fingerprint_suffix": cert.fingerprint_suffix,
                "not_before": _iso(cert.not_before),
                "not_after": _iso(cert.not_after),
                "public_key_type": cert.public_key_type,
                "status": cert.status,
                "test_only": cert.test_only,
            }
            for cert in rows
        ]
    }


# --- artifacts ---------------------------------------------------------------


@router.post("/artifacts", status_code=status.HTTP_201_CREATED, dependencies=[Enabled, Guards])
async def upload_artifact(
    user: CurrentUser, db: Database, store: Store, file: UploadFile
) -> dict:
    _rate_limit_upload(user.id)
    try:
        stored = await store.store_stream(
            _pdf_chunks(file), max_bytes=settings.signing_max_upload_bytes
        )
    except ArtifactTooLarge as error:
        raise HTTPException(status_code=413, detail="Belge çok büyük") from error
    except _NotPdf as error:
        raise HTTPException(status_code=422, detail="Yalnızca PDF yüklenebilir") from error

    artifact = SigningArtifact(
        owner_id=user.id,
        kind="input",
        display_filename=_safe_filename(file.filename),
        mime_type=signing_policy.ACCEPTED_UPLOAD_MIME,
        byte_count=stored.byte_count,
        sha256=stored.sha256,
        storage_key=stored.storage_key,
        preflight_status="accepted",
        expires_at=utcnow() + timedelta(hours=settings.signing_input_retention_hours),
    )
    db.add(artifact)
    await signing_audit.record(
        db, "artifact.upload", actor_type="user", actor_id=user.id,
        input_sha256=stored.sha256, detail={"byte_count": stored.byte_count},
    )
    await db.commit()
    return _artifact_out(artifact)


@router.get("/artifacts/{artifact_id}", dependencies=[Enabled])
async def get_artifact(artifact_id: str, user: CurrentUser, db: Database) -> dict:
    return _artifact_out(await _owned_artifact(db, artifact_id, user))


@router.delete("/artifacts/{artifact_id}", status_code=status.HTTP_204_NO_CONTENT,
               dependencies=[Enabled, Guards])
async def delete_artifact(
    artifact_id: str, user: CurrentUser, db: Database, store: Store
) -> Response:
    artifact = await _owned_artifact(db, artifact_id, user)
    if artifact.kind != "input" or artifact.referenced or artifact.retained:
        raise HTTPException(status_code=409, detail="Bu belge silinemez")
    await store.delete(artifact.storage_key)
    await db.delete(artifact)
    await db.commit()
    return Response(status_code=status.HTTP_204_NO_CONTENT)


# --- requests ----------------------------------------------------------------


@router.post("/requests", status_code=status.HTTP_201_CREATED, dependencies=[Enabled, Guards])
async def create_request(
    body: CreateRequestBody,
    user: CurrentUser,
    db: Database,
    response: Response,
    idempotency_key: Annotated[str | None, Header(alias="Idempotency-Key")] = None,
) -> dict:
    if not idempotency_key:
        raise HTTPException(status_code=400, detail="Idempotency-Key gerekli")

    existing = await db.scalar(
        select(SigningRequest).where(
            SigningRequest.owner_id == user.id,
            SigningRequest.operation == "sign_request",
            SigningRequest.idempotency_key == idempotency_key,
        )
    )
    if existing is not None:
        if existing.artifact_id != body.artifact_id or existing.certificate_id != body.certificate_id:
            raise HTTPException(status_code=409, detail="Idempotency-Key farklı bir istekle kullanılmış")
        response.status_code = status.HTTP_200_OK
        return _request_out(existing)

    artifact = await _owned_artifact(db, body.artifact_id, user)
    if artifact.kind != "input" or artifact.preflight_status != "accepted":
        raise HTTPException(status_code=409, detail="Belge imzaya uygun değil")
    cert = await _owned_certificate(db, body.certificate_id, user)

    request = SigningRequest(
        owner_id=user.id,
        idempotency_key=idempotency_key,
        artifact_id=artifact.id,
        certificate_id=cert.id,
        state=st.AWAITING_OWNER_CONFIRMATION,
        profile=signing_policy.PROFILE,
        algorithm=signing_policy.ALGORITHM,
        policy_version=signing_policy.POLICY_VERSION,
        input_sha256=artifact.sha256,
        input_byte_count=artifact.byte_count,
        certificate_fingerprint_sha256=cert.certificate_fingerprint_sha256,
    )
    db.add(request)
    artifact.referenced = True
    await signing_audit.record(
        db, "request.create", actor_type="user", actor_id=user.id,
        certificate_id=cert.id, input_sha256=artifact.sha256,
    )
    await db.commit()
    return _request_out(request)


@router.get("/requests", dependencies=[Enabled])
async def list_requests(
    user: CurrentUser, db: Database, limit: int = 50, offset: int = 0
) -> dict:
    limit = max(1, min(limit, 100))
    rows = (
        await db.execute(
            select(SigningRequest)
            .where(SigningRequest.owner_id == user.id)
            .order_by(SigningRequest.created_at.desc())
            .limit(limit)
            .offset(max(0, offset))
        )
    ).scalars().all()
    return {"items": [_request_out(r) for r in rows]}


@router.get("/requests/{request_id}", dependencies=[Enabled])
async def get_request(request_id: str, user: CurrentUser, db: Database) -> dict:
    return _request_out(await _owned_request(db, request_id, user))


@router.post("/requests/{request_id}/confirm", status_code=status.HTTP_202_ACCEPTED,
             dependencies=[Enabled, Guards])
async def confirm_request(
    request_id: str, body: ConfirmBody, user: CurrentUser, db: Database, signer: Signer, store: Store
) -> dict:
    request = await _owned_request(db, request_id, user)
    if request.state != st.AWAITING_OWNER_CONFIRMATION:
        raise HTTPException(status_code=409, detail="İstek onaya uygun durumda değil")
    if not await verify_fresh_password(user, body.current_password):
        raise HTTPException(status_code=400, detail="Mevcut şifre hatalı")

    cert = await db.get(SigningCertificate, request.certificate_id)
    if cert is None:
        raise HTTPException(status_code=409, detail="Sertifika bulunamadı")

    approval = SigningApproval(
        request_id=request.id,
        owner_id=user.id,
        approval_nonce=new_id(),
        input_sha256=request.input_sha256,
        certificate_fingerprint_sha256=request.certificate_fingerprint_sha256,
        policy_version=request.policy_version,
        expires_at=utcnow() + timedelta(seconds=settings.signing_approval_ttl_seconds),
        consumed_at=utcnow(),
    )
    db.add(approval)
    output = SigningArtifact(
        owner_id=user.id, kind="output", display_filename=_signed_name(request),
        byte_count=0, sha256="", storage_key="", preflight_status="pending",
    )
    db.add(output)
    await db.flush()
    request.output_artifact_id = output.id

    outbox = await wf.enqueue_create_job(
        db, request, credential_id=cert.service_credential_id, approval=approval,
        output_artifact_id=output.id, max_output_bytes=settings.signing_max_output_bytes,
        expires_at=utcnow() + timedelta(seconds=settings.signing_approval_ttl_seconds),
    )
    await signing_audit.record(
        db, "request.confirm", actor_type="user", actor_id=user.id,
        request_id=request.id, signer_job_id=request.signer_job_id,
    )
    await db.commit()
    await wf.dispatch_outbox(db, signer, outbox, store=store)
    await db.commit()
    return _request_out(request)


@router.get("/requests/{request_id}/events", dependencies=[Enabled])
async def request_events(
    request_id: str, user: CurrentUser, db: Database, signer: Signer, store: Store, after: int = 0
) -> dict:
    request = await _owned_request(db, request_id, user)
    if request.signer_job_id:
        try:
            await wf.project_events(
                db, signer, request, store=store, max_output_bytes=settings.signing_max_output_bytes
            )
            await db.commit()
        except SignerUnreached:
            pass  # serve durable events; signer temporarily unreachable
    rows = (
        await db.execute(
            select(SigningRequestEvent)
            .where(
                SigningRequestEvent.request_id == request.id,
                SigningRequestEvent.sequence > after,
            )
            .order_by(SigningRequestEvent.sequence)
        )
    ).scalars().all()
    return {
        "state": request.state,
        "state_label": st.label_tr(request.state),
        "last_sequence": request.last_event_sequence,
        "events": [
            {
                "sequence": e.sequence,
                "type": e.type,
                "state": e.state,
                "state_label": st.label_tr(e.state) if e.state else None,
                "created_at": _iso(e.created_at),
            }
            for e in rows
        ],
    }


@router.get("/requests/{request_id}/pin-challenge", dependencies=[Enabled])
async def pin_challenge(
    request_id: str, user: CurrentUser, db: Database, signer: Signer
) -> dict:
    request = await _owned_request(db, request_id, user)
    if request.state != st.PIN_REQUIRED or not request.signer_job_id:
        raise HTTPException(status_code=409, detail="PIN şu anda istenemez")
    try:
        return await signer.get_pin_challenge(request.signer_job_id)
    except SignerError as error:
        raise _mapped_signer_error(error)
    except SignerUnreached as error:
        raise HTTPException(status_code=503, detail="İmza servisi kullanılamıyor") from error


@router.post("/requests/{request_id}/pin-envelope", status_code=status.HTTP_202_ACCEPTED,
             dependencies=[Enabled, Guards])
async def pin_envelope(
    request_id: str, body: PinEnvelopeBody, user: CurrentUser, db: Database, signer: Signer, store: Store
) -> dict:
    request = await _owned_request(db, request_id, user)
    # Reconcile durable signer state before accepting an envelope.
    if request.signer_job_id:
        try:
            await wf.project_events(
                db, signer, request, store=store, max_output_bytes=settings.signing_max_output_bytes
            )
        except SignerUnreached:
            pass
    if request.pin_relay_status == "pending_unknown":
        raise HTTPException(status_code=409, detail="Önceki PIN denemesi belirsiz; durumu izleyin")
    if request.state != st.PIN_REQUIRED:
        raise HTTPException(status_code=409, detail="PIN gönderimi bu durumda kabul edilmiyor")

    pin_jwe = body.pin_jwe
    try:
        result = await signer.authorize(request.signer_job_id, body.challenge_id, pin_jwe)
    except SignerUnreached as error:
        # Proven not delivered: safe to keep the challenge; no state change.
        await signing_audit.record(
            db, "signing.pin_envelope", outcome="error", actor_type="user",
            actor_id=user.id, request_id=request.id, signer_job_id=request.signer_job_id,
        )
        await db.commit()
        raise HTTPException(status_code=503, detail="İmza servisine ulaşılamadı") from error
    except SignerError as error:
        raise _mapped_signer_error(error)
    finally:
        del pin_jwe

    outcome = str(result.get("authorization_status", "delivery_unknown"))
    if outcome == "delivery_unknown":
        request.pin_relay_status = "pending_unknown"
    else:
        request.pin_relay_status = "consumed"
    await signing_audit.record(
        db, "signing.pin_envelope", outcome="ok", actor_type="user", actor_id=user.id,
        request_id=request.id, signer_job_id=request.signer_job_id,
        detail={"authorization_status": outcome},
    )
    await db.commit()
    return {"authorization_status": outcome}


@router.post("/requests/{request_id}/cancel", dependencies=[Enabled, Guards])
async def cancel_request(
    request_id: str, user: CurrentUser, db: Database, signer: Signer
) -> dict:
    request = await _owned_request(db, request_id, user)
    if not st.cancellation_allowed(request.state):
        raise HTTPException(status_code=409, detail="İstek artık iptal edilemez")
    if request.state in (st.UPLOADED, st.VALIDATED, st.AWAITING_OWNER_CONFIRMATION):
        request.state = st.CANCELLED
    else:
        outbox = await wf.enqueue_cancel(db, request)
        await db.commit()
        try:
            await wf.dispatch_outbox(db, signer, outbox)
        except SignerUnreached:
            pass
    await signing_audit.record(
        db, "request.cancel", actor_type="user", actor_id=user.id, request_id=request.id
    )
    await db.commit()
    return _request_out(request)


# --- download ----------------------------------------------------------------


@router.post("/requests/{request_id}/download-authorization",
             status_code=status.HTTP_201_CREATED, dependencies=[Enabled, Guards])
async def download_authorization(
    request_id: str, body: DownloadAuthBody, user: CurrentUser, db: Database
) -> dict:
    request = await _owned_request(db, request_id, user)
    if request.state != st.COMPLETED or not request.output_artifact_id or not request.output_sha256:
        raise HTTPException(status_code=409, detail="İndirmeye hazır çıktı yok")
    if not await verify_fresh_password(user, body.current_password):
        raise HTTPException(status_code=400, detail="Mevcut şifre hatalı")

    raw_token = new_id() + new_id()
    expires_at = utcnow() + timedelta(seconds=settings.signing_download_ttl_seconds)
    db.add(
        SigningDownloadAuthorization(
            request_id=request.id,
            owner_id=user.id,
            token_hash=token_hash(raw_token),
            output_artifact_id=request.output_artifact_id,
            output_sha256=request.output_sha256,
            expires_at=expires_at,
        )
    )
    await signing_audit.record(
        db, "download.authorize", actor_type="user", actor_id=user.id, request_id=request.id
    )
    await db.commit()
    return {"download_token": raw_token, "expires_at": _iso(expires_at)}


@router.get("/requests/{request_id}/download", dependencies=[Enabled])
async def download(
    request_id: str, token: str, user: CurrentUser, db: Database, store: Store
) -> StreamingResponse:
    request = await _owned_request(db, request_id, user)
    if request.state != st.COMPLETED:
        raise HTTPException(status_code=409, detail="Çıktı hazır değil")
    auth = await db.scalar(
        select(SigningDownloadAuthorization).where(
            SigningDownloadAuthorization.token_hash == token_hash(token),
            SigningDownloadAuthorization.request_id == request.id,
            SigningDownloadAuthorization.owner_id == user.id,
        )
    )
    now = utcnow()
    if (
        auth is None
        or auth.consumed_at is not None
        or auth.revoked_at is not None
        or auth.expires_at.replace(tzinfo=None) < now.replace(tzinfo=None)
        or auth.output_sha256 != request.output_sha256
    ):
        raise HTTPException(status_code=404, detail="İndirme yetkisi geçersiz")

    output = await db.get(SigningArtifact, auth.output_artifact_id)
    if output is None or not output.storage_key:
        raise HTTPException(status_code=404, detail="Çıktı bulunamadı")
    auth.consumed_at = now  # one-use
    await db.commit()

    return StreamingResponse(
        store.read_iter(output.storage_key),
        media_type="application/pdf",
        headers={
            "Cache-Control": "private, no-store",
            "X-Content-Type-Options": "nosniff",
            "Content-Disposition": f'attachment; filename="{_signed_name(request)}"',
        },
    )


# --- helpers -----------------------------------------------------------------


class _NotPdf(Exception):
    pass


async def _pdf_chunks(file: UploadFile) -> AsyncIterator[bytes]:
    first = True
    while True:
        chunk = await file.read(_UPLOAD_CHUNK)
        if not chunk:
            break
        if first:
            if not chunk.startswith(_PDF_MAGIC):
                raise _NotPdf()
            first = False
        yield chunk
    if first:
        raise _NotPdf()  # empty upload


async def _owned_certificate(db: AsyncSession, certificate_id: str, user: User) -> SigningCertificate:
    assignment = await db.scalar(
        select(SigningCertificateAssignment).where(
            SigningCertificateAssignment.certificate_id == certificate_id,
            SigningCertificateAssignment.user_id == user.id,
            SigningCertificateAssignment.active == True,  # noqa: E712
        )
    )
    cert = await db.get(SigningCertificate, certificate_id)
    if assignment is None or cert is None or cert.status != "available":
        raise HTTPException(status_code=404, detail="Sertifika bulunamadı")
    return cert


def _mapped_signer_error(error: SignerError) -> HTTPException:
    if error.status_code in (409, 410, 422):
        detail = {409: "İşlem bu durumda yapılamıyor", 410: "Süre doldu", 422: "Geçersiz istek"}[
            error.status_code
        ]
        return HTTPException(status_code=error.status_code, detail=detail)
    return HTTPException(status_code=502, detail="İmza servisi hatası")


def _safe_filename(name: str | None) -> str:
    base = (name or "belge.pdf").rsplit("/", 1)[-1].rsplit("\\", 1)[-1]
    cleaned = "".join(c for c in base if c.isalnum() or c in "-_. ").strip() or "belge.pdf"
    return cleaned[:200]


def _signed_name(request: SigningRequest) -> str:
    return f"imzali-{request.id[:12]}-signed.pdf"


def _iso(value) -> str | None:
    if value is None:
        return None
    from datetime import timezone

    normalized = value if value.tzinfo else value.replace(tzinfo=timezone.utc)
    return normalized.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")
