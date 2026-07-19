// Source: github.com/KilimcininKorOglu/eimza-go, snapshot recorded in ../UPSTREAM.md.
// Local changes are documented there. MIT licensed; see ../LICENSE.
package pdf

import (
	"fmt"
	"sort"
)

// WriteXrefSection formats classic xref subsections for only the objects in
// this incremental update. Unchanged objects must be omitted so readers follow
// /Prev instead of treating them as newly freed entries.
func WriteXrefSection(entries []XrefEntry) []byte {
	if len(entries) == 0 {
		return nil
	}

	sorted := append([]XrefEntry(nil), entries...)
	sort.Slice(sorted, func(left, right int) bool {
		return sorted[left].ObjectNum < sorted[right].ObjectNum
	})

	var buf []byte
	buf = append(buf, []byte("xref\n")...)
	for start := 0; start < len(sorted); {
		end := start + 1
		for end < len(sorted) && sorted[end].ObjectNum == sorted[end-1].ObjectNum+1 {
			end++
		}
		buf = append(buf, []byte(fmt.Sprintf("%d %d\n", sorted[start].ObjectNum, end-start))...)
		for _, entry := range sorted[start:end] {
			marker := 'f'
			if entry.InUse {
				marker = 'n'
			}
			buf = append(buf, []byte(fmt.Sprintf("%010d %05d %c \n", entry.Offset, entry.Generation, marker))...)
		}
		start = end
	}
	return buf
}
