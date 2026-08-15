package tools

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetASN1Input struct {
	SpecID   string `json:"spec_id,omitempty" jsonschema:"Specification ID (e.g. TS 38.413). Omit it to look the name up across every specification in the database — use that when you do not know which specification defines the type."`
	Name     string `json:"name,omitempty" jsonschema:"ASN.1 assignment name (e.g. AMF-UE-NGAP-ID). Matching ignores case and separators, so an IE title like 'AMF UE NGAP ID' also resolves. Required when spec_id is omitted; with a spec_id, omit it to list every assignment name in the specification."`
	Version  string `json:"version,omitempty" jsonschema:"Specification version to read (e.g. 18.6.0). Also accepts an archive token (i60) or a release selector (Rel-18). Defaults to the version in the database, and requires spec_id. Use list_versions to see what exists."`
	Offset   int    `json:"offset,omitempty" jsonschema:"Start line number (0-based, default: 0)"`
	MaxLines int    `json:"max_lines,omitempty" jsonschema:"Maximum number of lines to return (default: 200)"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Maximum number of characters to return (can be combined with max_lines)"`
}

var GetASN1Tool = &mcp.Tool{
	Name:        "get_asn1",
	Description: "Get ASN.1 definitions from the 3GPP specifications. The protocol specifications (RRC TS 38.331/36.331, NGAP TS 38.413, S1AP TS 36.413, XnAP, F1AP, LPP TS 37.355, ...) write their ASN.1 between -- ASN1START / -- ASN1STOP markers, and this tool extracts every top-level assignment from those blocks. With `name`, it returns the full text of that assignment — type, constant or information object — together with the specification and section that define it, so the answer can be cited. Use it when you know a type, IE or constant name and need its definition or constraints: the defining clause can be hundreds of kilobytes, which get_section can only page through. If you do not know which specification defines the name, omit spec_id — the name is resolved across every specification in the database. Matching ignores case and separators, so an IE table title like 'AMF UE NGAP ID' finds AMF-UE-NGAP-ID. With a spec_id and no `name`, it lists every assignment name grouped by the section that defines it. Pass `version` (with spec_id) to read a past version, which is downloaded and converted on first use; call list_versions first to see which versions exist.",
}

// ASN1Assignment is one top-level ASN.1 assignment extracted from the
// ```asn1 fences of a specification.
type ASN1Assignment struct {
	// Section identifies where the assignment was found. Content is cleared.
	Section db.Section
	// Name is the identifier being assigned (the first token of the head line).
	Name string
	// Text is the assignment's full text, head line included.
	Text string
}

