package signer_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/artifacts"
	"github.com/caisergan/legally/signing-service/internal/credentials"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/persistence"
	"github.com/caisergan/legally/signing-service/internal/planauth"
	"github.com/caisergan/legally/signing-service/internal/policy"
	"github.com/caisergan/legally/signing-service/internal/signer"
	"github.com/caisergan/legally/signing-service/internal/token"
	"github.com/caisergan/legally/signing-service/internal/token/tokentest"
)

const opPIN = "op-pin-1234"

type countingBackend struct {
	inner token.Backend
	mu    sync.Mutex
	signs int
}

func (c *countingBackend) Slots() ([]token.SlotInfo, error) { return c.inner.Slots() }
func (c *countingBackend) Objects(slot uint) (token.SlotObjects, error) {
	return c.inner.Objects(slot)
}
func (c *countingBackend) Sign(slot uint, ckaID []byte, pin string, digest []byte) ([]byte, error) {
	c.mu.Lock()
	c.signs++
	c.mu.Unlock()
	return c.inner.Sign(slot, ckaID, pin, digest)
}
func (c *countingBackend) Close() error { return c.inner.Close() }
func (c *countingBackend) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.signs
}

func opMinimalPDF() []byte {
	catalog, pages, page := 1, 2, 3
	bodies := map[int]string{
		catalog: fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R /PageMode /UseNone >>", pages),
		pages:   fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", page),
		page:    fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] >>", pages),
	}
	order := []int{catalog, pages, page}
	sort.Ints(order)
	var document bytes.Buffer
	document.WriteString("%PDF-1.4\n")
	offsets := map[int]int{}
	for _, n := range order {
		offsets[n] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", n, bodies[n])
	}
	xrefOffset := document.Len()
	maxObject := order[len(order)-1]
	fmt.Fprintf(&document, "xref\n0 %d\n", maxObject+1)
	document.WriteString("0000000000 65535 f \n")
	for n := 1; n <= maxObject; n++ {
		if offset, ok := offsets[n]; ok {
			fmt.Fprintf(&document, "%010d 00000 n \n", offset)
		} else {
			document.WriteString("0000000000 65535 f \n")
		}
	}
	fmt.Fprintf(&document, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", maxObject+1, catalog, xrefOffset)
	return document.Bytes()
}

func opIdentity(t *testing.T) tokentest.Identity {
	t.Helper()
	now := time.Now()
	id, err := tokentest.NewIdentity("Ege Ayyildiz", "ckaid-1", now.Add(-time.Hour), now.Add(365*24*time.Hour))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	return id
}

func seedCredentialRow(t *testing.T, db *sql.DB, id tokentest.Identity) string {
	t.Helper()
	fingerprint := hex.EncodeToString(id.Fingerprint[:])
	_, err := db.Exec(`INSERT INTO credentials (
		service_credential_id, physical_token_queue_key, module_alias, module_sha256,
		slot_id, token_serial, token_label, certificate_der, certificate_sha256, key_ckaid,
		public_key_type, public_key_bits, not_before, not_after, enrolled_at, enabled
	) VALUES ('cred-1','queue-1','softhsm-test','00', 0,'SERIAL','label', ?, ?, ?, 'RSA',2048,
		'2026-01-01T00:00:00Z','2027-01-01T00:00:00Z','2026-07-19T00:00:00Z', 1)`,
		id.CertDER, fingerprint, hex.EncodeToString(id.CKAID))
	if err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	return fingerprint
}

