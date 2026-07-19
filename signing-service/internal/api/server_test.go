package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/config"
	"github.com/caisergan/legally/signing-service/internal/policy"
)

func TestHealthReturnsOnlySafeServiceMetadata(t *testing.T) {
	server := NewServer(config.Config{
		Enabled:         true,
		Environment:     "test",
		Mode:            config.ModeSoftHSM,
		Transport:       config.TransportUnix,
		SocketPath:      filepath.Join(t.TempDir(), "signerd.sock"),
		ShutdownTimeout: time.Second,
		Version:         "test-version",
	})

	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", response.Code)
	}
	if cacheControl := response.Header().Get("Cache-Control"); cacheControl != "no-store" {
		t.Fatalf("unexpected cache control: %q", cacheControl)
	}

	var body HealthResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Status != "ok" || body.Mode != "softhsm" || body.Version != "test-version" {
		t.Fatalf("unexpected health response: %+v", body)
	}
	if body.Policy != policy.Current() {
		t.Fatalf("unexpected policy: %+v", body.Policy)
	}
}

func TestProductionSocketDirectoryMustBePrivateAndOwned(t *testing.T) {
	directory := t.TempDir()
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatalf("resolve temp directory: %v", err)
	}
	if err := os.Chmod(resolvedDirectory, 0o700); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	cfg := config.Config{Environment: "production", SocketPath: filepath.Join(resolvedDirectory, "signerd.sock")}
	if err := prepareSocketDirectory(cfg); err != nil {
		t.Fatalf("private production directory rejected: %v", err)
	}

	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := prepareSocketDirectory(cfg); err == nil {
		t.Fatal("world-accessible production directory was accepted")
	}
}

func TestProductionSocketDirectoryRejectsSymlink(t *testing.T) {
	base := t.TempDir()
	realDirectory := filepath.Join(base, "real")
	if err := os.Mkdir(realDirectory, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	linkDirectory := filepath.Join(base, "link")
	if err := os.Symlink(realDirectory, linkDirectory); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	cfg := config.Config{Environment: "production", SocketPath: filepath.Join(linkDirectory, "signerd.sock")}
	if err := prepareSocketDirectory(cfg); err == nil {
		t.Fatal("symlinked production directory was accepted")
	}
}

func TestHealthRejectsOtherMethods(t *testing.T) {
	server := NewServer(config.Config{})
	request := httptest.NewRequest(http.MethodPost, "/v1/health", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("unexpected status: %d", response.Code)
	}
}
