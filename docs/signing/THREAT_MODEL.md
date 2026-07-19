# E-İmza Threat Model

**Status:** Draft; Phase 0 engineering baseline  
**Scope:** `signing-service/`, its future FastAPI control-plane integration, private artifacts, and owner-specific PKCS#11 credentials  
**Out of scope:** UYAP login/submission, UDF production, USB-over-IP, shared office certificates, public signer endpoints

## Security objectives

1. Only the authenticated certificate owner can approve a request for their assigned certificate.
2. The signed bytes are exactly the immutable, reviewed input represented by the request manifest.
3. A credential binding identifies one configured module, slot, token serial, certificate fingerprint, and private-key `CKA_ID`; no first-item fallback exists.
4. One physical token processes one operation at a time.
5. A PIN is used once in memory, is never persisted or logged, and is never retried automatically.
6. A request is completed only after independent output verification and durable evidence recording.
7. A failure after token signing may have begun never causes an automatic replay.
8. Private document bytes are owner-scoped and never enter shared caches, the LLM/MCP path, or public web assets.

## Trust boundaries

| Boundary | Trusted input | Untrusted input | Required controls |
|---|---|---|---|
| Browser → FastAPI | Existing same-origin session | Uploads, filenames, request bodies, Origin/Host headers | CSRF, exact Origin/Host checks, size bounds, fresh auth, strict schemas |
| FastAPI → artifact store | Opaque object key and expected hash | Uploaded PDF bytes | Private directories, atomic writes, `0600` files, streaming SHA-256, retention policy |
| FastAPI → signerd | Authenticated immutable manifest | Transport failures, replays | Unix socket permissions or mTLS, command IDs, expected versions, expiry, canonical payload hashes |
| Format worker → token worker | Locally approved signing plan | Parsed PDF structures | Isolation from device access, input hash binding, bounded parsing |
| Token worker → PKCS#11 | Exact configured credential tuple | Driver/device errors and mutable hardware state | Fresh session, one login attempt, exact selectors, full-operation serialization |
| Signerd → validators | Finalized output and expected evidence | Validator/network/process failure | Independent verification, fail-closed result, quarantine on disagreement |
| Chat/MCP → signing | Nothing | Model-generated tool requests | No signing tool registration; no signer credentials in model context |

## Primary threats and controls

### Unauthorized or confused-deputy signing

- Owner-filter every artifact, certificate assignment, and request lookup; inaccessible objects return `404`.
- Bind fresh approval to request ID, input hash, certificate fingerprint, policy version, and expiry.
- Do not let administrators use another owner’s normal signing flow.
- Do not expose browser-selectable module, slot, key, profile, algorithm, or validation endpoints.

### PIN theft, retention, or lockout

- No PIN field exists in database, events, audit, configuration, process arguments, environment variables, or logs.
- Production authorization must use a reviewed, one-time challenge-bound encrypted envelope.
- One PIN submission causes at most one `C_Login` attempt. Incorrect PIN is terminal for the window and triggers cooldown/lock-risk guidance.
- Tests must scan logs, journal, events, crash output, API payloads, and browser storage for submitted sentinel PIN values.

### Wrong certificate or key selection

- Enroll and verify the complete hardware tuple: allowlisted module digest, slot, token serial, certificate SHA-256 fingerprint, private-key `CKA_ID`.
- Reject ambiguity and missing values. Never choose the first module, slot, certificate, or private key.
- Recheck the binding immediately before every operation.

### Replay and duplicate signatures

- Every command includes a unique command ID, expected request version, immutable hashes, nonce, and expiry.
- Persist command/job state before dispatch and use idempotency records.
- If `C_Sign` may have run, transition to `OUTCOME_UNKNOWN`; do not auto-requeue.
- Retrying creates a new request/attempt and requires fresh owner approval.

### Malicious or incompatible PDFs

- Treat PDFs as hostile input; enforce byte limits, PDF magic, structural preflight, parser limits, and timeouts.
- Separate PDF parsing/finalization from USB/token access.
- Test malformed xrefs, object streams, forms, existing signatures, incremental updates, and mutation of signed bytes.
- Do not expose PAdES output until controlled corpus results pass independent validators.

### Cryptographic downgrade or false assurance

- Creation policy is fixed to PAdES Baseline B-B plus RSA PKCS#1 v1.5/SHA-256 for the test/pilot slice.
- MD5, SHA-1, DSA, RSA-PSS, ECDSA, timestamping, LT/LTA, and other formats fail closed until separately gated.
- Add `signing-certificate-v2` to actual CMS signed attributes and verify its certificate binding.
- Treat certificate-chain outcomes as `valid`, `invalid`, or `indeterminate`; signing policy fails closed on the latter two.
- Internal round-trip verification is never release evidence.

### Artifact disclosure or tampering

- Store bytes outside SQLite, `web/dist`, shared caches, and world-readable temporary directories.
- Use opaque keys, path containment checks, atomic writes, private modes, streaming hashes, and encrypted production volumes/backups.
- Rehash input in signerd and verify returned output hash in FastAPI.
- Downloads are owner-filtered with `Cache-Control: private, no-store`.

### Service compromise or excessive authority

- Bind signerd to a private Unix socket by default; no public listener exists.
- Run under a dedicated unprivileged account with narrow filesystem/device access.
- Do not offer generic arbitrary-digest, arbitrary-path, arbitrary-URL, or arbitrary-module endpoints.
- Restrict future egress to configured TSA/OCSP/CRL destinations with scheme, host, size, and timeout limits.

### Audit tampering and privacy leakage

- Audit only safe metadata, hash-chain records, and back them up off-host.
- Do not claim SQLite is immutable against a privileged host administrator.
- Exclude PINs, document content, raw tokens, credentials, and vendor error strings.
- Add database protections against ordinary update/delete once audit tables are introduced.

## Abuse cases that must remain impossible

- Ask the chat assistant to sign a document.
- Submit a raw digest for the hardware token to sign.
- Select an arbitrary PKCS#11 library or external URL through an API.
- Reuse another user’s artifact or certificate ID.
- Automatically retry after wrong PIN or ambiguous completion.
- Label SoftHSM output qualified, production-ready, timestamped, LT/LTA, or submitted to UYAP.

## Release gates

This document does not authorize production signing. Production remains disabled until the legal, retention, interoperability, real-token, deployment, backup/restore, and incident-response gates in `docs/EIMZA_INTEGRATION_PLAN.md` are completed and evidenced.
