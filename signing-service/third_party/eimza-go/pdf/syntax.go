// Local hardening helpers for the selected eimza-go fork.
// Snapshot and local changes are recorded in ../UPSTREAM.md. MIT licensed; see ../LICENSE.
package pdf

import (
	"encoding/hex"
	"fmt"
	"strconv"
)

// ContainsName reports whether data contains a PDF name token that decodes to target.
func ContainsName(data []byte, target string) bool {
	_, _, ok := findNameToken(data, target)
	return ok
}

// DictionaryHasKey reports whether the outer dictionary contains target as a
// direct key. Names in strings, comments, arrays, nested dictionaries, or value
// positions are ignored.
func DictionaryHasKey(dictionary []byte, target string) bool {
	_, _, ok := dictionaryValueBounds(dictionary, target)
	return ok
}

// DictionaryNameValue returns a direct dictionary key's name value after
// decoding PDF #xx escapes.
func DictionaryNameValue(dictionary []byte, target string) (string, bool) {
	start, end, ok := dictionaryValueBounds(dictionary, target)
	if !ok || start >= end || dictionary[start] != '/' {
		return "", false
	}
	nameEnd := scanNameEnd(dictionary, start+1, end)
	decoded, ok := decodeName(dictionary[start+1 : nameEnd])
	if !ok || nameEnd != end {
		return "", false
	}
	return decoded, true
}

func dictionaryValueBounds(dictionary []byte, target string) (int, int, bool) {
	start := skipWhitespaceAndComments(dictionary, 0, len(dictionary))
	if start+1 >= len(dictionary) || dictionary[start] != '<' || dictionary[start+1] != '<' {
		return 0, 0, false
	}
	dictionaryEnd, err := FindDictionaryEnd(dictionary, start)
	if err != nil {
		return 0, 0, false
	}
	limit := dictionaryEnd - 2
	for index := start + 2; ; {
		index = skipWhitespaceAndComments(dictionary, index, limit)
		if index >= limit {
			return 0, 0, false
		}
		if dictionary[index] != '/' {
			return 0, 0, false
		}
		keyEnd := scanNameEnd(dictionary, index+1, limit)
		key, valid := decodeName(dictionary[index+1 : keyEnd])
		if !valid {
			return 0, 0, false
		}
		valueStart := skipWhitespaceAndComments(dictionary, keyEnd, limit)
		if valueStart >= limit {
			return 0, 0, false
		}
		valueEnd, err := skipPDFValue(dictionary, valueStart, limit)
		if err != nil {
			return 0, 0, false
		}
		if key == target {
			return valueStart, valueEnd, true
		}
		index = valueEnd
	}
}

func skipPDFValue(data []byte, start, limit int) (int, error) {
	if start < 0 || start >= limit || limit > len(data) {
		return 0, fmt.Errorf("pdf: invalid object bounds")
	}
	switch data[start] {
	case '(':
		end, err := skipLiteralString(data[:limit], start+1)
		return end, err
	case '<':
		if start+1 < limit && data[start+1] == '<' {
			return FindDictionaryEnd(data[:limit], start)
		}
		return skipHexString(data[:limit], start+1)
	case '[':
		return findArrayEnd(data[:limit], start)
	case '/':
		return scanNameEnd(data, start+1, limit), nil
	case ')', '>', ']':
		return 0, fmt.Errorf("pdf: unexpected delimiter at offset %d", start)
	}

	firstEnd := scanRegularTokenEnd(data, start, limit)
	if firstEnd == start {
		return 0, fmt.Errorf("pdf: invalid object at offset %d", start)
	}
	if _, err := strconv.Atoi(string(data[start:firstEnd])); err != nil {
		return firstEnd, nil
	}

	secondStart := skipWhitespaceAndComments(data, firstEnd, limit)
	secondEnd := scanRegularTokenEnd(data, secondStart, limit)
	if secondEnd == secondStart {
		return firstEnd, nil
	}
	if _, err := strconv.Atoi(string(data[secondStart:secondEnd])); err != nil {
		return firstEnd, nil
	}
	thirdStart := skipWhitespaceAndComments(data, secondEnd, limit)
	thirdEnd := scanRegularTokenEnd(data, thirdStart, limit)
	if string(data[thirdStart:thirdEnd]) == "R" {
		return thirdEnd, nil
	}
	return firstEnd, nil
}

func skipWhitespaceAndComments(data []byte, index, limit int) int {
	for index < limit {
		if isPDFWhitespace(data[index]) {
			index++
			continue
		}
		if data[index] != '%' {
			break
		}
		index = skipComment(data[:limit], index+1)
	}
	return index
}

func scanNameEnd(data []byte, index, limit int) int {
	for index < limit && !isPDFWhitespace(data[index]) && !isPDFDelimiter(data[index]) {
		index++
	}
	return index
}

func scanRegularTokenEnd(data []byte, index, limit int) int {
	for index < limit && !isPDFWhitespace(data[index]) && !isPDFDelimiter(data[index]) {
		index++
	}
	return index
}

