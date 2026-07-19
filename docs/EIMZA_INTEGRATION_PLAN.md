# Yargı Asistan — e‑İmza Source Consumption and Integration Plan

**Project root:** `/Users/egeayyildiz/Desktop/personal-projects/legally`  
**Date:** 2026-07-18  
**Status:** Proposed implementation plan  
**Companion documents:**

- `docs/IMPLEMENTATION_PLAN.md` — current Yargı Asistan architecture and contracts
- `docs/research/e-imza-fizibilite-raporu.md` — feasibility, legal/operational research, and scope boundaries
- `docs/tasks/BUILD_LEDGER.md` — completed build history for the current application
- `eimza-go-main/` — untracked source snapshot to be selectively consumed

This document defines how to consume `eimza-go-main` and add a working electronic-signature subsystem to Yargı Asistan. It is intentionally conservative: the source contains valuable Go and PKCS#11 building blocks, but static inspection found correctness, interoperability, security, and service-lifecycle gaps that prevent a direct production integration.

---

## 0. Executive decision

### 0.1 Selected architecture

Build a **separate, private Go signing service** from an owned and hardened fork of the reusable `eimza-go` core. The existing FastAPI application remains the user-facing control plane and system of record.

```text
Owner browser
  └─ HTTPS + existing session + CSRF/origin checks + fresh reauthentication
       └─ Yargı Asistan FastAPI
            ├─ durable signing workflow and audit metadata
            ├─ private immutable artifact store
            └─ private signer client
                 └─ Unix socket in single-host deployments, or mTLS on a private network
                      └─ yargi-signerd (Go)
                           ├─ durable job journal
                           ├─ one serial worker per physical token
                           ├─ PDF preparation/finalization/verification worker
                           └─ explicit PKCS#11 module + slot + certificate + key binding
                                └─ owner-specific USB token
```

### 0.2 Initial capability ladder

1. **First working vertical slice:** SoftHSM/test certificate, PAdES Baseline B-B, explicitly marked as test-only.
2. **First hardware pilot:** one real owner-specific token, PAdES B-B, controlled non-sensitive PDF corpus, independent validators.
3. **First production target:** PAdES Baseline B-T only after the RFC 3161 timestamp path is implemented and independently validated.
4. **Future gated capabilities:** CAdES, PAdES LT/LTA, multiple token vendors, and UYAP helper workflows.

### 0.3 Non-negotiable boundaries

- Signing is **not** an MCP tool and is never exposed to the LLM/chat tool loop.
- The existing CLI and Fyne GUI are not used as service boundaries.
- The Go code is not loaded into the FastAPI process through FFI.
- Private uploaded and signed documents never use the shared `DocumentCache` model.
- A shared office certificate/PIN flow is not implemented. Every credential is assigned to its certificate owner.
- PINs are never stored, cached, logged, placed in process arguments/environment variables, or retried automatically.
- A request is not `completed` merely because the signing library returned bytes. Independent output validation is required.
- UYAP login, UYAP Editor automation, signed UDF production, petition submission, and USB-over-IP are outside this feature.

---

## 1. Why direct integration is rejected

### 1.1 Current Yargı Asistan constraints

The target system is a same-origin React/Vite SPA backed by FastAPI, SQLAlchemy async, and SQLite. FastAPI starts the embedded/HTTP MCP bridge and serves `web/dist` (`server/app/main.py:45-71`). Authentication is an opaque database session cookie (`server/app/security.py:45-62`).

The application currently has:

- no multipart upload route;
- no private binary artifact store;
- no signing request or certificate-assignment models;
- no durable hardware-job queue;
- no schema migration framework beyond `Base.metadata.create_all()` (`server/app/db.py:34-39`);
- no signing-specific step-up authentication or CSRF control;
- no production reverse-proxy/TLS deployment configuration;
- no first-party backend/frontend automated test suite.

The existing legal-document abstractions cannot substitute for these capabilities:

- `DocumentCache` is shared across users and stores fetched Markdown, not private binary documents (`server/app/models.py:125-147`).
- `DocRef`, `doc_key`, bookmarks, and `DocPanel` are tied to the 12 research-source adapters.
- Account export and deletion enumerate current tables explicitly and would not correctly handle retained signature evidence (`server/app/api/account.py:58-291`).

### 1.2 `eimza-go-main` constraints

`eimza-go-main` is a four-module Go source tree:

- `eimza-go/` — cryptographic/document-format core;
- `eimza-cli/` — local command-line tool;
- `eimza-gui/` — Fyne desktop application;
- `eyp-go/` — e-Yazışma package implementation.

It has no HTTP server, authorization model, queue, persistence, job state machine, audit boundary, or browser protocol.

Direct use is blocked by confirmed issues including:

1. **Missing CAdES `signing-certificate-v2` in the real signing path.**  
   The helper exists in `eimza-go-main/eimza-go/cades/attrs_signed.go:104-124`, but normal signing in `eimza-go-main/eimza-go/cades/signeddata.go:117-159` and `:301-334` does not add it.

2. **Fail-open certificate-chain reporting.**  
   `eimza-go-main/eimza-go/cades/validate.go:226-250` can retain a valid signature result when the chain is expired, untrusted, or otherwise invalid; revocation is the main path that flips the result to invalid.

3. **Incomplete timestamp validation.**  
   `eimza-go-main/eimza-go/timestamp/client.go:63-110` checks transport/ASN.1 status but does not fully validate the timestamp token signature, TSA chain/EKU, nonce, policy, and message-imprint binding.

4. **Unsafe token selection defaults.**  
   `eimza-go-main/eimza-go/smartcard/pkcs11.go:30-44` selects the first available slot; key lookup can fall back to the first private key (`:138-204`). `QuickOpen` can choose the first discovered driver/certificate (`eimza-go-main/eimza-go/smartcard/quick.go:27-85`).

