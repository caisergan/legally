"""Signer Unix-socket client and transport identity primitives (plan §8.2).

Single-host trust rests on three independent checks, not filesystem reachability:

* OS peer-credential validation (`SO_PEERCRED` / `LOCAL_PEERCRED`) against an
  explicit UID allowlist, plus the dedicated `yargi-signing` group;
* asymmetric command authentication — FastAPI signs signer commands with an
  Ed25519 key that signerd pins; signerd reciprocally signs broker calls that
  FastAPI pins here — each signature binding key id, method, canonical
  path/query, body SHA-256, timestamp, and nonce;
* bounded clock skew and one-use nonce replay protection with constant-time
  verification.
"""

from __future__ import annotations

import base64
import hashlib
import secrets
import socket
import struct
import time
from dataclasses import dataclass

import httpx
from cryptography.exceptions import InvalidSignature
from cryptography.hazmat.primitives.asymmetric.ed25519 import (
    Ed25519PrivateKey,
    Ed25519PublicKey,
)

SIG_KEY_ID = "X-Sig-KeyId"
SIG_TIMESTAMP = "X-Sig-Timestamp"
SIG_NONCE = "X-Sig-Nonce"
SIG_VALUE = "X-Sig-Value"


class TransportIdentityError(Exception):
    pass


class PeerRejected(TransportIdentityError):
    pass


class UnsupportedPeerCredentials(TransportIdentityError):
    pass


class CommandAuthError(TransportIdentityError):
    pass


class SignerUnavailable(Exception):
    """Signerd could not be reached or the delivery outcome is ambiguous."""


class SignerUnreached(SignerUnavailable):
    """The command provably did not reach signerd (connect refused/no socket)."""


class SignerError(Exception):
    """Signerd returned a safe error response."""

    def __init__(self, status_code: int, code: str, detail: str) -> None:
        super().__init__(f"{status_code} {code}: {detail}")
        self.status_code = status_code
        self.code = code
        self.detail = detail


# --- OS peer credentials -----------------------------------------------------


@dataclass(frozen=True)
class PeerCredentials:
    pid: int | None
    uid: int
    gid: int


def peer_credentials(sock: socket.socket) -> PeerCredentials:
    so_peercred = getattr(socket, "SO_PEERCRED", None)
    if so_peercred is not None:  # Linux
        raw = sock.getsockopt(socket.SOL_SOCKET, so_peercred, struct.calcsize("3i"))
        pid, uid, gid = struct.unpack("3i", raw)
        return PeerCredentials(pid=pid, uid=uid, gid=gid)
    # Darwin / BSD: SOL_LOCAL=0, LOCAL_PEERCRED=1 → struct xucred.
    try:
        raw = sock.getsockopt(0, 1, struct.calcsize("2I"))
        _version, uid = struct.unpack("2I", raw)
        return PeerCredentials(pid=None, uid=uid, gid=-1)
    except OSError as error:
        raise UnsupportedPeerCredentials("peer credentials unavailable") from error


def authorize_peer(
    creds: PeerCredentials,
    *,
    allowed_uids: set[int],
    allowed_gids: set[int] | None = None,
) -> None:
    if creds.uid not in allowed_uids:
        raise PeerRejected(f"peer uid {creds.uid} not in allowlist")
    if allowed_gids and creds.gid not in allowed_gids and creds.gid != -1:
        raise PeerRejected(f"peer gid {creds.gid} not in allowlist")


# --- command authentication --------------------------------------------------


def _canonical_request(
    method: str, path: str, query: str, body: bytes, timestamp: str, nonce: str
) -> bytes:
    body_sha256 = hashlib.sha256(body).hexdigest()
    return "\n".join(
        [method.upper(), path, query or "", body_sha256, timestamp, nonce]
    ).encode("utf-8")


