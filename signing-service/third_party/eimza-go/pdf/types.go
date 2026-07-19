// Source: github.com/KilimcininKorOglu/eimza-go, snapshot recorded in ../UPSTREAM.md.
// Local changes are documented there. MIT licensed; see ../LICENSE.
package pdf

// XrefEntry represents a single entry in a PDF cross-reference table.
type XrefEntry struct {
	ObjectNum  int
	Offset     int
	Generation int
	InUse      bool
}

// Trailer holds the parsed PDF trailer dictionary values.
type Trailer struct {
	Size              int
	Root              int
	RootGeneration    int
	Info              int
	InfoGeneration    int
	Encrypt           int
	EncryptGeneration int
	HasEncrypt        bool
	ID                []byte
	Prev              int
	HasPrev           bool
	XRefStm           int
	HasXRefStm        bool
}

// ParsedPDF holds the result of parsing a PDF file's structure.
type ParsedPDF struct {
	Raw          []byte
	Xref         []XrefEntry
	Trailer      Trailer
	StartXref    int
	NextObjectID int
}
