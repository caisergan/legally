// Package validation runs local structural/cryptographic checks and a bounded
// independent PAdES validator (plan §10, Phase 1A).
package validation

import "github.com/caisergan/legally/signing-service/internal/pades"

// Local runs the internal structural and cryptographic verification.
func Local(signed []byte) error {
	return pades.VerifyLocal(signed)
}
