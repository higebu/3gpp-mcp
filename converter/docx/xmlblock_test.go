package docx

import (
	"strings"
	"testing"
)

func TestXMLLineRegex(t *testing.T) {
	tests := []struct {
		text      string
		strong    bool
		candidate bool
	}{
		// Strong openers: never seen in prose, start a block by themselves.
		{`<?xml version="1.0" encoding="UTF-8"?>`, true, true},
		{`<?xml version="1.0"?>`, true, true},
		{`<!DOCTYPE MgmtTree PUBLIC "-//OMA//DTD-DM-DDF 1.2//EN"`, true, false},
		{`<!doctype html>`, true, false},
		// Candidate openers: whole-line tag/comment/DTD markup ending in ">".
		{`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`, false, true},
		{`<xcap-caps xmlns="urn:ietf:params:xml:ns:xcap-caps">`, false, true},
		{`<Value>1</Value>`, false, true},
		{`<!-- The IM CN subsystem XML body -->`, false, true},
		{`<!- broken comment marker -->`, false, true},
		{`<!— em-dash comment —>`, false, true},
		{`<!ELEMENT MgmtTree (VerDTD, Node*)>`, false, true},
		{`<!ATTLIST Node type CDATA #IMPLIED>`, false, true},
		// Not openers: prose, references, Diameter AVP lines, ASN.1.
		{`The <userid> element carries the user identity.`, false, false},
		{`<userid> is defined in clause 7.`, false, false},
		{`<xs:element name="alias"`, false, false}, // no trailing ">" — continuation only
		{`< Session-Id >`, false, false},
		{`-- ASN1START`, false, false},
		{`Foo ::= SEQUENCE {`, false, false},
		{``, false, false},
	}
	for _, tt := range tests {
		info := paragraphInfo{Text: tt.text, Runs: []runInfo{{Text: tt.text}}}
		if got := matchXMLStrongStart(info); got != tt.strong {
			t.Errorf("strong start for %q = %v, want %v", tt.text, got, tt.strong)
		}
		if got := matchXMLCandidateStart(info); got != tt.candidate {
			t.Errorf("candidate start for %q = %v, want %v", tt.text, got, tt.candidate)
		}
	}
}

func TestXMLLineTracker(t *testing.T) {
	var tr xmlLineTracker
	tr.observe(`<xs:element name="alias"`)
	if !tr.open {
		t.Error("expected tracker open after unterminated tag")
	}
	tr.observe(`type="xs:string">`)
	if tr.open {
		t.Error("expected tracker closed after the attribute continuation ends the tag")
	}
	tr.observe(`<!-- a comment spanning`)
	if !tr.open || !tr.inComment {
		t.Error("expected tracker open inside an unterminated comment")
	}
	tr.observe(`several paragraphs -->`)
	if tr.open || tr.inComment {
		t.Error("expected tracker closed after the comment ends")
	}
}

// An unterminated "<" that is not a genuine tag start — a mangled comment
// opener with no closer (TS 29.163) or a "<" in prose — must not keep the
// block open and swallow every following paragraph (issue #95).
func TestXMLLineTrackerUnterminatedNotSticky(t *testing.T) {
	prose := paragraphInfo{
		Text: "Ordinary prose paragraph.",
		Runs: []runInfo{{Text: "Ordinary prose paragraph."}},
	}

	var tr xmlLineTracker
	tr.observe(`<!-Definition of simple types`)
	if tr.open {
		t.Error("expected tracker closed after a mangled comment opener with no closer")
	}
	if matchXMLContinuation(prose, &tr) {
		t.Error("expected prose not to continue the block")
	}

	tr = xmlLineTracker{}
	tr.observe(`values where a < b hold`)
	if tr.open {
		t.Error("expected tracker closed after a '<' in prose")
	}

	// A genuine unterminated tag stays open for one continuation line only:
	// when that line does not close it either, the tag is abandoned instead
	// of absorbing everything up to the next stray ">".
	tr = xmlLineTracker{}
	tr.observe(`<mcpttinfo xmlns="urn:3gpp:ns:mcpttInfo:1.0"`)
	if !tr.open {
		t.Error("expected tracker open after an unterminated tag")
	}
	tr.observe(`this prose line has no closing angle bracket`)
	if tr.open {
		t.Error("expected the unterminated tag abandoned after a line with no '>'")
	}
	if matchXMLContinuation(prose, &tr) {
		t.Error("expected prose not to continue the block after the tag is abandoned")
	}
}

