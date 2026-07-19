package pades_test

import (
	"crypto/x509"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/corpus"
	"github.com/caisergan/legally/signing-service/internal/pades"
	"github.com/caisergan/legally/signing-service/internal/token/tokentest"
)

func corpusSigner(t *testing.T) (*x509.Certificate, *tokentest.Identity) {
	t.Helper()
	now := time.Now()
	id, err := tokentest.NewIdentity("Ege Ayyildiz", "ckaid-corpus", now.Add(-time.Hour), now.Add(365*24*time.Hour))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	cert, err := x509.ParseCertificate(id.CertDER)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return cert, &id
}

func TestCorpusAcceptedRoundTrip(t *testing.T) {
	cert, id := corpusSigner(t)
	for name, fixture := range corpus.Accepted() {
		signed, err := pades.Sign(fixture, cert, id.Priv)
		if err != nil {
			t.Fatalf("%s: sign accepted fixture: %v", name, err)
		}
		if err := pades.VerifyLocal(signed); err != nil {
			t.Fatalf("%s: local verify: %v", name, err)
		}
	}
}

func TestCorpusRejectedFailClosed(t *testing.T) {
	cert, id := corpusSigner(t)
	for name, fixture := range corpus.Rejected() {
		if _, err := pades.Sign(fixture, cert, id.Priv); err == nil {
			t.Errorf("%s: preflight accepted a rejected-class fixture", name)
		}
	}
	if _, err := pades.Sign(corpus.Oversized(), cert, id.Priv); err == nil {
		t.Error("oversized: preflight accepted an over-limit document")
	}
}
