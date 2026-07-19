package persistence

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
)

type migration struct {
	version int
	up      string
}

// migrations is the forward-only, ordered schema history. Never edit an applied
// migration's SQL; add a new version instead. Checksums are verified on startup.
var migrations = []migration{
	{version: 1, up: schema1},
}

// Migrate applies pending migrations and verifies that already-applied
// migrations have not drifted. It is idempotent and fails closed on drift.
func Migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		checksum   TEXT NOT NULL,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	applied := map[int]string{}
	rows, err := db.Query(`SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return fmt.Errorf("read schema_migrations: %w", err)
	}
	for rows.Next() {
		var version int
		var checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			return fmt.Errorf("scan schema_migrations: %w", err)
		}
		applied[version] = checksum
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate schema_migrations: %w", err)
	}
	rows.Close()

	for _, m := range migrations {
		checksum := checksumOf(m.up)
		if existing, ok := applied[m.version]; ok {
			if existing != checksum {
				return fmt.Errorf("migration %d checksum drift: journal has %s, want %s", m.version, existing, checksum)
			}
			continue
		}
		if err := applyMigration(db, m, checksum); err != nil {
			return err
		}
	}
	return nil
}

func applyMigration(db *sql.DB, m migration, checksum string) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin migration %d: %w", m.version, err)
	}
	if _, err := tx.Exec(m.up); err != nil {
		tx.Rollback()
		return fmt.Errorf("apply migration %d: %w", m.version, err)
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations (version, checksum) VALUES (?, ?)`, m.version, checksum); err != nil {
		tx.Rollback()
		return fmt.Errorf("record migration %d: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %d: %w", m.version, err)
	}
	return nil
}

func checksumOf(sqlText string) string {
	sum := sha256.Sum256([]byte(sqlText))
	return hex.EncodeToString(sum[:])
}

