package docx

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

// TestParseDocx_GroupShapeDiagramAndEquation builds a minimal DOCX in memory
// reproducing the issue #25 scenario from TS 38.101-1 clause 5.3A.3: a
// grouped VML diagram with text-box labels and no embedded picture, followed
// by a display equation whose paragraph starts with a <w:tab/> and uses
// adjacent same-VertAlign runs. It verifies the full ParseDocx path no
// longer emits garbled diagram text or a literal, unrendered <sub> tag.
func TestParseDocx_GroupShapeDiagramAndEquation(t *testing.T) {
	const contentTypes = `<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

	const rels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

	// A grouped VML diagram with two text-box labels and no blip/imagedata
	// anywhere inside it — the exact issue #25 shape (arrows/labels, no
	// embedded picture).
	diagram := `<w:r><w:pict><group>` +
		`<shape><textbox><txbxContent><w:p><w:r><w:t>Lower Edge</w:t></w:r></w:p></txbxContent></textbox></shape>` +
		`<shape><textbox><txbxContent><w:p><w:r><w:t>Resource block</w:t></w:r></w:p></txbxContent></textbox></shape>` +
		`</group></w:pict></w:r>`

	// The display equation: leading <w:tab/> plus two adjacent subscript
	// runs (a plain "edge,high" run followed by a subscript-marked space).
	equation := `<w:p><w:r><w:tab/></w:r>` +
		`<w:r><w:t>BW</w:t></w:r>` +
		`<w:r><w:rPr><w:vertAlign w:val="subscript"/></w:rPr><w:t>Channel_CA</w:t></w:r>` +
		`<w:r><w:rPr><w:vertAlign w:val="subscript"/></w:rPr><w:t xml:space="preserve"> </w:t></w:r>` +
		`<w:r><w:t>= F</w:t></w:r>` +
		`<w:r><w:rPr><w:vertAlign w:val="subscript"/></w:rPr><w:t>edge,high</w:t></w:r>` +
		`<w:r><w:rPr><w:vertAlign w:val="subscript"/></w:rPr><w:t xml:space="preserve"> </w:t></w:r>` +
		`<w:r><w:t>- F</w:t></w:r>` +
		`<w:r><w:rPr><w:vertAlign w:val="subscript"/></w:rPr><w:t>edge,low</w:t></w:r>` +
		`<w:r><w:t xml:space="preserve"> (MHz).</w:t></w:r>` +
		`</w:p>`

	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:pPr><w:pStyle w:val="Heading 1"/></w:pPr><w:r><w:t>5 Test</w:t></w:r></w:p>
<w:p>` + diagram + `</w:p>
<w:p><w:r><w:t>Figure 5.3A.3-1: Definition of Aggregated Channel Bandwidth</w:t></w:r></w:p>
` + equation + `
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

	result, err := ParseDocxFromBytes(buf.Bytes(), "38101-1-test.docx")
	if err != nil {
		t.Fatalf("ParseDocxFromBytes: %v", err)
	}
	if len(result.Sections) == 0 {
		t.Fatal("no sections parsed")
	}

	all := strings.Join(result.Sections[0].Content, "\n")
	t.Logf("section content:\n%s", all)

	// The diagram's labels must not appear as jumbled plain-text prose...
	if strings.Contains(all, "Lower EdgeResource block") {
		t.Errorf("diagram labels leaked as jumbled text:\n%s", all)
	}
	// ...but must still be surfaced via the diagnostic placeholder.
	if !strings.Contains(all, "[Figure: diagram not extracted") ||
		!strings.Contains(all, "Lower Edge") || !strings.Contains(all, "Resource block") {
		t.Errorf("expected diagram placeholder with labels:\n%s", all)
	}

	// The equation must render as normal markdown with real <sub> tags, not
	// inside a fenced code block (which would make them literal text).
	if strings.Contains(all, "```") {
		t.Errorf("equation must not be fenced as a code block:\n%s", all)
	}
	if !strings.Contains(all, "BW<sub>Channel_CA </sub>= F<sub>edge,high </sub>- F<sub>edge,low</sub> (MHz).") {
		t.Errorf("expected merged, unfenced subscript equation:\n%s", all)
	}
}
