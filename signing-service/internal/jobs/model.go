package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
)

var (
	ErrJobNotFound         = errors.New("signer job not found")
	ErrCommandNotFound     = errors.New("signer command not found")
	ErrIdempotencyConflict = errors.New("command reused with a different canonical payload")
	ErrVersionConflict     = errors.New("job version changed concurrently")
	ErrCredentialNotFound  = errors.New("credential not found")
	ErrCredentialDisabled  = errors.New("credential disabled")
	ErrCertificateMismatch = errors.New("certificate fingerprint does not match credential")
	ErrPolicyMismatch      = errors.New("signer policy does not match request")
)

// Input is the immutable source-document binding.
type Input struct {
	ArtifactID string
	ByteCount  int64
	SHA256     string
}

// Output is the reserved output-artifact binding.
type Output struct {
	ArtifactID   string
	MaxByteCount int64
}

// Approval is the owner-confirmation binding produced by FastAPI.
type Approval struct {
	ApprovalID string
	ApprovedAt time.Time
	ExpiresAt  time.Time
}

// CreateParams is the immutable job-creation command. Bearer artifact
// capabilities are deliberately excluded from the canonical payload (plan §8.2).
type CreateParams struct {
	CommandID                    string
	JobID                        string
	RequestID                    string
	CredentialID                 string
	CertificateFingerprintSHA256 string
	PolicyVersion                string
	Profile                      string
	Algorithm                    string
	Input                        Input
	Output                       Output
	Approval                     Approval
	Nonce                        string
	ExpiresAt                    time.Time
}

// Job is the durable signer job.
type Job struct {
	ID                           string
	RequestID                    string
	CommandID                    string
	CredentialID                 string
	CertificateFingerprintSHA256 string
	PolicyVersion                string
	Profile                      string
	Algorithm                    string
	Input                        Input
	Output                       Output
	OutputSHA256                 string
	OutputByteCount              int64
	QueueKey                     string
	Approval                     Approval
	Nonce                        string
	ExpiresAt                    time.Time
	ManifestSHA256               string
	State                        State
	Version                      int
	FailureCode                  errcodes.Code
	SigningClaimed               bool
	CreatedAt                    time.Time
	UpdatedAt                    time.Time
}

// Event is one durable, safe job event.
type Event struct {
	JobID     string
	Sequence  int
	Type      string
	State     State
	SafeCode  errcodes.Code
	Detail    string
	CreatedAt time.Time
}

// Command records an idempotent create command and its canonical payload hash.
type Command struct {
	CommandID              string
	CanonicalPayloadSHA256 string
	JobID                  string
	CreatedAt              time.Time
}

type canonicalPayload struct {
	CommandID                    string `json:"command_id"`
	JobID                        string `json:"job_id"`
	RequestID                    string `json:"request_id"`
	CredentialID                 string `json:"credential_id"`
	CertificateFingerprintSHA256 string `json:"certificate_fingerprint_sha256"`
	PolicyVersion                string `json:"policy_version"`
	Profile                      string `json:"profile"`
	Algorithm                    string `json:"algorithm"`
	InputArtifactID              string `json:"input_artifact_id"`
	InputByteCount               int64  `json:"input_byte_count"`
	InputSHA256                  string `json:"input_sha256"`
	OutputArtifactID             string `json:"output_artifact_id"`
	OutputMaxByteCount           int64  `json:"output_max_byte_count"`
	ApprovalID                   string `json:"approval_id"`
	ApprovedAt                   string `json:"approved_at"`
	ApprovalExpiresAt            string `json:"approval_expires_at"`
	Nonce                        string `json:"nonce"`
	ExpiresAt                    string `json:"expires_at"`
}

// CanonicalHash returns the deterministic SHA-256 over p's immutable fields.
func (p CreateParams) CanonicalHash() string {
	payload := canonicalPayload{
		CommandID:                    p.CommandID,
		JobID:                        p.JobID,
		RequestID:                    p.RequestID,
		CredentialID:                 p.CredentialID,
		CertificateFingerprintSHA256: p.CertificateFingerprintSHA256,
		PolicyVersion:                p.PolicyVersion,
		Profile:                      p.Profile,
		Algorithm:                    p.Algorithm,
		InputArtifactID:              p.Input.ArtifactID,
		InputByteCount:               p.Input.ByteCount,
		InputSHA256:                  p.Input.SHA256,
		OutputArtifactID:             p.Output.ArtifactID,
		OutputMaxByteCount:           p.Output.MaxByteCount,
		ApprovalID:                   p.Approval.ApprovalID,
		ApprovedAt:                   rfc3339(p.Approval.ApprovedAt),
		ApprovalExpiresAt:            rfc3339(p.Approval.ExpiresAt),
		Nonce:                        p.Nonce,
		ExpiresAt:                    rfc3339(p.ExpiresAt),
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func rfc3339(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
