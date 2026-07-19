package planauth

import (
	"errors"
	"testing"
	"time"
)

func fixture() (SigningPlan, JobManifest) {
	plan := SigningPlan{
		SchemaVersion: SchemaVersion, JobID: "job-1", CommandID: "cmd-1", JobVersion: 3,
		InputArtifactID: "in-1", InputSHA256: "abc", InputByteCount: 100,
		PreparedContentSHA256: "pc", SignedAttributesSHA256: "sa",
		CertificateFingerprintSHA256: "ff", Profile: "PAdES_BASELINE_B_B", Algorithm: "RSA_PKCS1_SHA256",
		Mechanism: "CKM_RSA_PKCS", PolicyVersion: "pades-b-b-rsa-sha256-v1",
		FormatEngineVersion: "format-v1", Nonce: "nonce-1", ExpiresAt: time.Now().Add(time.Minute),
	}
	manifest := JobManifest{
		JobID: "job-1", CommandID: "cmd-1", Version: 3, InputArtifactID: "in-1", InputSHA256: "abc", InputByteCount: 100,
		CertificateFingerprintSHA256: "ff", Profile: "PAdES_BASELINE_B_B", Algorithm: "RSA_PKCS1_SHA256",
		Mechanism: "CKM_RSA_PKCS", PolicyVersion: "pades-b-b-rsa-sha256-v1",
	}
	return plan, manifest
}

func newAuthorizer() *Authorizer {
	return NewAuthorizer([]byte("supervisor-secret"), []string{"format-v1"}, time.Minute)
}

func TestAuthorizeAndVerify(t *testing.T) {
	plan, manifest := fixture()
	authorizer := newAuthorizer()
	authorization, err := authorizer.Authorize(plan, manifest)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	if err := authorizer.Verify(authorization, plan); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyDetectsForgery(t *testing.T) {
	plan, manifest := fixture()
	authorizer := newAuthorizer()
	authorization, _ := authorizer.Authorize(plan, manifest)
	authorization.MAC = "deadbeef"
	if err := authorizer.Verify(authorization, plan); !errors.Is(err, ErrAuthorizationForged) {
		t.Fatalf("error = %v, want ErrAuthorizationForged", err)
	}
	// A different supervisor key cannot verify.
	other := NewAuthorizer([]byte("other-secret"), []string{"format-v1"}, time.Minute)
	authorization2, _ := authorizer.Authorize(plan, manifest)
	if err := other.Verify(authorization2, plan); !errors.Is(err, ErrAuthorizationForged) {
		t.Fatalf("cross-key verify = %v, want ErrAuthorizationForged", err)
	}
}

func TestVerifyIsOneUse(t *testing.T) {
	plan, manifest := fixture()
	authorizer := newAuthorizer()
	authorization, _ := authorizer.Authorize(plan, manifest)
	if err := authorizer.Verify(authorization, plan); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if err := authorizer.Verify(authorization, plan); !errors.Is(err, ErrAuthorizationReplayed) {
		t.Fatalf("replay = %v, want ErrAuthorizationReplayed", err)
	}
}

func TestVerifyRejectsWrongPlanHash(t *testing.T) {
	plan, manifest := fixture()
	authorizer := newAuthorizer()
	authorization, _ := authorizer.Authorize(plan, manifest)
	tampered := plan
	tampered.SignedAttributesSHA256 = "changed"
	if err := authorizer.Verify(authorization, tampered); !errors.Is(err, ErrPlanHashMismatch) {
		t.Fatalf("error = %v, want ErrPlanHashMismatch", err)
	}
}

func TestAuthorizeRejectsManifestMismatch(t *testing.T) {
	authorizer := newAuthorizer()
	plan, manifest := fixture()

	wrongInput := plan
	wrongInput.InputSHA256 = "tampered"
	if _, err := authorizer.Authorize(wrongInput, manifest); !errors.Is(err, ErrPlanMismatch) {
		t.Fatalf("wrong input = %v, want ErrPlanMismatch", err)
	}
	wrongJob := plan
	wrongJob.JobVersion = 99
	if _, err := authorizer.Authorize(wrongJob, manifest); !errors.Is(err, ErrPlanMismatch) {
		t.Fatalf("wrong job version = %v, want ErrPlanMismatch", err)
	}
	wrongEngine := plan
	wrongEngine.FormatEngineVersion = "rogue-engine"
	if _, err := authorizer.Authorize(wrongEngine, manifest); !errors.Is(err, ErrEngineNotAllowed) {
		t.Fatalf("rogue engine = %v, want ErrEngineNotAllowed", err)
	}
}

func TestVerifyRejectsExpiredAuthorization(t *testing.T) {
	plan, manifest := fixture()
	authorizer := newAuthorizer()
	authorization, _ := authorizer.Authorize(plan, manifest)
	authorizer.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if err := authorizer.Verify(authorization, plan); !errors.Is(err, ErrAuthorizationExpired) {
		t.Fatalf("error = %v, want ErrAuthorizationExpired", err)
	}
}
