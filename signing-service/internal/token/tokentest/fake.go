// Package tokentest provides an in-memory PKCS#11 Backend and certificate
// generator so exact-selection and enrollment logic can be tested without
// hardware or a real PKCS#11 module.
package tokentest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"sort"
	"time"

	"github.com/caisergan/legally/signing-service/internal/token"
)

// ErrLoginFailed simulates a rejected PKCS#11 login.
var ErrLoginFailed = errors.New("tokentest: login failed")

// Identity is a generated certificate and matching RSA private key.
type Identity struct {
	CertDER     []byte
	Fingerprint [32]byte
	CKAID       []byte
	Priv        *rsa.PrivateKey
	NotBefore   time.Time
	NotAfter    time.Time
}

// NewIdentity generates a self-signed RSA identity for tests.
func NewIdentity(subject, ckaID string, notBefore, notAfter time.Time) (Identity, error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return Identity{}, err
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: subject},
		Issuer:       pkix.Name{CommonName: "tokentest CA"},
		NotBefore:    notBefore,
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return Identity{}, err
	}
	return Identity{
		CertDER:     der,
		Fingerprint: sha256.Sum256(der),
		CKAID:       []byte(ckaID),
		Priv:        priv,
		NotBefore:   notBefore,
		NotAfter:    notAfter,
	}, nil
}

type fakeKey struct {
	obj    token.KeyObject
	priv   *rsa.PrivateKey
	hidden bool // simulates CKA_PRIVATE: signable, but not listed pre-login
}

type fakeSlot struct {
	info    token.SlotInfo
	certs   []token.CertObject
	keys    []fakeKey
	pubKeys []token.KeyObject
}

// FakeBackend is an in-memory token.Backend.
type FakeBackend struct {
	pin   string
	order []uint
	slots map[uint]*fakeSlot
}

// New returns a fake backend whose Sign accepts only the given PIN.
func New(pin string) *FakeBackend {
	return &FakeBackend{pin: pin, slots: map[uint]*fakeSlot{}}
}

func (f *FakeBackend) AddSlot(id uint, serial, label string) *FakeBackend {
	if _, ok := f.slots[id]; !ok {
		f.order = append(f.order, id)
	}
	f.slots[id] = &fakeSlot{info: token.SlotInfo{ID: id, TokenSerial: serial, TokenLabel: label}}
	return f
}

// AddCredential adds a matching cert+key pair. When exposePublic is true, the
// key object carries public attributes so proof needs no PIN.
func (f *FakeBackend) AddCredential(slotID uint, id Identity, exposePublic bool) *FakeBackend {
	f.AddCert(slotID, id.CKAID, id.CertDER)
	key := token.KeyObject{CKAID: id.CKAID, KeyType: "RSA"}
	if exposePublic {
		key.RSAModulus = id.Priv.N.Bytes()
		key.RSAExponent = big.NewInt(int64(id.Priv.E)).Bytes()
	}
	return f.AddKey(slotID, key, id.Priv)
}

// AddHiddenCredential adds a cert plus a private key that signs but is hidden
// from Objects (as a CKA_PRIVATE key is pre-login) and a visible public-key
// object carrying the RSA public attributes, matching a realistic token.
func (f *FakeBackend) AddHiddenCredential(slotID uint, id Identity) *FakeBackend {
	f.AddCert(slotID, id.CKAID, id.CertDER)
	slot := f.slots[slotID]
	slot.keys = append(slot.keys, fakeKey{obj: token.KeyObject{CKAID: id.CKAID, KeyType: "RSA"}, priv: id.Priv, hidden: true})
	slot.pubKeys = append(slot.pubKeys, token.KeyObject{
		CKAID:       id.CKAID,
		KeyType:     "RSA",
		RSAModulus:  id.Priv.N.Bytes(),
		RSAExponent: big.NewInt(int64(id.Priv.E)).Bytes(),
	})
	return f
}

func (f *FakeBackend) AddCert(slotID uint, ckaID, der []byte) *FakeBackend {
	slot := f.slots[slotID]
	slot.certs = append(slot.certs, token.CertObject{CKAID: ckaID, DER: der})
	return f
}

func (f *FakeBackend) AddKey(slotID uint, key token.KeyObject, priv *rsa.PrivateKey) *FakeBackend {
	slot := f.slots[slotID]
	slot.keys = append(slot.keys, fakeKey{obj: key, priv: priv})
	return f
}

func (f *FakeBackend) Slots() ([]token.SlotInfo, error) {
	ids := append([]uint(nil), f.order...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := make([]token.SlotInfo, 0, len(ids))
	for _, id := range ids {
		out = append(out, f.slots[id].info)
	}
	return out, nil
}

func (f *FakeBackend) Objects(slotID uint) (token.SlotObjects, error) {
	slot, ok := f.slots[slotID]
	if !ok {
		return token.SlotObjects{}, errors.New("tokentest: slot not present")
	}
	objects := token.SlotObjects{Certificates: append([]token.CertObject(nil), slot.certs...)}
	for _, key := range slot.keys {
		if key.hidden {
			continue // private object not visible without login
		}
		objects.PrivateKeys = append(objects.PrivateKeys, key.obj)
	}
	objects.PublicKeys = append(objects.PublicKeys, slot.pubKeys...)
	return objects, nil
}

func (f *FakeBackend) Sign(slotID uint, keyCKAID []byte, pin string, digest []byte) ([]byte, error) {
	if pin != f.pin {
		return nil, ErrLoginFailed
	}
	slot, ok := f.slots[slotID]
	if !ok {
		return nil, errors.New("tokentest: slot not present")
	}
	for _, key := range slot.keys {
		if string(key.obj.CKAID) == string(keyCKAID) {
			return rsa.SignPKCS1v15(rand.Reader, key.priv, crypto.SHA256, digest)
		}
	}
	return nil, errors.New("tokentest: key not found")
}

func (f *FakeBackend) Close() error { return nil }
