"""Admin assignment/revocation, one-owner enforcement, and append-only audit."""

from __future__ import annotations

import pytest
from sqlalchemy import func, select, text

from app import signing_admin
from app.models import SigningAuditEvent, SigningCertificateAssignment

from .factories import make_certificate, make_user


async def test_assign_enforces_single_active_owner(db):
    cert = await make_certificate(db)
    owner = await make_user(db, "owner@example.com")
    other = await make_user(db, "other@example.com")
    await db.commit()

    await signing_admin.assign(db, cert.id, owner.id)
    # Idempotent for the same owner.
    await signing_admin.assign(db, cert.id, owner.id)
    # A second owner is refused while an active assignment exists.
    with pytest.raises(signing_admin.AdminError):
        await signing_admin.assign(db, cert.id, other.id)

    # After revocation the certificate can be reassigned.
    await signing_admin.revoke_assignment(db, cert.id, owner.id)
    await signing_admin.assign(db, cert.id, other.id)

    active = await db.scalar(
        select(func.count())
        .select_from(SigningCertificateAssignment)
        .where(SigningCertificateAssignment.active == True)  # noqa: E712
    )
    assert active == 1


async def test_assign_and_revoke_write_audit(db):
    cert = await make_certificate(db)
    owner = await make_user(db, "owner@example.com")
    await db.commit()
    await signing_admin.assign(db, cert.id, owner.id)
    await signing_admin.revoke_assignment(db, cert.id, owner.id)

    actions = (
        await db.scalars(select(SigningAuditEvent.action).order_by(SigningAuditEvent.id))
    ).all()
    assert "certificate.assign" in actions
    assert "certificate.revoke_assignment" in actions


async def test_audit_is_append_only(db):
    cert = await make_certificate(db)
    owner = await make_user(db, "owner@example.com")
    await db.commit()
    await signing_admin.assign(db, cert.id, owner.id)

    audit_id = await db.scalar(select(SigningAuditEvent.id).limit(1))
    with pytest.raises(Exception):
        await db.execute(
            text("UPDATE signing_audit_events SET action='tampered' WHERE id=:i"),
            {"i": audit_id},
        )
        await db.flush()
    await db.rollback()
    with pytest.raises(Exception):
        await db.execute(
            text("DELETE FROM signing_audit_events WHERE id=:i"), {"i": audit_id}
        )
        await db.flush()


async def test_assign_unknown_certificate_or_user(db):
    owner = await make_user(db)
    await db.commit()
    with pytest.raises(signing_admin.AdminError):
        await signing_admin.assign(db, "missing-cert", owner.id)
