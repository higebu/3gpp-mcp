package web

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

func TestSIPLexerRegistered(t *testing.T) {
	for _, alias := range []string{"sip", "sdp", "rtsp"} {
		if lexers.Get(alias) == nil {
			t.Errorf("lexers.Get(%q) = nil, want the registered SIP lexer", alias)
		}
	}
}

func TestSIPLexerTokens(t *testing.T) {
	src := `INVITE tel:+8613587654321 SIP/2.0
Via: SIP/2.0/UDP pc33.example.com;branch=z9hG4bK776asdhds
v:SIP/2.0/UDP A;branch=z9hG4bK122456
3GPP-QoE-Metrics:url="rtsp://example.com/foo";rate=10
CSeq: 314159 INVITE
S->C 	RTSP/1.0 200 OK
v=0
m=audio 49155 RTP/AVP 0 8`

	lexer := lexers.Get("sip")
	it, err := lexer.Tokenise(nil, src)
	if err != nil {
		t.Fatalf("Tokenise() error = %v", err)
	}

	type tok struct {
		typ   chroma.TokenType
		value string
	}
	var tokens []tok
	for _, tk := range it.Tokens() {
		tokens = append(tokens, tok{tk.Type, tk.Value})
	}

	want := []tok{
		// Request line: method, URI, version.
		{chroma.KeywordReserved, "INVITE"},
		{chroma.LiteralString, "tel:+8613587654321"},
		{chroma.KeywordConstant, "SIP/2.0"},
		// Header name, and the version inside the Via value.
		{chroma.NameAttribute, "Via"},
		{chroma.KeywordConstant, "SIP/2.0"},
		// Compact-form header.
		{chroma.NameAttribute, "v"},
		{chroma.KeywordConstant, "SIP/2.0"},
		// Digit-leading extension header, quoted URI in its value.
		{chroma.NameAttribute, "3GPP-QoE-Metrics"},
		{chroma.LiteralString, "rtsp://example.com/foo"},
		// Method inside a header value.
		{chroma.NameAttribute, "CSeq"},
		{chroma.LiteralNumber, "314159"},
		{chroma.KeywordReserved, "INVITE"},
		// Direction prefix and status line.
		{chroma.NameLabel, "S->C"},
		{chroma.KeywordConstant, "RTSP/1.0"},
		{chroma.LiteralNumber, "200"},
		// SDP field lines.
		{chroma.NameAttribute, "v"},
		{chroma.Operator, "="},
		{chroma.LiteralNumber, "0"},
		{chroma.NameAttribute, "m"},
	}
	i := 0
	for _, w := range want {
		found := false
		for ; i < len(tokens); i++ {
			if tokens[i].value == w.value && tokens[i].typ == w.typ {
				found = true
				i++
				break
			}
		}
		if !found {
			t.Errorf("token %q (%v) not found in expected order; got tokens: %v", w.value, w.typ, tokens)
			break
		}
	}
}

// Colons and equals signs inside header values must not lex as header names
// or SDP fields — those rules are line-anchored.
func TestSIPLexerValueColonNotHeader(t *testing.T) {
	lexer := lexers.Get("sip")
	it, err := lexer.Tokenise(nil, "Via: SIP/2.0/UDP [5555::aaa:bbb:ccc:ddd];comp=sigcomp\n")
	if err != nil {
		t.Fatalf("Tokenise() error = %v", err)
	}
	var attrs []string
	for _, tk := range it.Tokens() {
		if tk.Type == chroma.NameAttribute {
			attrs = append(attrs, tk.Value)
		}
	}
	if len(attrs) != 1 || attrs[0] != "Via" {
		t.Errorf("NameAttribute tokens = %v, want only [Via]", attrs)
	}
}

func TestRenderMarkdownHighlightsSIPAndSDP(t *testing.T) {
	for _, tt := range []struct {
		content string
		wantTok string
	}{
		{"```sip\nINVITE sip:bob@example.net SIP/2.0\nVia: SIP/2.0/UDP pc33.example.com\n```", ">INVITE</span>"},
		{"```sdp\nv=0\no=- 4567 1234 IN IP4 1.1.1.1\n```", ">v</span>"},
	} {
		got := renderMarkdown(tt.content, "TS 24.228", "", nil, nil)
		if !strings.Contains(got, "chroma") {
			t.Errorf("renderMarkdown(%q) = %q, want chroma-highlighted output", tt.content, got)
		}
		if !strings.Contains(got, tt.wantTok) {
			t.Errorf("renderMarkdown(%q) = %q, want %q inside a token span", tt.content, got, tt.wantTok)
		}
	}
}
