# Signing Workflow and Credential Integration Implementation Plan

**Project:** Yargı Asistan
**Date:** 2026-07-19
**Status:** Proposed, implementation-ready engineering plan
**Scope:** Credential provisioning, signer jobs, PDF upload, owner confirmation, one-time PIN authorization, PAdES B-B signing, verification, and signed-artifact download

## Relationship to existing documents

This document is the focused execution plan for the unfinished signing workflow described in:

- [`../EIMZA_INTEGRATION_PLAN.md`](../EIMZA_INTEGRATION_PLAN.md) — authoritative architecture, security, legal, and release boundaries;
- [`../tasks/EIMZA_BUILD_LEDGER.md`](../tasks/EIMZA_BUILD_LEDGER.md) — implementation evidence and phase status;
- [`THREAT_MODEL.md`](THREAT_MODEL.md) — required security properties and abuse cases;
- [`RETENTION_AND_PRIVACY.md`](RETENTION_AND_PRIVACY.md) — artifact and evidence handling;
- [`INTEROPERABILITY_MATRIX.md`](INTEROPERABILITY_MATRIX.md) — external validation evidence;
- [`PILOT_PROTOCOL.md`](PILOT_PROTOCOL.md) — real-token pilot requirements.

If this document conflicts with `EIMZA_INTEGRATION_PLAN.md`, the integration plan wins. Completing an engineering phase here does not by itself authorize production or qualified-signature use.

---

## 1. Outcome

Implement a complete, test-only SoftHSM signing flow first, followed by a separately gated real-token pilot:

```text
Local administrator enrolls one exact token credential
  → FastAPI imports its safe public certificate inventory
  → administrator assigns the certificate to its owner
  → owner uploads an immutable PDF
  → owner creates and reviews a signing request
  → owner performs fresh account reauthentication
  → request enters the physical token's serial queue
  → signerd issues a one-time PIN challenge
  → owner enters the PIN once in the browser
  → browser encrypts the PIN for that challenge
  → FastAPI relays the opaque envelope without storing it
  → token worker performs one login and one approved signature
  → signerd finalizes and verifies the PAdES document
  → FastAPI records output hash and verification evidence
  → only the owner can download the completed PDF
```

The implementation must connect the existing service foundations:

- `internal/jobs/state.go`;
- `internal/jobs/queue.go`;
- `internal/policy/policy.go`;
- `third_party/eimza-go/cades`;
- `third_party/eimza-go/pades`;
- `third_party/eimza-go/pdf`.

The browser never calls `yargi-signerd` directly. FastAPI remains the authenticated control plane and owner-facing API.

---

## 2. Current baseline

The following already exists:

- disabled-by-default `yargi-signerd` process;
- private Unix-domain socket server;
- `GET /v1/health`;
- fixed PAdES Baseline B-B plus RSA PKCS#1 SHA-256 creation policy;
- legal job-state vocabulary and terminal-state protection;
- bounded per-token queues;
- controlled-corpus CAdES/PAdES/PDF implementation;
- local structural and cryptographic verification;
- one independent Poppler validation result for a synthetic software-key fixture.

The following does not exist:

- PKCS#11 or SoftHSM runtime dependency;
- module, slot, token, certificate, or key enrollment;
- credential registry;
- user-to-certificate assignment;
- durable signer job journal;
- PDF upload or private artifact broker;
- job, credential, event, confirmation, PIN, or download endpoints;
- PIN challenge/envelope handling;
- connection from the API to the local PAdES implementation;
- restart reconciliation;
- FastAPI signing models/routes/client;
- React signing interface;
- real-token evidence or production authorization.

There is currently no credential input channel. `yargi-signerctl` is a placeholder and the daemon accepts no PIN or credential request.

---

## 3. Fixed design decisions

### 3.1 Credential custody

For the initial implementation, the signing private key remains inside SoftHSM or the physical PKCS#11 token. The system stores only a binding to the key; it does not import, export, copy, or escrow the private key.

A signer credential is the exact tuple:

```text
allowlisted module alias and module SHA-256
+ slot ID
+ token serial
+ certificate SHA-256 fingerprint
+ private-key CKA_ID
```

Every field is mandatory. Missing or ambiguous matches fail closed. There is no fallback to the first module, slot, token, certificate, or private key.

Enrollment also derives and persists an opaque, stable `physical_token_queue_key` from signer-local physical-token identity. Every credential on the same physical token must have the same queue key, even when it refers to a different certificate or private key. The queue key, not `service_credential_id`, is mandatory for scheduling, locking, recovery, and queue metrics. It is never exposed to the browser.

### 3.2 Credential enrollment channel

Credential enrollment is a local administrative operation performed with `yargi-signerctl`. There is no remote or browser credential-enrollment endpoint in v1.

The enrollment flow may prompt for a PIN through a no-echo interactive TTY only when proof of certificate/key possession is required. A PIN must never be accepted through:

- a command-line flag;
- an environment variable;
- a configuration file;
- piped standard input;
- a remote API;
- a persisted enrollment record.

### 3.3 User ownership mapping

Two independent mappings are required:

1. `yargi-signerd` maps a service credential ID to the exact local PKCS#11 tuple.
2. FastAPI maps the corresponding public certificate to one active application owner.

The initial assignment workflow is an authenticated local administration command, not a normal browser endpoint. A later admin UI requires a separate authorization design.

### 3.4 PIN transport

The owner enters a PIN only after the job reaches `PIN_REQUIRED`.

The production-shaped design uses reviewed JOSE implementations:

- a signed, one-time challenge generated by `yargi-signerd`;
- an ephemeral recipient key per challenge;
- compact JWE using an explicitly allowlisted ECDH-ES plus authenticated-encryption profile;
- challenge, job, request, input hash, certificate fingerprint, policy, nonce, expiry, recipient-key thumbprint, JWE `alg`, JWE `enc`, and key ID binding;
- a deployment-pinned signer challenge-verification public key or keyset delivered to the SPA through trusted application configuration;
- explicit challenge-signing key rotation with overlapping verification keys and stable key IDs;
- one successful consumption attempt;
- 60–90 second expiry;
- no persistence of plaintext or ciphertext.

The signed JWS payload contains the exact ephemeral recipient JWK or its RFC 7638 thumbprint. The browser rejects an unsigned challenge, an unknown signer key, unknown JOSE algorithms, a recipient-key mismatch, or any manifest-binding mismatch before accepting PIN input. Signerd checks the same protected-header and recipient-key bindings before decryption.

FastAPI relays the compact JWE as an opaque value. It does not decrypt, persist, enqueue for retry, or log the body. A relay timeout is treated as an ambiguous delivery result: FastAPI discards its JWE reference and reconciles signer state before allowing any new challenge.

### 3.5 Signing and verification are workflow stages, not generic APIs

There will be no public or internal endpoint equivalent to:

- `POST /sign` with arbitrary bytes or digest;
- `POST /verify` as a caller-controlled validation oracle;
- `POST /load-module`;
- `POST /sign-path`;
- `POST /sign-url`.

Signing starts only after an immutable request is confirmed and its one-time PIN authorization is consumed. Verification always runs automatically after finalization. A signed artifact is downloadable only after the required verification stages pass.

### 3.6 Initial capability

The first complete slice is test-only:

- SoftHSM;
- one test credential;
- PAdES Baseline B-B;
- RSA PKCS#1 v1.5 with SHA-256;
- current controlled PDF corpus;
- no timestamp;
- no CAdES user-facing product;
- no XAdES, ASiC, PFX, EYP, UDF, UYAP login, or UYAP submission.

---

## 4. Target architecture

```text
React browser
  │
  │ HTTPS, session cookie, CSRF, Origin/Host checks, fresh auth
  ▼
FastAPI control plane
  ├── application database
  │    ├── artifact metadata
  │    ├── public certificate inventory and owner assignments
  │    ├── signing requests and approvals
  │    ├── safe request events and audit evidence
  │    └── transactional outbox
  ├── private artifact store
  │    ├── opaque object keys
  │    ├── 0700 directories / 0600 files
  │    └── atomic writes and streaming hashes
  ├── internal artifact capability broker
  └── SigningClient over private Unix socket
         │
         ▼
      yargi-signerd
        ├── internal HTTP API
        ├── durable local job/event journal
        ├── local credential registry
        ├── one serial worker per physical token
        ├── PIN challenge and in-memory decryptor
        ├── exact PKCS#11 token worker ──→ SoftHSM, then one approved physical token
        └── narrow IPC supervisor
               ├── sandboxed PDF prepare/finalize worker
               └── sandboxed independent validator worker
```

### Trust boundaries

