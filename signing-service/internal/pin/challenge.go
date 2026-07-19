// Package pin implements the one-time signed PIN challenge and the encrypted
// PIN envelope (plan §3.4, §8.2). Plaintext PINs and ciphertext are never
// persisted or logged.
package pin

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

const (
	// SignatureAlg signs the challenge; EnvelopeAlg/EnvelopeEnc form the single
	// allowlisted JWE profile the browser must use.
	SignatureAlg = "ES256"
	EnvelopeAlg  = "ECDH-ES"
	EnvelopeEnc  = "A256GCM"
)

// Binding is the immutable manifest data bound into a challenge and re-checked
// on authorize.
type Binding struct {
	JobID                        string
	RequestID                    string
	InputSHA256                  string
	CertificateFingerprintSHA256 string
	PolicyVersion                string
}

// Challenge is the browser-facing challenge material.
type Challenge struct {
	ChallengeID  string
	JWS          string
	RecipientJWK json.RawMessage
	ExpiresAt    time.Time
}

// Issued adds the in-memory-only ephemeral recipient key and binding metadata.
type Issued struct {
	Challenge
	Recipient  *ecdsa.PrivateKey
	Nonce      string
	Thumbprint string
}

type challengePayload struct {
	ChallengeID                  string          `json:"challenge_id"`
	JobID                        string          `json:"job_id"`
	RequestID                    string          `json:"request_id"`
	InputSHA256                  string          `json:"input_sha256"`
	CertificateFingerprintSHA256 string          `json:"certificate_fingerprint_sha256"`
	PolicyVersion                string          `json:"policy_version"`
	Nonce                        string          `json:"nonce"`
	ExpiresAt                    string          `json:"expires_at"`
	KeyID                        string          `json:"kid"`
	JWEAlg                       string          `json:"jwe_alg"`
	JWEEnc                       string          `json:"jwe_enc"`
	RecipientJWK                 json.RawMessage `json:"recipient_jwk"`
	RecipientThumbprint          string          `json:"recipient_jwk_thumbprint"`
}

// Signer issues signed challenges under a stable key ID.
type Signer struct {
	keyID   string
	signKey *ecdsa.PrivateKey
	ttl     time.Duration
	now     func() time.Time
}

// NewSigner returns a challenge signer. signKey must be a P-256 key for ES256.
func NewSigner(keyID string, signKey *ecdsa.PrivateKey, ttl time.Duration) *Signer {
	return &Signer{keyID: keyID, signKey: signKey, ttl: ttl, now: func() time.Time { return time.Now().UTC() }}
}

// Issue generates a challenge: an ephemeral ECDH-ES recipient key, a signed JWS
// binding the manifest, and the public recipient JWK for the browser.
func (s *Signer) Issue(binding Binding) (Issued, error) {
	recipient, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Issued{}, err
	}
	publicJWK := jose.JSONWebKey{Key: &recipient.PublicKey, Algorithm: EnvelopeAlg, Use: "enc"}
	thumb, err := publicJWK.Thumbprint(crypto.SHA256)
	if err != nil {
		return Issued{}, err
	}
	thumbprint := base64.RawURLEncoding.EncodeToString(thumb)
	publicBytes, err := publicJWK.MarshalJSON()
	if err != nil {
		return Issued{}, err
	}

	challengeID := randomID("chl")
	nonce := randomID("non")
	expiresAt := s.now().Add(s.ttl)

	payload := challengePayload{
		ChallengeID:                  challengeID,
		JobID:                        binding.JobID,
		RequestID:                    binding.RequestID,
		InputSHA256:                  binding.InputSHA256,
		CertificateFingerprintSHA256: binding.CertificateFingerprintSHA256,
		PolicyVersion:                binding.PolicyVersion,
		Nonce:                        nonce,
		ExpiresAt:                    expiresAt.Format(time.RFC3339Nano),
		KeyID:                        s.keyID,
		JWEAlg:                       EnvelopeAlg,
		JWEEnc:                       EnvelopeEnc,
		RecipientJWK:                 publicBytes,
		RecipientThumbprint:          thumbprint,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return Issued{}, err
	}

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.ES256, Key: s.signKey},
		(&jose.SignerOptions{}).WithHeader("kid", s.keyID),
	)
	if err != nil {
		return Issued{}, err
	}
	signed, err := signer.Sign(payloadBytes)
	if err != nil {
		return Issued{}, err
	}
	compact, err := signed.CompactSerialize()
	if err != nil {
		return Issued{}, err
	}

	return Issued{
		Challenge: Challenge{
			ChallengeID:  challengeID,
			JWS:          compact,
			RecipientJWK: publicBytes,
			ExpiresAt:    expiresAt,
		},
		Recipient:  recipient,
		Nonce:      nonce,
		Thumbprint: thumbprint,
	}, nil
}

// PublicJWK returns the challenge-verification public JWK to pin in FastAPI.
func (s *Signer) PublicJWK() jose.JSONWebKey {
	return jose.JSONWebKey{Key: &s.signKey.PublicKey, KeyID: s.keyID, Algorithm: SignatureAlg, Use: "sig"}
}

func randomID(prefix string) string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return prefix + "-" + hex.EncodeToString(buf)
}
