// New constrained structural verifier for the selected eimza-go fork.
// Snapshot and local changes are recorded in ../UPSTREAM.md. MIT licensed; see ../LICENSE.
package pades

import (
	"bytes"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strconv"

	"github.com/caisergan/legally/signing-service/third_party/eimza-go/cades"
)

var byteRangePattern = regexp.MustCompile(`/ByteRange\s*\[\s*(-?\d+)\s+(-?\d+)\s+(-?\d+)\s+(-?\d+)\s*\]`)

type VerificationEvidence struct {
	CMS *cades.VerificationEvidence
}

// VerifyCryptographic checks the one-signature controlled-corpus structure and
// delegates cryptographic/profile checks to cades.VerifyDetachedCryptographic.
// It is not an independent validator and does not establish certificate trust.
func VerifyCryptographic(pdfData []byte) (*VerificationEvidence, error) {
	const maxSignedPDFBytes = maxControlledPDFBytes + (1 << 20)
	if len(pdfData) == 0 || len(pdfData) > maxSignedPDFBytes {
		return nil, fmt.Errorf("pades: signed PDF size must be between 1 and %d bytes", maxSignedPDFBytes)
	}
	marker := []byte("/ETSI.CAdES.detached")
	markerOffset := bytes.Index(pdfData, marker)
	if markerOffset < 0 || bytes.Index(pdfData[markerOffset+len(marker):], marker) >= 0 {
		return nil, errors.New("pades: expected exactly one ETSI.CAdES.detached signature")
	}
	dictionaryStart := bytes.LastIndex(pdfData[:markerOffset], []byte("<<"))
	if dictionaryStart < 0 {
		return nil, errors.New("pades: signature dictionary start not found")
	}

	matches := byteRangePattern.FindSubmatch(pdfData[dictionaryStart:])
	if len(matches) != 5 {
		return nil, errors.New("pades: invalid ByteRange")
	}
	values := make([]int, 4)
	for index := range values {
		value, err := strconv.ParseInt(string(matches[index+1]), 10, 64)
		if err != nil || value < 0 || value > int64(len(pdfData)) {
			return nil, errors.New("pades: ByteRange contains an invalid value")
		}
		values[index] = int(value)
	}
	if values[0] != 0 || values[1] <= 0 || values[1] >= values[2] || values[2]+values[3] != len(pdfData) {
		return nil, errors.New("pades: ByteRange is not the supported two-part full-file range")
	}
	gap := pdfData[values[1]:values[2]]
	if len(gap) < 2 || gap[0] != '<' || gap[len(gap)-1] != '>' {
		return nil, errors.New("pades: ByteRange gap is not the Contents hex string")
	}
	cmsDER, err := decodePaddedDER(gap[1 : len(gap)-1])
	if err != nil {
		return nil, fmt.Errorf("pades: decode Contents: %w", err)
	}

	signedBytes := make([]byte, 0, values[1]+values[3])
	signedBytes = append(signedBytes, pdfData[:values[1]]...)
	signedBytes = append(signedBytes, pdfData[values[2]:]...)
	evidence, err := cades.VerifyDetachedCryptographic(cmsDER, signedBytes)
	if err != nil {
		return nil, err
	}
	return &VerificationEvidence{CMS: evidence}, nil
}

func decodePaddedDER(hexBytes []byte) ([]byte, error) {
	decoded := make([]byte, hex.DecodedLen(len(hexBytes)))
	count, err := hex.Decode(decoded, hexBytes)
	if err != nil {
		return nil, err
	}
	decoded = decoded[:count]
	var value asn1.RawValue
	rest, err := asn1.Unmarshal(decoded, &value)
	if err != nil {
		return nil, err
	}
	for _, trailing := range rest {
		if trailing != 0 {
			return nil, errors.New("non-zero data follows CMS DER")
		}
	}
	return append([]byte(nil), value.FullBytes...), nil
}
