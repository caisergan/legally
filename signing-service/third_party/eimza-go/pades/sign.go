// Adapted from github.com/KilimcininKorOglu/eimza-go/pades/sign.go and placeholder.go.
// Snapshot and local changes are recorded in ../UPSTREAM.md. MIT licensed; see ../LICENSE.
package pades

import (
	"bytes"
	"crypto"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/caisergan/legally/signing-service/third_party/eimza-go/cades"
	"github.com/caisergan/legally/signing-service/third_party/eimza-go/pdf"
)

const (
	defaultPlaceholderSize = 8192
	maxControlledPDFBytes  = 25 << 20
)

type SignOptions struct {
	Reason      string
	Location    string
	ContactInfo string
	Name        string
	SigningTime time.Time
}

// SignBaselineBB signs only the deliberately narrow, test-only PDF corpus
// described in UPSTREAM.md. It exposes no caller-selected profile or algorithm.
func SignBaselineBB(pdfData []byte, certificate *x509.Certificate, signer crypto.Signer, options SignOptions) ([]byte, error) {
	parsed, catalogDictionary, err := preflight(pdfData)
	if err != nil {
		return nil, err
	}

	signingTime := options.SigningTime
	if signingTime.IsZero() {
		signingTime = time.Now()
	}
	signingTime = signingTime.UTC().Truncate(time.Second)

	signatureDictionary, contentsRelativeOffset := buildSignatureDictionary(options, signingTime, defaultPlaceholderSize)
	signatureObject := parsed.AllocateObject()
	fieldObject := parsed.AllocateObject()
	acroFormObject := parsed.AllocateObject()

	updatedCatalog, err := addCatalogReference(catalogDictionary, fmt.Sprintf("/AcroForm %d 0 R", acroFormObject))
	if err != nil {
		return nil, err
	}
	fieldDictionary := []byte(fmt.Sprintf("<< /Type /Annot /Subtype /Widget /FT /Sig /T (YargiSignature%d) /V %d 0 R /F 132 /Rect [0 0 0 0] >>", signatureObject, signatureObject))
	acroFormDictionary := []byte(fmt.Sprintf("<< /Fields [%d 0 R] /SigFlags 3 >>", fieldObject))

	appended, err := pdf.IncrementalAppendParsed(parsed, map[int][]byte{
		parsed.Trailer.Root: updatedCatalog,
		signatureObject:     signatureDictionary,
		fieldObject:         fieldDictionary,
		acroFormObject:      acroFormDictionary,
	})
	if err != nil {
		return nil, fmt.Errorf("pades: incremental append failed: %w", err)
	}

	signatureHeader := []byte(fmt.Sprintf("%d 0 obj\n", signatureObject))
	signatureOffset := bytes.LastIndex(appended, signatureHeader)
	if signatureOffset < 0 {
		return nil, errors.New("pades: signature object not found in output")
	}
	contentsAbsoluteOffset := signatureOffset + len(signatureHeader) + contentsRelativeOffset
	patched, signedBytes, err := patchByteRange(appended, signatureOffset, contentsAbsoluteOffset, defaultPlaceholderSize)
	if err != nil {
		return nil, err
	}

	cmsDER, err := cades.SignDetachedRSASHA256(signedBytes, certificate, signer, signingTime)
	if err != nil {
		return nil, fmt.Errorf("pades: detached CAdES signing failed: %w", err)
	}
	result, err := spliceCMS(patched, cmsDER, contentsAbsoluteOffset, defaultPlaceholderSize)
	if err != nil {
		return nil, err
	}
	if _, err := VerifyCryptographic(result); err != nil {
		return nil, fmt.Errorf("pades: internal post-sign structural verification failed: %w", err)
	}
	return result, nil
}

