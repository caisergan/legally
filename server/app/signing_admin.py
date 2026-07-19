"""Local signing administration CLI (plan §5.4).

    python -m app.signing_admin sync-certificates
    python -m app.signing_admin assign --certificate <id> --user <id>
    python -m app.signing_admin revoke-assignment --certificate <id> --user <id>

Assignment enforces one active owner per certificate (v1) and audits every
mutation. Runs locally against the FastAPI database and, for sync, the signer.
"""

from __future__ import annotations

import argparse
import asyncio
import sys

from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from . import signing_audit, signing_workflow
from .db import SessionLocal
from .models import (
    SigningCertificate,
    SigningCertificateAssignment,
    User,
    utcnow,
)


class AdminError(Exception):
    pass


async def sync_certificates(db: AsyncSession) -> dict[str, int]:
    from .config import settings
    from .signing_client import build_signer_client

    client = build_signer_client(settings)
    try:
        summary = await signing_workflow.sync_certificates(db, client)
    finally:
        await client.aclose()
    await signing_audit.record(
        db, "certificate.sync", actor_type="admin", detail=summary
    )
    await db.commit()
    return summary


async def assign(db: AsyncSession, certificate_id: str, user_id: int) -> None:
    certificate = await db.get(SigningCertificate, certificate_id)
    if certificate is None:
        raise AdminError(f"certificate {certificate_id} not found")
    if await db.get(User, user_id) is None:
        raise AdminError(f"user {user_id} not found")

    active = await db.scalar(
        select(SigningCertificateAssignment).where(
            SigningCertificateAssignment.certificate_id == certificate_id,
            SigningCertificateAssignment.active == True,  # noqa: E712
        )
    )
    if active is not None:
        if active.user_id == user_id:
            return  # idempotent
        raise AdminError(
            f"certificate {certificate_id} already actively assigned to user {active.user_id}"
        )

    db.add(
        SigningCertificateAssignment(
            certificate_id=certificate_id,
            user_id=user_id,
            active=True,
            assigned_by=None,
        )
    )
    await signing_audit.record(
        db,
        "certificate.assign",
        actor_type="admin",
        certificate_id=certificate_id,
        detail={"user_id": user_id},
    )
    await db.commit()


async def revoke_assignment(db: AsyncSession, certificate_id: str, user_id: int) -> None:
    active = await db.scalar(
        select(SigningCertificateAssignment).where(
            SigningCertificateAssignment.certificate_id == certificate_id,
            SigningCertificateAssignment.user_id == user_id,
            SigningCertificateAssignment.active == True,  # noqa: E712
        )
    )
    if active is None:
        raise AdminError(
            f"no active assignment of certificate {certificate_id} to user {user_id}"
        )
    active.active = False
    active.revoked_at = utcnow()
    await signing_audit.record(
        db,
        "certificate.revoke_assignment",
        actor_type="admin",
        certificate_id=certificate_id,
        detail={"user_id": user_id},
    )
    await db.commit()


async def _run(args: argparse.Namespace) -> int:
    async with SessionLocal() as db:
        if args.command == "sync-certificates":
            summary = await sync_certificates(db)
            print(f"sync: {summary}")
        elif args.command == "assign":
            await assign(db, args.certificate, args.user)
            print(f"assigned {args.certificate} -> user {args.user}")
        elif args.command == "revoke-assignment":
            await revoke_assignment(db, args.certificate, args.user)
            print(f"revoked {args.certificate} from user {args.user}")
    return 0


def _parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="app.signing_admin")
    sub = parser.add_subparsers(dest="command", required=True)
    sub.add_parser("sync-certificates")
    for name in ("assign", "revoke-assignment"):
        cmd = sub.add_parser(name)
        cmd.add_argument("--certificate", required=True)
        cmd.add_argument("--user", required=True, type=int)
    return parser


def main(argv: list[str] | None = None) -> int:
    args = _parser().parse_args(argv)
    try:
        return asyncio.run(_run(args))
    except AdminError as error:
        print(f"error: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    raise SystemExit(main())
