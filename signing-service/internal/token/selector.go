package token

import (
	"bytes"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"fmt"
	"time"
)

// Criteria is the exact selection input. Every field that is set must match
// exactly; there is no fallback to the first slot, token, certificate, or key.
type Criteria struct {
	SlotID              uint
	CertificateSHA256   [32]byte
	KeyCKAID            []byte
	ExpectedTokenSerial string // enforced by verify/re-probe; empty on first enroll
}

// Selection is the resolved exact credential tuple plus safe certificate
// metadata used by the registry.
type Selection struct {
	SlotID         uint
	TokenSerial    string
	TokenLabel     string
	CertificateDER []byte
	SHA256         [32]byte
	KeyCKAID       []byte
	PublicKeyType  string
	PublicKeyBits  int
	Subject        string
	Issuer         string
	Serial         string
	NotBefore      time.Time
	NotAfter       time.Time

	certificate *x509.Certificate
	keyModulus  []byte
	keyExponent []byte
}

// matchKeyByCKAID returns the single key object whose CKA_ID matches, nil when
// none match, or ErrAmbiguousKey when more than one matches.
func matchKeyByCKAID(keys []KeyObject, ckaID []byte) (*KeyObject, error) {
	var matched *KeyObject
	for i := range keys {
		if bytes.Equal(keys[i].CKAID, ckaID) {
			if matched != nil {
				return nil, ErrAmbiguousKey
			}
			matched = &keys[i]
		}
	}
	return matched, nil
}

// Resolve binds exactly one slot, certificate, and private key or fails closed.
func Resolve(backend Backend, criteria Criteria) (Selection, error) {
	slots, err := backend.Slots()
	if err != nil {
		return Selection{}, err
	}
	var slot *SlotInfo
	for i := range slots {
		if slots[i].ID == criteria.SlotID {
			slot = &slots[i]
			break
		}
	}
	if slot == nil {
		return Selection{}, ErrSlotNotFound
	}
	if criteria.ExpectedTokenSerial != "" && slot.TokenSerial != criteria.ExpectedTokenSerial {
		return Selection{}, ErrTokenMismatch
	}

	objects, err := backend.Objects(slot.ID)
	if err != nil {
		return Selection{}, err
	}

	var matchedCert *CertObject
	for i := range objects.Certificates {
		if objects.Certificates[i].Fingerprint() == criteria.CertificateSHA256 {
			if matchedCert != nil {
				return Selection{}, ErrAmbiguousCertificate
			}
			matchedCert = &objects.Certificates[i]
		}
	}
	if matchedCert == nil {
		return Selection{}, ErrCertificateNotFound
	}

	// Match the key by CKA_ID. Prefer the private-key object when it is visible;
	// on tokens that hide private keys until login (CKA_PRIVATE) fall back to the
	// public-key object, which is enough to bind the tuple and cross-check the
	// public half against the certificate. The private half is proven at
	// VerifyKeyMatch (public-attribute check or a PIN-bearing signature) and again
	// at every C_Sign, which resolves the key by CKA_ID under an authenticated
	// session.
	matchedKey, err := matchKeyByCKAID(objects.PrivateKeys, criteria.KeyCKAID)
	if err != nil {
		return Selection{}, err
	}
	if matchedKey == nil {
		matchedKey, err = matchKeyByCKAID(objects.PublicKeys, criteria.KeyCKAID)
		if err != nil {
			return Selection{}, err
		}
	}
	if matchedKey == nil {
		return Selection{}, ErrKeyNotFound
	}

	certificate, err := x509.ParseCertificate(matchedCert.DER)
	if err != nil {
		return Selection{}, fmt.Errorf("parse certificate: %w", err)
	}
	rsaKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		return Selection{}, ErrUnsupportedKey
	}

	return Selection{
		SlotID:         slot.ID,
		TokenSerial:    slot.TokenSerial,
		TokenLabel:     slot.TokenLabel,
		CertificateDER: matchedCert.DER,
		SHA256:         sha256.Sum256(matchedCert.DER),
		KeyCKAID:       append([]byte(nil), matchedKey.CKAID...),
		PublicKeyType:  "RSA",
		PublicKeyBits:  rsaKey.N.BitLen(),
		Subject:        certificate.Subject.String(),
		Issuer:         certificate.Issuer.String(),
		Serial:         certificate.SerialNumber.Text(16),
		NotBefore:      certificate.NotBefore,
		NotAfter:       certificate.NotAfter,
		certificate:    certificate,
		keyModulus:     matchedKey.RSAModulus,
		keyExponent:    matchedKey.RSAExponent,
	}, nil
}
