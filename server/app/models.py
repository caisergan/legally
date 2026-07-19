"""SQLAlchemy models — schema follows design plan §5."""

import uuid
from datetime import date, datetime, timezone

from sqlalchemy import (
    DDL,
    Boolean,
    Date,
    DateTime,
    ForeignKey,
    Index,
    Integer,
    String,
    Text,
    UniqueConstraint,
    event,
    text,
)
from sqlalchemy.orm import DeclarativeBase, Mapped, mapped_column


def utcnow() -> datetime:
    return datetime.now(timezone.utc)


def new_id() -> str:
    return uuid.uuid4().hex


class Base(DeclarativeBase):
    pass


# --- identity & sessions -----------------------------------------------------


class User(Base):
    __tablename__ = "users"

    id: Mapped[int] = mapped_column(primary_key=True)
    email: Mapped[str] = mapped_column(String, unique=True, index=True)
    password_hash: Mapped[str] = mapped_column(String)
    display_name: Mapped[str | None] = mapped_column(String, nullable=True)
    role: Mapped[str] = mapped_column(String, default="user")  # user | admin
    locale: Mapped[str] = mapped_column(String, default="tr")
    theme: Mapped[str] = mapped_column(String, default="light")
    preferred_model: Mapped[str | None] = mapped_column(String, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    last_login_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)


class AuthSession(Base):
    __tablename__ = "sessions"

    token_hash: Mapped[str] = mapped_column(String, primary_key=True)  # sha256(token)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    expires_at: Mapped[datetime] = mapped_column(DateTime)
    last_seen_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    user_agent: Mapped[str | None] = mapped_column(String, nullable=True)


# --- chat ---------------------------------------------------------------------


class Conversation(Base):
    __tablename__ = "conversations"

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    title: Mapped[str | None] = mapped_column(String, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    updated_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow, onupdate=utcnow)
    archived_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)


class Message(Base):
    __tablename__ = "messages"

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    conversation_id: Mapped[str] = mapped_column(
        ForeignKey("conversations.id", ondelete="CASCADE"), index=True
    )
    role: Mapped[str] = mapped_column(String)  # user | assistant
    content: Mapped[str] = mapped_column(Text, default="")
    status: Mapped[str] = mapped_column(String, default="complete")  # complete | interrupted | error
    tool_calls_json: Mapped[str | None] = mapped_column(Text, nullable=True)
    citations_json: Mapped[str | None] = mapped_column(Text, nullable=True)
    model: Mapped[str | None] = mapped_column(String, nullable=True)
    input_tokens: Mapped[int] = mapped_column(Integer, default=0)
    output_tokens: Mapped[int] = mapped_column(Integer, default=0)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)


# --- search history -------------------------------------------------------------


class Search(Base):
    __tablename__ = "searches"

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    origin: Mapped[str] = mapped_column(String, default="manual")  # chat | manual
    conversation_id: Mapped[str | None] = mapped_column(String, nullable=True)
    source_db: Mapped[str] = mapped_column(String)
    tool_name: Mapped[str] = mapped_column(String)
    params_json: Mapped[str] = mapped_column(Text)
    query_text: Mapped[str] = mapped_column(String, default="")
    result_count: Mapped[int | None] = mapped_column(Integer, nullable=True)
    status: Mapped[str] = mapped_column(String, default="ok")  # ok | empty | error | rate_limited
    error_text: Mapped[str | None] = mapped_column(Text, nullable=True)
    duration_ms: Mapped[int | None] = mapped_column(Integer, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow, index=True)


class SearchResultSnapshot(Base):
    __tablename__ = "search_results_snapshot"

    search_id: Mapped[str] = mapped_column(
        ForeignKey("searches.id", ondelete="CASCADE"), primary_key=True
    )
    rank: Mapped[int] = mapped_column(Integer, primary_key=True)
    normalized_json: Mapped[str] = mapped_column(Text)


# --- documents -------------------------------------------------------------------


