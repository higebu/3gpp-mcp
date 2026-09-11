package tdoc

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/converter/pipeline"
)

// maxColumns is the widest a worksheet can be (column XFD); a cell
// reference past it is malformed and is dropped rather than grown into.
const maxColumns = 16384

// The TDoc list a meeting publishes is an .xlsx workbook. Only the little of
// the SpreadsheetML format that the list uses is read here — shared strings,
// inline strings and plain values — so no spreadsheet dependency is needed.

// readSheet returns the cells of one worksheet as rows of strings, the
// sheet named name when the workbook has one and the first sheet otherwise.
// Missing cells are empty strings; every row is padded to the widest row.
func readSheet(data []byte, name string) ([][]string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open workbook: %w", err)
	}
	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[strings.TrimPrefix(f.Name, "/")] = f
	}
	read := func(p string) ([]byte, error) {
		f, ok := files[p]
		if !ok {
			return nil, fmt.Errorf("workbook has no %s", p)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("open %s: %w", p, err)
		}
		defer rc.Close()
		// The workbook comes from the network: bound the inflated size like
		// every other archive part, so a crafted file cannot exhaust memory.
		if f.UncompressedSize64 > uint64(pipeline.MaxZipSize()) {
			return nil, fmt.Errorf("%s exceeds %d MB", p, pipeline.MaxZipSize()>>20)
		}
		b, err := readAllLimited(rc, pipeline.MaxZipSize())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}
		return b, nil
	}

	sheetPath, err := locateSheet(read, name)
	if err != nil {
		return nil, err
	}
	var shared []string
	if _, ok := files["xl/sharedStrings.xml"]; ok {
		b, err := read("xl/sharedStrings.xml")
		if err != nil {
			return nil, err
		}
		if shared, err = parseSharedStrings(b); err != nil {
			return nil, err
		}
	}
	b, err := read(sheetPath)
	if err != nil {
		return nil, err
	}
	return parseSheet(b, shared)
}

// locateSheet resolves the worksheet part through workbook.xml and its
// relationships.
func locateSheet(read func(string) ([]byte, error), name string) (string, error) {
	wb, err := read("xl/workbook.xml")
	if err != nil {
		return "", err
	}
	var workbook struct {
		Sheets []struct {
			Name string `xml:"name,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"sheets>sheet"`
	}
	if err := xml.Unmarshal(wb, &workbook); err != nil {
		return "", fmt.Errorf("parse workbook.xml: %w", err)
	}
	if len(workbook.Sheets) == 0 {
		return "", fmt.Errorf("workbook has no sheets")
	}
	rid := workbook.Sheets[0].ID
	for _, s := range workbook.Sheets {
		if strings.EqualFold(s.Name, name) {
			rid = s.ID
			break
		}
	}

	rels, err := read("xl/_rels/workbook.xml.rels")
	if err != nil {
		return "", err
	}
	var relationships struct {
		Rels []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
		} `xml:"Relationship"`
	}
	if err := xml.Unmarshal(rels, &relationships); err != nil {
		return "", fmt.Errorf("parse workbook.xml.rels: %w", err)
	}
	for _, r := range relationships.Rels {
		if r.ID != rid {
			continue
		}
		if strings.HasPrefix(r.Target, "/") {
			return strings.TrimPrefix(r.Target, "/"), nil
		}
		return path.Join("xl", r.Target), nil
	}
	return "", fmt.Errorf("workbook sheet %s has no part", rid)
}

// parseSharedStrings flattens every <si> into one string, rich-text runs
// concatenated.
func parseSharedStrings(data []byte) ([]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var strs []string
	var cur strings.Builder
	inSI, inT := false, false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse sharedStrings.xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "si":
				inSI = true
				cur.Reset()
			case "t":
				inT = inSI
			}
		case xml.CharData:
			if inT {
				cur.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inT = false
			case "si":
				strs = append(strs, cur.String())
				inSI = false
			}
		}
	}
	return strs, nil
}

// parseSheet reads the cells of a worksheet. A cell's column comes from its
// reference ("AB12"); its value is a shared string index (t="s"), an inline
// string (t="inlineStr"), or the literal <v> text.
func parseSheet(data []byte, shared []string) ([][]string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	var rows [][]string
	var row []string
	width := 0
	var (
		inRow, inCell, inV, inT bool
		cellType, cellRef       string
		cellText                strings.Builder
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("parse worksheet: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				inRow = true
				row = nil
			case "c":
				if !inRow {
					continue
				}
				inCell = true
				cellType, cellRef = "", ""
				cellText.Reset()
				for _, a := range t.Attr {
					switch a.Name.Local {
					case "t":
						cellType = a.Value
					case "r":
						cellRef = a.Value
					}
				}
			case "v":
				inV = inCell
			case "t":
				inT = inCell
			}
		case xml.CharData:
			if inV || inT {
				cellText.Write(t)
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "v":
				inV = false
			case "t":
				inT = false
			case "c":
				if !inCell {
					continue
				}
				inCell = false
				value := cellText.String()
				if cellType == "s" {
					var idx int
					if _, err := fmt.Sscanf(value, "%d", &idx); err != nil || idx < 0 || idx >= len(shared) {
						value = ""
					} else {
						value = shared[idx]
					}
				}
				col := columnIndex(cellRef)
				if col < 0 {
					col = len(row)
				}
				if col >= maxColumns {
					continue
				}
				for len(row) <= col {
					row = append(row, "")
				}
				row[col] = value
			case "row":
				inRow = false
				rows = append(rows, row)
				if len(row) > width {
					width = len(row)
				}
			}
		}
	}
	for i, r := range rows {
		for len(r) < width {
			r = append(r, "")
		}
		rows[i] = r
	}
	return rows, nil
}

// columnIndex turns the letters of a cell reference ("AB12") into a 0-based
// column number, or -1 when the reference carries none.
func columnIndex(ref string) int {
	n := 0
	seen := false
	for _, c := range ref {
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c < 'A' || c > 'Z' {
			break
		}
		n = n*26 + int(c-'A') + 1
		seen = true
	}
	if !seen {
		return -1
	}
	return n - 1
}
