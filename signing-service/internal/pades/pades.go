// Package pades wraps the quarantined eimza-go PAdES primitives so callers
// (token worker, validation) never import third_party directly.
package pades

import (
	"crypto"
	"crypto/x509"

	eimza "github.com/caisergan/legally/signing-service/third_party/eimza-go/pades"
)

// Sign produces a PAdES Baseline-B-B document. signer supplies the RSA
// PKCS#1 v1.5/SHA-256 signature over the CMS signed attributes; it is the sole
// seam through which the PKCS#11 token participates.
func Sign(input []byte, certificate *x509.Certificate, signer crypto.Signer) ([]byte, error) {
	return eimza.SignBaselineBB(input, certificate, signer, eimza.SignOptions{Name: "Yargı Asistan"})
}

// VerifyLocal runs local structural and cryptographic verification.
func VerifyLocal(signed []byte) error {
	_, err := eimza.VerifyCryptographic(signed)
	return err
}
