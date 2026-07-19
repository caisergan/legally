package pdf

import (
	"bytes"
	"fmt"
	"testing"
)

func TestParseRejectsMissingHeader(t *testing.T) {
	if _, err := Parse([]byte("startxref\n0\n%%EOF")); err == nil {
		t.Fatal("input without PDF header was accepted")
	}
}

func TestParseRejectsNegativeStartxrefWithoutPanic(t *testing.T) {
	if _, err := Parse([]byte("%PDF-1.4\nstartxref\n-1\n%%EOF")); err == nil {
		t.Fatal("negative startxref was accepted")
	}
}

func TestContainsNameDecodesHexEscapes(t *testing.T) {
	if !ContainsName([]byte("<< /Acro#46orm 7 0 R >>"), "AcroForm") {
		t.Fatal("escaped PDF name was not decoded")
	}
	if !ContainsName([]byte("<< /Encr#79pt 8 0 R >>"), "Encrypt") {
		t.Fatal("escaped Encrypt name was not decoded")
	}
}

func TestFindDictionaryEndIgnoresStringAndCommentDelimiters(t *testing.T) {
	data := []byte("<< /Type /Catalog /Lang (a >> b \\( c) % >> comment\n /Pages 2 0 R >> trailing")
	end, err := FindDictionaryEnd(data, 0)
	if err != nil {
		t.Fatalf("FindDictionaryEnd: %v", err)
	}
	if got := string(data[:end]); !bytes.HasSuffix([]byte(got), []byte("/Pages 2 0 R >>")) {
		t.Fatalf("dictionary ended early: %q", got)
	}
}

func TestDictionaryLookupUsesDirectKeysOnly(t *testing.T) {
	dictionary := []byte("<< /Ty#70e /Cat#61log /Label (/AcroForm) /Choice /AcroForm /Nested << /Encrypt 9 0 R >> >>")
	value, ok := DictionaryNameValue(dictionary, "Type")
	if !ok || value != "Catalog" {
		t.Fatalf("escaped direct name value = %q, %v", value, ok)
	}
	if DictionaryHasKey(dictionary, "AcroForm") || DictionaryHasKey(dictionary, "Encrypt") {
		t.Fatal("name in a value or nested dictionary was treated as a direct key")
	}
}

func TestTrailerParsingIgnoresDecoyNames(t *testing.T) {
	input := minimalPDFWithTrailer("")
	input = bytes.Replace(
		input,
		[]byte("/Root 1 0 R"),
		[]byte("/Producer (/Root 99 0 R /Encrypt 7 0 R) /Nested << /Info 8 0 R >> % /XRefStm 12\n /Root 1 0 R"),
		1,
	)
	parsed, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Trailer.Root != 1 || parsed.Trailer.Info != 0 || parsed.Trailer.HasEncrypt || parsed.Trailer.HasXRefStm {
		t.Fatalf("decoy trailer names affected parsing: %+v", parsed.Trailer)
	}
}

func TestIncrementalAppendPreservesTrailerIDAndInfo(t *testing.T) {
	input := minimalPDFWithTrailer("/Info 3 0 R /ID [<0102><A0B0>]")
	parsed, err := Parse(input)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if parsed.Trailer.Info != 3 || string(parsed.Trailer.ID) != "[<0102><A0B0>]" {
		t.Fatalf("trailer not parsed: %+v", parsed.Trailer)
	}
	object := parsed.AllocateObject()
	output, err := IncrementalAppendParsed(parsed, map[int][]byte{object: []byte("<< /Type /Test >>")})
	if err != nil {
		t.Fatalf("IncrementalAppendParsed: %v", err)
	}
	if !bytes.Contains(output, []byte("/Info 3 0 R")) || !bytes.Contains(output, []byte("/ID [<0102><A0B0>]")) {
		t.Fatalf("latest trailer lost identity metadata:\n%s", output)
	}
}

func TestIncrementalAppendRejectsEncryptedPDF(t *testing.T) {
	parsed, err := Parse(minimalPDFWithTrailer("/Encrypt 4 0 R"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if _, err := IncrementalAppendParsed(parsed, map[int][]byte{5: []byte("<<>>")}); err == nil {
		t.Fatal("encrypted PDF incremental update was accepted")
	}
}

func TestParseXrefStreamRejectsTruncatedEntries(t *testing.T) {
	data := []byte("1 0 obj\n<< /Type /XRef /Size 2 /Root 1 0 R /W [1 1 1] /Index [0 2] /Length 3 >>\nstream\n\x01\x00\x00\nendstream\nendobj")
	if _, _, err := parseXrefStream(data, 0); err == nil {
		t.Fatal("truncated xref stream was accepted")
	}
}

func TestParseXrefStreamRejectsNegativeWidthsWithoutPanic(t *testing.T) {
	data := []byte("1 0 obj\n<< /Type /XRef /Size 1 /Root 1 0 R /W [-1 2 2] /Index [0 1] /Length 3 >>\nstream\n\x01\x00\x00\nendstream\nendobj")
	if _, _, err := parseXrefStream(data, 0); err == nil {
		t.Fatal("negative xref field width was accepted")
	}
}

func TestParseXrefStreamRejectsMalformedIndex(t *testing.T) {
	data := []byte("1 0 obj\n<< /Type /XRef /Size 1 /Root 1 0 R /W [1 1 1] /Index [0 nope] /Length 3 >>\nstream\n\x01\x00\x00\nendstream\nendobj")
	if _, _, err := parseXrefStream(data, 0); err == nil {
		t.Fatal("malformed xref /Index was accepted")
	}
}

func TestParseRejectsTruncatedClassicXrefSubsection(t *testing.T) {
	input := minimalPDFWithTrailer("")
	input = bytes.Replace(input, []byte("xref\n0 4\n"), []byte("xref\n0 5\n"), 1)
	if _, err := Parse(input); err == nil {
		t.Fatal("truncated classic xref subsection was accepted")
	}
}

func minimalPDFWithTrailer(extra string) []byte {
	var document bytes.Buffer
	document.WriteString("%PDF-1.4\n")
	bodies := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [] /Count 0 >>",
		"<< /Producer (test) >>",
	}
	offsets := make([]int, len(bodies)+1)
	for index, body := range bodies {
		object := index + 1
		offsets[object] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", object, body)
	}
	xrefOffset := document.Len()
	document.WriteString("xref\n0 4\n")
	document.WriteString("0000000000 65535 f \n")
	for object := 1; object <= 3; object++ {
		fmt.Fprintf(&document, "%010d 00000 n \n", offsets[object])
	}
	fmt.Fprintf(&document, "trailer\n<< /Size 4 /Root 1 0 R %s >>\nstartxref\n%d\n%%%%EOF\n", extra, xrefOffset)
	return document.Bytes()
}

func TestWriteXrefSectionOmitsUnchangedGaps(t *testing.T) {
	section := WriteXrefSection([]XrefEntry{
		{ObjectNum: 9, Offset: 100, InUse: true},
		{ObjectNum: 24, Offset: 200, InUse: true},
		{ObjectNum: 25, Offset: 300, InUse: true},
	})
	if !bytes.Contains(section, []byte("9 1\n")) || !bytes.Contains(section, []byte("24 2\n")) {
		t.Fatalf("unexpected xref subsections:\n%s", section)
	}
	if bytes.Contains(section, []byte("0000000000 65535 f")) {
		t.Fatalf("incremental xref incorrectly frees unchanged objects:\n%s", section)
	}
}
