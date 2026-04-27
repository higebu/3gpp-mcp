package docx

import (
	"bytes"
	"encoding/xml"
	"fmt"
	htmlpkg "html"
	"strconv"
	"strings"
)

// vMergeKind represents the OOXML w:vMerge attribute state for a cell.
// The zero value means "not part of a vertical merge".
type vMergeKind int

const (
	vMergeRestart vMergeKind = iota + 1
	vMergeContinue
)

// tableInfo holds parsed data from a w:tbl element.
type tableInfo struct {
	Rows      []tableRow      // structured rows for HTML rendering
	CellParas []paragraphInfo // all paragraphs found inside table cells (used by metadata extraction)
}

// tableRow holds a parsed w:tr.
type tableRow struct {
	IsHeader bool // tblHeader flag from w:trPr
	Cells    []tableCell
}

// tableCell holds a parsed w:tc.
type tableCell struct {
	GridSpan int        // w:gridSpan w:val (defaults to 1)
	VMerge   vMergeKind // w:vMerge w:val ("restart" / "" → continue / absent → none)
	Paras    []paragraphInfo
}

// imageContext bundles the relationship and image maps used to render
// embedded image references inside table cells.
type imageContext struct {
	relMap map[string]string
	images map[string]*EmbeddedImage
}

// tableToHTML converts a parsed table into an HTML <table> string.
//
// Merged cells (w:gridSpan, w:vMerge) are preserved via colspan/rowspan;
// header rows flagged with w:tblHeader are emitted inside <thead> using <th>.
// Cell text is HTML-escaped; bold/italic/code runs are wrapped in
// <strong>/<em>/<code>. Images inside cells become <img src="image://..."> tags
// using the same URL scheme as the markdown image placeholder, so the web
// viewer rewrites them to real URLs.
func tableToHTML(info tableInfo, ctx imageContext) string {
	if len(info.Rows) == 0 {
		return ""
	}

	// Resolve column positions per cell, accounting for gridSpan, so vMerge
	// continuations can be aligned with their restart cell column.
	type cellPos struct {
		col   int
		width int
	}
	rowCellPositions := make([][]cellPos, len(info.Rows))
	for i, row := range info.Rows {
		col := 0
		positions := make([]cellPos, len(row.Cells))
		for j, c := range row.Cells {
			width := c.GridSpan
			if width < 1 {
				width = 1
			}
			positions[j] = cellPos{col: col, width: width}
			col += width
		}
		rowCellPositions[i] = positions
	}

	// Compute rowspan for each cell: 1 for non-restart cells; for restart
	// cells, count themselves plus the immediately following rows whose
	// column at the same starting position has vMerge=continue.
	rowspans := make([][]int, len(info.Rows))
	for i, row := range info.Rows {
		rowspans[i] = make([]int, len(row.Cells))
		for j, c := range row.Cells {
			if c.VMerge != vMergeRestart {
				rowspans[i][j] = 1
				continue
			}
			startCol := rowCellPositions[i][j].col
			span := 1
			for ri := i + 1; ri < len(info.Rows); ri++ {
				next := info.Rows[ri]
				matched := false
				for k, nc := range next.Cells {
					if rowCellPositions[ri][k].col == startCol && nc.VMerge == vMergeContinue {
						matched = true
						break
					}
				}
				if !matched {
					break
				}
				span++
			}
			rowspans[i][j] = span
		}
	}

	// Count leading header rows (rows with tblHeader at the top of the table).
	headerRows := 0
	for _, row := range info.Rows {
		if !row.IsHeader {
			break
		}
		headerRows++
	}

	var b strings.Builder
	b.WriteString("<table>")
	if headerRows > 0 {
		b.WriteString("<thead>")
		for i := 0; i < headerRows; i++ {
			writeRowHTML(&b, info.Rows[i], rowspans[i], true, ctx)
		}
		b.WriteString("</thead>")
	}
	if headerRows < len(info.Rows) {
		b.WriteString("<tbody>")
		for i := headerRows; i < len(info.Rows); i++ {
			writeRowHTML(&b, info.Rows[i], rowspans[i], false, ctx)
		}
		b.WriteString("</tbody>")
	}
	b.WriteString("</table>")
	return b.String()
}

