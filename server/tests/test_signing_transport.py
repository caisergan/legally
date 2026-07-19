"""Transport identity: command authentication and OS peer-credential checks."""

from __future__ import annotations

import os
import socket

import pytest
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

from app.signing_client import (
    CommandAuthError,
    CommandSigner,
    CommandVerifier,
    NonceCache,
    PeerCredentials,
    PeerRejected,
    UnsupportedPeerCredentials,
    authorize_peer,
    parse_pinned_public_keys,
    peer_credentials,
)


def _pair() -> tuple[CommandSigner, CommandVerifier, Ed25519PrivateKey]:
    key = Ed25519PrivateKey.generate()
    signer = CommandSigner(key, "cmd-1")
    verifier = CommandVerifier({"cmd-1": key.public_key()}, skew_seconds=30)
    return signer, verifier, key


def test_command_auth_roundtrip_and_body_binding():
    signer, verifier, _ = _pair()
    headers = signer.sign("POST", "/v1/jobs", "", b'{"a":1}')
    assert verifier.verify("POST", "/v1/jobs", "", b'{"a":1}', headers) == "cmd-1"
    with pytest.raises(CommandAuthError):
        verifier.verify("POST", "/v1/jobs", "", b'{"a":2}', headers)


def test_command_auth_rejects_wrong_method_and_path():
    signer, verifier, _ = _pair()
    headers = signer.sign("POST", "/v1/jobs", "", b"{}")
    with pytest.raises(CommandAuthError):
        verifier.verify("GET", "/v1/jobs", "", b"{}", headers)
    with pytest.raises(CommandAuthError):
        verifier.verify("POST", "/v1/jobs/x", "", b"{}", headers)


def test_command_auth_unpinned_key_rejected():
    signer, _, _ = _pair()
    other = CommandVerifier({"someone-else": Ed25519PrivateKey.generate().public_key()}, skew_seconds=30)
    headers = signer.sign("POST", "/v1/jobs", "", b"{}")
    with pytest.raises(CommandAuthError):
        other.verify("POST", "/v1/jobs", "", b"{}", headers)


def test_command_auth_clock_skew_rejected():
    signer, verifier, _ = _pair()
    headers = signer.sign("POST", "/v1/jobs", "", b"{}", now=1000)
    with pytest.raises(CommandAuthError):
        verifier.verify("POST", "/v1/jobs", "", b"{}", headers, now=1000 + 61)


def test_command_auth_nonce_replay_rejected():
    signer = CommandSigner(Ed25519PrivateKey.generate(), "cmd-1")
    key = Ed25519PrivateKey.generate()
    signer = CommandSigner(key, "cmd-1")
    verifier = CommandVerifier(
        {"cmd-1": key.public_key()}, skew_seconds=30, nonce_cache=NonceCache(30)
    )
    headers = signer.sign("POST", "/v1/jobs", "", b"{}", now=1000)
    assert verifier.verify("POST", "/v1/jobs", "", b"{}", headers, now=1000) == "cmd-1"
    with pytest.raises(CommandAuthError):
        verifier.verify("POST", "/v1/jobs", "", b"{}", headers, now=1000)


def test_authorize_peer_uid_and_gid():
    authorize_peer(PeerCredentials(1, 4242, 77), allowed_uids={4242}, allowed_gids={77})
    with pytest.raises(PeerRejected):
        authorize_peer(PeerCredentials(1, 9999, 77), allowed_uids={4242})
    with pytest.raises(PeerRejected):
        authorize_peer(
            PeerCredentials(1, 4242, 55), allowed_uids={4242}, allowed_gids={77}
        )


def test_peer_credentials_over_socketpair():
    left, right = socket.socketpair(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        creds = peer_credentials(left)
    except UnsupportedPeerCredentials:
        pytest.skip("peer credentials unsupported on this platform")
    finally:
        left.close()
        right.close()
    assert creds.uid == os.getuid()
    # An unauthorized peer UID is rejected against the service allowlist.
    with pytest.raises(PeerRejected):
        authorize_peer(creds, allowed_uids={creds.uid + 1})


async def test_signer_client_reports_unavailable(tmp_path):
    from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey

    from app.signing_client import CommandSigner, SignerClient, SignerUnavailable

    signer = CommandSigner(Ed25519PrivateKey.generate(), "cmd-1")
    client = SignerClient(
        str(tmp_path / "missing.sock"), signer, timeout_seconds=1.0
    )
    try:
        with pytest.raises(SignerUnavailable):
            await client.get_credentials()
    finally:
        await client.aclose()


def test_parse_pinned_public_keys():
    key = Ed25519PrivateKey.generate()
    raw = key.public_key().public_bytes_raw().hex()
    pinned = parse_pinned_public_keys(f"k1:{raw}")
    assert "k1" in pinned
    with pytest.raises(ValueError):
        parse_pinned_public_keys("bad-entry-without-colon")