// A ">" inside a quoted attribute value does not end the tag early: the
// element depth stays balanced and following prose is not absorbed
// (issue #96).
func TestXMLLineTrackerQuotedAngleBracket(t *testing.T) {
	var tr xmlLineTracker
	tr.observe(`<sig:Object MimeType="a>b" Encoding='c>d'/>`)
	if tr.open || tr.depth != 0 {
		t.Errorf("expected closed tracker with depth 0, got open=%v depth=%d", tr.open, tr.depth)
	}
	prose := paragraphInfo{
		Text: "Ordinary prose after the element.",
		Runs: []runInfo{{Text: "Ordinary prose after the element."}},
	}
	if matchXMLContinuation(prose, &tr) {
		t.Error("expected prose not to continue the block")
	}

	// The quoted value may also span the continuation of a tag left open on
	// the previous line.
	tr = xmlLineTracker{}
	tr.observe(`<elem attr="quote holding a >`)
	if !tr.open {
		t.Error("expected tracker open while the quote and tag are unterminated")
	}
	tr.observe(`still quoted" other="x">`)
	if tr.open || tr.depth != 1 {
		t.Errorf("expected the tag completed across the quote, got open=%v depth=%d", tr.open, tr.depth)
	}
}

// An arrow inside a comment does not close it early (issue #111); the
// mangled single-dash closers still do.
func TestXMLLineTrackerCommentArrow(t *testing.T) {
	var tr xmlLineTracker
	tr.observe(`<!-- maps A -> B via <xs:element name="m"> -->`)
	if tr.open || tr.inComment || tr.depth != 0 {
		t.Errorf("expected the whole comment consumed, got open=%v inComment=%v depth=%d",
			tr.open, tr.inComment, tr.depth)
	}

	tr = xmlLineTracker{}
	tr.observe(`<!-- a comment with an arrow ->`)
	if !tr.open || !tr.inComment {
		t.Error("expected tracker still inside the comment after a bare '->'")
	}
	tr.observe(`closed with a mangled dash —>`)
	if tr.open || tr.inComment {
		t.Error("expected the mangled dash closer to end the comment")
	}
}

