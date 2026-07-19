"""Combined live end-to-end over the real transport: the FastAPI SignerClient
drives a running signerd (command-auth on) that signs with a real SoftHSM token,
across the full chain — enroll, put_input, create_job, PIN relay, single C_Sign,
and output egress. Output stays QUARANTINED (self-signed cert), proving the
vertical, not a qualified signature.

Prerequisites: a provisioned SoftHSM token (scripts/provision-softhsm-test-token.sh)
exported as SOFTHSM2_CONF, SCRATCH_MODULE, SCRATCH_CERT_DER, SCRATCH_PIN,
SCRATCH_CKAID, SCRATCH_SLOT; a Go toolchain with cgo; the app venv.
"""

from __future__ import annotations

import asyncio
import hashlib
import json
import os
import secrets
import subprocess
import sys
import time
from datetime import datetime, timedelta, timezone
from pathlib import Path

REPO = Path(__file__).resolve().parents[2]
SIGNING_SERVICE = REPO / "signing-service"
SERVER = REPO / "server"
sys.path.insert(0, str(SERVER))

from cryptography.hazmat.primitives import serialization  # noqa: E402
from cryptography.hazmat.primitives.asymmetric import ec, ed25519  # noqa: E402
from joserfc import jwe  # noqa: E402
from joserfc.jwk import ECKey  # noqa: E402

from app.signing_client import CommandSigner, SignerClient, load_ed25519_private_key  # noqa: E402


def env(name: str) -> str:
    value = os.environ.get(name, "")
    if not value:
        sys.exit(f"missing required env {name}")
    return value


def iso(when: datetime) -> str:
    return when.astimezone(timezone.utc).isoformat().replace("+00:00", "Z")


def run_go(args: list[str], extra_env: dict[str, str]) -> str:
    proc = subprocess.run(
        ["go", "run", "-tags", "pkcs11", *args],
        cwd=str(SIGNING_SERVICE),
        env={**os.environ, **extra_env},
        capture_output=True,
        text=True,
    )
    if proc.returncode != 0:
        sys.exit(f"go run {args} failed:\n{proc.stdout}\n{proc.stderr}")
    return proc.stdout.strip()


