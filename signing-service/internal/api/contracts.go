package api

import (
	"encoding/json"

	"github.com/caisergan/legally/signing-service/internal/policy"
)

// HealthResponse carries only safe service metadata plus readiness that cannot
// expose credential inventory (plan §8.2 GET /v1/health).
type HealthResponse struct {
	Status   string                `json:"status"`
	Ready    bool                  `json:"ready"`
	Database string                `json:"database"`
	Mode     string                `json:"mode"`
	Version  string                `json:"version"`
	Policy   policy.CreationPolicy `json:"policy"`
}

// ErrorResponse carries a safe normalized code plus human-readable detail.
type ErrorResponse struct {
	Code   string `json:"code,omitempty"`
	Detail string `json:"detail"`
}

// The following DTOs define the strict internal job/credential contracts
// (plan §8.2). All request models reject unknown fields; artifact bearer
// capabilities are never part of the durable canonical payload.

type CreateJobRequest struct {
	CommandID                    string      `json:"command_id"`
	JobID                        string      `json:"job_id"`
	RequestID                    string      `json:"request_id"`
	ExpectedRequestVersion       int         `json:"expected_request_version"`
	CredentialID                 string      `json:"credential_id"`
	CertificateFingerprintSHA256 string      `json:"certificate_fingerprint_sha256"`
	PolicyVersion                string      `json:"policy_version"`
	Profile                      string      `json:"profile"`
	Algorithm                    string      `json:"algorithm"`
	Input                        JobInput    `json:"input"`
	Output                       JobOutput   `json:"output"`
	Approval                     JobApproval `json:"approval"`
	Nonce                        string      `json:"nonce"`
	ExpiresAt                    string      `json:"expires_at"`
}

type JobInput struct {
	ArtifactID string `json:"artifact_id"`
	ByteCount  int64  `json:"byte_count"`
	SHA256     string `json:"sha256"`
}

type JobOutput struct {
	ArtifactID   string `json:"artifact_id"`
	MaxByteCount int64  `json:"max_byte_count"`
}

type JobApproval struct {
	ApprovalID string `json:"approval_id"`
	ApprovedAt string `json:"approved_at"`
	ExpiresAt  string `json:"expires_at"`
}

type CreateJobResponse struct {
	ID            string `json:"id"`
	RequestID     string `json:"request_id"`
	State         string `json:"state"`
	Version       int    `json:"version"`
	EventSequence int    `json:"event_sequence"`
	CreatedAt     string `json:"created_at"`
}

type AuthorizeRequest struct {
	ChallengeID string `json:"challenge_id"`
	PINJWE      string `json:"pin_jwe"`
}

// CredentialInventoryItem is the safe public credential metadata returned to
// FastAPI. It never exposes module paths, module digests, slot IDs, full token
// serials, token labels, or CKA_ID values (plan §8.2 GET /v1/credentials).
type CredentialInventoryItem struct {
	ID                           string `json:"id"`
	CertificateFingerprintSHA256 string `json:"certificate_fingerprint_sha256"`
	SubjectDisplay               string `json:"subject_display"`
	IssuerDisplay                string `json:"issuer_display"`
	SerialSuffix                 string `json:"serial_suffix"`
	NotBefore                    string `json:"not_before"`
	NotAfter                     string `json:"not_after"`
	PublicKeyType                string `json:"public_key_type"`
	PublicKeyBits                int    `json:"public_key_bits"`
	Mode                         string `json:"mode"`
	Status                       string `json:"status"`
	LastCheckedAt                string `json:"last_checked_at,omitempty"`
}

type CredentialsResponse struct {
	Items []CredentialInventoryItem `json:"items"`
}

type CommandStatusResponse struct {
	CommandID string `json:"command_id"`
	JobID     string `json:"job_id"`
	State     string `json:"state"`
}

type JobStateResponse struct {
	ID                           string `json:"id"`
	RequestID                    string `json:"request_id"`
	State                        string `json:"state"`
	Version                      int    `json:"version"`
	CertificateFingerprintSHA256 string `json:"certificate_fingerprint_sha256"`
	InputSHA256                  string `json:"input_sha256"`
	InputByteCount               int64  `json:"input_byte_count"`
	PolicyVersion                string `json:"policy_version"`
	FailureCode                  string `json:"failure_code,omitempty"`
	OutputSHA256                 string `json:"output_sha256,omitempty"`
	OutputByteCount              int64  `json:"output_byte_count,omitempty"`
	CancellationEligible         bool   `json:"cancellation_eligible"`
	CreatedAt                    string `json:"created_at"`
	UpdatedAt                    string `json:"updated_at"`
}

type EventResponse struct {
	Sequence  int    `json:"sequence"`
	Type      string `json:"type"`
	State     string `json:"state,omitempty"`
	SafeCode  string `json:"safe_code,omitempty"`
	Detail    string `json:"detail,omitempty"`
	CreatedAt string `json:"created_at"`
}

type EventsResponse struct {
	Items []EventResponse `json:"items"`
}

type PINChallengeResponse struct {
	ChallengeID  string          `json:"challenge_id"`
	ChallengeJWS string          `json:"challenge_jws"`
	RecipientJWK json.RawMessage `json:"recipient_jwk"`
	ExpiresAt    string          `json:"expires_at"`
}

type AuthorizeResponse struct {
	AuthorizationStatus string `json:"authorization_status"`
}
