package validation_test

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/corpus"
	"github.com/caisergan/legally/signing-service/internal/pades"
	"github.com/caisergan/legally/signing-service/internal/token/tokentest"
	"github.com/caisergan/legally/signing-service/internal/validation"
)

// TestTwoIndependentValidatorMatrix signs an accepted corpus fixture with the
// eimza-go engine and validates it through the ExternalValidator seam with two
// independent full PAdES validators (Poppler pdfsig and pyHanko). Both must
// accept the cryptographic signature, and both must reject a tampered copy. The
// test skips gracefully when a validator is unavailable (e.g. CI without the
// tools), so it is local/CI-opt-in evidence, never a false pass.
func TestTwoIndependentValidatorMatrix(t *testing.T) {
	signed := signCorpusFixture(t)
	tampered := append([]byte{}, signed...)
	tampered[20] ^= 0xFF // break the covered ByteRange

	pdfsig := externalValidatorFromScript(t, "pdfsig", "pdfsig_validate.sh")
	pyhanko := externalPyhanko(t)

	validators := []validation.Validator{}
	if pdfsig != nil {
		validators = append(validators, *pdfsig)
	}
	if pyhanko != nil {
		validators = append(validators, *pyhanko)
	}
	if len(validators) == 0 {
		t.Skip("no independent PAdES validators available (need pdfsig and/or pyHanko)")
	}

	ctx := context.Background()
	for _, v := range validators {
		name, version, outcome, err := v.Validate(ctx, signed)
		if err != nil || outcome != "valid" {
			t.Fatalf("%s %s: signed outcome=%q err=%v, want valid", name, version, outcome, err)
		}
		t.Logf("validator %s %s accepted the signature", name, version)
		if _, _, tamperOutcome, _ := v.Validate(ctx, tampered); tamperOutcome != "invalid" {
			t.Fatalf("%s: tampered outcome=%q, want invalid", name, tamperOutcome)
		}
	}

	if len(validators) < 2 {
		t.Skip("only one independent validator available; two required for the full matrix")
	}
	multi := validation.MultiValidator{Validators: validators}
	if _, _, outcome, _ := multi.Validate(ctx, signed); outcome != "valid" {
		t.Fatalf("two-validator aggregate on signed = %q, want valid", outcome)
	}
	if _, _, outcome, _ := multi.Validate(ctx, tampered); outcome != "invalid" {
		t.Fatalf("two-validator aggregate on tampered = %q, want invalid", outcome)
	}
}

// TestTrustOutcomeFailsClosed proves that the self-signed controlled corpus
// yields a cryptographically valid signature but an explicit indeterminate trust
// and revocation outcome, so release is refused and the output stays quarantined.
func TestTrustOutcomeFailsClosed(t *testing.T) {
	python, ok := pyhankoPython()
	if !ok {
		t.Skip("pyhanko not available")
	}
	signed := signCorpusFixture(t)
	dir := t.TempDir()
	pdfPath := filepath.Join(dir, "signed.pdf")
	if err := os.WriteFile(pdfPath, signed, 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	wrapper, err := filepath.Abs(filepath.Join("..", "..", "tools", "pades_validate_pyhanko.py"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	out, err := exec.Command(python, wrapper, pdfPath).Output()
	if err != nil && len(out) == 0 {
		t.Fatalf("run wrapper: %v", err)
	}
	var report struct {
		Crypto     string `json:"crypto"`
		Trust      string `json:"trust"`
		Revocation string `json:"revocation"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("parse report %q: %v", out, err)
	}
	outcome := validation.TrustOutcome{Crypto: report.Crypto, Trust: report.Trust, Revocation: report.Revocation}
	if outcome.Crypto != "valid" {
		t.Fatalf("crypto = %q, want valid", outcome.Crypto)
	}
	if outcome.Trust != "indeterminate" {
		t.Fatalf("trust = %q, want indeterminate for a self-signed corpus", outcome.Trust)
	}
	if outcome.ReleaseAllowed() {
		t.Fatal("release must be refused (fail closed) for an untrusted certificate")
	}
	t.Logf("trust outcome %+v: release refused, output stays quarantined", outcome)
}

func signCorpusFixture(t *testing.T) []byte {
	t.Helper()
	now := time.Now()
	id, err := tokentest.NewIdentity("Ege Ayyildiz", "ckaid-matrix", now.Add(-time.Hour), now.Add(365*24*time.Hour))
	if err != nil {
		t.Fatalf("identity: %v", err)
	}
	cert, err := x509.ParseCertificate(id.CertDER)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	signed, err := pades.Sign(corpus.Accepted()["accepted_minimal"], cert, id.Priv)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return signed
}

func externalValidatorFromScript(t *testing.T, name, script string) *validation.ExternalValidator {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Logf("skipping %s: not on PATH", name)
		return nil
	}
	path, digest := scriptDigest(t, script)
	return &validation.ExternalValidator{
		Name: name, Version: toolVersion(name), Path: path, SHA256: digest,
		Timeout: 30 * time.Second, MaxOutputLen: 4096,
	}
}

func externalPyhanko(t *testing.T) *validation.ExternalValidator {
	t.Helper()
	python, ok := pyhankoPython()
	if !ok {
		t.Log("skipping pyhanko: no python with pyhanko found (set YARGI_PYHANKO_PYTHON)")
		return nil
	}
	os.Setenv("YARGI_PYHANKO_PYTHON", python)
	path, digest := scriptDigest(t, "pyhanko_validate.sh")
	version := "unknown"
	if out, err := exec.Command(python, "-c", "import importlib.metadata as m;print(m.version('pyhanko'))").Output(); err == nil {
		version = string(trimSpace(out))
	}
	return &validation.ExternalValidator{
		Name: "pyhanko", Version: version, Path: path, SHA256: digest,
		Timeout: 60 * time.Second, MaxOutputLen: 4096,
	}
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

func pyhankoPython() (string, bool) {
	candidates := []string{}
	if env := os.Getenv("YARGI_PYHANKO_PYTHON"); env != "" {
		candidates = append(candidates, env)
	}
	if abs, err := filepath.Abs(filepath.Join("..", "..", "..", "server", ".venv", "bin", "python")); err == nil {
		candidates = append(candidates, abs)
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err != nil {
			continue
		}
		if exec.Command(candidate, "-c", "import pyhanko").Run() == nil {
			return candidate, true
		}
	}
	return "", false
}

func toolVersion(name string) string {
	if out, err := exec.Command(name, "-v").CombinedOutput(); err == nil && len(out) > 0 {
		line := out
		if idx := indexByte(out, '\n'); idx >= 0 {
			line = out[:idx]
		}
		return string(trimSpace(line))
	}
	return "unknown"
}

func indexByte(b []byte, c byte) int {
	for i := range b {
		if b[i] == c {
			return i
		}
	}
	return -1
}

func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\n' || b[start] == '\r' || b[start] == '\t') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\n' || b[end-1] == '\r' || b[end-1] == '\t') {
		end--
	}
	return b[start:end]
}
