//go:build pkcs11

// Package integration drives a full live signing flow against a real SoftHSM
// token through the exact assembly.Build wiring the daemon serves: enroll a
// credential, create a job, reach PIN_REQUIRED, relay an encrypted PIN envelope,
// perform the single SoftHSM C_Sign, and verify the PAdES output with the local
// verifier and any available independent validators. The output correctly stays
// QUARANTINED because the SoftHSM certificate is self-signed (trust
// indeterminate); this proves the pipeline, never a qualified signature.
//
// The test is opt-in: it skips unless SCRATCH_MODULE, SCRATCH_CERT_DER,
// SCRATCH_PIN, SCRATCH_CKAID, and SCRATCH_SLOT point at a provisioned token
// (see scripts/provision-softhsm-test-token.sh). SOFTHSM2_CONF must select that
// token's isolated store.
package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"github.com/caisergan/legally/signing-service/internal/assembly"
	"github.com/caisergan/legally/signing-service/internal/corpus"
	"github.com/caisergan/legally/signing-service/internal/credentials"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/pades"
	"github.com/caisergan/legally/signing-service/internal/persistence"
	"github.com/caisergan/legally/signing-service/internal/pin"
	"github.com/caisergan/legally/signing-service/internal/policy"
	"github.com/caisergan/legally/signing-service/internal/signer"
	"github.com/caisergan/legally/signing-service/internal/token"
	"github.com/caisergan/legally/signing-service/internal/validation"
)

type liveEnv struct {
	module   string
	certDER  []byte
	certHash [32]byte
	pin      string
	ckaID    []byte
	slot     uint
}

func loadLiveEnv(t *testing.T) liveEnv {
	t.Helper()
	module := os.Getenv("SCRATCH_MODULE")
	certPath := os.Getenv("SCRATCH_CERT_DER")
	pinValue := os.Getenv("SCRATCH_PIN")
	ckaHex := os.Getenv("SCRATCH_CKAID")
	slotStr := os.Getenv("SCRATCH_SLOT")
	if module == "" || certPath == "" || pinValue == "" || ckaHex == "" || slotStr == "" {
		t.Skip("live SoftHSM env not set (SCRATCH_MODULE/CERT_DER/PIN/CKAID/SLOT)")
	}
	certDER, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatalf("read cert: %v", err)
	}
	cka, err := hex.DecodeString(ckaHex)
	if err != nil {
		t.Fatalf("ckaid: %v", err)
	}
	slot, err := strconv.ParseUint(slotStr, 10, 64)
	if err != nil {
		t.Fatalf("slot: %v", err)
	}
	return liveEnv{module: module, certDER: certDER, certHash: sha256.Sum256(certDER), pin: pinValue, ckaID: cka, slot: uint(slot)}
}

