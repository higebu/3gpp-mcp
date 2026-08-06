package docx

import (
	"strings"
	"testing"
)

func sipPara(text string) bodyElement {
	return bodyElement{Tag: "p", Paragraph: paragraphInfo{
		Text: text, Runs: []runInfo{{Text: text}},
	}}
}

func sipHeading(text string) bodyElement {
	return bodyElement{Tag: "p", Paragraph: paragraphInfo{
		StyleID: "Heading1", Text: text, Runs: []runInfo{{Text: text}},
	}}
}

func sipParse(elements []bodyElement) []*Section {
	return parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
}

func TestSIPStartLineRegex(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		// Request lines with the SIP-version token.
		{"INVITE sip:B-Party;tgrp=CGR2;trunk-context=Realm6@operator.net SIP/2.0", true},
		{"INVITE tel:+8613587654321 SIP/2.0", true},
		{"INVITE sip:bob@example.net SIP/2.0", true},
		{"REGISTER sip:registrar.home1.net SIP/2.0", true},
		// Request line without the version token (TS 25.933), including a
		// space after the URI scheme.
		{"INVITE sip: A2EA2@Iputrannode2.operator.net", true},
		{"INVITE sip:control@mrf.example.com;ccxml=http://server.example.com/conference.ccxml", true},
		// Status lines.
		{"SIP/2.0 100", true},
		{"SIP/2.0 100 Trying", true},
		{"SIP/2.0 183 Session Progress", true},
		{"SIP/2.0 486 Busy Here", true},
		// Prose that mentions SIP must not match.
		{"The UE sends an INVITE request to the P-CSCF.", false},
		{"INVITE sip:bob@example.com is then routed onwards", false},
		{"SIP/2.0 200 OK is sent to the UE", false},
		{"SIP/2.0 200 OK is sent to the UE.", false},
		{"sends an INVITE request", false},
		{"The SIP/2.0 protocol is used", false},
		{"OPTIONS for deployment are discussed below", false},
	}
	for _, tt := range tests {
		if got := sipStartLine(tt.line); got != tt.want {
			t.Errorf("sipStartLine(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

// TS 25.933 style: full SIP message with SDP body, each line its own
// Normal-styled paragraph, request line without a SIP-version token.
func TestParseSections_SIPFullMessageBlock(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tExample message"),
		sipPara("An example SIP Invite request could be represented as the following:"),
		sipPara("INVITE sip: A2EA2@Iputrannode2.operator.net"),
		sipPara("Via: SIP/2.0/SCTP 194.237.226.242:5062"),
		sipPara("From: sip: A2EA1@iwf1.operator.net"),
		sipPara("To: sip: A2EA2@Iputrannode2.operator.net"),
		sipPara("Call-ID: <BIDD1>"),
		sipPara("CSeq: 1 INVITE"),
		sipPara("Content-type: application/sdp"),
		sipPara("Content-length: 141"),
		sipPara("v=0"),
		sipPara("o= - <bidd1> 924526776692 IN IP4 194.237.226.242"),
		sipPara("s= -"),
		sipPara("c=IN IP4 194.237.226.242"),
		// Soft line break inside one paragraph.
		sipPara("t=76554467889 0\nm=app 7094 UDP/IubFP 96"),
		sipPara("a= fmtp: 96 41 38 16400 8550"),
		sipPara("where:"),
		sipPara("A2EA1 = E164 address of the ATM node"),
	}
	sections := sipParse(elements)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 4 {
		t.Fatalf("expected intro, fence, and two trailing prose paragraphs, got %v", content)
	}
	want := "```sip\n" +
		"INVITE sip: A2EA2@Iputrannode2.operator.net\n" +
		"Via: SIP/2.0/SCTP 194.237.226.242:5062\n" +
		"From: sip: A2EA1@iwf1.operator.net\n" +
		"To: sip: A2EA2@Iputrannode2.operator.net\n" +
		"Call-ID: <BIDD1>\n" +
		"CSeq: 1 INVITE\n" +
		"Content-type: application/sdp\n" +
		"Content-length: 141\n" +
		"v=0\n" +
		"o= - <bidd1> 924526776692 IN IP4 194.237.226.242\n" +
		"s= -\n" +
		"c=IN IP4 194.237.226.242\n" +
		"t=76554467889 0\nm=app 7094 UDP/IubFP 96\n" +
		"a= fmtp: 96 41 38 16400 8550\n" +
		"```"
	if content[1] != want {
		t.Errorf("expected verbatim SIP fence %q, got %q", want, content[1])
	}
	if content[2] != "where:" {
		t.Errorf("expected prose to resume at %q, got %q", "where:", content[2])
	}
}

// TR 23.850 style: a request line with nothing else, ended by prose.
func TestParseSections_SIPRequestLineOnly(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tRouting"),
		sipPara("For example as follows:"),
		sipPara("INVITE sip:B-Party;tgrp=CGR2;trunk-context=Realm6@operator.net SIP/2.0"),
		sipPara("Table 5.3.3.2-1"),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 3 {
		t.Fatalf("expected prose, fence, prose, got %v", content)
	}
	want := "```sip\nINVITE sip:B-Party;tgrp=CGR2;trunk-context=Realm6@operator.net SIP/2.0\n```"
	if content[1] != want {
		t.Errorf("expected single-line fence %q, got %q", want, content[1])
	}
}

// TR 23.700-19 style: request and status messages styled bold/italic, with
// compact-form headers and an SDP body, ended by an Editor's note.
func TestParseSections_SIPCompactHeadersAndEmphasis(t *testing.T) {
	emphaticPara := func(runs ...runInfo) bodyElement {
		var full []string
		for _, r := range runs {
			full = append(full, r.Text)
		}
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: strings.Join(full, ""), Runs: runs,
		}}
	}
	elements := []bodyElement{
		sipHeading("1\tMobile originating"),
		sipPara("The UE sends SIP INVITE with SDP offer to the P-CSCF."),
		emphaticPara(runInfo{Text: "INVITE", Bold: true, Italic: true}, runInfo{Text: " tel:+8613587654321 SIP/2.0", Italic: true}),
		emphaticPara(runInfo{Text: "v:SIP/2.0/UDP A;branch=z9hG4bK122456", Italic: true}),
		emphaticPara(runInfo{Text: "f:<sip:A>;tag=205847", Italic: true}),
		emphaticPara(runInfo{Text: "CSeq:81 INVITE", Italic: true}),
		emphaticPara(runInfo{Text: "Max-Forwards:70", Italic: true}),
		emphaticPara(runInfo{Text: "l:120", Italic: true}),
		emphaticPara(runInfo{Text: "v=0", Italic: true}),
		emphaticPara(runInfo{Text: "m=audio 49155 RTP/AVP 0 8", Italic: true}),
		sipPara("The P-CSCF sends SIP 100 to the UE."),
		emphaticPara(runInfo{Text: "SIP/2.0", Italic: true}, runInfo{Text: " 100", Bold: true, Italic: true}),
		emphaticPara(runInfo{Text: "i:a9gH", Italic: true}),
		sipPara("Editor's note:\tWhether SIP 100 can be omitted is FFS."),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 5 {
		t.Fatalf("expected prose, fence, prose, fence, prose, got %v", content)
	}
	wantFirst := "```sip\n" +
		"INVITE tel:+8613587654321 SIP/2.0\n" +
		"v:SIP/2.0/UDP A;branch=z9hG4bK122456\n" +
		"f:<sip:A>;tag=205847\n" +
		"CSeq:81 INVITE\n" +
		"Max-Forwards:70\n" +
		"l:120\n" +
		"v=0\n" +
		"m=audio 49155 RTP/AVP 0 8\n" +
		"```"
	if content[1] != wantFirst {
		t.Errorf("expected INVITE fence without emphasis markers %q, got %q", wantFirst, content[1])
	}
	wantSecond := "```sip\nSIP/2.0 100\ni:a9gH\n```"
	if content[3] != wantSecond {
		t.Errorf("expected status fence %q, got %q", wantSecond, content[3])
	}
	if !strings.HasPrefix(content[4], "Editor's note:") {
		t.Errorf("expected Editor's note to stay prose, got %q", content[4])
	}
}

// TS 22.948 style: a list dash before the request line. The dash is
// styling, not message content, and the next dashed list item stays prose.
func TestParseSections_SIPDashPrefixedRequestLine(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tRe-use of W3C conferencing capabilities"),
		sipPara("CCXML can be invoked using SIP in IMS networks. For example:"),
		sipPara("-\tINVITE sip:control@mrf.example.com;ccxml=http://server.example.com/conference.ccxml"),
		sipPara("-\tSIP/2.0\tThis allows application servers to use CCXML in IMS network."),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 3 {
		t.Fatalf("expected prose, fence, prose, got %v", content)
	}
	want := "```sip\nINVITE sip:control@mrf.example.com;ccxml=http://server.example.com/conference.ccxml\n```"
	if content[1] != want {
		t.Errorf("expected dash-stripped fence %q, got %q", want, content[1])
	}
	if !strings.Contains(content[2], "This allows application servers") {
		t.Errorf("expected following list item to stay prose, got %q", content[2])
	}
}

// TR 23.877 style: a standalone SDP description, one field line per
// paragraph, no SIP message around it.
func TestParseSections_SDPBlock(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tUni-directional codec and SDP"),
		sipPara("One example for DSR on the uplink is shown below:"),
		sipPara("v=0"),
		sipPara("o=SESPhone 0 1 IN IP4 10.132.30.33"),
		sipPara("s=asymmetric ses dsr session"),
		sipPara("c=IN IP4 10.132.30.33"),
		sipPara("t=0 0"),
		sipPara("m=audio 9002 RTP/AVP 97"),
		sipPara("a=rtpmap:97 AMR/8000"),
		sipPara("a=recvonly"),
		sipPara("This example above uses IP4 but can easily be extended to IP6 for IMS."),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 3 {
		t.Fatalf("expected prose, fence, prose, got %v", content)
	}
	want := "```sdp\n" +
		"v=0\n" +
		"o=SESPhone 0 1 IN IP4 10.132.30.33\n" +
		"s=asymmetric ses dsr session\n" +
		"c=IN IP4 10.132.30.33\n" +
		"t=0 0\n" +
		"m=audio 9002 RTP/AVP 97\n" +
		"a=rtpmap:97 AMR/8000\n" +
		"a=recvonly\n" +
		"```"
	if content[1] != want {
		t.Errorf("expected SDP fence %q, got %q", want, content[1])
	}
}

// TS 26.234 A.1 style: "EXAMPLE 1:<tab>v=0" starts a multi-line SDP
// paragraph, with a backslash-wrapped a=fmtp value whose continuation line
// is not itself a field line.
func TestParseSections_SDPExampleLabelAndWrappedLine(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tSDP"),
		sipPara("The example below shows an SDP file:"),
		sipPara("EXAMPLE 1:\tv=0\no=ghost 2890844526 2890842807 IN IP4 192.168.10.10\ns=3GPP Unicast SDP Example\nc=IN IP4 0.0.0.0\nt=0 0"),
		bodyElement{Tag: "p", Paragraph: paragraphInfo{}}, // blank paragraph
		sipPara("a=range:npt=0-45.678\nm=video 1024 RTP/AVP 96"),
		sipPara("a=fmtp:96 packetization-mode=1; profile-level-id=64001e; \\"),
		sipPara("sprop-parameter-sets= Z2QAHpWQC0PaAfyQ,aOuOoA=="),
		sipPara("a=control:rtsp://mediaserver.com/movie.3gp/trackID=1"),
		sipPara("The following examples show some usage of the \"alt\" attribute:"),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 4 {
		t.Fatalf("expected prose, label, fence, prose, got %v", content)
	}
	if content[1] != "EXAMPLE 1:" {
		t.Errorf("expected the example label as its own paragraph, got %q", content[1])
	}
	want := "```sdp\n" +
		"v=0\no=ghost 2890844526 2890842807 IN IP4 192.168.10.10\ns=3GPP Unicast SDP Example\nc=IN IP4 0.0.0.0\nt=0 0\n" +
		"\n" +
		"a=range:npt=0-45.678\nm=video 1024 RTP/AVP 96\n" +
		"a=fmtp:96 packetization-mode=1; profile-level-id=64001e; \\\n" +
		"sprop-parameter-sets= Z2QAHpWQC0PaAfyQ,aOuOoA==\n" +
		"a=control:rtsp://mediaserver.com/movie.3gp/trackID=1\n" +
		"```"
	if content[2] != want {
		t.Errorf("expected labeled SDP fence %q, got %q", want, content[2])
	}
}

// TS 26.234 A.2.1 style: an SDP fragment that starts at an m-line rather
// than v=0 still fences once three field lines run consecutively.
func TestParseSections_SDPFragmentStartingAtMediaLine(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tExamples"),
		sipPara("The equivalent SDP for alternative 1 (default) is:"),
		sipPara("EXAMPLE 3:\tm=audio 0 RTP/AVP 97"),
		sipPara("b=AS:12"),
		sipPara("b=TIAS:8500"),
		sipPara("a=rtpmap:97 AMR/8000"),
		sipPara("Alternative 2 is based on the default alternative."),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 4 {
		t.Fatalf("expected prose, label, fence, prose, got %v", content)
	}
	if content[1] != "EXAMPLE 3:" {
		t.Errorf("expected the example label as its own paragraph, got %q", content[1])
	}
	want := "```sdp\nm=audio 0 RTP/AVP 97\nb=AS:12\nb=TIAS:8500\na=rtpmap:97 AMR/8000\n```"
	if content[2] != want {
		t.Errorf("expected SDP fragment fence %q, got %q", want, content[2])
	}
}

// TS 23.207 Annex C prose: isolated SDP field-form templates separated by
// prose must not fence, and neither must equations or short field runs.
func TestParseSections_SDPFalsePositiveProse(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tMapping"),
		sipPara("The media announcements field is of the form:"),
		sipPara("m=<media> <port> <transport> <fmt list>"),
		sipPara("The attributes field is of the form:"),
		sipPara("a=<attribute><value>"),
		sipPara("The connection data field is of the form:"),
		sipPara("c=<network type> <address type> <connection address>"),
		sipPara("An equation like x=y+1 stays prose."),
		sipPara("v=0"),
		sipPara("Only one field line follows nothing, so it stays prose too."),
		// Prose enumerations of the SDP field types (TR 23.700-19) end each
		// line with a period, which no real field value does.
		sipPara("v= (protocol version)."),
		sipPara("o= (originator and session identifier)."),
		sipPara("s= (session name)."),
		sipPara("c= (connection information)."),
	}
	sections := sipParse(elements)
	for _, c := range sections[0].Content {
		if strings.Contains(c, "```") {
			t.Errorf("expected no fence in prose-only section, got %q", c)
		}
	}
	if len(sections[0].Content) != len(elements)-1 {
		t.Errorf("expected every paragraph kept as prose, got %v", sections[0].Content)
	}
}