- The browser trusts FastAPI, not `yargi-signerd` directly.
- FastAPI authenticates owners and controls artifact access.
- `yargi-signerd` independently validates immutable manifest bindings and input hashes.
- The format and independent-validator workers run as separate unprivileged processes with no PKCS#11 library, token device, signer registry, challenge key, or signer configuration access.
- The format worker cannot select or configure arbitrary hardware.
- The token worker signs only a locally generated, schema-validated approved signing plan received over narrow authenticated local IPC.
- Chat, MCP, and LLM tool paths receive no signing capability.

---

## 5. Credential lifecycle

### 5.1 Module allowlist

Add a signer-owned configuration file, readable only by the signer service account, containing module aliases rather than caller-provided paths:

```yaml
pkcs11_modules:
  - alias: softhsm-test
    path: /approved/path/libsofthsm2.so
    sha256: <expected-library-sha256>
    modes: [test]
  - alias: approved-token-driver
    path: /approved/path/vendor-pkcs11.so
    sha256: <expected-library-sha256>
    modes: [hardware]
```

Startup and every credential probe must verify:

- absolute canonical path;
- no unexpected symlink change;
- expected owner and mode;
- SHA-256 match;
- mode allowlist;
- no API-supplied module path.

### 5.2 `yargi-signerctl` commands

Implement the following local commands:

```text
yargi-signerctl modules list
yargi-signerctl credentials probe --module <allowlisted-alias>
yargi-signerctl credentials enroll --module <alias> --slot <slot-id> \
  --certificate-sha256 <fingerprint> --key-id <hex>
yargi-signerctl credentials list
yargi-signerctl credentials verify <service-credential-id>
yargi-signerctl credentials disable <service-credential-id>
yargi-signerctl credentials enable <service-credential-id>
yargi-signerctl credentials remove <service-credential-id>
```

Rules:

- `probe` may display full local token selectors only to the administrator running the command.
- `enroll` rejects multiple matching tokens, certificates, or keys.
- `enroll` confirms that the certificate public key and private key match, using a non-document proof operation when necessary.
- `remove` requires an explicit interactive confirmation and is blocked while unreconciled jobs reference the credential.
- no command prints a PIN or private-key material;
- normal command output masks token serials and certificate subjects unless `--local-details` is explicitly used from an interactive TTY;
- all mutations write safe administrative audit metadata.

### 5.3 Signer credential record

Persist locally in the signer registry:

- `service_credential_id`;
- signer-local `physical_token_queue_key`, shared by every credential on the same physical token;
- module alias and verified module digest;
- slot ID;
- full token serial, signer-local only;
- token label, signer-local only;
- certificate DER;
- certificate SHA-256 fingerprint;
- private-key `CKA_ID`;
- public-key algorithm and size;
- certificate validity dates;
- enrollment/probe timestamps;
- enabled/disabled status;
- last safe health result;
- registry schema version.

Never persist:

- PIN or PIN ciphertext;
- private-key bytes;
- logged-in PKCS#11 sessions;
- application user/session identifiers.

### 5.4 FastAPI certificate synchronization and assignment

FastAPI periodically or administratively calls `GET /v1/credentials`, upserts only safe public metadata, and retains the stable `service_credential_id`.

Initial local administrative commands:

```text
python -m app.signing_admin sync-certificates
python -m app.signing_admin assign --certificate <id> --user <id>
python -m app.signing_admin revoke-assignment --certificate <id> --user <id>
```

Each assignment or revocation is audited. Normal users see only active certificates assigned to them.

---

## 6. Canonical user workflow

### Step 1 — Discover capability

The SPA calls `GET /api/signing/capabilities`. If signing is disabled, unavailable, or test-only, the UI reflects that state before accepting a PDF.

### Step 2 — Enumerate assigned certificates

The SPA calls `GET /api/signing/certificates`. FastAPI returns only active certificates assigned to the current user. No local token selectors are returned.

### Step 3 — Upload PDF

The user uploads one PDF using `POST /api/signing/artifacts`.

FastAPI:

- streams rather than buffering the full file;
- enforces the byte limit;
- checks PDF magic and MIME expectations;
- computes SHA-256 during upload;
- writes to an opaque private object path;
- performs safe preflight;
- stores metadata, not document bytes, in the database;
- returns immutable artifact metadata.

### Step 4 — Create request

The user calls `POST /api/signing/requests` with `artifact_id` and `certificate_id`.

FastAPI fixes the profile, algorithm, and policy version. It snapshots:

- owner;
- input SHA-256 and byte count;
- certificate fingerprint;
- policy version;
- artifact and certificate IDs.

### Step 5 — Review and confirm

The UI displays filename, byte count, input hash, certificate identity, profile, and test-mode warning. The owner calls `POST /api/signing/requests/{id}/confirm` and re-enters their current application password.

FastAPI creates a short-lived, single-use approval bound to the immutable request and creates an idempotent signer job.

### Step 6 — Queue

The job is routed exclusively by the credential's signer-local `physical_token_queue_key`. Two credentials backed by the same physical token always share one worker and lock. Different physical tokens may operate concurrently. `service_credential_id` must never be used as the queue key.

### Step 7 — PIN challenge

When the job is at the head of the queue and the exact token binding is present, `yargi-signerd` changes the job to `PIN_REQUIRED` and creates a one-time challenge.

The browser obtains it through `GET /api/signing/requests/{id}/pin-challenge`.

### Step 8 — PIN authorization

The PIN exists only in component-local browser memory. The browser encrypts it for the signer challenge and sends the compact JWE through `POST /api/signing/requests/{id}/pin-envelope`.

FastAPI relays it synchronously to `POST /v1/jobs/{id}/authorize`. There is no database or outbox record containing the JWE. Signerd atomically marks the challenge consumed and transitions the job to `AUTHORIZATION_CONSUMED` before handing decrypted PIN material to the token worker. If FastAPI times out or loses the response, it records only a safe `AUTHORIZATION_DELIVERY_UNKNOWN` application event, discards the JWE, and polls signer state. It must not invalidate, resend, or request a replacement challenge once consumption may have occurred.

### Step 9 — Sign exactly once

The token worker:

1. revalidates job version, expiry, input hash, policy, and challenge;
2. re-probes the exact module, slot, token serial, certificate, and key;
3. opens a fresh PKCS#11 session;
4. decrypts the PIN in memory;
5. performs at most one `C_Login` attempt;
6. performs the approved signature operation once;
7. logs out and closes the session;
8. clears transient references.

Any failure after the signature may have executed becomes `OUTCOME_UNKNOWN`, `FAILED_POST_SIGN`, or `QUARANTINED`; it never causes automatic re-signing.

### Step 10 — Finalize and verify

The format worker assembles the CMS/PAdES output and runs:

1. local structural verification;
2. local cryptographic verification;
3. configured independent PAdES validation;
4. input/output hash and artifact checks.

Verification failure after token signing does not retry the token. Output is quarantined or marked failed according to the evidence available.

### Step 11 — Publish and download

FastAPI accepts output only through a job-scoped write capability, computes the output hash while streaming, records verification evidence, and marks the application request complete only after reconciliation with the signer.

After a separate short-lived fresh-auth download authorization bound to the request ID and output hash, the owner downloads through `GET /api/signing/requests/{id}/download` with `Cache-Control: private, no-store`.

---

## 7. State model and ownership

### 7.1 Canonical states

Reuse the existing Go state vocabulary:

```text
UPLOADED
VALIDATED
AWAITING_OWNER_CONFIRMATION
QUEUED
WAITING_FOR_TOKEN
PIN_REQUIRED
AUTHORIZATION_CONSUMED
SIGNING
TIMESTAMPING             # disabled in the initial slice
VERIFYING
COMPLETED

REJECTED_INPUT
DECLINED
CANCELLED
CONFIRMATION_EXPIRED
APPROVAL_EXPIRED
PIN_WINDOW_EXPIRED
PIN_REJECTED
TOKEN_LOCKED
TOKEN_UNAVAILABLE
FAILED_PRE_SIGN
FAILED_POST_SIGN
OUTCOME_UNKNOWN
QUARANTINED
```

### 7.2 State authority

