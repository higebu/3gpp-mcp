package docx

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

const numberingXML = `<?xml version="1.0"?>
<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:abstractNum w:abstractNumId="150">
<w:multiLevelType w:val="multilevel"/>
<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1"/><w:pStyle w:val="Heading1"/></w:lvl>
<w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1.%2"/></w:lvl>
<w:lvl w:ilvl="2"><w:start w:val="1"/><w:numFmt w:val="decimal"/><w:lvlText w:val="%1.%2.%3"/></w:lvl>
</w:abstractNum>
<w:abstractNum w:abstractNumId="7">
<w:lvl w:ilvl="0"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="&#xF0B7;"/></w:lvl>
<w:lvl w:ilvl="1"><w:start w:val="3"/><w:numFmt w:val="lowerLetter"/><w:lvlText w:val="(%2)"/></w:lvl>
<w:lvl w:ilvl="2"><w:start w:val="1"/><w:numFmt w:val="upperRoman"/><w:lvlText w:val="%3."/></w:lvl>
</w:abstractNum>
<w:num w:numId="3"><w:abstractNumId w:val="150"/></w:num>
<w:num w:numId="4"><w:abstractNumId w:val="7"/></w:num>
<w:num w:numId="5"><w:abstractNumId w:val="150"/><w:lvlOverride w:ilvl="0"><w:startOverride w:val="10"/></w:lvlOverride><w:lvlOverride w:ilvl="1"><w:lvl w:ilvl="1"><w:start w:val="1"/><w:numFmt w:val="upperLetter"/><w:lvlText w:val="%1-%2"/></w:lvl></w:lvlOverride></w:num>
</w:numbering>`

const numberedStylesXML = `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:style w:type="paragraph" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:pPr><w:numPr><w:numId w:val="3"/></w:numPr></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Heading1"/><w:pPr><w:numPr><w:ilvl w:val="1"/><w:numId w:val="3"/></w:numPr></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading3"><w:name w:val="heading 3"/><w:basedOn w:val="Heading2"/><w:pPr><w:numPr><w:ilvl w:val="2"/></w:numPr></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading4"><w:name w:val="heading 4"/><w:basedOn w:val="Normal"/></w:style>
<w:style w:type="paragraph" w:styleId="ListPara"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:pPr><w:numPr><w:ilvl w:val="1"/><w:numId w:val="4"/></w:numPr></w:pPr></w:style>
</w:styles>`

func TestParseNumbering(t *testing.T) {
	defs, err := parseNumbering([]byte(numberingXML))
	if err != nil {
		t.Fatal(err)
	}
	if len(defs.abstracts) != 2 || len(defs.nums) != 3 {
		t.Fatalf("abstracts %d, nums %d", len(defs.abstracts), len(defs.nums))
	}
	l, ok := defs.level("3", 1)
	if !ok || l.start != 1 || l.format != "decimal" || l.text != "%1.%2" {
		t.Errorf("num 3 level 1 = %+v, %v", l, ok)
	}
	// Levels the abstract numbering leaves out default to decimal from 1.
	if l, ok := defs.level("3", 8); !ok || l.start != 1 || l.format != "decimal" || l.text != "" {
		t.Errorf("num 3 level 8 = %+v, %v", l, ok)
	}
	// A start override and a whole-level override.
	if l, ok := defs.level("5", 0); !ok || l.start != 10 || l.text != "%1" {
		t.Errorf("num 5 level 0 = %+v, %v", l, ok)
	}
	if l, ok := defs.level("5", 1); !ok || l.start != 1 || l.format != "upperLetter" || l.text != "%1-%2" {
		t.Errorf("num 5 level 1 = %+v, %v", l, ok)
	}
	for _, bad := range []struct {
		num  string
		ilvl int
	}{{"9", 0}, {"3", -1}, {"3", 9}} {
		if _, ok := defs.level(bad.num, bad.ilvl); ok {
			t.Errorf("level(%q, %d) found", bad.num, bad.ilvl)
		}
	}
	empty, err := parseNumbering(nil)
	if err != nil || len(empty.nums) != 0 {
		t.Errorf("empty numbering: %v, %+v", err, empty)
	}
	// A truncated file reports the error and keeps what was read before it.
	truncated := numberingXML[:strings.Index(numberingXML, `<w:num w:numId="5"`)] + "<w:num w:numId=\"5\"><w:abstractNumId"
	partial, err := parseNumbering([]byte(truncated))
	if err == nil || partial == nil || len(partial.abstracts) != 2 || len(partial.nums) != 2 {
		t.Errorf("truncated numbering: err %v, %+v", err, partial)
	}
	// A level override outside any w:num is ignored rather than crashing.
	stray := `<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:lvlOverride w:ilvl="0"><w:startOverride w:val="3"/><w:lvl w:ilvl="0"><w:start w:val="2"/></w:lvl></w:lvlOverride><w:num w:numId="1"><w:abstractNumId w:val="150"/></w:num></w:numbering>`
	if defs, err := parseNumbering([]byte(stray)); err != nil || len(defs.nums) != 1 || len(defs.nums["1"].overrides) != 0 {
		t.Errorf("stray override: err %v, %+v", err, defs)
	}
}

