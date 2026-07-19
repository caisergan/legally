package pades

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"sort"
	"testing"
	"time"
)

func TestSignBaselineBBControlledCorpusRoundTrip(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	input := minimalPDF(t, 9, 17, 23)
	signed, err := SignBaselineBB(input, certificate, key, SignOptions{
		Reason:      "Onay",
		Location:    "İstanbul",
		Name:        "Test Signer",
		SigningTime: certificate.NotBefore.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !bytes.HasPrefix(signed, input) {
		t.Fatal("incremental signature did not preserve the original bytes")
	}
	if !bytes.Contains(signed, []byte("/PageMode /UseNone")) {
		t.Fatal("catalog content was not preserved")
	}
	if !bytes.Contains(signed, []byte("/ETSI.CAdES.detached")) {
		t.Fatal("PAdES subfilter is missing")
	}
	if _, err := VerifyCryptographic(signed); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestSignBaselineBBRejectsEscapedSensitiveNames(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	for name, replacement := range map[string][]byte{
		"AcroForm": []byte("/Acro#46orm 8 0 R"),
		"Encrypt":  []byte("/Encr#79pt 8 0 R"),
	} {
		t.Run(name, func(t *testing.T) {
			input := bytes.Replace(minimalPDF(t, 1, 2, 3), []byte("/PageMode /UseNone"), replacement, 1)
			if _, err := SignBaselineBB(input, certificate, key, SignOptions{SigningTime: certificate.NotBefore.Add(time.Hour)}); err == nil {
				t.Fatalf("escaped %s name was accepted", name)
			}
		})
	}
}

func TestSignBaselineBBDictionaryScannerIgnoresStringDelimiters(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	input := bytes.Replace(minimalPDF(t, 1, 2, 3), []byte("/PageMode /UseNone"), []byte("/Lang (a >> b)    "), 1)
	signed, err := SignBaselineBB(input, certificate, key, SignOptions{SigningTime: certificate.NotBefore.Add(time.Hour)})
	if err != nil {
		t.Fatalf("catalog string caused early dictionary termination: %v", err)
	}
	if !bytes.Contains(signed, []byte("/Lang (a >> b)")) {
		t.Fatal("catalog string was not preserved")
	}
}

func TestSignBaselineBBRejectsCatalogTypeDecoyInString(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	input := bytes.Replace(
		minimalPDF(t, 1, 2, 3),
		[]byte("/Type /Catalog"),
		[]byte("/Label (/Type /Catalog) /Type /Pages"),
		1,
	)
	if _, err := SignBaselineBB(input, certificate, key, SignOptions{SigningTime: certificate.NotBefore.Add(time.Hour)}); err == nil {
		t.Fatal("catalog type found only in a string was accepted")
	}
}

func TestSignBaselineBBRejectsExistingFormsAndSignatures(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	input := bytes.Replace(minimalPDF(t, 1, 2, 3), []byte("/PageMode /UseNone"), []byte("/AcroForm 8 0 R"), 1)
	if _, err := SignBaselineBB(input, certificate, key, SignOptions{SigningTime: certificate.NotBefore.Add(time.Hour)}); err == nil {
		t.Fatal("existing AcroForm was accepted")
	}
}

func TestVerifyCryptographicRejectsSignedByteMutation(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	signed, err := SignBaselineBB(minimalPDF(t, 1, 2, 3), certificate, key, SignOptions{SigningTime: certificate.NotBefore.Add(time.Hour)})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	mutated := append([]byte(nil), signed...)
	mutated[20] ^= 1
	if _, err := VerifyCryptographic(mutated); err == nil {
		t.Fatal("signed-byte mutation was accepted")
	}
}

func TestVerifyCryptographicRejectsOversizedInputBeforeAllocation(t *testing.T) {
	const maxSignedPDFBytes = maxControlledPDFBytes + (1 << 20)
	if _, err := VerifyCryptographic(make([]byte, maxSignedPDFBytes+1)); err == nil {
		t.Fatal("oversized verification input was accepted")
	}
}

func TestVerifyCryptographicRejectsNegativeByteRangeWithoutPanic(t *testing.T) {
	input := []byte("<< /Type /Sig /SubFilter /ETSI.CAdES.detached /ByteRange [0 -1 10 2] /Contents <3000> >>")
	if _, err := VerifyCryptographic(input); err == nil {
		t.Fatal("negative ByteRange was accepted")
	}
}

func TestSignBaselineBBRejectsPriorIncrementalUpdate(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	input := bytes.Replace(minimalPDF(t, 1, 2, 3), []byte("/Root 1 0 R >>"), []byte("/Root 1 0 R /Prev 12 >>"), 1)
	if _, err := SignBaselineBB(input, certificate, key, SignOptions{SigningTime: certificate.NotBefore.Add(time.Hour)}); err == nil {
		t.Fatal("prior incremental update was accepted")
	}
}

func TestSignBaselineBBRejectsHybridReferencePDF(t *testing.T) {
	certificate, key := generateRSACertificate(t)
	input := bytes.Replace(minimalPDF(t, 1, 2, 3), []byte("/Root 1 0 R >>"), []byte("/Root 1 0 R /XRefStm 12 >>"), 1)
	if _, err := SignBaselineBB(input, certificate, key, SignOptions{SigningTime: certificate.NotBefore.Add(time.Hour)}); err == nil {
		t.Fatal("hybrid-reference PDF was accepted")
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
		SerialNumber: big.NewInt(84),
		Subject:      pkix.Name{CommonName: "PAdES Test Signer"},
		Issuer:       pkix.Name{CommonName: "PAdES Test Signer"},
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

func minimalPDF(t *testing.T, catalogObject, pagesObject, pageObject int) []byte {
	t.Helper()
	bodies := map[int]string{
		catalogObject: fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R /PageMode /UseNone >>", pagesObject),
		pagesObject:   fmt.Sprintf("<< /Type /Pages /Kids [%d 0 R] /Count 1 >>", pageObject),
		pageObject:    fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] >>", pagesObject),
	}
	objectNumbers := []int{catalogObject, pagesObject, pageObject}
	sort.Ints(objectNumbers)
	var document bytes.Buffer
	document.WriteString("%PDF-1.4\n")
	offsets := make(map[int]int, len(bodies))
	for _, objectNumber := range objectNumbers {
		offsets[objectNumber] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", objectNumber, bodies[objectNumber])
	}
	xrefOffset := document.Len()
	maxObject := objectNumbers[len(objectNumbers)-1]
	fmt.Fprintf(&document, "xref\n0 %d\n", maxObject+1)
	document.WriteString("0000000000 65535 f \n")
	for objectNumber := 1; objectNumber <= maxObject; objectNumber++ {
		if offset, ok := offsets[objectNumber]; ok {
			fmt.Fprintf(&document, "%010d 00000 n \n", offset)
		} else {
			document.WriteString("0000000000 65535 f \n")
		}
	}
	fmt.Fprintf(&document, "trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", maxObject+1, catalogObject, xrefOffset)
	return document.Bytes()
}