func preflight(data []byte) (*pdf.ParsedPDF, []byte, error) {
	if len(data) == 0 || len(data) > maxControlledPDFBytes {
		return nil, nil, fmt.Errorf("pades: PDF size must be between 1 and %d bytes", maxControlledPDFBytes)
	}
	if pdf.ContainsName(data, "Encrypt") {
		return nil, nil, errors.New("pades: encrypted PDFs are unsupported")
	}
	if pdf.ContainsName(data, "AcroForm") || pdf.ContainsName(data, "ETSI.CAdES.detached") || pdf.ContainsName(data, "Sig") {
		return nil, nil, errors.New("pades: existing forms or signatures are outside the controlled B-B corpus")
	}

	parsed, err := pdf.Parse(data)
	if err != nil {
		return nil, nil, fmt.Errorf("pades: parse PDF: %w", err)
	}
	if parsed.Trailer.HasPrev {
		return nil, nil, errors.New("pades: PDFs with prior incremental updates are unsupported")
	}
	if parsed.Trailer.HasXRefStm {
		return nil, nil, errors.New("pades: hybrid-reference PDFs are unsupported")
	}
	if parsed.StartXref < 0 || parsed.StartXref >= len(data) || !bytes.HasPrefix(bytes.TrimLeft(data[parsed.StartXref:], " \t\r\n"), []byte("xref")) {
		return nil, nil, errors.New("pades: xref streams are not yet in the controlled signing corpus")
	}
	catalogDictionary, err := objectDictionary(parsed, parsed.Trailer.Root)
	if err != nil {
		return nil, nil, fmt.Errorf("pades: read catalog: %w", err)
	}
	catalogType, ok := pdf.DictionaryNameValue(catalogDictionary, "Type")
	if !ok || catalogType != "Catalog" {
		return nil, nil, errors.New("pades: trailer root is not a supported catalog dictionary")
	}
	if pdf.DictionaryHasKey(catalogDictionary, "AcroForm") {
		return nil, nil, errors.New("pades: existing AcroForm is unsupported")
	}
	return parsed, catalogDictionary, nil
}

func objectDictionary(parsed *pdf.ParsedPDF, objectNumber int) ([]byte, error) {
	var offset = -1
	for _, entry := range parsed.Xref {
		if entry.ObjectNum == objectNumber && entry.InUse && entry.Generation == 0 {
			offset = entry.Offset
			break
		}
	}
	if offset < 0 || offset >= len(parsed.Raw) {
		return nil, fmt.Errorf("object %d has no usable xref entry", objectNumber)
	}
	rest := parsed.Raw[offset:]
	header := []byte(fmt.Sprintf("%d 0 obj", objectNumber))
	if !bytes.HasPrefix(rest, header) {
		return nil, fmt.Errorf("object %d header does not match xref offset", objectNumber)
	}
	dictionaryStart := bytes.Index(rest[len(header):], []byte("<<"))
	if dictionaryStart < 0 {
		return nil, fmt.Errorf("object %d has no dictionary", objectNumber)
	}
	dictionaryStart += len(header)
	dictionaryEnd, err := pdf.FindDictionaryEnd(rest, dictionaryStart)
	if err != nil {
		return nil, fmt.Errorf("object %d: %w", objectNumber, err)
	}
	return append([]byte(nil), rest[dictionaryStart:dictionaryEnd]...), nil
}

func addCatalogReference(dictionary []byte, reference string) ([]byte, error) {
	trimmed := bytes.TrimSpace(dictionary)
	if len(trimmed) < 4 || !bytes.HasPrefix(trimmed, []byte("<<")) || !bytes.HasSuffix(trimmed, []byte(">>")) {
		return nil, errors.New("pades: invalid catalog dictionary")
	}
	result := make([]byte, 0, len(trimmed)+len(reference)+2)
	result = append(result, trimmed[:len(trimmed)-2]...)
	result = append(result, ' ')
	result = append(result, reference...)
	result = append(result, ' ', '>', '>')
	return result, nil
}

