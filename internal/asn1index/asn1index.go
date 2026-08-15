// Package asn1index extracts the top-level ASN.1 assignments from the
// ```asn1 fences of converted specifications and maintains the asn1_defs
// name index behind get_asn1's lookups. Extraction is also used directly,
// without the index, for archived versions and for databases the index was
// never built into.
package asn1index

import (
	"regexp"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/db"
)

// Assignment is one top-level ASN.1 assignment extracted from the ```asn1
// fences of a specification.
type Assignment struct {
	// Section identifies where the assignment was found. Content is cleared.
	Section db.Section
	// Name is the identifier being assigned (the first token of the head line).
	Name string
	// Text is the assignment's full text, head line included.
	Text string
}

// headRE matches the identifier that opens an assignment head line. ASN.1
// identifiers use hyphens, not underscores; the head itself is recognized by
// also requiring "::=" somewhere on the line, which covers type assignments
// (Name ::= ...), value assignments (maxFoo INTEGER ::= 256), parameterized
// types (Name {Class : param} ::= ...) and information objects alike.
var headRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*`)

// exampleMarkerRE matches a tagged extraction marker, "-- /example/
// ASN1START". The converter fences tagged blocks like untagged ones, but a
// tag marks the block as non-normative — the corpus only ever tags example
// variants — and a guideline example must not come back as a citable
// definition, least of all beside the normative definition of the same name.
var exampleMarkerRE = regexp.MustCompile(`^--\s*/[^/]*/\s*ASN1START\b`)

// isExampleMarker applies exampleMarkerRE the way the converter's
// matchASN1Marker matches marker paragraphs: NBSP normalized to space and
// surrounding whitespace trimmed, because 3GPP documents use both liberally
// and the fence keeps the marker line verbatim.
func isExampleMarker(line string) bool {
	line = strings.ReplaceAll(line, "\u00a0", " ")
	return exampleMarkerRE.MatchString(strings.TrimSpace(line))
}

// head returns the assignment name a fence line defines, or "" when the line
// does not open an assignment. "::=" is the discriminator: the lines inside a
// body — SEQUENCE fields, ENUMERATED values, object-set rows — never carry
// it, so indentation is allowed rather than required; NGAP writes its whole
// constant definitions list one tab in. Comment lines start with "--" and
// fail the identifier match. A module header carries "::=" too — NGAP puts
// "DEFINITIONS AUTOMATIC TAGS ::=" on a line of its own, RRC writes
// "NR-RRC-Definitions DEFINITIONS AUTOMATIC TAGS ::=" in one line — so any
// line with a DEFINITIONS token before the "::=" is excluded.
func head(line string) string {
	before, _, found := strings.Cut(line, "::=")
	if !found {
		return ""
	}
	// A "::=" that sits inside a line-tail comment ("field INTEGER, -- x ::= 3")
	// is comment text, not an assignment: "--" cannot occur in an identifier,
	// so its presence before the "::=" settles it.
	if strings.Contains(before, "--") {
		return ""
	}
	// NBSP counts as indentation like space and tab: fences keep the source
	// text verbatim, and 3GPP documents use NBSP liberally.
	name := headRE.FindString(strings.TrimLeft(before, " \t\u00a0"))
	if name == "" {
		return ""
	}
	for _, tok := range strings.Fields(before) {
		if tok == "DEFINITIONS" {
			return ""
		}
	}
	return name
}

// Extract collects every top-level ASN.1 assignment from the ```asn1 fences
// of the given sections, in document order. An assignment's body runs to the
// next assignment head, so an ENUMERATED keeps the closing brace its values
// end with; a module's END line (column 0, no "::=") also closes the
// assignment before it, so the last assignment of a module does not absorb
// the header of the next.
func Extract(sections []db.Section) []Assignment {
	var out []Assignment
	for _, sec := range sections {
		meta := sec
		meta.Content = ""
		lines := strings.Split(sec.Content, "\n")
		inFence, skipFence := false, false
		start, name := -1, ""
		flush := func(end int) {
			if start < 0 {
				return
			}
			// Trailing blank and comment-only lines are separators, not body:
			// the -- ASN1STOP marker and RRC's -- TAG-...-STOP line trail the
			// last assignment of a fence, and a banner comment introducing
			// the next assignment trails the one before it. A comment on the
			// tail of a code line stays — only whole comment lines go.
			for end > start+1 {
				t := strings.TrimSpace(lines[end-1])
				if t != "" && !strings.HasPrefix(t, "--") {
					break
				}
				end--
			}
			text := strings.Join(lines[start:end], "\n")
			// Join's single-element fast path returns the split substring,
			// which shares the whole section Content's backing array; clone
			// so a one-line constant does not pin its section's full text.
			if end-start == 1 {
				text = strings.Clone(text)
			}
			out = append(out, Assignment{
				Section: meta,
				Name:    name,
				Text:    text,
			})
			start = -1
		}
		for i, line := range lines {
			if !inFence {
				if line == "```asn1" {
					inFence, skipFence = true, false
				}
				continue
			}
			if strings.HasPrefix(line, "```") {
				flush(i)
				inFence = false
				continue
			}
			if skipFence {
				continue
			}
			if isExampleMarker(line) {
				flush(i)
				skipFence = true
				continue
			}
			if h := head(line); h != "" {
				flush(i)
				// Clone for the same reason Text clones its single-line
				// case: the head substring would otherwise keep the whole
				// section Content reachable.
				start, name = i, strings.Clone(h)
			} else if strings.TrimSpace(line) == "END" {
				flush(i)
			}
		}
		// The converter always closes its fences, but content is not trusted
		// to: an unterminated fence still yields the assignment.
		flush(len(lines))
	}
	return out
}

// Key reduces a name to what the two 3GPP notations agree on: the IE clause
// title says "AMF UE NGAP ID" where the ASN.1 says "AMF-UE-NGAP-ID".
func Key(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		}
	}
	return b.String()
}

// Match resolves a requested name against extracted assignments: exact match
// first, then separator- and case-insensitive. All definitions of the
// resolved name are returned — a name defined in more than one module keeps
// every body. It is the in-memory twin of db.LookupASN1, for archived
// versions and databases without the index.
func Match(assignments []Assignment, name string) []Assignment {
	var exact []Assignment
	for _, a := range assignments {
		if a.Name == name {
			exact = append(exact, a)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	key := Key(name)
	if key == "" {
		return nil
	}
	// A fuzzy key can collide across distinct names (Foo-Bar and FooBAR);
	// resolve to the first name the document defines and keep only its
	// definitions, so the answer is one type, not a mixture.
	var fuzzy []Assignment
	for _, a := range assignments {
		if Key(a.Name) != key {
			continue
		}
		if len(fuzzy) == 0 || fuzzy[0].Name == a.Name {
			fuzzy = append(fuzzy, a)
		}
	}
	return fuzzy
}

// Suggestions lists names related to a query that resolved nothing: names
// that contain the query's key, or that the query's key contains — the query
// may as well be more specific than the defined name (CauseRadio vs Cause).
// Deduplicated, in document order, capped. It is the in-memory twin of
// db.ASN1NameSuggestions.
func Suggestions(assignments []Assignment, name string, max int) []string {
	key := Key(name)
	if key == "" {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, a := range assignments {
		nameKey := Key(a.Name)
		if seen[a.Name] || (!strings.Contains(nameKey, key) && !strings.Contains(key, nameKey)) {
			continue
		}
		seen[a.Name] = true
		out = append(out, a.Name)
		if len(out) == max {
			break
		}
	}
	return out
}