class DocumentCache(Base):
    __tablename__ = "document_cache"

    source_db: Mapped[str] = mapped_column(String, primary_key=True)
    doc_key: Mapped[str] = mapped_column(String, primary_key=True)
    chunk_page: Mapped[int] = mapped_column(Integer, primary_key=True, default=1)
    doc_ref_json: Mapped[str] = mapped_column(Text)
    title: Mapped[str | None] = mapped_column(String, nullable=True)
    markdown: Mapped[str] = mapped_column(Text, default="")
    total_pages: Mapped[int] = mapped_column(Integer, default=1)
    fetched_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)


class DocumentView(Base):
    __tablename__ = "document_views"

    id: Mapped[int] = mapped_column(primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    source_db: Mapped[str] = mapped_column(String)
    doc_key: Mapped[str] = mapped_column(String)
    title: Mapped[str | None] = mapped_column(String, nullable=True)
    meta_json: Mapped[str | None] = mapped_column(Text, nullable=True)
    opened_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow, index=True)


class Bookmark(Base):
    __tablename__ = "bookmarks"
    __table_args__ = (UniqueConstraint("user_id", "source_db", "doc_key"),)

    id: Mapped[int] = mapped_column(primary_key=True)
    user_id: Mapped[int] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    source_db: Mapped[str] = mapped_column(String)
    doc_key: Mapped[str] = mapped_column(String)
    doc_ref_json: Mapped[str] = mapped_column(Text)
    title: Mapped[str | None] = mapped_column(String, nullable=True)
    meta_json: Mapped[str | None] = mapped_column(Text, nullable=True)
    note: Mapped[str] = mapped_column(Text, default="")
    tags_json: Mapped[str] = mapped_column(Text, default="[]")
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)


# --- quotas ----------------------------------------------------------------------


class UsageDaily(Base):
    __tablename__ = "usage_daily"

    user_id: Mapped[int] = mapped_column(
        ForeignKey("users.id", ondelete="CASCADE"), primary_key=True
    )
    day: Mapped[date] = mapped_column(Date, primary_key=True)
    llm_input_tokens: Mapped[int] = mapped_column(Integer, default=0)
    llm_output_tokens: Mapped[int] = mapped_column(Integer, default=0)
    tool_calls: Mapped[int] = mapped_column(Integer, default=0)


LEGACY_TABLE_NAMES: tuple[str, ...] = (
    "users",
    "sessions",
    "conversations",
    "messages",
    "searches",
    "search_results_snapshot",
    "document_cache",
    "document_views",
    "bookmarks",
    "usage_daily",
)

SIGNING_TABLE_NAMES: tuple[str, ...] = (
    "signing_artifacts",
    "signing_certificates",
    "signing_certificate_assignments",
    "signing_requests",
    "signing_approvals",
    "signing_download_authorizations",
    "signing_request_events",
    "signing_audit_events",
    "signing_outbox",
    "signing_capability_consumptions",
)


# --- signing (e-imza) — plan §9.1 --------------------------------------------
#
# Document bytes never live in SQLite. Owner FKs use RESTRICT so retained
# completed evidence cannot be cascade-deleted with an account; deletion is
# gated explicitly in the account handler. No column stores a PIN, PIN
# ciphertext, account password, or artifact capability.


class SigningArtifact(Base):
    __tablename__ = "signing_artifacts"

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    owner_id: Mapped[int] = mapped_column(
        ForeignKey("users.id", ondelete="RESTRICT"), index=True
    )
    kind: Mapped[str] = mapped_column(String, default="input")  # input | output
    display_filename: Mapped[str] = mapped_column(String)
    mime_type: Mapped[str] = mapped_column(String, default="application/pdf")
    byte_count: Mapped[int] = mapped_column(Integer)
    sha256: Mapped[str] = mapped_column(String, index=True)
    storage_key: Mapped[str] = mapped_column(String)  # opaque private-store segment, not a path
    preflight_status: Mapped[str] = mapped_column(String, default="pending")
    referenced: Mapped[bool] = mapped_column(Boolean, default=False)
    retained: Mapped[bool] = mapped_column(Boolean, default=False)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    expires_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    deleted_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)


