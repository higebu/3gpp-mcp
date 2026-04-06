package docx

import (
	"bytes"
	"encoding/xml"
	"strings"
)

// tableInfo holds parsed data from a w:tbl element.
type tableInfo struct {
	Rows      [][]string      // cell text grid for markdown conversion
	CellParas []paragraphInfo // all paragraphs found inside table cells
}

// tableToMarkdown converts a w:tbl XML element to a markdown table string.
func tableToMarkdown(tblData []byte) string {
	rows := extractTableRows(tblData)
	return rowsToMarkdown(rows)
}

// rowsToMarkdown formats table rows as a markdown table.
func rowsToMarkdown(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}

	var lines []string

	// Header row
	lines = append(lines, "| "+strings.Join(rows[0], " | ")+" |")
	// Separator
	seps := make([]string, len(rows[0]))
	for i := range seps {
		seps[i] = "---"
	}
	lines = append(lines, "| "+strings.Join(seps, " | ")+" |")

	// Data rows
	headerLen := len(rows[0])
	for _, row := range rows[1:] {
		// Pad row to match header length
		for len(row) < headerLen {
			row = append(row, "")
		}
		// Truncate to header length
		row = row[:headerLen]
		lines = append(lines, "| "+strings.Join(row, " | ")+" |")
	}

	return strings.Join(lines, "\n")
}

// extractTableRows extracts rows of cell text from a w:tbl XML element.
func extractTableRows(data []byte) [][]string {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := decoder.Token()
		if err != nil {
			return nil
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "tbl" {
			return parseTableFromDecoder(decoder, se).Rows
		}
	}
}

// parseTableFromDecoder parses a w:tbl element from an existing decoder.
// The start element has already been consumed; this reads through the matching end element.
// It produces both Rows (for markdown) and CellParas (for metadata extraction) in a single pass.
func parseTableFromDecoder(d *xml.Decoder, _ xml.StartElement) tableInfo {
	var info tableInfo
	var currentRow []string
	var inRow, inCell bool
	var cellTexts []string

	depth := 1
	const maxTokens = 10_000_000 // safety bound against malformed XML
	for iter := 0; depth > 0 && iter < maxTokens; iter++ {
		tok, err := d.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			local := t.Name.Local
			switch local {
			case "tr":
				inRow = true
				currentRow = nil
			case "tc":
				if inRow {
					inCell = true
					cellTexts = nil
				}
			case "p":
				if inCell {
					// parseParagraphFromDecoder consumes all tokens through the
					// matching </w:p> end element, so depth-- accounts for that.
					pInfo := parseParagraphFromDecoder(d, t)
					depth--
					info.CellParas = append(info.CellParas, pInfo)
					// Also accumulate text for the cell's Rows entry
					if pInfo.Text != "" {
						cellTexts = append(cellTexts, pInfo.Text)
					}
				}
			}
		case xml.EndElement:
			depth--
			local := t.Name.Local
			switch local {
			case "tr":
				if inRow && currentRow != nil {
					info.Rows = append(info.Rows, currentRow)
				}
				inRow = false
			case "tc":
				if inCell {
					cellText := strings.TrimSpace(strings.Join(cellTexts, ""))
					cellText = strings.ReplaceAll(cellText, "\n", " ")
					currentRow = append(currentRow, cellText)
					inCell = false
				}
			}
		}
	}

	return info
}
