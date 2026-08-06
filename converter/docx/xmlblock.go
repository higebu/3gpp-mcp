package docx

import (
	"regexp"
	"strings"
)

// XML/DTD content detection.
//
// Several specs embed XML schemas, XML body examples and DTDs as plain body
// paragraphs with no code style or font (e.g. TS 29.163 annex F, the OMA-DM
// DDF annexes of TS 24.216/24.305, the DTDs of TS 31.220), so — like the
// Diameter definitions — they are detected by content. Detection runs on the
// raw paragraph text (before bold/italic markers are applied, since some
// specs style whole XML blocks bold or italic).
//
// A strong opener (`<?xml`, `<!DOCTYPE`) starts a block by itself: neither
// appears in prose. An ordinary tag, comment or DTD markup line only starts
// a block together with a second consecutive XML-looking paragraph, so prose
// that quotes a single element like "<userid>" on its own line never fences.
// Comment openers include the mangled forms observed in converted documents:
// "<!-" (a hyphen lost) and "<!—"/"<!–" (hyphens folded into a dash).
var (
	xmlStrongStartRE = regexp.MustCompile(`(?i)^(?:<\?xml\b|<!DOCTYPE\b)`)

	// A candidate opener additionally requires the trimmed line to end with
	// ">" (checked in matchXMLCandidateStart): an opening tag, a processing
	// instruction, a comment or a DTD markup declaration. Closing tags are
	// excluded — no block starts with one.
	xmlCandidateStartRE = regexp.MustCompile(`(?i)^(?:<[A-Za-z_]|<\?[A-Za-z]|<!(?:--|[-—–])|<!(?:ELEMENT|ATTLIST|ENTITY|NOTATION)\b)`)

	// A line that continues an open block: any tag (closing included, and
	// tolerating a mangled space after "<" as seen in TS 31.220),
	// declaration, comment — or "]>" closing a DOCTYPE internal subset.
	xmlLooseLineRE = regexp.MustCompile(`^(?:< ?/? ?[A-Za-z_]|<[?!]|\])`)

	// Cross-paragraph comment tracking only trusts a well-formed "<!--":
	// the mangled openers sometimes lose their closer entirely (TS 29.163
	// carries "<!-Definition of simple types" with no "-->" at all), and a
	// sticky comment state with no closer would swallow everything up to the
	// next heading. The closer accepts the mangled dash forms ("—>", "–>",
	// hyphens folded into one dash) but not a bare "->": an arrow inside the
	// comment text would end it early and leak whatever tags follow into the
	// element depth (issue #111).
	xmlCommentOpenRE  = regexp.MustCompile(`<!--`)
	xmlCommentCloseRE = regexp.MustCompile(`(?:--|[—–])>`)

	// An unterminated "<…" only carries over to the next line when it looks
	// like a genuine tag left open: an element tag, a processing instruction
	// or a "<!NAME" markup declaration (a DOCTYPE identifier regularly spills
	// onto the next line). A comment-shaped "<!-"/"<!—" is deliberately
	// excluded — the mangled openers sometimes have no closer at all
	// ("<!-Definition of simple types", TS 29.163) and must not go sticky,
	// for the same reason the comment tracking above only trusts "<!--" —
	// and so is a bare "<" in prose (issue #95).
	xmlPendingTagRE = regexp.MustCompile(`^<(?:[/?]?[A-Za-z_]|![A-Za-z])`)
)

// xmlTrimmedText returns the paragraph's raw text (no markdown emphasis
// markers) with NBSP normalized to a space, trimmed. NBSP is normalized for
// the same reason as matchASN1Marker.
func xmlTrimmedText(info paragraphInfo) string {
	return strings.TrimSpace(strings.ReplaceAll(codeLineText(info), "\u00a0", " "))
}

// matchXMLStrongStart reports whether the paragraph opens an XML/DTD block on
// its own: an XML declaration or a DOCTYPE.
func matchXMLStrongStart(info paragraphInfo) bool {
	return xmlStrongStartRE.MatchString(xmlTrimmedText(info))
}

// matchXMLCandidateStart reports whether the paragraph could open an XML/DTD
// block if the next paragraph also looks like XML: a whole-line tag, comment
// or DTD markup declaration ending in ">".
func matchXMLCandidateStart(info paragraphInfo) bool {
	t := xmlTrimmedText(info)
	return strings.HasSuffix(t, ">") && xmlCandidateStartRE.MatchString(t)
}

// matchXMLLine reports whether the paragraph looks like XML on its own: a
// tag/comment/declaration line, or the completion of a construct the previous
// line left unterminated. Used to decide whether a held opener candidate gets
// its required second XML line. Math paragraphs (OMML converted to $…$) never
// count, even when a tag is left unterminated.
func matchXMLLine(info paragraphInfo, tr *xmlLineTracker) bool {
	t := xmlTrimmedText(info)
	if t == "" || strings.HasPrefix(t, "$") {
		return false
	}
	return tr.open || xmlLooseLineRE.MatchString(t)
}

// xmlMaxOpenElementPlainLines bounds how many consecutive markup-free
// paragraphs an element left open (depth > 0) may absorb. Element content
// spilling onto its own paragraph is a short run of lines (see
// matchXMLContinuation), while a block whose closing tag is missing keeps
// depth above zero forever and would otherwise swallow every following
// paragraph up to the next heading or table (issue #136) — the same runaway
// the comment and pending-tag states already give up on.
const xmlMaxOpenElementPlainLines = 2

