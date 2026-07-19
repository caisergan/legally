package token_test

import (
	"errors"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/token"
	"github.com/caisergan/legally/signing-service/internal/token/tokentest"
)

func mustIdentity(t *testing.T, subject, ckaID string) tokentest.Identity {
	t.Helper()
	now := time.Now()
	id, err := tokentest.NewIdentity(subject, ckaID, now.Add(-time.Hour), now.Add(24*time.Hour))
	if err != nil {
		t.Fatalf("new identity: %v", err)
	}
	return id
}

func TestResolveExactMatch(t *testing.T) {
	id := mustIdentity(t, "E. A.", "ckaid-1")
	backend := tokentest.New("1234").AddSlot(3, "SERIAL-A", "Token A")
	backend.AddCredential(3, id, true)

	sel, err := token.Resolve(backend, token.Criteria{SlotID: 3, CertificateSHA256: id.Fingerprint, KeyCKAID: id.CKAID})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sel.SlotID != 3 || sel.TokenSerial != "SERIAL-A" || sel.TokenLabel != "Token A" {
		t.Fatalf("wrong slot binding: %+v", sel)
	}
	if sel.SHA256 != id.Fingerprint || string(sel.KeyCKAID) != "ckaid-1" {
		t.Fatalf("wrong cert/key binding: %+v", sel)
	}
	if sel.PublicKeyType != "RSA" || sel.PublicKeyBits != 2048 {
		t.Fatalf("wrong public key metadata: %+v", sel)
	}
}

func TestResolveNoFallbackToFirstSlot(t *testing.T) {
	id := mustIdentity(t, "E. A.", "ckaid-1")
	backend := tokentest.New("1234").AddSlot(0, "SERIAL-0", "Token 0").AddSlot(1, "SERIAL-1", "Token 1")
	backend.AddCredential(1, id, true)

	// The certificate lives in slot 1; asking for slot 0 must not fall back.
	if _, err := token.Resolve(backend, token.Criteria{SlotID: 0, CertificateSHA256: id.Fingerprint, KeyCKAID: id.CKAID}); !errors.Is(err, token.ErrCertificateNotFound) {
		t.Fatalf("error = %v, want ErrCertificateNotFound", err)
	}
	// An absent slot fails closed even though other slots are present.
	if _, err := token.Resolve(backend, token.Criteria{SlotID: 9, CertificateSHA256: id.Fingerprint, KeyCKAID: id.CKAID}); !errors.Is(err, token.ErrSlotNotFound) {
		t.Fatalf("error = %v, want ErrSlotNotFound", err)
	}
}

func TestResolveRejectsAmbiguousCertificateAndKey(t *testing.T) {
	id := mustIdentity(t, "E. A.", "ckaid-1")

	dupCert := tokentest.New("1234").AddSlot(0, "SERIAL", "Token")
	dupCert.AddCert(0, id.CKAID, id.CertDER).AddCert(0, id.CKAID, id.CertDER)
	dupCert.AddKey(0, keyObject(id), id.Priv)
	if _, err := token.Resolve(dupCert, token.Criteria{SlotID: 0, CertificateSHA256: id.Fingerprint, KeyCKAID: id.CKAID}); !errors.Is(err, token.ErrAmbiguousCertificate) {
		t.Fatalf("error = %v, want ErrAmbiguousCertificate", err)
	}

	dupKey := tokentest.New("1234").AddSlot(0, "SERIAL", "Token")
	dupKey.AddCert(0, id.CKAID, id.CertDER)
	dupKey.AddKey(0, keyObject(id), id.Priv).AddKey(0, keyObject(id), id.Priv)
	if _, err := token.Resolve(dupKey, token.Criteria{SlotID: 0, CertificateSHA256: id.Fingerprint, KeyCKAID: id.CKAID}); !errors.Is(err, token.ErrAmbiguousKey) {
		t.Fatalf("error = %v, want ErrAmbiguousKey", err)
	}
}

func TestResolveRejectsMissingKey(t *testing.T) {
	id := mustIdentity(t, "E. A.", "ckaid-1")
	backend := tokentest.New("1234").AddSlot(0, "SERIAL", "Token")
	backend.AddCert(0, id.CKAID, id.CertDER) // certificate present, no key
	if _, err := token.Resolve(backend, token.Criteria{SlotID: 0, CertificateSHA256: id.Fingerprint, KeyCKAID: id.CKAID}); !errors.Is(err, token.ErrKeyNotFound) {
		t.Fatalf("error = %v, want ErrKeyNotFound", err)
	}
}

func TestResolveDetectsMovedTokenSerial(t *testing.T) {
	id := mustIdentity(t, "E. A.", "ckaid-1")
	backend := tokentest.New("1234").AddSlot(0, "SERIAL-NEW", "Token")
	backend.AddCredential(0, id, true)
	criteria := token.Criteria{SlotID: 0, CertificateSHA256: id.Fingerprint, KeyCKAID: id.CKAID, ExpectedTokenSerial: "SERIAL-OLD"}
	if _, err := token.Resolve(backend, criteria); !errors.Is(err, token.ErrTokenMismatch) {
		t.Fatalf("error = %v, want ErrTokenMismatch", err)
	}
}

func keyObject(id tokentest.Identity) token.KeyObject {
	return token.KeyObject{CKAID: id.CKAID, KeyType: "RSA"}
}
