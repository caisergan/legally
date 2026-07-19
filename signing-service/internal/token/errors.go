package token

import (
	"errors"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
)

var (
	ErrModuleNotAllowlisted = errors.New("pkcs11 module is not allowlisted")
	ErrModuleDigestMismatch = errors.New("pkcs11 module digest does not match the allowlist")
	ErrModulePathUnsafe     = errors.New("pkcs11 module path is not a canonical, symlink-free file")
	ErrModuleModeNotAllowed = errors.New("pkcs11 module is not allowlisted for this mode")
	ErrModuleOwnership      = errors.New("pkcs11 module has unexpected ownership or permissions")

	ErrSlotNotFound           = errors.New("no token present in the requested slot")
	ErrTokenMismatch          = errors.New("token serial does not match the enrolled credential")
	ErrCertificateNotFound    = errors.New("no certificate matches the requested fingerprint")
	ErrAmbiguousCertificate   = errors.New("multiple certificates match the requested fingerprint")
	ErrKeyNotFound            = errors.New("no private key matches the requested CKA_ID")
	ErrAmbiguousKey           = errors.New("multiple private keys match the requested CKA_ID")
	ErrCertificateKeyMismatch = errors.New("certificate public key does not match the private key")
	ErrUnsupportedKey         = errors.New("only RSA keys are supported")

	ErrPKCS11Unavailable = errors.New("signer was built without pkcs11 support; rebuild with -tags pkcs11")
)

// SafeCode maps a token error to a normalized safe code (plan §14). Unmapped
// errors collapse to SIGNING_FAILED_PRE_OPERATION so vendor text never leaks.
func SafeCode(err error) errcodes.Code {
	switch {
	case errors.Is(err, ErrSlotNotFound), errors.Is(err, ErrTokenMismatch):
		return errcodes.TokenMismatch
	case errors.Is(err, ErrCertificateNotFound), errors.Is(err, ErrAmbiguousCertificate):
		return errcodes.CertificateMismatch
	case errors.Is(err, ErrKeyNotFound), errors.Is(err, ErrAmbiguousKey), errors.Is(err, ErrCertificateKeyMismatch):
		return errcodes.KeyMismatch
	case errors.Is(err, ErrModuleNotAllowlisted), errors.Is(err, ErrModuleDigestMismatch),
		errors.Is(err, ErrModulePathUnsafe), errors.Is(err, ErrModuleModeNotAllowed),
		errors.Is(err, ErrModuleOwnership):
		return errcodes.CredentialNotFound
	default:
		return errcodes.SigningFailedPreOperation
	}
}
