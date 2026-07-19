package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeAllowlist(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "modules.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write allowlist: %v", err)
	}
	return path
}

const validAllowlist = `pkcs11_modules:
  - alias: softhsm-test
    path: /approved/path/libsofthsm2.so
    sha256: 0000000000000000000000000000000000000000000000000000000000000000
    modes: [test]
  - alias: approved-token-driver
    path: /approved/path/vendor-pkcs11.so
    sha256: 1111111111111111111111111111111111111111111111111111111111111111
    modes: [hardware]
`

func TestLoadModuleAllowlistValid(t *testing.T) {
	allowlist, err := LoadModuleAllowlist(writeAllowlist(t, validAllowlist))
	if err != nil {
		t.Fatalf("valid allowlist rejected: %v", err)
	}
	entry, ok := allowlist.Lookup("softhsm-test")
	if !ok {
		t.Fatal("softhsm-test not found")
	}
	if !entry.AllowsMode(ModuleModeTest) || entry.AllowsMode(ModuleModeHardware) {
		t.Fatalf("unexpected mode set for %q: %v", entry.Alias, entry.Modes)
	}
}

func TestLoadModuleAllowlistRejectsBadEntries(t *testing.T) {
	cases := map[string]string{
		"duplicate alias": `pkcs11_modules:
  - {alias: a, path: /a.so, sha256: 0000000000000000000000000000000000000000000000000000000000000000, modes: [test]}
  - {alias: a, path: /b.so, sha256: 1111111111111111111111111111111111111111111111111111111111111111, modes: [test]}`,
		"relative path": `pkcs11_modules:
  - {alias: a, path: rel.so, sha256: 0000000000000000000000000000000000000000000000000000000000000000, modes: [test]}`,
		"short sha256": `pkcs11_modules:
  - {alias: a, path: /a.so, sha256: abc, modes: [test]}`,
		"unknown mode": `pkcs11_modules:
  - {alias: a, path: /a.so, sha256: 0000000000000000000000000000000000000000000000000000000000000000, modes: [prod]}`,
		"empty modes": `pkcs11_modules:
  - {alias: a, path: /a.so, sha256: 0000000000000000000000000000000000000000000000000000000000000000, modes: []}`,
		"unknown field": `pkcs11_modules:
  - {alias: a, path: /a.so, sha256: 0000000000000000000000000000000000000000000000000000000000000000, modes: [test], extra: 1}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadModuleAllowlist(writeAllowlist(t, body)); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}

func TestLoadModuleAllowlistRequiresAbsolutePath(t *testing.T) {
	if _, err := LoadModuleAllowlist("relative/modules.yaml"); err == nil {
		t.Fatal("relative allowlist path accepted")
	}
}
