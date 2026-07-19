// New constrained verifier for the selected eimza-go fork.
// Snapshot and local changes are recorded in ../UPSTREAM.md. MIT licensed; see ../LICENSE.
package cades

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"time"

	"github.com/caisergan/legally/signing-service/third_party/eimza-go/asn/cms"
)

type VerificationEvidence struct {
	Certificate              *x509.Certificate
	CertificateFingerprint   [32]byte
	MessageDigest            [32]byte
	SigningCertificateV2Hash [32]byte
}

// VerifyDetachedCryptographic verifies the constrained CMS structure,
// mandatory B-B attributes, certificate binding, content digest, and RSA
// signature. It does not establish trust, revocation, qualification, or legal
// validity and cannot substitute for an independent PAdES validator.
func VerifyDetachedCryptographic(der, content []byte) (*VerificationEvidence, error) {
	var contentInfo cms.ContentInfo
	rest, err := asn1.Unmarshal(der, &contentInfo)
	if err != nil || len(rest) != 0 {
		return nil, errors.New("cades: invalid ContentInfo encoding")
	}
	if !contentInfo.ContentType.Equal(cms.OIDSignedData) {
		return nil, errors.New("cades: content is not CMS SignedData")
	}

	var signedData cms.SignedData
	rest, err = asn1.Unmarshal(contentInfo.Content.Bytes, &signedData)
	if err != nil || len(rest) != 0 {
		return nil, errors.New("cades: invalid SignedData encoding")
	}
	if len(signedData.DigestAlgorithms) != 1 || !signedData.DigestAlgorithms[0].Algorithm.Equal(oidSHA256) {
		return nil, errors.New("cades: SignedData must declare exactly one SHA-256 digest algorithm")
	}
	if !signedData.EncapContentInfo.EContentType.Equal(cms.OIDData) {
		return nil, errors.New("cades: encapsulated content type must be id-data")
	}
	if len(signedData.SignerInfos) != 1 {
		return nil, fmt.Errorf("cades: expected exactly one signer, got %d", len(signedData.SignerInfos))
	}
	if len(signedData.EncapContentInfo.EContent.FullBytes) != 0 || len(signedData.EncapContentInfo.EContent.Bytes) != 0 {
		return nil, errors.New("cades: embedded content is not allowed")
	}
	certificate, err := x509.ParseCertificate(signedData.Certificates.Bytes)
	if err != nil {
		return nil, fmt.Errorf("cades: parse signer certificate: %w", err)
	}

	signerInfo := signedData.SignerInfos[0]
	if signerInfo.SID.SerialNumber == nil || signerInfo.SID.SerialNumber.Cmp(certificate.SerialNumber) != 0 || !bytes.Equal(signerInfo.SID.Issuer.FullBytes, certificate.RawIssuer) {
		return nil, errors.New("cades: SignerInfo identifier does not match embedded certificate")
	}
	if !signerInfo.DigestAlgorithm.Algorithm.Equal(oidSHA256) {
		return nil, errors.New("cades: only SHA-256 digest is accepted")
	}
	if !signerInfo.SignatureAlgorithm.Algorithm.Equal(oidSHA256WithRSA) {
		return nil, errors.New("cades: only sha256WithRSAEncryption is accepted")
	}

	messageDigestValue, err := uniqueAttributeValue(signerInfo.SignedAttrs, cms.OIDMessageDigest)
	if err != nil {
		return nil, err
	}
	var messageDigest []byte
	if rest, err = asn1.Unmarshal(messageDigestValue, &messageDigest); err != nil || len(rest) != 0 {
		return nil, errors.New("cades: invalid messageDigest attribute")
	}
	expectedDigest := sha256.Sum256(content)
	if !bytes.Equal(messageDigest, expectedDigest[:]) {
		return nil, errors.New("cades: messageDigest does not bind the supplied content")
	}

	contentTypeValue, err := uniqueAttributeValue(signerInfo.SignedAttrs, cms.OIDContentType)
	if err != nil {
		return nil, err
	}
	var contentType asn1.ObjectIdentifier
	if rest, err = asn1.Unmarshal(contentTypeValue, &contentType); err != nil || len(rest) != 0 || !contentType.Equal(cms.OIDData) {
		return nil, errors.New("cades: invalid contentType attribute")
	}
	signingTimeValue, err := uniqueAttributeValue(signerInfo.SignedAttrs, cms.OIDSigningTime)
	if err != nil {
		return nil, err
	}
	var signingTime time.Time
	if rest, err = asn1.Unmarshal(signingTimeValue, &signingTime); err != nil || len(rest) != 0 || signingTime.IsZero() {
		return nil, errors.New("cades: invalid signingTime attribute")
	}
	if signingTime.Before(certificate.NotBefore) || signingTime.After(certificate.NotAfter) {
		return nil, errors.New("cades: signingTime is outside the certificate validity interval")
	}

	signingCertificateValue, err := uniqueAttributeValue(signerInfo.SignedAttrs, oidSigningCertificateV2)
	if err != nil {
		return nil, err
	}
	var signingCertificate signingCertificateV2
	if rest, err = asn1.Unmarshal(signingCertificateValue, &signingCertificate); err != nil || len(rest) != 0 {
		return nil, errors.New("cades: invalid SigningCertificateV2 attribute")
	}
	if len(signingCertificate.Certs) != 1 {
		return nil, errors.New("cades: SigningCertificateV2 must bind exactly one certificate")
	}
	if !signingCertificate.Certs[0].HashAlgorithm.Algorithm.Equal(oidSHA256) {
		return nil, errors.New("cades: SigningCertificateV2 must use SHA-256")
	}
	certificateFingerprint := sha256.Sum256(certificate.Raw)
	if !bytes.Equal(signingCertificate.Certs[0].CertHash, certificateFingerprint[:]) {
		return nil, errors.New("cades: SigningCertificateV2 hash does not match signer certificate")
	}

	canonicalAttributes, err := canonicalizeAttributes(signerInfo.SignedAttrs)
	if err != nil {
		return nil, fmt.Errorf("cades: canonicalize signed attributes: %w", err)
	}
	attributesDER, err := marshalAttributesForSigning(canonicalAttributes)
	if err != nil {
		return nil, fmt.Errorf("cades: encode signed attributes: %w", err)
	}
	attributesDigest := sha256.Sum256(attributesDER)
	publicKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("cades: unsupported certificate key type %T", certificate.PublicKey)
	}
	if err := rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, attributesDigest[:], signerInfo.Signature); err != nil {
		return nil, fmt.Errorf("cades: invalid RSA signature: %w", err)
	}

	return &VerificationEvidence{
		Certificate:              certificate,
		CertificateFingerprint:   certificateFingerprint,
		MessageDigest:            expectedDigest,
		SigningCertificateV2Hash: certificateFingerprint,
	}, nil
}

func uniqueAttributeValue(attributes []cms.Attribute, oid asn1.ObjectIdentifier) ([]byte, error) {
	var value []byte
	for _, attribute := range attributes {
		if !attribute.Type.Equal(oid) {
			continue
		}
		if value != nil {
			return nil, fmt.Errorf("cades: duplicate signed attribute %s", oid)
		}
		value = attribute.Values.Bytes
	}
	if value == nil {
		return nil, fmt.Errorf("cades: missing mandatory signed attribute %s", oid)
	}
	return value, nil
}
