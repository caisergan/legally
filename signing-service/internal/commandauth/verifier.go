// Package commandauth verifies the asymmetric command authentication FastAPI
// attaches to every signer request: an Ed25519 signature over a fixed six-field
// canonical string, carried in X-Sig-* headers, plus a skew bound and a one-use
// nonce. The scheme mirrors the FastAPI CommandSigner byte-for-byte so the two
// sides interoperate. It is one of three independent controls (with the private
// socket and peer credentials); it never sees a PIN or document bytes.
package commandauth

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	HeaderKeyID     = "X-Sig-KeyId"
	HeaderTimestamp = "X-Sig-Timestamp"
	HeaderNonce     = "X-Sig-Nonce"
	HeaderValue     = "X-Sig-Value"
)

var (
	ErrKeyUnknown    = errors.New("command key id is not pinned")
	ErrTimestamp     = errors.New("command timestamp is malformed")
	ErrSkew          = errors.New("command timestamp is outside the allowed skew")
	ErrSignatureForm = errors.New("command signature is not valid base64")
	ErrSignatureBad  = errors.New("command signature verification failed")
	ErrNonceReplayed = errors.New("command nonce was already used")
	ErrHeaderMissing = errors.New("command authentication headers are missing")
)

// ParsePinnedKeys parses a "kid:hex,kid:hex" specification of raw 32-byte
// Ed25519 public keys, matching the FastAPI pinned-key format.
func ParsePinnedKeys(spec string) (map[string]ed25519.PublicKey, error) {
	keys := map[string]ed25519.PublicKey{}
	for _, entry := range strings.Split(spec, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		kid, hexKey, ok := strings.Cut(entry, ":")
		kid, hexKey = strings.TrimSpace(kid), strings.TrimSpace(hexKey)
		if !ok || kid == "" || hexKey == "" {
			return nil, fmt.Errorf("invalid pinned key entry %q", entry)
		}
		raw, err := hex.DecodeString(hexKey)
		if err != nil {
			return nil, fmt.Errorf("invalid pinned key hex for %q: %w", kid, err)
		}
		if len(raw) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("pinned key %q must be %d bytes, got %d", kid, ed25519.PublicKeySize, len(raw))
		}
		keys[kid] = ed25519.PublicKey(raw)
	}
	return keys, nil
}

// Verifier authenticates signed commands against a pinned key set.
type Verifier struct {
	keys   map[string]ed25519.PublicKey
	skew   time.Duration
	now    func() time.Time
	nonces *nonceCache
}

func NewVerifier(keys map[string]ed25519.PublicKey, skew time.Duration) *Verifier {
	return &Verifier{
		keys:   keys,
		skew:   skew,
		now:    func() time.Time { return time.Now().UTC() },
		nonces: newNonceCache(),
	}
}

// canonicalRequest builds the exact bytes FastAPI signs: the method, path, query
// (or empty), lowercase-hex body SHA-256, timestamp, and nonce joined by "\n".
func canonicalRequest(method, path, query, timestamp, nonce string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(strings.Join([]string{
		strings.ToUpper(method),
		path,
		query,
		hex.EncodeToString(sum[:]),
		timestamp,
		nonce,
	}, "\n"))
}

// Verify authenticates one request. It enforces, in order: a pinned key id, a
// parseable timestamp within skew, a decodable signature, a valid signature, and
// a fresh nonce consumed exactly once only after every other check passes.
func (v *Verifier) Verify(method, path, query string, body []byte, header http.Header) error {
	kid := header.Get(HeaderKeyID)
	timestamp := header.Get(HeaderTimestamp)
	nonce := header.Get(HeaderNonce)
	value := header.Get(HeaderValue)
	if kid == "" || timestamp == "" || nonce == "" || value == "" {
		return ErrHeaderMissing
	}

	publicKey, ok := v.keys[kid]
	if !ok {
		return ErrKeyUnknown
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrTimestamp
	}
	delta := v.now().Unix() - seconds
	if delta < 0 {
		delta = -delta
	}
	if time.Duration(delta)*time.Second > v.skew {
		return ErrSkew
	}
	signature, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return ErrSignatureForm
	}
	if !ed25519.Verify(publicKey, canonicalRequest(method, path, query, timestamp, nonce, body), signature) {
		return ErrSignatureBad
	}
	if !v.nonces.consume(nonce, v.now(), v.skew) {
		return ErrNonceReplayed
	}
	return nil
}

type nonceCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func newNonceCache() *nonceCache {
	return &nonceCache{seen: map[string]time.Time{}}
}

// consume records a nonce once; it returns false if the nonce was already used.
// Entries older than twice the skew are pruned so the cache cannot grow without
// bound (a stale nonce would already fail the skew check).
func (c *nonceCache) consume(nonce string, now time.Time, skew time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	horizon := now.Add(-2 * skew)
	for key, when := range c.seen {
		if when.Before(horizon) {
			delete(c.seen, key)
		}
	}
	if _, used := c.seen[nonce]; used {
		return false
	}
	c.seen[nonce] = now
	return true
}