- FastAPI is authoritative for user-visible request ownership, upload, confirmation, and download authorization.
- `yargi-signerd` is authoritative from signer-job creation through token execution and verification.
- FastAPI never invents a signer transition; it projects durable signer events.
- A signer job is created initially in `QUEUED` after FastAPI has recorded valid owner confirmation.
- Approval and execution deadlines are rechecked before `PIN_REQUIRED` and immediately before `SIGNING`; an expired authorization fails before any token call.
- Extend the current Go state vocabulary with nonterminal `AUTHORIZATION_CONSUMED` and terminal `APPROVAL_EXPIRED`.
- Permit `PIN_REQUIRED → AUTHORIZATION_CONSUMED` only after durable one-time challenge consumption. While in `AUTHORIZATION_CONSUMED`, the worker may revalidate the token, open a session, and attempt one login; it may transition to `PIN_REJECTED`, `TOKEN_LOCKED`, `TOKEN_UNAVAILABLE`, or `FAILED_PRE_SIGN` without implying that a signature ran. User cancellation is no longer accepted once authorization is consumed.
- Permit `AUTHORIZATION_CONSUMED → SIGNING` only in a durable compare-and-swap immediately before the approved PKCS#11 `C_Sign` operation may execute.
- Permit `QUEUED`, `WAITING_FOR_TOKEN`, and `PIN_REQUIRED` to transition to `APPROVAL_EXPIRED`. Do not silently refresh approvals.
- Recovery from `AUTHORIZATION_CONSUMED` without a `SIGNING` claim must never reuse the consumed PIN; transition to `FAILED_PRE_SIGN` with safe code `AUTHORIZATION_LOST_BEFORE_SIGNING` and require a new owner-approved attempt rather than auto-requeueing.
- `COMPLETED` requires output artifact hash plus required verification evidence in both systems.

### 7.3 Cancellation

- Cancellation is allowed only before PIN authorization is consumed. Once the job reaches `AUTHORIZATION_CONSUMED`, cancellation returns `409 Conflict` even if `C_Sign` has not yet started.
- Browser disconnection does not cancel an operation.
- Once token signing may have started, cancellation returns `409 Conflict` and the job continues to a known or ambiguous terminal state.

### 7.4 Retry and idempotency

- Job creation is idempotent by command ID and canonical payload hash.
- Repeating the same idempotency key with identical immutable input returns the original job.
- Reusing it with different input returns `409 Conflict`.
- `PIN_REJECTED`, `TOKEN_LOCKED`, `FAILED_POST_SIGN`, `OUTCOME_UNKNOWN`, and `QUARANTINED` are never automatically retried.
- A new attempt requires a new request/attempt ID and fresh owner confirmation.

---

## 8. API contracts

All JSON request models reject unknown fields. All timestamps use RFC 3339 UTC. IDs are opaque UUIDs or equivalent nonsequential identifiers. Error responses expose safe codes and `{detail}` text, never raw PKCS#11/vendor errors.

### 8.1 Browser-facing FastAPI routes

#### `GET /api/signing/capabilities`

Returns safe feature metadata:

```json
{
  "enabled": true,
  "mode": "softhsm",
  "test_only": true,
  "service_available": true,
  "profile": "PAdES_BASELINE_B_B",
  "algorithm": "RSA_PKCS1_SHA256",
  "policy_version": "pades-b-b-rsa-sha256-v1",
  "max_upload_bytes": 26214400,
  "pin_window_seconds": 90
}
```

No module, socket, token, key, or internal validator details are returned.

#### `GET /api/signing/challenge-keyset`

Returns only the active and overlap-period signerd challenge-verification public JWKs whose RFC 7638 thumbprints are pinned in FastAPI configuration. FastAPI refuses startup when signerd advertises an unpinned key. Rotation adds the new key to the allowlist before issuance switches, retains the old verification key through the maximum challenge lifetime, and removes it only after all old challenges expire.

#### `GET /api/signing/certificates`

Returns active certificates assigned to the current owner:

```json
{
  "items": [
    {
      "id": "certificate-id",
      "subject_display": "E*** A***",
      "issuer_display": "Test CA",
      "serial_suffix": "A1B2",
      "fingerprint_suffix": "9F21D083",
      "not_before": "2026-01-01T00:00:00Z",
      "not_after": "2027-01-01T00:00:00Z",
      "public_key_type": "RSA",
      "status": "available",
      "test_only": true
    }
  ]
}
```

#### `POST /api/signing/artifacts`

- Content type: `multipart/form-data`.
- One `file` field only.
- Requires session authentication, CSRF token, exact Origin/Host validation, and upload rate limits.
- Streams to private storage and returns `201 Created`.

Response:

```json
{
  "id": "artifact-id",
  "display_filename": "petition.pdf",
  "mime_type": "application/pdf",
  "byte_count": 123456,
  "sha256": "hex-sha256",
  "preflight_status": "accepted",
  "created_at": "2026-07-19T12:00:00Z",
  "expires_at": "2026-08-18T12:00:00Z"
}
```

Rejected content is deleted immediately and returns a safe `400`, `413`, or `422` response.

#### `GET /api/signing/artifacts/{artifact_id}`

Returns owner-filtered metadata only. Cross-user and nonexistent IDs both return `404`.

#### `DELETE /api/signing/artifacts/{artifact_id}`

Deletes only an unreferenced input artifact when retention rules permit. Completed, quarantined, retained, or referenced artifacts cannot be deleted through this route.

#### `POST /api/signing/requests`

Request:

```json
{
  "artifact_id": "artifact-id",
  "certificate_id": "certificate-id"
}
```

Required header:

```text
Idempotency-Key: opaque-client-generated-value
```

FastAPI supplies profile and algorithm. Caller-provided policy fields are rejected.

Response: `201 Created` or the existing idempotent request.

#### `GET /api/signing/requests`

Returns the current owner’s paginated history with safe state, timestamps, certificate display data, and artifact metadata.

#### `GET /api/signing/requests/{request_id}`

Returns the immutable manifest, current state, safe timeline, cancellation eligibility, and output metadata when available.

#### `POST /api/signing/requests/{request_id}/confirm`

Request:

```json
{
  "current_password": "transient-value"
}
```

Requirements:

- current owner;
- request in `AWAITING_OWNER_CONFIRMATION`;
- recent session plus fresh password validation;
- exact CSRF and Origin/Host checks;
- approval bound to request ID, input hash, certificate fingerprint, policy version, and expiry.

The plaintext account password is consumed by existing authentication logic and is never added to signing records or audit payloads.

Successful confirmation queues the immutable signer job and returns `202 Accepted`.

#### `GET /api/signing/requests/{request_id}/events?after={sequence}`

Initial implementation returns monotonic JSON events for polling. SSE may be added later without changing event semantics.

#### `GET /api/signing/requests/{request_id}/pin-challenge`

Allowed only when:

- owner matches;
- request is `PIN_REQUIRED`;
- challenge is active and unconsumed.

Response:

```json
{
  "challenge_id": "challenge-id",
  "challenge_jws": "compact-jws",
  "recipient_jwk": {},
  "expires_at": "2026-07-19T12:01:30Z"
}
```

The JWS payload binds request/job IDs, input hash, certificate fingerprint, policy version, nonce, expiry, key ID, allowed JWE `alg`/`enc`, and the exact recipient JWK or its RFC 7638 thumbprint. The SPA verifies the JWS using a deployment-pinned signerd public key/keyset and rejects unknown keys, algorithms, or binding mismatches before displaying the PIN input.

#### `POST /api/signing/requests/{request_id}/pin-envelope`

Request:

```json
{
  "challenge_id": "challenge-id",
  "pin_jwe": "compact-jwe"
}
```

This route must bypass request-body logging and persistence middleware. FastAPI relays the body synchronously and then drops references. It must not place this command in the transactional outbox.

Responses:

- `202 Accepted` with `authorization_status: "consumed"` when signerd confirms consumption;
- `202 Accepted` with `authorization_status: "delivery_unknown"` when the response is ambiguous, after which the client polls request state and cannot submit another envelope;
- `409 Conflict` for wrong state, an already consumed challenge, or signing already started;
- `410 Gone` for an expired challenge;
- safe `422` for a malformed envelope;
- `503 Service Unavailable` only when FastAPI proves the request did not reach signerd; FastAPI must still reconcile signer state before permitting a replacement challenge.

#### `POST /api/signing/requests/{request_id}/cancel`

Cancels only before PIN authorization is consumed. Returns `409` in `AUTHORIZATION_CONSUMED`, `SIGNING`, or any later state.

#### `POST /api/signing/requests/{request_id}/download-authorization`

Requires fresh password authentication or approved WebAuthn and returns a short-lived, single-use server-side authorization bound to owner, request ID, output artifact ID, output SHA-256, and expiry. No reusable artifact URL or storage credential is returned.

#### `GET /api/signing/requests/{request_id}/download`

Available only to the owner in `COMPLETED` with the active download authorization. Streams the final artifact and sets:

```text
Cache-Control: private, no-store
X-Content-Type-Options: nosniff
Content-Disposition: attachment; filename="<sanitized-name>-signed.pdf"
```

### 8.2 Internal `yargi-signerd` API

The internal API is available only over the private signer transport. It never accepts application session cookies. For the single-host design, use a dedicated Unix group plus OS peer-credential validation (`SO_PEERCRED` or the platform-equivalent) and an application-to-signer command-authentication key. The signer accepts only the configured FastAPI service identity and rejects other local peers even if they can reach the socket. The artifact broker applies the reciprocal identity check. Multi-host operation requires private mTLS with pinned client/server identities instead.