func writeRowHTML(b *strings.Builder, row tableRow, rowspans []int, asHeader bool, ctx imageContext) {
	b.WriteString("<tr>")
	tag := "td"
	if asHeader {
		tag = "th"
	}
	for j, c := range row.Cells {
		if c.VMerge == vMergeContinue {
			continue
		}
		b.WriteByte('<')
		b.WriteString(tag)
		if c.GridSpan > 1 {
			fmt.Fprintf(b, ` colspan="%d"`, c.GridSpan)
		}
		if rowspans[j] > 1 {
			fmt.Fprintf(b, ` rowspan="%d"`, rowspans[j])
		}
		b.WriteByte('>')
		writeCellContent(b, c, ctx)
		b.WriteString("</")
		b.WriteString(tag)
		b.WriteByte('>')
	}
	b.WriteString("</tr>")
}

// listKind classifies a paragraph's list context based on its style ID.
type listKind int

const (
	notList listKind = iota
	bulletList
	numberList
)

// classifyListPara approximates list detection from the paragraph's StyleID.
// In 3GPP DOCX files, list paragraphs commonly use style IDs starting with
// "List" (e.g. "ListBullet", "ListNumber"). When the style ID is opaque (a
// numeric ID), the paragraph is treated as a non-list paragraph.
func classifyListPara(p paragraphInfo) listKind {
	sid := p.StyleID
	if !strings.HasPrefix(sid, "List") && !strings.HasPrefix(sid, "list") {
		return notList
	}
	if strings.Contains(sid, "Number") || strings.Contains(sid, "number") {
		return numberList
	}
	return bulletList
}

func writeCellContent(b *strings.Builder, c tableCell, ctx imageContext) {
	if len(c.Paras) == 0 {
		return
	}
	var inList = notList
	closeList := func() {
		switch inList {
		case bulletList:
			b.WriteString("</ul>")
		case numberList:
			b.WriteString("</ol>")
		}
		inList = notList
	}
	openList := func(kind listKind) {
		switch kind {
		case bulletList:
			b.WriteString("<ul>")
		case numberList:
			b.WriteString("<ol>")
		}
		inList = kind
	}

	for _, p := range c.Paras {
		kind := classifyListPara(p)
		if kind != inList {
			closeList()
			if kind != notList {
				openList(kind)
			}
		}
		switch kind {
		case notList:
			b.WriteString("<p>")
			writeParagraphInline(b, p, ctx)
			b.WriteString("</p>")
		default:
			b.WriteString("<li>")
			writeParagraphInline(b, p, ctx)
			b.WriteString("</li>")
		}
	}
	closeList()
}

func writeParagraphInline(b *strings.Builder, p paragraphInfo, ctx imageContext) {
	if len(p.Runs) > 0 {
		for _, r := range p.Runs {
			if r.Text == "" {
				continue
			}
			esc := htmlpkg.EscapeString(r.Text)
			switch {
			case r.IsCode:
				b.WriteString("<code>")
				b.WriteString(esc)
				b.WriteString("</code>")
			case r.Bold && r.Italic:
				b.WriteString("<strong><em>")
				b.WriteString(esc)
				b.WriteString("</em></strong>")
			case r.Bold:
				b.WriteString("<strong>")
				b.WriteString(esc)
				b.WriteString("</strong>")
			case r.Italic:
				b.WriteString("<em>")
				b.WriteString(esc)
				b.WriteString("</em>")
			default:
				b.WriteString(esc)
			}
		}
	} else if p.Text != "" {
		b.WriteString(htmlpkg.EscapeString(p.Text))
	}

	for _, ref := range p.Images {
		if html := imageHTML(ctx, ref); html != "" {
			b.WriteString(html)
		}
	}
}

