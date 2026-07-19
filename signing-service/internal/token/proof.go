package token

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"math/big"
)

// PINFunc supplies a PIN read from a no-echo interactive TTY. It is invoked
// only when a proof operation is required, and its result is never retained.
type PINFunc func() (string, error)

// VerifyKeyMatch confirms the selected certificate and private key form a pair.
// It cross-checks exposed public attributes without a PIN when possible, and
// otherwise performs a single PIN-bearing signature proof over a random nonce.
func VerifyKeyMatch(backend Backend, sel Selection, pinFunc PINFunc) error {
	rsaPub, ok := sel.certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		return ErrUnsupportedKey
	}

	if len(sel.keyModulus) > 0 && len(sel.keyExponent) > 0 {
		modulus := new(big.Int).SetBytes(sel.keyModulus)
		exponent := new(big.Int).SetBytes(sel.keyExponent)
		if modulus.Cmp(rsaPub.N) == 0 && exponent.IsInt64() && int(exponent.Int64()) == rsaPub.E {
			return nil
		}
		return ErrCertificateKeyMismatch
	}

	if pinFunc == nil {
		return errors.New("proof operation requires a PIN provider")
	}
	pin, err := pinFunc()
	if err != nil {
		return err
	}
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	digest := sha256.Sum256(nonce)
	signature, err := backend.Sign(sel.SlotID, sel.KeyCKAID, pin, digest[:])
	if err != nil {
		return err
	}
	if err := rsa.VerifyPKCS1v15(rsaPub, crypto.SHA256, digest[:], signature); err != nil {
		return ErrCertificateKeyMismatch
	}
	return nil
}
