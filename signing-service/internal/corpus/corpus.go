// Package corpus provides the versioned, controlled PDF corpus used by the
// Phase 1B format-conformance gate: deterministically built accepted classic-xref
// documents and one fixture per explicitly rejected class. Bytes are generated
// (not opaque blobs) so every fixture is reproducible and reviewable.
package corpus

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Version pins the corpus. Bump it whenever a fixture changes; the digest test
// then re-pins expected hashes.
const Version = "yargi-pdf-corpus-v1"

const maxControlledPDFBytes = 25 << 20

// buildClassic assembles a valid classic-xref 3-object PDF (catalog/pages/page)
// with optional extra catalog and trailer dictionary fragments. Offsets and the
// xref table stay valid so the parser accepts the structure; callers inject
// rejected-class markers through the extras.
func buildClassic(catalogExtra, trailerExtra, catalogType string) []byte {
	if catalogType == "" {
		catalogType = "Catalog"
	}
	bodies := map[int]string{
		1: fmt.Sprintf("<< /Type /%s /Pages 2 0 R /PageMode /UseNone%s >>", catalogType, catalogExtra),
		2: "<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		3: "<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >>",
	}
	order := []int{1, 2, 3}
	sort.Ints(order)
	var document bytes.Buffer
	document.WriteString("%PDF-1.4\n")
	offsets := map[int]int{}
	for _, n := range order {
		offsets[n] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", n, bodies[n])
	}
	xrefOffset := document.Len()
	maxObject := order[len(order)-1]
	fmt.Fprintf(&document, "xref\n0 %d\n", maxObject+1)
	document.WriteString("0000000000 65535 f \n")
	for n := 1; n <= maxObject; n++ {
		fmt.Fprintf(&document, "%010d 00000 n \n", offsets[n])
	}
	fmt.Fprintf(&document, "trailer\n<< /Size %d /Root 1 0 R%s >>\nstartxref\n%d\n%%%%EOF\n",
		maxObject+1, trailerExtra, xrefOffset)
	return document.Bytes()
}

func minimal() []byte { return buildClassic("", "", "") }
func withInfoDict() []byte {
	return buildClassic("", " /Info << /Producer (Yargi Asistan corpus) >>", "")
}

// Accepted returns fixtures the controlled B-B corpus signs.
func Accepted() map[string][]byte {
	return map[string][]byte{
		"accepted_minimal":   minimal(),
		"accepted_with_info": withInfoDict(),
	}
}

// Rejected returns one fixture per explicitly rejected class, each tripping a
// distinct preflight/parse rule.
func Rejected() map[string][]byte {
	nonClassicXref := swapStartxref(minimal(), 9) // points into the body, not "xref"
	malformedXref := corruptXref(minimal())
	return map[string][]byte{
		"rejected_encrypted":          buildClassic("", " /Encrypt << /Filter /Standard >>", ""),
		"rejected_acroform":           buildClassic(" /AcroForm 4 0 R", "", ""),
		"rejected_existing_signature": buildClassic(" /SubFilter /ETSI.CAdES.detached", "", ""),
		"rejected_prior_incremental":  buildClassic("", " /Prev 0", ""),
		"rejected_hybrid_reference":   buildClassic("", " /XRefStm 0", ""),
		"rejected_non_catalog_root":   buildClassic("", "", "Pages"),
		"rejected_non_classic_xref":   nonClassicXref,
		"rejected_malformed_xref":     malformedXref,
		"rejected_missing_header":     minimal()[9:],
		"rejected_empty":              {},
	}
}

// Oversized returns a >25 MiB document (not committed to disk); it is rejected by
// the size bound.
func Oversized() []byte {
	base := minimal()
	pad := bytes.Repeat([]byte("% padding\n"), (maxControlledPDFBytes/10)+16)
	return append(pad, base...)
}

func swapStartxref(data []byte, offset int) []byte {
	marker := []byte("startxref\n")
	index := bytes.LastIndex(data, marker)
	if index < 0 {
		return data
	}
	head := data[:index+len(marker)]
	tail := data[index+len(marker):]
	newlineIndex := bytes.IndexByte(tail, '\n')
	if newlineIndex < 0 {
		return data
	}
	return append(append(append([]byte{}, head...), []byte(fmt.Sprintf("%d", offset))...), tail[newlineIndex:]...)
}

func corruptXref(data []byte) []byte {
	// Drop one xref entry line so the subsection count no longer matches.
	text := string(data)
	index := strings.Index(text, "00000 n \n")
	if index < 0 {
		return data
	}
	return []byte(text[:index] + text[index+len("00000 n \n"):])
}

// Digest returns the lowercase hex SHA-256 of a fixture.
func Digest(fixture []byte) string {
	sum := sha256.Sum256(fixture)
	return hex.EncodeToString(sum[:])
}

// Manifest maps every named fixture (accepted + on-disk rejected) to its digest.
func Manifest() map[string]string {
	manifest := map[string]string{}
	for name, fixture := range Accepted() {
		manifest[name] = Digest(fixture)
	}
	for name, fixture := range Rejected() {
		manifest[name] = Digest(fixture)
	}
	return manifest
}
