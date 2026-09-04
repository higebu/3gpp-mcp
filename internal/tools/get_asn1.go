package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/asn1index"
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
	Description: "Get an ASN.1 definition (type, IE, constant or information object) by name from the 3GPP protocol specifications (RRC TS 38.331/36.331, NGAP TS 38.413, S1AP TS 36.413, XnAP, F1AP, LPP TS 37.355, ...), with the specification and section that define it so the answer can be cited. Prefer it over get_section for ASN.1: the defining clauses are hundreds of kilobytes long. Omit spec_id to resolve the name across every specification in the database. Matching ignores case and separators, so 'AMF UE NGAP ID' finds AMF-UE-NGAP-ID. With spec_id and no name, lists every assignment name grouped by defining section. Pass version (with spec_id) to read a past version; call list_versions to see which exist.",
}

// ASN1Assignment is one top-level ASN.1 assignment extracted from the
// ```asn1 fences of a specification.
type ASN1Assignment = asn1index.Assignment

// ExtractASN1 collects every top-level ASN.1 assignment from the ```asn1
// fences of the given sections; see asn1index.Extract. Shared with the CLI's
// get-asn1 command.
func ExtractASN1(sections []db.Section) []ASN1Assignment { return asn1index.Extract(sections) }

// MatchASN1 resolves a requested name against extracted assignments; see
// asn1index.Match. It serves the paths the asn1_defs index does not cover:
// archived versions, and databases the index was never built into. Shared
// with the CLI's get-asn1 command.
func MatchASN1(assignments []ASN1Assignment, name string) []ASN1Assignment {
	return asn1index.Match(assignments, name)
}

// ASN1Suggestions lists names related to a query that resolved nothing; see
// asn1index.Suggestions. Shared with the CLI's get-asn1 command.
func ASN1Suggestions(assignments []ASN1Assignment, name string, max int) []string {
	return asn1index.Suggestions(assignments, name, max)
}

// ASN1DefAssignments views index rows as assignments so both lookup paths
// feed the same renderers. Shared with the CLI's get-asn1 command.
func ASN1DefAssignments(defs []db.ASN1Def) []ASN1Assignment {
	assignments := make([]ASN1Assignment, 0, len(defs))
	for _, def := range defs {
		assignments = append(assignments, ASN1Assignment{
			Section: db.Section{
				SpecID:  def.SpecID,
				Version: def.Version,
				Release: def.Release,
				Number:  def.SectionNumber,
				Title:   def.SectionTitle,
			},
			Name: def.Name,
			Text: def.Body,
		})
	}
	return assignments
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
			key := asn1index.Key(input.Name)
			defs, err := src.DB.LookupASN1(ctx, input.Name, key, "")
			if errors.Is(err, db.ErrNoASN1Index) {
				return errorResult(err.Error() + "; run '3gpp-mcp build-asn1-index' to add it, or pass spec_id"), nil, nil
			}
			if err != nil {
				return errorResult(fmt.Sprintf("failed to look up ASN.1 definitions: %v", err)), nil, nil
			}
			if len(defs) == 0 {
				msg := fmt.Sprintf("ASN.1 assignment %q not found in any specification in the database.", input.Name)
				if suggestions, err := src.DB.ASN1NameSuggestions(ctx, key, "", 20); err == nil && len(suggestions) > 0 {
					msg += " Similar names: " + strings.Join(suggestions, ", ")
				}
				return errorResult(msg), nil, nil
			}
			return renderASN1Matches(ASN1DefAssignments(defs), false, "the database", input), nil, nil
		}

		// The database version of a specification is answered from the
		// prebuilt index. A spec the index holds nothing for falls through
		// to the full document read, which also covers family IDs, missing
		// specs, databases without the index, and the no-ASN.1 error label.
		if input.Version == "" {
			if result, ok := answerASN1FromIndex(ctx, src.DB, input); ok {
				return result, nil, nil
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
				msg += crossSpecHint(ctx, src.DB, input.Name)
			}
			return errorResult(msg), nil, nil
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
			suggestions := ASN1Suggestions(assignments, input.Name, 20)
			if len(suggestions) > 0 {
				msg += " Similar names: " + strings.Join(suggestions, ", ")
			}
			hint := crossSpecHint(ctx, src.DB, input.Name)
			msg += hint
			if len(suggestions) == 0 && hint == "" {
				msg += " Call get_asn1 without `name` to list the assignment names."
			}
			return errorResult(msg), nil, nil
		}

		return renderASN1Matches(matches, res.Archived, label, input), nil, nil
	}
}

// answerASN1FromIndex serves a per-spec request from the asn1_defs index.
// The second return is false when the caller must fall back to the full
// document read: the index is missing, failed, or holds nothing for the
// spec — the last is indistinguishable here from a spec that does not exist,
// and the full read tells those apart.
func answerASN1FromIndex(ctx context.Context, d *db.DB, input GetASN1Input) (*mcp.CallToolResult, bool) {
	listing, err := d.ASN1SpecListing(ctx, input.SpecID)
	if err != nil || len(listing) == 0 {
		return nil, false
	}
	label := specLabel(ASN1DefAssignments(listing[:1])[0].Section)

	if input.Name == "" {
		result := paginateText(RenderASN1Listing(ASN1DefAssignments(listing)), input.Offset, input.MaxLines, input.MaxChars)
		return prependLine(fmt.Sprintf("[Source: %s]", label), result), true
	}

	key := asn1index.Key(input.Name)
	defs, err := d.LookupASN1(ctx, input.Name, key, input.SpecID)
	if err != nil {
		return nil, false
	}
	if len(defs) > 0 {
		return renderASN1Matches(ASN1DefAssignments(defs), false, label, input), true
	}

	msg := fmt.Sprintf("ASN.1 assignment %q not found in %s.", input.Name, label)
	suggestions, _ := d.ASN1NameSuggestions(ctx, key, input.SpecID, 20)
	if len(suggestions) > 0 {
		msg += " Similar names: " + strings.Join(suggestions, ", ")
	}
	// A name aimed at the wrong specification is the common miss — the
	// caller guessed RRC for an LPP type, say — so say where the name does
	// live before answering a bare not-found.
	hint := crossSpecHint(ctx, d, input.Name)
	msg += hint
	if len(suggestions) == 0 && hint == "" {
		msg += " Call get_asn1 without `name` to list the assignment names."
	}
	return errorResult(msg), true
}

// crossSpecHint reports where a name that missed in one specification is
// actually defined, so a wrong guess costs one call instead of a search. On
// a database without the index the hint is omitted rather than turning a
// useful not-found into an error.
func crossSpecHint(ctx context.Context, d *db.DB, name string) string {
	defs, err := d.LookupASN1(ctx, name, asn1index.Key(name), "")
	if err != nil || len(defs) == 0 {
		return ""
	}
	return fmt.Sprintf(" It is defined in %s — call get_asn1 with that spec_id, or with no spec_id.", strings.Join(db.ASN1DefSpecs(defs), ", "))
}
