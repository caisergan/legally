package signer_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/persistence"
	"github.com/caisergan/legally/signing-service/internal/pin"
	"github.com/caisergan/legally/signing-service/internal/policy"
	"github.com/caisergan/legally/signing-service/internal/signer"
)

const sentinelPIN = "SENTINEL-PIN-7be91c22"

type fakeOperation struct {
	service *jobs.Service
	mu      sync.Mutex
	pin     string
	ran     bool
	delay   time.Duration
	to      jobs.State
	code    errcodes.Code
}

func (f *fakeOperation) Run(ctx context.Context, job jobs.Job, pinValue string) error {
	f.mu.Lock()
	f.pin = pinValue
	f.ran = true
	f.mu.Unlock()
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	_, err := f.service.Transition(ctx, job.ID, job.Version, f.to, f.code, "fake operation")
	return err
}

func newHarness(t *testing.T) (*jobs.Service, *sql.DB, *pin.Signer) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state", "signerd.db"))
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	return jobs.NewService(db.DB), db.DB, pin.NewSigner("challenge-key", key, 600*time.Millisecond)
}

var credCounter int

func seedJob(t *testing.T, service *jobs.Service, db *sql.DB, jobID, queueKey string) jobs.Job {
	t.Helper()
	credCounter++
	credID := "cred-" + jobID
	fingerprint := repeat64(byte('a' + credCounter))
	_, err := db.Exec(`INSERT INTO credentials (
		service_credential_id, physical_token_queue_key, module_alias, module_sha256,
		slot_id, token_serial, token_label, certificate_der, certificate_sha256, key_ckaid,
		public_key_type, public_key_bits, not_before, not_after, enrolled_at, enabled
	) VALUES (?,?, 'softhsm-test','00', ?, ?, 'label', X'00', ?, ?, 'RSA', 2048,
		'2026-01-01T00:00:00Z','2027-01-01T00:00:00Z','2026-07-19T00:00:00Z', 1)`,
		credID, queueKey, credCounter, "SERIAL-"+jobID, fingerprint, "ckaid-"+jobID)
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	now := time.Now().UTC()
	job, _, err := service.Create(context.Background(), jobs.CreateParams{
		CommandID: "cmd-" + jobID, JobID: jobID, RequestID: "req-" + jobID,
		CredentialID: credID, CertificateFingerprintSHA256: fingerprint,
		PolicyVersion: policy.Version, Profile: string(policy.ProfilePAdESBaselineBB), Algorithm: string(policy.AlgorithmRSAPKCS1SHA256),
		Input: jobs.Input{ArtifactID: "in", ByteCount: 1, SHA256: "ab"}, Output: jobs.Output{ArtifactID: "out", MaxByteCount: 10},
		Approval: jobs.Approval{ApprovalID: "a", ApprovedAt: now, ExpiresAt: now.Add(time.Minute)}, Nonce: "n-" + jobID, ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	return job
}

func repeat64(b byte) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = "0123456789abcdef"[int(b)%16]
	}
	return string(out)
}

func waitState(t *testing.T, service *jobs.Service, jobID string, want jobs.State) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		job, err := service.Get(context.Background(), jobID)
		if err == nil && job.State == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	job, _ := service.Get(context.Background(), jobID)
	t.Fatalf("job %s reached %s, want %s", jobID, job.State, want)
}

func encryptPIN(t *testing.T, challenge pin.Challenge, pinValue string) string {
	t.Helper()
	var jwk jose.JSONWebKey
	if err := json.Unmarshal(challenge.RecipientJWK, &jwk); err != nil {
		t.Fatalf("parse recipient jwk: %v", err)
	}
	encrypter, err := jose.NewEncrypter(
		jose.A256GCM,
		jose.Recipient{Algorithm: jose.ECDH_ES, Key: jwk.Key.(*ecdsa.PublicKey)},
		(&jose.EncrypterOptions{}).WithHeader("kid", challenge.ChallengeID),
	)
	if err != nil {
		t.Fatalf("encrypter: %v", err)
	}
	object, err := encrypter.Encrypt([]byte(pinValue))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	compact, err := object.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return compact
}

func TestSameQueueKeySerializes(t *testing.T) {
	service, db, sig := newHarness(t)
	queue := jobs.NewTokenQueue()
	defer queue.Close()
	op := &fakeOperation{service: service, to: jobs.StatePINRejected, code: errcodes.PINRejected}
	coordinator := signer.NewCoordinator(service, sig, queue, op)

	jobA := seedJob(t, service, db, "job-a", "shared-queue")
	jobB := seedJob(t, service, db, "job-b", "shared-queue")
	if err := coordinator.Submit(context.Background(), jobA); err != nil {
		t.Fatalf("submit a: %v", err)
	}
	if err := coordinator.Submit(context.Background(), jobB); err != nil {
		t.Fatalf("submit b: %v", err)
	}

	waitState(t, service, "job-a", jobs.StatePINRequired)
	// While A holds the shared token queue, B must not have started.
	time.Sleep(50 * time.Millisecond)
	if job, _ := service.Get(context.Background(), "job-b"); job.State != jobs.StateQueued {
		t.Fatalf("job-b advanced to %s while job-a held the shared token", job.State)
	}

	challenge, err := coordinator.Challenge("job-a")
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	if _, err := coordinator.Authorize(context.Background(), "job-a", challenge.ChallengeID, encryptPIN(t, challenge, sentinelPIN)); err != nil {
		t.Fatalf("authorize a: %v", err)
	}
	waitState(t, service, "job-a", jobs.StatePINRejected)
	// Now the shared token is free and B proceeds.
	waitState(t, service, "job-b", jobs.StatePINRequired)
}

