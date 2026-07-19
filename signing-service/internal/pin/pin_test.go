package pin

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

func testSigner(t *testing.T) (*Signer, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	return NewSigner("challenge-key-1", key, 90*time.Second), key
}

func sampleBinding() Binding {
	return Binding{
		JobID:                        "job-1",
		RequestID:                    "req-1",
		InputSHA256:                  "deadbeef",
		CertificateFingerprintSHA256: "ff00",
		PolicyVersion:                "pades-b-b-rsa-sha256-v1",
	}
}

func recipientPublic(t *testing.T, issued Issued) *ecdsa.PublicKey {
	t.Helper()
	var jwk jose.JSONWebKey
	if err := json.Unmarshal(issued.RecipientJWK, &jwk); err != nil {
		t.Fatalf("parse recipient jwk: %v", err)
	}
	pub, ok := jwk.Key.(*ecdsa.PublicKey)
	if !ok {
		t.Fatalf("recipient jwk is not an EC public key")
	}
	return pub
}

func encryptPIN(t *testing.T, pub *ecdsa.PublicKey, challengeID, pin string) string {
	t.Helper()
	encrypter, err := jose.NewEncrypter(
		jose.A256GCM,
		jose.Recipient{Algorithm: jose.ECDH_ES, Key: pub},
		(&jose.EncrypterOptions{}).WithHeader("kid", challengeID),
	)
	if err != nil {
		t.Fatalf("new encrypter: %v", err)
	}
	object, err := encrypter.Encrypt([]byte(pin))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	compact, err := object.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize: %v", err)
	}
	return compact
}

func TestChallengeSignatureVerifiesAndBindsManifest(t *testing.T) {
	signer, key := testSigner(t)
	issued, err := signer.Issue(sampleBinding())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	parsed, err := jose.ParseSigned(issued.JWS, []jose.SignatureAlgorithm{jose.ES256})
	if err != nil {
		t.Fatalf("parse jws: %v", err)
	}
	payload, err := parsed.Verify(&key.PublicKey)
	if err != nil {
		t.Fatalf("verify jws: %v", err)
	}
	var body challengePayload
	if err := json.Unmarshal(payload, &body); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if body.JobID != "job-1" || body.InputSHA256 != "deadbeef" || body.CertificateFingerprintSHA256 != "ff00" {
		t.Fatalf("manifest not bound: %+v", body)
	}
	if body.JWEAlg != EnvelopeAlg || body.JWEEnc != EnvelopeEnc || body.RecipientThumbprint != issued.Thumbprint {
		t.Fatalf("profile/thumbprint not bound: %+v", body)
	}
}

func TestEnvelopeRoundTrip(t *testing.T) {
	signer, _ := testSigner(t)
	issued, err := signer.Issue(sampleBinding())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	compact := encryptPIN(t, recipientPublic(t, issued), issued.ChallengeID, "8471")
	pin, err := DecryptPIN(compact, issued.Recipient, issued.ChallengeID)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if pin != "8471" {
		t.Fatalf("pin = %q, want 8471", pin)
	}
}

func TestEnvelopeRejectsWrongChallengeKid(t *testing.T) {
	signer, _ := testSigner(t)
	issued, err := signer.Issue(sampleBinding())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	compact := encryptPIN(t, recipientPublic(t, issued), "some-other-challenge", "8471")
	if _, err := DecryptPIN(compact, issued.Recipient, issued.ChallengeID); err != ErrEnvelopeChallenge {
		t.Fatalf("error = %v, want ErrEnvelopeChallenge", err)
	}
}

func TestEnvelopeRejectsDisallowedAlgorithm(t *testing.T) {
	signer, _ := testSigner(t)
	issued, err := signer.Issue(sampleBinding())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Encrypt with a different content encryption than the allowlisted A256GCM.
	encrypter, err := jose.NewEncrypter(
		jose.A128GCM,
		jose.Recipient{Algorithm: jose.ECDH_ES, Key: recipientPublic(t, issued)},
		(&jose.EncrypterOptions{}).WithHeader("kid", issued.ChallengeID),
	)
	if err != nil {
		t.Fatalf("new encrypter: %v", err)
	}
	object, _ := encrypter.Encrypt([]byte("8471"))
	compact, _ := object.CompactSerialize()
	if _, err := DecryptPIN(compact, issued.Recipient, issued.ChallengeID); err != ErrEnvelopeMalformed {
		t.Fatalf("error = %v, want ErrEnvelopeMalformed", err)
	}
}

func TestEnvelopeRejectsWrongRecipientKey(t *testing.T) {
	signer, _ := testSigner(t)
	issued, err := signer.Issue(sampleBinding())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	compact := encryptPIN(t, recipientPublic(t, issued), issued.ChallengeID, "8471")
	wrong, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if _, err := DecryptPIN(compact, wrong, issued.ChallengeID); err != ErrEnvelopeMalformed {
		t.Fatalf("error = %v, want ErrEnvelopeMalformed", err)
	}
}

func TestEnvelopeRejectsMalformed(t *testing.T) {
	signer, _ := testSigner(t)
	issued, _ := signer.Issue(sampleBinding())
	if _, err := DecryptPIN("not-a-jwe", issued.Recipient, issued.ChallengeID); err != ErrEnvelopeMalformed {
		t.Fatalf("error = %v, want ErrEnvelopeMalformed", err)
	}
}
