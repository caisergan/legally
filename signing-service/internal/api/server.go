package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/caisergan/legally/signing-service/internal/artifacts"
	"github.com/caisergan/legally/signing-service/internal/commandauth"
	"github.com/caisergan/legally/signing-service/internal/config"
	"github.com/caisergan/legally/signing-service/internal/credentials"
	"github.com/caisergan/legally/signing-service/internal/errcodes"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/peercred"
	"github.com/caisergan/legally/signing-service/internal/policy"
	"github.com/caisergan/legally/signing-service/internal/signer"
)

type peerCredKey struct{}

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
	broker              *artifacts.Broker
	maxArtifactBytes    int64
	verifier            *commandauth.Verifier
	allowedUIDs         map[int]bool
}

func NewServer(cfg config.Config) *Server {
	server := &Server{
		cfg:       cfg,
		readiness: func() Readiness { return Readiness{Ready: true, Database: "not_configured"} },
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", server.handleHealth)
	mux.HandleFunc("GET /v1/credentials", server.handleCredentials)
	mux.HandleFunc("PUT /v1/artifacts/{artifact_id}", server.handleArtifactPut)
	mux.HandleFunc("POST /v1/jobs", server.handleCreateJob)
	mux.HandleFunc("GET /v1/commands/{command_id}", server.handleCommandStatus)
	mux.HandleFunc("GET /v1/jobs/{job_id}", server.handleGetJob)
	mux.HandleFunc("GET /v1/jobs/{job_id}/events", server.handleJobEvents)
	mux.HandleFunc("GET /v1/jobs/{job_id}/output", server.handleJobOutput)
	mux.HandleFunc("GET /v1/jobs/{job_id}/pin-challenge", server.handlePINChallenge)
	mux.HandleFunc("POST /v1/jobs/{job_id}/authorize", server.handleAuthorize)
	mux.HandleFunc("POST /v1/jobs/{job_id}/cancel", server.handleCancelJob)
	server.httpServer = &http.Server{
		Handler:           server.authWrap(mux),
		ConnContext:       server.connContext,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	return server
}

// connContext captures the peer's OS credentials once per connection when a UID
// allowlist is installed, so authWrap can authorize every request on it.
func (s *Server) connContext(ctx context.Context, conn net.Conn) context.Context {
	if len(s.allowedUIDs) == 0 {
		return ctx
	}
	creds, err := peercred.FromConn(conn)
	if err != nil {
		return context.WithValue(ctx, peerCredKey{}, peercred.PeerCredentials{PID: -1, UID: -1, GID: -1})
	}
	return context.WithValue(ctx, peerCredKey{}, creds)
}

// authWrap verifies asymmetric command authentication when a verifier is
// installed. /v1/health stays open for local readiness. It buffers the body so
// the signature can be checked over the exact received bytes before the handler
// reads them; the buffer is bounded by the artifact size limit.
func (s *Server) authWrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if len(s.allowedUIDs) > 0 {
			creds, ok := request.Context().Value(peerCredKey{}).(peercred.PeerCredentials)
			if !ok || creds.UID < 0 || !s.allowedUIDs[creds.UID] {
				writeError(response, newSafeError(http.StatusForbidden, errcodes.Unauthorized, "peer not authorized"))
				return
			}
		}
		if s.verifier == nil || (request.Method == http.MethodGet && request.URL.Path == "/v1/health") {
			next.ServeHTTP(response, request)
			return
		}
		limit := s.maxArtifactBytes
		if limit <= 0 {
			limit = 1 << 20
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
		_ = request.Body.Close()
		if err != nil || int64(len(body)) > limit {
			writeError(response, newSafeError(http.StatusRequestEntityTooLarge, errcodes.InputRejected, "request body too large"))
			return
		}
		if err := s.verifier.Verify(request.Method, request.URL.Path, request.URL.RawQuery, body, request.Header); err != nil {
			writeError(response, newSafeError(http.StatusUnauthorized, errcodes.Unauthorized, "command authentication failed"))
			return
		}
		request.Body = io.NopCloser(bytes.NewReader(body))
		next.ServeHTTP(response, request)
	})
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

// SetArtifactBroker wires the in-process artifact broker used by input ingest
// and output egress, bounded by maxBytes per artifact.
func (s *Server) SetArtifactBroker(broker *artifacts.Broker, maxBytes int64) {
	s.broker = broker
	s.maxArtifactBytes = maxBytes
}

// SetCommandVerifier installs asymmetric command authentication. When set, every
// route except GET /v1/health requires a valid signed command.
func (s *Server) SetCommandVerifier(verifier *commandauth.Verifier) {
	s.verifier = verifier
}

// SetPeerAllowlist enforces that every connecting peer's OS uid is in the given
// set. An empty list disables the check (the private socket remains the control).
func (s *Server) SetPeerAllowlist(uids []int) {
	if len(uids) == 0 {
		s.allowedUIDs = nil
		return
	}
	allowed := make(map[int]bool, len(uids))
	for _, uid := range uids {
		allowed[uid] = true
	}
	s.allowedUIDs = allowed
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
