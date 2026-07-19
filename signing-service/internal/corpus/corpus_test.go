package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const manifestPath = "../../testdata/pdf/manifest.json"

type manifestFile struct {
	Version string            `json:"version"`
	Digests map[string]string `json:"digests"`
}

// TestCorpusManifest pins the corpus. With CORPUS_REGEN=1 it (re)writes the
// on-disk fixtures and manifest; otherwise it asserts the generated corpus still
// matches the committed manifest, forcing a Version bump when a builder changes.
func TestCorpusManifest(t *testing.T) {
	digests := Manifest()

	if os.Getenv("CORPUS_REGEN") == "1" {
		writeCorpus(t, digests)
		return
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatalf("read manifest (run with CORPUS_REGEN=1 to generate): %v", err)
	}
	var committed manifestFile
	if err := json.Unmarshal(raw, &committed); err != nil {
		t.Fatalf("parse manifest: %v", err)
	}
	if committed.Version != Version {
		t.Fatalf("manifest version %q != corpus Version %q", committed.Version, Version)
	}
	for name, digest := range digests {
		if committed.Digests[name] != digest {
			t.Errorf("%s: digest %s != committed %s", name, digest, committed.Digests[name])
		}
	}
	if len(committed.Digests) != len(digests) {
		t.Errorf("manifest has %d fixtures, corpus has %d", len(committed.Digests), len(digests))
	}
}

func TestCorpusClassesDistinct(t *testing.T) {
	if len(Accepted()) < 2 {
		t.Error("expected at least two accepted fixtures")
	}
	if len(Rejected()) < 8 {
		t.Errorf("expected the full rejected-class matrix, got %d", len(Rejected()))
	}
}

func writeCorpus(t *testing.T, digests map[string]string) {
	t.Helper()
	dir := filepath.Dir(manifestPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	write := func(fixtures map[string][]byte) {
		for name, fixture := range fixtures {
			if len(fixture) == 0 {
				continue // e.g. rejected_empty
			}
			if err := os.WriteFile(filepath.Join(dir, name+".pdf"), fixture, 0o644); err != nil {
				t.Fatalf("write %s: %v", name, err)
			}
		}
	}
	write(Accepted())
	write(Rejected())
	raw, err := json.MarshalIndent(manifestFile{Version: Version, Digests: digests}, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, append(raw, '\n'), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}