5. **No complete per-token serialization.**  
   The manager protects acquisition but not the full `SignInit`/`Sign` sequence (`eimza-go-main/eimza-go/smartcard/manager.go:36-107`, `p11signer.go:26-48`).

6. **Unproven general PDF compatibility.**  
   The PAdES implementation uses a shallow custom PDF parser/writer and assumptions around page/catalog objects (`eimza-go-main/eimza-go/pades/sign.go:55-105`, `:212-229`; `eimza-go-main/eimza-go/pdf/parser.go:10-52`). Existing tests use a minimal generated PDF.

7. **Profile labels exceed demonstrated behavior.**  
   `pades.Sign()` does not establish complete LT/LTA output even when those profiles are selected (`eimza-go-main/eimza-go/pades/sign.go:142-154`, `:196-207`).

8. **Weak algorithms remain reachable in the core model.**  
   MD5 and SHA-1 are represented under `eimza-go-main/eimza-go/crypto/alg/` and must not be exposed through the new signing policy.

9. **CLI secret/lifecycle hazards.**  
   The CLI accepts PIN/password values through flags or environment variables, uses `os.Exit`, reads/writes local paths, and may log command arguments. It is a diagnostic reference, not a safe server API.

The correct interpretation of “consume the project” is therefore: **take ownership of selected source, patch and qualify it, and place it behind a new service boundary**—not import the snapshot unchanged.

---

## 2. Source-consumption policy

### 2.1 Maintained fork location

Create a dedicated module:

```text
legally/
└── signing-service/
    ├── go.mod
    ├── go.sum
    ├── README.md
    ├── THIRD_PARTY_NOTICES.md
    ├── config/
    │   └── signerd.example.yaml
    ├── cmd/
    │   ├── yargi-signerd/
    │   │   └── main.go
    │   └── yargi-signerctl/
    │       └── main.go
    ├── internal/
    │   ├── api/
    │   ├── artifacts/
    │   ├── audit/
    │   ├── config/
    │   ├── credentials/
    │   ├── jobs/
    │   ├── pades/
    │   ├── persistence/
    │   ├── policy/
    │   ├── token/
    │   └── validation/
    ├── test/
    │   └── integration/
    ├── testdata/
    │   ├── pdf/
    │   └── validators/
    └── third_party/
        └── eimza-go/
            ├── LICENSE
            ├── UPSTREAM.md
            └── ...selected core source...
```

`UPSTREAM.md` must record:

- claimed upstream repository URL;
- imported snapshot date;
- source content digest or upstream commit when established;
- copied packages;
- local patch list;
- review owner;
- update/rebase procedure;
- packages intentionally excluded from runtime use.

Preserve the MIT license and generate an SBOM. Resolve the license, provenance, and maintenance status of `kamusm-go` before timestamp support or distribution.

### 2.2 Disposition matrix

| Source area | Treatment | Reason |
|---|---|---|
| Go `crypto.Signer` boundary | **Reuse** | Best abstraction between format code and hardware-backed signing. |
| Low-level PKCS#11 primitives | **Fork and harden** | Useful mechanics, but slot/key selection and lifecycle must be replaced. |
| CAdES/CMS ASN.1 building blocks | **Fork and harden** | Needed by PAdES; mandatory attributes and validation behavior require fixes. |
| PAdES and custom PDF packages | **Quarantine, then qualify** | May be reusable for a constrained corpus only after external validation and fuzz/corpus testing. |
| Certificate validation | **Quarantine, then replace/harden** | Current result semantics are not safe as a production validity authority. |
| Timestamp package | **Disabled, then harden** | Must validate full RFC 3161 evidence before PAdES B-T is enabled. |
| `eimza-cli/` | **Exclude from runtime** | Unsafe PIN/process/file interface and no durable service semantics. |
| `eimza-gui/` | **Exclude from runtime** | Fyne/local-filesystem UI cannot be embedded in the React SPA. |
| `eyp-go/` | **Exclude from initial scope** | EYP is not UDF and is not needed for PDF signing. |
| XAdES, ASiC, CMS encryption, mobile signing | **Exclude from initial scope** | Additional correctness/security issues and no MVP requirement. |
| PFX production signing | **Exclude initially** | The central-token requirement is PKCS#11; PFX expands key-custody risk. |

### 2.3 Pivot criterion

Do not indefinitely patch the custom PAdES/PDF implementation if it cannot pass the phase gates.

If the repaired Go format layer fails either of the following, retain the Go token-worker work but replace the document-format layer with a more mature implementation such as EU DSS or pyHanko:

- two independent validators reject controlled output after the required CAdES fixes; or
- the supported real-world PDF corpus cannot be signed without corruption or unsafe narrowing.

This pivot is a success condition for risk control, not a failure to consume the source: the token, PKCS#11, policy, queue, and service work remain reusable.

---

## 3. Target component responsibilities

### 3.1 Browser / React SPA

The SPA owns only user interaction:

- upload an owner-provided PDF;
- display immutable file hash and certificate identity;
- request signing;
- perform fresh reauthentication;
- collect one PIN only when the request reaches the head of the token queue;
- display safe status transitions;
- download the independently validated output.

The browser must not receive or choose:

- PKCS#11 module paths;
- slot IDs;
- token serials beyond minimal display information;
- private-key identifiers;
- algorithms or profile values outside server policy;
- TSA/OCSP/CRL URLs;
- signer-service credentials.

### 3.2 FastAPI control plane

FastAPI owns:

- existing application/session identity;
- signing certificate assignment to users;
- private artifact metadata and access control;
- request idempotency and durable user-visible state;
- fresh-auth/approval records;
- safe audit events and account-retention rules;
- the private client to `yargi-signerd`;
- reconciliation of signer events into application state;
- SSE or polling responses to the SPA.

FastAPI must never persist or log a PIN. The production target should relay a standards-based encrypted PIN envelope so plaintext is decrypted only inside the token worker.

### 3.3 Application artifact store

Use a separate private filesystem store; do not put document bytes in SQLite, `web/dist`, temporary world-readable paths, or `DocumentCache`.