func TestLiveSoftHSMEndToEnd(t *testing.T) {
	env := loadLiveEnv(t)
	ctx := context.Background()

	db, err := persistence.Open(filepath.Join(t.TempDir(), "signerd.db"))
	if err != nil {
		t.Fatalf("open journal: %v", err)
	}
	defer db.Close()

	backend, err := token.NewBackend(env.module)
	if err != nil {
		t.Fatalf("open pkcs11 module: %v", err)
	}
	defer backend.Close()

	challengeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("challenge key: %v", err)
	}
	supervisorKey := make([]byte, 32)
	if _, err := rand.Read(supervisorKey); err != nil {
		t.Fatalf("supervisor key: %v", err)
	}

	moduleBytes, err := os.ReadFile(env.module)
	if err != nil {
		t.Fatalf("read module: %v", err)
	}
	moduleHash := sha256.Sum256(moduleBytes)

	components := assembly.Build(assembly.Deps{
		DB:               db.DB,
		Backend:          backend,
		ChallengeKeyID:   "challenge-live",
		ChallengeKey:     challengeKey,
		SupervisorKey:    supervisorKey,
		Validator:        liveValidator(t),
		PINWindow:        90 * time.Second,
		PlanTTL:          60 * time.Second,
		AuthorizationTTL: 60 * time.Second,
		CapabilityTTL:    60 * time.Second,
	})

	cred, err := components.Registry.Enroll(ctx, backend, credentials.EnrollParams{
		ModuleAlias:       "softhsm-test",
		ModuleSHA256:      hex.EncodeToString(moduleHash[:]),
		SlotID:            env.slot,
		CertificateSHA256: env.certHash,
		KeyCKAID:          env.ckaID,
	}, func() (string, error) { return env.pin, nil })
	if err != nil {
		t.Fatalf("enroll credential: %v", err)
	}
	t.Logf("enrolled credential %s fingerprint=%s", cred.ServiceCredentialID, cred.CertificateSHA256)

	inputPDF := corpus.Accepted()["accepted_minimal"]
	inputArtifactID := "in-" + randomHex()
	outputArtifactID := "out-" + randomHex()
	inputHash := components.Broker.PutInput(inputArtifactID, inputPDF)

	now := time.Now().UTC()
	job, created, err := components.Jobs.Create(ctx, jobs.CreateParams{
		CommandID:                    "cmd-" + randomHex(),
		JobID:                        "job-" + randomHex(),
		RequestID:                    "req-" + randomHex(),
		CredentialID:                 cred.ServiceCredentialID,
		CertificateFingerprintSHA256: cred.CertificateSHA256,
		PolicyVersion:                policy.Version,
		Profile:                      string(policy.ProfilePAdESBaselineBB),
		Algorithm:                    string(policy.AlgorithmRSAPKCS1SHA256),
		Input:                        jobs.Input{ArtifactID: inputArtifactID, ByteCount: int64(len(inputPDF)), SHA256: inputHash},
		Output:                       jobs.Output{ArtifactID: outputArtifactID, MaxByteCount: 5 << 20},
		Approval:                     jobs.Approval{ApprovalID: "apr-" + randomHex(), ApprovedAt: now, ExpiresAt: now.Add(5 * time.Minute)},
		Nonce:                        randomHex(),
		ExpiresAt:                    now.Add(time.Hour),
	})
	if err != nil || !created {
		t.Fatalf("create job: created=%v err=%v", created, err)
	}
	if err := components.Coordinator.Submit(ctx, job); err != nil {
		t.Fatalf("submit job: %v", err)
	}

	waitForState(t, components.Jobs, job.ID, jobs.StatePINRequired, 5*time.Second)

	challenge, err := components.Coordinator.Challenge(job.ID)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	envelope := encryptPINEnvelope(t, challenge, env.pin)
	status, err := components.Coordinator.Authorize(ctx, job.ID, challenge.ChallengeID, envelope)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	t.Logf("authorization status=%s", status)

	final := waitForTerminal(t, components.Jobs, job.ID, 20*time.Second)
	if final.State != jobs.StateQuarantined {
		t.Fatalf("final state = %q, want QUARANTINED", final.State)
	}
	if final.FailureCode != "" {
		t.Fatalf("clean quarantine expected, got failure code %q", final.FailureCode)
	}

	signed, ok := components.Broker.Output(outputArtifactID)
	if !ok {
		t.Fatal("signed output missing from broker")
	}
	if err := pades.VerifyLocal(signed); err != nil {
		t.Fatalf("local PAdES verify failed: %v", err)
	}
	t.Logf("live SoftHSM signature produced: %d bytes, output stays QUARANTINED (self-signed => trust indeterminate)", len(signed))

	assertIndependentValidators(t, signed)
}

