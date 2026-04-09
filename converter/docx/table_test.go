package docx

import (
	"strings"
	"testing"
)

func TestRowsToMarkdown(t *testing.T) {
	tests := []struct {
		name string
		rows [][]string
		want string
	}{
		{
			name: "empty rows returns empty string",
			rows: nil,
			want: "",
		},
		{
			name: "single header row",
			rows: [][]string{{"a", "b"}},
			want: "| a | b |\n| --- | --- |",
		},
		{
			name: "header with data row",
			rows: [][]string{{"H1", "H2"}, {"v1", "v2"}},
			want: "| H1 | H2 |\n| --- | --- |\n| v1 | v2 |",
		},
		{
			name: "row padding when fewer cells than header",
			rows: [][]string{{"H1", "H2", "H3"}, {"v1"}},
			want: "| H1 | H2 | H3 |\n| --- | --- | --- |\n| v1 |  |  |",
		},
		{
			name: "row truncation when more cells than header",
			rows: [][]string{{"H1"}, {"v1", "v2", "v3"}},
			want: "| H1 |\n| --- |\n| v1 |",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rowsToMarkdown(tt.rows)
			if got != tt.want {
				t.Errorf("rowsToMarkdown:\nwant:\n%s\ngot:\n%s", tt.want, got)
			}
		})
	}
}

func TestExtractTableRows_Simple(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc><w:p><w:r><w:t>H1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>H2</w:t></w:r></w:p></w:tc></w:tr>
		<w:tr><w:tc><w:p><w:r><w:t>v1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>v2</w:t></w:r></w:p></w:tc></w:tr>
	</w:tbl>`
	rows := extractTableRows([]byte(xml))
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if len(rows[0]) != 2 || rows[0][0] != "H1" || rows[0][1] != "H2" {
		t.Errorf("header row = %v", rows[0])
	}
	if len(rows[1]) != 2 || rows[1][0] != "v1" || rows[1][1] != "v2" {
		t.Errorf("data row = %v", rows[1])
	}
}

func TestExtractTableRows_EmptyCell(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc><w:p><w:r><w:t>H1</w:t></w:r></w:p></w:tc><w:tc><w:p/></w:tc></w:tr>
	</w:tbl>`
	rows := extractTableRows([]byte(xml))
	if len(rows) != 1 || len(rows[0]) != 2 {
		t.Fatalf("rows = %v", rows)
	}
	if rows[0][1] != "" {
		t.Errorf("empty cell = %q, want empty string", rows[0][1])
	}
}

func TestExtractTableRows_NestedTable(t *testing.T) {
	// The parser does not track table nesting, so a nested w:tbl inside a cell
	// causes the inner <w:tr>/<w:tc> tokens to reset the outer row state. This
	// test pins down that behavior and asserts the parser does not panic on
	// nested input.
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc>
			<w:p><w:r><w:t>outer</w:t></w:r></w:p>
			<w:tbl>
				<w:tr><w:tc><w:p><w:r><w:t>inner</w:t></w:r></w:p></w:tc></w:tr>
			</w:tbl>
		</w:tc></w:tr>
	</w:tbl>`
	rows := extractTableRows([]byte(xml))
	if len(rows) == 0 {
		t.Fatal("expected at least one row from nested-table input")
	}
	if rows[0][0] != "inner" {
		t.Errorf("expected inner row to win over outer (current behavior), got %v", rows[0])
	}
}

func TestExtractTableRows_MultiParagraphCell(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc>
			<w:p><w:r><w:t>first</w:t></w:r></w:p>
			<w:p><w:r><w:t>second</w:t></w:r></w:p>
		</w:tc></w:tr>
	</w:tbl>`
	rows := extractTableRows([]byte(xml))
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatalf("rows = %v", rows)
	}
	if !strings.Contains(rows[0][0], "first") || !strings.Contains(rows[0][0], "second") {
		t.Errorf("cell text = %q, expected to contain both paragraphs", rows[0][0])
	}
}

func TestExtractTableRows_BoldRunInCell(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc><w:p>
			<w:r><w:rPr><w:b/></w:rPr><w:t>bold</w:t></w:r>
			<w:r><w:t> plain</w:t></w:r>
		</w:p></w:tc></w:tr>
	</w:tbl>`
	rows := extractTableRows([]byte(xml))
	if len(rows) != 1 {
		t.Fatalf("rows = %v", rows)
	}
	if rows[0][0] != "bold plain" {
		t.Errorf("cell text = %q, want %q", rows[0][0], "bold plain")
	}
}

func TestExtractTableRows_Malformed(t *testing.T) {
	// Garbage input must not panic or hang. The function returns nil if no
	// w:tbl token is found.
	rows := extractTableRows([]byte("not xml at all"))
	if rows != nil {
		t.Errorf("expected nil rows for malformed input, got %v", rows)
	}
}

func TestTableToMarkdown_RoundTrip(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc><w:p><w:r><w:t>Name</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Type</w:t></w:r></w:p></w:tc></w:tr>
		<w:tr><w:tc><w:p><w:r><w:t>foo</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>string</w:t></w:r></w:p></w:tc></w:tr>
	</w:tbl>`
	md := tableToMarkdown([]byte(xml))
	for _, want := range []string{"| Name | Type |", "| --- | --- |", "| foo | string |"} {
		if !strings.Contains(md, want) {
			t.Errorf("expected output to contain %q\n%s", want, md)
		}
	}
}

func TestTableToMarkdown_NoTable(t *testing.T) {
	if md := tableToMarkdown([]byte("garbage")); md != "" {
		t.Errorf("expected empty markdown for invalid input, got %q", md)
	}
}

