// Package errcodes defines the stable, safe failure vocabulary shared across
// the signer. These codes map internal failures to caller-visible strings
// without leaking PKCS#11 or vendor error text
// (SIGNING_WORKFLOW_IMPLEMENTATION_PLAN.md §14). Raw vendor errors must never
// be substituted for these values in audit evidence or client responses.
package errcodes

// Code is a normalized, safe failure identifier.
type Code string

const (
	InvalidRequest                 Code = "INVALID_REQUEST"
	PolicyMismatch                 Code = "POLICY_MISMATCH"
	InputRejected                  Code = "INPUT_REJECTED"
	InputHashMismatch              Code = "INPUT_HASH_MISMATCH"
	CapabilityInvalid              Code = "CAPABILITY_INVALID"
	CapabilityExpired              Code = "CAPABILITY_EXPIRED"
	CredentialNotFound             Code = "CREDENTIAL_NOT_FOUND"
	CredentialDisabled             Code = "CREDENTIAL_DISABLED"
	TokenNotPresent                Code = "TOKEN_NOT_PRESENT"
	TokenMismatch                  Code = "TOKEN_MISMATCH"
	CertificateMismatch            Code = "CERTIFICATE_MISMATCH"
	KeyMismatch                    Code = "KEY_MISMATCH"
	CertificateExpired             Code = "CERTIFICATE_EXPIRED"
	CertificateTrustInvalid        Code = "CERTIFICATE_TRUST_INVALID"
	CertificateTrustIndeterminate  Code = "CERTIFICATE_TRUST_INDETERMINATE"
	ApprovalExpired                Code = "APPROVAL_EXPIRED"
	ExecutionDeadlineExpired       Code = "EXECUTION_DEADLINE_EXPIRED"
	PINWindowExpired               Code = "PIN_WINDOW_EXPIRED"
	PINRejected                    Code = "PIN_REJECTED"
	AuthorizationLostBeforeSigning Code = "AUTHORIZATION_LOST_BEFORE_SIGNING"
	AuthorizationDeliveryUnknown   Code = "AUTHORIZATION_DELIVERY_UNKNOWN"
	TokenLocked                    Code = "TOKEN_LOCKED"
	TokenSessionFailed             Code = "TOKEN_SESSION_FAILED"
	SigningFailedPreOperation      Code = "SIGNING_FAILED_PRE_OPERATION"
	SigningOutcomeUnknown          Code = "SIGNING_OUTCOME_UNKNOWN"
	FinalizationFailed             Code = "FINALIZATION_FAILED"
	LocalVerificationFailed        Code = "LOCAL_VERIFICATION_FAILED"
	IndependentValidationFailed    Code = "INDEPENDENT_VALIDATION_FAILED"
	OutputStoreFailed              Code = "OUTPUT_STORE_FAILED"
	ServiceUnavailable             Code = "SERVICE_UNAVAILABLE"
	Unauthorized                   Code = "UNAUTHORIZED"
)

// known holds every recognized safe code so callers can assert that no
// unmapped string reaches audit evidence or the wire.
var known = map[Code]struct{}{
	InvalidRequest: {}, PolicyMismatch: {}, InputRejected: {}, InputHashMismatch: {},
	CapabilityInvalid: {}, CapabilityExpired: {}, CredentialNotFound: {}, CredentialDisabled: {},
	TokenNotPresent: {}, TokenMismatch: {}, CertificateMismatch: {}, KeyMismatch: {},
	CertificateExpired: {}, CertificateTrustInvalid: {}, CertificateTrustIndeterminate: {},
	ApprovalExpired: {}, ExecutionDeadlineExpired: {}, PINWindowExpired: {}, PINRejected: {},
	AuthorizationLostBeforeSigning: {}, AuthorizationDeliveryUnknown: {}, TokenLocked: {},
	TokenSessionFailed: {}, SigningFailedPreOperation: {}, SigningOutcomeUnknown: {},
	FinalizationFailed: {}, LocalVerificationFailed: {}, IndependentValidationFailed: {},
	OutputStoreFailed: {}, ServiceUnavailable: {}, Unauthorized: {},
}

// IsKnown reports whether c is a recognized safe failure code. The empty code
// is treated as "no failure recorded" and is not itself a known failure.
func IsKnown(c Code) bool {
	_, ok := known[c]
	return ok
}
