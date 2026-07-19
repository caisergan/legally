package jobs

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var (
	ErrChallengeNotFound = errors.New("pin challenge not found")
	ErrChallengeExpired  = errors.New("pin challenge expired")
	ErrChallengeConsumed = errors.New("pin challenge already consumed")
	ErrChallengeInactive = errors.New("job is not awaiting a pin")
)

// PersistChallenge records safe challenge metadata. It never stores the PIN,
// the ciphertext, or the ephemeral private key.
func (s *Service) PersistChallenge(ctx context.Context, jobID, challengeID, keyID, thumbprint, nonce string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO pin_challenges
		(challenge_id, job_id, key_id, recipient_jwk_thumbprint, nonce, state, created_at, expires_at)
		VALUES (?,?,?,?,?, 'active', ?, ?)`,
		challengeID, jobID, keyID, thumbprint, nonce, rfc3339(s.now()), rfc3339(expiresAt))
	return err
}

// ConsumeChallenge atomically marks the challenge consumed and transitions the
// job PIN_REQUIRED -> AUTHORIZATION_CONSUMED. A second call returns
// ErrChallengeConsumed and performs no transition (plan §8.2).
func (s *Service) ConsumeChallenge(ctx context.Context, jobID, challengeID string) (Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Job{}, err
	}
	defer tx.Rollback()

	var state, expiresAt string
	err = tx.QueryRowContext(ctx, `SELECT state, expires_at FROM pin_challenges WHERE challenge_id=? AND job_id=?`, challengeID, jobID).
		Scan(&state, &expiresAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Job{}, ErrChallengeNotFound
	case err != nil:
		return Job{}, err
	}
	switch state {
	case "consumed":
		return Job{}, ErrChallengeConsumed
	case "expired":
		return Job{}, ErrChallengeExpired
	}

	expiry, err := parseTime(expiresAt)
	if err != nil {
		return Job{}, err
	}
	now := s.now()
	if now.After(expiry) {
		if _, err := tx.ExecContext(ctx, `UPDATE pin_challenges SET state='expired' WHERE challenge_id=?`, challengeID); err != nil {
			return Job{}, err
		}
		if err := tx.Commit(); err != nil {
			return Job{}, err
		}
		return Job{}, ErrChallengeExpired
	}

	current, err := loadJob(ctx, tx, jobID)
	if err != nil {
		return Job{}, err
	}
	if current.State != StatePINRequired {
		return Job{}, ErrChallengeInactive
	}
	if _, err := tx.ExecContext(ctx, `UPDATE pin_challenges SET state='consumed', consumed_at=? WHERE challenge_id=?`, rfc3339(now), challengeID); err != nil {
		return Job{}, err
	}
	if err := s.applyTransitionTx(ctx, tx, current, StateAuthorizationConsumed, "", "pin authorization consumed"); err != nil {
		return Job{}, err
	}
	if err := tx.Commit(); err != nil {
		return Job{}, err
	}
	return loadJob(ctx, s.db, jobID)
}
