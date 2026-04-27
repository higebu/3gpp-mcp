package docx

import (
	"strings"
	"testing"
)

func TestTableToHTML_Empty(t *testing.T) {
	if got := tableToHTML(tableInfo{}, imageContext{}); got != "" {
		t.Errorf("tableToHTML(empty) = %q, want empty", got)
	}
}

func TestTableToHTML_BasicTwoRows(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc><w:p><w:r><w:t>H1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>H2</w:t></w:r></w:p></w:tc></w:tr>
		<w:tr><w:tc><w:p><w:r><w:t>v1</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>v2</w:t></w:r></w:p></w:tc></w:tr>
	</w:tbl>`
	info := extractTable([]byte(xml))
	if len(info.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(info.Rows))
	}
	html := tableToHTML(info, imageContext{})
	for _, want := range []string{
		"<table>",
		"<tbody>",
		"<tr><td><p>H1</p></td><td><p>H2</p></td></tr>",
		"<tr><td><p>v1</p></td><td><p>v2</p></td></tr>",
		"</tbody></table>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected output to contain %q\ngot: %s", want, html)
		}
	}
}

func TestTableToHTML_EmptyCell(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc><w:p><w:r><w:t>H1</w:t></w:r></w:p></w:tc><w:tc><w:p/></w:tc></w:tr>
	</w:tbl>`
	info := extractTable([]byte(xml))
	html := tableToHTML(info, imageContext{})
	if !strings.Contains(html, "<td><p>H1</p></td><td><p></p></td>") {
		t.Errorf("empty cell HTML not as expected: %s", html)
	}
}

func TestTableToHTML_NestedTableSkipped(t *testing.T) {
	// Nested tables are skipped to avoid corrupting outer row/cell state. The
	// outer cell's own paragraph is preserved; the inner table's text is dropped.
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc>
			<w:p><w:r><w:t>outer</w:t></w:r></w:p>
			<w:tbl>
				<w:tr><w:tc><w:p><w:r><w:t>inner</w:t></w:r></w:p></w:tc></w:tr>
			</w:tbl>
		</w:tc></w:tr>
	</w:tbl>`
	info := extractTable([]byte(xml))
	html := tableToHTML(info, imageContext{})
	if !strings.Contains(html, "<p>outer</p>") {
		t.Errorf("expected outer paragraph to survive: %s", html)
	}
	if strings.Contains(html, "inner") {
		t.Errorf("expected nested-table content to be dropped: %s", html)
	}
}

func TestTableToHTML_MultiParagraphCell(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc>
			<w:p><w:r><w:t>first</w:t></w:r></w:p>
			<w:p><w:r><w:t>second</w:t></w:r></w:p>
		</w:tc></w:tr>
	</w:tbl>`
	info := extractTable([]byte(xml))
	html := tableToHTML(info, imageContext{})
	if !strings.Contains(html, "<p>first</p><p>second</p>") {
		t.Errorf("expected two <p> tags in cell, got: %s", html)
	}
}

func TestTableToHTML_BoldRunInCell(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc><w:p>
			<w:r><w:rPr><w:b/></w:rPr><w:t>bold</w:t></w:r>
			<w:r><w:t> plain</w:t></w:r>
		</w:p></w:tc></w:tr>
	</w:tbl>`
	info := extractTable([]byte(xml))
	html := tableToHTML(info, imageContext{})
	if !strings.Contains(html, "<strong>bold</strong> plain") {
		t.Errorf("expected bold run wrapped in <strong>: %s", html)
	}
}

func TestTableToHTML_ItalicAndBoldItalic(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc><w:p>
			<w:r><w:rPr><w:i/></w:rPr><w:t>italic</w:t></w:r>
			<w:r><w:rPr><w:b/><w:i/></w:rPr><w:t>both</w:t></w:r>
		</w:p></w:tc></w:tr>
	</w:tbl>`
	info := extractTable([]byte(xml))
	html := tableToHTML(info, imageContext{})
	if !strings.Contains(html, "<em>italic</em>") {
		t.Errorf("expected italic run: %s", html)
	}
	if !strings.Contains(html, "<strong><em>both</em></strong>") {
		t.Errorf("expected bold+italic run: %s", html)
	}
}

func TestTableToHTML_HTMLEscape(t *testing.T) {
	// Cell text containing HTML special characters must be escaped.
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
		<w:tr><w:tc><w:p><w:r><w:t>a &lt;b&gt; &amp; "c"</w:t></w:r></w:p></w:tc></w:tr>
	</w:tbl>`
	info := extractTable([]byte(xml))
	html := tableToHTML(info, imageContext{})
	// The XML decoder unescapes entities, so the text we receive is literal
	// `a <b> & "c"`. tableToHTML must re-escape it.
	if !strings.Contains(html, `a &lt;b&gt; &amp; &#34;c&#34;`) {
		t.Errorf("expected HTML-escaped cell content, got: %s", html)
	}
}