const schema1 = `
CREATE TABLE credentials (
	service_credential_id    TEXT PRIMARY KEY,
	physical_token_queue_key TEXT NOT NULL,
	module_alias             TEXT NOT NULL,
	module_sha256            TEXT NOT NULL,
	slot_id                  INTEGER NOT NULL,
	token_serial             TEXT NOT NULL,
	token_label              TEXT NOT NULL,
	certificate_der          BLOB NOT NULL,
	certificate_sha256       TEXT NOT NULL,
	key_ckaid                TEXT NOT NULL,
	public_key_type          TEXT NOT NULL,
	public_key_bits          INTEGER NOT NULL,
	not_before               TEXT NOT NULL,
	not_after                TEXT NOT NULL,
	enrolled_at              TEXT NOT NULL,
	last_probed_at           TEXT,
	enabled                  INTEGER NOT NULL DEFAULT 1,
	last_health              TEXT,
	schema_version           INTEGER NOT NULL DEFAULT 1,
	UNIQUE (certificate_sha256),
	UNIQUE (module_alias, slot_id, token_serial, key_ckaid)
);

CREATE TABLE jobs (
	id                             TEXT PRIMARY KEY,
	request_id                     TEXT NOT NULL,
	command_id                     TEXT NOT NULL,
	credential_id                  TEXT NOT NULL,
	certificate_fingerprint_sha256 TEXT NOT NULL,
	policy_version                 TEXT NOT NULL,
	profile                        TEXT NOT NULL,
	algorithm                      TEXT NOT NULL,
	input_artifact_id              TEXT NOT NULL,
	input_sha256                   TEXT NOT NULL,
	input_byte_count               INTEGER NOT NULL,
	output_artifact_id             TEXT NOT NULL,
	output_max_byte_count          INTEGER NOT NULL,
	output_sha256                  TEXT,
	output_byte_count              INTEGER,
	physical_token_queue_key       TEXT NOT NULL,
	approval_id                    TEXT NOT NULL,
	approved_at                    TEXT NOT NULL,
	approval_expires_at            TEXT NOT NULL,
	expires_at                     TEXT NOT NULL,
	nonce                          TEXT NOT NULL,
	manifest_sha256                TEXT NOT NULL,
	state                          TEXT NOT NULL,
	version                        INTEGER NOT NULL DEFAULT 1,
	failure_code                   TEXT,
	signing_claimed_at             TEXT,
	created_at                     TEXT NOT NULL,
	updated_at                     TEXT NOT NULL
);
CREATE INDEX idx_jobs_request ON jobs(request_id);
CREATE INDEX idx_jobs_state ON jobs(state);
CREATE INDEX idx_jobs_queue_key ON jobs(physical_token_queue_key);

CREATE TABLE job_events (
	job_id     TEXT NOT NULL REFERENCES jobs(id),
	sequence   INTEGER NOT NULL,
	type       TEXT NOT NULL,
	state      TEXT,
	safe_code  TEXT,
	detail     TEXT,
	created_at TEXT NOT NULL,
	PRIMARY KEY (job_id, sequence)
);
CREATE TRIGGER job_events_no_update BEFORE UPDATE ON job_events
BEGIN SELECT RAISE(ABORT, 'job_events is append-only'); END;
CREATE TRIGGER job_events_no_delete BEFORE DELETE ON job_events
BEGIN SELECT RAISE(ABORT, 'job_events is append-only'); END;

CREATE TABLE commands (
	command_id               TEXT PRIMARY KEY,
	canonical_payload_sha256 TEXT NOT NULL,
	job_id                   TEXT NOT NULL REFERENCES jobs(id),
	created_at               TEXT NOT NULL
);

CREATE TABLE pin_challenges (
	challenge_id             TEXT PRIMARY KEY,
	job_id                   TEXT NOT NULL REFERENCES jobs(id),
	key_id                   TEXT NOT NULL,
	recipient_jwk_thumbprint TEXT NOT NULL,
	nonce                    TEXT NOT NULL,
	state                    TEXT NOT NULL,
	created_at               TEXT NOT NULL,
	expires_at               TEXT NOT NULL,
	consumed_at              TEXT
);
CREATE INDEX idx_pin_challenges_job ON pin_challenges(job_id);

CREATE TABLE plan_authorizations (
	id                             TEXT PRIMARY KEY,
	job_id                         TEXT NOT NULL REFERENCES jobs(id),
	canonical_plan_sha256          TEXT NOT NULL,
	job_version                    INTEGER NOT NULL,
	input_sha256                   TEXT NOT NULL,
	prepared_content_sha256        TEXT NOT NULL,
	signed_attributes_sha256       TEXT NOT NULL,
	certificate_fingerprint_sha256 TEXT NOT NULL,
	policy_version                 TEXT NOT NULL,
	mechanism                      TEXT NOT NULL,
	nonce                          TEXT NOT NULL,
	state                          TEXT NOT NULL,
	created_at                     TEXT NOT NULL,
	expires_at                     TEXT NOT NULL,
	consumed_at                    TEXT,
	UNIQUE (canonical_plan_sha256),
	UNIQUE (nonce)
);
CREATE INDEX idx_plan_authorizations_job ON plan_authorizations(job_id);

CREATE TABLE validation_evidence (
	id                   TEXT PRIMARY KEY,
	job_id               TEXT NOT NULL REFERENCES jobs(id),
	validator_name       TEXT NOT NULL,
	validator_version    TEXT NOT NULL,
	trust_policy_version TEXT NOT NULL,
	plan_sha256          TEXT,
	input_sha256         TEXT NOT NULL,
	output_sha256        TEXT NOT NULL,
	outcome              TEXT NOT NULL,
	created_at           TEXT NOT NULL
);
CREATE INDEX idx_validation_evidence_job ON validation_evidence(job_id);
CREATE TRIGGER validation_evidence_no_update BEFORE UPDATE ON validation_evidence
BEGIN SELECT RAISE(ABORT, 'validation_evidence is append-only'); END;
CREATE TRIGGER validation_evidence_no_delete BEFORE DELETE ON validation_evidence
BEGIN SELECT RAISE(ABORT, 'validation_evidence is append-only'); END;

CREATE TABLE administrative_events (
	id            TEXT PRIMARY KEY,
	event_type    TEXT NOT NULL,
	credential_id TEXT,
	detail        TEXT,
	created_at    TEXT NOT NULL
);
CREATE TRIGGER administrative_events_no_update BEFORE UPDATE ON administrative_events
BEGIN SELECT RAISE(ABORT, 'administrative_events is append-only'); END;
CREATE TRIGGER administrative_events_no_delete BEFORE DELETE ON administrative_events
BEGIN SELECT RAISE(ABORT, 'administrative_events is append-only'); END;
`
