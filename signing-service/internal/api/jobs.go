package api

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
	"github.com/caisergan/legally/signing-service/internal/jobs"
	"github.com/caisergan/legally/signing-service/internal/pin"
	"github.com/caisergan/legally/signing-service/internal/signer"
)

func (s *Server) requireJobs(response http.ResponseWriter) bool {
	if s.jobs == nil || s.coordinator == nil {
		writeError(response, newSafeError(http.StatusServiceUnavailable, errcodes.ServiceUnavailable, "job service unavailable"))
		return false
	}
	return true
}

func (s *Server) handleCreateJob(response http.ResponseWriter, request *http.Request) {
	if !s.requireJobs(response) {
		return
	}
	var body CreateJobRequest
	if err := decodeStrict(request.Body, &body); err != nil {
		writeError(response, newSafeError(http.StatusUnprocessableEntity, errcodes.InvalidRequest, "malformed job request"))
		return
	}
	if key := request.Header.Get("Idempotency-Key"); key == "" || key != body.CommandID {
		writeError(response, newSafeError(http.StatusBadRequest, errcodes.InvalidRequest, "Idempotency-Key must match command_id"))
		return
	}
	params, err := body.toParams()
	if err != nil {
		writeError(response, newSafeError(http.StatusUnprocessableEntity, errcodes.InvalidRequest, "invalid job fields"))
		return
	}

	job, created, err := s.jobs.Create(request.Context(), params)
	if err != nil {
		writeError(response, mapCreateError(err))
		return
	}
	if created {
		if err := s.coordinator.Submit(request.Context(), job); err != nil {
			writeError(response, newSafeError(http.StatusServiceUnavailable, errcodes.ServiceUnavailable, "failed to queue job"))
			return
		}
	}
	status := http.StatusCreated
	if !created {
		status = http.StatusOK
	}
	events, _ := s.jobs.Events(request.Context(), job.ID, 0)
	writeJSON(response, status, CreateJobResponse{
		ID:            job.ID,
		RequestID:     job.RequestID,
		State:         string(job.State),
		Version:       job.Version,
		EventSequence: len(events),
		CreatedAt:     job.CreatedAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleCommandStatus(response http.ResponseWriter, request *http.Request) {
	if !s.requireJobs(response) {
		return
	}
	job, err := s.jobs.GetByCommand(request.Context(), request.PathValue("command_id"))
	if err != nil {
		writeError(response, newSafeError(http.StatusNotFound, errcodes.InvalidRequest, "command not found"))
		return
	}
	writeJSON(response, http.StatusOK, CommandStatusResponse{CommandID: job.CommandID, JobID: job.ID, State: string(job.State)})
}

func (s *Server) handleGetJob(response http.ResponseWriter, request *http.Request) {
	if !s.requireJobs(response) {
		return
	}
	job, err := s.jobs.Get(request.Context(), request.PathValue("job_id"))
	if err != nil {
		writeError(response, newSafeError(http.StatusNotFound, errcodes.InvalidRequest, "job not found"))
		return
	}
	writeJSON(response, http.StatusOK, jobStateResponse(job))
}

func (s *Server) handleJobEvents(response http.ResponseWriter, request *http.Request) {
	if !s.requireJobs(response) {
		return
	}
	after := 0
	if raw := request.URL.Query().Get("after"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(response, newSafeError(http.StatusBadRequest, errcodes.InvalidRequest, "invalid after cursor"))
			return
		}
		after = parsed
	}
	events, err := s.jobs.Events(request.Context(), request.PathValue("job_id"), after)
	if err != nil {
		writeError(response, newSafeError(http.StatusInternalServerError, errcodes.ServiceUnavailable, "failed to read events"))
		return
	}
	out := EventsResponse{Items: make([]EventResponse, 0, len(events))}
	for _, event := range events {
		out.Items = append(out.Items, EventResponse{
			Sequence: event.Sequence, Type: event.Type, State: string(event.State),
			SafeCode: string(event.SafeCode), Detail: event.Detail,
			CreatedAt: event.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	writeJSON(response, http.StatusOK, out)
}

func (s *Server) handlePINChallenge(response http.ResponseWriter, request *http.Request) {
	if !s.requireJobs(response) {
		return
	}
	jobID := request.PathValue("job_id")
	job, err := s.jobs.Get(request.Context(), jobID)
	if err != nil {
		writeError(response, newSafeError(http.StatusNotFound, errcodes.InvalidRequest, "job not found"))
		return
	}
	if job.State != jobs.StatePINRequired {
		writeError(response, newSafeError(http.StatusConflict, errcodes.InvalidRequest, "job is not awaiting a pin"))
		return
	}
	challenge, err := s.coordinator.Challenge(jobID)
	if err != nil {
		writeError(response, newSafeError(http.StatusConflict, errcodes.InvalidRequest, "no active challenge"))
		return
	}
	writeJSON(response, http.StatusOK, PINChallengeResponse{
		ChallengeID:  challenge.ChallengeID,
		ChallengeJWS: challenge.JWS,
		RecipientJWK: challenge.RecipientJWK,
		ExpiresAt:    challenge.ExpiresAt.UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleAuthorize(response http.ResponseWriter, request *http.Request) {
	if !s.requireJobs(response) {
		return
	}
	var body AuthorizeRequest
	if err := decodeStrict(request.Body, &body); err != nil {
		writeError(response, newSafeError(http.StatusUnprocessableEntity, errcodes.InvalidRequest, "malformed authorization"))
		return
	}
	status, err := s.coordinator.Authorize(request.Context(), request.PathValue("job_id"), body.ChallengeID, body.PINJWE)
	if err != nil {
		writeError(response, mapAuthorizeError(err))
		return
	}
	writeJSON(response, http.StatusAccepted, AuthorizeResponse{AuthorizationStatus: string(status)})
}

func (s *Server) handleCancelJob(response http.ResponseWriter, request *http.Request) {
	if !s.requireJobs(response) {
		return
	}
	jobID := request.PathValue("job_id")
	job, err := s.jobs.Get(request.Context(), jobID)
	if err != nil {
		writeError(response, newSafeError(http.StatusNotFound, errcodes.InvalidRequest, "job not found"))
		return
	}
	if job.State == jobs.StateCancelled {
		writeJSON(response, http.StatusOK, jobStateResponse(job))
		return
	}
	if !jobs.CancellationAllowed(job.State) {
		writeError(response, newSafeError(http.StatusConflict, errcodes.InvalidRequest, "job can no longer be cancelled"))
		return
	}
	updated, err := s.jobs.Transition(request.Context(), jobID, job.Version, jobs.StateCancelled, "", "cancelled by request")
	if err != nil {
		writeError(response, newSafeError(http.StatusConflict, errcodes.InvalidRequest, "job can no longer be cancelled"))
		return
	}
	writeJSON(response, http.StatusOK, jobStateResponse(updated))
}

func jobStateResponse(job jobs.Job) JobStateResponse {
	return JobStateResponse{
		ID:                           job.ID,
		RequestID:                    job.RequestID,
		State:                        string(job.State),
		Version:                      job.Version,
		CertificateFingerprintSHA256: job.CertificateFingerprintSHA256,
		InputSHA256:                  job.Input.SHA256,
		InputByteCount:               job.Input.ByteCount,
		PolicyVersion:                job.PolicyVersion,
		FailureCode:                  string(job.FailureCode),
		OutputSHA256:                 job.OutputSHA256,
		OutputByteCount:              job.OutputByteCount,
		CancellationEligible:         jobs.CancellationAllowed(job.State),
		CreatedAt:                    job.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:                    job.UpdatedAt.UTC().Format(time.RFC3339),
	}
}

func (r CreateJobRequest) toParams() (jobs.CreateParams, error) {
	approvedAt, err := time.Parse(time.RFC3339Nano, r.Approval.ApprovedAt)
	if err != nil {
		return jobs.CreateParams{}, err
	}
	approvalExpires, err := time.Parse(time.RFC3339Nano, r.Approval.ExpiresAt)
	if err != nil {
		return jobs.CreateParams{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, r.ExpiresAt)
	if err != nil {
		return jobs.CreateParams{}, err
	}
	return jobs.CreateParams{
		CommandID:                    r.CommandID,
		JobID:                        r.JobID,
		RequestID:                    r.RequestID,
		CredentialID:                 r.CredentialID,
		CertificateFingerprintSHA256: r.CertificateFingerprintSHA256,
		PolicyVersion:                r.PolicyVersion,
		Profile:                      r.Profile,
		Algorithm:                    r.Algorithm,
		Input:                        jobs.Input{ArtifactID: r.Input.ArtifactID, ByteCount: r.Input.ByteCount, SHA256: r.Input.SHA256},
		Output:                       jobs.Output{ArtifactID: r.Output.ArtifactID, MaxByteCount: r.Output.MaxByteCount},
		Approval:                     jobs.Approval{ApprovalID: r.Approval.ApprovalID, ApprovedAt: approvedAt, ExpiresAt: approvalExpires},
		Nonce:                        r.Nonce,
		ExpiresAt:                    expiresAt,
	}, nil
}

func mapCreateError(err error) SafeError {
	switch {
	case errors.Is(err, jobs.ErrIdempotencyConflict):
		return newSafeError(http.StatusConflict, errcodes.InvalidRequest, "command reused with a different payload")
	case errors.Is(err, jobs.ErrPolicyMismatch):
		return newSafeError(http.StatusUnprocessableEntity, errcodes.PolicyMismatch, "policy does not match")
	case errors.Is(err, jobs.ErrCredentialNotFound):
		return newSafeError(http.StatusNotFound, errcodes.CredentialNotFound, "credential not found")
	case errors.Is(err, jobs.ErrCredentialDisabled):
		return newSafeError(http.StatusConflict, errcodes.CredentialDisabled, "credential disabled")
	case errors.Is(err, jobs.ErrCertificateMismatch):
		return newSafeError(http.StatusUnprocessableEntity, errcodes.CertificateMismatch, "certificate mismatch")
	default:
		return newSafeError(http.StatusInternalServerError, errcodes.ServiceUnavailable, "failed to create job")
	}
}

func mapAuthorizeError(err error) SafeError {
	switch {
	case errors.Is(err, jobs.ErrChallengeNotFound), errors.Is(err, signer.ErrChallengeUnavailable):
		return newSafeError(http.StatusConflict, errcodes.InvalidRequest, "no active challenge")
	case errors.Is(err, jobs.ErrChallengeExpired):
		return newSafeError(http.StatusGone, errcodes.PINWindowExpired, "challenge expired")
	case errors.Is(err, pin.ErrEnvelopeMalformed), errors.Is(err, pin.ErrEnvelopeChallenge):
		return newSafeError(http.StatusUnprocessableEntity, errcodes.InvalidRequest, "invalid pin envelope")
	default:
		return newSafeError(http.StatusInternalServerError, errcodes.ServiceUnavailable, "authorization failed")
	}
}
