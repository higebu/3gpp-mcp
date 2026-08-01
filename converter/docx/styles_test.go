package docx

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

const stylesXMLNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func TestParseStyles_MonospaceFromStyleDef(t *testing.T) {
	// The 3GPP template declares the ASN.1 code font on the style definition
	// (style "PL" uses Courier New), not on each paragraph.
	data := `<w:styles ` + stylesXMLNS + `>
<w:style w:type="paragraph" w:styleId="PL">
  <w:name w:val="PL"/>
  <w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New"/></w:rPr>
</w:style>
<w:style w:type="paragraph" w:styleId="Normal">
  <w:name w:val="Normal"/>
  <w:rPr><w:rFonts w:ascii="Times New Roman" w:hAnsi="Times New Roman"/></w:rPr>
</w:style>
</w:styles>`
	names, codeStyles, err := parseStyles([]byte(data))
	if err != nil {
		t.Fatalf("parseStyles: %v", err)
	}
	if names["PL"] != "PL" {
		t.Errorf("names[PL] = %q, want %q", names["PL"], "PL")
	}
	if !codeStyles["PL"] {
		t.Error("expected PL (Courier New) to be a code style")
	}
	if codeStyles["Normal"] {
		t.Error("expected Normal (Times New Roman) not to be a code style")
	}
}

func TestParseStyles_BasedOnChain(t *testing.T) {
	data := `<w:styles ` + stylesXMLNS + `>
<w:style w:type="paragraph" w:styleId="PL">
  <w:name w:val="PL"/>
  <w:rPr><w:rFonts w:ascii="Courier New"/></w:rPr>
</w:style>
<w:style w:type="paragraph" w:styleId="ASN1">
  <w:name w:val="ASN1"/>
  <w:basedOn w:val="PL"/>
</w:style>
<w:style w:type="paragraph" w:styleId="ProseFromCode">
  <w:name w:val="ProseFromCode"/>
  <w:basedOn w:val="PL"/>
  <w:rPr><w:rFonts w:ascii="Arial"/></w:rPr>
</w:style>
<w:style w:type="paragraph" w:styleId="CycleA">
  <w:name w:val="CycleA"/>
  <w:basedOn w:val="CycleB"/>
</w:style>
<w:style w:type="paragraph" w:styleId="CycleB">
  <w:name w:val="CycleB"/>
  <w:basedOn w:val="CycleA"/>
</w:style>
</w:styles>`
	_, codeStyles, err := parseStyles([]byte(data))
	if err != nil {
		t.Fatalf("parseStyles: %v", err)
	}
	if !codeStyles["ASN1"] {
		t.Error("expected ASN1 (basedOn PL, no own fonts) to inherit monospace")
	}
	if codeStyles["ProseFromCode"] {
		t.Error("expected ProseFromCode (own Arial overrides Courier parent) not to be a code style")
	}
	if codeStyles["CycleA"] || codeStyles["CycleB"] {
		t.Error("expected basedOn cycle to terminate without monospace")
	}
}

func TestParseStyles_DocDefaults(t *testing.T) {
	data := `<w:styles ` + stylesXMLNS + `>
<w:docDefaults>
  <w:rPrDefault><w:rPr><w:rFonts w:ascii="Courier New"/></w:rPr></w:rPrDefault>
</w:docDefaults>
<w:style w:type="paragraph" w:styleId="Plain">
  <w:name w:val="Plain"/>
</w:style>
</w:styles>`
	_, codeStyles, err := parseStyles([]byte(data))
	if err != nil {
		t.Fatalf("parseStyles: %v", err)
	}
	if !codeStyles["Plain"] {
		t.Error("expected style with no fonts to fall back to monospace docDefaults")
	}
}

// TestParseDocx_StyleBasedCodeBlock exercises the full ParseDocxFromBytes path
// with a styles.xml that declares Courier New on the style definition (the
// 3GPP "PL" pattern): paragraphs referencing the style carry no inline fonts
// yet must be fenced, and an ASN1START/STOP pair must produce an asn1 fence.
func TestParseDocx_StyleBasedCodeBlock(t *testing.T) {
	const contentTypes = `<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

	const rels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

	const styles = `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="Heading 1"/></w:style>
<w:style w:type="paragraph" w:styleId="PL">
  <w:name w:val="PL"/>
  <w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New"/></w:rPr>
</w:style>
</w:styles>`

	plPara := func(text string) string {
		return `<w:p><w:pPr><w:pStyle w:val="PL"/></w:pPr><w:r><w:t xml:space="preserve">` + text + `</w:t></w:r></w:p>`
	}
	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>6 Test</w:t></w:r></w:p>
<w:p><w:r><w:t>Prose before.</w:t></w:r></w:p>
` + plPara("-- ASN1START") +
		plPara("RRCSetup-IEs ::= SEQUENCE {") +
		plPara("\tradioBearerConfig\tRadioBearerConfig") +
		plPara("}") +
		plPara("-- ASN1STOP") + `
<w:p><w:r><w:t>Prose between.</w:t></w:r></w:p>
` + plPara("plainCode line1") +
		plPara("plainCode line2") + `
<w:p><w:r><w:t>Prose after.</w:t></w:r></w:p>
</w:body>
</w:document>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"[Content_Types].xml":          contentTypes,
		"_rels/.rels":                  rels,
		"word/document.xml":            doc,
		"word/styles.xml":              styles,
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

	result, err := ParseDocxFromBytes(buf.Bytes(), "38331-test.docx")
	if err != nil {
		t.Fatalf("ParseDocxFromBytes: %v", err)
	}
	if len(result.Sections) == 0 {
		t.Fatal("no sections parsed")
	}
	content := result.Sections[0].Content
	all := strings.Join(content, "\n")
	t.Logf("section content:\n%s", all)

	wantASN1 := "```asn1\n" +
		"-- ASN1START\n" +
		"RRCSetup-IEs ::= SEQUENCE {\n" +
		"\tradioBearerConfig\tRadioBearerConfig\n" +
		"}\n" +
		"-- ASN1STOP\n" +
		"```"
	if !strings.Contains(all, wantASN1) {
		t.Errorf("expected verbatim asn1 fence:\n%s\ngot:\n%s", wantASN1, all)
	}

	// PL-styled paragraphs outside the markers must still be fenced as a
	// generic code block, purely via the styles.xml font declaration.
	wantPlain := "```\nplainCode line1\nplainCode line2\n```"
	if !strings.Contains(all, wantPlain) {
		t.Errorf("expected style-based generic fence:\n%s\ngot:\n%s", wantPlain, all)
	}

	for _, prose := range []string{"Prose before.", "Prose between.", "Prose after."} {
		if !strings.Contains(all, prose) {
			t.Errorf("expected prose %q to remain outside fences:\n%s", prose, all)
		}
	}
}
