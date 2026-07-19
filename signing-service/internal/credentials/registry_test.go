package credentials_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/credentials"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/persistence"
	"github.com/caisergan/legally/signing-service/internal/policy"
	"github.com/caisergan/legally/signing-service/internal/token"
	"github.com/caisergan/legally/signing-service/internal/token/tokentest"
)

const sentinelPIN = "SENTINEL-PIN-9c3f27a1"

func openRegistry(t *testing.T) (*credentials.Registry, *sql.DB) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state", "signerd.db"))
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return credentials.NewRegistry(db.DB), db.DB
}

func validIdentity(t *testing.T, subject, ckaID string) tokentest.Identity {
	t.Helper()
	now := time.Now()
	id, err := tokentest.NewIdentity(subject, ckaID, now.Add(-time.Hour), now.Add(365*24*time.Hour))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	return id
}

func enrollParams(id tokentest.Identity, slot uint) credentials.EnrollParams {
	return credentials.EnrollParams{
		ModuleAlias:       "softhsm-test",
		ModuleSHA256:      "00",
		SlotID:            slot,
		CertificateSHA256: id.Fingerprint,
		KeyCKAID:          id.CKAID,
	}
}

func noPIN() (string, error) { return "", errors.New("PIN must not be requested") }

func TestEnrollPersistsExactCredential(t *testing.T) {
	registry, _ := openRegistry(t)
	id := validIdentity(t, "Ege Ayyildiz", "ckaid-1")
	backend := tokentest.New(sentinelPIN).AddSlot(0, "SERIAL-A", "Token A")
	backend.AddCredential(0, id, true) // public attributes -> no PIN

	cred, err := registry.Enroll(context.Background(), backend, enrollParams(id, 0), noPIN)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}
	if cred.TokenSerial != "SERIAL-A" || cred.PublicKeyBits != 2048 {
		t.Fatalf("unexpected credential: %+v", cred)
	}
	if cred.PhysicalTokenQueueKey != credentials.DeriveQueueKey("SERIAL-A") {
		t.Fatal("queue key not derived from the token serial")
	}
}

func TestEnrollRejectsDuplicateCertificate(t *testing.T) {
	registry, _ := openRegistry(t)
	id := validIdentity(t, "Ege Ayyildiz", "ckaid-1")
	backend := tokentest.New(sentinelPIN).AddSlot(0, "SERIAL-A", "Token A")
	backend.AddCredential(0, id, true)

	if _, err := registry.Enroll(context.Background(), backend, enrollParams(id, 0), noPIN); err != nil {
		t.Fatalf("first enroll: %v", err)
	}
	if _, err := registry.Enroll(context.Background(), backend, enrollParams(id, 0), noPIN); !errors.Is(err, credentials.ErrCredentialExists) {
		t.Fatalf("error = %v, want ErrCredentialExists", err)
	}
}

func TestEnrollRejectsCertificateKeyMismatch(t *testing.T) {
	registry, _ := openRegistry(t)
	cert := validIdentity(t, "Ege Ayyildiz", "ckaid-1")
	other := validIdentity(t, "Impostor", "ckaid-1")
	backend := tokentest.New(sentinelPIN).AddSlot(0, "SERIAL-A", "Token A")
	backend.AddCert(0, cert.CKAID, cert.CertDER)
	backend.AddKey(0, token.KeyObject{CKAID: cert.CKAID, KeyType: "RSA"}, other.Priv)

	_, err := registry.Enroll(context.Background(), backend, enrollParams(cert, 0), func() (string, error) { return sentinelPIN, nil })
	if !errors.Is(err, token.ErrCertificateKeyMismatch) {
		t.Fatalf("error = %v, want ErrCertificateKeyMismatch", err)
	}
}