class CommandSigner:
    def __init__(self, private_key: Ed25519PrivateKey, kid: str) -> None:
        self._key = private_key
        self._kid = kid

    def sign(
        self, method: str, path: str, query: str, body: bytes, *, now: int | None = None
    ) -> dict[str, str]:
        timestamp = str(int(now if now is not None else time.time()))
        nonce = secrets.token_hex(16)
        message = _canonical_request(method, path, query, body, timestamp, nonce)
        signature = self._key.sign(message)
        return {
            SIG_KEY_ID: self._kid,
            SIG_TIMESTAMP: timestamp,
            SIG_NONCE: nonce,
            SIG_VALUE: base64.b64encode(signature).decode("ascii"),
        }


class NonceCache:
    """In-memory one-use nonce store bounded by the skew window."""

    def __init__(self, ttl_seconds: int) -> None:
        self._ttl = ttl_seconds
        self._seen: dict[str, int] = {}

    def check_and_store(self, nonce: str, now: int) -> None:
        self._evict(now)
        if nonce in self._seen:
            raise CommandAuthError("nonce replay")
        self._seen[nonce] = now

    def _evict(self, now: int) -> None:
        cutoff = now - self._ttl
        for key in [k for k, seen in self._seen.items() if seen < cutoff]:
            del self._seen[key]


class CommandVerifier:
    def __init__(
        self,
        pinned_keys: dict[str, Ed25519PublicKey],
        *,
        skew_seconds: int,
        nonce_cache: NonceCache | None = None,
    ) -> None:
        self._pinned = pinned_keys
        self._skew = skew_seconds
        self._nonces = nonce_cache or NonceCache(skew_seconds)

    def verify(
        self,
        method: str,
        path: str,
        query: str,
        body: bytes,
        headers: dict[str, str],
        *,
        now: int | None = None,
    ) -> str:
        kid = headers.get(SIG_KEY_ID, "")
        timestamp = headers.get(SIG_TIMESTAMP, "")
        nonce = headers.get(SIG_NONCE, "")
        value = headers.get(SIG_VALUE, "")
        public_key = self._pinned.get(kid)
        if public_key is None:
            raise CommandAuthError("unpinned command key id")
        try:
            ts = int(timestamp)
        except ValueError as error:
            raise CommandAuthError("bad timestamp") from error
        current = int(now if now is not None else time.time())
        if abs(current - ts) > self._skew:
            raise CommandAuthError("timestamp outside skew window")
        try:
            signature = base64.b64decode(value)
        except (ValueError, TypeError) as error:
            raise CommandAuthError("bad signature encoding") from error
        message = _canonical_request(method, path, query, body, timestamp, nonce)
        try:
            public_key.verify(signature, message)
        except InvalidSignature as error:
            raise CommandAuthError("bad command signature") from error
        self._nonces.check_and_store(nonce, current)
        return kid


# --- signer client -----------------------------------------------------------


