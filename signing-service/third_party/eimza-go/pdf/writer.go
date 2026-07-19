// Source: github.com/KilimcininKorOglu/eimza-go, snapshot recorded in ../UPSTREAM.md.
// Local changes are documented there. MIT licensed; see ../LICENSE.
package pdf

import (
	"fmt"
	"sort"
	"strings"
)

// IncrementalAppend adds new objects to a PDF via incremental update.
// objects maps object numbers to their serialized PDF object content
// (without "N 0 obj" / "endobj" wrappers — those are added automatically).
func IncrementalAppend(pdfData []byte, objects map[int][]byte) ([]byte, error) {
	parsed, err := Parse(pdfData)
	if err != nil {
		return nil, fmt.Errorf("pdf: incremental append parse failed: %w", err)
	}

	return IncrementalAppendParsed(parsed, objects)
}

// IncrementalAppendParsed performs incremental append on an already-parsed PDF.
func IncrementalAppendParsed(parsed *ParsedPDF, objects map[int][]byte) ([]byte, error) {
	if parsed.Trailer.HasEncrypt {
		return nil, fmt.Errorf("pdf: incremental updates to encrypted PDFs are unsupported")
	}
	if len(objects) == 0 {
		return parsed.Raw, nil
	}

	buf := make([]byte, len(parsed.Raw))
	copy(buf, parsed.Raw)

	// Ensure we start on a new line
	if len(buf) > 0 && buf[len(buf)-1] != '\n' {
		buf = append(buf, '\n')
	}

	// Sort object numbers for deterministic output
	objNums := make([]int, 0, len(objects))
	for n := range objects {
		objNums = append(objNums, n)
	}
	sort.Ints(objNums)

	// Write each object and record xref entries
	var newEntries []XrefEntry
	for _, objNum := range objNums {
		body := objects[objNum]
		offset := len(buf)

		header := fmt.Sprintf("%d 0 obj\n", objNum)
		buf = append(buf, []byte(header)...)
		buf = append(buf, body...)
		buf = append(buf, []byte("\nendobj\n")...)

		newEntries = append(newEntries, XrefEntry{
			ObjectNum:  objNum,
			Offset:     offset,
			Generation: 0,
			InUse:      true,
		})
	}

	// Calculate new size (max of old size and new object numbers)
	newSize := parsed.Trailer.Size
	for _, e := range newEntries {
		if e.ObjectNum+1 > newSize {
			newSize = e.ObjectNum + 1
		}
	}

	// Write xref table
	xrefOffset := len(buf)
	xrefBytes := WriteXrefSection(newEntries)
	buf = append(buf, xrefBytes...)

	// Write trailer while preserving identity and metadata references from the
	// previous revision.
	var trailer strings.Builder
	fmt.Fprintf(
		&trailer,
		"trailer\n<< /Size %d /Root %d %d R",
		newSize,
		parsed.Trailer.Root,
		parsed.Trailer.RootGeneration,
	)
	if parsed.Trailer.Info > 0 {
		fmt.Fprintf(&trailer, " /Info %d %d R", parsed.Trailer.Info, parsed.Trailer.InfoGeneration)
	}
	if len(parsed.Trailer.ID) > 0 {
		trailer.WriteString(" /ID ")
		trailer.Write(parsed.Trailer.ID)
	}
	fmt.Fprintf(&trailer, " /Prev %d >>\n", parsed.StartXref)
	buf = append(buf, trailer.String()...)

	// Write startxref
	buf = append(buf, []byte(fmt.Sprintf("startxref\n%d\n%%%%EOF\n", xrefOffset))...)

	return buf, nil
}

// AllocateObject returns the next available object number and increments the counter.
func (p *ParsedPDF) AllocateObject() int {
	n := p.NextObjectID
	p.NextObjectID++
	return n
}
