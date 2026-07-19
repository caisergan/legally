package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/caisergan/legally/signing-service/internal/api"
	"github.com/caisergan/legally/signing-service/internal/assembly"
	"github.com/caisergan/legally/signing-service/internal/commandauth"
	"github.com/caisergan/legally/signing-service/internal/config"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/persistence"
	"github.com/caisergan/legally/signing-service/internal/token"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		log.Fatalf("invalid signer configuration: %v", err)
	}
	if !cfg.Enabled {
		log.Print("yargi-signerd is disabled")
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := persistence.Open(cfg.DatabasePath)
	if err != nil {
		log.Fatalf("open signer journal: %v", err)
	}
	defer db.Close()

	report, err := jobs.NewService(db.DB).Recover(ctx)
	if err != nil {
		log.Fatalf("reconcile signer jobs: %v", err)
	}
	log.Printf("recovery: outcome_unknown=%d authorization_lost=%d preserved=%d",
		report.OutcomeUnknown, report.AuthorizationLost, report.Preserved)

	backend, components, err := buildSigningPipeline(cfg, db.DB)
	if err != nil {
		log.Fatalf("wire signing pipeline: %v", err)
	}
	defer backend.Close()

	server := api.NewServer(cfg)
	server.SetReadiness(func() api.Readiness {
		if err := db.PingContext(context.Background()); err != nil {
			return api.Readiness{Ready: false, Database: "unavailable"}
		}
		return api.Readiness{Ready: true, Database: "ok"}
	})
	server.SetCredentialInventory(components.Registry)
	server.SetJobService(components.Jobs, components.Coordinator)
	server.SetArtifactBroker(components.Broker, cfg.MaxArtifactBytes)

	if cfg.CommandPubKeys != "" {
		keys, err := commandauth.ParsePinnedKeys(cfg.CommandPubKeys)
		if err != nil {
			log.Fatalf("parse command pubkeys: %v", err)
		}
		server.SetCommandVerifier(commandauth.NewVerifier(keys, cfg.CommandSkew))
		log.Printf("command authentication enabled: %d pinned key(s)", len(keys))
	} else {
		log.Print("command authentication disabled (no SIGNERD_COMMAND_PUBKEYS): relying on the private socket")
	}

	log.Printf("starting yargi-signerd version=%s mode=%s transport=%s module=%s", cfg.Version, cfg.Mode, cfg.Transport, cfg.ModuleAlias)
	if err := server.ListenAndServe(ctx); err != nil {
		log.Fatalf("signer service stopped: %v", err)
	}
}

// buildSigningPipeline verifies and loads the allowlisted PKCS#11 module and the
// signer-local challenge and supervisor keys, then wires the job pipeline. It
// fails closed: an enabled token mode with a missing module or key aborts start.
func buildSigningPipeline(cfg config.Config, db *sql.DB) (token.Backend, *assembly.Components, error) {
	if cfg.ConfigPath == "" {
		return nil, nil, errors.New("SIGNERD_CONFIG (module allowlist) is required")
	}
	if cfg.ModuleAlias == "" {
		return nil, nil, errors.New("SIGNERD_MODULE_ALIAS is required")
	}
	if cfg.ChallengeKeyPath == "" || cfg.SupervisorKeyPath == "" {
		return nil, nil, errors.New("SIGNERD_CHALLENGE_KEY and SIGNERD_SUPERVISOR_KEY are required")
	}

	allowlist, err := config.LoadModuleAllowlist(cfg.ConfigPath)
	if err != nil {
		return nil, nil, fmt.Errorf("load module allowlist: %w", err)
	}
	entry, ok := allowlist.Lookup(cfg.ModuleAlias)
	if !ok {
		return nil, nil, fmt.Errorf("module alias %q is not allowlisted", cfg.ModuleAlias)
	}
	if err := token.VerifyModule(entry, moduleMode(cfg.Mode)); err != nil {
		return nil, nil, fmt.Errorf("verify module %q: %w", cfg.ModuleAlias, err)
	}
	backend, err := token.NewBackend(entry.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("open pkcs11 module: %w", err)
	}

	challengeKey, err := loadChallengeKey(cfg.ChallengeKeyPath)
	if err != nil {
		backend.Close()
		return nil, nil, fmt.Errorf("load challenge key: %w", err)
	}
	supervisorKey, err := loadSupervisorKey(cfg.SupervisorKeyPath)
	if err != nil {
		backend.Close()
		return nil, nil, fmt.Errorf("load supervisor key: %w", err)
	}

	components := assembly.Build(assembly.Deps{
		DB:               db,
		Backend:          backend,
		ChallengeKeyID:   cfg.ChallengeKeyID,
		ChallengeKey:     challengeKey,
		SupervisorKey:    supervisorKey,
		Validator:        nil,
		PINWindow:        cfg.PINWindow,
		PlanTTL:          cfg.PlanTTL,
		AuthorizationTTL: cfg.AuthorizationTTL,
		CapabilityTTL:    cfg.CapabilityTTL,
	})
	return backend, components, nil
}

func moduleMode(m config.Mode) config.ModuleMode {
	if m == config.ModeHardware {
		return config.ModuleModeHardware
	}
	return config.ModuleModeTest
}

func loadChallengeKey(path string) (*ecdsa.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("challenge key is not PEM-encoded")
	}
	var key *ecdsa.PrivateKey
	switch block.Type {
	case "EC PRIVATE KEY":
		key, err = x509.ParseECPrivateKey(block.Bytes)
	case "PRIVATE KEY":
		var parsed any
		if parsed, err = x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
			ecKey, ok := parsed.(*ecdsa.PrivateKey)
			if !ok {
				return nil, errors.New("challenge key must be an ECDSA private key")
			}
			key = ecKey
		}
	default:
		return nil, fmt.Errorf("unsupported challenge key PEM type %q", block.Type)
	}
	if err != nil {
		return nil, err
	}
	if key.Curve != elliptic.P256() {
		return nil, errors.New("challenge key must be a P-256 key for ES256")
	}
	return key, nil
}

func loadSupervisorKey(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	key := bytes.TrimSpace(data)
	if len(key) < 32 {
		return nil, errors.New("supervisor key must be at least 32 bytes")
	}
	return append([]byte(nil), key...), nil
}
