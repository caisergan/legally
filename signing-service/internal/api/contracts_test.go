package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/config"
	"github.com/caisergan/legally/signing-service/internal/credentials"
)

type fakeInventory struct{ items []credentials.InventoryItem }

func (f fakeInventory) Inventory(context.Context) ([]credentials.InventoryItem, error) {
	return f.items, nil
}

func TestDecodeStrictRejectsUnknownFields(t *testing.T) {
	var request CreateJobRequest
	body := `{"command_id":"c","job_id":"j","surprise":true}`
	if err := decodeStrict(strings.NewReader(body), &request); err == nil {
		t.Fatal("unknown field was accepted")
	}
}

func TestDecodeStrictRejectsTrailingContent(t *testing.T) {
	var request AuthorizeRequest
	body := `{"challenge_id":"c","pin_jwe":"x"}{"extra":1}`
	if err := decodeStrict(strings.NewReader(body), &request); err == nil {
		t.Fatal("trailing content was accepted")
	}
}

func TestDecodeStrictAcceptsWellFormedRequest(t *testing.T) {
	var request CreateJobRequest
	body := `{"command_id":"c","job_id":"j","request_id":"r","expected_request_version":1,
		"credential_id":"cred","certificate_fingerprint_sha256":"ff","policy_version":"v",
		"profile":"PAdES_BASELINE_B_B","algorithm":"RSA_PKCS1_SHA256",
		"input":{"artifact_id":"in","byte_count":10,"sha256":"ab"},
		"output":{"artifact_id":"out","max_byte_count":20},
		"approval":{"approval_id":"a","approved_at":"2026-07-19T12:00:00Z","expires_at":"2026-07-19T12:05:00Z"},
		"nonce":"n","expires_at":"2026-07-19T12:05:00Z"}`
	if err := decodeStrict(strings.NewReader(body), &request); err != nil {
		t.Fatalf("well-formed request rejected: %v", err)
	}
	if request.Input.ByteCount != 10 || request.Output.MaxByteCount != 20 {
		t.Fatalf("decoded fields wrong: %+v", request)
	}
}

func TestHealthReportsReadiness(t *testing.T) {
	server := NewServer(config.Config{Mode: config.ModeSoftHSM, Version: "v"})
	server.SetReadiness(func() Readiness { return Readiness{Ready: true, Database: "ok"} })

	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)

	var body HealthResponse
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Ready || body.Database != "ok" {
		t.Fatalf("unexpected readiness: %+v", body)
	}
}

func TestCredentialsEndpointReturnsOnlySafeFields(t *testing.T) {
	server := NewServer(config.Config{Mode: config.ModeSoftHSM})
	probed := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	server.SetCredentialInventory(fakeInventory{items: []credentials.InventoryItem{{
		ID:                           "cred-1",
		CertificateFingerprintSHA256: "ff00",
		SubjectDisplay:               "E*** A***",
		IssuerDisplay:                "Test CA",
		SerialSuffix:                 "A1B2",
		NotBefore:                    probed,
		NotAfter:                     probed.Add(24 * time.Hour),
		PublicKeyType:                "RSA",
		PublicKeyBits:                2048,
		Status:                       "available",
		LastCheckedAt:                &probed,
	}}})

	request := httptest.NewRequest(http.MethodGet, "/v1/credentials", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}

	raw, _ := io.ReadAll(response.Body)
	for _, forbidden := range []string{`"module`, `"slot`, `"token_serial"`, `"token_label"`, `"key_ckaid"`, `"physical_token_queue_key"`, `"certificate_der"`} {
		if strings.Contains(string(raw), forbidden) {
			t.Fatalf("credential inventory leaked forbidden key %q: %s", forbidden, raw)
		}
	}

	var body CredentialsResponse
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].Mode != "softhsm" || body.Items[0].SubjectDisplay != "E*** A***" {
		t.Fatalf("unexpected inventory: %+v", body.Items)
	}
}

func TestCredentialsEndpointUnavailableWhenUnwired(t *testing.T) {
	server := NewServer(config.Config{Mode: config.ModeSoftHSM})
	request := httptest.NewRequest(http.MethodGet, "/v1/credentials", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.Code)
	}
}