func TestNumbererSequence(t *testing.T) {
	defs, _ := parseNumbering([]byte(numberingXML))
	styles := parseStyleNumbering([]byte(numberedStylesXML))
	n := newNumberer(defs, styles)
	para := func(style string) paragraphInfo { return paragraphInfo{StyleID: style} }
	got := []string{
		n.paragraphNumber(para("Heading1")), // 1
		n.paragraphNumber(para("Heading2")), // 1.1
		n.paragraphNumber(para("Heading3")), // 1.1.1: ilvl from Heading3, numId inherited through Heading2
		n.paragraphNumber(para("Heading2")), // 1.2, level 3 reset
		n.paragraphNumber(para("Heading3")), // 1.2.1
		n.paragraphNumber(para("Normal")),   // "" : not numbered
		n.paragraphNumber(para("Heading4")), // "" : no numbering anywhere in its chain
		n.paragraphNumber(para("Heading1")), // 2, deeper levels reset
		n.paragraphNumber(para("Heading2")), // 2.1
		// The paragraph's own numPr wins over the style's.
		n.paragraphNumber(paragraphInfo{StyleID: "Heading2", HasNumPr: true, NumID: "0"}),                         // "" : numbering turned off
		n.paragraphNumber(paragraphInfo{StyleID: "Heading1", HasNumPr: true, NumID: "3", ILvl: 2, HasILvl: true}), // 2.1.1
		n.paragraphNumber(paragraphInfo{StyleID: "Normal", HasNumPr: true, NumID: "5", HasILvl: true}),            // 10
		n.paragraphNumber(paragraphInfo{StyleID: "Normal", HasNumPr: true, NumID: "5", ILvl: 1, HasILvl: true}),   // 10-A
		n.paragraphNumber(paragraphInfo{StyleID: "Normal", HasNumPr: true, NumID: "5", ILvl: 1, HasILvl: true}),   // 10-B
		// A bullet renders nothing but still counts; a lettered level
		// starts at its own start value.
		n.paragraphNumber(paragraphInfo{StyleID: "Normal", HasNumPr: true, NumID: "4", HasILvl: true}),
		n.paragraphNumber(para("ListPara")), // (c)
		n.paragraphNumber(para("ListPara")), // (d)
		n.paragraphNumber(paragraphInfo{StyleID: "Normal", HasNumPr: true, NumID: "4", ILvl: 2, HasILvl: true}), // I.
		// An unknown numbering instance numbers nothing.
		n.paragraphNumber(paragraphInfo{StyleID: "Normal", HasNumPr: true, NumID: "99", HasILvl: true}),
	}
	want := []string{"1", "1.1", "1.1.1", "1.2", "1.2.1", "", "", "2", "2.1", "", "2.1.1", "10", "10-A", "10-B", "", "(c)", "(d)", "I.", ""}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("sequence:\n got %q\nwant %q", got, want)
	}
	if (*numberer)(nil).paragraphNumber(para("Heading1")) != "" {
		t.Error("nil numberer numbered a paragraph")
	}
}

func TestFormatNumber(t *testing.T) {
	for _, tt := range []struct {
		v      int
		format string
		want   string
	}{
		{3, "decimal", "3"}, {3, "decimalZero", "03"}, {1, "lowerLetter", "a"}, {26, "upperLetter", "Z"}, {27, "lowerLetter", "aa"},
		{0, "lowerLetter", "0"}, {4, "lowerRoman", "iv"}, {1994, "upperRoman", "MCMXCIV"}, {0, "upperRoman", "0"}, {7, "ordinal", "7"},
	} {
		if got := formatNumber(tt.v, tt.format); got != tt.want {
			t.Errorf("formatNumber(%d, %s) = %q, want %q", tt.v, tt.format, got, tt.want)
		}
	}
}

