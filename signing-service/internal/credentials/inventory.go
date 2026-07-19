package credentials

import (
	"context"
	"crypto/x509"
	"strings"
	"time"
)

// InventoryItem is safe public credential metadata (plan §8.2). It never
// carries module paths/digests, slot IDs, full token serials, labels, or CKA_ID.
type InventoryItem struct {
	ID                           string
	CertificateFingerprintSHA256 string
	SubjectDisplay               string
	IssuerDisplay                string
	SerialSuffix                 string
	NotBefore                    time.Time
	NotAfter                     time.Time
	PublicKeyType                string
	PublicKeyBits                int
	Status                       string
	LastCheckedAt                *time.Time
}

// Inventory returns safe public metadata for every credential.
func (r *Registry) Inventory(ctx context.Context) ([]InventoryItem, error) {
	creds, err := r.List(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]InventoryItem, 0, len(creds))
	for _, cred := range creds {
		item := InventoryItem{
			ID:                           cred.ServiceCredentialID,
			CertificateFingerprintSHA256: cred.CertificateSHA256,
			NotBefore:                    cred.NotBefore,
			NotAfter:                     cred.NotAfter,
			PublicKeyType:                cred.PublicKeyType,
			PublicKeyBits:                cred.PublicKeyBits,
			Status:                       statusOf(cred.Enabled),
			LastCheckedAt:                cred.LastProbedAt,
		}
		if certificate, parseErr := x509.ParseCertificate(cred.CertificateDER); parseErr == nil {
			item.SubjectDisplay = maskName(certificate.Subject.CommonName)
			item.IssuerDisplay = certificate.Issuer.CommonName
			item.SerialSuffix = serialSuffix(certificate.SerialNumber.Text(16))
		} else {
			item.SubjectDisplay = "***"
		}
		items = append(items, item)
	}
	return items, nil
}

func statusOf(enabled bool) string {
	if enabled {
		return "available"
	}
	return "disabled"
}

// maskName reduces "Ege Ayyildiz" to "E*** A***".
func maskName(name string) string {
	fields := strings.Fields(name)
	if len(fields) == 0 {
		return "***"
	}
	masked := make([]string, 0, len(fields))
	for _, field := range fields {
		runes := []rune(field)
		masked = append(masked, string(runes[0])+"***")
	}
	return strings.Join(masked, " ")
}

func serialSuffix(serialHex string) string {
	serialHex = strings.ToUpper(serialHex)
	if len(serialHex) <= 4 {
		return serialHex
	}
	return serialHex[len(serialHex)-4:]
}
