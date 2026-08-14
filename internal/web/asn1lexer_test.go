package web

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

func TestASN1LexerRegistered(t *testing.T) {
	if lexers.Get("asn1") == nil {
		t.Fatal("lexers.Get(\"asn1\") = nil, want the registered ASN.1 lexer")
	}
}

func TestASN1LexerTokens(t *testing.T) {
	src := `-- ASN1START
RRCSetup-r15 ::= SEQUENCE {
    rrc-TransactionIdentifier    RRC-TransactionIdentifier,
    spare                        BIT STRING (SIZE (1)),
    count                        INTEGER (0..maxNrofSCells) OPTIONAL, -- Need N
    pattern                      '0110'B,
    enabled                      TRUE
}
-- ASN1STOP`

	lexer := lexers.Get("asn1")
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
		{chroma.CommentSingle, "-- ASN1START"},
		{chroma.NameClass, "RRCSetup-r15"},
		{chroma.Operator, "::="},
		{chroma.Keyword, "SEQUENCE"},
		{chroma.Name, "rrc-TransactionIdentifier"},
		{chroma.NameClass, "RRC-TransactionIdentifier"},
		{chroma.Keyword, "BIT"},
		{chroma.Keyword, "STRING"},
		{chroma.Keyword, "SIZE"},
		{chroma.LiteralNumber, "1"},
		{chroma.Keyword, "INTEGER"},
		{chroma.Operator, ".."},
		{chroma.Name, "maxNrofSCells"},
		{chroma.Keyword, "OPTIONAL"},
		{chroma.CommentSingle, "-- Need N"},
		{chroma.LiteralNumberBin, "'0110'B"},
		{chroma.KeywordConstant, "TRUE"},
		{chroma.CommentSingle, "-- ASN1STOP"},
	}
	i := 0
	for _, w := range want {
		found := false
		for ; i < len(tokens); i++ {
			if tokens[i].value == w.value {
				found = true
				// SEQUENCE etc. are KeywordType/Keyword subtypes; compare categories.
				if tokens[i].typ.Category() != w.typ.Category() && tokens[i].typ != w.typ {
					t.Errorf("token %q type = %v, want category %v", w.value, tokens[i].typ, w.typ)
				}
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

func TestASN1LexerCommentEndsAtDoubleDash(t *testing.T) {
	lexer := lexers.Get("asn1")
	it, err := lexer.Tokenise(nil, "INTEGER -- inline -- OPTIONAL\n")
	if err != nil {
		t.Fatalf("Tokenise() error = %v", err)
	}
	var comment string
	sawOptional := false
	for _, tk := range it.Tokens() {
		if tk.Type == chroma.CommentSingle {
			comment = tk.Value
		}
		if tk.Value == "OPTIONAL" && tk.Type.Category() == chroma.Keyword {
			sawOptional = true
		}
	}
	if comment != "-- inline --" {
		t.Errorf("comment = %q, want %q", comment, "-- inline --")
	}
	if !sawOptional {
		t.Error("OPTIONAL after a closed inline comment should lex as a keyword")
	}
}

func TestASN1LexerHyphenatedIdentifierNotSplit(t *testing.T) {
	lexer := lexers.Get("asn1")
	it, err := lexer.Tokenise(nil, "SetupRelease-r16")
	if err != nil {
		t.Fatalf("Tokenise() error = %v", err)
	}
	tokens := it.Tokens()
	if len(tokens) != 1 || tokens[0].Value != "SetupRelease-r16" || tokens[0].Type != chroma.NameClass {
		t.Errorf("tokens = %v, want a single NameClass token %q", tokens, "SetupRelease-r16")
	}
}

func TestRenderMarkdownHighlightsASN1(t *testing.T) {
	content := "```asn1\nRRCSetup ::= SEQUENCE {}\n```"
	got := renderMarkdown(content, renderOpts{specID: "TS 38.331"})
	if !strings.Contains(got, "chroma") {
		t.Errorf("renderMarkdown() = %q, want chroma-highlighted output", got)
	}
	if !strings.Contains(got, ">SEQUENCE</span>") {
		t.Errorf("renderMarkdown() = %q, want SEQUENCE inside a token span", got)
	}
}
