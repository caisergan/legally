package jobs

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
	"github.com/caisergan/legally/signing-service/internal/persistence"
	"github.com/caisergan/legally/signing-service/internal/policy"
)

const (
	testQueueKey        = "queue-key-token-a"
	testCertFingerprint = "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44"
	testCredentialID    = "cred-1"
)

func newTestDB(t *testing.T) *persistence.DB {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state", "signerd.db"))
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedCredential(t *testing.T, db *sql.DB, id, queueKey, fingerprint string, enabled bool) {
	t.Helper()
	enabledInt := 0
	if enabled {
		enabledInt = 1
	}
	_, err := db.Exec(`INSERT INTO credentials (
		service_credential_id, physical_token_queue_key, module_alias, module_sha256,
		slot_id, token_serial, token_label, certificate_der, certificate_sha256, key_ckaid,
		public_key_type, public_key_bits, not_before, not_after, enrolled_at, enabled
	) VALUES (?,?, 'softhsm-test','`+testCertFingerprint+`', 0, ?, 'label', X'00', ?, ?,
		'RSA', 2048, '2026-01-01T00:00:00Z','2027-01-01T00:00:00Z','2026-07-19T00:00:00Z', ?)`,
		id, queueKey, "SERIAL-"+id, fingerprint, "ckaid-"+id, enabledInt)
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}
}

func sampleParams(commandID, jobID string) CreateParams {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	return CreateParams{
		CommandID:                    commandID,
		JobID:                        jobID,
		RequestID:                    "req-" + jobID,
		CredentialID:                 testCredentialID,
		CertificateFingerprintSHA256: testCertFingerprint,
		PolicyVersion:                policy.Version,
		Profile:                      string(policy.ProfilePAdESBaselineBB),
		Algorithm:                    string(policy.AlgorithmRSAPKCS1SHA256),
		Input:                        Input{ArtifactID: "in-1", ByteCount: 1024, SHA256: "deadbeef"},
		Output:                       Output{ArtifactID: "out-1", MaxByteCount: 52428800},
		Approval:                     Approval{ApprovalID: "appr-1", ApprovedAt: now, ExpiresAt: now.Add(5 * time.Minute)},
		Nonce:                        "nonce-1",
		ExpiresAt:                    now.Add(5 * time.Minute),
	}
}

func newTestService(t *testing.T) (*Service, *sql.DB) {
	db := newTestDB(t)
	seedCredential(t, db.DB, testCredentialID, testQueueKey, testCertFingerprint, true)
	return NewService(db.DB), db.DB
}