class SigningCertificate(Base):
    __tablename__ = "signing_certificates"

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    service_credential_id: Mapped[str] = mapped_column(String, unique=True)
    certificate_fingerprint_sha256: Mapped[str] = mapped_column(String, unique=True)
    subject_display: Mapped[str] = mapped_column(String)
    issuer_display: Mapped[str] = mapped_column(String)
    serial_suffix: Mapped[str] = mapped_column(String)
    fingerprint_suffix: Mapped[str] = mapped_column(String)
    not_before: Mapped[datetime] = mapped_column(DateTime)
    not_after: Mapped[datetime] = mapped_column(DateTime)
    public_key_type: Mapped[str] = mapped_column(String)
    public_key_bits: Mapped[int | None] = mapped_column(Integer, nullable=True)
    mode: Mapped[str] = mapped_column(String, default="softhsm")
    status: Mapped[str] = mapped_column(String, default="available")
    test_only: Mapped[bool] = mapped_column(Boolean, default=True)
    last_checked_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    updated_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow, onupdate=utcnow)


class SigningCertificateAssignment(Base):
    __tablename__ = "signing_certificate_assignments"
    __table_args__ = (
        # v1: at most one active assignment (owner) per certificate.
        Index(
            "ux_signing_assignment_active_certificate",
            "certificate_id",
            unique=True,
            sqlite_where=text("active = 1"),
        ),
    )

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    certificate_id: Mapped[str] = mapped_column(
        ForeignKey("signing_certificates.id", ondelete="RESTRICT"), index=True
    )
    user_id: Mapped[int] = mapped_column(
        ForeignKey("users.id", ondelete="RESTRICT"), index=True
    )
    active: Mapped[bool] = mapped_column(Boolean, default=True)
    assigned_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    assigned_by: Mapped[int | None] = mapped_column(Integer, nullable=True)
    revoked_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    revoked_by: Mapped[int | None] = mapped_column(Integer, nullable=True)


class SigningRequest(Base):
    __tablename__ = "signing_requests"
    __table_args__ = (
        UniqueConstraint(
            "owner_id", "operation", "idempotency_key",
            name="ux_signing_request_idempotency",
        ),
    )

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    owner_id: Mapped[int] = mapped_column(
        ForeignKey("users.id", ondelete="RESTRICT"), index=True
    )
    operation: Mapped[str] = mapped_column(String, default="sign_request")
    idempotency_key: Mapped[str] = mapped_column(String)
    artifact_id: Mapped[str] = mapped_column(
        ForeignKey("signing_artifacts.id", ondelete="RESTRICT")
    )
    certificate_id: Mapped[str] = mapped_column(
        ForeignKey("signing_certificates.id", ondelete="RESTRICT")
    )
    state: Mapped[str] = mapped_column(String, default="UPLOADED", index=True)
    version: Mapped[int] = mapped_column(Integer, default=1)
    # Immutable manifest snapshot (protected by trigger after QUEUED).
    profile: Mapped[str] = mapped_column(String)
    algorithm: Mapped[str] = mapped_column(String)
    policy_version: Mapped[str] = mapped_column(String)
    input_sha256: Mapped[str] = mapped_column(String)
    input_byte_count: Mapped[int] = mapped_column(Integer)
    certificate_fingerprint_sha256: Mapped[str] = mapped_column(String)
    # Signer linkage + output evidence.
    signer_job_id: Mapped[str | None] = mapped_column(String, nullable=True, index=True)
    signer_command_id: Mapped[str | None] = mapped_column(String, nullable=True)
    output_artifact_id: Mapped[str | None] = mapped_column(String, nullable=True)
    output_sha256: Mapped[str | None] = mapped_column(String, nullable=True)
    output_byte_count: Mapped[int | None] = mapped_column(Integer, nullable=True)
    failure_code: Mapped[str | None] = mapped_column(String, nullable=True)
    last_event_sequence: Mapped[int] = mapped_column(Integer, default=0)
    signer_event_cursor: Mapped[int] = mapped_column(Integer, default=0)
    retained: Mapped[bool] = mapped_column(Boolean, default=False)
    # PIN relay is synchronous and never queued; this durable marker blocks a
    # second envelope after an ambiguous delivery (§8.1). None | pending_unknown | consumed.
    pin_relay_status: Mapped[str | None] = mapped_column(String, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    updated_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow, onupdate=utcnow)
    queued_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    completed_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)


