"""Signing policy constants and safe capability metadata.

These values mirror the Go signer's fixed policy (`internal/policy`,
`signer.FormatEngineVersion`) and must stay byte-identical across both systems.
"""

from __future__ import annotations

from .config import Settings

PROFILE = "PAdES_BASELINE_B_B"
ALGORITHM = "RSA_PKCS1_SHA256"
POLICY_VERSION = "pades-b-b-rsa-sha256-v1"
FORMAT_ENGINE_VERSION = "yargi-pades-b-b-v1"

ACCEPTED_UPLOAD_MIME = "application/pdf"


def service_available(settings: Settings) -> bool:
    return settings.signing_enabled and not settings.signing_kill_switch


def capabilities(settings: Settings) -> dict[str, object]:
    """Safe feature metadata for `GET /api/signing/capabilities` (§8.1).

    No module, socket, token, key, or validator details are exposed.
    """
    return {
        "enabled": settings.signing_enabled and not settings.signing_kill_switch,
        "mode": settings.signing_mode,
        "test_only": settings.signing_test_only,
        "service_available": service_available(settings),
        "profile": PROFILE,
        "algorithm": ALGORITHM,
        "policy_version": POLICY_VERSION,
        "max_upload_bytes": settings.signing_max_upload_bytes,
        "pin_window_seconds": settings.signing_pin_window_seconds,
    }