// matchXMLContinuation reports whether the paragraph continues an open XML/DTD
// block. On top of matchXMLLine, an element left open (depth > 0) absorbs
// plain text lines: element content regularly spills onto its own paragraph
// (e.g. the <xs:documentation> text of TS 24.423 clause 6.4). Only a bounded
// run of them, though: an unbalanced block must not absorb a section's prose.
func matchXMLContinuation(info paragraphInfo, tr *xmlLineTracker) bool {
	t := xmlTrimmedText(info)
	if t == "" || strings.HasPrefix(t, "$") {
		return false
	}
	if tr.open || xmlLooseLineRE.MatchString(t) {
		return true
	}
	return tr.depth > 0 && tr.plainRun < xmlMaxOpenElementPlainLines
}

// xmlLineTracker tracks parse state across the captured lines of a block:
// whether the last line left a construct unterminated — a tag whose
// attributes spill onto the next paragraph (seen in TS 38.508-1) or a comment
// spanning paragraphs — and the element nesting depth, so that element text
// content on its own paragraph stays inside the fence. While open, the block
// absorbs lines that do not themselves look like XML.
type xmlLineTracker struct {
	open      bool
	inComment bool
	depth     int    // element nesting depth of the captured lines
	pending   string // unterminated tag text carried over from the previous line
	// plainRun counts the consecutive captured lines that carried no markup
	// at all, bounding what an unbalanced element absorbs (issue #136).
	plainRun int
}

// observe updates the tracker after line has been captured into the block.
func (t *xmlLineTracker) observe(line string) {
	if t.inComment || t.pending != "" || strings.ContainsRune(line, '<') {
		t.plainRun = 0
	} else {
		t.plainRun++
	}
	rest := line
	if t.inComment {
		loc := xmlCommentCloseRE.FindStringIndex(rest)
		if loc == nil {
			t.open = true
			return
		}
		t.inComment = false
		rest = rest[loc[1]:]
	}
	if t.pending != "" {
		joined := t.pending + rest
		t.pending = ""
		i := xmlTagEnd(joined)
		switch {
		case i < 0:
			// The tag did not close on this line either. Carrying the state
			// further would absorb every following paragraph until a stray
			// ">" turns up (issue #95), so give up on the tag instead — the
			// genuine attribute-spill case (TS 38.508-1) closes on the very
			// next line.
			t.open = false
			return
		case xmlTagHasInnerLT(joined[:i+1]):
			// The join only "closed" because this line opens a construct of
			// its own: a tag body never contains an unquoted "<". Counting
			// the join as a start element would inflate the depth and eat the
			// very close tag that ends it, leaving depth stuck above zero and
			// absorbing the following prose (issue #136). Drop the carried-over
			// "<…" and read this line on its own terms instead.
		default:
			t.observeTag(joined[:i+1])
			rest = joined[i+1:]
		}
	}
	for {
		i := strings.IndexByte(rest, '<')
		if i < 0 {
			break
		}
		rest = rest[i:]
		if loc := xmlCommentOpenRE.FindStringIndex(rest); loc != nil && loc[0] == 0 {
			end := xmlCommentCloseRE.FindStringIndex(rest[loc[1]:])
			if end == nil {
				t.inComment = true
				t.open = true
				return
			}
			rest = rest[loc[1]+end[1]:]
			continue
		}
		j := xmlTagEnd(rest)
		if j < 0 {
			if xmlPendingTagRE.MatchString(rest) {
				t.pending = rest
				t.open = true
			} else {
				t.open = false
			}
			return
		}
		t.observeTag(rest[:j+1])
		rest = rest[j+1:]
	}
	t.open = false
}

// xmlTagEnd returns the index of the ">" closing the tag that starts at
// s[0] == '<', skipping over quoted attribute values so that a ">" inside
// one does not split the tag and leak element depth (issue #96). Returns -1
// when the tag does not close within s.
func xmlTagEnd(s string) int {
	var quote byte
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return i
		}
	}
	return -1
}

// xmlTagHasInnerLT reports whether the "<…>" tag in s carries another unquoted
// "<" after its opening one — the signature of a bogus join between an
// unterminated "<…" and a line that starts markup of its own, since a
// well-formed tag body cannot contain "<" (issue #136).
func xmlTagHasInnerLT(s string) bool {
	var quote byte
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '<':
			return true
		}
	}
	return false
}

// observeTag adjusts the element depth for one complete "<…>" tag.
// Declarations, processing instructions, comments, self-closing tags and
// anything unrecognizable leave it unchanged; the mangled "< name>" spelling
// (TS 31.220) counts like a well-formed tag.
func (t *xmlLineTracker) observeTag(tag string) {
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(tag, "<"), ">"))
	if inner == "" || inner[0] == '!' || inner[0] == '?' {
		return
	}
	if strings.HasSuffix(inner, "/") {
		return
	}
	if inner[0] == '/' {
		if t.depth > 0 {
			t.depth--
		}
		return
	}
	c := inner[0]
	if c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') {
		t.depth++
	}
}
