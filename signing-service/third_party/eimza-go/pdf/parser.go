// Source: github.com/KilimcininKorOglu/eimza-go, snapshot recorded in ../UPSTREAM.md.
// Local changes are documented there. MIT licensed; see ../LICENSE.
package pdf

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Parse reads a PDF's trailer and cross-reference table from the end of the file.
// Supports both classic xref tables and xref streams (PDF 1.5+).
func Parse(data []byte) (*ParsedPDF, error) {
	if len(data) < len("%PDF-") || !bytes.HasPrefix(data, []byte("%PDF-")) {
		return nil, fmt.Errorf("pdf: missing PDF header")
	}

	startxref, err := findStartxref(data)
	if err != nil {
		return nil, err
	}

	xref, xrefErr := parseXrefTable(data, startxref)
	var trailer Trailer
	var trailerErr error

	if xrefErr == nil {
		trailer, trailerErr = parseTrailer(data, startxref)
		if trailerErr != nil {
			return nil, trailerErr
		}
		if trailer.Size <= 0 || trailer.Root <= 0 {
			return nil, fmt.Errorf("pdf: trailer missing valid /Size or /Root")
		}
		if trailer.HasPrev && trailer.Prev <= 0 {
			return nil, fmt.Errorf("pdf: trailer contains invalid /Prev")
		}
		if trailer.HasXRefStm && trailer.XRefStm <= 0 {
			return nil, fmt.Errorf("pdf: trailer contains invalid /XRefStm")
		}
	} else {
		var streamEntries []XrefEntry
		var streamTrailer Trailer
		streamEntries, streamTrailer, trailerErr = parseXrefStream(data, startxref)
		if trailerErr != nil {
			return nil, fmt.Errorf("pdf: neither xref table nor xref stream found at offset %d", startxref)
		}
		xref = streamEntries
		trailer = streamTrailer
	}

	nextObj := trailer.Size
	for _, entry := range xref {
		if entry.ObjectNum >= nextObj {
			nextObj = entry.ObjectNum + 1
		}
	}

	return &ParsedPDF{
		Raw:          data,
		Xref:         xref,
		Trailer:      trailer,
		StartXref:    startxref,
		NextObjectID: nextObj,
	}, nil
}

// findStartxref locates the startxref value by scanning from the end of the file.
func findStartxref(data []byte) (int, error) {
	tail := data
	if len(tail) > 1024 {
		tail = tail[len(data)-1024:]
	}

	idx := bytes.LastIndex(tail, []byte("startxref"))
	if idx < 0 {
		return 0, fmt.Errorf("pdf: startxref not found")
	}

	after := tail[idx+len("startxref"):]
	after = bytes.TrimLeft(after, " \t\r\n")

	eol := bytes.IndexAny(after, "\r\n")
	if eol < 0 {
		eol = len(after)
	}

	val, err := strconv.Atoi(strings.TrimSpace(string(after[:eol])))
	if err != nil {
		return 0, fmt.Errorf("pdf: invalid startxref value: %w", err)
	}

	return val, nil
}

// parseXrefTable parses a classic xref table starting at the given offset.
func parseXrefTable(data []byte, offset int) ([]XrefEntry, error) {
	if offset < 0 {
		return nil, fmt.Errorf("pdf: negative xref offset %d", offset)
	}
	if offset >= len(data) {
		return nil, fmt.Errorf("pdf: xref offset %d beyond file size %d", offset, len(data))
	}

	rest := data[offset:]
	if !bytes.HasPrefix(bytes.TrimLeft(rest, " \t\r\n"), []byte("xref")) {
		return nil, fmt.Errorf("pdf: expected 'xref' at offset %d", offset)
	}

	lines := strings.Split(string(rest), "\n")
	lineIndex := 0
	for lineIndex < len(lines) && strings.TrimSpace(lines[lineIndex]) != "xref" {
		lineIndex++
	}
	if lineIndex == len(lines) {
		return nil, fmt.Errorf("pdf: xref marker not found")
	}
	lineIndex++

	entries := make([]XrefEntry, 0)
	seenObjects := make(map[int]struct{})
	maxInt := int(^uint(0) >> 1)
	for lineIndex < len(lines) {
		line := strings.TrimSpace(lines[lineIndex])
		if line == "" {
			lineIndex++
			continue
		}
		if isTrailerLine(line) {
			if len(entries) == 0 {
				return nil, fmt.Errorf("pdf: xref table contains no entries")
			}
			return entries, nil
		}

		parts := strings.Fields(line)
		if len(parts) != 2 {
			return nil, fmt.Errorf("pdf: invalid xref subsection header %q", line)
		}
		startObject, err := strconv.Atoi(parts[0])
		if err != nil || startObject < 0 {
			return nil, fmt.Errorf("pdf: invalid xref subsection start %q", parts[0])
		}
		count, err := strconv.Atoi(parts[1])
		if err != nil || count <= 0 {
			return nil, fmt.Errorf("pdf: invalid xref subsection count %q", parts[1])
		}
		if count-1 > maxInt-startObject {
			return nil, fmt.Errorf("pdf: xref subsection object range overflows")
		}
		if count > len(lines)-lineIndex-1 {
			return nil, fmt.Errorf("pdf: truncated xref subsection")
		}

		for entryIndex := 0; entryIndex < count; entryIndex++ {
			entryLine := strings.TrimSpace(lines[lineIndex+1+entryIndex])
			fields := strings.Fields(entryLine)
			if len(fields) != 3 {
				return nil, fmt.Errorf("pdf: invalid xref entry %q", entryLine)
			}
			entryOffset, err := strconv.Atoi(fields[0])
			if err != nil || entryOffset < 0 {
				return nil, fmt.Errorf("pdf: invalid xref entry offset %q", fields[0])
			}
			generation, err := strconv.Atoi(fields[1])
			if err != nil || generation < 0 || generation > 65535 {
				return nil, fmt.Errorf("pdf: invalid xref generation %q", fields[1])
			}
			if fields[2] != "n" && fields[2] != "f" {
				return nil, fmt.Errorf("pdf: invalid xref entry marker %q", fields[2])
			}
			if fields[2] == "n" && entryOffset >= len(data) {
				return nil, fmt.Errorf("pdf: in-use xref offset %d exceeds file size", entryOffset)
			}
			objectNumber := startObject + entryIndex
			if _, duplicate := seenObjects[objectNumber]; duplicate {
				return nil, fmt.Errorf("pdf: duplicate xref entry for object %d", objectNumber)
			}
			seenObjects[objectNumber] = struct{}{}
			entries = append(entries, XrefEntry{
				ObjectNum:  objectNumber,
				Offset:     entryOffset,
				Generation: generation,
				InUse:      fields[2] == "n",
			})
		}
		lineIndex += count + 1
	}
	return nil, fmt.Errorf("pdf: trailer not found after xref table")
}

