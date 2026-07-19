"""signing subsystem

Creates the signing (e-imza) tables, the active-assignment partial unique index,
and the append-only / manifest-immutability triggers. Document bytes never enter
SQLite; no column stores a PIN, PIN ciphertext, password, or artifact capability.

Revision ID: 0002
Revises: 0001
Create Date: 2026-07-19
"""

from __future__ import annotations

from alembic import op

from app.models import SIGNING_TABLE_NAMES, SIGNING_TRIGGER_SQL, Base

revision = "0002"
down_revision = "0001"
branch_labels = None
depends_on = None


def _signing_tables():
    return [Base.metadata.tables[name] for name in SIGNING_TABLE_NAMES]


def upgrade() -> None:
    bind = op.get_bind()
    # Table `after_create` listeners emit the append-only/immutability triggers
    # and the partial unique index as each signing table is created.
    Base.metadata.create_all(bind=bind, tables=_signing_tables())


def downgrade() -> None:
    bind = op.get_bind()
    for statement in (
        "DROP TRIGGER IF EXISTS signing_requests_manifest_immutable",
        "DROP TRIGGER IF EXISTS signing_request_events_no_update",
        "DROP TRIGGER IF EXISTS signing_audit_events_no_delete",
        "DROP TRIGGER IF EXISTS signing_audit_events_no_update",
    ):
        op.execute(statement)
    Base.metadata.drop_all(bind=bind, tables=list(reversed(_signing_tables())))


_ = SIGNING_TRIGGER_SQL  # triggers are emitted via table after_create listeners
