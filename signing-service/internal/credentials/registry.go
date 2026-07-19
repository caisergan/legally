package credentials

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/caisergan/legally/signing-service/internal/jobs"
)

// Registry persists signer-local credentials in the journal.
type Registry struct {
	db  *sql.DB
	now func() time.Time
}

func NewRegistry(db *sql.DB) *Registry {
	return &Registry{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// NewServiceCredentialID returns an opaque credential identifier.
func NewServiceCredentialID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return "cred-" + hex.EncodeToString(buf)
}

// save persists a credential and records an administrative event. Unique
// constraints reject a duplicate certificate or exact tuple.
func (r *Registry) save(ctx context.Context, cred Credential) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.ExecContext(ctx, `INSERT INTO credentials (
		service_credential_id, physical_token_queue_key, module_alias, module_sha256,
		slot_id, token_serial, token_label, certificate_der, certificate_sha256, key_ckaid,
		public_key_type, public_key_bits, not_before, not_after, enrolled_at, enabled, last_health
	) VALUES (?,?,?,?, ?,?,?,?,?,?, ?,?,?,?,?,?,?)`,
		cred.ServiceCredentialID, cred.PhysicalTokenQueueKey, cred.ModuleAlias, cred.ModuleSHA256,
		cred.SlotID, cred.TokenSerial, cred.TokenLabel, cred.CertificateDER, cred.CertificateSHA256, cred.KeyCKAID,
		cred.PublicKeyType, cred.PublicKeyBits, rfc3339(cred.NotBefore), rfc3339(cred.NotAfter),
		rfc3339(cred.EnrolledAt), boolToInt(cred.Enabled), nullString(cred.LastHealth))
	if err != nil {
		if isUniqueViolation(err) {
			return ErrCredentialExists
		}
		return fmt.Errorf("insert credential: %w", err)
	}
	if err := recordAdminEvent(ctx, tx, "ENROLL", cred.ServiceCredentialID, "credential enrolled", r.now()); err != nil {
		return err
	}
	return tx.Commit()
}

// List returns all credentials.
func (r *Registry) List(ctx context.Context) ([]Credential, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT `+credentialColumns+` FROM credentials ORDER BY enrolled_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var creds []Credential
	for rows.Next() {
		cred, err := scanCredential(rows)
		if err != nil {
			return nil, err
		}
		creds = append(creds, cred)
	}
	return creds, rows.Err()
}

// Get returns one credential by ID.
func (r *Registry) Get(ctx context.Context, id string) (Credential, error) {
	row := r.db.QueryRowContext(ctx, `SELECT `+credentialColumns+` FROM credentials WHERE service_credential_id=?`, id)
	cred, err := scanCredential(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Credential{}, ErrCredentialNotFound
	}
	return cred, err
}

// SetEnabled enables or disables a credential.
func (r *Registry) SetEnabled(ctx context.Context, id string, enabled bool) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `UPDATE credentials SET enabled=? WHERE service_credential_id=?`, boolToInt(enabled), id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrCredentialNotFound
	}
	action := "DISABLE"
	if enabled {
		action = "ENABLE"
	}
	if err := recordAdminEvent(ctx, tx, action, id, "credential "+strings.ToLower(action)+"d", r.now()); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateProbe records the last probe timestamp and health result.
func (r *Registry) UpdateProbe(ctx context.Context, id, health string, probedAt time.Time) error {
	result, err := r.db.ExecContext(ctx, `UPDATE credentials SET last_probed_at=?, last_health=? WHERE service_credential_id=?`,
		rfc3339(probedAt), health, id)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrCredentialNotFound
	}
	return nil
}

// Remove deletes a credential, refusing while unreconciled jobs reference it.
func (r *Registry) Remove(ctx context.Context, id string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := scanCredentialExists(ctx, tx, id); err != nil {
		return err
	}
	referenced, err := hasUnreconciledJobs(ctx, tx, id)
	if err != nil {
		return err
	}
	if referenced {
		return ErrCredentialReferenced
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM credentials WHERE service_credential_id=?`, id); err != nil {
		return err
	}
	if err := recordAdminEvent(ctx, tx, "REMOVE", id, "credential removed", r.now()); err != nil {
		return err
	}
	return tx.Commit()
}

func hasUnreconciledJobs(ctx context.Context, tx *sql.Tx, credentialID string) (bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT state FROM jobs WHERE credential_id=?`, credentialID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			return false, err
		}
		if !jobs.State(state).IsTerminal() {
			return true, nil
		}
	}
	return false, rows.Err()
}

func scanCredentialExists(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM credentials WHERE service_credential_id=?`, id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrCredentialNotFound
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func recordAdminEvent(ctx context.Context, tx *sql.Tx, eventType, credentialID, detail string, now time.Time) error {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	_, err := tx.ExecContext(ctx, `INSERT INTO administrative_events (id, event_type, credential_id, detail, created_at) VALUES (?,?,?,?,?)`,
		hex.EncodeToString(id), eventType, credentialID, detail, rfc3339(now))
	return err
}

func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(strings.ToUpper(err.Error()), "UNIQUE")
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
