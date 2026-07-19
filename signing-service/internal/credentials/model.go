// Package credentials manages the signer-local credential registry: exact
// PKCS#11 tuples, the physical-token queue key, and safe public inventory
// (plan §3.1, §5.3, §5.4).
package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"
)

var (
	ErrCredentialNotFound   = errors.New("credential not found")
	ErrCredentialExists     = errors.New("credential already enrolled")
	ErrCredentialReferenced = errors.New("credential is referenced by unreconciled jobs")
	ErrCertificateExpired   = errors.New("certificate is outside its validity window")
)

// Credential is the durable signer-local credential record (plan §5.3). Token
// serial, label, module path/digest, and CKA_ID are signer-local only.
type Credential struct {
	ServiceCredentialID   string
	PhysicalTokenQueueKey string
	ModuleAlias           string
	ModuleSHA256          string
	SlotID                uint
	TokenSerial           string
	TokenLabel            string
	CertificateDER        []byte
	CertificateSHA256     string
	KeyCKAID              string
	PublicKeyType         string
	PublicKeyBits         int
	NotBefore             time.Time
	NotAfter              time.Time
	EnrolledAt            time.Time
	LastProbedAt          *time.Time
	Enabled               bool
	LastHealth            string
}

const queueKeyDomain = "yargi-physical-token-queue-v1\x00"

// DeriveQueueKey maps a physical token serial to a stable, opaque queue key.
// Every credential on the same physical token yields the same key; it is never
// exposed to the browser.
func DeriveQueueKey(tokenSerial string) string {
	sum := sha256.Sum256([]byte(queueKeyDomain + tokenSerial))
	return "ptk-" + hex.EncodeToString(sum[:16])
}
