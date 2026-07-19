package artifacts

import (
	"errors"
	"testing"
	"time"
)

func TestMintReadRequiresMatchingHashAndSize(t *testing.T) {
	broker := NewBroker(time.Minute)
	hash := broker.PutInput("in-1", []byte("hello world"))

	if _, err := broker.MintRead("job-1", "in-1", hash, 11); err != nil {
		t.Fatalf("valid read mint rejected: %v", err)
	}
	if _, err := broker.MintRead("job-1", "in-1", hash, 99); !errors.Is(err, ErrArtifactMismatch) {
		t.Fatalf("size mismatch = %v, want ErrArtifactMismatch", err)
	}
	if _, err := broker.MintRead("job-1", "in-1", "00", 11); !errors.Is(err, ErrArtifactMismatch) {
		t.Fatalf("hash mismatch = %v, want ErrArtifactMismatch", err)
	}
	if _, err := broker.MintRead("job-1", "missing", hash, 11); !errors.Is(err, ErrArtifactNotFound) {
		t.Fatalf("missing artifact = %v, want ErrArtifactNotFound", err)
	}
}

func TestFetchIsOneUse(t *testing.T) {
	broker := NewBroker(time.Minute)
	hash := broker.PutInput("in-1", []byte("hello world"))
	capability, _ := broker.MintRead("job-1", "in-1", hash, 11)

	data, err := broker.Fetch(capability)
	if err != nil || string(data) != "hello world" {
		t.Fatalf("first fetch = %q, %v", data, err)
	}
	if _, err := broker.Fetch(capability); !errors.Is(err, ErrCapabilityInvalid) {
		t.Fatalf("reuse = %v, want ErrCapabilityInvalid", err)
	}
}

func TestCapabilityMethodAndTokenAreEnforced(t *testing.T) {
	broker := NewBroker(time.Minute)
	hash := broker.PutInput("in-1", []byte("hello world"))
	read, _ := broker.MintRead("job-1", "in-1", hash, 11)

	if _, _, err := broker.Store(read, []byte("x")); !errors.Is(err, ErrMethodMismatch) {
		t.Fatalf("read cap used for store = %v, want ErrMethodMismatch", err)
	}
	forged := read
	forged.Token = "tampered"
	if _, err := broker.Fetch(forged); !errors.Is(err, ErrCapabilityInvalid) {
		t.Fatalf("forged token = %v, want ErrCapabilityInvalid", err)
	}
}

func TestCapabilityExpiry(t *testing.T) {
	broker := NewBroker(time.Minute)
	hash := broker.PutInput("in-1", []byte("hello world"))
	capability, _ := broker.MintRead("job-1", "in-1", hash, 11)
	broker.now = func() time.Time { return time.Now().Add(2 * time.Minute) }
	if _, err := broker.Fetch(capability); !errors.Is(err, ErrCapabilityExpired) {
		t.Fatalf("expired = %v, want ErrCapabilityExpired", err)
	}
}

func TestStoreEnforcesSizeBound(t *testing.T) {
	broker := NewBroker(time.Minute)
	capability := broker.MintWrite("job-1", "out-1", 4)
	if _, _, err := broker.Store(capability, []byte("toolong")); !errors.Is(err, ErrSizeOverflow) {
		t.Fatalf("overflow = %v, want ErrSizeOverflow", err)
	}
	capability = broker.MintWrite("job-1", "out-1", 16)
	hash, size, err := broker.Store(capability, []byte("ok"))
	if err != nil || size != 2 || hash == "" {
		t.Fatalf("store = %q, %d, %v", hash, size, err)
	}
	if data, ok := broker.Output("out-1"); !ok || string(data) != "ok" {
		t.Fatalf("output not stored: %q %v", data, ok)
	}
}