Initial implementation rules:

- opaque object keys, never user filenames as paths;
- path traversal prevention;
- atomic write then rename;
- file mode `0600`, directory mode `0700`;
- sanitized display filename stored only as metadata;
- SHA-256 computed while streaming upload;
- server-side size limit and PDF magic/MIME/preflight checks;
- encrypted host volume and encrypted backups in production;
- explicit retention class and expiry;
- no broad filesystem mount into the signer service.

The signer obtains a job-scoped, expiring read capability for the exact input and an expiring write capability for the exact output. It rehashes the input and FastAPI verifies the returned output hash.

### 3.4 Go signing service

`yargi-signerd` owns:

- trusted credential inventory;
- explicit PKCS#11 module/driver allowlist;
- exact module + slot + token serial + certificate fingerprint + private-key `CKA_ID` mapping;
- one serial queue/worker per physical token;
- one fresh PKCS#11 session per signing attempt;
- one login attempt per PIN window;
- signature creation;
- low-level job journal and recovery classification;
- safe hardware error mapping;
- independent output verification before success is emitted.

It must never expose a generic “sign this arbitrary hash” or “load this arbitrary URL/path/module” endpoint.

### 3.5 Token worker

A token worker owns one configured physical credential binding. Its transaction is:

1. verify request/manifest version and input hash;
2. verify the configured token, slot, certificate fingerprint, and private-key `CKA_ID` are exact matches;
3. open a fresh session;
4. decrypt the one-time PIN envelope in memory;
5. perform one `C_Login` attempt;
6. execute the approved signing plan once;
7. logout and close the session;
8. clear transient references;
9. emit a safe result code and evidence metadata.

No first-slot, first-certificate, or first-key fallback is permitted.

### 3.6 PDF format and verification worker

PDF input is untrusted and must be isolated from USB/token access.

Refactor the monolithic source path into:

```text
preflight → prepare signing plan → token sign → finalize PDF → independent verify
```

The signing plan binds:

- request ID and one-time command ID;
- input artifact ID, byte count, and SHA-256;
- exact bytes/digest to be signed;
- certificate fingerprint and public-key algorithm;
- fixed signing profile and policy version;
- finalization-engine version;
- expiry and replay nonce.

The token worker signs only a plan generated and approved by the local supervisor.

### 3.7 MCP and LLM boundary

Signing must remain absent from `server/app/mcp_bridge.py` and the tool list in `server/app/chat.py`.

The chat system currently dynamically exposes most bridge tools (`server/app/chat.py:535-544`) and states that tools are read-only. A mutating legal-signature tool would invalidate that security assumption. Chat may help draft text, but the user must export/upload an immutable PDF and enter the separate signing flow.

---

## 4. Security and authorization model

### 4.1 Certificate ownership

Two independent mappings are required:

1. **Application mapping:** which authenticated user owns and may request a public certificate.
2. **Signer mapping:** which local hardware tuple may execute for that certificate.

Administrative provisioning may create/update mappings, but an administrator must not be able to sign through another owner’s ordinary user flow.

### 4.2 Fresh authentication

Existing session possession is insufficient for a legally significant action.

Initial default:

- require the owner’s current password immediately before queueing/approval;
- issue a short-lived, single-use approval record bound to request ID, input hash, certificate fingerprint, policy version, and expiry;
- later replace or augment password reentry with WebAuthn.

### 4.3 PIN envelope

Use a vetted JOSE implementation rather than custom cryptography.

Recommended production flow:

1. When a request reaches `PIN_REQUIRED`, `signerd` generates a one-time ECDH key and signed challenge.
2. The challenge binds request ID, input hash, certificate fingerprint, policy version, nonce, and 60–90 second expiry.
3. The browser uses a reviewed JOSE library to create a compact JWE using an approved `ECDH-ES` + authenticated-encryption profile.
4. FastAPI relays only the opaque JWE.
5. `signerd` accepts it once, validates all bindings, and decrypts it only in the token worker.

PIN rules:

- no database field;
- no audit field;
- no browser storage;
- no React context/global state;
- no telemetry or error echo;
- no environment variable or process argument;
- no PIN cache between requests;
- no automatic retry after an incorrect PIN;
- cooldown and lock-risk alert after a rejected PIN;
- field cleared on submit, error, unmount, and navigation.

### 4.4 CSRF, Origin, and HTTPS

All state-changing `/api/signing/*` routes require:

- existing session authentication;
- exact allowed `Origin`/`Host` validation;
- a CSRF token bound to the session;
- HTTPS in non-development mode;
- secure cookies with trusted reverse-proxy configuration;
- per-user and per-IP rate limits;
- strict body and upload limits.

`SIGNING_ENABLED=true` must fail startup if production HTTPS/proxy, signer authentication, storage, or secret requirements are missing.

### 4.5 Algorithm and profile policy

Client input cannot choose arbitrary cryptography.

Initial service policy:

- PAdES Baseline B-B;
- RSA PKCS#1 v1.5 with SHA-256, only after real-token verification;
- reject MD5, SHA-1, DSA, RSA-PSS, and ECDSA for creation until separately tested;
- reject PAdES T/LT/LTA, CAdES, EPES, XAdES, and ASiC requests until their gates pass;
- parse legacy algorithms only where a future read-only verification feature explicitly requires it.

---

## 5. Durable state model

### 5.1 Request states

```text
UPLOADED
  → VALIDATED
  → AWAITING_OWNER_CONFIRMATION
  → QUEUED
  → PIN_REQUIRED
  → SIGNING
  → VERIFYING
  → COMPLETED
```

When timestamping is enabled later:

```text
SIGNING → TIMESTAMPING → VERIFYING
```

Exceptional/terminal states:

