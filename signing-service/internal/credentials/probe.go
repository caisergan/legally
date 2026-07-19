package credentials

import (
	"context"
	"encoding/hex"

	"github.com/caisergan/legally/signing-service/internal/token"
)

// EnrollParams is the exact selection input for enrollment. The module digest
// must already be verified by the caller (plan §5.1).
type EnrollParams struct {
	ModuleAlias       string
	ModuleSHA256      string
	SlotID            uint
	CertificateSHA256 [32]byte
	KeyCKAID          []byte
}

// Enroll resolves the exact tuple, rejects an out-of-validity certificate,
// proves the certificate and private key form a pair, derives the physical
// token queue key, and persists the credential.
func (r *Registry) Enroll(ctx context.Context, backend token.Backend, params EnrollParams, pinFunc token.PINFunc) (Credential, error) {
	sel, err := token.Resolve(backend, token.Criteria{
		SlotID:            params.SlotID,
		CertificateSHA256: params.CertificateSHA256,
		KeyCKAID:          params.KeyCKAID,
	})
	if err != nil {
		return Credential{}, err
	}
	now := r.now()
	if now.Before(sel.NotBefore) || now.After(sel.NotAfter) {
		return Credential{}, ErrCertificateExpired
	}
	if err := token.VerifyKeyMatch(backend, sel, pinFunc); err != nil {
		return Credential{}, err
	}

	cred := Credential{
		ServiceCredentialID:   NewServiceCredentialID(),
		PhysicalTokenQueueKey: DeriveQueueKey(sel.TokenSerial),
		ModuleAlias:           params.ModuleAlias,
		ModuleSHA256:          params.ModuleSHA256,
		SlotID:                sel.SlotID,
		TokenSerial:           sel.TokenSerial,
		TokenLabel:            sel.TokenLabel,
		CertificateDER:        sel.CertificateDER,
		CertificateSHA256:     hex.EncodeToString(sel.SHA256[:]),
		KeyCKAID:              hex.EncodeToString(sel.KeyCKAID),
		PublicKeyType:         sel.PublicKeyType,
		PublicKeyBits:         sel.PublicKeyBits,
		NotBefore:             sel.NotBefore,
		NotAfter:              sel.NotAfter,
		EnrolledAt:            now,
		Enabled:               true,
		LastHealth:            "ok",
	}
	if err := r.save(ctx, cred); err != nil {
		return Credential{}, err
	}
	return cred, nil
}

// Verify re-probes an enrolled credential: it confirms the exact slot still
// holds the same token serial, certificate, and key, and records the result.
func (r *Registry) Verify(ctx context.Context, id string, backend token.Backend, pinFunc token.PINFunc) error {
	cred, err := r.Get(ctx, id)
	if err != nil {
		return err
	}
	fingerprint, err := hexToArray(cred.CertificateSHA256)
	if err != nil {
		return err
	}
	ckaID, err := hex.DecodeString(cred.KeyCKAID)
	if err != nil {
		return err
	}
	sel, err := token.Resolve(backend, token.Criteria{
		SlotID:              cred.SlotID,
		CertificateSHA256:   fingerprint,
		KeyCKAID:            ckaID,
		ExpectedTokenSerial: cred.TokenSerial,
	})
	if err != nil {
		_ = r.UpdateProbe(ctx, id, string(token.SafeCode(err)), r.now())
		return err
	}
	if err := token.VerifyKeyMatch(backend, sel, pinFunc); err != nil {
		_ = r.UpdateProbe(ctx, id, string(token.SafeCode(err)), r.now())
		return err
	}
	return r.UpdateProbe(ctx, id, "ok", r.now())
}

func hexToArray(value string) ([32]byte, error) {
	var out [32]byte
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return out, err
	}
	copy(out[:], decoded)
	return out, nil
}
