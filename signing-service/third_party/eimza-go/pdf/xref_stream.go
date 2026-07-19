// Source: github.com/KilimcininKorOglu/eimza-go, snapshot recorded in ../UPSTREAM.md.
// Local changes are documented there. MIT licensed; see ../LICENSE.
package pdf

import (
	"bytes"
	"compress/zlib"
	"fmt"
	"io"
	"strconv"
)

// parseXrefStream parses a cross-reference stream object (PDF 1.5+).
func parseXrefStream(data []byte, offset int) ([]XrefEntry, Trailer, error) {
	if offset < 0 {
		return nil, Trailer{}, fmt.Errorf("pdf: negative xref stream offset %d", offset)
	}
	if offset >= len(data) {
		return nil, Trailer{}, fmt.Errorf("pdf: xref stream offset %d beyond file size", offset)
	}

	rest := data[offset:]

	dictStart := bytes.Index(rest, []byte("<<"))
	if dictStart < 0 {
		return nil, Trailer{}, fmt.Errorf("pdf: xref stream dictionary not found")
	}
	dictEnd, err := FindDictionaryEnd(rest, dictStart)
	if err != nil {
		return nil, Trailer{}, fmt.Errorf("pdf: xref stream dictionary: %w", err)
	}
	dictBytes := rest[dictStart:dictEnd]

	xrefType, ok := DictionaryNameValue(dictBytes, "Type")
	if !ok || xrefType != "XRef" {
		return nil, Trailer{}, fmt.Errorf("pdf: xref stream must declare /Type /XRef")
	}

	trailer := parseTrailerDictionary(dictBytes)
	if trailer.Size <= 0 || trailer.Root <= 0 {
		return nil, Trailer{}, fmt.Errorf("pdf: xref stream missing /Size or /Root")
	}

	w, present, err := parseIntArray(dictBytes, "W")
	if err != nil {
		return nil, Trailer{}, fmt.Errorf("pdf: invalid xref stream /W: %w", err)
	}
	if !present || len(w) != 3 {
		return nil, Trailer{}, fmt.Errorf("pdf: xref stream /W must have 3 elements, got %d", len(w))
	}
	for _, width := range w {
		if width < 0 || width > strconv.IntSize/8 {
			return nil, Trailer{}, fmt.Errorf("pdf: xref stream /W contains unsupported width %d", width)
		}
	}

	indexArr, present, err := parseIntArray(dictBytes, "Index")
	if err != nil {
		return nil, Trailer{}, fmt.Errorf("pdf: invalid xref stream /Index: %w", err)
	}
	if !present {
		indexArr = []int{0, trailer.Size}
	}

	streamData, err := extractStreamData(rest, dictEnd, dictBytes)
	if err != nil {
		return nil, Trailer{}, fmt.Errorf("pdf: xref stream data extraction failed: %w", err)
	}

	entrySize := w[0] + w[1] + w[2]
	if entrySize == 0 {
		return nil, Trailer{}, fmt.Errorf("pdf: xref stream entry size is 0")
	}
	if len(indexArr) == 0 || len(indexArr)%2 != 0 {
		return nil, Trailer{}, fmt.Errorf("pdf: xref stream /Index must contain start/count pairs")
	}
	expectedEntries := 0
	for index := 0; index < len(indexArr); index += 2 {
		startObject := indexArr[index]
		count := indexArr[index+1]
		if startObject < 0 || count < 0 {
			return nil, Trailer{}, fmt.Errorf("pdf: xref stream /Index contains a negative value")
		}
		if count > int(^uint(0)>>1)-startObject {
			return nil, Trailer{}, fmt.Errorf("pdf: xref stream /Index object range overflows")
		}
		if count > (len(streamData)/entrySize)-expectedEntries {
			return nil, Trailer{}, fmt.Errorf("pdf: xref stream data is truncated")
		}
		expectedEntries += count
	}
	if expectedEntries*entrySize != len(streamData) {
		return nil, Trailer{}, fmt.Errorf("pdf: xref stream data length does not match /Index and /W")
	}

	entries := make([]XrefEntry, 0, expectedEntries)
	pos := 0
	for i := 0; i < len(indexArr); i += 2 {
		startObj := indexArr[i]
		count := indexArr[i+1]
		for j := 0; j < count; j++ {
			entry := streamData[pos : pos+entrySize]
			pos += entrySize

			fieldType, err := readField(entry, 0, w[0])
			if err != nil {
				return nil, Trailer{}, err
			}
			if w[0] == 0 {
				fieldType = 1
			}
			fieldVal2, err := readField(entry, w[0], w[1])
			if err != nil {
				return nil, Trailer{}, err
			}
			fieldVal3, err := readField(entry, w[0]+w[1], w[2])
			if err != nil {
				return nil, Trailer{}, err
			}

			objNum := startObj + j

			switch fieldType {
			case 0:
				entries = append(entries, XrefEntry{ObjectNum: objNum, Offset: 0, Generation: fieldVal3, InUse: false})
			case 1:
				entries = append(entries, XrefEntry{ObjectNum: objNum, Offset: fieldVal2, Generation: fieldVal3, InUse: true})
			case 2:
				// Compressed object in ObjectStream — record but mark specially
				entries = append(entries, XrefEntry{ObjectNum: objNum, Offset: 0, Generation: 0, InUse: true})
			}
		}
	}

	return entries, trailer, nil
}

