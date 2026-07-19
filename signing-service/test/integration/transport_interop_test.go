// Package integration proves the FastAPI↔signerd transport interoperates: the
// real FastAPI CommandSigner signs an artifact-ingest PUT that the Go
// command-auth verifier accepts and the broker stores, over a real Unix socket.
// It is opt-in: it skips unless the FastAPI app venv is available, and it needs
// no SoftHSM (it exercises the transport, not signing).
package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/api"
	"github.com/caisergan/legally/signing-service/internal/artifacts"
	"github.com/caisergan/legally/signing-service/internal/commandauth"
	"github.com/caisergan/legally/signing-service/internal/config"
)

func TestFastAPITransportInterop(t *testing.T) {
	serverDir, err := filepath.Abs(filepath.Join("..", "..", "..", "server"))
	if err != nil {
		t.Fatalf("abs server dir: %v", err)
	}
	python := filepath.Join(serverDir, ".venv", "bin", "python")
	probe := filepath.Join(serverDir, "scripts", "signer_transport_probe.py")
	if _, err := os.Stat(python); err != nil {
		t.Skip("FastAPI app venv not available")
	}
	if _, err := os.Stat(probe); err != nil {
		t.Skip("transport probe not found")
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal key: %v", err)
	}
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "command.pem")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	socketPath := filepath.Join(dir, "signerd.sock")

	kid := "fastapi-cmd-1"
	broker := artifacts.NewBroker(time.Minute)
	server := api.NewServer(config.Config{
		Environment:     "development",
		Transport:       config.TransportUnix,
		SocketPath:      socketPath,
		ShutdownTimeout: time.Second,
		Version:         "interop",
	})
	server.SetArtifactBroker(broker, 1<<20)
	server.SetCommandVerifier(commandauth.NewVerifier(map[string]ed25519.PublicKey{kid: pub}, 30*time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe(ctx) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("signer socket did not appear")
		}
		time.Sleep(20 * time.Millisecond)
	}

	out, err := exec.Command(python, probe, socketPath, keyPath, kid).Output()
	if err != nil {
		t.Fatalf("probe failed: %v (out=%s)", err, out)
	}
	var report struct {
		OK        bool   `json:"ok"`
		ByteCount int    `json:"byte_count"`
		SHA256    string `json:"sha256"`
		Error     string `json:"error"`
		Detail    string `json:"detail"`
	}
	if err := json.Unmarshal(out, &report); err != nil {
		t.Fatalf("parse probe output %q: %v", out, err)
	}
	if !report.OK {
		t.Fatalf("probe rejected: %s: %s", report.Error, report.Detail)
	}

	stored, ok := broker.Output("art-interop")
	if !ok {
		t.Fatal("broker did not receive the artifact")
	}
	sum := sha256.Sum256(stored)
	if hex.EncodeToString(sum[:]) != report.SHA256 || len(stored) != report.ByteCount {
		t.Fatalf("stored artifact mismatch: got %d bytes sha %s", len(stored), hex.EncodeToString(sum[:]))
	}
	t.Logf("FastAPI CommandSigner ↔ Go verifier interop OK: %d bytes ingested over the socket", len(stored))

	cancel()
	select {
	case <-serveErr:
	case <-time.After(2 * time.Second):
	}
}
