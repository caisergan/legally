package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
)

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const jobColumns = `id, request_id, command_id, credential_id, certificate_fingerprint_sha256,
	policy_version, profile, algorithm,
	input_artifact_id, input_sha256, input_byte_count,
	output_artifact_id, output_max_byte_count, output_sha256, output_byte_count,
	physical_token_queue_key, approval_id, approved_at, approval_expires_at,
	expires_at, nonce, manifest_sha256, state, version, failure_code, signing_claimed_at,
	created_at, updated_at`

func loadJob(ctx context.Context, q querier, jobID string) (Job, error) {
	row := q.QueryRowContext(ctx, `SELECT `+jobColumns+` FROM jobs WHERE id=?`, jobID)

	var (
		job                                       Job
		outputSHA256, failureCode, signingClaimed sql.NullString
		outputByteCount                           sql.NullInt64
		approvedAt, approvalExpiresAt, expiresAt  string
		createdAt, updatedAt                      string
		state                                     string
	)
	err := row.Scan(
		&job.ID, &job.RequestID, &job.CommandID, &job.CredentialID, &job.CertificateFingerprintSHA256,
		&job.PolicyVersion, &job.Profile, &job.Algorithm,
		&job.Input.ArtifactID, &job.Input.SHA256, &job.Input.ByteCount,
		&job.Output.ArtifactID, &job.Output.MaxByteCount, &outputSHA256, &outputByteCount,
		&job.QueueKey, &job.Approval.ApprovalID, &approvedAt, &approvalExpiresAt,
		&expiresAt, &job.Nonce, &job.ManifestSHA256, &state, &job.Version, &failureCode, &signingClaimed,
		&createdAt, &updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrJobNotFound
	}
	if err != nil {
		return Job{}, err
	}

	job.State = State(state)
	job.OutputSHA256 = outputSHA256.String
	job.OutputByteCount = outputByteCount.Int64
	job.FailureCode = errcodes.Code(failureCode.String)
	job.SigningClaimed = signingClaimed.Valid && signingClaimed.String != ""

	for _, field := range []struct {
		text   string
		target *time.Time
	}{
		{approvedAt, &job.Approval.ApprovedAt},
		{approvalExpiresAt, &job.Approval.ExpiresAt},
		{expiresAt, &job.ExpiresAt},
		{createdAt, &job.CreatedAt},
		{updatedAt, &job.UpdatedAt},
	} {
		parsed, perr := parseTime(field.text)
		if perr != nil {
			return Job{}, perr
		}
		*field.target = parsed
	}
	return job, nil
}

func scanEvent(scanner rowScanner) (Event, error) {
	var (
		event                   Event
		state, safeCode, detail sql.NullString
		createdAt               string
	)
	if err := scanner.Scan(&event.JobID, &event.Sequence, &event.Type, &state, &safeCode, &detail, &createdAt); err != nil {
		return Event{}, err
	}
	event.State = State(state.String)
	event.SafeCode = errcodes.Code(safeCode.String)
	event.Detail = detail.String
	parsed, err := parseTime(createdAt)
	if err != nil {
		return Event{}, err
	}
	event.CreatedAt = parsed
	return event, nil
}