func isTrailerLine(line string) bool {
	return line == "trailer" || strings.HasPrefix(line, "trailer ") || strings.HasPrefix(line, "trailer<<")
}

// parseTrailer extracts the trailer dictionary from the PDF data.
func parseTrailer(data []byte, xrefOffset int) (Trailer, error) {
	rest := data[xrefOffset:]
	trailerIdx := bytes.Index(rest, []byte("trailer"))
	if trailerIdx < 0 {
		return Trailer{}, fmt.Errorf("pdf: trailer not found after xref")
	}

	dictStart := bytes.Index(rest[trailerIdx:], []byte("<<"))
	if dictStart < 0 {
		return Trailer{}, fmt.Errorf("pdf: trailer dictionary not found")
	}

	dictionaryOffset := trailerIdx + dictStart
	dictionaryEnd, err := FindDictionaryEnd(rest, dictionaryOffset)
	if err != nil {
		return Trailer{}, fmt.Errorf("pdf: trailer dictionary: %w", err)
	}

	return parseTrailerDictionary(rest[dictionaryOffset:dictionaryEnd]), nil
}

func parseTrailerDictionary(dictionary []byte) Trailer {
	root, rootGeneration := extractObjRef(dictionary, "Root")
	info, infoGeneration := extractObjRef(dictionary, "Info")
	encrypt, encryptGeneration := extractObjRef(dictionary, "Encrypt")
	trailer := Trailer{
		Size:              extractIntValue(dictionary, "Size"),
		Root:              root,
		RootGeneration:    rootGeneration,
		Info:              info,
		InfoGeneration:    infoGeneration,
		Encrypt:           encrypt,
		EncryptGeneration: encryptGeneration,
		HasEncrypt:        DictionaryHasKey(dictionary, "Encrypt"),
		ID:                extractRawArrayValue(dictionary, "ID"),
		Prev:              extractIntValue(dictionary, "Prev"),
		HasPrev:           DictionaryHasKey(dictionary, "Prev"),
		XRefStm:           extractIntValue(dictionary, "XRefStm"),
		HasXRefStm:        DictionaryHasKey(dictionary, "XRefStm"),
	}
	return trailer
}

// extractIntValue extracts a direct integer value from a PDF dictionary.
func extractIntValue(dictionary []byte, key string) int {
	start, end, ok := dictionaryValueBounds(dictionary, key)
	if !ok {
		return 0
	}
	value, _ := strconv.Atoi(string(dictionary[start:end]))
	return value
}

// extractObjRef extracts a direct indirect-object reference (for example,
// "1 0 R") from a PDF dictionary.
func extractObjRef(dictionary []byte, key string) (int, int) {
	start, end, ok := dictionaryValueBounds(dictionary, key)
	if !ok {
		return 0, 0
	}
	objectEnd := scanRegularTokenEnd(dictionary, start, end)
	objectNumber, err := strconv.Atoi(string(dictionary[start:objectEnd]))
	if err != nil || objectNumber <= 0 {
		return 0, 0
	}
	generationStart := skipWhitespaceAndComments(dictionary, objectEnd, end)
	generationEnd := scanRegularTokenEnd(dictionary, generationStart, end)
	generation, err := strconv.Atoi(string(dictionary[generationStart:generationEnd]))
	if err != nil || generation < 0 {
		return 0, 0
	}
	referenceStart := skipWhitespaceAndComments(dictionary, generationEnd, end)
	referenceEnd := scanRegularTokenEnd(dictionary, referenceStart, end)
	if string(dictionary[referenceStart:referenceEnd]) != "R" || referenceEnd != end {
		return 0, 0
	}
	return objectNumber, generation
}

func extractRawArrayValue(dictionary []byte, name string) []byte {
	start, end, ok := dictionaryValueBounds(dictionary, name)
	if !ok || start >= end || dictionary[start] != '[' {
		return nil
	}
	return append([]byte(nil), dictionary[start:end]...)
}
