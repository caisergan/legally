package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeExecutable(t *testing.T, body string) (string, string) {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve dir: %v", err)
	}
	path := filepath.Join(dir, "validator.sh")
	content := "#!/bin/sh\n" + body + "\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}
	sum := sha256.Sum256([]byte(content))
	return path, hex.EncodeToString(sum[:])
}

func TestExternalValidatorFailsClosedOnDigestMismatch(t *testing.T) {
	path, _ := writeExecutable(t, "echo valid")
	validator := ExternalValidator{Name: "test", Version: "1", Path: path, SHA256: hex.EncodeToString(make([]byte, 32)), Timeout: time.Second}
	_, _, outcome, err := validator.Validate(context.Background(), []byte("pdf"))
	if !errors.Is(err, ErrValidatorDigest) || outcome != "indeterminate" {
		t.Fatalf("outcome=%q err=%v, want indeterminate/ErrValidatorDigest", outcome, err)
	}
}

func TestExternalValidatorMissingExecutable(t *testing.T) {
	validator := ExternalValidator{Name: "test", Version: "1", Path: "/nonexistent/validator", SHA256: "00", Timeout: time.Second}
	if _, _, _, err := validator.Validate(context.Background(), []byte("pdf")); !errors.Is(err, ErrValidatorMissing) {
		t.Fatalf("err=%v, want ErrValidatorMissing", err)
	}
}

func TestExternalValidatorNormalizesOutcome(t *testing.T) {
	path, digest := writeExecutable(t, "echo Signature is Valid")
	validator := ExternalValidator{Name: "poppler", Version: "1", Path: path, SHA256: digest, Timeout: 5 * time.Second, MaxOutputLen: 4096}
	_, _, outcome, err := validator.Validate(context.Background(), []byte("pdf"))
	if err != nil || outcome != "valid" {
		t.Fatalf("outcome=%q err=%v, want valid", outcome, err)
	}
}