func TestDifferentQueueKeysRunInParallel(t *testing.T) {
	service, db, sig := newHarness(t)
	queue := jobs.NewTokenQueue()
	defer queue.Close()
	op := &fakeOperation{service: service, to: jobs.StatePINRejected, code: errcodes.PINRejected}
	coordinator := signer.NewCoordinator(service, sig, queue, op)

	coordinator.Submit(context.Background(), seedJob(t, service, db, "job-a", "queue-a"))
	coordinator.Submit(context.Background(), seedJob(t, service, db, "job-b", "queue-b"))

	waitState(t, service, "job-a", jobs.StatePINRequired)
	waitState(t, service, "job-b", jobs.StatePINRequired)
}

func TestAuthorizeConsumesOnce(t *testing.T) {
	service, db, sig := newHarness(t)
	queue := jobs.NewTokenQueue()
	defer queue.Close()
	op := &fakeOperation{service: service, to: jobs.StatePINRejected, code: errcodes.PINRejected, delay: 300 * time.Millisecond}
	coordinator := signer.NewCoordinator(service, sig, queue, op)

	coordinator.Submit(context.Background(), seedJob(t, service, db, "job-a", "queue-a"))
	waitState(t, service, "job-a", jobs.StatePINRequired)
	challenge, _ := coordinator.Challenge("job-a")
	envelope := encryptPIN(t, challenge, sentinelPIN)

	first, err := coordinator.Authorize(context.Background(), "job-a", challenge.ChallengeID, envelope)
	if err != nil || first != signer.StatusConsumed {
		t.Fatalf("first authorize = %v, %v", first, err)
	}
	waitState(t, service, "job-a", jobs.StateAuthorizationConsumed)
	second, err := coordinator.Authorize(context.Background(), "job-a", challenge.ChallengeID, envelope)
	if err != nil || second != signer.StatusAlreadyConsumed {
		t.Fatalf("second authorize = %v, %v (want already_consumed)", second, err)
	}

	op.mu.Lock()
	got := op.pin
	op.mu.Unlock()
	if got != sentinelPIN {
		t.Fatalf("operation received pin %q, want the submitted sentinel", got)
	}
}

func TestPINNeverAppearsInJournal(t *testing.T) {
	service, db, sig := newHarness(t)
	queue := jobs.NewTokenQueue()
	defer queue.Close()
	op := &fakeOperation{service: service, to: jobs.StatePINRejected, code: errcodes.PINRejected}
	coordinator := signer.NewCoordinator(service, sig, queue, op)

	coordinator.Submit(context.Background(), seedJob(t, service, db, "job-a", "queue-a"))
	waitState(t, service, "job-a", jobs.StatePINRequired)
	challenge, _ := coordinator.Challenge("job-a")
	if _, err := coordinator.Authorize(context.Background(), "job-a", challenge.ChallengeID, encryptPIN(t, challenge, sentinelPIN)); err != nil {
		t.Fatalf("authorize: %v", err)
	}
	waitState(t, service, "job-a", jobs.StatePINRejected)

	assertSentinelAbsent(t, db, sentinelPIN)
}

func TestChallengeUnavailableForUnknownJob(t *testing.T) {
	service, _, sig := newHarness(t)
	queue := jobs.NewTokenQueue()
	defer queue.Close()
	coordinator := signer.NewCoordinator(service, sig, queue, &fakeOperation{service: service, to: jobs.StatePINRejected})
	if _, err := coordinator.Challenge("nope"); err != signer.ErrChallengeUnavailable {
		t.Fatalf("error = %v, want ErrChallengeUnavailable", err)
	}
}

func assertSentinelAbsent(t *testing.T, db *sql.DB, sentinel string) {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatalf("tables: %v", err)
	}
	var names []string
	for rows.Next() {
		var name string
		rows.Scan(&name)
		names = append(names, name)
	}
	rows.Close()
	for _, name := range names {
		data, err := db.Query(`SELECT * FROM "` + name + `"`)
		if err != nil {
			t.Fatalf("select %s: %v", name, err)
		}
		cols, _ := data.Columns()
		for data.Next() {
			cells := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range cells {
				ptrs[i] = &cells[i]
			}
			data.Scan(ptrs...)
			for _, cell := range cells {
				var text string
				switch v := cell.(type) {
				case string:
					text = v
				case []byte:
					text = string(v)
				}
				if len(text) > 0 && containsSub(text, sentinel) {
					data.Close()
					t.Fatalf("PIN sentinel found in table %s", name)
				}
			}
		}
		data.Close()
	}
}

func containsSub(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