class SigningApproval(Base):
    __tablename__ = "signing_approvals"

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    request_id: Mapped[str] = mapped_column(
        ForeignKey("signing_requests.id", ondelete="RESTRICT"), index=True
    )
    owner_id: Mapped[int] = mapped_column(Integer, index=True)
    approval_nonce: Mapped[str] = mapped_column(String, unique=True)
    input_sha256: Mapped[str] = mapped_column(String)
    certificate_fingerprint_sha256: Mapped[str] = mapped_column(String)
    policy_version: Mapped[str] = mapped_column(String)
    approved_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    expires_at: Mapped[datetime] = mapped_column(DateTime)
    consumed_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)


class SigningDownloadAuthorization(Base):
    __tablename__ = "signing_download_authorizations"

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    request_id: Mapped[str] = mapped_column(
        ForeignKey("signing_requests.id", ondelete="RESTRICT"), index=True
    )
    owner_id: Mapped[int] = mapped_column(Integer, index=True)
    token_hash: Mapped[str] = mapped_column(String, unique=True)  # one-way hash only
    output_artifact_id: Mapped[str] = mapped_column(String)
    output_sha256: Mapped[str] = mapped_column(String)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    expires_at: Mapped[datetime] = mapped_column(DateTime)
    consumed_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    revoked_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)


class SigningRequestEvent(Base):
    __tablename__ = "signing_request_events"
    __table_args__ = (
        UniqueConstraint("request_id", "sequence", name="ux_signing_event_sequence"),
    )

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    request_id: Mapped[str] = mapped_column(
        ForeignKey("signing_requests.id", ondelete="RESTRICT"), index=True
    )
    sequence: Mapped[int] = mapped_column(Integer)  # monotonic per request
    type: Mapped[str] = mapped_column(String)
    state: Mapped[str | None] = mapped_column(String, nullable=True)
    detail_json: Mapped[str | None] = mapped_column(Text, nullable=True)
    signer_event_sequence: Mapped[int | None] = mapped_column(Integer, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)


class SigningAuditEvent(Base):
    __tablename__ = "signing_audit_events"

    id: Mapped[int] = mapped_column(primary_key=True)  # global monotonic order
    actor_type: Mapped[str] = mapped_column(String, default="system")  # user|admin|system
    actor_id: Mapped[int | None] = mapped_column(Integer, nullable=True)  # no FK: survives deletion
    request_id: Mapped[str | None] = mapped_column(String, nullable=True, index=True)
    signer_job_id: Mapped[str | None] = mapped_column(String, nullable=True)
    command_id: Mapped[str | None] = mapped_column(String, nullable=True)
    certificate_id: Mapped[str | None] = mapped_column(String, nullable=True)
    action: Mapped[str] = mapped_column(String)
    outcome: Mapped[str] = mapped_column(String, default="ok")  # ok|denied|error
    input_sha256: Mapped[str | None] = mapped_column(String, nullable=True)
    output_sha256: Mapped[str | None] = mapped_column(String, nullable=True)
    detail_json: Mapped[str | None] = mapped_column(Text, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow, index=True)


class SigningOutbox(Base):
    __tablename__ = "signing_outbox"

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    request_id: Mapped[str] = mapped_column(
        ForeignKey("signing_requests.id", ondelete="RESTRICT"), index=True
    )
    command_id: Mapped[str] = mapped_column(String, unique=True)
    command_type: Mapped[str] = mapped_column(String)  # create_job | cancel (never pin_envelope)
    target: Mapped[str] = mapped_column(String, default="signerd")
    payload_json: Mapped[str] = mapped_column(Text)  # canonical, no capability, no PIN
    status: Mapped[str] = mapped_column(String, default="pending", index=True)
    attempts: Mapped[int] = mapped_column(Integer, default=0)
    next_attempt_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    last_error_code: Mapped[str | None] = mapped_column(String, nullable=True)
    created_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow)
    updated_at: Mapped[datetime] = mapped_column(DateTime, default=utcnow, onupdate=utcnow)
    dispatched_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)
    acknowledged_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)


