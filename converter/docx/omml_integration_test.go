package docx

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// TestParseDocx_OMMLMatrix builds a minimal DOCX in memory containing a table
// cell with a native OMML matrix (the exact issue #17 scenario) and a paragraph
// with inline math, then verifies the full ParseDocx path produces LaTeX
// instead of garbled concatenated numbers.
func TestParseDocx_OMMLMatrix(t *testing.T) {
	const contentTypes = `<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

	const rels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

	// A matrix [[1, j], [-1, j]] — the case that used to garble to "1j-1j".
	matrix := `<m:oMath><m:m>` +
		`<m:mr><m:e><m:r><m:t>1</m:t></m:r></m:e><m:e><m:r><m:t>j</m:t></m:r></m:e></m:mr>` +
		`<m:mr><m:e><m:r><m:t>-1</m:t></m:r></m:e><m:e><m:r><m:t>j</m:t></m:r></m:e></m:mr>` +
		`</m:m></m:oMath>`

	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math">
<w:body>
<w:p><w:pPr><w:pStyle w:val="Heading 1"/></w:pPr><w:r><w:t>6 Test</w:t></w:r></w:p>
<w:p><w:r><w:t>Precoder </w:t></w:r>` + matrix + `<w:r><w:t> applies.</w:t></w:r></w:p>
<w:tbl>
<w:tr><w:tc><w:p><w:r><w:t>W = </w:t></w:r>` + matrix + `</w:p></w:tc></w:tr>
</w:tbl>
</w:body>
</w:document>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"[Content_Types].xml":          contentTypes,
		"_rels/.rels":                  rels,
		"word/document.xml":            doc,
		"word/_rels/document.xml.rels": `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`,
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := ParseDocxFromBytes(buf.Bytes(), "38211-test.docx")
	if err != nil {
		t.Fatalf("ParseDocxFromBytes: %v", err)
	}
	if len(result.Sections) == 0 {
		t.Fatal("no sections parsed")
	}

	all := strings.Join(result.Sections[0].Content, "\n")
	t.Logf("section content:\n%s", all)

	// Paragraph math is emitted raw and delimited inline.
	rawLatex := `$\begin{matrix} 1 & j \\ -1 & j \end{matrix}$`
	if !strings.Contains(all, rawLatex) {
		t.Errorf("paragraph inline LaTeX matrix missing:\n%s", all)
	}
	// Table-cell math flows through HTML escaping (& → &amp;).
	escLatex := `$\begin{matrix} 1 &amp; j \\ -1 &amp; j \end{matrix}$`
	if !strings.Contains(all, escLatex) {
		t.Errorf("table-cell LaTeX matrix missing:\n%s", all)
	}
	// The old garbled form concatenated the entries with no structure.
	if strings.Contains(all, "1j-1j") || strings.Contains(all, "11-1") {
		t.Errorf("garbled concatenation still present:\n%s", all)
	}
}
