package credentials

import (
	"database/sql"
	"time"
)

const credentialColumns = `service_credential_id, physical_token_queue_key, module_alias, module_sha256,
	slot_id, token_serial, token_label, certificate_der, certificate_sha256, key_ckaid,
	public_key_type, public_key_bits, not_before, not_after, enrolled_at, last_probed_at, enabled, last_health`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanCredential(scanner rowScanner) (Credential, error) {
	var (
		cred                            Credential
		slotID                          int64
		notBefore, notAfter, enrolledAt string
		lastProbedAt, lastHealth        sql.NullString
		enabledInt                      int
	)
	err := scanner.Scan(
		&cred.ServiceCredentialID, &cred.PhysicalTokenQueueKey, &cred.ModuleAlias, &cred.ModuleSHA256,
		&slotID, &cred.TokenSerial, &cred.TokenLabel, &cred.CertificateDER, &cred.CertificateSHA256, &cred.KeyCKAID,
		&cred.PublicKeyType, &cred.PublicKeyBits, &notBefore, &notAfter, &enrolledAt, &lastProbedAt, &enabledInt, &lastHealth,
	)
	if err != nil {
		return Credential{}, err
	}
	cred.SlotID = uint(slotID)
	cred.Enabled = enabledInt != 0
	cred.LastHealth = lastHealth.String

	for _, field := range []struct {
		text   string
		target *time.Time
	}{{notBefore, &cred.NotBefore}, {notAfter, &cred.NotAfter}, {enrolledAt, &cred.EnrolledAt}} {
		parsed, perr := time.Parse(time.RFC3339Nano, field.text)
		if perr != nil {
			return Credential{}, perr
		}
		*field.target = parsed
	}
	if lastProbedAt.Valid && lastProbedAt.String != "" {
		parsed, perr := time.Parse(time.RFC3339Nano, lastProbedAt.String)
		if perr != nil {
			return Credential{}, perr
		}
		cred.LastProbedAt = &parsed
	}
	return cred, nil
}
