package web

import (
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// The DOCX converter emits SIP/RTSP message examples as ```sip fences and
// standalone SDP session descriptions as ```sdp fences; Chroma ships no
// lexer for either notation, so this registers one under both aliases
// (SDP lines also appear inside SIP message bodies, so one lexer covers
// both). The existing light/dark .chroma stylesheets cover the emitted
// token classes.
func init() {
	lexers.Register(chroma.MustNewLexer(
		&chroma.Config{
			Name:    "SIP",
			Aliases: []string{"sip", "sdp", "rtsp"},
		},
		sipRules,
	))
}

// sipRules covers SIP and RTSP messages as printed in 3GPP specifications —
// request/status lines (optionally behind an interchange direction prefix
// like "S->C"), "Name: value" header lines including the one-letter compact
// forms, and SDP field lines ("v=0"). Header-name and SDP-field rules are
// line-anchored so colons and equals signs inside header values never lex
// as field names; methods, protocol versions and URIs are matched anywhere,
// which also highlights them inside values (CSeq: 1 INVITE, Via: SIP/2.0/UDP).
func sipRules() chroma.Rules {
	rule := func(pattern string, typ chroma.TokenType, mutator chroma.Mutator) chroma.Rule {
		return chroma.Rule{Pattern: pattern, Type: typ, Mutator: mutator}
	}
	byGroups := func(pattern string, types ...chroma.Emitter) chroma.Rule {
		return chroma.Rule{Pattern: pattern, Type: chroma.ByGroups(types...)}
	}
	return chroma.Rules{
		"root": {
			// Interchange direction prefix: "S->C", "C->S" (TS 26.234).
			byGroups(`^([ \t]*)([SC][ \t]*->[ \t]*[SC])\b`,
				chroma.TextWhitespace, chroma.NameLabel),
			// SDP field line: the type letter is the RFC 4566 field set, as
			// in the converter's detection (converter/docx/sipblock.go).
			byGroups(`^([ \t]*)([abceikmoprstuvz])(=)`,
				chroma.TextWhitespace, chroma.NameAttribute, chroma.Operator),
			// Header line, including compact forms ("v:", "f:", "l:") and
			// digit-leading extension names ("3GPP-QoE-Metrics:").
			byGroups(`^([ \t]*)([A-Za-z0-9][A-Za-z0-9-]*)(:)`,
				chroma.TextWhitespace, chroma.NameAttribute, chroma.Punctuation),
			// Protocol version, on status lines and inside Via values.
			rule(`\b(?:SIP|RTSP)/\d+\.\d+`, chroma.KeywordConstant, nil),
			// Request methods (SIP and RTSP), on request lines and inside
			// values such as "CSeq: 1 INVITE".
			rule(`\b(?:INVITE|REGISTER|ACK|BYE|CANCEL|OPTIONS|SUBSCRIBE|NOTIFY|PUBLISH|MESSAGE|REFER|UPDATE|PRACK|INFO|DESCRIBE|SETUP|PLAY|PAUSE|TEARDOWN|ANNOUNCE|RECORD|REDIRECT|GET_PARAMETER|SET_PARAMETER)\b`,
				chroma.KeywordReserved, nil),
			// URIs. The character class stops at delimiters that end a URI
			// in headers (<>, quotes, commas, whitespace).
			rule(`\b(?:sips?|tel|rtspu?|https?):[^\s<>",]+`, chroma.LiteralString, nil),
			rule(`\b\d+\b`, chroma.LiteralNumber, nil),
			rule(`[<>;,=/@]`, chroma.Punctuation, nil),
			rule(`[ \t]+`, chroma.TextWhitespace, nil),
			rule(`\n`, chroma.TextWhitespace, nil),
			rule(`\w+`, chroma.Text, nil),
			rule(`.`, chroma.Text, nil),
		},
	}
}