// TS 29.332 style: SDP lines inside an H.248 Local descriptor. The braces
// stay prose; the field lines in between fence.
func TestParseSections_SDPInsideH248Local(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tAMR and AMR-WB Codecs"),
		sipPara("ABNF:"),
		sipPara("Local {"),
		sipPara("v=0"),
		sipPara("c=IN IP4 $"),
		sipPara("m=audio $ RTP/AVP 96"),
		sipPara("a=rtpmap:96 AMR/8000"),
		sipPara("}"),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 4 {
		t.Fatalf("expected two prose paragraphs, fence, closing brace, got %v", content)
	}
	want := "```sdp\nv=0\nc=IN IP4 $\nm=audio $ RTP/AVP 96\na=rtpmap:96 AMR/8000\n```"
	if content[2] != want {
		t.Errorf("expected SDP fence %q, got %q", want, content[2])
	}
}

// TS 33.838 style: generic hyphenated headers and parameter continuation
// lines ("UC-Indicator=true;") stay inside the message.
func TestParseSections_SIPHyphenatedHeaderContinuation(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tPUCI information"),
		sipPara("The UC Score could be incorporated into the SIP header as shown below:"),
		sipPara("INVITE sip:bob@example.net SIP/2.0"),
		sipPara("Via: SIP/2.0/UDP sip.example.net;branch=z9hG4bKnashds8;received=192.0.2.1"),
		sipPara("UC-Score: 75 by sip.example.net;"),
		sipPara("UC-Indicator=true;"),
		sipPara("Max-Forwards: 70"),
		sipPara("To: Bob <sip:bob@example.net>"),
		sipPara("Content-Length: 142"),
		sipPara("[... SDP excluded from this example...]"),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 3 {
		t.Fatalf("expected prose, fence, trailing prose, got %v", content)
	}
	fence := content[1]
	for _, line := range []string{"UC-Score: 75", "UC-Indicator=true;", "Content-Length: 142"} {
		if !strings.Contains(fence, line) {
			t.Errorf("expected %q inside the fence, got %q", line, fence)
		}
	}
	if strings.Contains(fence, "SDP excluded") {
		t.Errorf("expected the bracketed omission note to stay prose, got %q", fence)
	}
}