- `REJECTED_INPUT`
- `DECLINED`
- `CANCELLED`
- `CONFIRMATION_EXPIRED`
- `PIN_WINDOW_EXPIRED`
- `PIN_REJECTED`
- `TOKEN_LOCKED`
- `WAITING_FOR_TOKEN`
- `TOKEN_UNAVAILABLE`
- `FAILED_PRE_SIGN`
- `FAILED_POST_SIGN`
- `OUTCOME_UNKNOWN`
- `QUARANTINED`

### 5.2 Transition rules

- Cancellation is effective only before `SIGNING` begins.
- Browser disconnection does not cancel a hardware operation.
- A wrong PIN is terminal for that PIN window and is never automatically retried.
- If a process, transport, or token failure occurs after `C_Sign` may have executed, state becomes `OUTCOME_UNKNOWN`.
- `OUTCOME_UNKNOWN` is never auto-requeued. It requires operator reconciliation and fresh owner approval for any new attempt.
- Timestamp, finalization, storage, or validation failure after token signing becomes `FAILED_POST_SIGN` or `QUARANTINED`; the token operation is not repeated automatically.
- `COMPLETED` is immutable and requires an output artifact hash plus independent verification evidence.
- Retrying a failed request creates a new request/attempt ID and audit trail.

### 5.3 Idempotency

Use an `Idempotency-Key` header when creating uploads and signing requests.

An existing request may be returned only when all immutable inputs match:

- owner;
- input SHA-256 and size;
- assigned certificate ID/fingerprint;
- policy/profile version.

Reuse with different inputs returns `409 Conflict`.

---

## 6. Application data model and migrations

### 6.1 Add Alembic before signing tables

The current delete-and-recreate development convention is unacceptable for signing evidence.

Add:

```text
server/alembic.ini
server/migrations/env.py
server/migrations/script.py.mako
server/migrations/versions/0001_legacy_schema_baseline.py
server/migrations/versions/0002_signing_subsystem.py
```

Migration rollout:

1. generate and review a baseline matching the existing schema;
2. take and restore-test a backup of the current SQLite database;
3. schema-check and stamp existing databases at the baseline revision;
4. apply the additive signing migration;
5. retain `create_all()` only for disposable test/development databases;
6. make production startup verify migration revision instead of silently creating tables.

SQLite remains acceptable only for the current single-host, single-FastAPI-worker topology. Move to PostgreSQL before active-active application nodes, multiple writers, or HA.

### 6.2 Proposed models

#### `SigningArtifact`

- `id`
- `owner_user_id`
- `kind`: `input | output | quarantine`
- `storage_key`
- `display_filename`
- `mime_type`
- `byte_count`
- `sha256`
- `parent_artifact_id`
- `preflight_status`
- `retention_class`
- `created_at`, `expires_at`, `deleted_at`

No document bytes in SQLite.

#### `SigningCertificate`

- `id`
- `service_credential_id`
- `certificate_fingerprint_sha256`
- masked subject/issuer display data
- certificate serial suffix
- `not_before`, `not_after`
- public-key type
- `status`
- last inventory/health timestamp

Do not expose PKCS#11 paths, full token serials, or private-key identifiers to normal users.

#### `SigningCertificateAssignment`

- `certificate_id`
- `user_id`
- `assigned_by_user_id`
- `active`
- `created_at`, `revoked_at`
- unique active owner assignment per certificate for v1

#### `SigningRequest`

- `id`
- `owner_user_id`
- `input_artifact_id`
- `output_artifact_id`
- `certificate_id`
- certificate fingerprint snapshot
- input hash/size snapshot
- fixed profile/algorithm/policy snapshot
- `state`, `version`
- `idempotency_key`
- signer job/attempt ID
- safe failure code
- queue/sign/verify/terminal timestamps

Immutable fields are locked once the request enters `QUEUED`.

#### `SigningApproval`

- `request_id`
- `owner_user_id`
- one-time approval-token hash
- fresh-auth timestamp
- expiry
- consumed/revoked timestamp

Never stores PIN or PIN ciphertext.

#### `SigningRequestEvent`

Append-only safe event projection for the UI and reconciliation:

- request ID;
- monotonic sequence;
- state/event type;
- safe JSON metadata;
- source (`app | signer`);
- source event ID;
- timestamp.

#### `SigningAuditEvent`

Append-only, hash-chain-linked evidence metadata:

- global/partition sequence;
- request and attempt IDs;
- actor user/session identifier where appropriate;
- event type and state transition;
- input/output hashes;
- certificate fingerprint and serial suffix;
- policy, application, signer, format-engine, and driver versions;
- external-validation result reference;
- safe failure class;
- previous event hash and current event hash;
- timestamp.

Never include PINs, complete document contents, raw session tokens, signer credentials, TSA credentials, or raw vendor error messages.

Use database triggers to prevent ordinary update/delete of audit rows. Hash-chain sealing plus protected off-host backup makes the log tamper-evident; do not claim that a SQLite table is absolutely immutable against privileged host administrators.

#### `SigningOutbox`

Transactional outbox/deduplication table for application-to-signer commands and signer events:

- command/event ID;
- request ID and expected version;
- canonical payload hash;
- state and retry metadata;
- created/delivered timestamps.

Only pre-sign transport operations are retryable. Commands that may have reached token execution require reconciliation rather than automatic replay.

### 6.3 Account export and deletion

Modify `server/app/api/account.py` before exposing signing:

- `delete-history` never removes signing requests, artifacts, approvals, or audit events;
- account deletion is blocked while retention-bound signing records exist, or follows an approved anonymization workflow;
- export includes allowed signing metadata but not raw private artifacts by default;
- artifact export is a separate authenticated download flow;
- export never includes PIN data, signer secrets, PKCS#11 configuration, audit sealing keys, or raw internal errors.

Retention periods, legal hold, and anonymization require legal/records-owner approval. Until decided, default to **no self-service destruction of completed signing evidence**.

---

## 7. API contracts

### 7.1 Browser-facing FastAPI routes

All routes are under `/api/signing` and use existing `get_current_user` ownership conventions.