func readField(data []byte, offset, width int) (int, error) {
	if offset < 0 || width < 0 || offset > len(data)-width {
		return 0, fmt.Errorf("pdf: xref stream field exceeds entry bounds")
	}
	value := 0
	maxInt := int(^uint(0) >> 1)
	for _, current := range data[offset : offset+width] {
		if value > (maxInt-int(current))/256 {
			return 0, fmt.Errorf("pdf: xref stream field overflows platform integer")
		}
		value = value*256 + int(current)
	}
	return value, nil
}

func extractStreamData(objData []byte, dictionaryEnd int, dictionary []byte) ([]byte, error) {
	streamStart := skipWhitespaceAndComments(objData, dictionaryEnd, len(objData))
	const streamKeyword = "stream"
	if streamStart+len(streamKeyword) > len(objData) || string(objData[streamStart:streamStart+len(streamKeyword)]) != streamKeyword {
		return nil, fmt.Errorf("stream keyword not found after xref dictionary")
	}
	position := streamStart + len(streamKeyword)
	if position >= len(objData) {
		return nil, fmt.Errorf("xref stream has no data")
	}
	if objData[position] == '\r' {
		position++
		if position < len(objData) && objData[position] == '\n' {
			position++
		}
	} else if objData[position] == '\n' {
		position++
	} else {
		return nil, fmt.Errorf("xref stream keyword is not followed by an end-of-line marker")
	}

	length := extractIntValue(dictionary, "Length")
	if length <= 0 || length > len(objData)-position {
		return nil, fmt.Errorf("xref stream has an invalid direct /Length")
	}
	streamEnd := position + length
	afterData := streamEnd
	if afterData < len(objData) && objData[afterData] == '\r' {
		afterData++
		if afterData < len(objData) && objData[afterData] == '\n' {
			afterData++
		}
	} else if afterData < len(objData) && objData[afterData] == '\n' {
		afterData++
	}
	afterData = skipWhitespaceAndComments(objData, afterData, len(objData))
	const endStreamKeyword = "endstream"
	if afterData+len(endStreamKeyword) > len(objData) || string(objData[afterData:afterData+len(endStreamKeyword)]) != endStreamKeyword {
		return nil, fmt.Errorf("endstream not found at declared xref stream length")
	}

	raw := objData[position:streamEnd]
	filter, hasFilter := DictionaryNameValue(dictionary, "Filter")
	if !hasFilter {
		return raw, nil
	}
	if filter != "FlateDecode" {
		return nil, fmt.Errorf("unsupported xref stream filter /%s", filter)
	}
	return inflateData(raw)
}

func inflateData(data []byte) ([]byte, error) {
	r, err := zlib.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("zlib open failed: %w", err)
	}
	defer r.Close()

	const maxInflatedXrefBytes = 16 << 20
	result, err := io.ReadAll(io.LimitReader(r, maxInflatedXrefBytes+1))
	if err != nil {
		return nil, fmt.Errorf("zlib decompress failed: %w", err)
	}
	if len(result) > maxInflatedXrefBytes {
		return nil, fmt.Errorf("pdf: inflated xref stream exceeds %d bytes", maxInflatedXrefBytes)
	}
	return result, nil
}

func parseIntArray(dictionary []byte, key string) ([]int, bool, error) {
	start, end, ok := dictionaryValueBounds(dictionary, key)
	if !ok {
		return nil, false, nil
	}
	if start >= end || dictionary[start] != '[' || dictionary[end-1] != ']' {
		return nil, true, fmt.Errorf("value is not an array")
	}
	values := make([]int, 0)
	for index := start + 1; ; {
		index = skipWhitespaceAndComments(dictionary, index, end-1)
		if index >= end-1 {
			return values, true, nil
		}
		tokenEnd := scanRegularTokenEnd(dictionary, index, end-1)
		if tokenEnd == index {
			return nil, true, fmt.Errorf("unexpected delimiter at offset %d", index)
		}
		value, err := strconv.Atoi(string(dictionary[index:tokenEnd]))
		if err != nil {
			return nil, true, fmt.Errorf("invalid integer %q", dictionary[index:tokenEnd])
		}
		values = append(values, value)
		index = tokenEnd
	}
}
