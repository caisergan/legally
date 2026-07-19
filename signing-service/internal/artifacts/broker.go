// Package artifacts is the isolated test artifact store and just-in-time
// capability broker (plan §8.3). Capabilities are one-use bearer secrets scoped
// to a method, artifact, job, hash/size, nonce, and expiry. Phase 3A replaces
// this in-process broker with the FastAPI-owned control plane over a socket
// without changing the capability contract.
package artifacts

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sync"
	"time"
)

type Method string

const (
	MethodRead  Method = "GET"
	MethodWrite Method = "PUT"
)

var (
	ErrArtifactNotFound   = errors.New("artifact not found")
	ErrArtifactMismatch   = errors.New("artifact hash or size does not match")
	ErrCapabilityInvalid  = errors.New("capability is invalid or already consumed")
	ErrCapabilityExpired  = errors.New("capability has expired")
	ErrMethodMismatch     = errors.New("capability method mismatch")
	ErrSizeOverflow       = errors.New("payload exceeds the capability size bound")
	ErrOutputHashMismatch = errors.New("stored output hash mismatch")
)

// Capability is a one-use bearer grant for a single transfer.
type Capability struct {
	Token        string
	Method       Method
	ArtifactID   string
	JobID        string
	SHA256       string
	ByteCount    int64
	MaxByteCount int64
	Nonce        string
	ExpiresAt    time.Time
}

type minted struct {
	capability Capability
	consumed   bool
}

// Broker stores artifact bytes privately and mints scoped one-use capabilities.
type Broker struct {
	ttl time.Duration
	now func() time.Time

	mu      sync.Mutex
	objects map[string][]byte
	minted  map[string]*minted
}

func NewBroker(ttl time.Duration) *Broker {
	return &Broker{
		ttl:     ttl,
		now:     func() time.Time { return time.Now().UTC() },
		objects: map[string][]byte{},
		minted:  map[string]*minted{},
	}
}

// PutInput seeds an input artifact (test/broker-owned; not a signer path).
func (b *Broker) PutInput(artifactID string, data []byte) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.objects[artifactID] = append([]byte(nil), data...)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// Output returns stored output bytes (test inspection).
func (b *Broker) Output(artifactID string) ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[artifactID]
	return data, ok
}

// MintRead issues a read capability only after verifying the artifact exists
// and matches the declared hash and size.
func (b *Broker) MintRead(jobID, artifactID, expectedSHA256 string, byteCount int64) (Capability, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	data, ok := b.objects[artifactID]
	if !ok {
		return Capability{}, ErrArtifactNotFound
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != expectedSHA256 || int64(len(data)) != byteCount {
		return Capability{}, ErrArtifactMismatch
	}
	return b.mint(Capability{
		Method: MethodRead, ArtifactID: artifactID, JobID: jobID,
		SHA256: expectedSHA256, ByteCount: byteCount,
	}), nil
}

// MintWrite issues a one-use write capability bounded by maxByteCount.
func (b *Broker) MintWrite(jobID, artifactID string, maxByteCount int64) Capability {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.mint(Capability{
		Method: MethodWrite, ArtifactID: artifactID, JobID: jobID, MaxByteCount: maxByteCount,
	})
}

func (b *Broker) mint(capability Capability) Capability {
	capability.Token = randomToken()
	capability.Nonce = randomToken()
	capability.ExpiresAt = b.now().Add(b.ttl)
	b.minted[capability.Token] = &minted{capability: capability}
	return capability
}

// Fetch validates a read capability, returns the bytes, and consumes the grant.
func (b *Broker) Fetch(capability Capability) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, err := b.consume(capability, MethodRead)
	if err != nil {
		return nil, err
	}
	data, ok := b.objects[stored.ArtifactID]
	if !ok {
		return nil, ErrArtifactNotFound
	}
	return append([]byte(nil), data...), nil
}

// Store validates a write capability, enforces the size bound, writes the
// bytes, consumes the grant, and returns the output hash and size.
func (b *Broker) Store(capability Capability, data []byte) (string, int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	stored, err := b.consume(capability, MethodWrite)
	if err != nil {
		return "", 0, err
	}
	if int64(len(data)) > stored.MaxByteCount {
		return "", 0, ErrSizeOverflow
	}
	b.objects[stored.ArtifactID] = append([]byte(nil), data...)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), int64(len(data)), nil
}

func (b *Broker) consume(presented Capability, method Method) (Capability, error) {
	entry, ok := b.minted[presented.Token]
	if !ok {
		return Capability{}, ErrCapabilityInvalid
	}
	if entry.consumed {
		return Capability{}, ErrCapabilityInvalid
	}
	if entry.capability.Method != method {
		return Capability{}, ErrMethodMismatch
	}
	if b.now().After(entry.capability.ExpiresAt) {
		return Capability{}, ErrCapabilityExpired
	}
	entry.consumed = true
	return entry.capability, nil
}

func randomToken() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