The selected single-host design is:

- a dedicated `yargi-signing` Unix group containing only the FastAPI and signerd service accounts;
- signer socket parent owned by the signer account and `yargi-signing`, mode `0710` or an equivalently reviewed traverse-only group mode;
- signer socket owned by the signer account and `yargi-signing`, mode `0660`;
- connection peer UID captured before HTTP handling through `SO_PEERCRED` or the platform equivalent and attached to request context;
- an allowlist containing only the configured FastAPI service UID;
- asymmetric service-request signatures: FastAPI signs and signerd pins its public key; signerd separately signs artifact-broker calls and FastAPI pins that public key;
- each signature binds key ID, method, canonical path/query, body SHA-256, timestamp, and nonce;
- bounded clock skew, one-use nonce replay protection, constant-time verification, and overlap-period key rotation.

Filesystem reachability alone is never authorization. The current signer-owned `0700` directory and `0600` socket cannot simply be loosened ad hoc. Phase 3A must implement and test this identity, mode, peer-validation, request-signature, nonce, rotation, and startup contract. Multi-host operation replaces this mechanism with private mTLS and replay-safe signed commands.

#### `GET /v1/health`

Keep the existing behavior and add safe readiness fields only when they cannot expose credential inventory.

#### `GET /v1/credentials`

Returns safe inventory for FastAPI reconciliation:

```json
{
  "items": [
    {
      "id": "service-credential-id",
      "certificate_fingerprint_sha256": "full-public-fingerprint",
      "subject_display": "E*** A***",
      "issuer_display": "Test CA",
      "serial_suffix": "A1B2",
      "not_before": "2026-01-01T00:00:00Z",
      "not_after": "2027-01-01T00:00:00Z",
      "public_key_type": "RSA",
      "public_key_bits": 2048,
      "mode": "softhsm",
      "status": "available",
      "last_checked_at": "2026-07-19T12:00:00Z"
    }
  ]
}
```

Do not return module paths, module digests, slot IDs, full token serials, token labels, or `CKA_ID` values.

#### `POST /v1/jobs`

Creates an idempotent signer job after owner confirmation.

Required headers:

```text
Idempotency-Key: <command-id>
Content-Type: application/json
```

Request:

```json
{
  "command_id": "command-id",
  "job_id": "deterministic-signer-job-id",
  "request_id": "application-request-id",
  "expected_request_version": 4,
  "credential_id": "service-credential-id",
  "certificate_fingerprint_sha256": "hex-sha256",
  "policy_version": "pades-b-b-rsa-sha256-v1",
  "profile": "PAdES_BASELINE_B_B",
  "algorithm": "RSA_PKCS1_SHA256",
  "input": {
    "artifact_id": "input-artifact-id",
    "byte_count": 123456,
    "sha256": "hex-sha256"
  },
  "output": {
    "artifact_id": "reserved-output-artifact-id",
    "max_byte_count": 52428800
  },
  "approval": {
    "approval_id": "approval-id",
    "approved_at": "2026-07-19T12:00:00Z",
    "expires_at": "2026-07-19T12:05:00Z"
  },
  "nonce": "one-time-random-value",
  "expires_at": "2026-07-19T12:05:00Z"
}
```

Rules:

- `job_id` is deterministically derived by FastAPI from the durable command ID or otherwise reserved before dispatch, so an ambiguous create response can be reconciled without a secret lookup token;
- signer policy must exactly match the request;
- artifact bearer capabilities are not part of the durable canonical job payload;
- signerd requests fresh, job-bound transfer capabilities only when it is ready to fetch input or store output, through the authenticated artifact-broker control protocol;
- signerd rehashes fetched input and checks byte count;
- raw capabilities are never journaled, logged, placed in the outbox, or copied into safe events;
- duplicate command plus same canonical payload returns the existing job;
- duplicate command plus different canonical payload returns `409`.

Response:

```json
{
  "id": "signer-job-id",
  "request_id": "application-request-id",
  "state": "QUEUED",
  "version": 1,
  "event_sequence": 1,
  "created_at": "2026-07-19T12:00:00Z"
}
```

#### `GET /v1/commands/{command_id}`

Reconciles an ambiguous `POST /v1/jobs` result and returns the deterministic job ID plus safe command status. It reveals no artifact capability or secret payload. Only the authenticated FastAPI service identity may call it.

#### `GET /v1/jobs/{job_id}`

Returns safe state and evidence metadata:

- job and request IDs;
- state and version;
- credential public fingerprint;
- input hash and byte count;
- policy version;
- queue/sign/verify timestamps;
- safe failure code;
- output hash/size when present;
- validator names, versions, and normalized outcomes;
- cancellation eligibility;
- current event sequence.

#### `GET /v1/jobs/{job_id}/events?after={sequence}`

Returns durable monotonic events. Events contain no PIN, capability token, full token serial, key ID, raw vendor error, document content, or private path.

#### `GET /v1/jobs/{job_id}/pin-challenge`

Creates or returns the active one-time challenge only when the job is `PIN_REQUIRED`. Repeated reads return the same unexpired challenge; they do not create multiple usable challenges.

#### `POST /v1/jobs/{job_id}/authorize`

Request:

```json
{
  "challenge_id": "challenge-id",
  "pin_jwe": "compact-jwe"
}
```

The handler validates the envelope and, in one durable transaction, marks the challenge consumed and transitions the job to `AUTHORIZATION_CONSUMED` before passing decrypted PIN material directly to the waiting token worker through a bounded in-memory handoff. The worker may revalidate/open/login in this state and records `SIGNING` through a durable compare-and-swap immediately before `C_Sign` may execute. Neither plaintext nor ciphertext is written to the journal. Repeating the call after an ambiguous client timeout returns the already-consumed safe status and never performs a second login or signature.

#### `POST /v1/jobs/{job_id}/cancel`

Records cancellation only before PIN authorization is consumed. It is idempotent for an already-cancelled job and returns `409` in `AUTHORIZATION_CONSUMED`, `SIGNING`, or any later state.

### 8.3 Internal artifact capability broker

Document bytes must not be included in JSON APIs or exposed through storage paths. Implement a separate internal-only artifact broker transport, preferably a second Unix socket inaccessible from the public listener.

#### `POST /internal/signing/capabilities`

Authenticated signer-to-broker control call that mints one just-in-time capability only after the broker verifies the deterministic job ID, artifact reservation, expected method, hash/size constraints, current job phase, and signer peer identity. The response is never persisted by signerd and has a bounded lifetime appropriate to the immediate transfer.

#### `GET /internal/signing/artifacts/{artifact_id}`

Requires a read capability bound to:

- HTTP method;
- artifact ID;
- signer job ID;
- expected input SHA-256 and byte count;
- nonce;
- expiry.

The broker streams bytes. Signerd independently verifies the declared hash and size.

#### `PUT /internal/signing/artifacts/{artifact_id}`

Requires a one-use write capability bound to:

- HTTP method;
- reserved output artifact ID;
- signer job ID;
- maximum byte count;
- nonce;
- expiry.

The broker computes SHA-256 while streaming to a temporary private file, atomically renames on success, and returns the final hash and size. Reuse returns `409` or `410`.

Capabilities are bearer secrets. They must be short-lived, method-scoped, artifact-scoped, job-scoped, omitted from logs, and stored only as salted hashes when replay tracking is required. They are minted just in time after the broker authenticates the signer peer and verifies the durable job/artifact binding; they are not included in `POST /v1/jobs`, the application outbox, or retry payloads. Before the token operation starts, signerd must prove that input is already fetched and that a fresh output capability can cover the bounded completion window. After possible `C_Sign`, an expired output capability may be replaced only for storage of the already finalized bytes; it must never trigger a new signature.

---

## 9. Persistence and data model

### 9.1 FastAPI database

Add Alembic before signing tables. Create:

- `SigningArtifact`;
- `SigningCertificate`;
- `SigningCertificateAssignment`;
- `SigningRequest`;
- `SigningApproval`;
- `SigningDownloadAuthorization`;
- `SigningRequestEvent`;
- `SigningAuditEvent`;
- `SigningOutbox`;
- artifact capability replay/consumption records.

Document bytes remain outside SQLite.

Important constraints:

- unique active owner assignment per certificate in v1;
- immutable request manifest after `QUEUED`;
- unique idempotency key scoped to owner and operation;
- monotonic event sequence per request;
- append-only audit events protected against ordinary update/delete;
- no cascade deletion of retained completed evidence;
- no PIN or PIN ciphertext column anywhere;
- download authorizations store only a one-way token hash or server-side nonce, output binding, expiry, and consumed/revoked timestamps.

### 9.2 Signer journal

