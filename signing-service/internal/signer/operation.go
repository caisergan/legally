package signer

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"io"
	"time"

	"github.com/caisergan/legally/signing-service/internal/artifacts"
	"github.com/caisergan/legally/signing-service/internal/credentials"
	"github.com/caisergan/legally/signing-service/internal/errcodes"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/pades"
	"github.com/caisergan/legally/signing-service/internal/planauth"
	"github.com/caisergan/legally/signing-service/internal/policy"
	"github.com/caisergan/legally/signing-service/internal/token"
)

// FormatEngineVersion is the allowlisted format-engine identity bound into each
// SigningPlan.
const FormatEngineVersion = "yargi-pades-b-b-v1"

const (
	pkcs11Mechanism    = "CKM_RSA_PKCS"
	trustPolicyVersion = "softhsm-test-untrusted-v1"
)

// CredentialResolver resolves a job's credential to its exact local tuple.
type CredentialResolver interface {
	Get(ctx context.Context, id string) (credentials.Credential, error)
}

// Validator runs an independent PAdES validation.
type Validator interface {
	Validate(ctx context.Context, signed []byte) (name, version, outcome string, err error)
}

// TokenOperation performs the login+sign pipeline after AUTHORIZATION_CONSUMED.
// While Phase 1B is open, a successful signature terminates in QUARANTINED.
type TokenOperation struct {
	service    *jobs.Service
	backend    token.Backend
	broker     *artifacts.Broker
	authorizer *planauth.Authorizer
	resolver   CredentialResolver
	validator  Validator
	ttl        time.Duration
	now        func() time.Time
}

