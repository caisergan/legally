package pdf

import "testing"

// Phase 1B fuzz targets over the constrained PDF parser and lexical scanners.
// The invariant under fuzzing is: never panic, never index out of bounds, and
// never return a parse result with offsets outside the input. Go's fuzzing
// engine fails the target on any panic. A size cap bounds per-input work.

const fuzzMaxInput = 1 << 20

func pdfFuzzSeeds() [][]byte {
	return [][]byte{
		[]byte("%PDF-1.4\n1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
			"xref\n0 2\n0000000000 65535 f \n0000000009 00000 n \n" +
			"trailer\n<< /Size 2 /Root 1 0 R >>\nstartxref\n9\n%%EOF\n"),
		[]byte("%PDF-1.7\nstartxref\n-1\n%%EOF"),
		[]byte("%PDF-1.4\nxref\n0 1\n0000000000 65535 f \ntrailer\n<< >>\nstartxref\n9\n%%EOF"),
		[]byte("not a pdf at all"),
		[]byte("%PDF-1.4\ntrailer<< /Prev 5 /XRefStm 7 >>startxref\n99999999\n%%EOF"),
		{},
	}
}

func FuzzParse(f *testing.F) {
	for _, seed := range pdfFuzzSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzMaxInput {
			t.Skip()
		}
		parsed, err := Parse(data)
		if err != nil {
			return
		}
		if parsed == nil {
			t.Fatal("Parse returned nil result and nil error")
		}
		if parsed.StartXref < 0 || parsed.StartXref > len(data) {
			t.Fatalf("StartXref %d out of bounds for input length %d", parsed.StartXref, len(data))
		}
	})
}

func FuzzContainsName(f *testing.F) {
	for _, seed := range pdfFuzzSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzMaxInput {
			t.Skip()
		}
		_ = ContainsName(data, "Encrypt")
		_ = ContainsName(data, "AcroForm")
		_ = ContainsName(data, "ETSI.CAdES.detached")
	})
}

func FuzzFindDictionaryEnd(f *testing.F) {
	f.Add([]byte("<< /A 1 /B (nested >> not end) >> tail"))
	f.Add([]byte("<< /A << /B 2 >> >>"))
	f.Add([]byte("<<"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > fuzzMaxInput {
			t.Skip()
		}
		end, err := FindDictionaryEnd(data, 0)
		if err == nil && (end < 0 || end > len(data)) {
			t.Fatalf("FindDictionaryEnd returned out-of-bounds end %d for length %d", end, len(data))
		}
	})
}