| Method and route | Purpose |
|---|---|
| `GET /capabilities` | Feature mode, safe supported profile, upload limit, and service availability. |
| `GET /certificates` | Active certificates assigned to the current user only. |
| `POST /artifacts` | Multipart owner PDF upload; stream, validate, hash, and store privately. |
| `GET /artifacts/{id}` | Owner-filtered safe metadata. |
| `DELETE /artifacts/{id}` | Delete only unreferenced artifacts allowed by retention policy. |
| `POST /requests` | Create an idempotent request from `artifact_id` and `certificate_id`; server fixes profile/algorithm. |
| `GET /requests` | Current user’s signing history. |
| `GET /requests/{id}` | Owner-filtered manifest, state, timeline, and output metadata. |
| `POST /requests/{id}/confirm` | Verify current password/fresh auth and queue the immutable manifest. |
| `GET /requests/{id}/events` | Read-only SSE state transitions; polling may be used in the first slice. |
| `GET /requests/{id}/pin-challenge` | Return the one-time signer challenge only in `PIN_REQUIRED`. |
| `POST /requests/{id}/pin-envelope` | Relay one JWE envelope; never accept/store a reusable PIN value. |
| `POST /requests/{id}/cancel` | Cancel only before hardware signing begins. |
| `GET /requests/{id}/download` | Owner-filtered completed output with `Cache-Control: private, no-store`. |

Request bodies use strict Pydantic models with `extra="forbid"`. Safe errors preserve the project’s `{detail}` convention and Turkish UI copy. Raw PKCS#11, certificate path, TSA, or internal service details remain server-side.

### 7.2 Internal signer API

Development/single-host default: Unix domain socket.  
Multi-host production: private interface, mTLS, firewall allowlist, replay-safe signed commands.

| Method and route | Purpose |
|---|---|
| `GET /v1/health` | Service and policy version; no private inventory details. |
| `GET /v1/credentials` | Safe public inventory for trusted FastAPI reconciliation. |
| `POST /v1/jobs` | Create idempotent job using immutable manifest and artifact capability. |
| `GET /v1/jobs/{id}` | Safe state and output/evidence metadata. |
| `GET /v1/jobs/{id}/events?after=` | Monotonic durable signer events. |
| `POST /v1/jobs/{id}/authorize` | Submit one challenge-bound JWE PIN envelope. |
| `POST /v1/jobs/{id}/cancel` | Cancel only before signing claim. |

The API must not accept:

- arbitrary raw digest-sign requests;
- browser-selected driver/module/slot/key IDs;
- arbitrary file paths or external URLs;
- browser-selected algorithms/profiles;
- browser-selected TSA/OCSP/CRL endpoints;
- raw application session cookies.

Every command binds command ID, request ID, expected version, input hash, certificate fingerprint, policy version, nonce, and expiry. `signerd` independently rehashes input bytes.

---

## 8. Repository changes

### 8.1 New Go service files

```text
signing-service/go.mod
signing-service/go.sum
signing-service/README.md
signing-service/THIRD_PARTY_NOTICES.md
signing-service/config/signerd.example.yaml
signing-service/cmd/yargi-signerd/main.go
signing-service/cmd/yargi-signerctl/main.go
signing-service/internal/api/contracts.go
signing-service/internal/api/handlers.go
signing-service/internal/api/middleware.go
signing-service/internal/api/server.go
signing-service/internal/artifacts/capability.go
signing-service/internal/audit/audit.go
signing-service/internal/config/config.go
signing-service/internal/credentials/probe.go
signing-service/internal/credentials/registry.go
signing-service/internal/jobs/queue.go
signing-service/internal/jobs/service.go
signing-service/internal/jobs/state.go
signing-service/internal/pades/prepare.go
signing-service/internal/pades/finalize.go
signing-service/internal/persistence/journal.go
signing-service/internal/policy/policy.go
signing-service/internal/token/errors.go
signing-service/internal/token/selector.go
signing-service/internal/token/worker.go
signing-service/internal/validation/preflight.go
signing-service/internal/validation/verify.go
signing-service/third_party/eimza-go/LICENSE
signing-service/third_party/eimza-go/UPSTREAM.md
```

### 8.2 FastAPI files to modify

- `server/pyproject.toml` — Alembic, multipart upload, test dependencies.
- `server/.env.example` — signing feature, signer transport, limits, security, retention.
- `server/app/config.py` — typed signing configuration and startup invariants.
- `server/app/db.py` — migration revision checks; `create_all()` limited to disposable databases.
- `server/app/models.py` — signing metadata models.
- `server/app/security.py` — CSRF/origin and fresh-auth helpers.
- `server/app/main.py` — signer/artifact client lifecycle; no in-process PKCS#11.
- `server/app/api/__init__.py` — register signing router.
- `server/app/api/account.py` — export/deletion/retention behavior.
- `.gitignore` — signer state, artifacts, credentials, test-token data.
- `README.md` — local test mode and production boundary.

### 8.3 FastAPI files to create

```text
server/app/api/signing.py
server/app/artifact_store.py
server/app/signing_audit.py
server/app/signing_client.py
server/app/signing_policy.py
server/app/signing_state.py
server/app/signing_workflow.py
server/alembic.ini
server/migrations/env.py
server/migrations/script.py.mako
server/migrations/versions/0001_legacy_schema_baseline.py
server/migrations/versions/0002_signing_subsystem.py
```

### 8.4 React files to modify

- `web/src/state/app.tsx` — add `signing` to `AppView`.
- `web/src/App.tsx` — lazy-load the dedicated view.
- `web/src/components/Sidebar.tsx` — new navigation item.
- `web/src/components/Header.tsx` — title/state mapping.
- `web/src/components/Icon.tsx` — signing icon.
- `web/src/lib/api.ts` — multipart/download helpers as needed.
- `web/src/lib/types.ts` — signing contracts.
- `web/src/styles/app.css` — signing surface classes using existing tokens.
- `web/package.json` — component-test dependencies.

Leave `web/src/components/DocPanel.tsx` unchanged in the first release. Research-source Markdown is not an owned, immutable signable PDF.