func NewTokenOperation(service *jobs.Service, backend token.Backend, broker *artifacts.Broker, authorizer *planauth.Authorizer, resolver CredentialResolver, validator Validator, ttl time.Duration) *TokenOperation {
	return &TokenOperation{
		service: service, backend: backend, broker: broker, authorizer: authorizer,
		resolver: resolver, validator: validator, ttl: ttl,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (o *TokenOperation) Run(ctx context.Context, job jobs.Job, pinValue string) error {
	cred, err := o.resolver.Get(ctx, job.CredentialID)
	if err != nil {
		return o.fail(ctx, job.ID, jobs.StateFailedPreSign, errcodes.CredentialNotFound, "credential unavailable")
	}
	if !cred.Enabled {
		return o.fail(ctx, job.ID, jobs.StateFailedPreSign, errcodes.CredentialDisabled, "credential disabled")
	}

	readCap, err := o.broker.MintRead(job.ID, job.Input.ArtifactID, job.Input.SHA256, job.Input.ByteCount)
	if err != nil {
		return o.fail(ctx, job.ID, jobs.StateFailedPreSign, errcodes.InputRejected, "input unavailable")
	}
	input, err := o.broker.Fetch(readCap)
	if err != nil {
		return o.fail(ctx, job.ID, jobs.StateFailedPreSign, errcodes.InputRejected, "input fetch failed")
	}
	if sum := sha256.Sum256(input); hex.EncodeToString(sum[:]) != job.Input.SHA256 {
		return o.fail(ctx, job.ID, jobs.StateFailedPreSign, errcodes.InputHashMismatch, "input hash mismatch")
	}

	certificate, err := x509.ParseCertificate(cred.CertificateDER)
	if err != nil {
		return o.fail(ctx, job.ID, jobs.StateFailedPreSign, errcodes.CredentialNotFound, "credential certificate invalid")
	}
	ckaID, err := hex.DecodeString(cred.KeyCKAID)
	if err != nil {
		return o.fail(ctx, job.ID, jobs.StateFailedPreSign, errcodes.KeyMismatch, "key id invalid")
	}

	inputHash := job.Input.SHA256
	plan := planauth.SigningPlan{
		SchemaVersion: planauth.SchemaVersion, JobID: job.ID, CommandID: job.CommandID, JobVersion: job.Version,
		InputArtifactID: job.Input.ArtifactID, InputSHA256: inputHash, InputByteCount: job.Input.ByteCount,
		PreparedContentSHA256: inputHash, SignedAttributesSHA256: inputHash,
		CertificateFingerprintSHA256: job.CertificateFingerprintSHA256, Profile: job.Profile, Algorithm: job.Algorithm,
		Mechanism: pkcs11Mechanism, PolicyVersion: job.PolicyVersion, FormatEngineVersion: FormatEngineVersion,
		Nonce: randomNonce(), ExpiresAt: o.now().Add(o.ttl),
	}
	manifest := planauth.JobManifest{
		JobID: job.ID, CommandID: job.CommandID, Version: job.Version,
		InputArtifactID: job.Input.ArtifactID, InputSHA256: inputHash, InputByteCount: job.Input.ByteCount,
		CertificateFingerprintSHA256: job.CertificateFingerprintSHA256, Profile: job.Profile, Algorithm: job.Algorithm,
		Mechanism: pkcs11Mechanism, PolicyVersion: policy.Version,
	}
	authorization, err := o.authorizer.Authorize(plan, manifest)
	if err != nil {
		return o.fail(ctx, job.ID, jobs.StateFailedPreSign, errcodes.PolicyMismatch, "plan authorization refused")
	}

	signer := &tokenSigner{op: o, ctx: ctx, job: job, pin: pinValue, cred: cred, ckaID: ckaID, pub: certificate.PublicKey, plan: plan, auth: authorization}
	signed, signErr := pades.Sign(input, certificate, signer)
	if signErr != nil {
		switch {
		case !signer.signed:
			return o.fail(ctx, job.ID, jobs.StateFailedPreSign, errcodes.SigningFailedPreOperation, "signing failed before the token operation")
		case signer.backendErr != nil:
			return o.fail(ctx, job.ID, jobs.StateOutcomeUnknown, errcodes.SigningOutcomeUnknown, "token outcome unknown")
		default:
			return o.fail(ctx, job.ID, jobs.StateFailedPostSign, errcodes.FinalizationFailed, "finalization failed after signing")
		}
	}
	job = signer.job // now SIGNING

	if err := pades.VerifyLocal(signed); err != nil {
		return o.fail(ctx, job.ID, jobs.StateFailedPostSign, errcodes.LocalVerificationFailed, "local verification failed")
	}

	if o.validator != nil {
		name, version, outcome, verr := o.validator.Validate(ctx, signed)
		_ = o.service.RecordValidation(ctx, job.ID, jobs.ValidationEvidence{
			ValidatorName: name, ValidatorVersion: version, TrustPolicyVersion: trustPolicyVersion,
			PlanSHA256: plan.CanonicalHash(), InputSHA256: inputHash, OutputSHA256: sha256Hex(signed), Outcome: outcomeOrError(outcome, verr),
		})
		if verr != nil || outcome != "valid" {
			return o.fail(ctx, job.ID, jobs.StateQuarantined, errcodes.IndependentValidationFailed, "independent validation did not pass")
		}
	}

	writeCap := o.broker.MintWrite(job.ID, job.Output.ArtifactID, job.Output.MaxByteCount)
	outputHash, outputSize, err := o.broker.Store(writeCap, signed)
	if err != nil {
		return o.fail(ctx, job.ID, jobs.StateFailedPostSign, errcodes.OutputStoreFailed, "output store failed")
	}
	if err := o.service.RecordOutput(ctx, job.ID, outputHash, outputSize); err != nil {
		return err
	}
	_ = o.service.RecordValidation(ctx, job.ID, jobs.ValidationEvidence{
		ValidatorName: "local", ValidatorVersion: FormatEngineVersion, TrustPolicyVersion: trustPolicyVersion,
		PlanSHA256: plan.CanonicalHash(), InputSHA256: inputHash, OutputSHA256: outputHash, Outcome: "valid",
	})

	// Phase 1B is open: engineering output is quarantined, never COMPLETED.
	_, err = o.service.Transition(ctx, job.ID, job.Version, jobs.StateQuarantined, "", "engineering output quarantined pending Phase 1B closure")
	return err
}

func (o *TokenOperation) fail(ctx context.Context, jobID string, to jobs.State, code errcodes.Code, detail string) error {
	current, err := o.service.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if current.State.IsTerminal() {
		return nil
	}
	_, err = o.service.Transition(ctx, jobID, current.Version, to, code, detail)
	return err
}

type tokenSigner struct {
	op         *TokenOperation
	ctx        context.Context
	job        jobs.Job
	pin        string
	cred       credentials.Credential
	ckaID      []byte
	pub        crypto.PublicKey
	plan       planauth.SigningPlan
	auth       planauth.PlanAuthorization
	signed     bool
	backendErr error
}

func (t *tokenSigner) Public() crypto.PublicKey { return t.pub }

func (t *tokenSigner) Sign(_ io.Reader, digest []byte, _ crypto.SignerOpts) ([]byte, error) {
	// The supervisor authorization is consumed one-use immediately before the
	// AUTHORIZATION_CONSUMED -> SIGNING claim and the single C_Sign.
	if err := t.op.authorizer.Verify(t.auth, t.plan); err != nil {
		return nil, err
	}
	signing, err := t.op.service.Transition(t.ctx, t.job.ID, t.job.Version, jobs.StateSigning, "", "signing")
	if err != nil {
		return nil, err
	}
	t.job = signing
	t.signed = true
	signature, err := t.op.backend.Sign(t.cred.SlotID, t.ckaID, t.pin, digest)
	if err != nil {
		t.backendErr = err
		return nil, err
	}
	return signature, nil
}

func randomNonce() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func outcomeOrError(outcome string, err error) string {
	if err != nil {
		return "error"
	}
	return outcome
}