// TestExtractTableRows_GridSpanMergedCells pins down current behaviour for
// tables with horizontally merged cells (w:gridSpan). 3GPP protocol flow
// tables rely on gridSpan for header rows, so this documents how the parser
// treats them today — if the behaviour changes, review the diff carefully.
func TestExtractTableRows_GridSpanMergedCells(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr>
			<w:tc>
				<w:tcPr><w:gridSpan w:val="2"/></w:tcPr>
				<w:p><w:r><w:t>merged-header</w:t></w:r></w:p>
			</w:tc>
		</w:tr>
		<w:tr>
			<w:tc><w:p><w:r><w:t>a1</w:t></w:r></w:p></w:tc>
			<w:tc><w:p><w:r><w:t>a2</w:t></w:r></w:p></w:tc>
		</w:tr>
	</w:tbl>`

	rows := extractTableRows([]byte(xml))
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0][0] != "merged-header" {
		t.Errorf("row[0] cell[0] = %q, want 'merged-header'", rows[0][0])
	}
	if len(rows[1]) != 2 || rows[1][0] != "a1" || rows[1][1] != "a2" {
		t.Errorf("row[1] = %v, want [a1 a2]", rows[1])
	}
}

// TestExtractTableRows_VMergedCells pins down behaviour for vertically merged
// cells (w:vMerge). The parser currently treats continuation cells as empty.
func TestExtractTableRows_VMergedCells(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr>
			<w:tc>
				<w:tcPr><w:vMerge w:val="restart"/></w:tcPr>
				<w:p><w:r><w:t>top</w:t></w:r></w:p>
			</w:tc>
			<w:tc><w:p><w:r><w:t>r1</w:t></w:r></w:p></w:tc>
		</w:tr>
		<w:tr>
			<w:tc>
				<w:tcPr><w:vMerge/></w:tcPr>
				<w:p/>
			</w:tc>
			<w:tc><w:p><w:r><w:t>r2</w:t></w:r></w:p></w:tc>
		</w:tr>
	</w:tbl>`

	rows := extractTableRows([]byte(xml))
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0][0] != "top" || rows[0][1] != "r1" {
		t.Errorf("row[0] = %v, want [top r1]", rows[0])
	}
	// The continuation row has an empty first cell (current behaviour, pinned).
	if rows[1][0] != "" || rows[1][1] != "r2" {
		t.Errorf("row[1] = %v, want ['' r2]", rows[1])
	}
}

// TestExtractTableRows_VaryingRowWidths verifies the parser accepts rows with
// differing cell counts without panicking, and that rowsToMarkdown pads/trims
// to the header width when rendering.
func TestExtractTableRows_VaryingRowWidths(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr>
			<w:tc><w:p><w:r><w:t>H1</w:t></w:r></w:p></w:tc>
			<w:tc><w:p><w:r><w:t>H2</w:t></w:r></w:p></w:tc>
			<w:tc><w:p><w:r><w:t>H3</w:t></w:r></w:p></w:tc>
		</w:tr>
		<w:tr>
			<w:tc><w:p><w:r><w:t>only-one</w:t></w:r></w:p></w:tc>
		</w:tr>
		<w:tr>
			<w:tc><w:p><w:r><w:t>a</w:t></w:r></w:p></w:tc>
			<w:tc><w:p><w:r><w:t>b</w:t></w:r></w:p></w:tc>
			<w:tc><w:p><w:r><w:t>c</w:t></w:r></w:p></w:tc>
			<w:tc><w:p><w:r><w:t>extra</w:t></w:r></w:p></w:tc>
		</w:tr>
	</w:tbl>`

	rows := extractTableRows([]byte(xml))
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	md := rowsToMarkdown(rows)
	// Header defines width=3, so the single-cell row pads to 3 and the 4-cell
	// row truncates to 3.
	for _, want := range []string{
		"| H1 | H2 | H3 |",
		"| only-one |  |  |",
		"| a | b | c |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("expected markdown to contain %q, got:\n%s", want, md)
		}
	}
	if strings.Contains(md, "extra") {
		t.Errorf("expected extra cell to be dropped, got:\n%s", md)
	}
}

// TestExtractTableRows_StyledHeaderRow exercises a header row with tblHeader
// styling applied via tcPr — the parser should still include it as row[0].
func TestExtractTableRows_StyledHeaderRow(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr>
			<w:trPr><w:tblHeader/></w:trPr>
			<w:tc>
				<w:tcPr><w:shd w:fill="D9D9D9"/></w:tcPr>
				<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>Name</w:t></w:r></w:p>
			</w:tc>
			<w:tc>
				<w:tcPr><w:shd w:fill="D9D9D9"/></w:tcPr>
				<w:p><w:r><w:rPr><w:b/></w:rPr><w:t>Value</w:t></w:r></w:p>
			</w:tc>
		</w:tr>
		<w:tr>
			<w:tc><w:p><w:r><w:t>k1</w:t></w:r></w:p></w:tc>
			<w:tc><w:p><w:r><w:t>v1</w:t></w:r></w:p></w:tc>
		</w:tr>
	</w:tbl>`

	rows := extractTableRows([]byte(xml))
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	if rows[0][0] != "Name" || rows[0][1] != "Value" {
		t.Errorf("header row = %v, want [Name Value]", rows[0])
	}
	if rows[1][0] != "k1" || rows[1][1] != "v1" {
		t.Errorf("data row = %v, want [k1 v1]", rows[1])
	}
}