### 8.5 React files to create

```text
web/src/lib/signing.ts
web/src/views/SigningView.tsx
web/src/components/signing/SigningUploadCard.tsx
web/src/components/signing/SigningRequestCard.tsx
web/src/components/signing/SigningStatusTimeline.tsx
web/src/components/signing/PinApprovalDialog.tsx
web/vitest.config.ts
web/src/test/setup.ts
```

### 8.6 Deployment and operations files

```text
deploy/README.md
deploy/nginx/yargi-asistan.conf
deploy/systemd/yargi-asistan.service
deploy/systemd/yargi-signerd.service
deploy/systemd/yargi-signerd.tmpfiles.conf
docs/tasks/EIMZA_BUILD_LEDGER.md
docs/signing/THREAT_MODEL.md
docs/signing/RETENTION_AND_PRIVACY.md
docs/signing/INTEROPERABILITY_MATRIX.md
docs/signing/PILOT_PROTOCOL.md
```

---

## 9. Configuration contract

Add safe defaults:

```dotenv
SIGNING_ENABLED=false
SIGNING_MODE=disabled                 # disabled | softhsm | hardware
SIGNING_SERVICE_TRANSPORT=unix        # unix | https
SIGNING_SERVICE_SOCKET=/run/yargi-signerd/signerd.sock
SIGNING_SERVICE_URL=
SIGNING_SERVICE_CA_PATH=
SIGNING_SERVICE_CLIENT_CERT_PATH=
SIGNING_SERVICE_CLIENT_KEY_PATH=
SIGNING_SERVICE_TIMEOUT_SECONDS=30
SIGNING_ARTIFACT_ROOT=./data/signing-artifacts
SIGNING_MAX_UPLOAD_BYTES=26214400
SIGNING_APPROVAL_TTL_SECONDS=300
SIGNING_PIN_WINDOW_SECONDS=90
SIGNING_MAX_PENDING_PER_USER=10
SIGNING_INPUT_RETENTION_DAYS=30
SIGNING_OUTPUT_RETENTION_DAYS=365
SIGNING_FORCE_HTTPS=true
SIGNING_ALLOWED_ORIGINS=
```

Values above are implementation defaults, not final records-policy decisions.

When signing is enabled outside development, startup must refuse to continue if:

- a default/development session secret is active;
- HTTPS/trusted-proxy configuration is absent;
- signer Unix-socket permissions or mTLS credentials are absent;
- the signer endpoint is plain public HTTP;
- artifact storage is inside `web/dist`, a shared cache, or an unsafe temporary path;
- `softhsm` mode is configured as production;
- migration revision is behind;
- the signing policy exposes unsupported algorithms/profiles.

---

## 10. Implementation phases

## Phase 0 — Intake, provenance, and hard boundaries

**Goal:** Convert the untracked snapshot into a traceable owned source dependency and freeze the MVP scope.

Tasks:

- record source origin/content digest and preserve license;
- create `signing-service/` and `UPSTREAM.md`;
- import the selected `eimza-go` core only;
- add SBOM and third-party notices;
- create threat model, privacy/retention draft, interoperability matrix, pilot protocol, and build ledger;
- document owner-only use, PAdES-only scope, and the UYAP exclusion;
- pin the Go toolchain and dependencies;
- add CI for formatting, tests, vet/static analysis, and vulnerability scanning.

**Gate:** The source can be reproduced and audited; unsupported modules cannot enter the runtime dependency graph; legal/compliance review items are explicitly tracked.

## Phase 1 — Core remediation and conformance harness

**Goal:** Make the selected source safe enough for a test signer.

Tasks:

- wire `signing-certificate-v2` into actual CAdES signing and validate certificate binding;
- make chain, expiry, qualification-policy, and revocation outcomes explicit: `valid | invalid | indeterminate`;
- fail closed for signing policy on `invalid` or `indeterminate`;
- verify CRL signatures and chain binding;
- add bounded OCSP/CRL clients with scheme/host/size/time limits;
- remove weak algorithms from service-reachable policy;
- reject unsupported PAdES profiles rather than silently downgrade;
- replace first-slot/certificate/key fallback with exact selectors;
- add one-worker-per-token abstractions and fresh-session lifecycle;
- create a PDF corpus and negative/fuzz tests;
- integrate two independent validators into the conformance workflow where automation permits.

**Gate:** Controlled PAdES B-B output contains the required signed attributes, exact credential selection is proven, unsupported profiles fail closed, and independent validators accept the supported fixture set.

**Pivot gate:** If PAdES/PDF conformance remains structurally unreliable, replace that layer with DSS/pyHanko before proceeding; do not lower acceptance criteria.

## Phase 2 — `yargi-signerd` and SoftHSM vertical slice

**Goal:** Produce a complete but explicitly non-production signing flow without real credentials.

Tasks:

- implement private Unix-socket API;
- implement durable signer job journal;
- implement exact SoftHSM credential enrollment via `yargi-signerctl`;
- implement per-token queue and state machine;
- implement artifact capability fetch/return and hash verification;
- implement prepare → sign → finalize → verify;
- implement safe error classes and event cursor;
- implement restart recovery and `OUTCOME_UNKNOWN` behavior;
- prove no overlapping calls for one token;
- prove PIN redaction across logs, journal, events, crash output, and tests.

**Gate:** Two simultaneous requests serialize correctly; output validates independently; restart/crash tests do not cause an automatic duplicate signature; no PIN text is retained anywhere.

**What this phase does not prove:** qualified certificate status, Turkish token support, legal sufficiency, TSA/PAdES B-T, LT/LTA, or UYAP compatibility.

## Phase 3 — FastAPI persistence, artifact store, and signer bridge

**Goal:** Add durable application ownership and privacy controls.

Tasks:

- introduce Alembic baseline and additive migration;
- implement signing models and immutable-state constraints;
- implement private artifact store and streaming upload limits;
- implement `SigningClient` lifecycle and event reconciliation;
- implement transactional outbox/idempotency;
- implement ownership-filtered `/api/signing` routes;
- implement CSRF/origin checks and fresh password reauthentication;
- implement request/account retention rules;
- add backend tests for IDOR, state transitions, redaction, migration, audit immutability, and ambiguous completion.

**Gate:** Migration succeeds against a populated copy of the current database; cross-user access is impossible; no document bytes enter SQLite/shared caches/logs; all state transitions are covered by tests.

## Phase 4 — React signing experience

**Goal:** Add an isolated, explicit signing surface.

User flow:

```text
Upload PDF
  → review filename, size, SHA-256, and assigned certificate
  → create immutable request
  → fresh password confirmation
  → wait in token queue
  → enter PIN once when prompted
  → signing and independent verification
  → download completed PDF
```

Tasks:

- add `signing` view/navigation/header/icon;
- build upload, manifest, request, timeline, confirmation, PIN, and download components;
- show an unavoidable SoftHSM/test-mode banner;
- show profile, input hash, certificate fingerprint suffix, and output hash;
- clear PIN and approval values on all lifecycle exits;
- show explicit `OUTCOME_UNKNOWN` guidance: do not resubmit automatically;
- state that the result is not submitted to UYAP;
- add Vitest/React Testing Library coverage.

**Gate:** TypeScript build and component tests pass; route changes and errors leave no PIN/approval values in component/global/browser storage; all exceptional states have safe user copy.

## Phase 5 — One-owner real-token pilot

**Goal:** Prove the hardware path with one consenting credential owner.

Tasks:

- document exact OS, token model, reader, driver/module version and hash, slot identity, certificate fingerprint, and `CKA_ID`;
- enroll locally through `yargi-signerctl`; no remote token configuration;
- start with non-sensitive controlled PDFs;
- require owner PIN for every operation;
- use a disposable/spare token for wrong-PIN/lock testing;
- test removal/reinsertion, wrong token, moved slot, multiple certificates, queueing, driver errors, FastAPI restart, signer restart, and failure around `C_Sign`;
- validate with at least Adobe Acrobat/Reader plus an ETSI-aware independent validator such as EU DSS;
- save validator versions, trust settings, reports, and document hashes without committing real client documents.

**Gate:** No wrong credential can be selected; all same-token calls serialize; external validators accept output; ambiguous failures are not retried; privacy/log review passes.

## Phase 6 — Trusted timestamp and PAdES B-T

**Goal:** Reach the initial production profile.

Tasks:

- resolve KamuSM/TSA provider and dependency licensing;
- implement nonce and message-imprint binding;
- validate timestamp CMS signature, TSA certificate chain, EKU, policy, and time bounds;
- enforce configured endpoint allowlist and bounded HTTPS client behavior;
- persist timestamp validation evidence metadata;
- add negative vectors for wrong imprint, replay, malformed token, invalid/expired TSA chain, timeout, and unavailable endpoint;
- validate final B-T output independently.

**Gate:** A timestamp response cannot be accepted solely because transport/ASN.1 status succeeded; two independent validators accept B-T output under documented trust configuration.

## Phase 7 — Production hardening and controlled rollout

**Goal:** Enable a limited office deployment.

Tasks:

- deploy FastAPI behind TLS with trusted proxy and secure-cookie configuration;
- run signer host-native under a dedicated unprivileged account;
- use systemd hardening, resource limits, protected paths, and explicit device access;
- keep signer off the public/LAN interface; use Unix socket or mTLS/firewall;
- restrict signer egress to approved TSA/OCSP/CRL destinations;
- enable encrypted volume and encrypted backups;
- test metadata/artifact backup and restore together;
- verify audit hash chain after restore;
- add monitoring, kill switch, token-replacement, PIN-lock, TSA-outage, revocation, and incident runbooks;
- preserve one FastAPI worker and one signer daemon for initial SQLite deployment.

**Gate:** Security review, legal/operational approval, backup/restore drill, audit verification, real-token evidence, external-validation evidence, and incident runbooks are complete.

---

## 11. Test plan

### 11.1 Go unit/integration tests

Create coverage for:

- legal state transitions and terminal-state immutability;
- per-token queue serialization and multi-token parallelism;
- exact module/slot/token/certificate/`CKA_ID` selection;
- no first-item fallbacks;
- one-login-attempt PIN behavior;
- `CKR_PIN_INCORRECT`, locked token, removal, session loss, and driver error classification;
- artifact hash mismatch and capability expiry;
- duplicate idempotency keys;
- cancellation race before signing;
- process restart before/during/after token call;
- no automatic retry after `OUTCOME_UNKNOWN`;
- mandatory CAdES attribute presence and value binding;
- invalid/expired/untrusted/revoked/indeterminate certificate states;
- invalid CRL signature and OCSP unknown/network failure;
- unsupported profile/algorithm rejection;
- PDF malformed input, invalid ByteRange, large-file bounds, xref streams, AcroForms, existing signatures, and signed-byte mutation;
- race detector on queue/token abstractions where the PKCS#11 test backend permits it.

### 11.2 FastAPI tests

Create:

```text
server/tests/conftest.py
server/tests/test_signing_api.py
server/tests/test_signing_client.py
server/tests/test_signing_state_machine.py
server/tests/test_signing_privacy.py
server/tests/test_signing_account_retention.py
server/tests/test_signing_migrations.py
```

Cover:

- disabled feature behavior;
- two-user IDOR attempts returning 404;
- upload size/type/preflight failures;
- CSRF/origin and fresh-auth enforcement;
- immutable manifest and request fields;
- idempotency conflicts;
- legal and illegal state transitions;
- PIN redaction in logs, exceptions, database, audit, export, and test captures;
- signer timeout before sign versus after possible sign;
- `OUTCOME_UNKNOWN` without retry;
- audit trigger/update/delete rejection and hash-chain verification;
- existing populated DB migration and restore;
- delete-history/account-delete behavior with retained evidence.

