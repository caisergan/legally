package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/caisergan/legally/signing-service/internal/api"
	"github.com/caisergan/legally/signing-service/internal/config"
	"github.com/caisergan/legally/signing-service/internal/credentials"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/persistence"
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

	if cfg.ConfigPath != "" {
		allowlist, err := config.LoadModuleAllowlist(cfg.ConfigPath)
		if err != nil {
			log.Fatalf("invalid module allowlist: %v", err)
		}
		log.Printf("loaded module allowlist: %d modules", len(allowlist.Modules))
	}

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

	server := api.NewServer(cfg)
	server.SetReadiness(func() api.Readiness {
		if err := db.PingContext(context.Background()); err != nil {
			return api.Readiness{Ready: false, Database: "unavailable"}
		}
		return api.Readiness{Ready: true, Database: "ok"}
	})
	server.SetCredentialInventory(credentials.NewRegistry(db.DB))

	log.Printf("starting yargi-signerd version=%s mode=%s transport=%s", cfg.Version, cfg.Mode, cfg.Transport)
	if err := server.ListenAndServe(ctx); err != nil {
		log.Fatalf("signer service stopped: %v", err)
	}
}