// TS 26.234 11.3.3 style: a continuation paragraph whose header value wraps
// onto a soft-break line that matches no line rule on its own. Blocks are
// absorbed at paragraph granularity — only the first line decides — so the
// wrapped value stays inside the message instead of ending the block; prose
// in these specs lives in its own paragraphs, never after a soft break in a
// message paragraph.
func TestParseSections_SIPWrappedHeaderContinuation(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tMetrics initiation with RTSP"),
		sipPara("SETUP rtsp://example.com/foo/bar/baz.3gp/trackID=3 RTSP/1.0"),
		sipPara("Cseq: 2"),
		sipPara("3GPP-QoE-Metrics:url=\"rtsp://example.com/foo/bar/baz.3gp/trackID=3\"; metrics={Corruption_Duration };rate=10, url=\"rtsp://example.com/foo/bar/baz.3gp\";\nmetrics={Initial_Buffering_Duration|Rebuffering_Duration };rate=End"),
		sipPara("In the above SETUP request, the client modifies the sending rate."),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected fence and trailing prose, got %v", content)
	}
	if !strings.Contains(content[0], "\nmetrics={Initial_Buffering_Duration|Rebuffering_Duration };rate=End\n") {
		t.Errorf("expected the wrapped header value inside the fence, got %q", content[0])
	}
	if !strings.HasPrefix(content[1], "In the above SETUP request") {
		t.Errorf("expected trailing prose after the fence, got %q", content[1])
	}
}