// Element text content on its own paragraph (the <xs:documentation> text of
// TS 24.423 clause 6.4) is absorbed while an element is open, and the fence
// closes with the root element so trailing prose stays out.
func TestParseSections_XMLElementContentSplit(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("6.4\tTemplate"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<xs:annotation><xs:documentation>Template of a`),
		xmlTestPara(`Service XML Schema`),
		xmlTestPara(`</xs:documentation></xs:annotation>`),
		xmlTestPara("Trailing prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected fence + prose, got %v", content)
	}
	if !strings.Contains(content[0], "Template of a\nService XML Schema\n</xs:documentation>") {
		t.Errorf("expected the split element content inside the fence, got %q", content[0])
	}
	if content[1] != "Trailing prose." {
		t.Errorf("expected trailing prose outside the fence, got %q", content[1])
	}
}

func xmlTestPara(text string) bodyElement {
	return bodyElement{Tag: "p", Paragraph: paragraphInfo{
		Text: text, Runs: []runInfo{{Text: text}},
	}}
}

func xmlTestHeading(text string) bodyElement {
	return bodyElement{Tag: "p", Paragraph: paragraphInfo{
		StyleID: "Heading1", Text: text, Runs: []runInfo{{Text: text}},
	}}
}

// Unstyled XML schema paragraphs (the TS 29.163 annex F shape) become one
// ```xml fence, with surrounding prose untouched.
func TestParseSections_XMLBlockUnstyled(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("F.3\tXML schema"),
		xmlTestPara("The following XML schema is used:"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`),
		xmlTestPara(`	<xs:element name="ims-3gpp"/>`),
		{Tag: "p", Paragraph: paragraphInfo{}}, // blank paragraph → blank line
		xmlTestPara(`<!- broken comment marker is still captured -->`),
		xmlTestPara(`</xs:schema>`),
		xmlTestPara("Trailing prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	want := "```xml\n" +
		`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">` + "\n" +
		"\t" + `<xs:element name="ims-3gpp"/>` + "\n" +
		"\n" +
		`<!- broken comment marker is still captured -->` + "\n" +
		`</xs:schema>` + "\n" +
		"```"
	if len(content) != 3 {
		t.Fatalf("expected prose + fence + prose, got %v", content)
	}
	if content[0] != "The following XML schema is used:" {
		t.Errorf("expected leading prose intact, got %q", content[0])
	}
	if content[1] != want {
		t.Errorf("expected xml fence %q, got %q", want, content[1])
	}
	if content[2] != "Trailing prose." {
		t.Errorf("expected trailing prose intact, got %q", content[2])
	}
}

// A mangled comment opener with no closer (the TS 29.163 shape the comment
// tracking guards against) must not keep the block open through its pending
// state: the following prose stays outside the fence (issue #95).
func TestParseSections_XMLUnterminatedNotSwallowingProse(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("F.3\tXML schema"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<!-Definition of simple types`),
		xmlTestPara("This prose paragraph must stay outside the fence."),
		xmlTestPara("And so must this one, even without a '>' anywhere."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 3 {
		t.Fatalf("expected fence + two prose paragraphs, got %v", content)
	}
	if !strings.Contains(content[0], "<!-Definition of simple types") {
		t.Errorf("expected the mangled comment line inside the fence, got %q", content[0])
	}
	if strings.Contains(content[0], "prose") {
		t.Errorf("prose swallowed into the fence: %q", content[0])
	}
	if content[1] != "This prose paragraph must stay outside the fence." {
		t.Errorf("expected first prose paragraph intact, got %q", content[1])
	}
}

// Two consecutive tag lines fence without an XML declaration (weak start),
// and bold/italic runs (TS 24.423, TS 24.801) leave no markdown markers.
func TestParseSections_XMLBoldRunsWeakStart(t *testing.T) {
	boldPara := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: text, Runs: []runInfo{{Text: text, Bold: true}},
		}}
	}
	elements := []bodyElement{
		xmlTestHeading("6.3\tXML sample"),
		boldPara(`<xcap-caps xmlns="urn:ietf:params:xml:ns:xcap-caps">`),
		boldPara(`<auids>`),
		boldPara(`</auids>`),
		boldPara(`</xcap-caps>`),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if len(sections[0].Content) != 1 {
		t.Fatalf("expected exactly 1 content entry, got %v", sections[0].Content)
	}
	c := sections[0].Content[0]
	if !strings.HasPrefix(c, "```xml\n") || !strings.HasSuffix(c, "\n```") {
		t.Errorf("expected xml fence, got %q", c)
	}
	if strings.Contains(c, "**") {
		t.Errorf("bold markers leaked into the fence: %q", c)
	}
	if !strings.Contains(c, "<auids>\n</auids>") {
		t.Errorf("expected consecutive tag lines captured verbatim, got %q", c)
	}
}

// A tag whose attributes spill onto the next paragraph (TS 38.508-1) is
// absorbed until the tag closes.
func TestParseSections_XMLAttributeContinuation(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("4.14\tXML body"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<mcpttinfo xmlns="urn:3gpp:ns:mcpttInfo:1.0"`),
		xmlTestPara(`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">`),
		xmlTestPara(`</mcpttinfo>`),
		xmlTestPara("Trailing prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected fence + prose, got %v", content)
	}
	if !strings.Contains(content[0], "<mcpttinfo xmlns=\"urn:3gpp:ns:mcpttInfo:1.0\"\nxmlns:xsi=") {
		t.Errorf("expected the attribute continuation line inside the fence, got %q", content[0])
	}
}

// A DOCTYPE with an internal subset (the OMA-DM DDF and TS 31.220 DTD shape)
// fences from the DOCTYPE line through the "]>" close.
func TestParseSections_XMLDoctypeInternalSubset(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("A.1\tDTD"),
		xmlTestPara(`<!DOCTYPE MgmtTree [`),
		xmlTestPara(`<!ELEMENT MgmtTree (VerDTD, Node*)>`),
		xmlTestPara(`<!— em-dash mangled comment —>`),
		xmlTestPara(`<!ATTLIST Node type CDATA #IMPLIED>`),
		xmlTestPara(`]>`),
		xmlTestPara("Trailing prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 2 || !strings.HasPrefix(content[0], "```xml\n") {
		t.Fatalf("expected xml fence + prose, got %v", content)
	}
	for _, line := range []string{"<!DOCTYPE MgmtTree [", "<!ELEMENT", "<!— em-dash", "]>"} {
		if !strings.Contains(content[0], line) {
			t.Errorf("expected %q inside the fence, got %q", line, content[0])
		}
	}
}

// Prose that quotes an element on its own line never fences: a single
// XML-looking paragraph is replayed as a normal paragraph.
func TestParseSections_XMLProseReferenceNotFenced(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("1\tGeneral"),
		xmlTestPara("The <userid> element carries the user identity."),
		xmlTestPara(`<userid>`),
		xmlTestPara("is encoded as a string."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	joined := strings.Join(sections[0].Content, "\n")
	if strings.Contains(joined, "```") {
		t.Errorf("expected no fence for prose references, got %v", sections[0].Content)
	}
	if len(sections[0].Content) != 3 {
		t.Errorf("expected all 3 prose paragraphs preserved, got %v", sections[0].Content)
	}
}

// A held opener candidate followed by a heading or table is replayed into the
// section it belongs to, and an open block is flushed by both.
func TestParseSections_XMLFlushedByHeadingAndTable(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("1\tFirst"),
		xmlTestPara(`<pending-tag-before-heading>`),
		xmlTestHeading("2\tSecond"),
		xmlTestPara(`<?xml version="1.0"?>`),
		xmlTestPara(`<body>`),
		{Tag: "tbl", Table: tableInfo{Rows: []tableRow{{Cells: []tableCell{{Paras: []paragraphInfo{{Text: "cell", Runs: []runInfo{{Text: "cell"}}}}}}}}}},
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if len(sections[0].Content) != 1 || strings.Contains(sections[0].Content[0], "```") {
		t.Errorf("expected the abandoned candidate as plain paragraph in first section, got %v", sections[0].Content)
	}
	second := strings.Join(sections[1].Content, "\n")
	if !strings.Contains(second, "```xml\n") || !strings.Contains(second, "cell") {
		t.Errorf("expected xml fence flushed by table plus table content, got %v", sections[1].Content)
	}
	if strings.Index(second, "```xml") > strings.Index(second, "cell") {
		t.Errorf("expected fence before table content, got %v", sections[1].Content)
	}
}

// Code-styled XML paragraphs keep their existing bare ``` fence: content
// detection must not retag the ~190 specs already fenced via style/font.
func TestParseSections_XMLCodeStyledUnchanged(t *testing.T) {
	codePara := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: text, Runs: []runInfo{{Text: text, IsCode: true}}, IsCode: true,
		}}
	}
	elements := []bodyElement{
		xmlTestHeading("10.2.2.3\tXML schema"),
		codePara(`<?xml version="1.0" encoding="UTF-8"?>`),
		codePara(`<xs:schema xmlns:xs="http://www.w3.org/2001/XMLSchema">`),
		codePara(`</xs:schema>`),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if len(sections[0].Content) != 1 {
		t.Fatalf("expected exactly 1 content entry, got %v", sections[0].Content)
	}
	c := sections[0].Content[0]
	if !strings.HasPrefix(c, "```\n") {
		t.Errorf("expected the pre-existing bare fence for code-styled XML, got %q", c)
	}
	if strings.HasPrefix(c, "```xml") {
		t.Errorf("code-styled XML must not be retagged, got %q", c)
	}
}

