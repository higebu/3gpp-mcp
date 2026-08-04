package docx

// Content-based detection of SIP message and SDP session-description
// examples. Several specs (e.g. TS 23.877, TS 23.850, TS 23.700-19) write
// these examples as plain body paragraphs — Normal style, no code font —
// so none of the style-based code paths fire and every line ends up as an
// ordinary prose paragraph. Like the Diameter definitions, they are
// recognized by their line syntax instead: a SIP request/status line or an
// SDP field-line run opens a block, message-shaped lines continue it, and
// the first paragraph that is neither ends it. Matched blocks become bare
// ``` fences.
//
// Detection runs on the raw paragraph text (codeLineText), before any
// bold/italic markers are applied — TS 23.700-19 styles entire SIP
// messages in italics — and only on paragraphs that are not already
// code-styled, so specs whose examples are monospace-styled (TS 24.228,
// TS 24.337, ...) keep their existing fenced output byte-for-byte.

import (
	"regexp"
	"strings"
)

// sipMethodsPat lists the SIP request methods that may open an example
// (RFC 3261 plus the standard extension methods used in 3GPP specs), and
// the RTSP methods — the PSS specs (TS 26.234, TS 26.905) carry their SDP
// examples inside RTSP exchanges of the exact same shape.
const sipMethodsPat = `(?:INVITE|REGISTER|ACK|BYE|CANCEL|OPTIONS|SUBSCRIBE|NOTIFY|PUBLISH|MESSAGE|REFER|UPDATE|PRACK|INFO|DESCRIBE|SETUP|PLAY|PAUSE|TEARDOWN|ANNOUNCE|RECORD|REDIRECT|GET_PARAMETER|SET_PARAMETER)`

// sipVersionPat matches the protocol-version token ending a request line or
// starting a status line.
const sipVersionPat = `(?:SIP/2\.0|RTSP/1\.[01])`

// sipDirectionPat matches an optional interchange direction prefix:
// "S->C  RTSP/1.0 200 OK" (TS 26.234 clause 11.3).
const sipDirectionPat = `(?:[SC][ \t]*->[ \t]*[SC][ \t]+)?`