func findNameToken(data []byte, target string) (int, int, bool) {
	for start := 0; start < len(data); start++ {
		if data[start] != '/' {
			continue
		}
		end := start + 1
		for end < len(data) && !isPDFWhitespace(data[end]) && !isPDFDelimiter(data[end]) {
			end++
		}
		decoded, ok := decodeName(data[start+1 : end])
		if ok && decoded == target {
			return start, end, true
		}
		start = end - 1
	}
	return 0, 0, false
}

func decodeName(value []byte) (string, bool) {
	decoded := make([]byte, 0, len(value))
	for index := 0; index < len(value); index++ {
		if value[index] != '#' {
			decoded = append(decoded, value[index])
			continue
		}
		if index+2 >= len(value) {
			return "", false
		}
		pair := []byte{value[index+1], value[index+2]}
		buffer := make([]byte, 1)
		if _, err := hex.Decode(buffer, pair); err != nil {
			return "", false
		}
		decoded = append(decoded, buffer[0])
		index += 2
	}
	return string(decoded), true
}

// FindDictionaryEnd returns the exclusive end offset of the dictionary that
// starts at start. It ignores delimiter-like bytes inside strings, comments,
// and hex strings.
func FindDictionaryEnd(data []byte, start int) (int, error) {
	if start < 0 || start+1 >= len(data) || data[start] != '<' || data[start+1] != '<' {
		return 0, fmt.Errorf("pdf: dictionary does not start at offset %d", start)
	}
	depth := 0
	for index := start; index < len(data); {
		switch data[index] {
		case '%':
			index = skipComment(data, index+1)
		case '(':
			next, err := skipLiteralString(data, index+1)
			if err != nil {
				return 0, err
			}
			index = next
		case '<':
			if index+1 < len(data) && data[index+1] == '<' {
				depth++
				index += 2
			} else {
				next, err := skipHexString(data, index+1)
				if err != nil {
					return 0, err
				}
				index = next
			}
		case '>':
			if index+1 < len(data) && data[index+1] == '>' {
				depth--
				index += 2
				if depth == 0 {
					return index, nil
				}
				if depth < 0 {
					return 0, fmt.Errorf("pdf: unbalanced dictionary at offset %d", index-2)
				}
			} else {
				index++
			}
		default:
			index++
		}
	}
	return 0, fmt.Errorf("pdf: unterminated dictionary at offset %d", start)
}

func findArrayEnd(data []byte, start int) (int, error) {
	if start < 0 || start >= len(data) || data[start] != '[' {
		return 0, fmt.Errorf("pdf: array does not start at offset %d", start)
	}
	depth := 0
	for index := start; index < len(data); {
		switch data[index] {
		case '%':
			index = skipComment(data, index+1)
		case '(':
			next, err := skipLiteralString(data, index+1)
			if err != nil {
				return 0, err
			}
			index = next
		case '<':
			if index+1 < len(data) && data[index+1] == '<' {
				next, err := FindDictionaryEnd(data, index)
				if err != nil {
					return 0, err
				}
				index = next
			} else {
				next, err := skipHexString(data, index+1)
				if err != nil {
					return 0, err
				}
				index = next
			}
		case '[':
			depth++
			index++
		case ']':
			depth--
			index++
			if depth == 0 {
				return index, nil
			}
			if depth < 0 {
				return 0, fmt.Errorf("pdf: unbalanced array at offset %d", index-1)
			}
		default:
			index++
		}
	}
	return 0, fmt.Errorf("pdf: unterminated array at offset %d", start)
}

func skipComment(data []byte, index int) int {
	for index < len(data) && data[index] != '\r' && data[index] != '\n' {
		index++
	}
	return index
}

func skipLiteralString(data []byte, index int) (int, error) {
	depth := 1
	for index < len(data) {
		switch data[index] {
		case '\\':
			index++
			if index < len(data) && data[index] == '\r' {
				index++
				if index < len(data) && data[index] == '\n' {
					index++
				}
				continue
			}
			if index < len(data) {
				index++
			}
		case '(':
			depth++
			index++
		case ')':
			depth--
			index++
			if depth == 0 {
				return index, nil
			}
		default:
			index++
		}
	}
	return 0, fmt.Errorf("pdf: unterminated literal string")
}

func skipHexString(data []byte, index int) (int, error) {
	for index < len(data) {
		if data[index] == '>' {
			return index + 1, nil
		}
		index++
	}
	return 0, fmt.Errorf("pdf: unterminated hex string")
}

func isPDFWhitespace(value byte) bool {
	switch value {
	case 0, '\t', '\n', '\f', '\r', ' ':
		return true
	default:
		return false
	}
}

func isPDFDelimiter(value byte) bool {
	switch value {
	case '(', ')', '<', '>', '[', ']', '{', '}', '/', '%':
		return true
	default:
		return false
	}
}
