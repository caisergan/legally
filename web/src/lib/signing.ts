// Typed client for the /api/signing routes. State-changing calls carry the
// double-submit CSRF header read from the (non-httponly) ya_csrf cookie, which
// the capabilities call seeds; the browser adds the same-origin Origin header.
import { apiDownload, apiFetch, withQuery } from "./api";

export interface SigningCapabilities {
  enabled: boolean;
  mode: string;
  test_only: boolean;
  service_available: boolean;
  profile: string;
  algorithm: string;
  policy_version: string;
  max_upload_bytes: number;
  pin_window_seconds: number;
}

export interface SigningCertificate {
  id: string;
  subject_display: string;
  issuer_display: string;
  serial_suffix: string;
  fingerprint_suffix: string;
  not_before: string | null;
  not_after: string | null;
  public_key_type: string;
  status: string;
  test_only: boolean;
}

export interface SigningArtifact {
  id: string;
  display_filename: string;
  mime_type: string;
  byte_count: number;
  sha256: string;
  preflight_status: string;
  created_at: string;
  expires_at: string | null;
}

export interface SigningRequest {
  id: string;
  state: string;
  state_label: string;
  profile: string;
  algorithm: string;
  policy_version: string;
  certificate_id: string;
  certificate_fingerprint_sha256: string;
  input_sha256: string;
  input_byte_count: number;
  output_sha256: string | null;
  output_byte_count: number | null;
  failure_code: string | null;
  cancellable: boolean;
  created_at: string;
  queued_at: string | null;
  completed_at: string | null;
}

export interface SigningEvent {
  sequence: number;
  type: string;
  state: string | null;
  state_label: string | null;
  created_at: string;
}

export interface SigningEventsResponse {
  state: string;
  state_label: string;
  last_sequence: number;
  events: SigningEvent[];
}

export interface PinChallenge {
  challenge_id: string;
  challenge_jws: string;
  recipient_jwk: Record<string, unknown>;
  expires_at: string;
}

export interface ChallengeKeyset {
  keys: JsonWebKey[];
}

export interface PinEnvelopeResult {
  authorization_status: string;
}

export interface DownloadAuthorization {
  download_token: string;
  expires_at: string;
}

function csrfToken(): string {
  const match = document.cookie.match(/(?:^|;\s*)ya_csrf=([^;]+)/);
  const value = match?.[1];
  return value ? decodeURIComponent(value) : "";
}

function csrfHeaders(extra?: Record<string, string>): Record<string, string> {
  return { "X-CSRF-Token": csrfToken(), ...(extra ?? {}) };
}

// getCapabilities also seeds the ya_csrf cookie server-side; call it first.
export function getCapabilities(): Promise<SigningCapabilities> {
  return apiFetch<SigningCapabilities>("/api/signing/capabilities");
}

export function getChallengeKeyset(): Promise<ChallengeKeyset> {
  return apiFetch<ChallengeKeyset>("/api/signing/challenge-keyset");
}

export function listCertificates(): Promise<{ items: SigningCertificate[] }> {
  return apiFetch<{ items: SigningCertificate[] }>("/api/signing/certificates");
}

export function uploadArtifact(file: File): Promise<SigningArtifact> {
  const form = new FormData();
  form.append("file", file);
  return apiFetch<SigningArtifact>("/api/signing/artifacts", {
    method: "POST",
    body: form,
    headers: csrfHeaders(),
  });
}

export function deleteArtifact(artifactId: string): Promise<void> {
  return apiFetch<void>(`/api/signing/artifacts/${encodeURIComponent(artifactId)}`, {
    method: "DELETE",
    headers: csrfHeaders(),
  });
}

export function createRequest(artifactId: string, certificateId: string, idempotencyKey: string): Promise<SigningRequest> {
  return apiFetch<SigningRequest>("/api/signing/requests", {
    method: "POST",
    body: JSON.stringify({ artifact_id: artifactId, certificate_id: certificateId }),
    headers: csrfHeaders({ "Idempotency-Key": idempotencyKey }),
  });
}

export function listRequests(limit = 20): Promise<{ items: SigningRequest[] }> {
  return apiFetch<{ items: SigningRequest[] }>(withQuery("/api/signing/requests", { limit }));
}

export function getRequest(requestId: string): Promise<SigningRequest> {
  return apiFetch<SigningRequest>(`/api/signing/requests/${encodeURIComponent(requestId)}`);
}

export function confirmRequest(requestId: string, currentPassword: string): Promise<SigningRequest> {
  return apiFetch<SigningRequest>(`/api/signing/requests/${encodeURIComponent(requestId)}/confirm`, {
    method: "POST",
    body: JSON.stringify({ current_password: currentPassword }),
    headers: csrfHeaders(),
  });
}

export function getEvents(requestId: string, after = 0): Promise<SigningEventsResponse> {
  return apiFetch<SigningEventsResponse>(
    withQuery(`/api/signing/requests/${encodeURIComponent(requestId)}/events`, { after }),
  );
}

export function getPinChallenge(requestId: string): Promise<PinChallenge> {
  return apiFetch<PinChallenge>(`/api/signing/requests/${encodeURIComponent(requestId)}/pin-challenge`);
}

export function sendPinEnvelope(requestId: string, challengeId: string, pinJwe: string): Promise<PinEnvelopeResult> {
  return apiFetch<PinEnvelopeResult>(`/api/signing/requests/${encodeURIComponent(requestId)}/pin-envelope`, {
    method: "POST",
    body: JSON.stringify({ challenge_id: challengeId, pin_jwe: pinJwe }),
    headers: csrfHeaders(),
  });
}

export function cancelRequest(requestId: string): Promise<SigningRequest> {
  return apiFetch<SigningRequest>(`/api/signing/requests/${encodeURIComponent(requestId)}/cancel`, {
    method: "POST",
    headers: csrfHeaders(),
  });
}

export function authorizeDownload(requestId: string, currentPassword: string): Promise<DownloadAuthorization> {
  return apiFetch<DownloadAuthorization>(`/api/signing/requests/${encodeURIComponent(requestId)}/download-authorization`, {
    method: "POST",
    body: JSON.stringify({ current_password: currentPassword }),
    headers: csrfHeaders(),
  });
}

export function downloadSigned(requestId: string, token: string, filename: string): Promise<void> {
  return apiDownload(
    withQuery(`/api/signing/requests/${encodeURIComponent(requestId)}/download`, { token }),
    filename,
  );
}
