package jobs

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
)

// RecoveryReport summarizes crash-recovery classification at startup.
type RecoveryReport struct {
	OutcomeUnknown    int
	AuthorizationLost int
	Preserved         int
}

// Recover reconciles in-flight jobs after a restart (plan §7.2, §9.2):
//   - a job whose SIGNING claim is durable becomes OUTCOME_UNKNOWN;
//   - a job stuck in AUTHORIZATION_CONSUMED becomes FAILED_PRE_SIGN with
//     AUTHORIZATION_LOST_BEFORE_SIGNING, discarding the consumed authorization;
//   - all other non-terminal jobs are preserved and never auto-replayed.
func (s *Service) Recover(ctx context.Context) (RecoveryReport, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, state, version, signing_claimed_at FROM jobs`)
	if err != nil {
		return RecoveryReport{}, err
	}
	type candidate struct {
		id             string
		state          State
		version        int
		signingClaimed bool
	}
	var candidates []candidate
	for rows.Next() {
		var (
			id, state string
			version   int
			claimed   sql.NullString
		)
		if err := rows.Scan(&id, &state, &version, &claimed); err != nil {
			rows.Close()
			return RecoveryReport{}, err
		}
		candidates = append(candidates, candidate{id, State(state), version, claimed.Valid && claimed.String != ""})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return RecoveryReport{}, err
	}
	rows.Close()

	var report RecoveryReport
	for _, c := range candidates {
		if c.state.IsTerminal() {
			continue
		}
		switch {
		case c.signingClaimed:
			if err := s.forceRecovery(ctx, c.id, c.version, StateOutcomeUnknown, errcodes.SigningOutcomeUnknown,
				"crash after signing claim without durable final evidence"); err != nil {
				return report, err
			}
			report.OutcomeUnknown++
		case c.state == StateAuthorizationConsumed:
			if err := s.forceRecovery(ctx, c.id, c.version, StateFailedPreSign, errcodes.AuthorizationLostBeforeSigning,
				"authorization lost before signing; new owner approval required"); err != nil {
				return report, err
			}
			report.AuthorizationLost++
		default:
			report.Preserved++
		}
	}
	return report, nil
}

// forceRecovery moves a crashed job directly to a terminal recovery state.
// It bypasses the normal transition graph because it reconciles a crash rather
// than a normal operation, but it never targets a non-terminal state and never
// re-enqueues work.
func (s *Service) forceRecovery(ctx context.Context, jobID string, version int, to State, code errcodes.Code, detail string) error {
	if !to.IsTerminal() {
		return fmt.Errorf("recovery target %q must be terminal", to)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx, `UPDATE jobs SET state=?, version=?, failure_code=COALESCE(failure_code, ?), updated_at=? WHERE id=? AND version=?`,
		string(to), version+1, string(code), rfc3339(s.now()), jobID, version)
	if err != nil {
		return err
	}
	if affected, _ := result.RowsAffected(); affected != 1 {
		return ErrVersionConflict
	}
	if _, err := appendEventTx(ctx, tx, jobID, "recovery", to, code, detail, s.now()); err != nil {
		return err
	}
	return tx.Commit()
}