func TestTableToHTML_Malformed(t *testing.T) {
	info := extractTable([]byte("not xml at all"))
	if len(info.Rows) != 0 {
		t.Errorf("expected empty rows for malformed input, got %d", len(info.Rows))
	}
	if got := tableToHTML(info, imageContext{}); got != "" {
		t.Errorf("expected empty HTML for empty info, got %q", got)
	}
}

// TestTableToHTML_GridSpanMergedCells verifies horizontal cell merges via
// w:gridSpan are translated to colspan attributes.
func TestTableToHTML_GridSpanMergedCells(t *testing.T) {
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
	info := extractTable([]byte(xml))
	html := tableToHTML(info, imageContext{})
	if !strings.Contains(html, `<td colspan="2"><p>merged-header</p></td>`) {
		t.Errorf("expected colspan=2 cell, got: %s", html)
	}
	if !strings.Contains(html, `<td><p>a1</p></td><td><p>a2</p></td>`) {
		t.Errorf("expected unmerged data row, got: %s", html)
	}
}

// TestTableToHTML_VMergedCells verifies vertical cell merges via w:vMerge are
// translated to rowspan on the restart cell, with continuation cells skipped.
func TestTableToHTML_VMergedCells(t *testing.T) {
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
	info := extractTable([]byte(xml))
	html := tableToHTML(info, imageContext{})
	if !strings.Contains(html, `<td rowspan="2"><p>top</p></td>`) {
		t.Errorf("expected rowspan=2 on restart cell, got: %s", html)
	}
	// Continuation row should only emit the second cell, not the merged one.
	if !strings.Contains(html, `<tr><td><p>r2</p></td></tr>`) {
		t.Errorf("expected continuation row with only r2, got: %s", html)
	}
}

// TestTableToHTML_VaryingRowWidths checks that rows with differing cell counts
// are emitted as-is (each row reflects what was in the source).
func TestTableToHTML_VaryingRowWidths(t *testing.T) {
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
	info := extractTable([]byte(xml))
	if len(info.Rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(info.Rows))
	}
	html := tableToHTML(info, imageContext{})
	for _, want := range []string{
		"<tr><td><p>H1</p></td><td><p>H2</p></td><td><p>H3</p></td></tr>",
		"<tr><td><p>only-one</p></td></tr>",
		"<tr><td><p>a</p></td><td><p>b</p></td><td><p>c</p></td><td><p>extra</p></td></tr>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("expected HTML to contain %q, got:\n%s", want, html)
		}
	}
}

// TestTableToHTML_StyledHeaderRow verifies a row with w:trPr/w:tblHeader is
// emitted inside <thead> using <th>.
func TestTableToHTML_StyledHeaderRow(t *testing.T) {
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
	info := extractTable([]byte(xml))
	if len(info.Rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(info.Rows))
	}
	if !info.Rows[0].IsHeader {
		t.Errorf("row[0] IsHeader = false, want true")
	}
	html := tableToHTML(info, imageContext{})
	if !strings.Contains(html, "<thead>") || !strings.Contains(html, "</thead>") {
		t.Errorf("expected <thead> wrapper, got: %s", html)
	}
	if !strings.Contains(html, "<th><p><strong>Name</strong></p></th>") {
		t.Errorf("expected <th> with bold Name, got: %s", html)
	}
	if !strings.Contains(html, "<tr><td><p>k1</p></td><td><p>v1</p></td></tr>") {
		t.Errorf("expected data row in tbody with <td>, got: %s", html)
	}
}

// TestTableToHTML_ImageInCell verifies that an image reference inside a cell
// is emitted as an <img src="image://..."> tag using the relMap/images context.
func TestTableToHTML_ImageInCell(t *testing.T) {
	xml := `<w:tbl xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
		xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"
		xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"
		xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">
		<w:tr><w:tc>
			<w:p><w:r>
				<w:drawing>
					<wp:inline>
						<wp:extent cx="952500" cy="952500"/>
						<a:graphic><a:graphicData>
							<a:blip r:embed="rId7"/>
						</a:graphicData></a:graphic>
					</wp:inline>
				</w:drawing>
			</w:r></w:p>
		</w:tc></w:tr>
	</w:tbl>`
	info := extractTable([]byte(xml))
	ctx := imageContext{
		relMap: map[string]string{"rId7": "media/image1.png"},
		images: map[string]*EmbeddedImage{
			"media/image1.png": {Name: "image1.png", LLMReadable: true},
		},
	}
	html := tableToHTML(info, ctx)
	if !strings.Contains(html, `<img src="image://image1.png?w=100&h=100" alt="Figure" width="100" height="100">`) {
		t.Errorf("expected <img> tag with image:// src in cell, got: %s", html)
	}
}