func waitForState(t *testing.T, svc *jobs.Service, jobID string, want jobs.State, timeout time.Duration) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		job, err := svc.Get(context.Background(), jobID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if job.State == want {
			return job
		}
		if job.State.IsTerminal() {
			t.Fatalf("job reached terminal %q before %q", job.State, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %q, stuck at %q", want, job.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForTerminal(t *testing.T, svc *jobs.Service, jobID string, timeout time.Duration) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		job, err := svc.Get(context.Background(), jobID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if job.State.IsTerminal() {
			return job
		}
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for terminal state, stuck at %q", job.State)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func encryptPINEnvelope(t *testing.T, challenge pin.Challenge, pinValue string) string {
	t.Helper()
	var recipient jose.JSONWebKey
	if err := recipient.UnmarshalJSON(challenge.RecipientJWK); err != nil {
		t.Fatalf("parse recipient jwk: %v", err)
	}
	encrypter, err := jose.NewEncrypter(
		jose.A256GCM,
		jose.Recipient{Algorithm: jose.ECDH_ES, Key: recipient.Key},
		(&jose.EncrypterOptions{}).WithHeader("kid", challenge.ChallengeID),
	)
	if err != nil {
		t.Fatalf("new encrypter: %v", err)
	}
	object, err := encrypter.Encrypt([]byte(pinValue))
	if err != nil {
		t.Fatalf("encrypt pin: %v", err)
	}
	compact, err := object.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize envelope: %v", err)
	}
	return compact
}

// liveValidator wires a MultiValidator into the operation only when at least one
// independent validator is available, so an offline run still quarantines
// cleanly through the local verifier.
func liveValidator(t *testing.T) signer.Validator {
	t.Helper()
	validators := availableValidators(t)
	if len(validators) == 0 {
		return nil
	}
	return &validation.MultiValidator{Validators: validators}
}

// assertIndependentValidators re-verifies the output with each available
// validator and confirms a one-byte-tampered copy is rejected.
func assertIndependentValidators(t *testing.T, signed []byte) {
	t.Helper()
	validators := availableValidators(t)
	if len(validators) == 0 {
		t.Log("no independent validators available; skipped external verification")
		return
	}
	tampered := append([]byte(nil), signed...)
	tampered[20] ^= 0xFF
	ctx := context.Background()
	for _, v := range validators {
		name, version, outcome, err := v.Validate(ctx, signed)
		if err != nil || outcome != "valid" {
			t.Fatalf("%s %s: signed outcome=%q err=%v, want valid", name, version, outcome, err)
		}
		if _, _, tamperOutcome, _ := v.Validate(ctx, tampered); tamperOutcome != "invalid" {
			t.Fatalf("%s: tampered outcome=%q, want invalid", name, tamperOutcome)
		}
		t.Logf("independent validator %s %s accepted signed, rejected tampered", name, version)
	}
}

func availableValidators(t *testing.T) []validation.Validator {
	t.Helper()
	var validators []validation.Validator
	if v := scriptValidator(t, "pdfsig", "pdfsig_validate.sh"); v != nil {
		validators = append(validators, *v)
	}
	if v := pyhankoValidator(t); v != nil {
		validators = append(validators, *v)
	}
	return validators
}

func scriptValidator(t *testing.T, tool, script string) *validation.ExternalValidator {
	t.Helper()
	if _, err := exec.LookPath(tool); err != nil {
		return nil
	}
	path, digest := scriptDigest(t, script)
	return &validation.ExternalValidator{Name: tool, Version: "live", Path: path, SHA256: digest, Timeout: 30 * time.Second, MaxOutputLen: 4096}
}

func pyhankoValidator(t *testing.T) *validation.ExternalValidator {
	t.Helper()
	python := os.Getenv("YARGI_PYHANKO_PYTHON")
	if python == "" {
		if abs, err := filepath.Abs(filepath.Join("..", "..", "..", "server", ".venv", "bin", "python")); err == nil {
			python = abs
		}
	}
	if python == "" {
		return nil
	}
	if _, err := os.Stat(python); err != nil {
		return nil
	}
	if exec.Command(python, "-c", "import pyhanko").Run() != nil {
		return nil
	}
	os.Setenv("YARGI_PYHANKO_PYTHON", python)
	path, digest := scriptDigest(t, "pyhanko_validate.sh")
	return &validation.ExternalValidator{Name: "pyhanko", Version: "live", Path: path, SHA256: digest, Timeout: 60 * time.Second, MaxOutputLen: 4096}
}

func scriptDigest(t *testing.T, script string) (string, string) {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "..", "testdata", "validators", script))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod script: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read script: %v", err)
	}
	sum := sha256.Sum256(data)
	return path, hex.EncodeToString(sum[:])
}

func randomHex() string {
	buf := make([]byte, 12)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
