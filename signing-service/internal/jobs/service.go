package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
	"github.com/caisergan/legally/signing-service/internal/policy"
)

// Service is the durable job journal API over the signer database.
type Service struct {
	db  *sql.DB
	now func() time.Time
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db, now: func() time.Time { return time.Now().UTC() }}
}

// Create records an idempotent signer job. Repeating a command with the same
// canonical payload returns the existing job; a different payload conflicts.
func (s *Service) Create(ctx context.Context, p CreateParams) (Job, bool, error) {
	if err := policy.ValidateCreationRequest(policy.Profile(p.Profile), policy.Algorithm(p.Algorithm)); err != nil {
		return Job{}, false, fmt.Errorf("%w: %v", ErrPolicyMismatch, err)
	}
	if p.PolicyVersion != policy.Version {
		return Job{}, false, ErrPolicyMismatch
	}
	canonical := p.CanonicalHash()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, false, err
	}
	defer tx.Rollback()

	var existingHash, existingJobID string
	err = tx.QueryRowContext(ctx, `SELECT canonical_payload_sha256, job_id FROM commands WHERE command_id=?`, p.CommandID).
		Scan(&existingHash, &existingJobID)
	switch {
	case err == nil:
		if existingHash != canonical {
			return Job{}, false, ErrIdempotencyConflict
		}
		job, loadErr := loadJob(ctx, tx, existingJobID)
		if loadErr != nil {
			return Job{}, false, loadErr
		}
		return job, false, nil
	case errors.Is(err, sql.ErrNoRows):
		// fall through to create
	default:
		return Job{}, false, err
	}

	queueKey, certFingerprint, enabled, credErr := lookupCredential(ctx, tx, p.CredentialID)
	if credErr != nil {
		return Job{}, false, credErr
	}
	if !enabled {
		return Job{}, false, ErrCredentialDisabled
	}
	if p.CertificateFingerprintSHA256 != certFingerprint {
		return Job{}, false, ErrCertificateMismatch
	}

	now := s.now()
	nowText := rfc3339(now)
	job := Job{
		ID:                           p.JobID,
		RequestID:                    p.RequestID,
		CommandID:                    p.CommandID,
		CredentialID:                 p.CredentialID,
		CertificateFingerprintSHA256: p.CertificateFingerprintSHA256,
		PolicyVersion:                p.PolicyVersion,
		Profile:                      p.Profile,
		Algorithm:                    p.Algorithm,
		Input:                        p.Input,
		Output:                       p.Output,
		QueueKey:                     queueKey,
		Approval:                     p.Approval,
		Nonce:                        p.Nonce,
		ExpiresAt:                    p.ExpiresAt,
		ManifestSHA256:               canonical,
		State:                        StateQueued,
		Version:                      1,
		CreatedAt:                    now,
		UpdatedAt:                    now,
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO jobs (
		id, request_id, command_id, credential_id, certificate_fingerprint_sha256,
		policy_version, profile, algorithm,
		input_artifact_id, input_sha256, input_byte_count,
		output_artifact_id, output_max_byte_count,
		physical_token_queue_key, approval_id, approved_at, approval_expires_at,
		expires_at, nonce, manifest_sha256, state, version, created_at, updated_at
	) VALUES (?,?,?,?,?, ?,?,?, ?,?,?, ?,?, ?,?,?,?, ?,?,?,?,?,?,?)`,
		job.ID, job.RequestID, job.CommandID, job.CredentialID, job.CertificateFingerprintSHA256,
		job.PolicyVersion, job.Profile, job.Algorithm,
		job.Input.ArtifactID, job.Input.SHA256, job.Input.ByteCount,
		job.Output.ArtifactID, job.Output.MaxByteCount,
		job.QueueKey, job.Approval.ApprovalID, rfc3339(job.Approval.ApprovedAt), rfc3339(job.Approval.ExpiresAt),
		rfc3339(job.ExpiresAt), job.Nonce, job.ManifestSHA256, string(job.State), job.Version, nowText, nowText,
	); err != nil {
		return Job{}, false, fmt.Errorf("insert job: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO commands (command_id, canonical_payload_sha256, job_id, created_at) VALUES (?,?,?,?)`,
		p.CommandID, canonical, job.ID, nowText); err != nil {
		return Job{}, false, fmt.Errorf("insert command: %w", err)
	}

	if _, err := appendEventTx(ctx, tx, job.ID, "created", StateQueued, "", "", now); err != nil {
		return Job{}, false, err
	}

	if err := tx.Commit(); err != nil {
		return Job{}, false, err
	}
	return job, true, nil
}

