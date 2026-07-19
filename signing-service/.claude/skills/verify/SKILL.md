---
name: verify
summary: Drive the signing daemon and constrained public package surface
---

# Signing service verification

Use isolated temporary paths. Do not treat local cryptographic verification as independent PAdES release evidence.

1. Run `go run ./cmd/yargi-signerd` from `signing-service/`; observe disabled-by-default exit.
2. Run with `SIGNERD_ENABLED=true SIGNERD_ENVIRONMENT=production SIGNERD_MODE=softhsm SIGNERD_SOCKET=/run/yargi-signerd/signerd.sock`; observe fail-closed exit.
3. Build `cmd/yargi-signerd`, launch with test/SoftHSM mode on a `mktemp` Unix socket, and use `curl --unix-socket` to drive:
   - `GET /v1/health` → safe policy metadata;
   - `POST /v1/health` → `405`;
   - `GET /v1/jobs` → `404` until the durable job phase exists.
   Confirm socket mode `0600` and removal after graceful shutdown.
4. Create a temporary external Go module with a `replace` to this module. Import `third_party/eimza-go/pades`, sign a controlled classic-xref PDF through `SignBaselineBB`, then call `VerifyCryptographic`. Observe original-prefix/catalog preservation and matching certificate/SigningCertificateV2 SHA-256.
5. At the same package surface, probe signed-byte mutation, an existing AcroForm, and a mismatched key; all must fail closed.
6. Start the existing FastAPI app on an ephemeral port. Confirm OpenAPI has no `/signing` paths, `/api/signing/capabilities` is `404`, and no signerd socket is created by FastAPI.

Report the exact response bodies and probe errors. Formatting/tests/race/vet/dependency scans are CI checks and should be reported separately from runtime verification.
