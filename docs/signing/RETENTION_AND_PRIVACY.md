# E-İmza Retention and Privacy Draft

**Status:** Engineering default only; requires records/legal approval before production  
**Data classification:** Private legal documents and legally significant signature evidence

## Current defaults

| Record | Default | Destruction rule |
|---|---:|---|
| Unreferenced input artifact | 30 days | Owner may delete only while no retained request references it |
| Completed output artifact | 365 days | No self-service deletion until records policy is approved |
| Quarantined artifact | Case-specific | Operator/legal review required |
| Signing request and safe events | At least output retention | Preserve with evidence |
| Approval record | Short-lived operational metadata | Token hash only; never PIN or PIN ciphertext |
| Audit evidence | Undecided | No ordinary update/delete; legal hold may extend indefinitely |
| Signer journal/outbox | Operational recovery period | Prune only after reconciled, sealed evidence exists |

These values are placeholders, not a statement of Turkish legal sufficiency.

## Data minimization

Persist only what is needed to prove ownership, authorization, exact input/output, credential identity, policy version, state transitions, validation, and recovery behavior. Do not persist:

- PINs or PIN ciphertext;
- complete token serials in user-visible records;
- private-key identifiers in browser-facing records;
- raw application sessions or signer credentials;
- document contents in audit events, logs, or SQLite;
- raw PKCS#11/vendor errors;
- TSA credentials or audit sealing keys.

## Storage rules

- Document bytes live in a dedicated private artifact root, never `DocumentCache`, SQLite, `web/dist`, or broad temporary paths.
- Object paths use opaque storage keys; display filenames are sanitized metadata only.
- Directories use `0700`; files use `0600`; writes are atomic.
- Production volumes and backups must be encrypted.
- Artifact metadata and bytes must be backed up and restored together; hash verification is mandatory after restore.

## Access and export

- Only the artifact owner and explicitly authorized operators may access private bytes.
- Account export may include safe signing metadata but excludes artifacts by default.
- Artifact export is a separate fresh-authenticated download path.
- Download responses use `Cache-Control: private, no-store`.
- `delete-history` must never delete signing requests, artifacts, approvals, events, or audit evidence.

## Account deletion

Before signing is exposed, account deletion must either:

1. be blocked while retention-bound signing evidence exists; or
2. execute a legally approved anonymization workflow that preserves evidentiary integrity.

Cascading deletion of completed signing evidence is prohibited. Until a policy owner decides otherwise, the safe default is no self-service destruction of completed evidence.

## Legal hold and incidents

- Legal hold suspends expiry and deletion for the affected request, artifacts, and evidence.
- Security incidents may extend retention for forensic preservation.
- Any operator override must be authenticated, reasoned, audited, and reviewable.

## Open policy decisions

- Required retention periods by artifact/evidence category.
- Lawful basis, privacy notice wording, and data-subject request handling.
- Legal-hold authority and release process.
- Approved anonymization fields and evidentiary impact.
- Backup retention and geographic/storage controls.
- Whether and when completed outputs can be owner-deleted.
