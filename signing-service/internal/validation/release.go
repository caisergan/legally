package validation

// TrustOutcome is the explicit certificate-chain / revocation verdict.
// Phase 1B requires these outcomes to be modeled and to fail closed.
type TrustOutcome struct {
	Crypto     string // valid | invalid | indeterminate
	Trust      string // valid | invalid | indeterminate
	Revocation string // valid | invalid | indeterminate
}

// ReleaseAllowed returns true only when the signature is cryptographically valid
// AND the certificate chain is trusted AND revocation is confirmed valid. Any
// invalid or indeterminate outcome (e.g. an untrusted self-signed test
// certificate) fails closed and keeps the output quarantined.
func (o TrustOutcome) ReleaseAllowed() bool {
	return o.Crypto == "valid" && o.Trust == "valid" && o.Revocation == "valid"
}