async def main() -> None:
    conf = env("SOFTHSM2_CONF")
    module = env("SCRATCH_MODULE")
    cert_der = Path(env("SCRATCH_CERT_DER")).read_bytes()
    pin = env("SCRATCH_PIN")
    ckaid = env("SCRATCH_CKAID")
    slot = env("SCRATCH_SLOT")
    fingerprint = hashlib.sha256(cert_der).hexdigest()

    work = Path(subprocess.check_output(["mktemp", "-d", "-t", "yc-e2e"]).decode().strip())
    db_path = work / "signerd.db"
    state_dir = work / "state"
    state_dir.mkdir()

    module_real = os.path.realpath(module)
    module_sha = hashlib.sha256(Path(module_real).read_bytes()).hexdigest()
    allowlist = work / "modules.yaml"
    allowlist.write_text(
        "pkcs11_modules:\n"
        f"  - alias: softhsm-test\n    path: {module_real}\n    sha256: {module_sha}\n    modes: [test]\n"
    )

    # Signerd challenge key (ES256) and the FastAPI command key (Ed25519).
    challenge_key = ec.generate_private_key(ec.SECP256R1())
    challenge_pem = work / "challenge.pem"
    challenge_pem.write_bytes(challenge_key.private_bytes(
        serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
    command_key = ed25519.Ed25519PrivateKey.generate()
    command_pem = work / "command.pem"
    command_pem.write_bytes(command_key.private_bytes(
        serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8, serialization.NoEncryption()))
    command_pub_hex = command_key.public_key().public_bytes(
        serialization.Encoding.Raw, serialization.PublicFormat.Raw).hex()
    supervisor = work / "supervisor.key"
    supervisor.write_text(secrets.token_hex(32))

    signer_env = {
        "SIGNERD_DATABASE": str(db_path),
        "SIGNERD_CONFIG": str(allowlist),
        "SIGNERD_MODE": "softhsm",
        "SOFTHSM2_CONF": conf,
    }

    enroll_out = run_go(
        ["./cmd/yargi-signerctl", "credentials", "enroll", "--module", "softhsm-test",
         "--slot", slot, "--certificate-sha256", fingerprint, "--key-id", ckaid],
        signer_env,
    )
    print(f"[enroll] {enroll_out}")
    credential_id = enroll_out.split()[2]

    socket_dir = Path(subprocess.check_output(["mktemp", "-d", "-t", "ycs"]).decode().strip())
    socket_path = socket_dir / "s.sock"
    daemon_env = {
        **os.environ,
        "SIGNERD_ENABLED": "true",
        "SIGNERD_MODE": "softhsm",
        "SIGNERD_ENVIRONMENT": "development",
        "SIGNERD_TRANSPORT": "unix",
        "SIGNERD_SOCKET": str(socket_path),
        "SIGNERD_STATE_DIR": str(state_dir),
        "SIGNERD_DATABASE": str(db_path),
        "SIGNERD_CONFIG": str(allowlist),
        "SIGNERD_MODULE_ALIAS": "softhsm-test",
        "SIGNERD_CHALLENGE_KEY": str(challenge_pem),
        "SIGNERD_CHALLENGE_KEY_ID": "challenge-1",
        "SIGNERD_SUPERVISOR_KEY": str(supervisor),
        "SIGNERD_COMMAND_PUBKEYS": f"fastapi-cmd-1:{command_pub_hex}",
        "SIGNERD_ALLOWED_UIDS": str(os.getuid()),
        "SOFTHSM2_CONF": conf,
    }
    log_path = work / "daemon.log"
    daemon = subprocess.Popen(
        ["go", "run", "-tags", "pkcs11", "./cmd/yargi-signerd"],
        cwd=str(SIGNING_SERVICE), env=daemon_env,
        stdout=open(log_path, "w"), stderr=subprocess.STDOUT, text=True,
    )
    try:
        await _drive(socket_path, command_pem, credential_id, fingerprint, pin, daemon)
    except SystemExit:
        print("--- signerd log ---")
        print(log_path.read_text()[-2000:])
        raise
    finally:
        daemon.terminate()
        try:
            daemon.wait(timeout=5)
        except subprocess.TimeoutExpired:
            daemon.kill()


async def _drive(socket_path, command_pem, credential_id, fingerprint, pin, daemon) -> None:
    deadline = time.time() + 60
    while not socket_path.exists():
        if daemon.poll() is not None:
            sys.exit("signerd exited early")
        if time.time() > deadline:
            sys.exit("signerd socket did not appear")
        time.sleep(0.2)

    signer = CommandSigner(load_ed25519_private_key(command_pem.read_bytes()), "fastapi-cmd-1")
    client = SignerClient(str(socket_path), signer, timeout_seconds=15)
    try:
        creds = await client.get_credentials()
        assert any(c["id"] == credential_id for c in creds["items"]), "enrolled credential not advertised"
        print(f"[credentials] signerd advertises {len(creds['items'])} credential(s)")

        pdf = (REPO / "signing-service" / "testdata" / "pdf" / "accepted_minimal.pdf").read_bytes()
        input_id, output_id = "in-" + secrets.token_hex(6), "out-" + secrets.token_hex(6)
        input_sha = hashlib.sha256(pdf).hexdigest()
        await client.put_input(input_id, pdf, input_sha)
        print(f"[put_input] ingested {len(pdf)} bytes")

        now = datetime.now(timezone.utc)
        command_id, job_id = "cmd-" + secrets.token_hex(8), "job-" + secrets.token_hex(8)
        payload = {
            "command_id": command_id, "job_id": job_id, "request_id": "req-" + secrets.token_hex(6),
            "expected_request_version": 1, "credential_id": credential_id,
            "certificate_fingerprint_sha256": fingerprint,
            "policy_version": "pades-b-b-rsa-sha256-v1",
            "profile": "PAdES_BASELINE_B_B", "algorithm": "RSA_PKCS1_SHA256",
            "input": {"artifact_id": input_id, "byte_count": len(pdf), "sha256": input_sha},
            "output": {"artifact_id": output_id, "max_byte_count": 5 << 20},
            "approval": {"approval_id": "apr-" + secrets.token_hex(6),
                         "approved_at": iso(now), "expires_at": iso(now + timedelta(minutes=5))},
            "nonce": secrets.token_hex(16), "expires_at": iso(now + timedelta(hours=1)),
        }
        created = await client.create_job(command_id, payload)
        print(f"[create_job] state={created['state']}")

        state = await _wait_state(client, job_id, {"PIN_REQUIRED"}, {"QUARANTINED"})
        print(f"[poll] reached {state}")

        challenge = await client.get_pin_challenge(job_id)
        envelope = jwe.encrypt_compact(
            {"alg": "ECDH-ES", "enc": "A256GCM", "kid": challenge["challenge_id"]},
            pin.encode(), ECKey.import_key(challenge["recipient_jwk"]),
        )
        result = await client.authorize(job_id, challenge["challenge_id"], envelope)
        print(f"[authorize] {result['authorization_status']}")

        final = await _wait_state(client, job_id, {"QUARANTINED"}, set())
        assert final == "QUARANTINED", f"unexpected terminal state {final}"

        output, out_sha = await client.get_output(job_id)
        assert output[:5] == b"%PDF-" and len(output) > 1000, "output is not a PDF"
        assert hashlib.sha256(output).hexdigest() == out_sha, "output hash mismatch"
        print(f"[get_output] {len(output)} byte signed PDF, sha256 verified")
        print("PASS: FastAPI client -> signerd -> SoftHSM -> output, QUARANTINED end-to-end")
    finally:
        await client.aclose()


async def _wait_state(client, job_id, want, unexpected, timeout=30) -> str:
    deadline = time.time() + timeout
    while time.time() < deadline:
        job = await client.get_job(job_id)
        state = job["state"]
        if state in want:
            return state
        if state in unexpected:
            sys.exit(f"job reached {state} before {want}")
        await asyncio.sleep(0.3)
    sys.exit(f"timeout waiting for {want}")


if __name__ == "__main__":
    asyncio.run(main())