// An OMML math paragraph ($…$) and an image paragraph end an open block
// instead of being swallowed into the fence.
func TestParseSections_XMLNotSwallowingMathOrImages(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("1\tFirst"),
		xmlTestPara(`<?xml version="1.0"?>`),
		xmlTestPara(`<body>`),
		xmlTestPara(`$x = y$`),
		xmlTestPara(`<?xml version="1.0"?>`),
		{Tag: "p", Paragraph: paragraphInfo{
			Text:   "",
			Images: []imageRef{{RID: "rId1"}},
			Runs:   []runInfo{{Image: &imageRef{RID: "rId1"}}},
		}},
		xmlTestPara("Trailing prose."),
	}
	relMap := map[string]string{"rId1": "media/image1.png"}
	images := map[string]*EmbeddedImage{
		"media/image1.png": {Name: "image1.png", MIMEType: "image/png"},
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, relMap, images)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	joined := strings.Join(sections[0].Content, "\n")
	if strings.Contains(joined, "```xml\n<?xml version=\"1.0\"?>\n<body>\n$x") {
		t.Errorf("math paragraph swallowed into the fence: %v", sections[0].Content)
	}
	if !strings.Contains(joined, "$x = y$") {
		t.Errorf("expected math paragraph preserved outside the fence, got %v", sections[0].Content)
	}
	if !strings.Contains(joined, "image://image1.png") {
		t.Errorf("expected image placeholder preserved outside the fence, got %v", sections[0].Content)
	}
	if c := strings.Count(joined, "```xml"); c != 2 {
		t.Errorf("expected 2 separate xml fences, got %d in %v", c, sections[0].Content)
	}
}

