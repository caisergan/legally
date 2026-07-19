package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	ErrValidatorDigest  = errors.New("independent validator digest mismatch")
	ErrValidatorMissing = errors.New("independent validator executable not found")
)

// ExternalValidator runs a digest-pinned validator executable with a bounded
// timeout on a private temporary copy of the signed document.
type ExternalValidator struct {
	Name         string
	Version      string
	Path         string
	SHA256       string
	Timeout      time.Duration
	MaxOutputLen int
}

// Validate returns a normalized outcome: "valid", "invalid", or "indeterminate".
func (v ExternalValidator) Validate(ctx context.Context, signed []byte) (string, string, string, error) {
	if err := v.verifyExecutable(); err != nil {
		return v.Name, v.Version, "indeterminate", err
	}
	dir, err := os.MkdirTemp("", "yargi-validate-")
	if err != nil {
		return v.Name, v.Version, "indeterminate", err
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return v.Name, v.Version, "indeterminate", err
	}
	target := filepath.Join(dir, "document.pdf")
	if err := os.WriteFile(target, signed, 0o600); err != nil {
		return v.Name, v.Version, "indeterminate", err
	}

	runCtx, cancel := context.WithTimeout(ctx, v.Timeout)
	defer cancel()
	output, err := exec.CommandContext(runCtx, v.Path, target).CombinedOutput()
	if runCtx.Err() != nil {
		return v.Name, v.Version, "indeterminate", runCtx.Err()
	}
	if v.MaxOutputLen > 0 && len(output) > v.MaxOutputLen {
		output = output[:v.MaxOutputLen]
	}
	return v.Name, v.Version, normalizeOutcome(err, string(output)), nil
}

func (v ExternalValidator) verifyExecutable() error {
	info, err := os.Stat(v.Path)
	if err != nil || !info.Mode().IsRegular() {
		return ErrValidatorMissing
	}
	data, err := os.ReadFile(v.Path)
	if err != nil {
		return ErrValidatorMissing
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != strings.ToLower(v.SHA256) {
		return ErrValidatorDigest
	}
	return nil
}

// normalizeOutcome fails closed: any run error or negative marker yields
// "invalid", and a positive "valid" marker counts only when no negative marker
// is present (so "Signature is Invalid" is never mistaken for valid).
func normalizeOutcome(runErr error, output string) string {
	if runErr != nil {
		return "invalid"
	}
	lower := strings.ToLower(output)
	for _, negative := range []string{"invalid", "not valid", "verification failed", "failed", "no signature"} {
		if strings.Contains(lower, negative) {
			return "invalid"
		}
	}
	if strings.Contains(lower, "indeterminate") {
		return "indeterminate"
	}
	if strings.Contains(lower, "valid") {
		return "valid"
	}
	return "indeterminate"
}