class SignerClient:
    """Deadline-bounded signer client over a private Unix socket."""

    def __init__(
        self,
        uds_path: str,
        signer: CommandSigner,
        *,
        timeout_seconds: float,
        base_url: str = "http://signerd",
    ) -> None:
        self._signer = signer
        self._base_url = base_url
        self._client = httpx.AsyncClient(
            transport=httpx.AsyncHTTPTransport(uds=str(uds_path)),
            timeout=timeout_seconds,
        )

    async def aclose(self) -> None:
        await self._client.aclose()

    async def get_credentials(self) -> dict:
        return await self._request("GET", "/v1/credentials")

    async def create_job(self, command_id: str, payload: dict) -> dict:
        return await self._request(
            "POST",
            "/v1/jobs",
            json_body=payload,
            headers={"Idempotency-Key": command_id},
        )

    async def get_command(self, command_id: str) -> dict:
        return await self._request("GET", f"/v1/commands/{command_id}")

    async def get_job(self, job_id: str) -> dict:
        return await self._request("GET", f"/v1/jobs/{job_id}")

    async def get_job_events(self, job_id: str, after: int) -> dict:
        return await self._request(
            "GET", f"/v1/jobs/{job_id}/events", query=f"after={int(after)}"
        )

    async def cancel_job(self, job_id: str) -> dict:
        return await self._request("POST", f"/v1/jobs/{job_id}/cancel")

    async def get_pin_challenge(self, job_id: str) -> dict:
        return await self._request("GET", f"/v1/jobs/{job_id}/pin-challenge")

    async def authorize(self, job_id: str, challenge_id: str, pin_jwe: str) -> dict:
        """Synchronous PIN-envelope relay. The envelope is never logged or stored.

        Distinguishes provably-unreached (raise ``SignerUnreached``) from an
        ambiguous outcome (return ``delivery_unknown``) so the caller can apply
        the §8.1 reconciliation rules without ever replaying a second envelope.
        """
        import json as _json

        path = f"/v1/jobs/{job_id}/authorize"
        body = _json.dumps({"challenge_id": challenge_id, "pin_jwe": pin_jwe}).encode()
        headers = {**self._signer.sign("POST", path, "", body), "Content-Type": "application/json"}
        try:
            response = await self._client.request(
                "POST", f"{self._base_url}{path}", content=body, headers=headers
            )
        except (httpx.ConnectError, httpx.ConnectTimeout, FileNotFoundError) as error:
            raise SignerUnreached(str(error)) from error
        except httpx.TransportError:
            # Sent but no clear response: ambiguous, must not resend.
            return {"authorization_status": "delivery_unknown"}
        finally:
            del body  # drop the envelope reference promptly
        if response.status_code >= 400:
            raise _safe_signer_error(response)
        return response.json()

    async def _request(
        self,
        method: str,
        path: str,
        *,
        query: str = "",
        json_body: dict | None = None,
        headers: dict[str, str] | None = None,
    ) -> dict:
        import json as _json

        body = b"" if json_body is None else _json.dumps(json_body).encode("utf-8")
        sig_headers = self._signer.sign(method, path, query, body)
        request_headers = {**(headers or {}), **sig_headers}
        if json_body is not None:
            request_headers["Content-Type"] = "application/json"
        url = f"{self._base_url}{path}" + (f"?{query}" if query else "")
        try:
            response = await self._client.request(
                method, url, content=body, headers=request_headers
            )
        except (httpx.ConnectError, httpx.ConnectTimeout, FileNotFoundError) as error:
            raise SignerUnreached(str(error)) from error
        except httpx.TransportError as error:
            # Ambiguous: the command may or may not have been applied.
            raise SignerUnavailable(str(error)) from error
        if response.status_code >= 400:
            raise _safe_signer_error(response)
        return response.json()


def _safe_signer_error(response: httpx.Response) -> SignerError:
    code, detail = "signer_error", "signer returned an error"
    try:
        body = response.json()
        code = str(body.get("code", code))
        detail = str(body.get("detail", detail))
    except ValueError:
        pass
    return SignerError(response.status_code, code, detail)


def build_signer_client(settings) -> SignerClient:
    """Construct a signer client from application settings."""
    if settings.signing_command_key_path is None:
        raise TransportIdentityError("signing_command_key_path is not configured")
    pem = settings.signing_command_key_path.read_bytes()
    signer = CommandSigner(load_ed25519_private_key(pem), settings.signing_command_key_id)
    return SignerClient(
        str(settings.signing_signer_socket),
        signer,
        timeout_seconds=settings.signing_request_timeout_seconds,
    )


def load_ed25519_private_key(pem_bytes: bytes) -> Ed25519PrivateKey:
    from cryptography.hazmat.primitives.serialization import load_pem_private_key

    key = load_pem_private_key(pem_bytes, password=None)
    if not isinstance(key, Ed25519PrivateKey):
        raise ValueError("expected an Ed25519 private key")
    return key


def parse_pinned_public_keys(spec: str) -> dict[str, Ed25519PublicKey]:
    """Parse "kid:hex,kid:hex" raw Ed25519 public keys from configuration."""
    pinned: dict[str, Ed25519PublicKey] = {}
    for entry in filter(None, (part.strip() for part in spec.split(","))):
        kid, _, hex_key = entry.partition(":")
        if not kid or not hex_key:
            raise ValueError(f"invalid pinned key entry: {entry!r}")
        pinned[kid] = Ed25519PublicKey.from_public_bytes(bytes.fromhex(hex_key))
    return pinned