// Monospace-styled SIP examples (TS 24.228, TS 24.337, ...) must keep
// flowing through the style-based code path unchanged.
func TestParseSections_SIPCodeStyledUnchanged(t *testing.T) {
	codePara := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: text, Runs: []runInfo{{Text: text, IsCode: true}}, IsCode: true,
		}}
	}
	elements := []bodyElement{
		sipHeading("1\tSignalling flows"),
		codePara("INVITE sip:bob@example.net SIP/2.0"),
		codePara("Via: SIP/2.0/UDP pc33.example.com"),
		codePara("v=0"),
		sipPara("Trailing prose."),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected one fence and one prose paragraph, got %v", content)
	}
	want := "```\nINVITE sip:bob@example.net SIP/2.0\nVia: SIP/2.0/UDP pc33.example.com\nv=0\n```"
	if content[0] != want {
		t.Errorf("expected the code-styled fence unchanged %q, got %q", want, content[0])
	}
}

// A heading or table while capturing flushes the pending block.
func TestParseSections_SIPFlushedByHeadingAndTable(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tFirst"),
		sipPara("INVITE sip:bob@example.net SIP/2.0"),
		sipPara("Via: SIP/2.0/UDP pc33.example.com"),
		sipHeading("2\tSecond"),
		sipPara("SIP/2.0 183 Session Progress"),
		{Tag: "tbl", Table: tableInfo{Rows: []tableRow{{Cells: []tableCell{{Paras: []paragraphInfo{{Text: "cell", Runs: []runInfo{{Text: "cell"}}}}}}}}}},
	}
	sections := sipParse(elements)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if len(sections[0].Content) != 1 || !strings.HasPrefix(sections[0].Content[0], "```sip\n") {
		t.Errorf("expected SIP fence flushed by heading in first section, got %v", sections[0].Content)
	}
	second := strings.Join(sections[1].Content, "\n")
	if !strings.Contains(second, "```sip\nSIP/2.0 183 Session Progress\n```") || !strings.Contains(second, "cell") {
		t.Errorf("expected SIP fence flushed by table plus table content, got %v", sections[1].Content)
	}
	if strings.Index(second, "```") > strings.Index(second, "cell") {
		t.Errorf("expected fence before table content, got %v", sections[1].Content)
	}
}