// imageHTML returns an HTML <img> tag for an embedded image reference, using
// the same image://name?w&h URL convention as the markdown image placeholder
// so the web viewer can rewrite it to a real URL.
func imageHTML(ctx imageContext, ref imageRef) string {
	if ctx.relMap == nil {
		return ""
	}
	target, ok := ctx.relMap[ref.RID]
	if !ok {
		return ""
	}
	img, ok := ctx.images[target]
	if !ok {
		return ""
	}
	dimSuffix := ""
	dimAttrs := ""
	if ref.WidthPx > 0 && ref.HeightPx > 0 {
		dimSuffix = fmt.Sprintf("?w=%d&h=%d", ref.WidthPx, ref.HeightPx)
		dimAttrs = fmt.Sprintf(` width="%d" height="%d"`, ref.WidthPx, ref.HeightPx)
	}
	alt := img.Name
	if ref.AltText != "" {
		alt = ref.AltText
	} else if img.LLMReadable {
		alt = "Figure"
	}
	return fmt.Sprintf(`<img src="image://%s%s" alt="%s"%s>`,
		img.Name, dimSuffix, htmlpkg.EscapeString(alt), dimAttrs)
}

// extractTable parses a w:tbl XML element and returns its parsed structure.
// Used by tests; the main parser path uses parseTableFromDecoder directly.
func extractTable(data []byte) tableInfo {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := decoder.Token()
		if err != nil {
			return tableInfo{}
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "tbl" {
			return parseTableFromDecoder(decoder, se)
		}
	}
}

// parseTableFromDecoder parses a w:tbl element from an existing decoder.
// The start element has already been consumed; this reads through the matching
// end element.
//
// Nested tables inside a cell are skipped in their entirety: we don't model
// them and would otherwise corrupt the outer row/cell state.
func parseTableFromDecoder(d *xml.Decoder, _ xml.StartElement) tableInfo {
	var info tableInfo
	var currentRow *tableRow
	var currentCell *tableCell
	var inRow, inCell, inRowProps, inCellProps bool

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
			case "tbl":
				// Nested table — skip the whole subtree.
				if inCell {
					if err := d.Skip(); err == nil {
						depth--
					}
				}
			case "tr":
				if !inCell {
					inRow = true
					currentRow = &tableRow{}
				}
			case "trPr":
				if inRow && !inCell {
					inRowProps = true
				}
			case "tblHeader":
				if inRowProps && currentRow != nil {
					val := getAttrVal(t, "val")
					if val != "false" && val != "0" {
						currentRow.IsHeader = true
					}
				}
			case "tc":
				if inRow {
					inCell = true
					currentCell = &tableCell{GridSpan: 1}
				}
			case "tcPr":
				if inCell {
					inCellProps = true
				}
			case "gridSpan":
				if inCellProps && currentCell != nil {
					if v := getAttrVal(t, "val"); v != "" {
						if n, err := strconv.Atoi(v); err == nil && n > 0 {
							currentCell.GridSpan = n
						}
					}
				}
			case "vMerge":
				if inCellProps && currentCell != nil {
					val := getAttrVal(t, "val")
					if val == "restart" {
						currentCell.VMerge = vMergeRestart
					} else {
						currentCell.VMerge = vMergeContinue
					}
				}
			case "p":
				if inCell && currentCell != nil {
					pInfo := parseParagraphFromDecoder(d, t)
					depth--
					info.CellParas = append(info.CellParas, pInfo)
					currentCell.Paras = append(currentCell.Paras, pInfo)
				}
			}
		case xml.EndElement:
			depth--
			local := t.Name.Local
			switch local {
			case "tr":
				if inRow && currentRow != nil {
					info.Rows = append(info.Rows, *currentRow)
				}
				inRow = false
				currentRow = nil
			case "trPr":
				inRowProps = false
			case "tc":
				if inCell && currentRow != nil && currentCell != nil {
					currentRow.Cells = append(currentRow.Cells, *currentCell)
				}
				inCell = false
				currentCell = nil
			case "tcPr":
				inCellProps = false
			}
		}
	}

	return info
}
