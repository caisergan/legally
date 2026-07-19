package api

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/caisergan/legally/signing-service/internal/config"
	"github.com/caisergan/legally/signing-service/internal/errcodes"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/persistence"
	"github.com/caisergan/legally/signing-service/internal/pin"
	"github.com/caisergan/legally/signing-service/internal/policy"
	"github.com/caisergan/legally/signing-service/internal/signer"
)

const testFingerprint = "aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44ee55ff66aa11bb22cc33dd44"

type rejectOperation struct{ service *jobs.Service }

func (o rejectOperation) Run(ctx context.Context, job jobs.Job, pinValue string) error {
	_, err := o.service.Transition(ctx, job.ID, job.Version, jobs.StatePINRejected, errcodes.PINRejected, "test op")
	return err
}

func wiredServer(t *testing.T) (*Server, *sql.DB) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state", "signerd.db"))
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	_, err = db.DB.Exec(`INSERT INTO credentials (
		service_credential_id, physical_token_queue_key, module_alias, module_sha256,
		slot_id, token_serial, token_label, certificate_der, certificate_sha256, key_ckaid,
		public_key_type, public_key_bits, not_before, not_after, enrolled_at, enabled
	) VALUES ('cred-1','queue-1','softhsm-test','00', 0,'SERIAL','label', X'00', ?, 'abcd','RSA',2048,
		'2026-01-01T00:00:00Z','2027-01-01T00:00:00Z','2026-07-19T00:00:00Z', 1)`, testFingerprint)
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	service := jobs.NewService(db.DB)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	queue := jobs.NewTokenQueue()
	t.Cleanup(queue.Close)
	coordinator := signer.NewCoordinator(service, pin.NewSigner("k", key, time.Second), queue, rejectOperation{service: service})

	server := NewServer(config.Config{Mode: config.ModeSoftHSM})
	server.SetJobService(service, coordinator)
	return server, db.DB
}

func createBody(command, jobID string) CreateJobRequest {
	return CreateJobRequest{
		CommandID: command, JobID: jobID, RequestID: "req-" + jobID, ExpectedRequestVersion: 1,
		CredentialID: "cred-1", CertificateFingerprintSHA256: testFingerprint,
		PolicyVersion: policy.Version, Profile: string(policy.ProfilePAdESBaselineBB), Algorithm: string(policy.AlgorithmRSAPKCS1SHA256),
		Input:    JobInput{ArtifactID: "in", ByteCount: 10, SHA256: "ab"},
		Output:   JobOutput{ArtifactID: "out", MaxByteCount: 20},
		Approval: JobApproval{ApprovalID: "a", ApprovedAt: "2026-07-19T12:00:00Z", ExpiresAt: "2026-07-19T12:05:00Z"},
		Nonce:    "n-" + jobID, ExpiresAt: "2026-07-19T12:05:00Z",
	}
}

func do(t *testing.T, server *Server, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, reader)
	if b, ok := body.(CreateJobRequest); ok {
		request.Header.Set("Idempotency-Key", b.CommandID)
	}
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

func TestCreateJobIdempotencyAndConflict(t *testing.T) {
	server, _ := wiredServer(t)
	if r := do(t, server, http.MethodPost, "/v1/jobs", createBody("cmd-1", "job-1")); r.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", r.Code, r.Body)
	}
	if r := do(t, server, http.MethodPost, "/v1/jobs", createBody("cmd-1", "job-1")); r.Code != http.StatusOK {
		t.Fatalf("idempotent repeat status = %d", r.Code)
	}
	changed := createBody("cmd-1", "job-1")
	changed.Input.SHA256 = "different"
	if r := do(t, server, http.MethodPost, "/v1/jobs", changed); r.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d", r.Code)
	}
}

