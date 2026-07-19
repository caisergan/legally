//go:build !pkcs11

package token

// NewBackend reports that PKCS#11 support was not compiled in. Build with
// `-tags pkcs11` (and cgo plus an allowlisted module) to enable the real
// adapter. Selection, proof, and registry logic remain fully testable without
// it via an in-memory Backend.
func NewBackend(modulePath string) (Backend, error) {
	return nil, ErrPKCS11Unavailable
}
