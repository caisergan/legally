// Adapted from github.com/KilimcininKorOglu/eimza-go/cades/signeddata.go.
// Snapshot and local changes are recorded in ../UPSTREAM.md. MIT licensed; see ../LICENSE.
package cades

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/caisergan/legally/signing-service/third_party/eimza-go/asn/cms"
)

var (
	oidSHA256               = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidSHA256WithRSA        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 11}
	oidSigningCertificateV2 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 47}
)

type signingCertificateV2 struct {
	Certs []essCertIDv2
}

type essCertIDv2 struct {
	HashAlgorithm pkix.AlgorithmIdentifier
	CertHash      []byte
}

// SignDetachedRSASHA256 creates the constrained CMS/CAdES payload used by the
// test-only PAdES Baseline B-B path. No other creation algorithm is reachable.
func SignDetachedRSASHA256(content []byte, certificate *x509.Certificate, signer crypto.Signer, signingTime time.Time) ([]byte, error) {
	if err := validateSigner(certificate, signer, signingTime); err != nil {
		return nil, err
	}

	contentDigest := sha256.Sum256(content)
	attributes, err := buildSignedAttributes(contentDigest[:], certificate, signingTime.UTC().Truncate(time.Second))
	if err != nil {
		return nil, fmt.Errorf("cades: build signed attributes: %w", err)
	}
	attributes, err = canonicalizeAttributes(attributes)
	if err != nil {
		return nil, fmt.Errorf("cades: canonicalize signed attributes: %w", err)
	}

	attributesDER, err := marshalAttributesForSigning(attributes)
	if err != nil {
		return nil, fmt.Errorf("cades: encode signed attributes: %w", err)
	}
	attributesDigest := sha256.Sum256(attributesDER)
	signature, err := signer.Sign(rand.Reader, attributesDigest[:], crypto.SHA256)
	if err != nil {
		return nil, fmt.Errorf("cades: RSA/SHA-256 signing failed: %w", err)
	}

	digestAlgorithm := algorithmIdentifier(oidSHA256)
	signatureAlgorithm := algorithmIdentifier(oidSHA256WithRSA)
	signerInfo := cms.SignerInfo{
		Version: 1,
		SID: cms.IssuerAndSerialNumber{
			Issuer:       asn1.RawValue{FullBytes: certificate.RawIssuer},
			SerialNumber: certificate.SerialNumber,
		},
		DigestAlgorithm:    digestAlgorithm,
		SignedAttrs:        attributes,
		SignatureAlgorithm: signatureAlgorithm,
		Signature:          signature,
	}

	signedData := cms.SignedData{
		Version:          1,
		DigestAlgorithms: []pkix.AlgorithmIdentifier{digestAlgorithm},
		EncapContentInfo: cms.EncapsulatedContentInfo{EContentType: cms.OIDData},
		Certificates: asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        0,
			IsCompound: true,
			Bytes:      certificate.Raw,
		},
		SignerInfos: []cms.SignerInfo{signerInfo},
	}
	signedDataDER, err := asn1.Marshal(signedData)
	if err != nil {
		return nil, fmt.Errorf("cades: encode SignedData: %w", err)
	}
	return asn1.Marshal(cms.ContentInfo{
		ContentType: cms.OIDSignedData,
		Content: asn1.RawValue{
			Class:      asn1.ClassContextSpecific,
			Tag:        0,
			IsCompound: true,
			Bytes:      signedDataDER,
		},
	})
}

func validateSigner(certificate *x509.Certificate, signer crypto.Signer, signingTime time.Time) error {
	if certificate == nil {
		return errors.New("cades: signer certificate is required")
	}
	if signer == nil || signer.Public() == nil {
		return errors.New("cades: crypto.Signer is required")
	}
	certificateKey, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("cades: certificate key type %T is unsupported; only RSA is enabled", certificate.PublicKey)
	}
	if certificateKey.N.BitLen() < 2048 {
		return fmt.Errorf("cades: RSA key is %d bits; minimum is 2048", certificateKey.N.BitLen())
	}
	signerKey, ok := signer.Public().(*rsa.PublicKey)
	if !ok {
		return fmt.Errorf("cades: signer key type %T is unsupported; only RSA is enabled", signer.Public())
	}
	if certificateKey.E != signerKey.E || certificateKey.N.Cmp(signerKey.N) != 0 {
		return errors.New("cades: signer public key does not match certificate")
	}
	if signingTime.Before(certificate.NotBefore) || signingTime.After(certificate.NotAfter) {
		return errors.New("cades: certificate is not valid at signing time")
	}
	if certificate.KeyUsage != 0 && certificate.KeyUsage&(x509.KeyUsageDigitalSignature|x509.KeyUsageContentCommitment) == 0 {
		return errors.New("cades: certificate key usage does not permit signing")
	}
	return nil
}

func buildSignedAttributes(contentDigest []byte, certificate *x509.Certificate, signingTime time.Time) ([]cms.Attribute, error) {
	contentTypeValue, err := asn1.Marshal(cms.OIDData)
	if err != nil {
		return nil, err
	}
	messageDigestValue, err := asn1.Marshal(contentDigest)
	if err != nil {
		return nil, err
	}
	signingTimeValue, err := asn1.Marshal(signingTime)
	if err != nil {
		return nil, err
	}
	certificateHash := sha256.Sum256(certificate.Raw)
	signingCertificateValue, err := asn1.Marshal(signingCertificateV2{
		Certs: []essCertIDv2{{
			HashAlgorithm: algorithmIdentifier(oidSHA256),
			CertHash:      certificateHash[:],
		}},
	})
	if err != nil {
		return nil, err
	}

	return []cms.Attribute{
		attribute(cms.OIDContentType, contentTypeValue),
		attribute(cms.OIDMessageDigest, messageDigestValue),
		attribute(cms.OIDSigningTime, signingTimeValue),
		attribute(oidSigningCertificateV2, signingCertificateValue),
	}, nil
}

func attribute(oid asn1.ObjectIdentifier, encodedValue []byte) cms.Attribute {
	return cms.Attribute{
		Type: oid,
		Values: asn1.RawValue{
			Class:      asn1.ClassUniversal,
			Tag:        17,
			IsCompound: true,
			Bytes:      encodedValue,
		},
	}
}

func algorithmIdentifier(oid asn1.ObjectIdentifier) pkix.AlgorithmIdentifier {
	return pkix.AlgorithmIdentifier{Algorithm: oid, Parameters: asn1.RawValue{Tag: 5}}
}

func canonicalizeAttributes(attributes []cms.Attribute) ([]cms.Attribute, error) {
	type encodedAttribute struct {
		attribute cms.Attribute
		der       []byte
	}
	encoded := make([]encodedAttribute, len(attributes))
	for index, attribute := range attributes {
		der, err := asn1.Marshal(attribute)
		if err != nil {
			return nil, err
		}
		encoded[index] = encodedAttribute{attribute: attribute, der: der}
	}
	sort.Slice(encoded, func(left, right int) bool {
		return bytes.Compare(encoded[left].der, encoded[right].der) < 0
	})
	result := make([]cms.Attribute, len(encoded))
	for index, value := range encoded {
		result[index] = value.attribute
	}
	return result, nil
}

func marshalAttributesForSigning(attributes []cms.Attribute) ([]byte, error) {
	encoded, err := asn1.Marshal(attributes)
	if err != nil {
		return nil, err
	}
	if len(encoded) == 0 || encoded[0] != 0x30 {
		return nil, errors.New("cades: signed attributes did not encode as a sequence")
	}
	encoded[0] = 0x31
	return encoded, nil
}