var (
	// Full request line: "INVITE sip:B-Party;tgrp=CGR2@operator.net SIP/2.0".
	// End-anchored so prose that merely mentions a method never matches.
	sipRequestFullRE = regexp.MustCompile(`^` + sipDirectionPat + sipMethodsPat + `[ \t]+\S+[ \t]+` + sipVersionPat + `[ \t]*$`)
	// Request line without the version token, as some older specs write it:
	// "INVITE sip: A2EA2@Iputrannode2.operator.net" (TS 25.933). The whole
	// line must be method + URI — trailing prose disqualifies it.
	sipRequestURIRE = regexp.MustCompile(`^` + sipMethodsPat + `[ \t]+(?:sips?|tel|rtspu?):[ \t]*\S+[ \t]*$`)
	// Status line: "SIP/2.0 100", "SIP/2.0 183 Session Progress". The reason
	// phrase is limited to a few capitalized words so sentences like
	// "SIP/2.0 200 OK is sent to the UE" stay prose.
	sipStatusRE = regexp.MustCompile(`^` + sipDirectionPat + sipVersionPat + `[ \t]+\d{3}(?:[ \t]+[A-Z][A-Za-z-]*){0,4}[ \t]*$`)

	// Header lines that continue a SIP message. A hyphenated field name
	// ("Call-ID:", "Max-Forwards:70", "UC-Indicator=true;") is accepted
	// generically; hyphen-less names are limited to well-known SIP headers
	// so prose like "where:" or "NOTE:" ends the block.
	sipHyphenHeaderRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*(?:-[A-Za-z0-9]+)+[ \t]*[:=]`)
	sipPlainHeaderRE  = regexp.MustCompile(`(?i)^(?:via|to|from|contact|cseq|route|path|require|supported|expires|event|accept|allow|authorization|privacy|reason|warning|server|unsupported|date|subject|priority|organization|rack|rseq|join|replaces|timestamp)[ \t]*:`)
	// Compact-form header, one letter and a colon: "v:SIP/2.0/UDP A;...",
	// "f:<sip:A>;tag=205847", "l:120" (TS 23.700-19).
	sipCompactHeaderRE = regexp.MustCompile(`^[a-z]:`)

	// One SDP field line. The type letter is restricted to the RFC 4566
	// field set so prose equations ("x=y") never match.
	sdpFieldLineRE = regexp.MustCompile(`^[abceikmoprstuvz]=`)
	sdpVersionRE   = regexp.MustCompile(`^v=0[ \t]*$`)

	// "EXAMPLE 1:<tab>v=0" (TS 26.234): a label immediately followed by the
	// first line of the example, on the same line or after a soft line
	// break. The label is emitted as its own prose paragraph before the
	// fence.
	sipExampleLabelRE = regexp.MustCompile(`(?i)^(example(?:[ \t\x{00a0}]+\d+)?[ \t\x{00a0}]*:)[ \t\x{00a0}\n]+`)
	// "-<tab>INVITE sip:..." (TS 22.948): a list dash before the start line.
	// The dash is list styling, not message content, and is dropped.
	sipDashPrefixRE = regexp.MustCompile(`^\p{Pd}[ \t\x{00a0}]+`)
)

// sipNormalize returns the paragraph's raw text with NBSP normalized to a
// space for matching (Go's \s and literal spaces do not match U+00A0).
func sipNormalize(s string) string {
	return strings.ReplaceAll(s, "\u00a0", " ")
}

// sipFirstLine returns the first line of a possibly multi-line paragraph
// text (soft line breaks inside a paragraph become \n in run text).
func sipFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// sipStartLine reports whether line is a SIP request or status line that
// can open a message example.
func sipStartLine(line string) bool {
	line = strings.TrimSpace(line)
	return sipRequestFullRE.MatchString(line) ||
		sipRequestURIRE.MatchString(line) ||
		sipStatusRE.MatchString(line)
}

// sipHeaderLine reports whether line looks like a SIP header field line.
func sipHeaderLine(line string) bool {
	return sipHyphenHeaderRE.MatchString(line) ||
		sipPlainHeaderRE.MatchString(line) ||
		sipCompactHeaderRE.MatchString(line)
}

// splitSIPExampleLabel splits an optional example prefix off the paragraph
// text: an "EXAMPLE n:" label (returned so the caller can emit it as prose)
// or a list dash (dropped). rest is the candidate message text.
func splitSIPExampleLabel(text string) (label, rest string) {
	if m := sipExampleLabelRE.FindStringSubmatchIndex(text); m != nil {
		return text[m[2]:m[3]], text[m[1]:]
	}
	if loc := sipDashPrefixRE.FindStringIndex(text); loc != nil {
		return "", text[loc[1]:]
	}
	return "", text
}

// sipLastBufferedLine returns the final line of the last buffered
// paragraph, for the backslash-continuation check.
func sipLastBufferedLine(buffer []string) string {
	if len(buffer) == 0 {
		return ""
	}
	last := buffer[len(buffer)-1]
	if i := strings.LastIndexByte(last, '\n'); i >= 0 {
		last = last[i+1:]
	}
	return last
}

// sdpFieldLine reports whether line is an SDP field line. A trailing
// period marks a sentence, not a field value — prose enumerations of the
// SDP field types ("v= (protocol version).", TS 23.700-19) must stay
// prose, and no real field value ends with one.
func sdpFieldLine(line string) bool {
	return sdpFieldLineRE.MatchString(line) && !strings.HasSuffix(line, ".")
}

// sdpFieldLineCount counts the SDP field lines of a paragraph. allFields
// reports whether every non-blank line is one (a line following a
// backslash-continued line counts, e.g. the wrapped a=fmtp value in
// TS 26.234 A.1).
func sdpFieldLineCount(text string) (count int, allFields bool) {
	prevContinued := false
	for _, ln := range strings.Split(text, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		if !prevContinued && !sdpFieldLine(ln) {
			return count, false
		}
		count++
		prevContinued = strings.HasSuffix(ln, `\`)
	}
	return count, true
}

// sipExampleStart reports whether the paragraph opens a SIP message
// example. text is the paragraph text to buffer (prefix stripped); label,
// when non-empty, is an "EXAMPLE n:" label to emit as prose first.
func sipExampleStart(info paragraphInfo) (label, text string, ok bool) {
	label, rest := splitSIPExampleLabel(codeLineText(info))
	if sipStartLine(sipFirstLine(sipNormalize(rest))) {
		return label, rest, true
	}
	return "", "", false
}

// sdpExampleStart reports whether the paragraph at elements[idx] opens a
// standalone SDP example. To keep isolated field-form templates in prose
// ("m=<media> <port> <transport> <fmt list>", TS 23.207) from matching, an
// example needs a run of at least three consecutive field lines — two when
// it starts with "v=0" — counted across the paragraph itself and the
// immediately following all-SDP paragraphs (blank paragraphs skipped).
func sdpExampleStart(elements []bodyElement, idx int) (label, text string, ok bool) {
	label, rest := splitSIPExampleLabel(codeLineText(elements[idx].Paragraph))
	norm := sipNormalize(rest)
	count, allFields := sdpFieldLineCount(norm)
	if !allFields || count == 0 {
		return "", "", false
	}
	need := 3
	if sdpVersionRE.MatchString(strings.TrimSpace(sipFirstLine(norm))) {
		need = 2
	}
	for j := idx + 1; count < need && j < len(elements); j++ {
		if elements[j].Tag != "p" {
			break
		}
		p := elements[j].Paragraph
		if len(p.Images) > 0 || p.SkippedDiagramLabels != nil {
			break
		}
		t := sipNormalize(codeLineText(p))
		if strings.TrimSpace(t) == "" {
			continue
		}
		c, a := sdpFieldLineCount(t)
		if !a {
			break
		}
		count += c
	}
	if count < need {
		return "", "", false
	}
	return label, rest, true
}

// sipBlockContinues reports whether the paragraph continues a SIP message
// block: a blank line, another start line (back-to-back messages), a
// header line, an SDP body line, or any line after a backslash-continued
// one. Leading whitespace is ignored when matching — TS 23.700-19 indents
// every message line with a tab — but indentation alone never continues a
// block, since the surrounding prose is indented the same way.
func sipBlockContinues(info paragraphInfo, lastLine string) bool {
	if len(info.Images) > 0 || info.SkippedDiagramLabels != nil {
		return false
	}
	t := sipNormalize(codeLineText(info))
	if strings.TrimSpace(t) == "" {
		return true
	}
	if strings.HasSuffix(strings.TrimSpace(lastLine), `\`) {
		return true
	}
	line := strings.TrimSpace(sipFirstLine(t))
	return sipStartLine(line) || sipHeaderLine(line) || sdpFieldLine(line)
}

// sdpBlockContinues reports whether the paragraph continues a standalone
// SDP block: a blank line, further field lines, or any line after a
// backslash-continued one. SIP headers deliberately do not continue an
// SDP-only block.
func sdpBlockContinues(info paragraphInfo, lastLine string) bool {
	if len(info.Images) > 0 || info.SkippedDiagramLabels != nil {
		return false
	}
	t := sipNormalize(codeLineText(info))
	if strings.TrimSpace(t) == "" {
		return true
	}
	if strings.HasSuffix(strings.TrimSpace(lastLine), `\`) {
		return true
	}
	_, allFields := sdpFieldLineCount(t)
	return allFields
}
