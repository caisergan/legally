# Yargı Signer Service

`yargi-signerd` is the private signing data plane described in [`docs/EIMZA_INTEGRATION_PLAN.md`](../docs/EIMZA_INTEGRATION_PLAN.md). It is intentionally separate from FastAPI and is disabled by default.

## Current implementation status

The current foundation provides:

- a pinned, owned Go module;
- fail-closed configuration validation;
- a fixed PAdES Baseline B-B / RSA PKCS#1 SHA-256 creation policy;
- the signing-state transition vocabulary and terminal-state protection;
- one serial worker per token identity, with parallel work allowed across different tokens;
- a private Unix-domain-socket HTTP server with a safe `GET /v1/health` response;
- a quarantined, controlled-corpus software-key PAdES B-B implementation used only by tests and conformance work.

The current code can create a local software-key signature for a narrow PDF fixture, but no signing job API exposes it. It does **not** access PKCS#11, accept PINs, enroll credentials, establish certificate trust/revocation/qualification, or provide legal/qualified signatures. SoftHSM and hardware modes must not be considered usable until their phase gates are recorded in `docs/tasks/EIMZA_BUILD_LEDGER.md`.

## Run the disabled default

```bash
cd signing-service
go run ./cmd/yargi-signerd
```

The process exits after reporting that signing is disabled.

## Run the private health endpoint in test mode

```bash
SIGNERD_ENABLED=true \
SIGNERD_ENVIRONMENT=test \
SIGNERD_MODE=softhsm \
SIGNERD_SOCKET=/tmp/yargi-signerd/signerd.sock \
go run ./cmd/yargi-signerd
```

The service binds only the configured Unix socket. Network transport is rejected until authenticated private HTTPS transport is implemented and reviewed.

## Security boundaries

- No browser or FastAPI session cookie is accepted by this service.
- No generic digest-sign, arbitrary path, arbitrary URL, or arbitrary PKCS#11 module API is exposed.
- Creation profile and algorithm are server policy, not caller choices.
- PIN handling is not implemented; adding it requires the one-time challenge-bound envelope flow in the integration plan.
- Unsupported profiles, algorithms, transports, and production SoftHSM configuration fail closed.

## Development checks

```bash
go fmt ./...
go test ./...
go test -race ./...
go vet ./...
```