// A paragraph with an image never joins or continues a block.
func TestParseSections_SIPImageParagraphEndsBlock(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tFirst"),
		sipPara("INVITE sip:bob@example.net SIP/2.0"),
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "v=0", Runs: []runInfo{{Text: "v=0"}},
			Images: []imageRef{{RID: "rId1"}},
		}},
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) == 0 || content[0] != "```sip\nINVITE sip:bob@example.net SIP/2.0\n```" {
		t.Errorf("expected the image paragraph to end the fence, got %v", content)
	}
	for _, c := range content[1:] {
		if strings.Contains(c, "```") {
			t.Errorf("expected no second fence, got %q", c)
		}
	}
}

// A backslash-continued line only folds the first line of the next
// paragraph into the block, and never a sentence: a prose paragraph after a
// value fold must not be absorbed (issue #101).
func TestBlockContinues_BackslashContinuation(t *testing.T) {
	para := func(text string) paragraphInfo {
		return paragraphInfo{Text: text, Runs: []runInfo{{Text: text}}}
	}
	const foldedLast = `a=fmtp:97 mode-set=0,2 \`

	tests := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "wrapped value remainder continues",
			text: "sprop-parameter-sets= Z2QAHpWQC0PaAfyQ,aOuOoA==",
			want: true,
		},
		{
			name: "wrapped remainder followed by field lines continues",
			text: "mode-change-period=2\na=control:rtsp://example.com/x",
			want: true,
		},
		{
			name: "prose sentence does not continue",
			text: "This clause describes how the session is established.",
			want: false,
		},
		{
			name: "single-line prose without a trailing period does not continue",
			text: "The above example shows the session setup",
			want: false,
		},
		{
			name: "folded QoE-metrics style remainder continues",
			text: "metrics={Initial_Buffering_Duration|Rebuffering_Duration };rate=End",
			want: true,
		},
		{
			name: "single parameter token continues",
			text: "mode-change-period=2",
			want: true,
		},
		{
			// A fold split right after "=", leaving the bare blob alone on
			// the wrapped line.
			name: "bare wrapped blob token continues",
			text: "Z2QAHpWQC0PaAfyQ,aOuOoA",
			want: true,
		},
		{
			// The digit-bearing "1:" token breaks any consecutive-word test,
			// so the positive parameter-syntax requirement has to reject it.
			name: "figure caption does not continue",
			text: "Figure 1: message flow",
			want: false,
		},
		{
			name: "table caption does not continue",
			text: "Table 5.1: parameters",
			want: false,
		},
		{
			name: "step prose does not continue",
			text: "Step 2: the UE sends",
			want: false,
		},
		{
			name: "short reference prose does not continue",
			text: "See below",
			want: false,
		},
		{
			// Prose quoting a parameter fragment carries an '=' but is still
			// sentence-shaped: the word-run rejection applies on top of the
			// positive signal.
			name: "prose quoting a parameter does not continue",
			text: "The value mode-set=0 is used by the UE",
			want: false,
		},
		{
			name: "prose after the wrapped remainder does not continue",
			text: "mode-change-period=2\nThe session is then established as usual",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sipBlockContinues(para(tt.text), foldedLast); got != tt.want {
				t.Errorf("sipBlockContinues(%q) = %v, want %v", tt.text, got, tt.want)
			}
			if got := sdpBlockContinues(para(tt.text), foldedLast); got != tt.want {
				t.Errorf("sdpBlockContinues(%q) = %v, want %v", tt.text, got, tt.want)
			}
		})
	}

	img := paragraphInfo{
		Text: "v=0", Runs: []runInfo{{Text: "v=0"}},
		Images: []imageRef{{RID: "rId1"}},
	}
	if sipBlockContinues(img, foldedLast) || sdpBlockContinues(img, foldedLast) {
		t.Error("expected an image paragraph never to continue a block")
	}
}

// Full-pipeline shape of issue #101: a backslash-wrapped SDP value followed
// by a prose paragraph — the prose ends the block instead of being pulled
// into the fence verbatim.
func TestParseSections_SDPBackslashDoesNotSwallowProse(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tSDP"),
		sipPara("v=0\no=ghost 1 1 IN IP4 10.0.0.1\ns=example\nt=0 0"),
		sipPara(`a=fmtp:97 mode-set=0,2 \`),
		sipPara("This clause describes how the session is established."),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected fence + prose, got %v", content)
	}
	if !strings.HasPrefix(content[0], "```sdp\n") || !strings.Contains(content[0], `a=fmtp:97 mode-set=0,2 \`) {
		t.Errorf("expected the folded field line inside the fence, got %q", content[0])
	}
	if strings.Contains(content[0], "This clause") {
		t.Errorf("prose swallowed into the fence: %q", content[0])
	}
	if content[1] != "This clause describes how the session is established." {
		t.Errorf("expected the prose paragraph after the fence, got %q", content[1])
	}
}

