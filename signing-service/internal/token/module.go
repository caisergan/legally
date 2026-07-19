package token

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/caisergan/legally/signing-service/internal/config"
)

// VerifyModule checks an allowlisted module before it is loaded: canonical
// symlink-free path, expected ownership and permissions, exact SHA-256, and
// mode allowlist (plan §5.1). No API-supplied module path is ever accepted.
func VerifyModule(entry config.ModuleEntry, requiredMode config.ModuleMode) error {
	if !filepath.IsAbs(entry.Path) || entry.Path != filepath.Clean(entry.Path) {
		return ErrModulePathUnsafe
	}
	resolved, err := filepath.EvalSymlinks(entry.Path)
	if err != nil || resolved != entry.Path {
		return ErrModulePathUnsafe
	}
	info, err := os.Stat(entry.Path)
	if err != nil || !info.Mode().IsRegular() {
		return ErrModulePathUnsafe
	}
	if info.Mode().Perm()&0o022 != 0 {
		return ErrModuleOwnership
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		if int(stat.Uid) != os.Geteuid() && stat.Uid != 0 {
			return ErrModuleOwnership
		}
	}

	data, err := os.ReadFile(entry.Path)
	if err != nil {
		return ErrModulePathUnsafe
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != strings.ToLower(entry.SHA256) {
		return ErrModuleDigestMismatch
	}
	if !entry.AllowsMode(requiredMode) {
		return ErrModuleModeNotAllowed
	}
	return nil
}
