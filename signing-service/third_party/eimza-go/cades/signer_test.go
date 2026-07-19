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
	"math/big"
	"testing"
	"time"

	"github.com/caisergan/legally/signing-service/third_party/eimza-go/asn/cms"
)

func TestSignDetachedRSASHA256BindsContentAndCertificate(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	content := []byte("controlled detached content")
	signingTime := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)

	der, err := SignDetachedRSASHA256(content, certificate, key, signingTime)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	evidence, err := VerifyDetachedCryptographic(der, content)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	expectedFingerprint := sha256.Sum256(certificate.Raw)
	if evidence.CertificateFingerprint != expectedFingerprint || evidence.SigningCertificateV2Hash != expectedFingerprint {
		t.Fatal("certificate fingerprint evidence does not match signer certificate")
	}

	signerInfo := parseSignerInfo(t, der)
	value, err := uniqueAttributeValue(signerInfo.SignedAttrs, oidSigningCertificateV2)
	if err != nil {
		t.Fatalf("SigningCertificateV2: %v", err)
	}
	var attribute signingCertificateV2
	rest, err := asn1.Unmarshal(value, &attribute)
	if err != nil || len(rest) != 0 || len(attribute.Certs) != 1 {
		t.Fatalf("invalid outer SigningCertificateV2 sequence: rest=%d err=%v value=%x", len(rest), err, value)
	}
	if !bytes.Equal(attribute.Certs[0].CertHash, expectedFingerprint[:]) {
		t.Fatal("SigningCertificateV2 does not bind the exact certificate")
	}
}

func TestVerifyDetachedRejectsTamperedContent(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	der, err := SignDetachedRSASHA256([]byte("original"), certificate, key, certificate.NotBefore.Add(time.Hour))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := VerifyDetachedCryptographic(der, []byte("changed")); err == nil {
		t.Fatal("tampered content was accepted")
	}
}

func TestVerifyRejectsSignerIdentifierMismatch(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	der, err := SignDetachedRSASHA256([]byte("content"), certificate, key, certificate.NotBefore.Add(time.Hour))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	var contentInfo cms.ContentInfo
	if rest, err := asn1.Unmarshal(der, &contentInfo); err != nil || len(rest) != 0 {
		t.Fatalf("parse ContentInfo: rest=%d err=%v", len(rest), err)
	}
	var signedData cms.SignedData
	if rest, err := asn1.Unmarshal(contentInfo.Content.Bytes, &signedData); err != nil || len(rest) != 0 {
		t.Fatalf("parse SignedData: rest=%d err=%v", len(rest), err)
	}
	signedData.SignerInfos[0].SID.SerialNumber = big.NewInt(999)
	signedDataDER, err := asn1.Marshal(signedData)
	if err != nil {
		t.Fatalf("marshal SignedData: %v", err)
	}
	contentInfo.Content.FullBytes = nil
	contentInfo.Content.Bytes = signedDataDER
	mutatedDER, err := asn1.Marshal(contentInfo)
	if err != nil {
		t.Fatalf("marshal ContentInfo: %v", err)
	}
	if _, err := VerifyDetachedCryptographic(mutatedDER, []byte("content")); err == nil {
		t.Fatal("mismatched SignerInfo identifier was accepted")
	}
}

func TestVerifyRejectsOuterDigestAlgorithmMismatch(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	content := []byte("content")
	der, err := SignDetachedRSASHA256(content, certificate, key, certificate.NotBefore.Add(time.Hour))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	mutated := mutateSignedData(t, der, func(signedData *cms.SignedData) {
		signedData.DigestAlgorithms = []pkix.AlgorithmIdentifier{algorithmIdentifier(asn1.ObjectIdentifier{1, 2, 840, 113549, 2, 5})}
	})
	if _, err := VerifyDetachedCryptographic(mutated, content); err == nil {
		t.Fatal("outer digest algorithm mismatch was accepted")
	}
}

func TestVerifyRejectsOuterContentTypeMismatch(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	content := []byte("content")
	der, err := SignDetachedRSASHA256(content, certificate, key, certificate.NotBefore.Add(time.Hour))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	mutated := mutateSignedData(t, der, func(signedData *cms.SignedData) {
		signedData.EncapContentInfo.EContentType = cms.OIDSignedData
	})
	if _, err := VerifyDetachedCryptographic(mutated, content); err == nil {
		t.Fatal("outer content type mismatch was accepted")
	}
}

