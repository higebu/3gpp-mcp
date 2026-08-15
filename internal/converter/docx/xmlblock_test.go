package docx

import (
	"fmt"
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

// An unterminated "<…" joined with a line that opens markup of its own must
// not be counted as a start element: the fabricated tag both inflates the depth
// and swallows the close tag that would balance it, so the depth would never
// return to zero and the following prose would be absorbed (issue #136).
func TestXMLLineTrackerPendingJoinNotAnElement(t *testing.T) {
	prose := paragraphInfo{
		Text: "Ordinary prose paragraph.",
		Runs: []runInfo{{Text: "Ordinary prose paragraph."}},
	}

	var tr xmlLineTracker
	tr.observe(`<a><b`)
	if !tr.open || tr.depth != 1 {
		t.Fatalf("expected open tracker at depth 1, got open=%v depth=%d", tr.open, tr.depth)
	}
	tr.observe(`</a>`)
	if tr.open || tr.depth != 0 {
		t.Errorf("expected the close tag to balance the block, got open=%v depth=%d", tr.open, tr.depth)
	}
	if matchXMLContinuation(prose, &tr) {
		t.Error("expected prose not to continue the balanced block")
	}

	// The same join against a comment line: the comment is consumed normally
	// and leaves the depth alone.
	tr = xmlLineTracker{}
	tr.observe(`<tuple id="t1"`)
	tr.observe(`<!-- the tag above lost its ">" -->`)
	if tr.open || tr.inComment || tr.depth != 0 {
		t.Errorf("expected the comment consumed with no depth change, got open=%v inComment=%v depth=%d",
			tr.open, tr.inComment, tr.depth)
	}

	// A quoted "<" inside a genuine attribute spill is not a bogus join.
	tr = xmlLineTracker{}
	tr.observe(`<elem attr="a`)
	tr.observe(`< b">`)
	if tr.open || tr.depth != 1 {
		t.Errorf("expected the quoted '<' to complete one start element, got open=%v depth=%d", tr.open, tr.depth)
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

// An XML block whose closing tags are missing must not turn the rest of the
// clause into code: paragraphs absorbed only because an element stayed open
// are replayed as prose when no markup ever confirms them (issue #136).
func TestParseSections_XMLUnbalancedStopsAbsorbingProse(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("6.4\tXML body"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<presence>`),
		xmlTestPara(`<tuple id="t1">`),
	}
	const proseCount = 5
	for i := 1; i <= proseCount; i++ {
		elements = append(elements, xmlTestPara(fmt.Sprintf("Prose paragraph %d of the clause.", i)))
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 1+proseCount {
		t.Fatalf("expected the fence plus %d prose paragraphs, got %v", proseCount, content)
	}
	if !strings.HasPrefix(content[0], "```xml\n") || strings.Contains(content[0], "Prose paragraph") {
		t.Errorf("expected an xml fence holding only the markup lines, got %q", content[0])
	}
	for i := 1; i <= proseCount; i++ {
		want := fmt.Sprintf("Prose paragraph %d of the clause.", i)
		if content[i] != want {
			t.Errorf("content[%d] = %q, want %q", i, content[i], want)
		}
	}
}

// Element text content is confirmed by the markup that follows it, however
// long the run: a well-formed element whose content spans many paragraphs
// stays in one fence together with its closing tag (issue #136 follow-up).
func TestParseSections_XMLLongElementContentStaysFenced(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("6.4\tXML schema"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<xs:annotation><xs:documentation>`),
	}
	const contentLines = 6
	for i := 1; i <= contentLines; i++ {
		elements = append(elements, xmlTestPara(fmt.Sprintf("Documentation line %d.", i)))
	}
	elements = append(elements,
		xmlTestPara(`</xs:documentation></xs:annotation>`),
		xmlTestPara("Trailing prose."),
	)
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected one fence + trailing prose, got %v", content)
	}
	for i := 1; i <= contentLines; i++ {
		if !strings.Contains(content[0], fmt.Sprintf("Documentation line %d.", i)) {
			t.Errorf("expected documentation line %d inside the fence, got %q", i, content[0])
		}
	}
	if !strings.Contains(content[0], "</xs:documentation></xs:annotation>\n```") {
		t.Errorf("expected the closing tags to end the fence, got %q", content[0])
	}
	if content[1] != "Trailing prose." {
		t.Errorf("expected trailing prose outside the fence, got %q", content[1])
	}
}

// Element content interleaved with child elements is still confirmed by the
// close of the element it belongs to, so mixed content stays in one fence.
func TestParseSections_XMLMixedContentStaysFenced(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("6.6\tXML body"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<tuple id="t1">`),
		xmlTestPara("Text content of the tuple."),
		xmlTestPara(`<status>`),
		xmlTestPara(`<basic>open</basic>`),
		xmlTestPara(`</status>`),
		xmlTestPara("More text content."),
		xmlTestPara(`</tuple>`),
		xmlTestPara("Trailing prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected one fence + trailing prose, got %v", content)
	}
	for _, line := range []string{
		"Text content of the tuple.", "<basic>open</basic>", "More text content.", "</tuple>",
	} {
		if !strings.Contains(content[0], line) {
			t.Errorf("expected %q inside the fence, got %q", line, content[0])
		}
	}
	if content[1] != "Trailing prose." {
		t.Errorf("expected trailing prose outside the fence, got %q", content[1])
	}
}

// A paragraph that closes an element and opens a sibling in one go ends at the
// depth it started from, but it did close the element whose content preceded
// it: that content belongs in the fence, while text absorbed by the sibling
// that never closes does not (issue #136).
func TestParseSections_XMLCloseAndSiblingOpenConfirmsContent(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("6.8\tXML body"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<a>`),
		xmlTestPara("Legit text content of a."),
		xmlTestPara(`</a><b>`),
		xmlTestPara("Prose absorbed by the unclosed sibling."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected fence + prose, got %v", content)
	}
	if !strings.Contains(content[0], "Legit text content of a.\n</a><b>\n```") {
		t.Errorf("expected the content confirmed by the close inside the fence, got %q", content[0])
	}
	if content[1] != "Prose absorbed by the unclosed sibling." {
		t.Errorf("expected the unconfirmed paragraph replayed as prose, got %q", content[1])
	}

	// The same holds several levels in: closing back past the depth where
	// holding started confirms the content, even in one paragraph.
	elements = []bodyElement{
		xmlTestHeading("6.9\tXML body"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<a><b><c>`),
		xmlTestPara("Deeply nested text."),
		xmlTestPara(`</c></b>`),
		xmlTestPara("Trailing prose."),
	}
	sections = parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	content = sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected fence + prose, got %v", content)
	}
	if !strings.Contains(content[0], "Deeply nested text.\n</c></b>\n```") {
		t.Errorf("expected the nested content inside the fence, got %q", content[0])
	}
}

// Text that closes the element it was absorbed under, in the very paragraph
// that would otherwise be held, needs no confirmation from a later line and
// must keep its place in the fence.
func TestParseSections_XMLContentClosingItsOwnElement(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("6.10\tXML body"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<a>`),
		xmlTestPara("some text </a>"),
		xmlTestPara("Trailing prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected fence + prose, got %v", content)
	}
	if !strings.Contains(content[0], "<a>\nsome text </a>\n```") {
		t.Errorf("expected the self-closing content inside the fence, got %q", content[0])
	}
	if content[1] != "Trailing prose." {
		t.Errorf("expected trailing prose outside the fence, got %q", content[1])
	}
}

// Markup that leaves the element depth alone — a comment, a CDATA section, a
// self-closing tag — proves nothing about the open element, so it must not
// confirm the paragraphs held before it.
func TestParseSections_XMLDepthNeutralMarkupDoesNotConfirm(t *testing.T) {
	for _, neutral := range []string{`<!-- note -->`, `<![CDATA[x < y]]>`, `<br/>`} {
		elements := []bodyElement{
			xmlTestHeading("6.11\tXML body"),
			xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
			xmlTestPara(`<a>`),
			xmlTestPara("This clause text must stay prose."),
			xmlTestPara(neutral),
			xmlTestPara("Tail prose."),
		}
		sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
		content := sections[0].Content
		if len(content) != 4 {
			t.Fatalf("%s: expected fence + 3 replayed paragraphs, got %v", neutral, content)
		}
		if strings.Contains(content[0], "clause text") {
			t.Errorf("%s: prose fenced by depth-neutral markup: %q", neutral, content[0])
		}
		if content[1] != "This clause text must stay prose." || content[2] != neutral {
			t.Errorf("%s: expected the held paragraphs replayed in order, got %v", neutral, content[1:])
		}
	}
}

// Unconfirmed paragraphs must not be fenced by markup that merely turns up
// later in the clause: an unrelated tag line does not close the element they
// were absorbed under, so the clause text between the two stays prose.
func TestParseSections_XMLHeldProseNotFencedByUnrelatedMarkup(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("6.7\tXML body"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<presence>`),
		xmlTestPara("This clause text must stay prose."),
		xmlTestPara("So must this second paragraph."),
		xmlTestPara(`<status>Open</status>`),
		xmlTestPara("Tail prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 5 {
		t.Fatalf("expected the fence + 4 replayed paragraphs, got %v", content)
	}
	if !strings.HasPrefix(content[0], "```xml\n") || strings.Contains(content[0], "clause text") {
		t.Errorf("expected a fence holding only the markup lines, got %q", content[0])
	}
	for i, want := range []string{
		"This clause text must stay prose.",
		"So must this second paragraph.",
		"<status>Open</status>",
		"Tail prose.",
	} {
		if content[i+1] != want {
			t.Errorf("content[%d] = %q, want %q", i+1, content[i+1], want)
		}
	}
}

// An unterminated tag joined with the next line's comment must not fabricate
// an element: the block stays balanced and the trailing prose stays out of the
// fence (issue #136).
func TestParseSections_XMLPendingJoinKeepsDepthBalanced(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("6.5\tXML body"),
		xmlTestPara(`<?xml version="1.0" encoding="UTF-8"?>`),
		xmlTestPara(`<presence><tuple id="t1"`),
		xmlTestPara(`<!-- the tag above lost its ">" -->`),
		xmlTestPara(`</presence>`),
		xmlTestPara("Trailing prose."),
		xmlTestPara("More trailing prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 3 {
		t.Fatalf("expected fence + two prose paragraphs, got %v", content)
	}
	if !strings.Contains(content[0], `<!-- the tag above lost its ">" -->`) {
		t.Errorf("expected the comment line inside the fence, got %q", content[0])
	}
	if strings.Contains(content[0], "Trailing prose.") {
		t.Errorf("prose swallowed into the fence: %q", content[0])
	}
	if content[1] != "Trailing prose." || content[2] != "More trailing prose." {
		t.Errorf("expected both prose paragraphs intact, got %v", content[1:])
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

// The guard above works on a paragraph whose literal text starts with "$".
// A real converted formula is recognized structurally instead, so an equation
// that renders to LaTeX not starting with "$" — the common case, since the
// delimiters are added by markdownText — still ends an open block rather than
// being swallowed into the fence.
func TestParseSections_XMLNotSwallowingConvertedMath(t *testing.T) {
	elements := []bodyElement{
		xmlTestHeading("1\tFirst"),
		xmlTestPara(`<?xml version="1.0"?>`),
		xmlTestPara(`<body>`),
		{Tag: "p", Paragraph: mathPara("", `x=y`, false, []string{"where "}, nil)},
		xmlTestPara("Trailing prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	joined := strings.Join(sections[0].Content, "\n")
	if strings.Contains(joined, "```xml\n<?xml version=\"1.0\"?>\n<body>\nwhere $x=y$") {
		t.Errorf("converted math swallowed into the fence: %v", sections[0].Content)
	}
	if !strings.Contains(joined, "where $x=y$") {
		t.Errorf("expected the math paragraph preserved outside the fence, got %v", sections[0].Content)
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
