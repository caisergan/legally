"""legacy schema baseline

Baseline of the pre-signing application schema. Fresh databases run this to
create the legacy tables; already-populated databases are stamped at this
revision (``alembic stamp 0001``) before ``upgrade head`` applies signing.

Revision ID: 0001
Revises:
Create Date: 2026-07-19
"""

from __future__ import annotations

from alembic import op

from app.models import LEGACY_TABLE_NAMES, Base

revision = "0001"
down_revision = None
branch_labels = None
depends_on = None


def _legacy_tables():
    return [Base.metadata.tables[name] for name in LEGACY_TABLE_NAMES]


def upgrade() -> None:
    Base.metadata.create_all(bind=op.get_bind(), tables=_legacy_tables())


def downgrade() -> None:
    Base.metadata.drop_all(bind=op.get_bind(), tables=list(reversed(_legacy_tables())))
