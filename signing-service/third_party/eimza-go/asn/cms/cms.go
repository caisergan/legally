// Source: github.com/KilimcininKorOglu/eimza-go, snapshot recorded in ../../UPSTREAM.md.
// Local changes are documented there. MIT licensed; see ../../LICENSE.
package cms

import (
	"crypto/x509/pkix"
	"encoding/asn1"
	"math/big"
)

// ContentInfo is the top-level CMS structure (RFC 5652 Section 3).
type ContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0"`
}

// SignedData is the CMS SignedData structure (RFC 5652 Section 5.1).
type SignedData struct {
	Version          int
	DigestAlgorithms []pkix.AlgorithmIdentifier `asn1:"set"`
	EncapContentInfo EncapsulatedContentInfo
	Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	CRLs             asn1.RawValue `asn1:"optional,tag:1"`
	SignerInfos      []SignerInfo  `asn1:"set"`
}

// EncapsulatedContentInfo holds the signed content (RFC 5652 Section 5.2).
type EncapsulatedContentInfo struct {
	EContentType asn1.ObjectIdentifier
	EContent     asn1.RawValue `asn1:"optional,explicit,tag:0"`
}

// SignerInfo identifies a signer and their signature (RFC 5652 Section 5.3).
type SignerInfo struct {
	Version            int
	SID                IssuerAndSerialNumber
	DigestAlgorithm    pkix.AlgorithmIdentifier
	SignedAttrs        []Attribute `asn1:"optional,tag:0"`
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Signature          []byte
	UnsignedAttrs      []Attribute `asn1:"optional,tag:1"`
}

// IssuerAndSerialNumber identifies a certificate by issuer DN and serial.
type IssuerAndSerialNumber struct {
	Issuer       asn1.RawValue
	SerialNumber *big.Int
}

// Attribute is a signed or unsigned attribute (RFC 5652 Section 5.3).
type Attribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue `asn1:"set"`
}

// EnvelopedData is the CMS EnvelopedData structure (RFC 5652 Section 6.1).
type EnvelopedData struct {
	Version              int
	RecipientInfos       []KeyTransRecipientInfo `asn1:"set"`
	EncryptedContentInfo EncryptedContentInfo
}

// KeyTransRecipientInfo identifies a recipient via key transport (RFC 5652 Section 6.2.1).
type KeyTransRecipientInfo struct {
	Version                int
	RID                    IssuerAndSerialNumber
	KeyEncryptionAlgorithm pkix.AlgorithmIdentifier
	EncryptedKey           []byte
}

// EncryptedContentInfo holds the encrypted content (RFC 5652 Section 6.1).
type EncryptedContentInfo struct {
	ContentType                asn1.ObjectIdentifier
	ContentEncryptionAlgorithm pkix.AlgorithmIdentifier
	EncryptedContent           asn1.RawValue `asn1:"optional,tag:0"`
}

// Common OIDs used in CMS/CAdES.
var (
	OIDData                 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	OIDSignedData           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	OIDEnvelopedData        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 3}
	OIDContentType          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	OIDMessageDigest        = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	OIDSigningTime          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	OIDCounterSignature     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 6}
	OIDSigningCertificate   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 12}
	OIDSigningCertificateV2 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 47}
	OIDTimeStampToken       = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 16, 2, 14}

	OIDRSAOAEP     = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 7}
	OIDRSAPKCS1v15 = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
	OIDAES256CBC   = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 1, 42}
)