Use a dedicated signer-owned SQLite database in WAL mode with a pinned, reviewed pure-Go driver unless an ADR approves an equivalent transactional store. The database and parent directory use `0600` and `0700` permissions respectively.

Tables:

- `credentials` — exact signer-local tuple, `physical_token_queue_key`, and certificate metadata;
- `jobs` — immutable manifest snapshots and current state;
- `job_events` — monotonic append-only safe events;
- `commands` — command ID, canonical payload hash, and idempotent result;
- `pin_challenges` — challenge metadata and consumed/expired state, never PIN/JWE;
- `plan_authorizations` — canonical plan hash, immutable bindings, nonce, expiry, and consumed state, never a reusable generic signing grant;
- `validation_evidence` — normalized verifier results and hashes;
- `administrative_events` — safe enrollment/enable/disable/remove metadata;
- `schema_migrations`.

Durability rules:

- persist job creation before queue submission;
- atomically persist challenge consumption and `AUTHORIZATION_CONSUMED` before PIN handoff, then compare-and-swap to `SIGNING` immediately before `C_Sign` may execute;
- persist terminal result and event atomically;
- force `OUTCOME_UNKNOWN` during recovery when a crash occurred after `SIGNING` claim without durable final evidence;
- terminate recovered `AUTHORIZATION_CONSUMED` jobs without replay, discard any lost PIN authorization, and require a new approved attempt;
- never auto-enqueue `OUTCOME_UNKNOWN`;
- capabilities are not stored raw beyond the minimum active transfer window.

### 9.3 Safe audit metadata

Allowed:

- request/job/command IDs;
- owner ID where appropriate in FastAPI only;
- input/output hashes and sizes;
- public certificate fingerprint and masked serial suffix;
- policy and software versions;
- safe state transitions;
- normalized failure codes;
- validator names, versions, and outcomes;
- timestamps.

Forbidden:

- PIN or PIN JWE;
- account password;
- document bytes or extracted text;
- full token serial in application-visible records;
- private-key ID in application-visible records;
- artifact capabilities;
- raw session tokens;
- raw PKCS#11/vendor errors;
- arbitrary module paths in browser-visible records.

---

## 10. Signing pipeline refactor

The existing `SignBaselineBB` function combines preparation, token signing, finalization, and local verification. Refactor it behind owned internal interfaces:

```text
PDF preflight
  → prepare incremental PDF placeholder and ByteRange
  → prepare deterministic CMS signed attributes and digest
  → create immutable SigningPlan
  → exact token worker signs the approved digest once
  → assemble CMS with returned signature
  → finalize PDF Contents/ByteRange
  → local structural and cryptographic verification
  → independent validator
  → output capability upload
```

### `SigningPlan`

The internal plan must bind:

- schema version;
- job/request/command IDs;
- input artifact ID, hash, and byte count;
- exact prepared content ranges or their canonical digest;
- certificate fingerprint;
- algorithm and PKCS#11 mechanism policy;
- PAdES/CAdES profile and policy version;
- signed-attribute DER hash;
- format-engine version;
- expiry and nonce.

The plan is internal only. It is never accepted from the browser or FastAPI as an arbitrary digest-sign request.

### Supervisor plan authorization

The sandboxed format worker has no standing authority to invoke the token worker. It returns a candidate `SigningPlan` to a trusted signer-side plan authorizer inside the signerd supervisor. The authorizer independently verifies:

- the job and command are durable, approved, unexpired, and in the expected version/state;
- the plan's input artifact ID, byte count, and SHA-256 equal the immutable job manifest;
- the prepared content ranges and digest are reproducible from the broker-fetched input or a supervisor-verified preparation transcript;
- the certificate fingerprint, profile, algorithm, mechanism, and policy exactly match the credential and server policy;
- the format-engine version is allowlisted;
- the canonical plan hash, nonce, and expiry are unique and bounded.

After validation, the supervisor issues a short-lived, single-use `PlanAuthorization` authenticated with a supervisor-held key unavailable to sandboxed workers. It binds job ID, command ID, job version, input hash/size, prepared-content digest, signed-attribute digest, certificate fingerprint, policy, mechanism, canonical plan hash, nonce, and expiry.

The token worker accepts plans only over its private supervisor IPC and only with a valid `PlanAuthorization`. It atomically consumes the authorization together with the `AUTHORIZATION_CONSUMED → SIGNING` compare-and-swap before `C_Sign`; replay, expiry, job-version mismatch, and plan-hash mismatch fail closed. Format and validator workers cannot call the token worker directly or obtain the authorization key.

Final validation evidence is bound to job ID, plan hash, input hash, final artifact SHA-256, validator identity/version, trust-policy version, and normalized outcome before `COMPLETED` is permitted.

### Process-isolation contract

PDF preflight, preparation, finalization, and independent validation run outside the token-bearing signer process in separately sandboxed workers. Those workers run under different unprivileged identities or equivalent OS-enforced sandboxes and have:

- no PKCS#11 module or device access;
- no signer credential registry access;
- no PIN challenge/decryption key access;
- no arbitrary network access;
- bounded CPU, memory, file size, process, and wall-clock resources;
- a private scratch directory destroyed after the job;
- narrow authenticated local IPC carrying only versioned bounded messages.

The token-worker IPC is not reachable by the sandboxed workers. Only the trusted supervisor can submit a candidate plan plus a valid one-use `PlanAuthorization`. The token worker returns only the signature result plus safe metadata. It cannot alter the PDF, profile, certificate, or plan. The format worker cannot choose a module, slot, token, key, invoke PKCS#11, mint plan authorization, or directly address the token worker. The external validator is also sandboxed and receives only the finalized document plus expected evidence metadata.

### Verification and quarantine policy

Before any Phase 2D output can reach `COMPLETED` or become downloadable, the combined authoritative Phase 1B gate must be closed and recorded in `EIMZA_BUILD_LEDGER.md`. That closure requires:

- the Phase 1A format/trust prerequisites: versioned PDF corpus, negative/fuzz and resource-bound evidence, two independent full PAdES validators, explicit `valid | invalid | indeterminate` trust outcomes, fail-closed release policy, bounded authenticated revocation handling, and updated interoperability evidence;
- the Phase 2B/2C selector and lifecycle prerequisites: exact module/slot/token/certificate/`CKA_ID` binding, no first-item fallback, one physical-token worker, fresh session per attempt, and one login attempt;
- resolution of the current quarantine status in the interoperability matrix and build ledger.

Until that gate closes, the SoftHSM workflow may produce only quarantined engineering artifacts unavailable to normal users. It must not set the application request to `COMPLETED` or expose the normal download endpoint.

After Phase 1B closure, require for the SoftHSM slice:

- local PAdES structural check;
- local CMS cryptographic check;
- both required independent PAdES validator outcomes;
- explicit certificate trust/revocation outcome according to the approved test policy.

If the token operation succeeded but finalization, validation, or trust policy fails, do not sign again. Preserve or quarantine safe evidence according to state and retention policy.

---

## 11. Repository changes

### 11.1 Go service

Create or expand:

```text
signing-service/cmd/yargi-signerctl/main.go
signing-service/internal/api/contracts.go
signing-service/internal/api/handlers.go
signing-service/internal/api/middleware.go
signing-service/internal/api/server.go
signing-service/internal/artifacts/client.go
signing-service/internal/audit/audit.go
signing-service/internal/config/config.go
signing-service/internal/credentials/model.go
signing-service/internal/credentials/probe.go
signing-service/internal/credentials/registry.go
signing-service/internal/jobs/model.go
signing-service/internal/jobs/service.go
signing-service/internal/jobs/recovery.go
signing-service/internal/jobs/state.go
signing-service/internal/jobs/queue.go
signing-service/internal/pades/prepare.go
signing-service/internal/pades/finalize.go
signing-service/internal/workeripc/contracts.go
signing-service/internal/planauth/authorizer.go
signing-service/cmd/yargi-format-worker/main.go
signing-service/cmd/yargi-validator-worker/main.go
signing-service/internal/persistence/db.go
signing-service/internal/persistence/migrations.go
signing-service/internal/pin/challenge.go
signing-service/internal/pin/envelope.go
signing-service/internal/policy/policy.go
signing-service/internal/token/module.go
signing-service/internal/token/selector.go
signing-service/internal/token/session.go
signing-service/internal/token/worker.go
signing-service/internal/token/errors.go
signing-service/internal/validation/local.go
signing-service/internal/validation/external.go
signing-service/test/integration/
signing-service/testdata/
```

The `third_party/eimza-go` packages remain quarantined implementation details and must not be imported directly by API handlers.

### 11.2 FastAPI

Create or modify:

```text
server/alembic.ini
server/migrations/env.py
server/migrations/versions/0001_legacy_schema_baseline.py
server/migrations/versions/0002_signing_subsystem.py
server/app/config.py
server/app/db.py
server/app/models.py
server/app/security.py
server/app/main.py
server/app/api/signing.py
server/app/artifact_store.py
server/app/artifact_capabilities.py
server/app/signing_admin.py
server/app/signing_audit.py
server/app/signing_client.py
server/app/signing_policy.py
server/app/signing_state.py
server/app/signing_workflow.py
server/app/api/account.py
server/tests/test_signing_*.py
```

### 11.3 React

Create or modify:

```text
web/src/state/app.tsx
web/src/App.tsx
web/src/components/Sidebar.tsx
web/src/components/Header.tsx
web/src/components/Icon.tsx
web/src/lib/api.ts
web/src/lib/types.ts
web/src/lib/signing.ts
web/src/views/SigningView.tsx
web/src/components/signing/SigningUploadCard.tsx
web/src/components/signing/SigningRequestCard.tsx
web/src/components/signing/SigningStatusTimeline.tsx
web/src/components/signing/PinApprovalDialog.tsx
web/src/test/setup.ts
web/vitest.config.ts
```

Use a maintained JOSE library for JWE construction. Do not implement cryptography manually.

### 11.4 Deployment and configuration

Add:

- signer config path and module allowlist settings;
- signer journal/registry path;
- challenge signing-key path and deployment-pinned public keyset/rotation configuration;
- authenticated signer API and artifact-broker Unix socket identities, groups, peer checks, and command-authentication keys;
- sandboxed format-worker and validator-worker identities, IPC paths, resource limits, and filesystem/device denials;
- supervisor plan-authorization key, private token-worker IPC, authorization TTL, and replay store;
- upload/output limits;
- approval and PIN TTLs;
- independent validator executable/configuration and expected digest/version;
- systemd or launchd service definitions as appropriate;
- SoftHSM test setup scripts that contain no production credentials;
- startup checks and a global signing kill switch.

All feature flags remain disabled by default.

---

## 12. Implementation phases

The numbering expands the authoritative project phases rather than replacing them.

## Phase 1A — Format conformance and trust prerequisites

**Goal:** Complete the format-validator and trust-policy portion of authoritative Phase 1. This is a partial prerequisite, not authority to mark Phase 1 complete or release output.

### Work

- expand and version a controlled PDF corpus covering supported classic-xref variants plus explicitly rejected encrypted, xref-stream, hybrid-reference, prior-incremental, AcroForm, existing-signature, malformed-xref, malformed-object, oversized, and adversarial lexical cases;
- add seeded Go fuzz targets for PDF parsing, lexical scanning, xref/xref-stream handling, PAdES preflight, ByteRange/Contents extraction, CMS decoding, and local verification;
- enforce fuzz/resource invariants: no panic, unbounded allocation, unbounded decompression, path/file escape, silent policy downgrade, or acceptance outside the declared corpus policy;
- preserve minimized crash/rejection inputs as regression fixtures and record fuzz duration, seed corpus digest, Go version, and findings in the build ledger;
- complete the second independent full PAdES validator integration and corpus evidence;
- implement explicit certificate-chain and revocation outcomes: `valid`, `invalid`, or `indeterminate`;
- fail closed for output release on `invalid` or `indeterminate`;
- verify CRL signatures and chain binding wherever CRL evidence is used;
- bound OCSP/CRL schemes, hosts, sizes, redirects, and timeouts;
- record validator, trust-store, and policy versions;
- update `INTEROPERABILITY_MATRIX.md` and `EIMZA_BUILD_LEDGER.md` with the partial evidence while leaving authoritative Phase 1 open.

### Exit gate

The versioned controlled corpus and negative/fuzz suite pass with recorded reproducible evidence, two independent full PAdES validators accept every supported fixture, rejected classes fail safely, and trust/revocation semantics are explicit and fail closed. Output remains quarantined because authoritative Phase 1 also requires the exact selector, fresh-session, and one-worker-per-token evidence delivered by Phases 2B and 2C.

## Phase 2A — Persistence, configuration, and internal contracts

**Goal:** Establish durable service foundations before adding token operations.

### Work

- add signer configuration-file support and module allowlist schema;
- choose and pin the signer SQLite driver after license and vulnerability review;
- implement schema migrations, jobs, events, commands, challenges, validation evidence, and credential tables;
- define strict API contracts and normalized safe error codes;
- implement command canonicalization and idempotency;
- implement restart recovery classification;
- extend health with safe readiness status;
- keep all signing routes unreachable behind the disabled feature flag.

### Tests

- migration from empty database;
- migration rollback/forward policy;
- permission checks for config, DB, and parent directories;
- duplicate command with same/different payload;
- event ordering and transactional state changes;
- crash recovery before and after `SIGNING` claim;
- no secret fields in schema or logs.

### Exit gate

A process restart preserves jobs and events, ambiguous signing claims become `OUTCOME_UNKNOWN`, and no job is automatically replayed.

## Phase 2B — Exact SoftHSM credential enrollment

**Goal:** Replace upstream first-match behavior with exact local credential binding.

### Work

- add the reviewed PKCS#11 dependency and bounded wrapper;
- implement module digest verification;
- enumerate slots, tokens, certificates, and keys without fallback;
- implement exact certificate fingerprint and `CKA_ID` matching;
- implement certificate/private-key proof;
- implement `yargi-signerctl` commands;
- implement encrypted-at-rest host volume expectation and private registry modes;
- implement safe `GET /v1/credentials` inventory;
- provision a disposable SoftHSM token and test certificate.

### Tests

- zero, one, and multiple matching modules/slots/tokens/certificates/keys;
- wrong module digest;
- moved slot;
- changed token serial;
- duplicate certificate;
- certificate/key mismatch;
- disabled credential;
- expired certificate;
- no PIN in args, environment, output, journal, or logs;
- real SoftHSM login and proof operation.

### Exit gate

The selected SoftHSM credential is uniquely bound and a wrong token, certificate, or key cannot be silently selected.

## Phase 2C — Durable job API, queue, events, and PIN challenge

**Goal:** Make the service workflow operable without yet producing a final PDF.

### Work

- implement all internal job endpoints;
- wire persistent jobs into the existing queue using only `physical_token_queue_key`;
- add legal state-transition persistence, including explicit approval/execution expiry from queued and waiting states;
- add event cursor semantics;
- add cancellation boundary;
- implement signed ephemeral PIN challenges;
- add `AUTHORIZATION_CONSUMED` state and recovery semantics;
- implement JWE validation and bounded in-memory worker handoff;
- enforce one login attempt and no challenge retry;
- add safe PKCS#11 error mapping.

### Tests

- same-token serialization and cross-token parallelism, including two different credentials mapped to the same physical-token queue key;
- queue limits and backpressure;
- challenge expiry, reuse, wrong job, wrong hash, wrong certificate, and wrong policy;
- recipient JWK substitution, thumbprint mismatch, unknown key ID, unknown JOSE algorithm, and signing-key rotation overlap;
- malformed JWS/JWE;
- one login attempt for wrong PIN;
- crash after challenge consumption but before `SIGNING`, proving no PIN replay and no automatic retry;
- token lock and removal;
- cancellation races;
- sentinel PIN scan across logs, DB, events, panics, and crash output.

### Exit gate

Two concurrent same-token jobs never overlap, and a submitted PIN appears nowhere after the in-memory authorization attempt ends.

## Phase 1B gate — Authoritative core-remediation closure

**Goal:** Close authoritative Phase 1 only after combining Phase 1A format/trust evidence with the credential and token-lifecycle evidence from Phases 2B and 2C.

### Required evidence

- every supported operation uses exact module, slot, token serial, certificate fingerprint, and `CKA_ID` selectors;
- no first-item fallback is reachable;
- credentials on the same physical token share one queue and lock;
- each attempt uses a fresh PKCS#11 session and one PIN login attempt;
- unsupported profiles and algorithms fail closed;
- the versioned PDF corpus, negative/fuzz suite, two-validator matrix, parser resource bounds, and explicit trust/revocation outcomes from Phase 1A remain passing;
- all evidence and versions are recorded in `EIMZA_BUILD_LEDGER.md`.

### Exit gate

Only after these combined criteria pass may the build ledger mark authoritative Phase 1 complete and permit Phase 2D to produce test-only `COMPLETED` output. Otherwise Phase 2D output remains `QUARANTINED` engineering evidence.

## Phase 2D — Prepare, sign, finalize, verify, and artifact capabilities

**Goal:** Complete the standalone SoftHSM PAdES B-B vertical slice.

### Work

