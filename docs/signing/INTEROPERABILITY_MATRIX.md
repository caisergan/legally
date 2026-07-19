# E-İmza Interoperability Matrix

**Status:** Not yet qualified. Empty evidence cells are release blockers, not implied passes.

## Validator matrix

| Fixture/profile | Signing backend | Internal verify | Validator 1 | Validator 2 | Result | Evidence |
|---|---|---|---|---|---|---|
| Controlled classic-xref PDF / B-B | Software test key | Pass | Poppler `pdfsig` 26.04.0: signature valid; issuer unknown (reconfirmed 2026-07-19) | Second full PAdES validator pending | PARTIAL — NOT QUALIFIED | LibreSSL 3.3.6 also verified detached CMS; 2026-07-19 ephemeral output SHA-256 `81044f556c0fdf733a09f0e34006657e08d487e918141e60f754237792274f97`; self-signed test certificate intentionally has no trusted issuer |
| Corpus PDFs / B-B | SoftHSM | Pending | Pending | Pending | NOT QUALIFIED | — |
| Controlled PDFs / B-B | Real owner token | Pending | Adobe Reader pending | ETSI-aware validator pending | NOT QUALIFIED | — |
| Controlled PDFs / B-T | Real owner token + TSA | Pending | Pending | Pending | DISABLED | — |

Internal validation by the imported source does not satisfy either independent-validator column. OpenSSL detached-CMS verification is useful secondary cryptographic evidence but does not count as a second full PAdES validator.

## PDF corpus dimensions

Each supported input class needs a stored non-sensitive fixture or reproducible generator, input/output SHA-256, tool versions, and evidence report.

| Dimension | Fixture | Sign succeeds | Opens unchanged | Signature valid | Notes |
|---|---|---:|---:|---:|---|
| Classic xref table | Pending | — | — | — | |
| Xref stream/object streams | Pending | — | — | — | |
| Multiple pages | Pending | — | — | — | |
| AcroForm | Pending | — | — | — | |
| Existing unsigned signature field | Pending | — | — | — | |
| Existing prior signature | Pending | — | — | — | Must preserve prior revision validity |
| Incremental updates | Pending | — | — | — | |
| Encrypted PDF | Pending | — | — | — | Expected reject until policy exists |
| Malformed/truncated PDF | Pending | — | — | — | Must fail safely |
| Near upload-size limit | Pending | — | — | — | Memory/time bound required |

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