### 11.3 Frontend tests

Cover:

- upload and manifest rendering;
- certificate ownership filtering;
- test-mode banner;
- confirmation and PIN-window expiry;
- PIN clearing on success, failure, cancel, unmount, navigation, and network error;
- no PIN in toast/error/debug payload;
- disabled cancel after signing begins;
- `OUTCOME_UNKNOWN` guidance;
- completed download and no-store behavior;
- no UYAP submission claim.

### 11.4 External validation matrix

| Surface | Required evidence |
|---|---|
| SoftHSM B-B | Independent cryptographic validation and attribute inspection. |
| Real-token B-B | Adobe/Reader plus ETSI-aware validator acceptance. |
| B-T | TSA token imprint/signature/chain evidence plus independent PAdES validation. |
| PDF corpus | Supported inputs remain readable and unchanged except for valid incremental signature update. |
| Token matrix | One recorded pass per exact vendor, model, OS, and driver version. |
| Recovery | Crash injection proves no automatic duplicate signature. |
| Privacy | Redaction scan and artifact-access tests. |
| Operations | Backup/restore, audit verification, alerting, kill-switch, and incident drills. |

Internal round-trip verification by the same source library is never sufficient release evidence.

---

## 12. Acceptance criteria

### 12.1 First working solution — test mode

The solution is considered working in SoftHSM mode when:

1. An authenticated user uploads a bounded PDF through the React view.
2. FastAPI stores it privately, records its SHA-256, and creates an owner-scoped request.
3. The request reaches a serialized SoftHSM token worker.
4. The owner performs fresh auth and submits one ephemeral PIN authorization.
5. The Go service produces a PAdES B-B artifact.
6. An independent validator accepts the output.
7. The output is downloaded only by its owner.
8. Two concurrent same-token requests do not overlap hardware calls.
9. Browser disconnect/restart does not lose the durable request state.
10. No PIN appears in storage, logs, events, error payloads, browser storage, or exports.
11. The UI clearly labels the signature as test-only and makes no legal/qualified/UYAP claim.

### 12.2 Real-token pilot

The hardware pilot passes when:

- exact credential binding prevents wrong-token/key selection;
- owner-only PIN entry works once per transaction;
- real output passes independent validators;
- token removal, wrong PIN, lock, restart, and timeout behavior are known and safe;
- no post-`C_Sign` failure is automatically retried;
- audit and privacy evidence passes review.

### 12.3 Production release

Production signing remains disabled until all of the following are complete:

- written legal/operational approval for central custody and remote owner PIN entry;
- approved retention, deletion, legal-hold, and audit policies;
- PAdES B-T external validation evidence;
- supported token/driver matrix;
- TLS/mTLS or Unix-socket isolation and host hardening;
- secure-cookie, CSRF/origin, and fresh-auth controls;
- migration and backup/restore evidence;
- audit-chain verification;
- incident, token replacement, PIN lock, TSA outage, and revocation runbooks;
- feature kill switch and monitoring.

---

## 13. Explicitly rejected approaches

| Approach | Decision | Reason |
|---|---|---|
| FastAPI invokes `eimza-cli` | Reject | PIN/process/log/file hazards, `os.Exit`, no queue or durable semantics. |
| Python FFI/c-shared Go library | Reject | Collapses native driver/PIN failures into the web process and lacks a supported ABI. |
| Signing as MCP/chat tool | Reject | LLM must have no authority over a legally significant mutating action. |
| Reuse `DocumentCache` | Reject | Shared, text-oriented, mutable cache would risk cross-user disclosure. |
| Trust source self-validation | Reject | Confirmed fail-open and profile/timestamp gaps. |
| Enable `--host 0.0.0.0` immediately | Reject | Existing app lacks the required production HTTPS, CSRF, provisioning, and network posture. |
| Shared office token/certificate | Reject | Ownership, attribution, PIN-lock, evidentiary, and legal risk. |
| Advertise LT/LTA from current enums | Reject | Source behavior does not establish those profiles. |
| UYAP/UDF automation | Reject for this feature | Separate local-client/device problem with no demonstrated API path. |
| Docker-first hardware signer | Defer | Host-native PKCS#11/USB deployment is easier to audit for the first pilot. |

---

## 14. Open decisions and safe defaults

| Decision | Safe default used by this plan | Required owner |
|---|---|---|
| Central token/remote PIN legal sufficiency | Keep production disabled | Legal/compliance and office management |
| Initial format | PAdES B-B test/pilot; B-T production target | Product + legal/compliance |
| Token vendors/host OS | Support only exact combinations that pass pilot | Operations/security |
| TSA provider/policy | Disabled until validated | Legal/compliance + operations |
| Input/output retention | 30/365-day implementation placeholders; no self-service deletion of evidence | Records/legal owner |
| Temporary PIN session/cache | Never | Legal/security exception required to change |
| Staff delegation | None | Legal/operations exception required to change |
| Deployment topology | One FastAPI worker, one signer daemon, private transport | Engineering/operations |
| Database | SQLite while single-host; PostgreSQL before HA/multi-writer | Engineering |
| PAdES engine failure | Pivot to DSS/pyHanko rather than weaken gates | Engineering/security |
| UYAP support | Separate research/pilot workstream | Product/legal/operations |

---

## 15. Recommended implementation order summary

```text
0. Provenance + scope + threat/retention documents
1. Repair/qualify selected eimza-go core
2. Build signerd + SoftHSM durable vertical slice
3. Add Alembic, artifacts, models, bridge, and secure FastAPI API
4. Add dedicated React signing UI
5. Pilot one real owner-specific token
6. Implement and validate RFC 3161 → PAdES B-T
7. Harden deployment and perform controlled rollout
8. Consider CAdES, LT/LTA, more token vendors, and separate UYAP helpers
```

The key delivery principle is: **each phase must leave a useful, testable system, but no phase may claim more legal or interoperability assurance than its evidence supports.**
