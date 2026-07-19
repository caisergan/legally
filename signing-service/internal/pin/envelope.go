package pin

import (
	"crypto/ecdsa"
	"errors"

	jose "github.com/go-jose/go-jose/v4"
)

var (
	ErrEnvelopeMalformed = errors.New("pin envelope is malformed")
	ErrEnvelopeChallenge = errors.New("pin envelope challenge binding mismatch")
)

// DecryptPIN validates and decrypts a compact JWE PIN envelope against the
// per-challenge ephemeral recipient key. Only the ECDH-ES + A256GCM profile is
// accepted, and the protected header kid must equal the challenge ID. The
// plaintext PIN is returned to the caller and never logged or persisted.
func DecryptPIN(compactJWE string, recipient *ecdsa.PrivateKey, challengeID string) (string, error) {
	object, err := jose.ParseEncrypted(
		compactJWE,
		[]jose.KeyAlgorithm{jose.ECDH_ES},
		[]jose.ContentEncryption{jose.A256GCM},
	)
	if err != nil {
		return "", ErrEnvelopeMalformed
	}
	if object.Header.KeyID != challengeID {
		return "", ErrEnvelopeChallenge
	}
	plaintext, err := object.Decrypt(recipient)
	if err != nil {
		return "", ErrEnvelopeMalformed
	}
	return string(plaintext), nil
}
