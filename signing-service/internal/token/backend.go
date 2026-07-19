// Package token performs exact, no-fallback PKCS#11 credential selection and
// proof (plan §3.1, §5). The concrete PKCS#11 module is abstracted behind
// Backend so selection logic is testable without hardware; the real cgo
// adapter lives behind the `pkcs11` build tag.
package token

import "crypto/sha256"

// SlotInfo identifies a slot with a present token. Serial and label are
// signer-local only and must never reach browser-visible records.
type SlotInfo struct {
	ID          uint
	TokenSerial string
	TokenLabel  string
}

// CertObject is a certificate object on a token.
type CertObject struct {
	CKAID []byte
	DER   []byte
}

// Fingerprint returns the SHA-256 of the certificate DER.
func (c CertObject) Fingerprint() [32]byte {
	return sha256.Sum256(c.DER)
}

// KeyObject is a private-key object on a token. RSAModulus/RSAExponent are
// populated only when the token exposes public attributes on the private key,
// which lets enrollment avoid a PIN-bearing proof operation.
type KeyObject struct {
	CKAID       []byte
	KeyType     string
	RSAModulus  []byte
	RSAExponent []byte
}

// SlotObjects holds the certificate, private-key, and public-key objects in one
// slot. On tokens that mark private keys CKA_PRIVATE (hidden until login), the
// public-key object stays visible and carries the same CKA_ID and RSA public
// attributes, so enrollment can still bind the credential without a PIN.
type SlotObjects struct {
	Certificates []CertObject
	PrivateKeys  []KeyObject
	PublicKeys   []KeyObject
}

// Backend is the minimal PKCS#11 surface needed for enrollment and proof.
type Backend interface {
	Slots() ([]SlotInfo, error)
	Objects(slotID uint) (SlotObjects, error)
	// Sign logs in with pin, signs digest with the key identified by keyCKAID
	// using the fixed RSA PKCS#1 v1.5 mechanism, logs out, and returns the
	// signature. The pin is never retained.
	Sign(slotID uint, keyCKAID []byte, pin string, digest []byte) ([]byte, error)
	Close() error
}