- refactor the CAdES/PAdES code into prepare and finalize stages;
- create and validate immutable signing plans and one-use supervisor `PlanAuthorization` records;
- implement an isolated test artifact store/broker and just-in-time capability issuer required by this phase; Phase 3A later replaces it with the FastAPI-owned production control plane without changing the protocol;
- implement artifact broker client and capability validation;
- implement sandboxed format and validator workers with narrow IPC and resource limits;
- rehash input before preparation;
- perform one SoftHSM signature;
- finalize PAdES output;
- run local structural and cryptographic verification;
- run configured independent validation;
- upload output through one-use capability;
- record output hash and normalized evidence;
- quarantine or fail closed on disagreement.

### Tests

- just-in-time capability issuance, expiry, reuse, method mismatch, artifact mismatch, job mismatch, hash mismatch, and size overflow;
- PDF corpus and malformed input cases;
- signed-byte mutation;
- forged, replayed, expired, wrong-job, wrong-plan-hash, and wrong-input supervisor plan authorizations;
- compromised format-worker simulation proving it cannot directly invoke the token worker or sign an arbitrary digest;
- failure before token sign;
- crash/failure immediately before, during, and after token sign;
- finalization failure after successful token operation;
- validator unavailable, rejects, disagrees, or times out;
- no automatic second signature in any post-sign failure.

### Exit gate

After Phase 1B closure, a SoftHSM job creates a PAdES B-B output accepted by both required independent validators and the approved trust policy, preserves complete durable evidence, and never repeats an ambiguous token operation. Before Phase 1B closure, the same pipeline may terminate only in `QUARANTINED` engineering output.

## Phase 3A — FastAPI migrations, private artifacts, and signer client

**Goal:** Add the authenticated application control plane and system of record.

### Work

- add and verify Alembic baseline against a populated database copy;
- create signing models and constraints;
- implement private streaming artifact storage;
- implement internal artifact broker and capability tokens;
- implement signer Unix-socket client with deadlines, dedicated group ownership, OS peer-credential validation, and command authentication;
- implement inventory synchronization and local owner-assignment command;
- implement outbox/reconciliation for all commands except PIN envelopes;
- update account export/deletion behavior;
- add startup invariants and disabled-mode behavior.

### Tests

- migration and restore;
- owner isolation and IDOR;
- path traversal and symlink attacks;
- Unix peer-credential, group, and command-authentication rejection for unauthorized local processes;
- atomic write and hash verification;
- capability replay and expiry;
- signer unavailable and stale event cursor;
- account deletion/retention constraints;
- no document bytes in SQLite, logs, or shared caches.

### Exit gate

FastAPI can privately store an input, create an immutable owner-scoped request, communicate with signerd, reconcile durable events, and prevent cross-user access.

## Phase 3B — Browser-facing signing routes and fresh authorization

**Goal:** Expose the complete safe workflow to an authenticated API client.

### Work

- implement all `/api/signing` routes;
- enforce CSRF, Origin/Host, rate, body, and upload limits;
- implement fresh password confirmation and one-use approvals;
- implement request idempotency;
- implement synchronous PIN-envelope relay with logging/persistence bypass;
- implement fresh-authenticated, one-use completed-output download authorization and download;
- add safe Turkish error/state copy contracts.

### Tests

- disabled feature behavior;
- current-password failure and approval expiry;
- immutable manifest after confirmation;
- cross-user artifact/certificate/request access returning `404`;
- illegal state operations;
- PIN relay timeout, delivery-unknown reconciliation, and no second authorization or login;
- no PIN/JWE in logs, exceptions, DB, outbox, audit, or account export;
- fresh download authorization expiry, one-use consumption, output-hash binding, and `Cache-Control: private, no-store` behavior.

### Exit gate

An API-only authenticated user can complete the SoftHSM flow, and all ownership, fresh-auth, CSRF, privacy, and state controls pass.

## Phase 4 — React signing experience

**Goal:** Provide an explicit user flow without weakening secret handling.

### Work

- add dedicated signing navigation and lazy-loaded view;
- show capability and unavoidable test-only banner;
- implement upload and immutable manifest review;
- implement assigned-certificate selection;
- implement password confirmation;
- implement queue/status timeline with polling first;
- implement pinned challenge-keyset verification, recipient-key/thumbprint and `alg`/`enc` binding checks, and JWE construction with a reviewed JOSE library;
- hold PIN only in component-local state;
- clear password/PIN values on submit, failure, timeout, cancel, unmount, navigation, and network error;
- disable cancellation once PIN authorization is consumed;
- implement fresh-authenticated, one-use owner-only output download;
- show explicit `OUTCOME_UNKNOWN` and no-auto-resubmit guidance.

### Tests

- upload and manifest display;
- certificate filtering;
- test-mode claims;
- challenge expiry, signer-key rotation, recipient-key substitution, and unknown-algorithm rejection;
- PIN clearing on every lifecycle exit;
- no PIN in global state, storage, URL, telemetry, toast, or debug payload;
- terminal/error state rendering;
- download behavior;
- no UYAP submission claim.

### Exit gate

The complete test-only workflow succeeds through the UI and browser inspection confirms no reusable secret remains after each PIN interaction.

## Phase 5 — Recovery, security, interoperability, and one-owner hardware pilot

**Goal:** Prove that replacing SoftHSM with one approved physical token preserves all safety properties.

### Work

- review the exact token model, reader, OS, driver path/version/hash, slot, serial, certificate, and `CKA_ID`;
- enroll locally through `yargi-signerctl`;
- add the token/driver combination to the support matrix;
- test removal, reinsertion, wrong token, moved slot, multiple certificates, wrong PIN, lock, process restart, driver crash, and failure around `C_Sign`;
- run controlled non-sensitive PDF corpus;
- validate output with at least two independent PAdES validators, including an ETSI-aware validator;
- perform privacy/redaction and audit review;
- complete incident, PIN-lock, token-replacement, kill-switch, and recovery runbooks.

### Exit gate

The pilot satisfies `PILOT_PROTOCOL.md`, no wrong credential can be selected, same-token calls serialize, external validators accept output, ambiguous operations are not retried, and privacy evidence passes review.

## Phase 6 — Trusted timestamp and PAdES B-T

**Goal:** Add the initial intended production profile only after B-B and hardware gates pass.

This phase is unchanged from the authoritative integration plan and remains blocked until:

- TSA and dependency provenance are approved;
- nonce and message-imprint binding are implemented;
- TSA signature, chain, EKU, policy, and time bounds are verified;
- egress is allowlisted and bounded;
- negative timestamp vectors pass;
- two independent validators accept final B-T output.

## Phase 7 — Production hardening and controlled rollout

**Goal:** Enable a limited approved deployment.

Required before enabling production:

- written legal and operational authorization;
- approved retention/deletion/legal-hold policy;
- hardened service identities and private transport;
- TLS/proxy/cookie/CSRF configuration;
- encrypted artifact and journal volumes/backups;
- backup/restore drill with hash and audit-chain verification;
- monitoring and alerting;
- supported token/driver matrix;
- real-token and B-T interoperability evidence;
- incident and credential-replacement runbooks;
- global kill switch tested.

---

## 13. Test and evidence matrix

| Layer | Required evidence |
|---|---|
| Credential registry | Exact tuple match; no first-item fallback; module digest enforcement; shared physical-token queue key across multiple credentials. |
| PKCS#11 lifecycle | Fresh session, one login attempt, full-operation serialization, logout/close. |
| Job journal | Transactional transitions, monotonic events, restart recovery, no replay after ambiguity. |
| PIN flow | Pinned challenge signature, recipient-key and JOSE algorithm binding, expiry, one use, delivery-unknown reconciliation, malformed/replay rejection, complete redaction scan. |
| Artifact store | Owner isolation, private modes, atomic writes, path containment, hashes, just-in-time capability scope. |
| Process isolation | PDF and validator workers cannot access PKCS#11, credential registry, challenge keys, token-worker IPC, or token devices; resource limits and IPC validation hold. |
| Plan authorization | Supervisor independently binds immutable job/input/plan/policy data; one-use authorization is unforgeable and atomically consumed before `C_Sign`. |
| PAdES/CAdES | Mandatory attributes, certificate binding, strict ByteRange, mutation rejection. |
| PDF corpus and fuzzing | Versioned supported/rejected fixtures, minimized regressions, seeded fuzz targets, bounded resources, no panics, and supported inputs differ only by a valid incremental signing update. |
| Independent validation | Two full PAdES validators and explicit fail-closed trust/revocation semantics before normal test-only completion or download. |
| FastAPI | IDOR, CSRF, Origin/Host, fresh auth, idempotency, retention, account operations. |
| React | Secret clearing, no browser persistence, state/error behavior, test-only claims. |
| Recovery | Fault injection before/during/after token call proves no duplicate signatures. |
| Operations | Backup/restore, audit verification, kill switch, token replacement, incident drills. |