func TestCreateStoresQueuedJob(t *testing.T) {
	service, _ := newTestService(t)
	job, created, err := service.Create(context.Background(), sampleParams("cmd-1", "job-1"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created {
		t.Fatal("first create was not marked as created")
	}
	if job.State != StateQueued || job.Version != 1 {
		t.Fatalf("unexpected job state/version: %s/%d", job.State, job.Version)
	}
	if job.QueueKey != testQueueKey {
		t.Fatalf("queue key = %q, want %q (must derive from credential, not the request)", job.QueueKey, testQueueKey)
	}
}

func TestCreateIsIdempotentForSamePayload(t *testing.T) {
	service, _ := newTestService(t)
	first, _, err := service.Create(context.Background(), sampleParams("cmd-1", "job-1"))
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	second, created, err := service.Create(context.Background(), sampleParams("cmd-1", "job-1"))
	if err != nil {
		t.Fatalf("repeat create: %v", err)
	}
	if created {
		t.Fatal("repeat create with identical payload created a new job")
	}
	if second.ID != first.ID {
		t.Fatalf("repeat returned job %q, want original %q", second.ID, first.ID)
	}
}

func TestCreateConflictsOnDifferentPayload(t *testing.T) {
	service, _ := newTestService(t)
	if _, _, err := service.Create(context.Background(), sampleParams("cmd-1", "job-1")); err != nil {
		t.Fatalf("first create: %v", err)
	}
	changed := sampleParams("cmd-1", "job-1")
	changed.Input.SHA256 = "cafebabe"
	if _, _, err := service.Create(context.Background(), changed); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("error = %v, want ErrIdempotencyConflict", err)
	}
}

func TestCreateRejectsUnknownAndDisabledCredentials(t *testing.T) {
	service, db := newTestService(t)
	missing := sampleParams("cmd-x", "job-x")
	missing.CredentialID = "nope"
	if _, _, err := service.Create(context.Background(), missing); !errors.Is(err, ErrCredentialNotFound) {
		t.Fatalf("error = %v, want ErrCredentialNotFound", err)
	}

	const otherFingerprint = "bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55"
	seedCredential(t, db, "cred-off", "queue-off", otherFingerprint, false)
	disabled := sampleParams("cmd-off", "job-off")
	disabled.CredentialID = "cred-off"
	disabled.CertificateFingerprintSHA256 = otherFingerprint
	if _, _, err := service.Create(context.Background(), disabled); !errors.Is(err, ErrCredentialDisabled) {
		t.Fatalf("error = %v, want ErrCredentialDisabled", err)
	}
}

func TestCreateRejectsCertificateAndPolicyMismatch(t *testing.T) {
	service, _ := newTestService(t)
	wrongCert := sampleParams("cmd-c", "job-c")
	wrongCert.CertificateFingerprintSHA256 = "00000000000000000000000000000000000000000000000000000000deadbeef"
	if _, _, err := service.Create(context.Background(), wrongCert); !errors.Is(err, ErrCertificateMismatch) {
		t.Fatalf("error = %v, want ErrCertificateMismatch", err)
	}
	wrongPolicy := sampleParams("cmd-p", "job-p")
	wrongPolicy.Algorithm = "RSA_PSS_SHA512"
	if _, _, err := service.Create(context.Background(), wrongPolicy); !errors.Is(err, ErrPolicyMismatch) {
		t.Fatalf("error = %v, want ErrPolicyMismatch", err)
	}
}

func TestTransitionEnforcesVersionAndLegality(t *testing.T) {
	service, _ := newTestService(t)
	if _, _, err := service.Create(context.Background(), sampleParams("cmd-1", "job-1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.Transition(context.Background(), "job-1", 1, StatePINRequired, "", ""); err != nil {
		t.Fatalf("QUEUED->PIN_REQUIRED: %v", err)
	}
	// Stale version is refused.
	if _, err := service.Transition(context.Background(), "job-1", 1, StateAuthorizationConsumed, "", ""); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("error = %v, want ErrVersionConflict", err)
	}
	// Illegal transition is refused.
	if _, err := service.Transition(context.Background(), "job-1", 2, StateCompleted, "", ""); err == nil {
		t.Fatal("illegal PIN_REQUIRED->COMPLETED transition accepted")
	}
}

func TestEventsAreMonotonic(t *testing.T) {
	service, _ := newTestService(t)
	if _, _, err := service.Create(context.Background(), sampleParams("cmd-1", "job-1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.Transition(context.Background(), "job-1", 1, StatePINRequired, "", ""); err != nil {
		t.Fatalf("transition: %v", err)
	}
	if _, err := service.Transition(context.Background(), "job-1", 2, StateAuthorizationConsumed, "", ""); err != nil {
		t.Fatalf("transition: %v", err)
	}
	events, err := service.Events(context.Background(), "job-1", 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("event count = %d, want 3", len(events))
	}
	for i, event := range events {
		if event.Sequence != i+1 {
			t.Fatalf("event %d has sequence %d", i, event.Sequence)
		}
	}
	after, err := service.Events(context.Background(), "job-1", 2)
	if err != nil {
		t.Fatalf("events after cursor: %v", err)
	}
	if len(after) != 1 || after[0].Sequence != 3 {
		t.Fatalf("cursor query returned %d events", len(after))
	}
}

func TestGetByCommandReconciles(t *testing.T) {
	service, _ := newTestService(t)
	created, _, err := service.Create(context.Background(), sampleParams("cmd-9", "job-9"))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := service.GetByCommand(context.Background(), "cmd-9")
	if err != nil {
		t.Fatalf("get by command: %v", err)
	}
	if got.ID != created.ID {
		t.Fatalf("reconciled job %q, want %q", got.ID, created.ID)
	}
	if _, err := service.GetByCommand(context.Background(), "missing"); !errors.Is(err, ErrCommandNotFound) {
		t.Fatalf("error = %v, want ErrCommandNotFound", err)
	}
}

func TestTransitionRejectsUnknownSafeCode(t *testing.T) {
	service, _ := newTestService(t)
	if _, _, err := service.Create(context.Background(), sampleParams("cmd-1", "job-1")); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := service.Transition(context.Background(), "job-1", 1, StateFailedPreSign, errcodes.Code("MADE_UP"), ""); err == nil {
		t.Fatal("transition accepted an unknown safe failure code")
	}
}