// Transition performs a durable compare-and-swap from expectedVersion to a new
// state, appends a transition event, and returns the updated job.
func (s *Service) Transition(ctx context.Context, jobID string, expectedVersion int, to State, code errcodes.Code, detail string) (Job, error) {
	if code != "" && !errcodes.IsKnown(code) {
		return Job{}, fmt.Errorf("refusing unknown safe failure code %q", code)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	current, err := loadJob(ctx, tx, jobID)
	if err != nil {
		return Job{}, err
	}
	if current.Version != expectedVersion {
		return Job{}, ErrVersionConflict
	}
	if err := s.applyTransitionTx(ctx, tx, current, to, code, detail); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return loadJob(ctx, s.db, jobID)
}

// applyTransitionTx performs a validated, version-CAS state change plus a
// transition event within an existing transaction.
func (s *Service) applyTransitionTx(ctx context.Context, tx *sql.Tx, current Job, to State, code errcodes.Code, detail string) error {
	if err := ValidateTransition(current.State, to, false); err != nil {
		return err
	}
	now := s.now()
	// COALESCE keeps an existing claim timestamp; a nil param leaves it unchanged.
	var claimParam any
	if to == StateSigning {
		claimParam = rfc3339(now)
	}
	var failureCode any
	if code != "" {
		failureCode = string(code)
	}
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?, version=?, failure_code=COALESCE(?, failure_code), signing_claimed_at=COALESCE(signing_claimed_at, ?), updated_at=? WHERE id=? AND version=?`,
		string(to), current.Version+1, failureCode, claimParam, rfc3339(now), current.ID, current.Version)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrVersionConflict
	}
	if _, err := appendEventTx(ctx, tx, current.ID, "transition", to, code, detail, now); err != nil {
		return err
	}
	return nil
}

// AppendEvent records a safe event without changing job state.
func (s *Service) AppendEvent(ctx context.Context, jobID, eventType string, code errcodes.Code, detail string) (Event, error) {
	if code != "" && !errcodes.IsKnown(code) {
		return Event{}, fmt.Errorf("refusing unknown safe failure code %q", code)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()
	event, err := appendEventTx(ctx, tx, jobID, eventType, "", code, detail, s.now())
	if err != nil {
		return Event{}, err
	}
	if err := tx.Commit(); err != nil {
		return Event{}, err
	}
	return event, nil
}

// Get returns the job by ID.
func (s *Service) Get(ctx context.Context, jobID string) (Job, error) {
	return loadJob(ctx, s.db, jobID)
}

// GetByCommand reconciles an ambiguous create by command ID.
func (s *Service) GetByCommand(ctx context.Context, commandID string) (Job, error) {
	var jobID string
	err := s.db.QueryRowContext(ctx, `SELECT job_id FROM commands WHERE command_id=?`, commandID).Scan(&jobID)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrCommandNotFound
	}
	if err != nil {
		return Job{}, err
	}
	return loadJob(ctx, s.db, jobID)
}

// Events returns durable events for a job after the given sequence.
func (s *Service) Events(ctx context.Context, jobID string, afterSequence int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT job_id, sequence, type, state, safe_code, detail, created_at
		FROM job_events WHERE job_id=? AND sequence>? ORDER BY sequence ASC`, jobID, afterSequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func appendEventTx(ctx context.Context, tx *sql.Tx, jobID, eventType string, state State, code errcodes.Code, detail string, now time.Time) (Event, error) {
	var maxSeq int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(sequence),0) FROM job_events WHERE job_id=?`, jobID).Scan(&maxSeq); err != nil {
		return Event{}, fmt.Errorf("read event sequence: %w", err)
	}
	seq := maxSeq + 1
	var stateValue, codeValue, detailValue any
	if state != "" {
		stateValue = string(state)
	}
	if code != "" {
		codeValue = string(code)
	}
	if detail != "" {
		detailValue = detail
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO job_events (job_id, sequence, type, state, safe_code, detail, created_at) VALUES (?,?,?,?,?,?,?)`,
		jobID, seq, eventType, stateValue, codeValue, detailValue, rfc3339(now)); err != nil {
		return Event{}, fmt.Errorf("insert event: %w", err)
	}
	return Event{JobID: jobID, Sequence: seq, Type: eventType, State: state, SafeCode: code, Detail: detail, CreatedAt: now}, nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func lookupCredential(ctx context.Context, tx *sql.Tx, credentialID string) (queueKey, certFingerprint string, enabled bool, err error) {
	var enabledInt int
	scanErr := tx.QueryRowContext(ctx, `SELECT physical_token_queue_key, certificate_sha256, enabled FROM credentials WHERE service_credential_id=?`, credentialID).
		Scan(&queueKey, &certFingerprint, &enabledInt)
	if errors.Is(scanErr, sql.ErrNoRows) {
		return "", "", false, ErrCredentialNotFound
	}
	if scanErr != nil {
		return "", "", false, scanErr
	}
	return queueKey, certFingerprint, enabledInt != 0, nil
}