CI must continue to run formatting, unit tests, race tests where supported, vet/static analysis, vulnerability scanning, dependency-boundary checks, and SBOM generation. Hardware and external-validator suites may run in separately controlled jobs but their evidence must be recorded in the build ledger.

---

## 14. Safe error taxonomy

Map internal failures to stable codes without leaking vendor strings:

```text
INVALID_REQUEST
POLICY_MISMATCH
INPUT_REJECTED
INPUT_HASH_MISMATCH
CAPABILITY_INVALID
CAPABILITY_EXPIRED
CREDENTIAL_NOT_FOUND
CREDENTIAL_DISABLED
TOKEN_NOT_PRESENT
TOKEN_MISMATCH
CERTIFICATE_MISMATCH
KEY_MISMATCH
CERTIFICATE_EXPIRED
CERTIFICATE_TRUST_INVALID
CERTIFICATE_TRUST_INDETERMINATE
APPROVAL_EXPIRED
EXECUTION_DEADLINE_EXPIRED
PIN_WINDOW_EXPIRED
PIN_REJECTED
AUTHORIZATION_LOST_BEFORE_SIGNING
AUTHORIZATION_DELIVERY_UNKNOWN
TOKEN_LOCKED
TOKEN_SESSION_FAILED
SIGNING_FAILED_PRE_OPERATION
SIGNING_OUTCOME_UNKNOWN
FINALIZATION_FAILED
LOCAL_VERIFICATION_FAILED
INDEPENDENT_VALIDATION_FAILED
OUTPUT_STORE_FAILED
SERVICE_UNAVAILABLE
```

Raw errors may be retained only in a separately protected operator diagnostic channel if approved by the privacy and logging design. They must never be sent to normal users or copied into audit evidence.

---

## 15. Observability

Expose safe metrics only:

- jobs by safe state and mode;
- queue depth and wait duration per opaque physical-token queue key;
- signing and verification duration histograms;
- token-present boolean;
- challenge issued/expired/rejected counts;
- normalized failure-code counts;
- reconciliation lag;
- artifact capability failure counts;
- validator availability and outcome counts.

Never label metrics with:

- user ID or email;
- document filename or content;
- PIN/JWE;
- full token serial;
- private-key ID;
- module path;
- artifact capability;
- unbounded request/job IDs.

Alerts are required for token lock risk, repeated credential mismatch, validator outage, `OUTCOME_UNKNOWN`, audit-chain failure, queue starvation, backup failure, and use of the kill switch.

---

## 16. Configuration additions

Suggested safe defaults:

```dotenv
SIGNING_ENABLED=false
SIGNING_MODE=disabled
SIGNING_SERVICE_SOCKET=/run/yargi-signerd/signerd.sock
SIGNING_SERVICE_EXPECTED_PEER_UID=
SIGNING_SERVICE_COMMAND_KEY_PATH=
SIGNING_ARTIFACT_BROKER_SOCKET=/run/yargi-asistan/signing-artifacts.sock
SIGNING_ARTIFACT_BROKER_EXPECTED_PEER_UID=
SIGNING_SERVICE_TIMEOUT_SECONDS=30
SIGNING_ARTIFACT_ROOT=./data/signing-artifacts
SIGNING_MAX_UPLOAD_BYTES=26214400
SIGNING_MAX_OUTPUT_BYTES=52428800
SIGNING_APPROVAL_TTL_SECONDS=300
SIGNING_PIN_WINDOW_SECONDS=90
SIGNING_MAX_PENDING_PER_USER=10

SIGNERD_CONFIG=/etc/yargi-signerd/signerd.yaml
SIGNERD_STATE_DIR=/var/lib/yargi-signerd
SIGNERD_DATABASE=/var/lib/yargi-signerd/signerd.db
SIGNERD_CHALLENGE_SIGNING_KEY=/etc/yargi-signerd/challenge-signing.key
SIGNERD_CHALLENGE_KEY_ID=
SIGNERD_CHALLENGE_PUBLIC_KEYSET=/etc/yargi-signerd/challenge-keyset.json
SIGNERD_PLAN_AUTHORIZATION_KEY=/etc/yargi-signerd/plan-authorization.key
SIGNING_CHALLENGE_ALLOWED_JWK_THUMBPRINTS=
SIGNING_PIN_JWE_ALG=
SIGNING_PIN_JWE_ENC=
SIGNERD_FORMAT_WORKER_SOCKET=/run/yargi-signerd/format-worker.sock
SIGNERD_VALIDATOR_WORKER_SOCKET=/run/yargi-signerd/validator-worker.sock
SIGNERD_EXTERNAL_VALIDATOR_PATH=
SIGNERD_EXTERNAL_VALIDATOR_SHA256=
```

Startup must fail closed when enabled if configuration, file ownership/modes, module hashes, database migrations, challenge keys, artifact broker, independent validator, or transport authentication do not satisfy the selected environment.

---

## 17. Acceptance criteria

### Complete SoftHSM implementation

The test-only implementation is complete only when all are true:

1. A local administrator enrolls an exact SoftHSM credential using `yargi-signerctl`.
2. No PIN or private-key material is persisted during enrollment.
3. FastAPI imports safe certificate metadata and assigns it to one owner.
4. That owner uploads a bounded PDF into private storage.
5. FastAPI records immutable hash, size, certificate, and policy snapshots.
6. Fresh password confirmation is required before queueing.
7. Same-token jobs serialize and other-token jobs may proceed independently.
8. The owner receives one expiring challenge and submits one encrypted PIN envelope.
9. One envelope causes at most one token login attempt.
10. Signerd independently rehashes the input and verifies the exact credential tuple.
11. Exactly one approved token signature operation is attempted.
12. The authoritative Phase 1B gate is closed in the build ledger, combining versioned corpus and negative/fuzz evidence, two independent full PAdES validators, and fail-closed trust/revocation semantics with exact selector, one-worker-per-token, fresh-session, and one-login-attempt evidence.
13. The PAdES B-B output passes local checks, both independent validators, and the approved certificate trust policy.
14. FastAPI verifies and records the output hash.
15. Only the freshly reauthenticated owner can obtain a one-use download authorization and download the output.
16. Restart and fault-injection tests cause no automatic duplicate signature.
17. Sentinel PIN values appear nowhere in storage, logs, events, errors, browser storage, or exports.
18. The UI clearly identifies the output as test-only and makes no qualified, timestamped, LT/LTA, or UYAP claim.
19. The build ledger contains commands, versions, hashes, and validator evidence.

### Real-token pilot

The hardware pilot additionally requires:

- one consenting owner;
- exact recorded token/reader/driver/OS tuple;
- local enrollment only;
- disposable-token lock testing where destructive tests are required;
- two independent validator passes;
- completed privacy, security, recovery, and operational review;
- no unresolved `OUTCOME_UNKNOWN` or credential-selection ambiguity.

### Production

Production remains disabled until the Phase 6 and Phase 7 gates and all legal/records approvals are complete.

---

## 18. Explicit non-goals for this plan

This plan does not implement or authorize:

- browser or remote token enrollment;
- storing a PIN, PIN ciphertext, PFX, or private key;
- shared office credentials;
- signing through chat, MCP, or an LLM tool;
- generic digest signing;
- arbitrary module/path/URL loading;
- caller-selected algorithms, profiles, TSA, OCSP, or CRL endpoints;
- XAdES, ASiC, EYP, UDF, CMS encryption, or mobile signing;
- PAdES B-T, LT, or LTA in the initial slice;
- UYAP login, automation, submission, or legal-validity claims;
- multi-host or active-active operation in the initial SQLite topology.

---

## 19. Implementation order

Execute in this order; do not start the next security boundary merely because partial code compiles:

1. Phase 1A — format conformance and trust prerequisites;
2. Phase 2A — persistence and contracts;
3. Phase 2B — exact SoftHSM credential enrollment;
4. Phase 2C — jobs, queue, events, and PIN challenge;
5. Phase 1B gate — combine selector/session/queue evidence and close authoritative Phase 1;
6. Phase 2D — prepare/sign/finalize/verify and isolated test artifact broker;
7. Phase 3A — FastAPI migrations, production artifacts, inventory, transport identity, and signer client;
8. Phase 3B — browser-facing routes and fresh authorization;
9. Phase 4 — React experience;
10. end-to-end SoftHSM acceptance and evidence update;
11. Phase 5 — one-owner hardware pilot;
12. Phase 6 — trusted timestamping;
13. Phase 7 — production hardening and approval.

At the end of every phase:

- run the phase-specific tests;
- run the full existing regression suite;
- run vulnerability and dependency-boundary checks;
- update the SBOM when dependencies change;
- update `EIMZA_BUILD_LEDGER.md` with exact evidence;
- leave later features disabled until their exit gate is satisfied.