func TestGetJobAndEvents(t *testing.T) {
	server, _ := wiredServer(t)
	do(t, server, http.MethodPost, "/v1/jobs", createBody("cmd-1", "job-1"))

	r := do(t, server, http.MethodGet, "/v1/jobs/job-1", nil)
	if r.Code != http.StatusOK {
		t.Fatalf("get status = %d", r.Code)
	}
	var state JobStateResponse
	json.Unmarshal(r.Body.Bytes(), &state)
	if state.ID != "job-1" || state.InputSHA256 != "ab" {
		t.Fatalf("unexpected job state: %+v", state)
	}

	e := do(t, server, http.MethodGet, "/v1/jobs/job-1/events?after=0", nil)
	var events EventsResponse
	json.Unmarshal(e.Body.Bytes(), &events)
	if len(events.Items) == 0 || events.Items[0].Sequence != 1 {
		t.Fatalf("unexpected events: %+v", events.Items)
	}
}

func TestPINChallengeAndAuthorizeOverHTTP(t *testing.T) {
	server, _ := wiredServer(t)
	do(t, server, http.MethodPost, "/v1/jobs", createBody("cmd-1", "job-1"))
	waitJobState(t, server, "job-1", "PIN_REQUIRED")

	r := do(t, server, http.MethodGet, "/v1/jobs/job-1/pin-challenge", nil)
	if r.Code != http.StatusOK {
		t.Fatalf("pin-challenge status = %d body=%s", r.Code, r.Body)
	}
	var challenge PINChallengeResponse
	json.Unmarshal(r.Body.Bytes(), &challenge)

	envelope := encryptForChallenge(t, challenge)
	a := do(t, server, http.MethodPost, "/v1/jobs/job-1/authorize", AuthorizeRequest{ChallengeID: challenge.ChallengeID, PINJWE: envelope})
	if a.Code != http.StatusAccepted {
		t.Fatalf("authorize status = %d body=%s", a.Code, a.Body)
	}
	var authorized AuthorizeResponse
	json.Unmarshal(a.Body.Bytes(), &authorized)
	if authorized.AuthorizationStatus != "consumed" {
		t.Fatalf("authorization_status = %q", authorized.AuthorizationStatus)
	}
}

func TestCancelBoundaryOverHTTP(t *testing.T) {
	server, _ := wiredServer(t)
	do(t, server, http.MethodPost, "/v1/jobs", createBody("cmd-1", "job-1"))
	waitJobState(t, server, "job-1", "PIN_REQUIRED")

	// Cancellation is allowed before authorization.
	if r := do(t, server, http.MethodPost, "/v1/jobs/job-1/cancel", nil); r.Code != http.StatusOK {
		t.Fatalf("cancel status = %d", r.Code)
	}
	// A second cancel of a terminal job is idempotent.
	if r := do(t, server, http.MethodPost, "/v1/jobs/job-1/cancel", nil); r.Code != http.StatusOK {
		t.Fatalf("idempotent cancel status = %d", r.Code)
	}
}

func waitJobState(t *testing.T, server *Server, jobID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r := do(t, server, http.MethodGet, "/v1/jobs/"+jobID, nil)
		var state JobStateResponse
		json.Unmarshal(r.Body.Bytes(), &state)
		if state.State == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", jobID, want)
}

func encryptForChallenge(t *testing.T, challenge PINChallengeResponse) string {
	t.Helper()
	var jwk jose.JSONWebKey
	if err := json.Unmarshal(challenge.RecipientJWK, &jwk); err != nil {
		t.Fatalf("parse jwk: %v", err)
	}
	encrypter, err := jose.NewEncrypter(
		jose.A256GCM,
		jose.Recipient{Algorithm: jose.ECDH_ES, Key: jwk.Key.(*ecdsa.PublicKey)},
		(&jose.EncrypterOptions{}).WithHeader("kid", challenge.ChallengeID),
	)
	if err != nil {
		t.Fatalf("encrypter: %v", err)
	}
	object, _ := encrypter.Encrypt([]byte("4821"))
	compact, _ := object.CompactSerialize()
	return compact
}
