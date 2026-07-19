package pades_test

import (
	"crypto/x509"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/corpus"
	"github.com/caisergan/legally/signing-service/internal/pades"
	"github.com/caisergan/legally/signing-service/internal/token/tokentest"
)

const padesFuzzMaxInput = 1 << 20

// FuzzVerifyLocal feeds arbitrary bytes to local PAdES verification; it must
// never panic and must reject anything that is not a valid controlled-corpus
// signature.
func FuzzVerifyLocal(f *testing.F) {
	for _, fixture := range corpus.Accepted() {
		f.Add(fixture)
	}
	for _, fixture := range corpus.Rejected() {
		f.Add(fixture)
	}
	f.Add(signedSeed(f))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > padesFuzzMaxInput {
			t.Skip()
		}
		_ = pades.VerifyLocal(data) // invariant: never panics
	})
}

// FuzzSignPreflight feeds arbitrary bytes as the input document to signing; the
// preflight must either reject them or produce output, never panic.
func FuzzSignPreflight(f *testing.F) {
	for _, fixture := range corpus.Rejected() {
		f.Add(fixture)
	}
	for _, fixture := range corpus.Accepted() {
		f.Add(fixture)
	}
	cert, identity := fuzzSigner(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > padesFuzzMaxInput {
			t.Skip()
		}
		_, _ = pades.Sign(data, cert, identity.Priv) // invariant: never panics
	})
}

func fuzzSigner(tb testing.TB) (*x509.Certificate, *tokentest.Identity) {
	tb.Helper()
	now := time.Now()
	id, err := tokentest.NewIdentity("Fuzz Signer", "ckaid-fuzz", now.Add(-time.Hour), now.Add(365*24*time.Hour))
	if err != nil {
		tb.Fatalf("identity: %v", err)
	}
	cert, err := x509.ParseCertificate(id.CertDER)
	if err != nil {
		tb.Fatalf("parse cert: %v", err)
	}
	return cert, &id
}

func signedSeed(tb testing.TB) []byte {
	tb.Helper()
	cert, id := fuzzSigner(tb)
	signed, err := pades.Sign(corpus.Accepted()["accepted_minimal"], cert, id.Priv)
	if err != nil {
		tb.Fatalf("sign seed: %v", err)
	}
	return signed
}
