package integration

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/api"
	"github.com/caisergan/legally/signing-service/internal/config"
)

func startServer(t *testing.T, configure func(*api.Server)) (string, func()) {
	t.Helper()
	// A short base dir keeps the socket path under the ~104-char Unix limit.
	dir, err := os.MkdirTemp("", "yc")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "s.sock")
	server := api.NewServer(config.Config{
		Environment:     "development",
		Transport:       config.TransportUnix,
		SocketPath:      socketPath,
		ShutdownTimeout: time.Second,
		Version:         "peercred-test",
	})
	configure(server)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = server.ListenAndServe(ctx); close(done) }()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(socketPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("signer socket did not appear")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return socketPath, func() { cancel(); <-done }
}

func healthOverSocket(t *testing.T, socketPath string) int {
	t.Helper()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
	}}
	response, err := client.Get("http://signerd/v1/health")
	if err != nil {
		t.Fatalf("request over socket: %v", err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

func TestPeerCredentialAllowlistAdmitsAndDenies(t *testing.T) {
	allowed, stopAllowed := startServer(t, func(s *api.Server) {
		s.SetPeerAllowlist([]int{os.Getuid()})
	})
	defer stopAllowed()
	if code := healthOverSocket(t, allowed); code != http.StatusOK {
		t.Fatalf("allowed peer got %d, want 200", code)
	}

	denied, stopDenied := startServer(t, func(s *api.Server) {
		s.SetPeerAllowlist([]int{os.Getuid() + 99999})
	})
	defer stopDenied()
	if code := healthOverSocket(t, denied); code != http.StatusForbidden {
		t.Fatalf("disallowed peer got %d, want 403", code)
	}
}
