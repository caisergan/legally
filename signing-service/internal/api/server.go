package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/caisergan/legally/signing-service/internal/config"
	"github.com/caisergan/legally/signing-service/internal/credentials"
	"github.com/caisergan/legally/signing-service/internal/errcodes"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/policy"
	"github.com/caisergan/legally/signing-service/internal/signer"
)

// CredentialInventory supplies safe public credential metadata.
type CredentialInventory interface {
	Inventory(ctx context.Context) ([]credentials.InventoryItem, error)
}

// Readiness is the safe, non-secret liveness view surfaced by /v1/health.
type Readiness struct {
	Ready    bool
	Database string
}

type Server struct {
	cfg                 config.Config
	httpServer          *http.Server
	readiness           func() Readiness
	credentialInventory CredentialInventory
	jobs                *jobs.Service
	coordinator         *signer.Coordinator
}

func NewServer(cfg config.Config) *Server {
	server := &Server{
		cfg:       cfg,
		readiness: func() Readiness { return Readiness{Ready: true, Database: "not_configured"} },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", server.handleHealth)
	mux.HandleFunc("GET /v1/credentials", server.handleCredentials)
	mux.HandleFunc("POST /v1/jobs", server.handleCreateJob)
	mux.HandleFunc("GET /v1/commands/{command_id}", server.handleCommandStatus)
	mux.HandleFunc("GET /v1/jobs/{job_id}", server.handleGetJob)
	mux.HandleFunc("GET /v1/jobs/{job_id}/events", server.handleJobEvents)
	mux.HandleFunc("GET /v1/jobs/{job_id}/pin-challenge", server.handlePINChallenge)
	mux.HandleFunc("POST /v1/jobs/{job_id}/authorize", server.handleAuthorize)
	mux.HandleFunc("POST /v1/jobs/{job_id}/cancel", server.handleCancelJob)
	server.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	return server
}

func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// SetReadiness installs a safe readiness probe used by /v1/health.
func (s *Server) SetReadiness(probe func() Readiness) {
	if probe != nil {
		s.readiness = probe
	}
}

// SetCredentialInventory wires the safe credential inventory source.
func (s *Server) SetCredentialInventory(inventory CredentialInventory) {
	s.credentialInventory = inventory
}

// SetJobService wires the durable job journal and its coordinator.
func (s *Server) SetJobService(service *jobs.Service, coordinator *signer.Coordinator) {
	s.jobs = service
	s.coordinator = coordinator
}

func (s *Server) ListenAndServe(ctx context.Context) error {
	if err := prepareSocketDirectory(s.cfg); err != nil {
		return err
	}
	if _, err := os.Lstat(s.cfg.SocketPath); err == nil {
		return fmt.Errorf("signer socket path already exists: %s", s.cfg.SocketPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect signer socket path: %w", err)
	}

	listener, err := net.Listen("unix", s.cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listen on signer Unix socket: %w", err)
	}
	defer listener.Close()
	defer os.Remove(s.cfg.SocketPath)

	if err := os.Chmod(s.cfg.SocketPath, 0o600); err != nil {
		return fmt.Errorf("restrict signer Unix socket: %w", err)
	}

	serveResult := make(chan error, 1)
	go func() {
		serveResult <- s.httpServer.Serve(listener)
	}()

	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		if err := s.httpServer.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down signer service: %w", err)
		}
		if err := <-serveResult; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func prepareSocketDirectory(cfg config.Config) error {
	directory := filepath.Dir(cfg.SocketPath)
	if cfg.Environment != "production" {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create signer socket directory: %w", err)
		}
		return nil
	}

	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("resolve production signer socket directory: %w", err)
	}
	if filepath.Clean(resolved) != filepath.Clean(directory) {
		return errors.New("production signer socket directory cannot contain symlinks")
	}
	info, err := os.Stat(directory)
	if err != nil {
		return fmt.Errorf("inspect production signer socket directory: %w", err)
	}
	if !info.IsDir() {
		return errors.New("production signer socket parent is not a directory")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("production signer socket directory permissions must be 0700, got %04o", info.Mode().Perm())
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("production signer socket directory must be owned by the signer process user")
	}
	return nil
}

func (s *Server) handleHealth(response http.ResponseWriter, _ *http.Request) {
	readiness := s.readiness()
	writeJSON(response, http.StatusOK, HealthResponse{
		Status:   "ok",
		Ready:    readiness.Ready,
		Database: readiness.Database,
		Mode:     string(s.cfg.Mode),
		Version:  s.cfg.Version,
		Policy:   policy.Current(),
	})
}

func (s *Server) handleCredentials(response http.ResponseWriter, request *http.Request) {
	if s.credentialInventory == nil {
		writeError(response, newSafeError(http.StatusServiceUnavailable, errcodes.ServiceUnavailable, "credential inventory unavailable"))
		return
	}
	items, err := s.credentialInventory.Inventory(request.Context())
	if err != nil {
		writeError(response, newSafeError(http.StatusInternalServerError, errcodes.ServiceUnavailable, "failed to read credential inventory"))
		return
	}
	out := CredentialsResponse{Items: make([]CredentialInventoryItem, 0, len(items))}
	for _, item := range items {
		safe := CredentialInventoryItem{
			ID:                           item.ID,
			CertificateFingerprintSHA256: item.CertificateFingerprintSHA256,
			SubjectDisplay:               item.SubjectDisplay,
			IssuerDisplay:                item.IssuerDisplay,
			SerialSuffix:                 item.SerialSuffix,
			NotBefore:                    item.NotBefore.UTC().Format(time.RFC3339),
			NotAfter:                     item.NotAfter.UTC().Format(time.RFC3339),
			PublicKeyType:                item.PublicKeyType,
			PublicKeyBits:                item.PublicKeyBits,
			Mode:                         string(s.cfg.Mode),
			Status:                       item.Status,
		}
		if item.LastCheckedAt != nil {
			safe.LastCheckedAt = item.LastCheckedAt.UTC().Format(time.RFC3339)
		}
		out.Items = append(out.Items, safe)
	}
	writeJSON(response, http.StatusOK, out)
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