func buildSignatureDictionary(options SignOptions, signingTime time.Time, placeholderSize int) ([]byte, int) {
	placeholder := "<" + strings.Repeat("0", placeholderSize*2) + ">"
	var builder strings.Builder
	builder.WriteString("<< /Type /Sig\n")
	builder.WriteString("   /Filter /Adobe.PPKLite\n")
	builder.WriteString("   /SubFilter /ETSI.CAdES.detached\n")
	builder.WriteString("   /ByteRange [0 0000000000 0000000000 0000000000]\n")
	builder.WriteString("   /Contents ")
	contentsOffset := builder.Len()
	builder.WriteString(placeholder)
	builder.WriteString("\n   /M (")
	builder.WriteString(signingTime.Format("D:20060102150405Z"))
	builder.WriteString(")\n")
	appendPDFString(&builder, "Reason", options.Reason)
	appendPDFString(&builder, "Location", options.Location)
	appendPDFString(&builder, "ContactInfo", options.ContactInfo)
	appendPDFString(&builder, "Name", options.Name)
	builder.WriteString(">>")
	return []byte(builder.String()), contentsOffset
}

func appendPDFString(builder *strings.Builder, key, value string) {
	if value == "" {
		return
	}
	builder.WriteString("   /")
	builder.WriteString(key)
	builder.WriteString(" (")
	builder.WriteString(pdfEscapeString(value))
	builder.WriteString(")\n")
}

func patchByteRange(data []byte, signatureOffset, contentsOffset, placeholderSize int) ([]byte, []byte, error) {
	contentsEnd := contentsOffset + 1 + placeholderSize*2 + 1
	if signatureOffset < 0 || contentsOffset < signatureOffset || contentsEnd > len(data) {
		return nil, nil, errors.New("pades: invalid Contents placeholder bounds")
	}
	length1 := contentsOffset
	offset2 := contentsEnd
	length2 := len(data) - offset2
	if length1 > 9999999999 || offset2 > 9999999999 || length2 > 9999999999 {
		return nil, nil, errors.New("pades: ByteRange value exceeds fixed field width")
	}

	placeholder := []byte("[0 0000000000 0000000000 0000000000]")
	relativeIndex := bytes.Index(data[signatureOffset:contentsOffset], placeholder)
	if relativeIndex < 0 {
		return nil, nil, errors.New("pades: ByteRange placeholder not found in signature object")
	}
	byteRange := []byte(fmt.Sprintf("[0 %010d %010d %010d]", length1, offset2, length2))
	if len(byteRange) != len(placeholder) {
		return nil, nil, errors.New("pades: invalid ByteRange encoding length")
	}

	patched := append([]byte(nil), data...)
	copy(patched[signatureOffset+relativeIndex:signatureOffset+relativeIndex+len(placeholder)], byteRange)
	signedBytes := make([]byte, 0, length1+length2)
	signedBytes = append(signedBytes, patched[:length1]...)
	signedBytes = append(signedBytes, patched[offset2:]...)
	return patched, signedBytes, nil
}

func spliceCMS(data, cmsDER []byte, contentsOffset, placeholderSize int) ([]byte, error) {
	if len(cmsDER) > placeholderSize {
		return nil, fmt.Errorf("pades: CMS payload of %d bytes exceeds %d-byte placeholder", len(cmsDER), placeholderSize)
	}
	if contentsOffset < 0 || contentsOffset+1+placeholderSize*2 >= len(data) || data[contentsOffset] != '<' {
		return nil, errors.New("pades: invalid Contents offset")
	}
	encoded := strings.ToUpper(hex.EncodeToString(cmsDER)) + strings.Repeat("0", (placeholderSize-len(cmsDER))*2)
	result := append([]byte(nil), data...)
	copy(result[contentsOffset+1:contentsOffset+1+len(encoded)], encoded)
	return result, nil
}

func pdfEscapeString(value string) string {
	value = strings.ReplaceAll(value, "\\", "\\\\")
	value = strings.ReplaceAll(value, "(", "\\(")
	return strings.ReplaceAll(value, ")", "\\)")
}
