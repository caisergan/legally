#!/usr/bin/env python3
"""Independent PAdES validator #2 (pyHanko).

Complements Poppler `pdfsig` as a second, independently implemented full PAdES
validator for the Phase 1B two-validator matrix. Reports cryptographic integrity,
byte-range coverage, and an explicit fail-closed trust/revocation outcome
separately, so an untrusted self-signed test certificate yields
`trust: indeterminate` (never a false "trusted") without masking a
cryptographically valid signature.

Usage:  python pades_validate_pyhanko.py <signed.pdf>
Prints a single JSON object on stdout. Exit code: 0 crypto-valid, 2 crypto-invalid,
3 no signatures / undecidable, 4 error.
"""

from __future__ import annotations

import json
import logging
import sys

# pyHanko logs trust-path failures at ERROR to stderr; the wrapper handles those
# as an explicit "indeterminate" trust outcome, so silence the noisy loggers.
for _name in ("pyhanko", "pyhanko_certvalidator"):
    logging.getLogger(_name).setLevel(logging.CRITICAL)

VALIDATOR_NAME = "pyhanko"


def _version() -> str:
    import pyhanko

    return pyhanko.version.__version__


def validate(path: str) -> dict:
    from pyhanko.pdf_utils.reader import PdfFileReader
    from pyhanko.sign.validation import validate_pdf_signature
    from pyhanko_certvalidator import ValidationContext

    # No trust anchors and no network: a self-signed test certificate is
    # cryptographically checkable but its chain stays explicitly indeterminate.
    context = ValidationContext(trust_roots=[], allow_fetching=False)

    with open(path, "rb") as handle:
        reader = PdfFileReader(handle)
        signatures = reader.embedded_signatures
        if not signatures:
            return _result(0, "indeterminate", "indeterminate", "none", outcome="indeterminate")

        crypto_valid = True
        trusted_all = True
        coverage = None
        for embedded in signatures:
            status = validate_pdf_signature(embedded, signer_validation_context=context)
            if not (status.intact and status.valid):
                crypto_valid = False
            if not status.trusted:
                trusted_all = False
            raw_coverage = getattr(status, "coverage", None)
            coverage = str(raw_coverage).rsplit(".", 1)[-1] if raw_coverage is not None else coverage

        crypto = "valid" if crypto_valid else "invalid"
        if not crypto_valid:
            trust = "invalid"
            outcome = "invalid"
        elif trusted_all:
            trust = "trusted"
            outcome = "valid"
        else:
            # Cryptographically valid but no established trust anchor: fail closed.
            trust = "indeterminate"
            outcome = "indeterminate"
        return _result(len(signatures), crypto, trust, coverage or "unknown", outcome=outcome)


def _result(count: int, crypto: str, trust: str, coverage: str, *, outcome: str) -> dict:
    return {
        "validator": VALIDATOR_NAME,
        "version": _version(),
        "signatures": count,
        "crypto": crypto,
        "trust": trust,
        "revocation": "indeterminate",
        "coverage": coverage,
        "outcome": outcome,
    }


_EXIT = {"valid": 0, "invalid": 2, "indeterminate": 3}


def main(argv: list[str]) -> int:
    if len(argv) != 2:
        print(json.dumps({"validator": VALIDATOR_NAME, "error": "usage: <pdf>"}))
        return 4
    try:
        result = validate(argv[1])
    except Exception as error:  # noqa: BLE001 - fail closed, never crash the harness
        print(json.dumps({"validator": VALIDATOR_NAME, "error": str(error), "outcome": "invalid"}))
        return 2
    print(json.dumps(result))
    # Exit reflects cryptographic validity for the crude external-validator path.
    return _EXIT.get(result["crypto"], 2)


if __name__ == "__main__":
    raise SystemExit(main(sys.argv))
