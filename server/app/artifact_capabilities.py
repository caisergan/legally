"""Internal artifact capability broker and one-use capability tokens (plan §8.3).

Capabilities are short-lived bearer secrets, scoped to method + artifact + job +
hash/size + nonce + expiry, minted just in time. They are never logged, never
journaled, never placed in the outbox, and stored only as a keyed one-way hash
for replay tracking. Document bytes move only through the streaming broker.
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
import secrets
import time
from collections.abc import AsyncIterator, Iterator
from dataclasses import dataclass

from sqlalchemy import select
from sqlalchemy.exc import IntegrityError
from sqlalchemy.ext.asyncio import AsyncSession

from .artifact_store import ArtifactStore
from .models import SigningArtifact, SigningCapabilityConsumption, utcnow

_TOKEN_PREFIX = "cap1"


class CapabilityError(Exception):
    pass


class CapabilityInvalid(CapabilityError):
    pass


class CapabilityExpired(CapabilityError):
    pass


class CapabilityReplay(CapabilityError):
    pass


class CapabilityMismatch(CapabilityError):
    pass


@dataclass(frozen=True)
class Capability:
    kid: str
    method: str
    artifact_id: str
    job_id: str
    sha256: str
    size: int  # exact byte_count for GET; max_byte_count for PUT
    nonce: str
    issued_at: int
    expires_at: int


def _b64u(raw: bytes) -> str:
    return base64.urlsafe_b64encode(raw).rstrip(b"=").decode("ascii")


def _b64u_decode(text: str) -> bytes:
    pad = "=" * (-len(text) % 4)
    return base64.urlsafe_b64decode(text + pad)


class CapabilityIssuer:
    """Mints and verifies HMAC-authenticated capability tokens (no DB)."""

    def __init__(self, secret: bytes, kid: str, ttl_seconds: int) -> None:
        if not secret:
            raise ValueError("capability secret required")
        self._secret = secret
        self._kid = kid
        self._ttl = ttl_seconds

    def mint(
        self,
        *,
        method: str,
        artifact_id: str,
        job_id: str,
        sha256: str,
        size: int,
        now: int | None = None,
    ) -> tuple[str, Capability]:
        issued = int(now if now is not None else time.time())
        cap = Capability(
            kid=self._kid,
            method=method,
            artifact_id=artifact_id,
            job_id=job_id,
            sha256=sha256,
            size=size,
            nonce=secrets.token_hex(16),
            issued_at=issued,
            expires_at=issued + self._ttl,
        )
        payload = _b64u(_canonical(cap))
        signature = _b64u(self._mac(payload.encode("ascii")))
        return f"{_TOKEN_PREFIX}.{payload}.{signature}", cap

    def parse(self, token: str, *, now: int | None = None) -> Capability:
        try:
            prefix, payload, signature = token.split(".")
        except ValueError as error:
            raise CapabilityInvalid("malformed capability") from error
        if prefix != _TOKEN_PREFIX:
            raise CapabilityInvalid("unknown capability version")
        expected = _b64u(self._mac(payload.encode("ascii")))
        if not hmac.compare_digest(expected, signature):
            raise CapabilityInvalid("bad capability signature")
        cap = _decode(_b64u_decode(payload))
        if cap.kid != self._kid:
            raise CapabilityInvalid("unknown capability key id")
        current = int(now if now is not None else time.time())
        if current >= cap.expires_at:
            raise CapabilityExpired("capability expired")
        return cap

    def token_hash(self, token: str) -> str:
        return hmac.new(self._secret, token.encode("ascii"), hashlib.sha256).hexdigest()

    def _mac(self, data: bytes) -> bytes:
        return hmac.new(self._secret, data, hashlib.sha256).digest()


def _canonical(cap: Capability) -> bytes:
    return json.dumps(
        {
            "v": 1,
            "kid": cap.kid,
            "m": cap.method,
            "aid": cap.artifact_id,
            "jid": cap.job_id,
            "sha": cap.sha256,
            "n": cap.size,
            "nonce": cap.nonce,
            "iat": cap.issued_at,
            "exp": cap.expires_at,
        },
        sort_keys=True,
        separators=(",", ":"),
    ).encode("utf-8")


def _decode(raw: bytes) -> Capability:
    try:
        data = json.loads(raw)
        return Capability(
            kid=str(data["kid"]),
            method=str(data["m"]),
            artifact_id=str(data["aid"]),
            job_id=str(data["jid"]),
            sha256=str(data["sha"]),
            size=int(data["n"]),
            nonce=str(data["nonce"]),
            issued_at=int(data["iat"]),
            expires_at=int(data["exp"]),
        )
    except (KeyError, ValueError, TypeError) as error:
        raise CapabilityInvalid("undecodable capability") from error


async def _consume_once(
    db: AsyncSession, issuer: CapabilityIssuer, token: str, cap: Capability
) -> None:
    """Atomically record one-use consumption; raise on replay."""
    row = SigningCapabilityConsumption(
        capability_hash=issuer.token_hash(token),
        job_id=cap.job_id,
        artifact_id=cap.artifact_id,
        method=cap.method,
        issued_at=_epoch_dt(cap.issued_at),
        expires_at=_epoch_dt(cap.expires_at),
        consumed_at=utcnow(),
    )
    db.add(row)
    try:
        await db.flush()
    except IntegrityError as error:
        await db.rollback()
        raise CapabilityReplay("capability already consumed") from error


class ArtifactBroker:
    """FastAPI-owned broker: streams bytes for verified one-use capabilities."""

    def __init__(self, store: ArtifactStore, issuer: CapabilityIssuer) -> None:
        self._store = store
        self._issuer = issuer

    def issuer(self) -> CapabilityIssuer:
        return self._issuer

    async def read(
        self, db: AsyncSession, token: str, *, now: int | None = None
    ) -> tuple[Capability, Iterator[bytes]]:
        cap = self._issuer.parse(token, now=now)
        if cap.method != "GET":
            raise CapabilityMismatch("capability method is not GET")
        artifact = await _artifact_for_job(db, cap, kind="input")
        if artifact.sha256 != cap.sha256 or artifact.byte_count != cap.size:
            raise CapabilityMismatch("capability does not match artifact")
        await _consume_once(db, self._issuer, token, cap)
        await db.commit()
        return cap, self._store.read_iter(artifact.storage_key)

    async def write(
        self,
        db: AsyncSession,
        token: str,
        chunks: AsyncIterator[bytes],
        *,
        now: int | None = None,
    ) -> tuple[Capability, str, int]:
        cap = self._issuer.parse(token, now=now)
        if cap.method != "PUT":
            raise CapabilityMismatch("capability method is not PUT")
        artifact = await _artifact_for_job(db, cap, kind="output")
        if artifact.storage_key:
            raise CapabilityReplay("output artifact already written")
        await _consume_once(db, self._issuer, token, cap)
        stored = await self._store.store_stream(chunks, max_bytes=cap.size)
        artifact.storage_key = stored.storage_key
        artifact.sha256 = stored.sha256
        artifact.byte_count = stored.byte_count
        artifact.preflight_status = "accepted"
        await db.commit()
        return cap, stored.sha256, stored.byte_count


async def _artifact_for_job(
    db: AsyncSession, cap: Capability, *, kind: str
) -> SigningArtifact:
    artifact = await db.get(SigningArtifact, cap.artifact_id)
    if artifact is None or artifact.deleted_at is not None or artifact.kind != kind:
        raise CapabilityMismatch("capability artifact not found")
    return artifact


def _epoch_dt(epoch: int):
    from datetime import datetime, timezone

    return datetime.fromtimestamp(epoch, tz=timezone.utc).replace(tzinfo=None)
