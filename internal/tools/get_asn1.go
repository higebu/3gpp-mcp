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
	SpecID   string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 38.413)"`
	Name     string `json:"name,omitempty" jsonschema:"ASN.1 assignment name (e.g. AMF-UE-NGAP-ID). Matching ignores case and separators, so an IE title like 'AMF UE NGAP ID' also resolves. Omit to list every assignment name in the specification."`
	Version  string `json:"version,omitempty" jsonschema:"Specification version to read (e.g. 18.6.0). Also accepts an archive token (i60) or a release selector (Rel-18). Defaults to the version in the database. Use list_versions to see what exists."`
	Offset   int    `json:"offset,omitempty" jsonschema:"Start line number (0-based, default: 0)"`
	MaxLines int    `json:"max_lines,omitempty" jsonschema:"Maximum number of lines to return (default: 200)"`
	MaxChars int    `json:"max_chars,omitempty" jsonschema:"Maximum number of characters to return (can be combined with max_lines)"`
}

var GetASN1Tool = &mcp.Tool{
	Name:        "get_asn1",
	Description: "Get ASN.1 definitions from a 3GPP specification. The protocol specifications (RRC TS 38.331/36.331, NGAP TS 38.413, S1AP TS 36.413, XnAP, F1AP, ...) write their ASN.1 between -- ASN1START / -- ASN1STOP markers, and this tool extracts every top-level assignment from those blocks. With `name`, it returns the full text of that assignment — type, constant or information object — together with the section that defines it, so the answer can be cited. Use it when you know a type, IE or constant name and need its definition or constraints: the defining clause can be hundreds of kilobytes, which get_section can only page through. Matching ignores case and separators, so an IE table title like 'AMF UE NGAP ID' finds AMF-UE-NGAP-ID. Without `name`, it lists every assignment name grouped by the section that defines it. Pass `version` to read a past version, which is downloaded and converted on first use; call list_versions first to see which versions exist.",
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

// asn1Head returns the assignment name a fence line defines, or "" when the
// line does not open an assignment. "::=" is the discriminator: the lines
// inside a body — SEQUENCE fields, ENUMERATED values, object-set rows — never
// carry it, so indentation is allowed rather than required; NGAP writes its
// whole constant definitions list one tab in. Comment lines start with "--"
// and fail the identifier match. A module header's "DEFINITIONS ... ::= BEGIN"
// line carries "::=" too and is excluded by its keyword.
func asn1Head(line string) string {
	if !strings.Contains(line, "::=") {
		return ""
	}
	name := asn1HeadRE.FindString(strings.TrimLeft(line, " \t"))
	if name == "" || name == "DEFINITIONS" {
		return ""
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
		inFence := false
		start, name := -1, ""
		flush := func(end int) {
			if start < 0 {
				return
			}
			for end > start && strings.TrimSpace(lines[end-1]) == "" {
				end--
			}
			out = append(out, ASN1Assignment{
				Section: meta,
				Name:    name,
				Text:    strings.Join(lines[start:end], "\n"),
			})
			start = -1
		}
		for i, line := range lines {
			if !inFence {
				inFence = line == "```asn1"
				continue
			}
			if strings.HasPrefix(line, "```") {
				flush(i)
				inFence = false
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
// section is listed once. Shared with the CLI's get-asn1 command.
func RenderASN1Listing(assignments []ASN1Assignment) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%d ASN.1 assignments. Pass `name` to get one definition.\n", len(assignments))
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
			fmt.Fprintf(&sb, "\nSection %s%s:\n", section, title)
		}
		if seen[a.Name] {
			continue
		}
		seen[a.Name] = true
		sb.WriteString(a.Name)
		sb.WriteString("\n")
	}
	return sb.String()
}

func HandleGetASN1(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input GetASN1Input) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetASN1Input) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
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
			return errorResult(fmt.Sprintf("%s contains no ASN.1 definitions (no -- ASN1START blocks)", label)), nil, nil
		}

		if input.Name == "" {
			result := paginateText(RenderASN1Listing(assignments), input.Offset, input.MaxLines, input.MaxChars)
			header := fmt.Sprintf("[Source: %s]", label)
			if res.Archived {
				header = fmt.Sprintf("[Source: %s (archived version)]", label)
			}
			return prependLine(header, result), nil, nil
		}

		matches := MatchASN1(assignments, input.Name)
		if len(matches) == 0 {
			msg := fmt.Sprintf("ASN.1 assignment %q not found in %s.", input.Name, label)
			if suggestions := ASN1Suggestions(assignments, input.Name, 20); len(suggestions) > 0 {
				msg += " Similar names: " + strings.Join(suggestions, ", ")
			} else {
				msg += " Call get_asn1 without `name` to list the assignment names."
			}
			return errorResult(msg), nil, nil
		}

		if len(matches) == 1 {
			a := matches[0]
			result := paginateText("```asn1\n"+a.Text+"\n```\n", input.Offset, input.MaxLines, input.MaxChars)
			return prependLine(ASN1SourceLine(a, res.Archived), result), nil, nil
		}
		result := paginateText(RenderASN1Definitions(matches, res.Archived), input.Offset, input.MaxLines, input.MaxChars)
		header := fmt.Sprintf("[%d definitions of %s in %s]", len(matches), matches[0].Name, label)
		return prependLine(header, result), nil, nil
	}
}
