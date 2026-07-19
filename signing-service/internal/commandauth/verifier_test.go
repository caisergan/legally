package commandauth

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

func signLikeFastAPI(t *testing.T, priv ed25519.PrivateKey, kid, method, path, query string, body []byte, ts, nonce string) http.Header {
	t.Helper()
	message := strings.Join([]string{
		strings.ToUpper(method), path, query,
		hexSum(body), ts, nonce,
	}, "\n")
	sig := ed25519.Sign(priv, []byte(message))
	header := http.Header{}
	header.Set(HeaderKeyID, kid)
	header.Set(HeaderTimestamp, ts)
	header.Set(HeaderNonce, nonce)
	header.Set(HeaderValue, base64.StdEncoding.EncodeToString(sig))
	return header
}

func hexSum(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func newTestVerifier(t *testing.T) (*Verifier, ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	kid := "fastapi-cmd-1"
	return NewVerifier(map[string]ed25519.PublicKey{kid: pub}, 30*time.Second), priv, kid
}

func TestVerifyRoundTrip(t *testing.T) {
	verifier, priv, kid := newTestVerifier(t)
	body := []byte(`{"command_id":"abc"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	header := signLikeFastAPI(t, priv, kid, "POST", "/v1/jobs", "", body, ts, "nonce-1")
	if err := verifier.Verify("POST", "/v1/jobs", "", body, header); err != nil {
		t.Fatalf("valid command rejected: %v", err)
	}
}

func TestVerifyParsePinnedKeys(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	spec := "fastapi-cmd-1:" + hex.EncodeToString(pub)
	keys, err := ParsePinnedKeys(spec)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := keys["fastapi-cmd-1"]; !ok || len(keys) != 1 {
		t.Fatalf("expected one pinned key, got %v", keys)
	}
	if _, err := ParsePinnedKeys("bad-entry-without-colon"); err == nil {
		t.Fatal("expected error for malformed entry")
	}
	if _, err := ParsePinnedKeys("kid:zz"); err == nil {
		t.Fatal("expected error for non-hex key")
	}
}

func TestVerifyRejections(t *testing.T) {
	verifier, priv, kid := newTestVerifier(t)
	body := []byte("payload")
	now := strconv.FormatInt(time.Now().Unix(), 10)

	t.Run("unknown kid", func(t *testing.T) {
		header := signLikeFastAPI(t, priv, "other", "GET", "/v1/jobs/x", "", body, now, "n1")
		if err := verifier.Verify("GET", "/v1/jobs/x", "", body, header); err != ErrKeyUnknown {
			t.Fatalf("got %v, want ErrKeyUnknown", err)
		}
	})
	t.Run("missing headers", func(t *testing.T) {
		if err := verifier.Verify("GET", "/v1/jobs/x", "", body, http.Header{}); err != ErrHeaderMissing {
			t.Fatalf("got %v, want ErrHeaderMissing", err)
		}
	})
	t.Run("skew", func(t *testing.T) {
		old := strconv.FormatInt(time.Now().Add(-5*time.Minute).Unix(), 10)
		header := signLikeFastAPI(t, priv, kid, "GET", "/v1/jobs/x", "", body, old, "n2")
		if err := verifier.Verify("GET", "/v1/jobs/x", "", body, header); err != ErrSkew {
			t.Fatalf("got %v, want ErrSkew", err)
		}
	})
	t.Run("bad timestamp", func(t *testing.T) {
		header := signLikeFastAPI(t, priv, kid, "GET", "/v1/jobs/x", "", body, "not-a-number", "n3")
		if err := verifier.Verify("GET", "/v1/jobs/x", "", body, header); err != ErrTimestamp {
			t.Fatalf("got %v, want ErrTimestamp", err)
		}
	})
	t.Run("tampered body", func(t *testing.T) {
		header := signLikeFastAPI(t, priv, kid, "POST", "/v1/jobs", "", body, now, "n4")
		if err := verifier.Verify("POST", "/v1/jobs", "", []byte("different"), header); err != ErrSignatureBad {
			t.Fatalf("got %v, want ErrSignatureBad", err)
		}
	})
	t.Run("wrong path", func(t *testing.T) {
		header := signLikeFastAPI(t, priv, kid, "POST", "/v1/jobs", "", body, now, "n5")
		if err := verifier.Verify("POST", "/v1/jobs/evil", "", body, header); err != ErrSignatureBad {
			t.Fatalf("got %v, want ErrSignatureBad", err)
		}
	})
	t.Run("nonce replay", func(t *testing.T) {
		header := signLikeFastAPI(t, priv, kid, "GET", "/v1/jobs/x", "after=0", body, now, "n6")
		if err := verifier.Verify("GET", "/v1/jobs/x", "after=0", body, header); err != nil {
			t.Fatalf("first use rejected: %v", err)
		}
		if err := verifier.Verify("GET", "/v1/jobs/x", "after=0", body, header); err != ErrNonceReplayed {
			t.Fatalf("got %v, want ErrNonceReplayed", err)
		}
	})
}
