package web

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

func TestDiameterLexerRegistered(t *testing.T) {
	if lexers.Get("diameter") == nil {
		t.Fatal("lexers.Get(\"diameter\") = nil, want the registered Diameter CCF lexer")
	}
}

func TestDiameterLexerTokens(t *testing.T) {
	src := `< Update-Location-Request> ::=	< Diameter Header: 316, REQ, PXY, 16777251 >
< Session-Id >
[ DRMP ]
{ Origin-Host }
*[ Supported-Features ]
1*{ Proxy-Info }`

	lexer := lexers.Get("diameter")
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
		{chroma.NameClass, "Update-Location-Request"},
		{chroma.Operator, "::="},
		{chroma.KeywordDeclaration, "Diameter Header"},
		{chroma.LiteralNumber, "316"},
		{chroma.KeywordConstant, "REQ"},
		{chroma.KeywordConstant, "PXY"},
		{chroma.LiteralNumber, "16777251"},
		{chroma.NameClass, "Session-Id"},
		{chroma.NameClass, "DRMP"},
		{chroma.Keyword, "{"},
		{chroma.NameClass, "Origin-Host"},
		{chroma.LiteralNumber, "*"},
		{chroma.NameClass, "Supported-Features"},
		{chroma.LiteralNumber, "1*"},
		{chroma.NameClass, "Proxy-Info"},
	}
	i := 0
	for _, w := range want {
		found := false
		for ; i < len(tokens); i++ {
			if tokens[i].value == w.value {
				found = true
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

func TestDiameterLexerDigitLeadingNameNotSplit(t *testing.T) {
	lexer := lexers.Get("diameter")
	it, err := lexer.Tokenise(nil, "3GPP-Charging-Characteristics")
	if err != nil {
		t.Fatalf("Tokenise() error = %v", err)
	}
	tokens := it.Tokens()
	if len(tokens) != 1 || tokens[0].Value != "3GPP-Charging-Characteristics" || tokens[0].Type != chroma.NameClass {
		t.Errorf("tokens = %v, want a single NameClass token %q", tokens, "3GPP-Charging-Characteristics")
	}
}

func TestRenderMarkdownHighlightsDiameter(t *testing.T) {
	content := "```diameter\n< Session-Id >\n{ Origin-Host }\n```"
	got := renderMarkdown(content, renderOpts{specID: "TS 29.272"})
	if !strings.Contains(got, "chroma") {
		t.Errorf("renderMarkdown() = %q, want chroma-highlighted output", got)
	}
	if !strings.Contains(got, ">Origin-Host</span>") {
		t.Errorf("renderMarkdown() = %q, want Origin-Host inside a token span", got)
	}
}
