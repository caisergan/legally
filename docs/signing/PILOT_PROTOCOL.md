# One-Owner E-İmza Pilot Protocol

**Status:** Draft; not authorization to use a real credential  
**Prerequisites:** Phase 1 conformance gate, Phase 2 SoftHSM gate, owner consent, security/operations approval

## Pilot objective

Demonstrate that one exact owner-specific token configuration can produce independently accepted PAdES Baseline B-B output without wrong-credential selection, overlapping token calls, PIN retention, or unsafe retry.

The pilot does not prove qualified-signature legal sufficiency, TSA/PAdES B-T, LT/LTA, UYAP compatibility, or support for other tokens/drivers.

## Required inventory record

Record locally in protected operational storage; do not commit secrets or complete client documents:

- consenting owner and application user ID;
- host OS/version/architecture;
- reader manufacturer/model/firmware where available;
- token manufacturer/model/serial (mask in reports);
- PKCS#11 module path, version, and SHA-256;
- exact slot identity and token label/serial binding;
- certificate SHA-256 fingerprint, subject/issuer display, validity, serial suffix, key type;
- private-key `CKA_ID` binding;
- signerd/application/format-engine versions;
- validator versions and trust settings.

## Data set

1. Begin with generated, non-sensitive PDFs.
2. Add a controlled corpus covering the qualified PDF matrix.
3. Do not use active client matter until privacy, retention, backup, and incident controls have passed review.
4. Store input/output hashes and validator reports; never commit real documents.

## Procedure

1. Verify the feature kill switch and backups.
2. Enroll the exact credential locally with `yargi-signerctl`; no remote arbitrary module configuration.
3. Confirm the displayed owner certificate fingerprint suffix and immutable input SHA-256.
4. Perform fresh owner authentication.
5. Queue the request and wait for `PIN_REQUIRED`.
6. Owner enters the PIN once through the approved ephemeral path.
7. Observe one serialized token transaction and independent output verification.
8. Download as the owner and confirm another user receives `404`.
9. Validate with Adobe Acrobat/Reader and an ETSI-aware independent validator such as EU DSS.
10. Preserve hashes, safe events, audit verification, and validator reports.

## Failure tests

Use a disposable/spare token for lock-risk scenarios. Never endanger the owner’s only production credential.

- wrong PIN, one attempt only;
- PIN window expiry;
- locked token;
- token removed before login, during preparation, and around signing;
- wrong token inserted;
- moved/changed slot;
- multiple certificates and multiple private keys;
- two simultaneous requests for the same token;
- simultaneous requests for distinct test tokens;
- FastAPI restart before and after dispatch;
- signerd restart before token call, around possible `C_Sign`, and after output creation;
- driver error/session loss;
- artifact hash mismatch;
- validator rejection and quarantine;
- browser disconnect during operation.

## Pass criteria

- No wrong credential can be selected and no first-item fallback exists.
- Same-token calls never overlap.
- Every transaction requires fresh owner PIN entry; no PIN appears in logs, journal, events, storage, crash output, exports, or browser storage.
- Output passes both independent validators under recorded trust settings.
- A failure after possible `C_Sign` becomes `OUTCOME_UNKNOWN`, `FAILED_POST_SIGN`, or `QUARANTINED` and is never automatically retried.
- Owner/cross-user artifact and request access tests pass.
- Backup/restore preserves metadata/artifact hash integrity and the audit chain.

## Stop conditions

Immediately disable signing and preserve evidence if:

- token selection is ambiguous;
- PIN attempt behavior is not exactly one;
- same-token operations overlap;
- any PIN sentinel is retained;
- a validator rejects output unexpectedly;
- a crash can trigger duplicate token execution;
- artifact hashes disagree;
- owner isolation fails;
- the token approaches lockout or hardware health is uncertain.