class SigningCapabilityConsumption(Base):
    """One-use artifact-capability replay/consumption record (salted hash only)."""

    __tablename__ = "signing_capability_consumptions"

    id: Mapped[str] = mapped_column(String, primary_key=True, default=new_id)
    capability_hash: Mapped[str] = mapped_column(String, unique=True)  # salted, one-way
    job_id: Mapped[str] = mapped_column(String, index=True)
    artifact_id: Mapped[str] = mapped_column(String)
    method: Mapped[str] = mapped_column(String)  # GET | PUT
    issued_at: Mapped[datetime] = mapped_column(DateTime)
    expires_at: Mapped[datetime] = mapped_column(DateTime)
    consumed_at: Mapped[datetime | None] = mapped_column(DateTime, nullable=True)


# --- append-only / immutability triggers (shared by create_all and Alembic) ---
#
# Each trigger is attached to its owning table's `after_create` (sqlite only) so
# partial create_all (legacy-only baseline) never emits DDL for absent tables.

_AUDIT_APPEND_ONLY = (
    """CREATE TRIGGER IF NOT EXISTS signing_audit_events_no_update
    BEFORE UPDATE ON signing_audit_events
    BEGIN SELECT RAISE(ABORT, 'signing_audit_events is append-only'); END""",
    """CREATE TRIGGER IF NOT EXISTS signing_audit_events_no_delete
    BEFORE DELETE ON signing_audit_events
    BEGIN SELECT RAISE(ABORT, 'signing_audit_events is append-only'); END""",
)

# Request events are immutable once written (no UPDATE) but remain deletable so a
# departing user's non-retained events can be erased. Only the audit log is fully
# delete-protected (§9.1).
_REQUEST_EVENTS_APPEND_ONLY = (
    """CREATE TRIGGER IF NOT EXISTS signing_request_events_no_update
    BEFORE UPDATE ON signing_request_events
    BEGIN SELECT RAISE(ABORT, 'signing_request_events rows are immutable'); END""",
)

_REQUEST_MANIFEST_IMMUTABLE = (
    """CREATE TRIGGER IF NOT EXISTS signing_requests_manifest_immutable
    BEFORE UPDATE ON signing_requests
    FOR EACH ROW
    WHEN OLD.state NOT IN ('UPLOADED','VALIDATED','AWAITING_OWNER_CONFIRMATION')
      AND (
        NEW.owner_id <> OLD.owner_id
        OR NEW.artifact_id <> OLD.artifact_id
        OR NEW.certificate_id <> OLD.certificate_id
        OR NEW.profile <> OLD.profile
        OR NEW.algorithm <> OLD.algorithm
        OR NEW.policy_version <> OLD.policy_version
        OR NEW.input_sha256 <> OLD.input_sha256
        OR NEW.input_byte_count <> OLD.input_byte_count
        OR NEW.certificate_fingerprint_sha256 <> OLD.certificate_fingerprint_sha256
      )
    BEGIN SELECT RAISE(ABORT, 'signing request manifest is immutable after QUEUED'); END""",
)

# Ordered list consumed by the Alembic signing migration.
SIGNING_TRIGGER_SQL: tuple[str, ...] = (
    *_AUDIT_APPEND_ONLY,
    *_REQUEST_EVENTS_APPEND_ONLY,
    *_REQUEST_MANIFEST_IMMUTABLE,
)

for _table, _statements in (
    (SigningAuditEvent, _AUDIT_APPEND_ONLY),
    (SigningRequestEvent, _REQUEST_EVENTS_APPEND_ONLY),
    (SigningRequest, _REQUEST_MANIFEST_IMMUTABLE),
):
    for _sql in _statements:
        event.listen(
            _table.__table__,
            "after_create",
            DDL(_sql).execute_if(dialect="sqlite"),
        )
