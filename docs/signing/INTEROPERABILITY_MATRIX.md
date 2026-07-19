# E-İmza Interoperability Matrix

**Status:** Not yet qualified. Empty evidence cells are release blockers, not implied passes.

## Phase 1B automated evidence (2026-07-19)

Reproducible in `signing-service`: two independent full PAdES validators now both
accept the eimza-go signature (`TestTwoIndependentValidatorMatrix`), the trust
outcome is explicitly fail-closed (`TestTrustOutcomeFailsClosed`), the versioned
corpus signs/rejects as specified (`TestCorpus*`, `testdata/pdf/manifest.json`,
version `yargi-pdf-corpus-v1`), and seeded fuzz targets over the parser, preflight,
and verify surfaces run without panics. Validator wrappers:
`testdata/validators/{pdfsig,pyhanko}_validate.sh`; pyHanko wrapper:
`tools/pades_validate_pyhanko.py`. Trust store is empty and no revocation is
fetched, so release stays refused and output stays quarantined.

## Validator matrix

| Fixture/profile | Signing backend | Internal verify | Validator 1 | Validator 2 | Result | Evidence |
|---|---|---|---|---|---|---|
| Controlled classic-xref PDF / B-B | Software test key | Pass | Poppler `pdfsig` 26.04.0: signature valid, entire-file coverage; issuer unknown (reconfirmed 2026-07-19) | pyHanko 0.35.2: crypto valid, coverage `ENTIRE_FILE`; trust/revocation indeterminate (self-signed) | PARTIAL — NOT QUALIFIED (both validators accept crypto; trust indeterminate; SoftHSM/live pending) | Both validators reject a one-byte-tampered copy; LibreSSL 3.3.6 also verified detached CMS; self-signed test certificate intentionally has no trusted issuer |
| Corpus PDFs / B-B | SoftHSM | Pending | Pending | Pending | NOT QUALIFIED | — |
| Controlled PDFs / B-B | Real owner token | Pending | Adobe Reader pending | ETSI-aware validator pending | NOT QUALIFIED | — |
| Controlled PDFs / B-T | Real owner token + TSA | Pending | Pending | Pending | DISABLED | — |

Internal validation by the imported source does not satisfy either independent-validator column. OpenSSL detached-CMS verification is useful secondary cryptographic evidence but does not count as a second full PAdES validator.

## PDF corpus dimensions

Each supported input class needs a stored non-sensitive fixture or reproducible generator, input/output SHA-256, tool versions, and evidence report.

Corpus `yargi-pdf-corpus-v1`; digests in `signing-service/testdata/pdf/manifest.json`;
accept/reject behavior asserted by `internal/pades` and `internal/corpus` tests.

| Dimension | Fixture | Sign succeeds | Opens unchanged | Signature valid | Notes |
|---|---|---:|---:|---:|---|
| Classic xref table | `accepted_minimal` | yes | yes | yes (both validators) | round-trips through `pades.Sign`/`VerifyLocal` |
| Classic xref + Info dict | `accepted_with_info` | yes | yes | yes (both validators) | |
| Xref stream / non-classic | `rejected_non_classic_xref` | reject | — | — | preflight fails closed |
| Multiple pages | Pending | — | — | — | not yet in corpus |
| AcroForm | `rejected_acroform` | reject | — | — | expected reject |
| Existing unsigned signature field | `rejected_existing_signature` | reject | — | — | expected reject |
| Existing prior signature | Pending | — | — | — | Must preserve prior revision validity |
| Incremental updates | `rejected_prior_incremental` | reject | — | — | trailer `/Prev` rejected |
| Hybrid reference | `rejected_hybrid_reference` | reject | — | — | trailer `/XRefStm` rejected |
| Encrypted PDF | `rejected_encrypted` | reject | — | — | Expected reject until policy exists |
| Malformed/truncated PDF | `rejected_malformed_xref`, `rejected_missing_header` | reject | — | — | Must fail safely |
| Near upload-size limit | `corpus.Oversized` (>25 MiB) | reject | — | — | Memory/time bound enforced |

## Credential and platform matrix

| Environment | OS/arch | Token/HSM | PKCS#11 module + digest | Slot/token binding | Certificate/key binding | Result |
|---|---|---|---|---|---|---|
| Test | Pending | SoftHSM | Pending | Pending | Pending | NOT CONFIGURED |
| Pilot | Pending | Owner token #1 | Pending | Pending | Pending | NOT AUTHORIZED |

Support attaches to the exact OS, token model, reader, driver/module version and digest, slot identity, certificate fingerprint, and private-key `CKA_ID`; passing one combination does not qualify another.

## Required negative evidence

- Missing/wrong `signing-certificate-v2` binding.
- Invalid, expired, untrusted, revoked, and indeterminate certificate chains.
- Wrong token, slot, certificate fingerprint, or `CKA_ID`.
- Weak/unsupported algorithms and profiles.
- Invalid ByteRange or signed-byte mutation.
- Token removal, session loss, wrong PIN, locked token, and driver failure.
- Crash before, during, and after possible `C_Sign`, proving no automatic duplicate operation.
- For B-T: wrong imprint, invalid TSA signature/chain/EKU/policy, replay, malformed token, timeout, and unavailable endpoint.

## Pivot rule

If two independent validators reject repaired output, or the corpus cannot be signed safely without unreasonable narrowing, retain the Go token/service work and replace the PAdES/PDF layer with a mature implementation such as EU DSS or pyHanko. Acceptance criteria must not be lowered.
