package jobs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// ValidationEvidence is a normalized verifier result bound to a job.
type ValidationEvidence struct {
	ValidatorName      string
	ValidatorVersion   string
	TrustPolicyVersion string
	PlanSHA256         string
	InputSHA256        string
	OutputSHA256       string
	Outcome            string
}

// RecordOutput persists the output artifact hash and size.
func (s *Service) RecordOutput(ctx context.Context, jobID, outputSHA256 string, byteCount int64) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET output_sha256=?, output_byte_count=?, updated_at=? WHERE id=?`,
		outputSHA256, byteCount, rfc3339(s.now()), jobID)
	return err
}

// RecordValidation appends normalized validation evidence (append-only table).
func (s *Service) RecordValidation(ctx context.Context, jobID string, evidence ValidationEvidence) error {
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	_, err := s.db.ExecContext(ctx, `INSERT INTO validation_evidence
		(id, job_id, validator_name, validator_version, trust_policy_version, plan_sha256, input_sha256, output_sha256, outcome, created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		hex.EncodeToString(id), jobID, evidence.ValidatorName, evidence.ValidatorVersion, evidence.TrustPolicyVersion,
		nullText(evidence.PlanSHA256), evidence.InputSHA256, evidence.OutputSHA256, evidence.Outcome, rfc3339(s.now()))
	return err
}

func nullText(value string) any {
	if value == "" {
		return nil
	}
	return value
}
