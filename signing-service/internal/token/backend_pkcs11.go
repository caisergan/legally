//go:build pkcs11

package token

import (
	"fmt"
	"strings"

	"github.com/miekg/pkcs11"
)

// sha256DigestInfoPrefix is the DER DigestInfo header for SHA-256, prepended
// before CKM_RSA_PKCS so the signature matches rsa.VerifyPKCS1v15(SHA256).
var sha256DigestInfoPrefix = []byte{
	0x30, 0x31, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48,
	0x01, 0x65, 0x03, 0x04, 0x02, 0x01, 0x05, 0x00, 0x04, 0x20,
}

type pkcs11Backend struct {
	ctx *pkcs11.Ctx
}

// NewBackend loads and initializes an already-digest-verified PKCS#11 module.
func NewBackend(modulePath string) (Backend, error) {
	ctx := pkcs11.New(modulePath)
	if ctx == nil {
		return nil, fmt.Errorf("load pkcs11 module")
	}
	if err := ctx.Initialize(); err != nil {
		ctx.Destroy()
		return nil, fmt.Errorf("initialize pkcs11 module: %w", err)
	}
	return &pkcs11Backend{ctx: ctx}, nil
}

func (b *pkcs11Backend) Slots() ([]SlotInfo, error) {
	slots, err := b.ctx.GetSlotList(true)
	if err != nil {
		return nil, err
	}
	out := make([]SlotInfo, 0, len(slots))
	for _, slot := range slots {
		info, err := b.ctx.GetTokenInfo(slot)
		if err != nil {
			continue
		}
		out = append(out, SlotInfo{
			ID:          slot,
			TokenSerial: strings.TrimSpace(info.SerialNumber),
			TokenLabel:  strings.TrimSpace(info.Label),
		})
	}
	return out, nil
}

func (b *pkcs11Backend) Objects(slotID uint) (SlotObjects, error) {
	session, err := b.ctx.OpenSession(slotID, pkcs11.CKF_SERIAL_SESSION)
	if err != nil {
		return SlotObjects{}, err
	}
	defer b.ctx.CloseSession(session)

	var objects SlotObjects
	certHandles, err := b.findObjects(session, []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_CERTIFICATE),
	})
	if err != nil {
		return SlotObjects{}, err
	}
	for _, handle := range certHandles {
		attrs, err := b.ctx.GetAttributeValue(session, handle, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_ID, nil),
			pkcs11.NewAttribute(pkcs11.CKA_VALUE, nil),
		})
		if err != nil {
			continue
		}
		var cert CertObject
		for _, attr := range attrs {
			switch attr.Type {
			case pkcs11.CKA_ID:
				cert.CKAID = append([]byte(nil), attr.Value...)
			case pkcs11.CKA_VALUE:
				cert.DER = append([]byte(nil), attr.Value...)
			}
		}
		if len(cert.DER) > 0 {
			objects.Certificates = append(objects.Certificates, cert)
		}
	}

	keyHandles, err := b.findObjects(session, []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PRIVATE_KEY),
	})
	if err != nil {
		return SlotObjects{}, err
	}
	for _, handle := range keyHandles {
		key := KeyObject{KeyType: "RSA"}
		if attrs, err := b.ctx.GetAttributeValue(session, handle, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_ID, nil),
		}); err == nil {
			for _, attr := range attrs {
				if attr.Type == pkcs11.CKA_ID {
					key.CKAID = append([]byte(nil), attr.Value...)
				}
			}
		}
		if pub, err := b.ctx.GetAttributeValue(session, handle, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_MODULUS, nil),
			pkcs11.NewAttribute(pkcs11.CKA_PUBLIC_EXPONENT, nil),
		}); err == nil {
			for _, attr := range pub {
				switch attr.Type {
				case pkcs11.CKA_MODULUS:
					key.RSAModulus = append([]byte(nil), attr.Value...)
				case pkcs11.CKA_PUBLIC_EXPONENT:
					key.RSAExponent = append([]byte(nil), attr.Value...)
				}
			}
		}
		objects.PrivateKeys = append(objects.PrivateKeys, key)
	}

	pubHandles, err := b.findObjects(session, []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PUBLIC_KEY),
	})
	if err != nil {
		return SlotObjects{}, err
	}
	for _, handle := range pubHandles {
		key := KeyObject{KeyType: "RSA"}
		attrs, err := b.ctx.GetAttributeValue(session, handle, []*pkcs11.Attribute{
			pkcs11.NewAttribute(pkcs11.CKA_ID, nil),
			pkcs11.NewAttribute(pkcs11.CKA_MODULUS, nil),
			pkcs11.NewAttribute(pkcs11.CKA_PUBLIC_EXPONENT, nil),
		})
		if err != nil {
			continue
		}
		for _, attr := range attrs {
			switch attr.Type {
			case pkcs11.CKA_ID:
				key.CKAID = append([]byte(nil), attr.Value...)
			case pkcs11.CKA_MODULUS:
				key.RSAModulus = append([]byte(nil), attr.Value...)
			case pkcs11.CKA_PUBLIC_EXPONENT:
				key.RSAExponent = append([]byte(nil), attr.Value...)
			}
		}
		objects.PublicKeys = append(objects.PublicKeys, key)
	}
	return objects, nil
}

func (b *pkcs11Backend) Sign(slotID uint, keyCKAID []byte, pin string, digest []byte) ([]byte, error) {
	session, err := b.ctx.OpenSession(slotID, pkcs11.CKF_SERIAL_SESSION)
	if err != nil {
		return nil, err
	}
	defer b.ctx.CloseSession(session)
	if err := b.ctx.Login(session, pkcs11.CKU_USER, pin); err != nil {
		return nil, err
	}
	defer b.ctx.Logout(session)

	handles, err := b.findObjects(session, []*pkcs11.Attribute{
		pkcs11.NewAttribute(pkcs11.CKA_CLASS, pkcs11.CKO_PRIVATE_KEY),
		pkcs11.NewAttribute(pkcs11.CKA_ID, keyCKAID),
	})
	if err != nil {
		return nil, err
	}
	if len(handles) == 0 {
		return nil, ErrKeyNotFound
	}
	if len(handles) > 1 {
		return nil, ErrAmbiguousKey
	}
	mechanism := []*pkcs11.Mechanism{pkcs11.NewMechanism(pkcs11.CKM_RSA_PKCS, nil)}
	if err := b.ctx.SignInit(session, mechanism, handles[0]); err != nil {
		return nil, err
	}
	payload := append(append([]byte(nil), sha256DigestInfoPrefix...), digest...)
	return b.ctx.Sign(session, payload)
}

func (b *pkcs11Backend) findObjects(session pkcs11.SessionHandle, template []*pkcs11.Attribute) ([]pkcs11.ObjectHandle, error) {
	if err := b.ctx.FindObjectsInit(session, template); err != nil {
		return nil, err
	}
	var all []pkcs11.ObjectHandle
	for {
		handles, _, err := b.ctx.FindObjects(session, 32)
		if err != nil {
			b.ctx.FindObjectsFinal(session)
			return nil, err
		}
		if len(handles) == 0 {
			break
		}
		all = append(all, handles...)
	}
	if err := b.ctx.FindObjectsFinal(session); err != nil {
		return nil, err
	}
	return all, nil
}

func (b *pkcs11Backend) Close() error {
	if b.ctx == nil {
		return nil
	}
	_ = b.ctx.Finalize()
	b.ctx.Destroy()
	b.ctx = nil
	return nil
}
