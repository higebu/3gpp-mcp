package web

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// The DOCX converter emits Diameter Command Code Format definitions
// (RFC 6733 clause 3.2) as ```diameter fences; Chroma ships no lexer for
// that notation, so this registers one under the "diameter" alias. The
// existing light/dark .chroma stylesheets cover the emitted token classes.
func init() {
	lexers.Register(chroma.MustNewLexer(
		&chroma.Config{
			Name:    "Diameter CCF",
			Aliases: []string{"diameter"},
		},
		diameterRules,
	))
}

// diameterRules covers the Command Code Format as printed in 3GPP Diameter
// specifications: a `Name ::= < Diameter|AVP Header: ... >` line followed by
// one AVP reference per line, each an optional multiplicity qualifier and a
// fixed < >, mandatory { } or optional [ ] reference.
func diameterRules() chroma.Rules {
	rule := func(pattern string, typ chroma.TokenType, mutator chroma.Mutator) chroma.Rule {
		return chroma.Rule{Pattern: pattern, Type: typ, Mutator: mutator}
	}
	return chroma.Rules{
		"root": {
			rule(`\s+`, chroma.TextWhitespace, nil),
			rule(`::=`, chroma.Operator, nil),
			// Before the name rule so the words never lex as AVP names.
			rule(`(?i)\b(?:diameter|avp)[ \t]+header\b`, chroma.KeywordDeclaration, nil),
			rule(`\b(?:REQ|PXY|ERR)\b`, chroma.KeywordConstant, nil),
			// Multiplicity qualifier ([min] "*" [max]) — before the plain
			// number rule so "1*" lexes as one token.
			rule(`\d+\*\d*|\*\d*`, chroma.LiteralNumber, nil),
			// Command code, application id, vendor id. `\b` after the digits
			// keeps digit-leading AVP names (3GPP-Charging-Characteristics)
			// falling through to the name rule below.
			rule(`\b\d+\b`, chroma.LiteralNumber, nil),
			// Fixed-position AVPs and the header delimiters.
			rule(`[<>]`, chroma.Operator, nil),
			// Mandatory AVPs — visually strongest.
			rule(`[{}]`, chroma.Keyword, nil),
			// Optional AVPs — neutral.
			rule(`[\[\]]`, chroma.Punctuation, nil),
			rule(`[A-Za-z0-9][A-Za-z0-9-]*`, chroma.NameClass, nil),
			rule(`[,:;()]`, chroma.Punctuation, nil),
			rule(`.`, chroma.Text, nil),
		},
	}
}