func authConsumedJob(t *testing.T, service *jobs.Service, fingerprint, inputHash string, inputBytes int64) jobs.Job {
	t.Helper()
	now := time.Now().UTC()
	_, _, err := service.Create(context.Background(), jobs.CreateParams{
		CommandID: "cmd-1", JobID: "job-1", RequestID: "req-1", CredentialID: "cred-1",
		CertificateFingerprintSHA256: fingerprint, PolicyVersion: policy.Version,
		Profile: string(policy.ProfilePAdESBaselineBB), Algorithm: string(policy.AlgorithmRSAPKCS1SHA256),
		Input:    jobs.Input{ArtifactID: "in-1", ByteCount: inputBytes, SHA256: inputHash},
		Output:   jobs.Output{ArtifactID: "out-1", MaxByteCount: 5 << 20},
		Approval: jobs.Approval{ApprovalID: "a", ApprovedAt: now, ExpiresAt: now.Add(time.Minute)},
		Nonce:    "n", ExpiresAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if _, err := service.Transition(context.Background(), "job-1", 1, jobs.StatePINRequired, "", ""); err != nil {
		t.Fatalf("to pin_required: %v", err)
	}
	job, err := service.Transition(context.Background(), "job-1", 2, jobs.StateAuthorizationConsumed, "", "")
	if err != nil {
		t.Fatalf("to authorization_consumed: %v", err)
	}
	return job
}

func opHarness(t *testing.T) (*jobs.Service, *sql.DB, *credentials.Registry, *artifacts.Broker, *planauth.Authorizer) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state", "signerd.db"))
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	authorizer := planauth.NewAuthorizer([]byte("supervisor-key"), []string{signer.FormatEngineVersion}, time.Minute)
	return jobs.NewService(db.DB), db.DB, credentials.NewRegistry(db.DB), artifacts.NewBroker(time.Minute), authorizer
}

func TestPipelineProducesQuarantinedOutput(t *testing.T) {
	service, db, registry, broker, authorizer := opHarness(t)
	id := opIdentity(t)
	fingerprint := seedCredentialRow(t, db, id)

	pdf := opMinimalPDF()
	inputHash := broker.PutInput("in-1", pdf)
	job := authConsumedJob(t, service, fingerprint, inputHash, int64(len(pdf)))

	backend := &countingBackend{inner: tokentest.New(opPIN).AddSlot(0, "SERIAL", "label")}
	backend.inner.(*tokentest.FakeBackend).AddCredential(0, id, false)
	operation := signer.NewTokenOperation(service, backend, broker, authorizer, registry, nil, time.Minute)

	if err := operation.Run(context.Background(), job, opPIN); err != nil {
		t.Fatalf("run: %v", err)
	}
	final, _ := service.Get(context.Background(), "job-1")
	if final.State != jobs.StateQuarantined {
		t.Fatalf("state = %s, want QUARANTINED", final.State)
	}
	if final.OutputSHA256 == "" || final.OutputByteCount == 0 {
		t.Fatalf("output hash/size not recorded: %+v", final)
	}
	if backend.count() != 1 {
		t.Fatalf("token signed %d times, want 1", backend.count())
	}
	if data, ok := broker.Output("out-1"); !ok || len(data) <= len(pdf) {
		t.Fatalf("signed output not stored (len=%d)", len(data))
	}
	var evidence int
	db.QueryRow(`SELECT COUNT(*) FROM validation_evidence WHERE job_id='job-1'`).Scan(&evidence)
	if evidence == 0 {
		t.Fatal("no validation evidence recorded")
	}
}

func TestPipelineFailsBeforeSignOnInputMismatch(t *testing.T) {
	service, db, registry, broker, authorizer := opHarness(t)
	id := opIdentity(t)
	fingerprint := seedCredentialRow(t, db, id)

	pdf := opMinimalPDF()
	broker.PutInput("in-1", pdf)
	// Job declares a wrong input hash; MintRead fails before any token call.
	job := authConsumedJob(t, service, fingerprint, hex.EncodeToString(make([]byte, 32)), int64(len(pdf)))

	backend := &countingBackend{inner: tokentest.New(opPIN).AddSlot(0, "SERIAL", "label")}
	backend.inner.(*tokentest.FakeBackend).AddCredential(0, id, false)
	operation := signer.NewTokenOperation(service, backend, broker, authorizer, registry, nil, time.Minute)

	if err := operation.Run(context.Background(), job, opPIN); err != nil {
		t.Fatalf("run: %v", err)
	}
	final, _ := service.Get(context.Background(), "job-1")
	if final.State != jobs.StateFailedPreSign {
		t.Fatalf("state = %s, want FAILED_PRE_SIGN", final.State)
	}
	if backend.count() != 0 {
		t.Fatalf("token signed %d times before a valid input, want 0", backend.count())
	}
}

func TestPipelineFailsPostSignWithoutSecondSignature(t *testing.T) {
	service, db, registry, broker, authorizer := opHarness(t)
	certID := opIdentity(t)
	fingerprint := seedCredentialRow(t, db, certID)

	pdf := opMinimalPDF()
	inputHash := broker.PutInput("in-1", pdf)
	job := authConsumedJob(t, service, fingerprint, inputHash, int64(len(pdf)))

	// The token signs with a different key than the enrolled certificate, so the
	// CMS is invalid and finalization fails after exactly one signature.
	wrong := opIdentity(t)
	fake := tokentest.New(opPIN).AddSlot(0, "SERIAL", "label")
	fake.AddCert(0, certID.CKAID, certID.CertDER)
	fake.AddKey(0, token.KeyObject{CKAID: certID.CKAID, KeyType: "RSA"}, wrong.Priv)
	backend := &countingBackend{inner: fake}
	operation := signer.NewTokenOperation(service, backend, broker, authorizer, registry, nil, time.Minute)

	if err := operation.Run(context.Background(), job, opPIN); err != nil {
		t.Fatalf("run: %v", err)
	}
	final, _ := service.Get(context.Background(), "job-1")
	if final.State != jobs.StateFailedPostSign {
		t.Fatalf("state = %s, want FAILED_POST_SIGN", final.State)
	}
	if backend.count() != 1 {
		t.Fatalf("token signed %d times, want exactly 1 (no retry)", backend.count())
	}
}
