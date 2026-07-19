package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	yaml "gopkg.in/yaml.v3"
)

// ModuleMode is an allowlist entry's permitted operating mode.
type ModuleMode string

const (
	ModuleModeTest     ModuleMode = "test"
	ModuleModeHardware ModuleMode = "hardware"
)

// ModuleEntry is one allowlisted PKCS#11 module. Callers reference modules by
// alias only; API-supplied module paths are never honored (plan §5.1).
type ModuleEntry struct {
	Alias  string       `yaml:"alias"`
	Path   string       `yaml:"path"`
	SHA256 string       `yaml:"sha256"`
	Modes  []ModuleMode `yaml:"modes"`
}

// ModuleAllowlist is the signer-owned PKCS#11 module allowlist.
type ModuleAllowlist struct {
	Modules []ModuleEntry `yaml:"pkcs11_modules"`
}

var sha256Pattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// LoadModuleAllowlist reads and structurally validates the allowlist file.
func LoadModuleAllowlist(path string) (ModuleAllowlist, error) {
	if !filepath.IsAbs(path) {
		return ModuleAllowlist{}, fmt.Errorf("module allowlist path must be absolute: %s", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ModuleAllowlist{}, fmt.Errorf("read module allowlist: %w", err)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var allowlist ModuleAllowlist
	if err := decoder.Decode(&allowlist); err != nil {
		return ModuleAllowlist{}, fmt.Errorf("parse module allowlist: %w", err)
	}
	if err := allowlist.Validate(); err != nil {
		return ModuleAllowlist{}, err
	}
	return allowlist, nil
}

// Validate enforces the structural rules for the allowlist. Runtime file
// ownership, mode, and digest verification happen at probe time (Phase 2B).
func (a ModuleAllowlist) Validate() error {
	seenAlias := map[string]struct{}{}
	seenPath := map[string]struct{}{}
	for index, entry := range a.Modules {
		alias := strings.TrimSpace(entry.Alias)
		if alias == "" {
			return fmt.Errorf("module allowlist entry %d has an empty alias", index)
		}
		if _, ok := seenAlias[alias]; ok {
			return fmt.Errorf("duplicate module alias %q", alias)
		}
		seenAlias[alias] = struct{}{}

		if !filepath.IsAbs(entry.Path) {
			return fmt.Errorf("module %q path must be absolute", alias)
		}
		if entry.Path != filepath.Clean(entry.Path) {
			return fmt.Errorf("module %q path must be canonical", alias)
		}
		if _, ok := seenPath[entry.Path]; ok {
			return fmt.Errorf("duplicate module path %q", entry.Path)
		}
		seenPath[entry.Path] = struct{}{}

		if !sha256Pattern.MatchString(strings.ToLower(entry.SHA256)) {
			return fmt.Errorf("module %q sha256 must be 64 lowercase hex characters", alias)
		}
		if len(entry.Modes) == 0 {
			return fmt.Errorf("module %q must declare at least one mode", alias)
		}
		for _, mode := range entry.Modes {
			switch mode {
			case ModuleModeTest, ModuleModeHardware:
			default:
				return fmt.Errorf("module %q has unsupported mode %q", alias, mode)
			}
		}
	}
	return nil
}

// Lookup returns the entry for alias, if present.
func (a ModuleAllowlist) Lookup(alias string) (ModuleEntry, bool) {
	for _, entry := range a.Modules {
		if entry.Alias == alias {
			return entry, true
		}
	}
	return ModuleEntry{}, false
}

// AllowsMode reports whether the entry permits the given mode.
func (e ModuleEntry) AllowsMode(mode ModuleMode) bool {
	for _, candidate := range e.Modes {
		if candidate == mode {
			return true
		}
	}
	return false
}
