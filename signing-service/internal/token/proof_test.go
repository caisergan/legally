package token_test

import (
	"errors"
	"testing"

	"github.com/caisergan/legally/signing-service/internal/token"
	"github.com/caisergan/legally/signing-service/internal/token/tokentest"
)

func resolve(t *testing.T, backend token.Backend, id tokentest.Identity) token.Selection {
	t.Helper()
	sel, err := token.Resolve(backend, token.Criteria{SlotID: 0, CertificateSHA256: id.Fingerprint, KeyCKAID: id.CKAID})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return sel
}

func TestVerifyKeyMatchViaPublicAttributesNeedsNoPIN(t *testing.T) {
	id := mustIdentity(t, "E. A.", "ckaid-1")
	backend := tokentest.New("1234").AddSlot(0, "SERIAL", "Token")
	backend.AddCredential(0, id, true) // exposes public attributes
	sel := resolve(t, backend, id)

	pinCalled := false
	err := token.VerifyKeyMatch(backend, sel, func() (string, error) { pinCalled = true; return "1234", nil })
	if err != nil {
		t.Fatalf("proof failed: %v", err)
	}
	if pinCalled {
		t.Fatal("public-attribute cross-check must not request a PIN")
	}
}

func TestVerifyKeyMatchViaSignatureProof(t *testing.T) {
	id := mustIdentity(t, "E. A.", "ckaid-1")
	backend := tokentest.New("1234").AddSlot(0, "SERIAL", "Token")
	backend.AddCredential(0, id, false) // no public attributes -> PIN proof
	sel := resolve(t, backend, id)

	if err := token.VerifyKeyMatch(backend, sel, func() (string, error) { return "1234", nil }); err != nil {
		t.Fatalf("signature proof failed: %v", err)
	}
}

func TestVerifyKeyMatchDetectsMismatchedKey(t *testing.T) {
	cert := mustIdentity(t, "E. A.", "ckaid-1")
	other := mustIdentity(t, "Someone Else", "ckaid-1")
	backend := tokentest.New("1234").AddSlot(0, "SERIAL", "Token")
	backend.AddCert(0, cert.CKAID, cert.CertDER)
	backend.AddKey(0, token.KeyObject{CKAID: cert.CKAID, KeyType: "RSA"}, other.Priv) // wrong private key
	sel := resolve(t, backend, cert)

	if err := token.VerifyKeyMatch(backend, sel, func() (string, error) { return "1234", nil }); !errors.Is(err, token.ErrCertificateKeyMismatch) {
		t.Fatalf("error = %v, want ErrCertificateKeyMismatch", err)
	}
}

func TestVerifyKeyMatchPropagatesLoginFailure(t *testing.T) {
	id := mustIdentity(t, "E. A.", "ckaid-1")
	backend := tokentest.New("1234").AddSlot(0, "SERIAL", "Token")
	backend.AddCredential(0, id, false)
	sel := resolve(t, backend, id)

	if err := token.VerifyKeyMatch(backend, sel, func() (string, error) { return "wrong", nil }); err == nil {
		t.Fatal("proof accepted a wrong PIN")
	}
}