// An XML-looking but code-styled paragraph neither confirms a held opener
// candidate nor continues an open block: code-styled XML keeps its bare
// fence in both positions.
func TestParseSections_XMLCodeStyledStopsPendingAndContinuation(t *testing.T) {
	codePara := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: text, Runs: []runInfo{{Text: text, IsCode: true}}, IsCode: true,
		}}
	}
	elements := []bodyElement{
		xmlTestHeading("1\tFirst"),
		// Unstyled candidate followed by code-styled XML: no commit.
		xmlTestPara(`<single-tag-line>`),
		codePara(`<code-styled-tag>`),
		xmlTestHeading("2\tSecond"),
		// Open block ended by code-styled XML: fence flushes before it.
		xmlTestPara(`<?xml version="1.0"?>`),
		codePara(`<code-styled-tag>`),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	first := sections[0].Content
	if len(first) != 2 || strings.Contains(first[0], "```") {
		t.Fatalf("expected abandoned candidate as plain paragraph, got %v", first)
	}
	if !strings.HasPrefix(first[1], "```\n") || strings.HasPrefix(first[1], "```xml") {
		t.Errorf("expected code-styled paragraph in a bare fence, got %q", first[1])
	}
	second := sections[1].Content
	if len(second) != 2 {
		t.Fatalf("expected xml fence + bare fence, got %v", second)
	}
	if !strings.HasPrefix(second[0], "```xml\n") || strings.Contains(second[0], "code-styled-tag") {
		t.Errorf("expected xml fence without the code-styled line, got %q", second[0])
	}
	if !strings.HasPrefix(second[1], "```\n") || !strings.Contains(second[1], "<code-styled-tag>") {
		t.Errorf("expected code-styled paragraph in a bare fence, got %q", second[1])
	}
}

// A candidate opener directly followed by a code-styled paragraph is
// abandoned and the code block forms as before.
func TestParseSections_XMLPendingAbandonedByCodePara(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("1\tFirst"),
		xmlTestPara(`<single-tag-line>`),
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "yaml: sample", Runs: []runInfo{{Text: "yaml: sample"}}, IsCode: true,
		}},
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected plain paragraph + bare fence, got %v", content)
	}
	if strings.Contains(content[0], "```") {
		t.Errorf("expected abandoned candidate as plain paragraph, got %q", content[0])
	}
	if !strings.HasPrefix(content[1], "```\n") || !strings.Contains(content[1], "yaml: sample") {
		t.Errorf("expected bare code fence after abandoned candidate, got %q", content[1])
	}
}
