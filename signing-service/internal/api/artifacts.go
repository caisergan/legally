package api

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"

	"github.com/caisergan/legally/signing-service/internal/errcodes"
)

const (
	headerArtifactSHA256    = "X-Artifact-Sha256"
	headerArtifactByteCount = "X-Artifact-Byte-Count"
	headerOutputSHA256      = "X-Output-Sha256"
)

// handleArtifactPut ingests an input artifact's bytes into the broker under the
// caller-supplied artifact id, verifying the declared SHA-256 before storing.
// The bytes are held privately in-process; a later job binds them by id, hash,
// and size at MintRead. Command authentication (when enabled) already bounded
// and authenticated the body in authWrap.
func (s *Server) handleArtifactPut(response http.ResponseWriter, request *http.Request) {
	if s.broker == nil {
		writeError(response, newSafeError(http.StatusServiceUnavailable, errcodes.ServiceUnavailable, "artifact broker unavailable"))
		return
	}
	artifactID := request.PathValue("artifact_id")
	if artifactID == "" {
		writeError(response, newSafeError(http.StatusBadRequest, errcodes.InvalidRequest, "artifact id required"))
		return
	}
	expected := request.Header.Get(headerArtifactSHA256)

	limit := s.maxArtifactBytes
	if limit <= 0 {
		limit = 1 << 20
	}
	data, err := io.ReadAll(io.LimitReader(request.Body, limit+1))
	if err != nil {
		writeError(response, newSafeError(http.StatusBadRequest, errcodes.InputRejected, "failed to read artifact body"))
		return
	}
	if int64(len(data)) > limit {
		writeError(response, newSafeError(http.StatusRequestEntityTooLarge, errcodes.InputRejected, "artifact exceeds the size bound"))
		return
	}
	if len(data) == 0 {
		writeError(response, newSafeError(http.StatusUnprocessableEntity, errcodes.InputRejected, "artifact is empty"))
		return
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if expected != "" && expected != digest {
		writeError(response, newSafeError(http.StatusUnprocessableEntity, errcodes.InputHashMismatch, "artifact hash mismatch"))
		return
	}
	s.broker.PutInput(artifactID, data)
	writeJSON(response, http.StatusOK, map[string]any{"artifact_id": artifactID, "sha256": digest, "byte_count": len(data)})
}

// handleJobOutput streams a finished job's signed output from the broker. It
// requires the job to have recorded an output hash and the stored bytes to still
// match it; the caller (FastAPI) persists the bytes into its own store.
func (s *Server) handleJobOutput(response http.ResponseWriter, request *http.Request) {
	if s.jobs == nil || s.broker == nil {
		writeError(response, newSafeError(http.StatusServiceUnavailable, errcodes.ServiceUnavailable, "output store unavailable"))
		return
	}
	job, err := s.jobs.Get(request.Context(), request.PathValue("job_id"))
	if err != nil {
		writeError(response, newSafeError(http.StatusNotFound, errcodes.InvalidRequest, "job not found"))
		return
	}
	if job.OutputSHA256 == "" {
		writeError(response, newSafeError(http.StatusConflict, errcodes.InvalidRequest, "no output available"))
		return
	}
	data, ok := s.broker.Output(job.Output.ArtifactID)
	if !ok {
		writeError(response, newSafeError(http.StatusNotFound, errcodes.InvalidRequest, "output not found"))
		return
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != job.OutputSHA256 {
		writeError(response, newSafeError(http.StatusConflict, errcodes.OutputStoreFailed, "output hash mismatch"))
		return
	}
	response.Header().Set("Content-Type", "application/pdf")
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set(headerOutputSHA256, job.OutputSHA256)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(data)
}
