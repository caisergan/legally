// Package planauth implements the immutable SigningPlan and the supervisor's
// one-use PlanAuthorization (plan §10). The authorization key is held only by
// the supervisor and is never available to sandboxed workers.
package planauth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const SchemaVersion = 1

var (
	ErrPlanMismatch          = errors.New("signing plan does not match the job manifest")
	ErrEngineNotAllowed      = errors.New("format-engine version is not allowlisted")
	ErrPlanExpired           = errors.New("signing plan expired")
	ErrAuthorizationForged   = errors.New("plan authorization signature is invalid")
	ErrAuthorizationReplayed = errors.New("plan authorization already consumed")
	ErrAuthorizationExpired  = errors.New("plan authorization expired")
	ErrPlanHashMismatch      = errors.New("plan authorization is bound to a different plan")
)

type SigningPlan struct {
	SchemaVersion                int       `json:"schema_version"`
	JobID                        string    `json:"job_id"`
	CommandID                    string    `json:"command_id"`
	JobVersion                   int       `json:"job_version"`
	InputArtifactID              string    `json:"input_artifact_id"`
	InputSHA256                  string    `json:"input_sha256"`
	InputByteCount               int64     `json:"input_byte_count"`
	PreparedContentSHA256        string    `json:"prepared_content_sha256"`
	SignedAttributesSHA256       string    `json:"signed_attributes_sha256"`
	CertificateFingerprintSHA256 string    `json:"certificate_fingerprint_sha256"`
	Profile                      string    `json:"profile"`
	Algorithm                    string    `json:"algorithm"`
	Mechanism                    string    `json:"mechanism"`
	PolicyVersion                string    `json:"policy_version"`
	FormatEngineVersion          string    `json:"format_engine_version"`
	Nonce                        string    `json:"nonce"`
	ExpiresAt                    time.Time `json:"expires_at"`
}

func (p SigningPlan) CanonicalHash() string {
	encoded, _ := json.Marshal(p)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

// JobManifest is the trusted immutable job data the supervisor checks against.
type JobManifest struct {
	JobID                        string
	CommandID                    string
	Version                      int
	InputArtifactID              string
	InputSHA256                  string
	InputByteCount               int64
	CertificateFingerprintSHA256 string
	Profile                      string
	Algorithm                    string
	Mechanism                    string
	PolicyVersion                string
}

type PlanAuthorization struct {
	JobID                        string
	CommandID                    string
	JobVersion                   int
	InputSHA256                  string
	InputByteCount               int64
	PreparedContentSHA256        string
	SignedAttributesSHA256       string
	CertificateFingerprintSHA256 string
	PolicyVersion                string
	Mechanism                    string
	CanonicalPlanSHA256          string
	Nonce                        string
	ExpiresAt                    time.Time
	MAC                          string
}

type Authorizer struct {
	key      []byte
	engines  map[string]struct{}
	ttl      time.Duration
	now      func() time.Time
	mu       sync.Mutex
	consumed map[string]struct{}
}

func NewAuthorizer(key []byte, allowedEngines []string, ttl time.Duration) *Authorizer {
	engines := map[string]struct{}{}
	for _, engine := range allowedEngines {
		engines[engine] = struct{}{}
	}
	return &Authorizer{
		key:      append([]byte(nil), key...),
		engines:  engines,
		ttl:      ttl,
		now:      func() time.Time { return time.Now().UTC() },
		consumed: map[string]struct{}{},
	}
}

func (a *Authorizer) Authorize(plan SigningPlan, manifest JobManifest) (PlanAuthorization, error) {
	if plan.SchemaVersion != SchemaVersion ||
		plan.JobID != manifest.JobID ||
		plan.CommandID != manifest.CommandID ||
		plan.JobVersion != manifest.Version ||
		plan.InputArtifactID != manifest.InputArtifactID ||
		plan.InputSHA256 != manifest.InputSHA256 ||
		plan.InputByteCount != manifest.InputByteCount ||
		plan.CertificateFingerprintSHA256 != manifest.CertificateFingerprintSHA256 ||
		plan.Profile != manifest.Profile ||
		plan.Algorithm != manifest.Algorithm ||
		plan.Mechanism != manifest.Mechanism ||
		plan.PolicyVersion != manifest.PolicyVersion {
		return PlanAuthorization{}, ErrPlanMismatch
	}
	if _, ok := a.engines[plan.FormatEngineVersion]; !ok {
		return PlanAuthorization{}, ErrEngineNotAllowed
	}
	if a.now().After(plan.ExpiresAt) {
		return PlanAuthorization{}, ErrPlanExpired
	}

	authorization := PlanAuthorization{
		JobID:                        plan.JobID,
		CommandID:                    plan.CommandID,
		JobVersion:                   plan.JobVersion,
		InputSHA256:                  plan.InputSHA256,
		InputByteCount:               plan.InputByteCount,
		PreparedContentSHA256:        plan.PreparedContentSHA256,
		SignedAttributesSHA256:       plan.SignedAttributesSHA256,
		CertificateFingerprintSHA256: plan.CertificateFingerprintSHA256,
		PolicyVersion:                plan.PolicyVersion,
		Mechanism:                    plan.Mechanism,
		CanonicalPlanSHA256:          plan.CanonicalHash(),
		Nonce:                        plan.Nonce,
		ExpiresAt:                    a.now().Add(a.ttl),
	}
	authorization.MAC = a.mac(authorization)
	return authorization, nil
}

// Verify authenticates the authorization, binds it to plan, enforces expiry,
// and consumes the nonce exactly once.
func (a *Authorizer) Verify(authorization PlanAuthorization, plan SigningPlan) error {
	expected := a.mac(authorization)
	if !hmac.Equal([]byte(expected), []byte(authorization.MAC)) {
		return ErrAuthorizationForged
	}
	if authorization.CanonicalPlanSHA256 != plan.CanonicalHash() {
		return ErrPlanHashMismatch
	}
	if a.now().After(authorization.ExpiresAt) {
		return ErrAuthorizationExpired
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.consumed[authorization.Nonce]; ok {
		return ErrAuthorizationReplayed
	}
	a.consumed[authorization.Nonce] = struct{}{}
	return nil
}

func (a *Authorizer) mac(authorization PlanAuthorization) string {
	authorization.MAC = ""
	payload, _ := json.Marshal(authorization)
	mac := hmac.New(sha256.New, a.key)
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
