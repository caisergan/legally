// Package signer coordinates durable jobs, the physical-token queue, PIN
// challenges, and the one-time authorization handoff (plan §6-§8). It reaches
// PIN_REQUIRED and hands a decrypted PIN to an Operation; it never signs.
package signer

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/pin"
)

var (
	ErrChallengeUnavailable = errors.New("no active challenge for job")
	ErrAuthorizationClosed  = errors.New("job is no longer awaiting authorization")
)

// Operation performs the token work after AUTHORIZATION_CONSUMED: exactly one
// login with the decrypted PIN and, on success, the atomic SIGNING claim and
// signature. It owns every transition from AUTHORIZATION_CONSUMED onward and
// must never sign twice.
type Operation interface {
	Run(ctx context.Context, job jobs.Job, pin string) error
}

// AuthorizeStatus is the safe result of an authorize attempt.
type AuthorizeStatus string

const (
	StatusConsumed        AuthorizeStatus = "consumed"
	StatusAlreadyConsumed AuthorizeStatus = "already_consumed"
)

type activeChallenge struct {
	issued  pin.Issued
	handoff chan string
}

// Coordinator serializes jobs per physical-token queue key and runs the PIN
// challenge/authorize lifecycle.
type Coordinator struct {
	service   *jobs.Service
	signer    *pin.Signer
	queue     *jobs.TokenQueue
	operation Operation

	mu     sync.Mutex
	active map[string]*activeChallenge
}

func NewCoordinator(service *jobs.Service, signer *pin.Signer, queue *jobs.TokenQueue, operation Operation) *Coordinator {
	return &Coordinator{
		service:   service,
		signer:    signer,
		queue:     queue,
		operation: operation,
		active:    map[string]*activeChallenge{},
	}
}

// Submit enqueues a QUEUED job onto its physical-token queue. Work runs
// serialized per queue key; different tokens run in parallel.
func (c *Coordinator) Submit(ctx context.Context, job jobs.Job) error {
	result := c.queue.Submit(ctx, job.QueueKey, func(workCtx context.Context) error {
		return c.run(workCtx, job.ID)
	})
	go func() { <-result }()
	return nil
}

func (c *Coordinator) run(ctx context.Context, jobID string) error {
	job, err := c.service.Get(ctx, jobID)
	if err != nil {
		return err
	}
	if job.State != jobs.StateQueued {
		return nil // cancelled or already handled while queued
	}

	issued, err := c.signer.Issue(pin.Binding{
		JobID:                        job.ID,
		RequestID:                    job.RequestID,
		InputSHA256:                  job.Input.SHA256,
		CertificateFingerprintSHA256: job.CertificateFingerprintSHA256,
		PolicyVersion:                job.PolicyVersion,
	})
	if err != nil {
		return err
	}
	handoff := make(chan string, 1)
	c.mu.Lock()
	c.active[jobID] = &activeChallenge{issued: issued, handoff: handoff}
	c.mu.Unlock()
	defer c.clear(jobID)

	if err := c.service.PersistChallenge(ctx, jobID, issued.ChallengeID, "challenge-key", issued.Thumbprint, issued.Nonce, issued.ExpiresAt); err != nil {
		return err
	}
	pinRequired, err := c.service.Transition(ctx, jobID, job.Version, jobs.StatePINRequired, "", "pin required")
	if err != nil {
		return err
	}

	select {
	case pinValue := <-handoff:
		consumed, err := c.service.Get(ctx, jobID)
		if err != nil {
			return err
		}
		return c.operation.Run(ctx, consumed, pinValue)
	case <-time.After(time.Until(issued.ExpiresAt)):
		_, _ = c.service.Transition(ctx, jobID, pinRequired.Version, jobs.StatePINWindowExpired, errcodes.PINWindowExpired, "pin window expired")
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Challenge returns the active challenge for a PIN_REQUIRED job.
func (c *Coordinator) Challenge(jobID string) (pin.Challenge, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.active[jobID]
	if !ok {
		return pin.Challenge{}, ErrChallengeUnavailable
	}
	return entry.issued.Challenge, nil
}

// Authorize validates and decrypts a PIN envelope, atomically consumes the
// one-time challenge together with the AUTHORIZATION_CONSUMED transition, and
// hands the PIN to the waiting worker. A repeat after consumption returns
// StatusAlreadyConsumed and never triggers a second login.
func (c *Coordinator) Authorize(ctx context.Context, jobID, challengeID, pinJWE string) (AuthorizeStatus, error) {
	c.mu.Lock()
	entry, ok := c.active[jobID]
	c.mu.Unlock()
	if !ok || entry.issued.ChallengeID != challengeID {
		return "", jobs.ErrChallengeNotFound
	}

	pinValue, err := pin.DecryptPIN(pinJWE, entry.issued.Recipient, challengeID)
	if err != nil {
		return "", err
	}

	if _, err := c.service.ConsumeChallenge(ctx, jobID, challengeID); err != nil {
		if errors.Is(err, jobs.ErrChallengeConsumed) {
			return StatusAlreadyConsumed, nil
		}
		return "", err
	}

	select {
	case entry.handoff <- pinValue:
		return StatusConsumed, nil
	default:
		// Worker is gone (timeout/shutdown); consumption is already durable and
		// recovery will terminate the job without replay.
		return StatusConsumed, nil
	}
}

func (c *Coordinator) clear(jobID string) {
	c.mu.Lock()
	delete(c.active, jobID)
	c.mu.Unlock()
}
