package jobs

import (
	"context"
	"database/sql"
	"testing"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
)

func mustTransition(t *testing.T, service *Service, jobID string, version int, to State) {
	t.Helper()
	if _, err := service.Transition(context.Background(), jobID, version, to, "", ""); err != nil {
		t.Fatalf("transition %s v%d -> %s: %v", jobID, version, to, err)
	}
}

func stateOf(t *testing.T, db *sql.DB, jobID string) (State, string) {
	t.Helper()
	var state string
	var failure sql.NullString
	if err := db.QueryRow(`SELECT state, failure_code FROM jobs WHERE id=?`, jobID).Scan(&state, &failure); err != nil {
		t.Fatalf("read job %s: %v", jobID, err)
	}
	return State(state), failure.String
}

func TestRecoverClassifiesInFlightJobs(t *testing.T) {
	service, db := newTestService(t)
	ctx := context.Background()

	// Job driven past the SIGNING claim.
	if _, _, err := service.Create(ctx, sampleParams("cmd-sign", "job-sign")); err != nil {
		t.Fatalf("create sign job: %v", err)
	}
	mustTransition(t, service, "job-sign", 1, StatePINRequired)
	mustTransition(t, service, "job-sign", 2, StateAuthorizationConsumed)
	mustTransition(t, service, "job-sign", 3, StateSigning)

	// Job stuck at AUTHORIZATION_CONSUMED with no signing claim.
	if _, _, err := service.Create(ctx, sampleParams("cmd-auth", "job-auth")); err != nil {
		t.Fatalf("create auth job: %v", err)
	}
	mustTransition(t, service, "job-auth", 1, StatePINRequired)
	mustTransition(t, service, "job-auth", 2, StateAuthorizationConsumed)

	// Job still queued, never touched by the token.
	if _, _, err := service.Create(ctx, sampleParams("cmd-queue", "job-queue")); err != nil {
		t.Fatalf("create queued job: %v", err)
	}

	// Simulate a restart: a fresh service over the same durable journal.
	recovered := NewService(db)
	report, err := recovered.Recover(ctx)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if report.OutcomeUnknown != 1 || report.AuthorizationLost != 1 || report.Preserved != 1 {
		t.Fatalf("unexpected report: %+v", report)
	}

	if state, code := stateOf(t, db, "job-sign"); state != StateOutcomeUnknown || code != string(errcodes.SigningOutcomeUnknown) {
		t.Fatalf("sign job = %s/%s, want OUTCOME_UNKNOWN/%s", state, code, errcodes.SigningOutcomeUnknown)
	}
	if state, code := stateOf(t, db, "job-auth"); state != StateFailedPreSign || code != string(errcodes.AuthorizationLostBeforeSigning) {
		t.Fatalf("auth job = %s/%s, want FAILED_PRE_SIGN/%s", state, code, errcodes.AuthorizationLostBeforeSigning)
	}
	if state, _ := stateOf(t, db, "job-queue"); state != StateQueued {
		t.Fatalf("queued job was mutated to %s; recovery must not replay it", state)
	}

	// A durable recovery event is recorded for each forced job.
	events, err := recovered.Events(ctx, "job-sign", 0)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	last := events[len(events)-1]
	if last.Type != "recovery" || last.State != StateOutcomeUnknown {
		t.Fatalf("missing recovery event: %+v", last)
	}
}

func TestRecoverIsIdempotent(t *testing.T) {
	service, db := newTestService(t)
	ctx := context.Background()
	if _, _, err := service.Create(ctx, sampleParams("cmd-sign", "job-sign")); err != nil {
		t.Fatalf("create: %v", err)
	}
	mustTransition(t, service, "job-sign", 1, StatePINRequired)
	mustTransition(t, service, "job-sign", 2, StateAuthorizationConsumed)
	mustTransition(t, service, "job-sign", 3, StateSigning)

	if _, err := service.Recover(ctx); err != nil {
		t.Fatalf("first recover: %v", err)
	}
	report, err := service.Recover(ctx)
	if err != nil {
		t.Fatalf("second recover: %v", err)
	}
	if report.OutcomeUnknown != 0 || report.AuthorizationLost != 0 {
		t.Fatalf("second recovery re-touched terminal jobs: %+v", report)
	}
	if state, _ := stateOf(t, db, "job-sign"); state != StateOutcomeUnknown {
		t.Fatalf("job-sign drifted to %s on second recovery", state)
	}
}