// A one-line prose paragraph with no trailing period after a value fold has
// no "remaining lines" for the shape check to reject, so the wrapped-line
// test itself must spot the prose (PR #122 review finding on issue #101).
func TestParseSections_SDPBackslashDoesNotSwallowUnpunctuatedProse(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tSDP"),
		sipPara("v=0\no=ghost 1 1 IN IP4 10.0.0.1\ns=example\nt=0 0"),
		sipPara(`a=fmtp:97 mode-set=0,2 \`),
		sipPara("The above example shows the session setup"),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected fence + prose, got %v", content)
	}
	if strings.Contains(content[0], "The above example") {
		t.Errorf("prose swallowed into the fence: %q", content[0])
	}
	if content[1] != "The above example shows the session setup" {
		t.Errorf("expected the prose paragraph after the fence, got %q", content[1])
	}
}

// The genuine TS 26.234 A.1 shape keeps working end to end: the wrapped
// base64 remainder of a folded a=fmtp value stays inside the fence.
func TestParseSections_SDPBackslashKeepsWrappedValue(t *testing.T) {
	elements := []bodyElement{
		sipHeading("1\tSDP"),
		sipPara("v=0\no=ghost 1 1 IN IP4 10.0.0.1\ns=example\nt=0 0"),
		sipPara(`a=fmtp:96 packetization-mode=1; profile-level-id=64001e; \`),
		sipPara("sprop-parameter-sets= Z2QAHpWQC0PaAfyQ,aOuOoA=="),
		sipPara("The example is explained below."),
	}
	sections := sipParse(elements)
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected fence + prose, got %v", content)
	}
	if !strings.Contains(content[0], "sprop-parameter-sets= Z2QAHpWQC0PaAfyQ,aOuOoA==") {
		t.Errorf("expected the wrapped value inside the fence, got %q", content[0])
	}
	if content[1] != "The example is explained below." {
		t.Errorf("expected trailing prose after the fence, got %q", content[1])
	}
}

func TestSDPFieldLineCount(t *testing.T) {
	tests := []struct {
		name          string
		text          string
		wantCount     int
		wantAllFields bool
	}{
		{
			name:          "plain field lines",
			text:          "v=0\nm=audio 49170 RTP/AVP 0",
			wantCount:     2,
			wantAllFields: true,
		},
		{
			name:          "backslash continuation covers the next line",
			text:          "m=audio 49170 RTP/AVP 96\na=fmtp:96 mode-set=0,2,4,7; \\\n         mode-change-period=2",
			wantCount:     3,
			wantAllFields: true,
		},
		{
			name:          "prose line is rejected",
			text:          "v=0\nThis paragraph explains the fields.",
			wantCount:     1,
			wantAllFields: false,
		},
		{
			// A blank line ends the continuation, so the prose after it has to
			// be checked like any other line instead of being waved through.
			name:          "continuation does not survive a blank line",
			text:          "a=fmtp:96 mode-set=0,2,4,7; \\\n\nThis paragraph explains the fields.",
			wantCount:     1,
			wantAllFields: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, allFields := sdpFieldLineCount(tt.text)
			if count != tt.wantCount || allFields != tt.wantAllFields {
				t.Errorf("sdpFieldLineCount(%q) = (%d, %v), want (%d, %v)",
					tt.text, count, allFields, tt.wantCount, tt.wantAllFields)
			}
		})
	}
}