func TestVerifyRejectsMalformedSignedSigningTime(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	content := []byte("content")
	der, err := SignDetachedRSASHA256(content, certificate, key, certificate.NotBefore.Add(time.Hour))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	mutated := mutateSignedData(t, der, func(signedData *cms.SignedData) {
		for index := range signedData.SignerInfos[0].SignedAttrs {
			attribute := &signedData.SignerInfos[0].SignedAttrs[index]
			if !attribute.Type.Equal(cms.OIDSigningTime) {
				continue
			}
			encoded, err := asn1.Marshal("not-a-time")
			if err != nil {
				t.Fatalf("marshal malformed signingTime: %v", err)
			}
			attribute.Values.FullBytes = nil
			attribute.Values.Bytes = encoded
		}
		resignSignedAttributes(t, &signedData.SignerInfos[0], key)
	})
	if _, err := VerifyDetachedCryptographic(mutated, content); err == nil {
		t.Fatal("malformed signed signingTime was accepted")
	}
}

func TestSigningRejectsMismatchedPrivateKey(t *testing.T) {
	certificate, _ := generateRSACertificate(t)
	_, otherKey := generateRSACertificate(t)
	if _, err := SignDetachedRSASHA256([]byte("content"), certificate, otherKey, certificate.NotBefore.Add(time.Hour)); err == nil {
		t.Fatal("certificate/private-key mismatch was accepted")
	}
}

func TestSigningRejectsExpiredCertificateAtSigningTime(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	if _, err := SignDetachedRSASHA256([]byte("content"), certificate, key, certificate.NotAfter.Add(time.Second)); err == nil {
		t.Fatal("expired certificate was accepted")
	}
}

func generateRSACertificate(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	notBefore := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: "Test Signer"},
		Issuer:       pkix.Name{CommonName: "Test Signer"},
		NotBefore:    notBefore,
		NotAfter:     notBefore.AddDate(2, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageContentCommitment,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse certificate: %v", err)
	}
	return certificate, key
}

func parseSignerInfo(t *testing.T, der []byte) cms.SignerInfo {
	t.Helper()
	var contentInfo cms.ContentInfo
	if rest, err := asn1.Unmarshal(der, &contentInfo); err != nil || len(rest) != 0 {
		t.Fatalf("parse ContentInfo: rest=%d err=%v", len(rest), err)
	}
	var signedData cms.SignedData
	if rest, err := asn1.Unmarshal(contentInfo.Content.Bytes, &signedData); err != nil || len(rest) != 0 {
		t.Fatalf("parse SignedData: rest=%d err=%v", len(rest), err)
	}
	if len(signedData.SignerInfos) != 1 {
		t.Fatalf("signers=%d", len(signedData.SignerInfos))
	}
	return signedData.SignerInfos[0]
}

func mutateSignedData(t *testing.T, der []byte, mutate func(*cms.SignedData)) []byte {
	t.Helper()
	var contentInfo cms.ContentInfo
	if rest, err := asn1.Unmarshal(der, &contentInfo); err != nil || len(rest) != 0 {
		t.Fatalf("parse ContentInfo: rest=%d err=%v", len(rest), err)
	}
	var signedData cms.SignedData
	if rest, err := asn1.Unmarshal(contentInfo.Content.Bytes, &signedData); err != nil || len(rest) != 0 {
		t.Fatalf("parse SignedData: rest=%d err=%v", len(rest), err)
	}
	mutate(&signedData)
	signedDataDER, err := asn1.Marshal(signedData)
	if err != nil {
		t.Fatalf("marshal SignedData: %v", err)
	}
	contentInfo.Content.FullBytes = nil
	contentInfo.Content.Bytes = signedDataDER
	mutated, err := asn1.Marshal(contentInfo)
	if err != nil {
		t.Fatalf("marshal ContentInfo: %v", err)
	}
	return mutated
}

func resignSignedAttributes(t *testing.T, signerInfo *cms.SignerInfo, key *rsa.PrivateKey) {
	t.Helper()
	attributes, err := canonicalizeAttributes(signerInfo.SignedAttrs)
	if err != nil {
		t.Fatalf("canonicalize attributes: %v", err)
	}
	encoded, err := marshalAttributesForSigning(attributes)
	if err != nil {
		t.Fatalf("marshal attributes: %v", err)
	}
	digest := sha256.Sum256(encoded)
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatalf("resign attributes: %v", err)
	}
	signerInfo.SignedAttrs = attributes
	signerInfo.Signature = signature
}