func TestEnrollRejectsExpiredCertificate(t *testing.T) {
	registry, _ := openRegistry(t)
	now := time.Now()
	id, err := tokentest.NewIdentity("Expired", "ckaid-1", now.Add(-48*time.Hour), now.Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	backend := tokentest.New(sentinelPIN).AddSlot(0, "SERIAL-A", "Token A")
	backend.AddCredential(0, id, true)
	if _, err := registry.Enroll(context.Background(), backend, enrollParams(id, 0), noPIN); !errors.Is(err, credentials.ErrCertificateExpired) {
		t.Fatalf("error = %v, want ErrCertificateExpired", err)
	}
}

func TestCredentialsOnSameTokenShareQueueKey(t *testing.T) {
	registry, _ := openRegistry(t)
	a := validIdentity(t, "Cert A", "ckaid-a")
	b := validIdentity(t, "Cert B", "ckaid-b")
	backend := tokentest.New(sentinelPIN).AddSlot(0, "SERIAL-SHARED", "Token")
	backend.AddCredential(0, a, true)
	backend.AddCredential(0, b, true)

	credA, err := registry.Enroll(context.Background(), backend, enrollParams(a, 0), noPIN)
	if err != nil {
		t.Fatalf("enroll a: %v", err)
	}
	credB, err := registry.Enroll(context.Background(), backend, enrollParams(b, 0), noPIN)
	if err != nil {
		t.Fatalf("enroll b: %v", err)
	}
	if credA.PhysicalTokenQueueKey != credB.PhysicalTokenQueueKey {
		t.Fatal("two credentials on the same physical token got different queue keys")
	}
}

func TestDisableAndInventory(t *testing.T) {
	registry, _ := openRegistry(t)
	id := validIdentity(t, "Ege Ayyildiz", "ckaid-1")
	backend := tokentest.New(sentinelPIN).AddSlot(0, "SERIAL-A", "Token A")
	backend.AddCredential(0, id, true)
	cred, err := registry.Enroll(context.Background(), backend, enrollParams(id, 0), noPIN)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}

	items, err := registry.Inventory(context.Background())
	if err != nil || len(items) != 1 {
		t.Fatalf("inventory: %v items=%d", err, len(items))
	}
	item := items[0]
	if item.SubjectDisplay != "E*** A***" {
		t.Fatalf("subject not masked: %q", item.SubjectDisplay)
	}
	if item.CertificateFingerprintSHA256 != cred.CertificateSHA256 || item.Status != "available" {
		t.Fatalf("unexpected inventory item: %+v", item)
	}

	if err := registry.SetEnabled(context.Background(), cred.ServiceCredentialID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	items, _ = registry.Inventory(context.Background())
	if items[0].Status != "disabled" {
		t.Fatalf("status after disable = %q", items[0].Status)
	}
}

func TestRemoveBlockedByUnreconciledJob(t *testing.T) {
	registry, db := openRegistry(t)
	ctx := context.Background()
	id := validIdentity(t, "Ege Ayyildiz", "ckaid-1")
	backend := tokentest.New(sentinelPIN).AddSlot(0, "SERIAL-A", "Token A")
	backend.AddCredential(0, id, true)
	cred, err := registry.Enroll(ctx, backend, enrollParams(id, 0), noPIN)
	if err != nil {
		t.Fatalf("enroll: %v", err)
	}

	jobService := jobs.NewService(db)
	now := time.Now().UTC()
	createParams := jobs.CreateParams{
		CommandID: "cmd-1", JobID: "job-1", RequestID: "req-1",
		CredentialID: cred.ServiceCredentialID, CertificateFingerprintSHA256: cred.CertificateSHA256,
		PolicyVersion: policy.Version, Profile: string(policy.ProfilePAdESBaselineBB), Algorithm: string(policy.AlgorithmRSAPKCS1SHA256),
		Input: jobs.Input{ArtifactID: "in", ByteCount: 1, SHA256: "ab"}, Output: jobs.Output{ArtifactID: "out", MaxByteCount: 10},
		Approval: jobs.Approval{ApprovalID: "a", ApprovedAt: now, ExpiresAt: now.Add(time.Minute)}, Nonce: "n", ExpiresAt: now.Add(time.Minute),
	}
	if _, _, err := jobService.Create(ctx, createParams); err != nil {
		t.Fatalf("create job: %v", err)
	}

	if err := registry.Remove(ctx, cred.ServiceCredentialID); !errors.Is(err, credentials.ErrCredentialReferenced) {
		t.Fatalf("error = %v, want ErrCredentialReferenced", err)
	}

	// Once the job reaches a terminal state, removal is permitted.
	if _, err := jobService.Transition(ctx, "job-1", 1, jobs.StateCancelled, "", ""); err != nil {
		t.Fatalf("cancel job: %v", err)
	}
	if err := registry.Remove(ctx, cred.ServiceCredentialID); err != nil {
		t.Fatalf("remove after terminal job: %v", err)
	}
}

func TestEnrollNeverLeaksPIN(t *testing.T) {
	registry, db := openRegistry(t)
	id := validIdentity(t, "Ege Ayyildiz", "ckaid-1")
	backend := tokentest.New(sentinelPIN).AddSlot(0, "SERIAL-A", "Token A")
	backend.AddCredential(0, id, false) // force the PIN-bearing proof path
	if _, err := registry.Enroll(context.Background(), backend, enrollParams(id, 0), func() (string, error) { return sentinelPIN, nil }); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	assertSentinelAbsent(t, db, sentinelPIN)
}

func assertSentinelAbsent(t *testing.T, db *sql.DB, sentinel string) {
	t.Helper()
	tables, err := db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		names = append(names, name)
	}
	tables.Close()

	for _, name := range names {
		rows, err := db.Query(`SELECT * FROM "` + name + `"`)
		if err != nil {
			t.Fatalf("select %s: %v", name, err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			cells := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range cells {
				ptrs[i] = &cells[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				t.Fatalf("scan %s: %v", name, err)
			}
			for _, cell := range cells {
				switch v := cell.(type) {
				case string:
					if contains(v, sentinel) {
						t.Fatalf("PIN sentinel found in table %s", name)
					}
				case []byte:
					if contains(string(v), sentinel) {
						t.Fatalf("PIN sentinel found in table %s (blob)", name)
					}
				}
			}
		}
		rows.Close()
	}
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && stringIndex(haystack, needle) >= 0
}

func stringIndex(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
