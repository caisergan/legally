package api

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/internal/artifacts"
	"github.com/caisergan/legally/signing-service/internal/commandauth"
	"github.com/caisergan/legally/signing-service/internal/config"
)

func signedRequest(priv ed25519.PrivateKey, kid, method, path, query string, body []byte) *http.Request {
	url := path
	if query != "" {
		url += "?" + query
	}
	request := httptest.NewRequest(method, url, bytes.NewReader(body))
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce := hex.EncodeToString([]byte(ts + method + path))
	sum := sha256.Sum256(body)
	message := strings.Join([]string{strings.ToUpper(method), path, query, hex.EncodeToString(sum[:]), ts, nonce}, "\n")
	request.Header.Set(commandauth.HeaderKeyID, kid)
	request.Header.Set(commandauth.HeaderTimestamp, ts)
	request.Header.Set(commandauth.HeaderNonce, nonce)
	request.Header.Set(commandauth.HeaderValue, base64.StdEncoding.EncodeToString(ed25519.Sign(priv, []byte(message))))
	return request
}

func brokerServer(t *testing.T) (*Server, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	kid := "fastapi-cmd-1"
	server := NewServer(config.Config{Version: "test"})
	server.SetArtifactBroker(artifacts.NewBroker(time.Minute), 1<<20)
	server.SetCommandVerifier(commandauth.NewVerifier(map[string]ed25519.PublicKey{kid: pub}, 30*time.Second))
	return server, priv, kid
}

func TestArtifactPutStoresAuthenticatedBytes(t *testing.T) {
	server, priv, kid := brokerServer(t)
	data := []byte("%PDF-1.7 test artifact")
	sum := sha256.Sum256(data)
	request := signedRequest(priv, kid, http.MethodPut, "/v1/artifacts/art-1", "", data)
	request.Header.Set(headerArtifactSHA256, hex.EncodeToString(sum[:]))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
	}
	stored, ok := server.broker.Output("art-1")
	if !ok || !bytes.Equal(stored, data) {
		t.Fatalf("broker did not store the artifact bytes")
	}
}

func TestArtifactPutRejectsUnsigned(t *testing.T) {
	server, _, _ := brokerServer(t)
	request := httptest.NewRequest(http.MethodPut, "/v1/artifacts/art-1", bytes.NewReader([]byte("data")))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned request status = %d, want 401", response.Code)
	}
}

func TestArtifactPutRejectsHashMismatch(t *testing.T) {
	server, priv, kid := brokerServer(t)
	data := []byte("real bytes")
	request := signedRequest(priv, kid, http.MethodPut, "/v1/artifacts/art-2", "", data)
	request.Header.Set(headerArtifactSHA256, hex.EncodeToString(make([]byte, 32)))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("hash mismatch status = %d, want 422", response.Code)
	}
	if _, ok := server.broker.Output("art-2"); ok {
		t.Fatal("mismatched artifact must not be stored")
	}
}

func TestHealthStaysOpenUnderCommandAuth(t *testing.T) {
	server, _, _ := brokerServer(t)
	request := httptest.NewRequest(http.MethodGet, "/v1/health", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("health under command auth status = %d, want 200", response.Code)
	}
}

func TestJobOutputRequiresBrokerAndJobs(t *testing.T) {
	server := NewServer(config.Config{Version: "test"})
	request := httptest.NewRequest(http.MethodGet, "/v1/jobs/job-1/output", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 when output store unavailable", response.Code)
	}
}