func TestStyleNumberingInheritance(t *testing.T) {
	styles := parseStyleNumbering([]byte(numberedStylesXML))
	s, ok := resolveStyleNumbering("Heading3", styles)
	if !ok || s.numID != "3" || s.ilvl != 2 {
		t.Errorf("Heading3 = %+v, %v", s, ok)
	}
	if _, ok := resolveStyleNumbering("Heading4", styles); ok {
		t.Error("Heading4 resolved a numbering")
	}
	if _, ok := resolveStyleNumbering("Unknown", styles); ok {
		t.Error("an unknown style resolved a numbering")
	}
	// A basedOn cycle terminates.
	cyclic := map[string]styleNumbering{"A": {basedOn: "B"}, "B": {basedOn: "A"}}
	if _, ok := resolveStyleNumbering("A", cyclic); ok {
		t.Error("cycle resolved a numbering")
	}
	if len(parseStyleNumbering(nil)) != 0 {
		t.Error("empty styles parsed something")
	}
}

// numberedDocx builds a document whose headings are numbered by their
// styles, the way a meeting report is, with one heading that types its
// number and one paragraph that turns numbering off.
func numberedDocx(t *testing.T) []byte {
	t.Helper()
	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>
<w:p><w:r><w:t>Cover text.</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Opening of the meeting</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>Call for IPR</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="ListPara"/></w:pPr><w:r><w:t>a list item</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Heading2"/></w:pPr><w:r><w:t>Agreement</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Approval of Agenda</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>7.1 Typed number</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Heading1"/><w:numPr><w:numId w:val="0"/></w:numPr></w:pPr><w:r><w:t>Numbering turned off</w:t></w:r></w:p>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>Closing</w:t></w:r></w:p>
</w:body>
</w:document>`
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"></Types>`,
		"_rels/.rels":                  `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`,
		"word/document.xml":            doc,
		"word/styles.xml":              numberedStylesXML,
		"word/numbering.xml":           numberingXML,
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
	return buf.Bytes()
}

func TestNumberHeadings(t *testing.T) {
	data := numberedDocx(t)
	numbers := func(opts ParseOptions) string {
		res, err := ParseDocxFromBytesWithOptions(data, "report.docx", opts)
		if err != nil {
			t.Fatal(err)
		}
		var parts []string
		for _, s := range res.Sections {
			parts = append(parts, s.Number+"="+s.Title+"@"+s.ParentNumber)
		}
		return strings.Join(parts, " | ")
	}
	// Off (the default, as specifications are converted): the headings
	// keep their text as their number.
	want := "Opening of the meeting=Opening of the meeting@ | Call for IPR=Call for IPR@Opening of the meeting | Agreement=Agreement@Opening of the meeting | Approval of Agenda=Approval of Agenda@ | 7.1=Typed number@ | Numbering turned off=Numbering turned off@ | Closing=Closing@"
	if got := numbers(ParseOptions{}); got != want {
		t.Errorf("without NumberHeadings:\n got %s\nwant %s", got, want)
	}
	// On: the numbers Word would show, the typed number kept, the
	// paragraph with numbering turned off left alone, and the counter
	// unaffected by it. The list paragraph belongs to another numbering.
	want = "=Preamble@ | 1=Opening of the meeting@ | 1.1=Call for IPR@1 | 1.2=Agreement@1 | 2=Approval of Agenda@ | 7.1=Typed number@ | Numbering turned off=Numbering turned off@ | 4=Closing@"
	if got := numbers(ParseOptions{KeepPreamble: true, NumberHeadings: true}); got != want {
		t.Errorf("with NumberHeadings:\n got %s\nwant %s", got, want)
	}
	// A document without numbering.xml is numbered by nothing.
	var buf bytes.Buffer
	zr, _ := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	zw := zip.NewWriter(&buf)
	for _, f := range zr.File {
		if f.Name == "word/numbering.xml" {
			continue
		}
		rc, _ := f.Open()
		w, _ := zw.Create(f.Name)
		var b bytes.Buffer
		_, _ = b.ReadFrom(rc)
		rc.Close()
		_, _ = w.Write(b.Bytes())
	}
	_ = zw.Close()
	res, err := ParseDocxFromBytesWithOptions(buf.Bytes(), "plain.docx", ParseOptions{NumberHeadings: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Sections[0].Number != "Opening of the meeting" {
		t.Errorf("without numbering.xml: %q", res.Sections[0].Number)
	}
}
