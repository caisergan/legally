// Package assembly wires the signer job pipeline (durable journal, credential
// registry, in-process artifact broker, plan authorizer, token operation, PIN
// challenge signer, and per-token coordinator) that the daemon serves and the
// live end-to-end tests drive. Both call Build so they exercise identical wiring.
package assembly

import (
	"crypto/ecdsa"
	"database/sql"
	"time"

	"github.com/caisergan/legally/signing-service/internal/artifacts"
	"github.com/caisergan/legally/signing-service/internal/credentials"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/pin"
	"github.com/caisergan/legally/signing-service/internal/planauth"
	"github.com/caisergan/legally/signing-service/internal/signer"
	"github.com/caisergan/legally/signing-service/internal/token"
)

// Deps are the injected components and policy durations for the signer pipeline.
// Backend, ChallengeKey, and SupervisorKey are signer-local secrets supplied by
// the caller (loaded from disk in the daemon, generated in tests). Validator is
// optional; when nil the operation still runs the local PAdES verify.
type Deps struct {
	DB               *sql.DB
	Backend          token.Backend
	ChallengeKeyID   string
	ChallengeKey     *ecdsa.PrivateKey
	SupervisorKey    []byte
	Validator        signer.Validator
	PINWindow        time.Duration
	PlanTTL          time.Duration
	AuthorizationTTL time.Duration
	CapabilityTTL    time.Duration
}

// Components are the wired pipeline objects. Jobs and Coordinator feed the API
// server; Registry and Broker are exposed for inventory and input seeding.
type Components struct {
	Jobs        *jobs.Service
	Registry    *credentials.Registry
	Broker      *artifacts.Broker
	Coordinator *signer.Coordinator
	Queue       *jobs.TokenQueue
}

// Build assembles the pipeline. It performs no I/O and never touches the token.
func Build(d Deps) *Components {
	service := jobs.NewService(d.DB)
	registry := credentials.NewRegistry(d.DB)
	broker := artifacts.NewBroker(d.CapabilityTTL)
	authorizer := planauth.NewAuthorizer(d.SupervisorKey, []string{signer.FormatEngineVersion}, d.AuthorizationTTL)
	operation := signer.NewTokenOperation(service, d.Backend, broker, authorizer, registry, d.Validator, d.PlanTTL)
	pinSigner := pin.NewSigner(d.ChallengeKeyID, d.ChallengeKey, d.PINWindow)
	queue := jobs.NewTokenQueue()
	coordinator := signer.NewCoordinator(service, pinSigner, queue, operation)
	return &Components{
		Jobs:        service,
		Registry:    registry,
		Broker:      broker,
		Coordinator: coordinator,
		Queue:       queue,
	}
}
