package web

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// Chroma ships no ASN.1 lexer, so the ```asn1 fences the DOCX converter emits
// (from the -- ASN1START / -- ASN1STOP extraction markers) would fall back to
// unhighlighted text. This registers one under the "asn1" alias; the existing
// light/dark .chroma stylesheets then cover it like any other language.
func init() {
	lexers.Register(chroma.MustNewLexer(
		&chroma.Config{
			Name:      "ASN.1",
			Aliases:   []string{"asn1"},
			Filenames: []string{"*.asn", "*.asn1"},
			MimeTypes: []string{"text/x-asn1"},
		},
		asn1Rules,
	))
}

// asn1Rules covers the X.680 notation as used in 3GPP specifications.
// Keyword alternations reject a trailing hyphen so hyphenated identifiers
// (e.g. SEQUENCE-like type names such as SetupRelease-r16) are never split;
// chroma.Words sorts alternatives longest-first, so multi-word reserved names
// like NOT-A-NUMBER win over their prefixes.
func asn1Rules() chroma.Rules {
	kw := func(words ...string) string {
		return chroma.Words(`\b`, `\b(?!-)`, words...)
	}
	rule := func(pattern string, typ chroma.TokenType, mutator chroma.Mutator) chroma.Rule {
		return chroma.Rule{Pattern: pattern, Type: typ, Mutator: mutator}
	}
	return chroma.Rules{
		"root": {
			rule(`\s+`, chroma.TextWhitespace, nil),
			// A "--" comment ends at the next "--" or at end of line.
			rule(`--(?:[^\n-]|-(?!-))*(?:--)?`, chroma.CommentSingle, nil),
			rule(`/\*`, chroma.CommentMultiline, chroma.Push("comment")),
			rule(`"(?:[^"]|"")*"`, chroma.LiteralString, nil),
			rule(`'[01 \t]*'B`, chroma.LiteralNumberBin, nil),
			rule(`'[0-9A-Fa-f \t]*'H`, chroma.LiteralNumberHex, nil),
			rule(`::=`, chroma.Operator, nil),
			rule(`\.\.\.?`, chroma.Operator, nil),
			rule(kw("DEFINITIONS", "BEGIN", "END", "IMPORTS", "EXPORTS", "FROM",
				"CLASS", "INSTANCE", "SYNTAX"), chroma.KeywordDeclaration, nil),
			rule(kw("BOOLEAN", "INTEGER", "REAL", "ENUMERATED", "SEQUENCE", "SET",
				"CHOICE", "BIT", "OCTET", "STRING", "OBJECT", "IDENTIFIER",
				"CHARACTER", "EXTERNAL", "EMBEDDED", "PDV", "RELATIVE-OID-IRI",
				"RELATIVE-OID", "OID-IRI", "TIME-OF-DAY", "DATE-TIME", "DATE",
				"TIME", "DURATION", "BMPString", "GeneralString",
				"GraphicString", "IA5String", "ISO646String", "NumericString",
				"PrintableString", "T61String", "TeletexString",
				"UniversalString", "UTF8String", "VideotexString",
				"VisibleString", "GeneralizedTime", "UTCTime",
				"ObjectDescriptor"), chroma.KeywordType, nil),
			rule(kw("TRUE", "FALSE", "NULL", "MIN", "MAX", "PLUS-INFINITY",
				"MINUS-INFINITY", "NOT-A-NUMBER"), chroma.KeywordConstant, nil),
			rule(kw("ABSENT", "ABSTRACT-SYNTAX", "ALL", "APPLICATION", "AUTOMATIC",
				"BY", "COMPONENTS", "COMPONENT", "CONSTRAINED", "CONTAINING",
				"DEFAULT", "ENCODED", "EXCEPT", "EXPLICIT", "EXTENSIBILITY",
				"IMPLICIT", "IMPLIED", "INCLUDES", "INSTRUCTIONS",
				"INTERSECTION", "OF", "OPTIONAL", "PATTERN", "PRESENT",
				"PRIVATE", "SETTINGS", "SIZE", "TAGS", "TYPE-IDENTIFIER",
				"UNION", "UNIQUE", "UNIVERSAL", "WITH"), chroma.Keyword, nil),
			rule(`&[A-Za-z][A-Za-z0-9-]*`, chroma.NameAttribute, nil),
			rule(`[A-Z][A-Za-z0-9-]*`, chroma.NameClass, nil),
			rule(`[a-z][A-Za-z0-9-]*`, chroma.Name, nil),
			rule(`-?\d+(?:\.\d+)?(?:[eE][-+]?\d+)?`, chroma.LiteralNumber, nil),
			rule(`[{}\[\]().,;:|@!^<>]`, chroma.Punctuation, nil),
			rule(`.`, chroma.Text, nil),
		},
		// X.680 block comments nest.
		"comment": {
			rule(`[^*/]+`, chroma.CommentMultiline, nil),
			rule(`/\*`, chroma.CommentMultiline, chroma.Push()),
			rule(`\*/`, chroma.CommentMultiline, chroma.Pop(1)),
			rule(`[*/]`, chroma.CommentMultiline, nil),
		},
	}
}
