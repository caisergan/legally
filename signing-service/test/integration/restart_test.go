package integration

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/persistence"
	"github.com/caisergan/legally/signing-service/internal/policy"
)

const (
	queueKey        = "queue-key-token-a"
	certFingerprint = "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44"
	credentialID    = "cred-1"
)

func seedCredential(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO credentials (
		service_credential_id, physical_token_queue_key, module_alias, module_sha256,
		slot_id, token_serial, token_label, certificate_der, certificate_sha256, key_ckaid,
		public_key_type, public_key_bits, not_before, not_after, enrolled_at, enabled
	) VALUES (?,?, 'softhsm-test','`+certFingerprint+`', 0,'SERIAL','label', X'00', ?, 'abcd',
		'RSA', 2048, '2026-01-01T00:00:00Z','2027-01-01T00:00:00Z','2026-07-19T00:00:00Z', 1)`,
		credentialID, queueKey, certFingerprint)
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}
}

func params() jobs.CreateParams {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	return jobs.CreateParams{
		CommandID:                    "cmd-1",
		JobID:                        "job-1",
		RequestID:                    "req-1",
		CredentialID:                 credentialID,
		CertificateFingerprintSHA256: certFingerprint,
		PolicyVersion:                policy.Version,
		Profile:                      string(policy.ProfilePAdESBaselineBB),
		Algorithm:                    string(policy.AlgorithmRSAPKCS1SHA256),
		Input:                        jobs.Input{ArtifactID: "in-1", ByteCount: 1024, SHA256: "deadbeef"},
		Output:                       jobs.Output{ArtifactID: "out-1", MaxByteCount: 52428800},
		Approval:                     jobs.Approval{ApprovalID: "appr-1", ApprovedAt: now, ExpiresAt: now.Add(5 * time.Minute)},
		Nonce:                        "nonce-1",
		ExpiresAt:                    now.Add(5 * time.Minute),
	}
}

// TestRestartPreservesJobsAndReconcilesSigningClaim is the Phase 2A exit-gate
// evidence: a genuine on-disk close/reopen preserves the job and its events,
// and an ambiguous SIGNING claim becomes OUTCOME_UNKNOWN with no replay.
func TestRestartPreservesJobsAndReconcilesSigningClaim(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state", "signerd.db")

	// First "process": create a job and drive it past the SIGNING claim.
	first, err := persistence.Open(path)
	if err != nil {
		t.Fatalf("open first: %v", err)
	}
	seedCredential(t, first.DB)
	service := jobs.NewService(first.DB)
	if _, _, err := service.Create(ctx, params()); err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, step := range []struct {
		version int
		to      jobs.State
	}{{1, jobs.StatePINRequired}, {2, jobs.StateAuthorizationConsumed}, {3, jobs.StateSigning}} {
		if _, err := service.Transition(ctx, "job-1", step.version, step.to, "", ""); err != nil {
			t.Fatalf("transition v%d -> %s: %v", step.version, step.to, err)
		}
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}

	// Second "process": reopen the same journal and reconcile.
	second, err := persistence.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()
	recovered := jobs.NewService(second.DB)

	job, err := recovered.Get(ctx, "job-1")
	if err != nil {
		t.Fatalf("job did not survive restart: %v", err)
	}
	if job.State != jobs.StateSigning {
		t.Fatalf("pre-recovery state = %s, want SIGNING", job.State)
	}
	events, err := recovered.Events(ctx, "job-1", 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 4 {
		t.Fatalf("event history not preserved: got %d events", len(events))
	}

	report, err := recovered.Recover(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.OutcomeUnknown != 1 {
		t.Fatalf("unexpected recovery report: %+v", report)
	}
	reconciled, err := recovered.Get(ctx, "job-1")
	if err != nil {
		t.Fatalf("get after recovery: %v", err)
	}
	if reconciled.State != jobs.StateOutcomeUnknown {
		t.Fatalf("state after recovery = %s, want OUTCOME_UNKNOWN", reconciled.State)
	}
}