// asn1HeadRE matches the identifier that opens an assignment head line. ASN.1
// identifiers use hyphens, not underscores; the head itself is recognized by
// also requiring "::=" somewhere on the line, which covers type assignments
// (Name ::= ...), value assignments (maxFoo INTEGER ::= 256), parameterized
// types (Name {Class : param} ::= ...) and information objects alike.
var asn1HeadRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]*`)

// asn1ExampleMarkerRE matches a tagged extraction marker, "-- /example/
// ASN1START". The converter fences tagged blocks like untagged ones, but a
// tag marks the block as non-normative — the corpus only ever tags example
// variants — and a guideline example must not come back as a citable
// definition, least of all beside the normative definition of the same name.
var asn1ExampleMarkerRE = regexp.MustCompile(`^--\s*/[^/]*/\s*ASN1START\b`)

// isASN1ExampleMarker applies asn1ExampleMarkerRE the way the converter's
// matchASN1Marker matches marker paragraphs: NBSP normalized to space and
// surrounding whitespace trimmed, because 3GPP documents use both liberally
// and the fence keeps the marker line verbatim.
func isASN1ExampleMarker(line string) bool {
	line = strings.ReplaceAll(line, "\u00a0", " ")
	return asn1ExampleMarkerRE.MatchString(strings.TrimSpace(line))
}

// asn1Head returns the assignment name a fence line defines, or "" when the
// line does not open an assignment. "::=" is the discriminator: the lines
// inside a body — SEQUENCE fields, ENUMERATED values, object-set rows — never
// carry it, so indentation is allowed rather than required; NGAP writes its
// whole constant definitions list one tab in. Comment lines start with "--"
// and fail the identifier match. A module header carries "::=" too — NGAP
// puts "DEFINITIONS AUTOMATIC TAGS ::=" on a line of its own, RRC writes
// "NR-RRC-Definitions DEFINITIONS AUTOMATIC TAGS ::=" in one line — so any
// line with a DEFINITIONS token before the "::=" is excluded.
func asn1Head(line string) string {
	head, _, found := strings.Cut(line, "::=")
	if !found {
		return ""
	}
	// A "::=" that sits inside a line-tail comment ("field INTEGER, -- x ::= 3")
	// is comment text, not an assignment: "--" cannot occur in an identifier,
	// so its presence before the "::=" settles it.
	if strings.Contains(head, "--") {
		return ""
	}
	// NBSP counts as indentation like space and tab: fences keep the source
	// text verbatim, and 3GPP documents use NBSP liberally.
	name := asn1HeadRE.FindString(strings.TrimLeft(head, " \t\u00a0"))
	if name == "" {
		return ""
	}
	for _, tok := range strings.Fields(head) {
		if tok == "DEFINITIONS" {
			return ""
		}
	}
	return name
}

// ExtractASN1 collects every top-level ASN.1 assignment from the ```asn1
// fences of the given sections, in document order. An assignment's body runs
// to the next assignment head, so an ENUMERATED keeps the closing brace its
// values end with; a module's END line (column 0, no "::=") also closes the
// assignment before it, so the last assignment of a module does not absorb the
// header of the next.
func ExtractASN1(sections []db.Section) []ASN1Assignment {
	var out []ASN1Assignment
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
			// which shares the whole section Content's backing array — and
			// the corpus index keeps assignments for the process lifetime,
			// so a one-line constant would pin its section's full text.
			if end-start == 1 {
				text = strings.Clone(text)
			}
			out = append(out, ASN1Assignment{
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
			if isASN1ExampleMarker(line) {
				flush(i)
				skipFence = true
				continue
			}
			if h := asn1Head(line); h != "" {
				flush(i)
				start, name = i, h
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

// asn1Key reduces a name to what the two 3GPP notations agree on: the IE
// clause title says "AMF UE NGAP ID" where the ASN.1 says "AMF-UE-NGAP-ID".
func asn1Key(name string) string {
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

// MatchASN1 resolves a requested name against the extracted assignments:
// exact match first, then separator- and case-insensitive. All definitions of
// the resolved name are returned — a name defined in more than one module
// keeps every body. Shared with the CLI's get-asn1 command.
func MatchASN1(assignments []ASN1Assignment, name string) []ASN1Assignment {
	var exact []ASN1Assignment
	for _, a := range assignments {
		if a.Name == name {
			exact = append(exact, a)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	key := asn1Key(name)
	if key == "" {
		return nil
	}
	// A fuzzy key can collide across distinct names (Foo-Bar and FooBAR);
	// resolve to the first name the document defines and keep only its
	// definitions, so the answer is one type, not a mixture.
	var fuzzy []ASN1Assignment
	for _, a := range assignments {
		if asn1Key(a.Name) != key {
			continue
		}
		if len(fuzzy) == 0 || fuzzy[0].Name == a.Name {
			fuzzy = append(fuzzy, a)
		}
	}
	return fuzzy
}

// ASN1Suggestions lists names related to a query that resolved nothing: names
// that contain the query's key, or that the query's key contains — the query
// may as well be more specific than the defined name (CauseRadio vs Cause).
// Deduplicated, in document order, capped. Shared with the CLI's get-asn1
// command.
func ASN1Suggestions(assignments []ASN1Assignment, name string, max int) []string {
	key := asn1Key(name)
	if key == "" {
		return nil
	}
	seen := make(map[string]bool)
	var out []string
	for _, a := range assignments {
		nameKey := asn1Key(a.Name)
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

// ASN1SourceLine is the provenance line above one ASN.1 definition,
// e.g. "[Source: TS 38.413 v18.6.0 (Rel-18) — Section 9.4.5 — AMF-UE-NGAP-ID]".
// Shared with the CLI's get-asn1 command.
func ASN1SourceLine(a ASN1Assignment, archived bool) string {
	h := fmt.Sprintf("[Source: %s — Section %s — %s", specLabel(a.Section), a.Section.Number, a.Name)
	if archived {
		h += " (archived version)"
	}
	return h + "]"
}

// RenderASN1Definitions renders every matched definition as a source line and
// an ```asn1 fence. Shared with the CLI's get-asn1 command.
func RenderASN1Definitions(matches []ASN1Assignment, archived bool) string {
	var sb strings.Builder
	for i, a := range matches {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(ASN1SourceLine(a, archived))
		sb.WriteString("\n```asn1\n")
		sb.WriteString(a.Text)
		sb.WriteString("\n```\n")
	}
	return sb.String()
}

// RenderASN1Listing renders the assignment names grouped by defining section,
// one name per line so a page stays scannable. A name defined twice in one
// section is listed once, and the header counts what is listed, so a caller
// counting lines against the total is never off. Shared with the CLI's
// get-asn1 command.
func RenderASN1Listing(assignments []ASN1Assignment) string {
	var body strings.Builder
	total := 0
	section := ""
	seen := make(map[string]bool)
	for _, a := range assignments {
		if a.Section.Number != section {
			section = a.Section.Number
			seen = make(map[string]bool)
			title := ""
			if a.Section.Title != "" {
				title = " — " + a.Section.Title
			}
			fmt.Fprintf(&body, "\nSection %s%s:\n", section, title)
		}
		if seen[a.Name] {
			continue
		}
		seen[a.Name] = true
		total++
		body.WriteString(a.Name)
		body.WriteString("\n")
	}
	return fmt.Sprintf("%d ASN.1 assignments. Pass `name` to get one definition.\n", total) + body.String()
}

// ASN1DefiningSpecs names the specifications that define a name, deduplicated
// in document order, for the cross-spec hint on a per-spec miss. Shared with
// the CLI's get-asn1 command.
func ASN1DefiningSpecs(assignments []ASN1Assignment, name string) []string {
	var specs []string
	seen := make(map[string]bool)
	for _, a := range MatchASN1(assignments, name) {
		if !seen[a.Section.SpecID] {
			seen[a.Section.SpecID] = true
			specs = append(specs, a.Section.SpecID)
		}
	}
	return specs
}

// renderASN1Matches renders resolved definitions as a paginated result: a
// single definition gets its source line as the page-stable header, several
// get per-definition source lines under a count header.
func renderASN1Matches(matches []ASN1Assignment, archived bool, scope string, input GetASN1Input) *mcp.CallToolResult {
	if len(matches) == 1 {
		a := matches[0]
		result := paginateText("```asn1\n"+a.Text+"\n```\n", input.Offset, input.MaxLines, input.MaxChars)
		return prependLine(ASN1SourceLine(a, archived), result)
	}
	result := paginateText(RenderASN1Definitions(matches, archived), input.Offset, input.MaxLines, input.MaxChars)
	header := fmt.Sprintf("[%d definitions of %s in %s]", len(matches), matches[0].Name, scope)
	return prependLine(header, result)
}

func HandleGetASN1(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input GetASN1Input) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetASN1Input) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			if input.Name == "" {
				return errorResult("name is required when spec_id is omitted; pass a spec_id to list a specification's assignment names"), nil, nil
			}
			if input.Version != "" {
				return errorResult("version requires spec_id: the cross-specification lookup covers the versions in the database only"), nil, nil
			}
			assignments, err := src.CorpusASN1(ctx)
			if err != nil {
				return errorResult(fmt.Sprintf("failed to index ASN.1 definitions: %v", err)), nil, nil
			}
			matches := MatchASN1(assignments, input.Name)
			if len(matches) == 0 {
				msg := fmt.Sprintf("ASN.1 assignment %q not found in any specification in the database.", input.Name)
				if suggestions := ASN1Suggestions(assignments, input.Name, 20); len(suggestions) > 0 {
					msg += " Similar names: " + strings.Join(suggestions, ", ")
				}
				return errorResult(msg), nil, nil
			}
			return renderASN1Matches(matches, false, "the database", input), nil, nil
		}

		// The database version of a specification is answered from the corpus
		// index, so repeated lookups do not re-read and re-parse a document
		// that can run to tens of megabytes. A spec the index holds nothing
		// for falls through to the full read, which also covers family IDs,
		// missing specs, databases without FTS, and the no-ASN.1 error label.
		if input.Version == "" {
			if corpus, err := src.CorpusASN1(ctx); err == nil {
				var assignments []ASN1Assignment
				for _, a := range corpus {
					if a.Section.SpecID == input.SpecID {
						assignments = append(assignments, a)
					}
				}
				if len(assignments) > 0 {
					return answerASN1(ctx, src, input, assignments, specLabel(assignments[0].Section), false), nil, nil
				}
			}
		}

		sections, res, err := src.AllSections(ctx, input.SpecID, input.Version)
		if err != nil {
			return versionErrorResult(err, "failed to get ASN.1 definitions"), nil, nil
		}
		if len(sections) == 0 {
			parts, partsErr := src.DB.FindSpecIDsByFamily(ctx, input.SpecID)
			if partsErr != nil {
				return errorResult(fmt.Sprintf("failed to check %s for parts: %v", input.SpecID, partsErr)), nil, nil
			}
			if len(parts) > 0 {
				return errorResult(fmt.Sprintf("%s has multiple parts: %s — specify one", input.SpecID, strings.Join(parts, ", "))), nil, nil
			}
			return errorResult(fmt.Sprintf("specification %s not found", input.SpecID)), nil, nil
		}

		assignments := ExtractASN1(sections)
		label := specLabel(sections[0])
		if len(assignments) == 0 {
			msg := fmt.Sprintf("%s contains no ASN.1 definitions (no -- ASN1START blocks).", label)
			if input.Name != "" {
				msg += crossSpecHint(ctx, src, input.Name)
			}
			return errorResult(msg), nil, nil
		}

		return answerASN1(ctx, src, input, assignments, label, res.Archived), nil, nil
	}
}

// answerASN1 is the per-spec tail shared by the corpus-index fast path and
// the full read: list when no name was asked, resolve and render otherwise,
// with suggestions and the cross-spec hint on a miss.
func answerASN1(ctx context.Context, src *Source, input GetASN1Input, assignments []ASN1Assignment, label string, archived bool) *mcp.CallToolResult {
	if input.Name == "" {
		result := paginateText(RenderASN1Listing(assignments), input.Offset, input.MaxLines, input.MaxChars)
		header := fmt.Sprintf("[Source: %s]", label)
		if archived {
			header = fmt.Sprintf("[Source: %s (archived version)]", label)
		}
		return prependLine(header, result)
	}

	matches := MatchASN1(assignments, input.Name)
	if len(matches) == 0 {
		msg := fmt.Sprintf("ASN.1 assignment %q not found in %s.", input.Name, label)
		suggestions := ASN1Suggestions(assignments, input.Name, 20)
		if len(suggestions) > 0 {
			msg += " Similar names: " + strings.Join(suggestions, ", ")
		}
		// A name aimed at the wrong specification is the common miss — the
		// caller guessed RRC for an LPP type, say — so check where the name
		// does live before answering a bare not-found.
		hint := crossSpecHint(ctx, src, input.Name)
		msg += hint
		if len(suggestions) == 0 && hint == "" {
			msg += " Call get_asn1 without `name` to list the assignment names."
		}
		return errorResult(msg)
	}

	return renderASN1Matches(matches, archived, label, input)
}

// crossSpecHint reports where a name that missed in one specification is
// actually defined, so a wrong guess costs one call instead of a search. The
// corpus index build can fail (no FTS in a hand-built database); the hint is
// then omitted rather than turning a useful not-found into an error.
func crossSpecHint(ctx context.Context, src *Source, name string) string {
	assignments, err := src.CorpusASN1(ctx)
	if err != nil {
		return ""
	}
	specs := ASN1DefiningSpecs(assignments, name)
	if len(specs) == 0 {
		return ""
	}
	return fmt.Sprintf(" It is defined in %s — call get_asn1 with that spec_id, or with no spec_id.", strings.Join(specs, ", "))
}
