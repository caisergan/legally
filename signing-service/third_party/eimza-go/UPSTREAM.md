# Selected eimza-go Fork Provenance

## Source identity

- Claimed upstream: <https://github.com/KilimcininKorOglu/eimza-go.git>
- Source snapshot location at intake: `eimza-go-main/`
- Imported on: 2026-07-18
- Upstream commit: unknown; the supplied snapshot contains no Git metadata
- Deterministic snapshot digest: `sha256:5c60312a93e26f54dfc9b63e8dfe294731c5c27cea1d73d38fcf3e6adc9ff1a7`
- Digest method: lexicographically sort all 203 snapshot files; for each file hash an 8-byte big-endian path length, UTF-8 relative path, 8-byte big-endian content length, and file bytes into one SHA-256 stream
- Claimed module: `github.com/kilimcininkoroglu/eimza-go`
- Snapshot Go directive: `go 1.26.1`
- Owned service minimum/toolchain: Go `1.26.5` (raised after govulncheck found reachable standard-library vulnerabilities in 1.26.3)
- License: MIT; preserved in `LICENSE`
- Review owner: Yargı Asistan engineering/security

The upstream README claims Go/PAdES interoperability, but no independent PAdES validator report or fixture was present in the supplied snapshot. This fork makes no interoperability or legal-validity claim until the repository’s own matrix is populated.

## Imported source

Copied substantially from upstream:

- `asn/cms/cms.go`
- `pdf/parser.go`
- `pdf/types.go`
- `pdf/writer.go`
- `pdf/xref.go`
- `pdf/xref_stream.go`

Rewritten from selected upstream CAdES/PAdES mechanics:

- `cades/signer.go`
- `pades/sign.go`

New local verification and tests:

- `cades/verify.go`
- `cades/signer_test.go`
- `pades/verify.go`
- `pades/sign_test.go`
- `pdf/parser_test.go`

## Intentionally excluded

The runtime graph does not import:

- upstream `eimza-cli`, `eimza-gui`, or `eyp-go`;
- PKCS#11/smartcard discovery, first-slot/first-key selection, or PFX helpers;
- upstream algorithm registries containing MD5, SHA-1, DSA, ECDSA, RSA-PSS, and multiple caller-selectable creation algorithms;
- upstream certificate-chain, OCSP, CRL, embedded KamuSM roots, or fail-open validation result code;
- timestamp/KamuSM dependencies and all B-T/T/LT/LTA upgrade code;
- ASiC, XAdES, CMS encryption, mobile signing, or other format packages;
- upstream general PAdES verify/profile heuristics.

`go list -deps` for this module currently shows only standard-library packages and packages under `github.com/caisergan/legally/signing-service`; no third-party Go module is in the runtime dependency graph.

## Local patch list

1. Added a fixed creation boundary: RSA PKCS#1 v1.5 with SHA-256 only.
2. Required an RSA key of at least 2048 bits and exact signer/certificate public-key equality.
3. Rejected certificates outside their validity interval or without signing key usage.
4. Added mandatory CMS `contentType`, `messageDigest`, `signingTime`, and `SigningCertificateV2` signed attributes.
5. Corrected the snapshot helper’s ambiguous `SigningCertificateV2` shape by preserving the outer sequence and explicitly encoding the SHA-256 `AlgorithmIdentifier`.
6. Canonically sorted signed attributes before hashing and signing.
7. Added a constrained cryptographic verifier that requires the fixed algorithms, mandatory attributes, content digest, certificate fingerprint binding, and RSA signature.
8. Removed all profile and algorithm input from the PAdES creation API.
9. Restricted the initial PDF corpus to unencrypted, classic-xref PDFs with no prior incremental update, AcroForm, or signature.
10. Located the actual trailer-root catalog object from xref data instead of assuming fixed catalog/pages/page object numbers.
11. Preserved the existing catalog dictionary rather than replacing it with a minimal hard-coded catalog.
12. Added strict ByteRange non-negative/order/full-file checks and signed-byte mutation tests.
13. Fixed incremental xref emission to write only changed-object subsections; the snapshot writer incorrectly marked untouched objects in gaps as free.
14. Added a proper invisible `/Widget` signature field annotation so independent PDF readers do not need to synthesize one.
15. Added PDF header and negative-xref checks plus a 16 MiB decompressed xref-stream bound. Xref-stream signing remains rejected for the controlled corpus.
16. Added an internal post-sign structural/cryptographic check while explicitly documenting that it is not independent release evidence.
17. Required CMS `SignerInfo` issuer/serial to match the embedded certificate.
18. Added PDF name `#xx` decoding and lexical dictionary scanning so names inside strings, comments, arrays, nested dictionaries, or value positions cannot be mistaken for direct trailer/catalog keys.
19. Preserved trailer `/Info`, `/ID`, and indirect-reference generations; detected direct `/Encrypt`, invalid `/Prev`, and hybrid `/XRefStm` entries and rejected them from the controlled signer corpus.
20. Made classic xref tables and xref streams reject malformed headers, duplicate/truncated entries, invalid `/Index`, negative or overflowing `/W` fields, inconsistent stream lengths, and unsupported filters instead of partially parsing them.
21. Bounded signed-PDF verification input before allocating the detached signed-byte buffer.
22. Required the outer CMS `SignedData` envelope to declare exactly SHA-256 and `id-data`, and decoded/validated the signed `signingTime` against the certificate validity interval.

## Current qualification status

The selected fork compiles and has local negative/unit tests, but remains quarantined:

- It is not wired to a service job endpoint.
- It has not signed through SoftHSM or a real token.
- It has not passed two independent PAdES validators.
- Its supported PDF corpus is intentionally narrow and not production-qualified.
- It performs cryptographic/profile checks only; trust-chain, revocation, qualified-certificate policy, and legal validity remain outside this code and unimplemented.

## Update procedure

1. Obtain the upstream repository with verifiable Git history and record the exact commit/tag.
2. Recompute and record the source snapshot digest.
3. Diff only the selected imported files and review every upstream change.
4. Do not wholesale-copy newly added packages or restore excluded dependencies.
5. Reapply or replace each local patch explicitly; update this patch list.
6. Run formatting, unit tests, race tests, vet, vulnerability scan, and dependency-graph check.
7. Re-run the PDF corpus and two independent validators.
8. Record validator/tool versions, trust configuration, hashes, and reports in `docs/signing/INTEROPERABILITY_MATRIX.md`.
9. Keep production signing disabled until all affected phase gates pass again.
