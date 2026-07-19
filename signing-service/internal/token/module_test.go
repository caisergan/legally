package token_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/caisergan/legally/signing-service/internal/config"
	"github.com/caisergan/legally/signing-service/internal/token"
)

func writeModule(t *testing.T, content []byte, perm os.FileMode) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	path := filepath.Join(dir, "module.so")
	if err := os.WriteFile(path, content, perm); err != nil {
		t.Fatalf("write module: %v", err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatalf("chmod module: %v", err)
	}
	return path
}

func digestOf(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func TestVerifyModuleAcceptsMatching(t *testing.T) {
	content := []byte("fake pkcs11 module")
	path := writeModule(t, content, 0o600)
	entry := config.ModuleEntry{Alias: "softhsm-test", Path: path, SHA256: digestOf(content), Modes: []config.ModuleMode{config.ModuleModeTest}}
	if err := token.VerifyModule(entry, config.ModuleModeTest); err != nil {
		t.Fatalf("valid module rejected: %v", err)
	}
}

func TestVerifyModuleRejectsWrongDigest(t *testing.T) {
	path := writeModule(t, []byte("actual bytes"), 0o600)
	entry := config.ModuleEntry{Alias: "a", Path: path, SHA256: digestOf([]byte("different bytes")), Modes: []config.ModuleMode{config.ModuleModeTest}}
	if err := token.VerifyModule(entry, config.ModuleModeTest); !errors.Is(err, token.ErrModuleDigestMismatch) {
		t.Fatalf("error = %v, want ErrModuleDigestMismatch", err)
	}
}

func TestVerifyModuleRejectsSymlink(t *testing.T) {
	content := []byte("module")
	real := writeModule(t, content, 0o600)
	link := filepath.Join(filepath.Dir(real), "link.so")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	entry := config.ModuleEntry{Alias: "a", Path: link, SHA256: digestOf(content), Modes: []config.ModuleMode{config.ModuleModeTest}}
	if err := token.VerifyModule(entry, config.ModuleModeTest); !errors.Is(err, token.ErrModulePathUnsafe) {
		t.Fatalf("error = %v, want ErrModulePathUnsafe", err)
	}
}

func TestVerifyModuleRejectsWritablePermissions(t *testing.T) {
	content := []byte("module")
	path := writeModule(t, content, 0o666)
	entry := config.ModuleEntry{Alias: "a", Path: path, SHA256: digestOf(content), Modes: []config.ModuleMode{config.ModuleModeTest}}
	if err := token.VerifyModule(entry, config.ModuleModeTest); !errors.Is(err, token.ErrModuleOwnership) {
		t.Fatalf("error = %v, want ErrModuleOwnership", err)
	}
}

func TestVerifyModuleRejectsDisallowedMode(t *testing.T) {
	content := []byte("module")
	path := writeModule(t, content, 0o600)
	entry := config.ModuleEntry{Alias: "a", Path: path, SHA256: digestOf(content), Modes: []config.ModuleMode{config.ModuleModeTest}}
	if err := token.VerifyModule(entry, config.ModuleModeHardware); !errors.Is(err, token.ErrModuleModeNotAllowed) {
		t.Fatalf("error = %v, want ErrModuleModeNotAllowed", err)
	}
}

func TestVerifyModuleRejectsRelativePath(t *testing.T) {
	entry := config.ModuleEntry{Alias: "a", Path: "relative/module.so", SHA256: digestOf([]byte("x")), Modes: []config.ModuleMode{config.ModuleModeTest}}
	if err := token.VerifyModule(entry, config.ModuleModeTest); !errors.Is(err, token.ErrModulePathUnsafe) {
		t.Fatalf("error = %v, want ErrModulePathUnsafe", err)
	}
}
