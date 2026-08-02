package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetSectionInput struct {
	SpecID             string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 23.501)"`
	SectionNumber      string `json:"section_number" jsonschema:"required,Section number to retrieve (e.g. 5.1.2)"`
	Version            string `json:"version,omitempty" jsonschema:"Specification version to read (e.g. 18.6.0). Also accepts an archive token (i60) or a release selector (Rel-18). Defaults to the version in the database. Use list_versions to see what exists."`
	IncludeSubsections bool   `json:"include_subsections,omitempty" jsonschema:"Include all subsections (default: false)"`
	Offset             int    `json:"offset,omitempty" jsonschema:"Start line number (0-based, default: 0)"`
	MaxLines           int    `json:"max_lines,omitempty" jsonschema:"Maximum number of lines to return (default: 200)"`
	MaxChars           int    `json:"max_chars,omitempty" jsonschema:"Maximum number of characters to return (can be combined with max_lines)"`
}

var GetSectionTool = &mcp.Tool{
	Name:        "get_section",
	Description: "Get the markdown content of a specific section in a 3GPP specification. This tool is for reading specification document text (architecture, procedures, requirements). For API details such as HTTP request/response bodies, paths, and data models of 5G service-based interfaces (TS 29.xxx series), use get_openapi instead. Specify the section number with the `section_number` parameter (e.g. 5.1.2). Figures appear as `![...](image://NAME)` links; fetch one with get_image and that NAME. Pass `version` to read a past version, which is downloaded and converted on first use; call list_versions first to see which versions exist. Large sections are paginated (default 200 lines). Use offset and max_lines to navigate.",
}

func HandleGetSection(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input GetSectionInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetSectionInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}
		if input.SectionNumber == "" {
			return errorResult("section_number is required"), nil, nil
		}

		sections, res, err := src.GetSection(ctx, input.SpecID, input.Version, input.SectionNumber, input.IncludeSubsections)
		if err != nil {
			return versionErrorResult(err, "failed to get section"), nil, nil
		}

		if len(sections) == 0 {
			if parts, partsErr := src.DB.FindSpecIDsByFamily(input.SpecID); partsErr == nil && len(parts) > 0 {
				return errorResult(fmt.Sprintf("%s has multiple parts: %s — specify one", input.SpecID, strings.Join(parts, ", "))), nil, nil
			}
			return errorResult(fmt.Sprintf("section %s not found in %s%s", input.SectionNumber, input.SpecID, versionSuffix(res))), nil, nil
		}

		// Combine all section content
		var full strings.Builder
		for _, s := range sections {
			full.WriteString(s.Content)
			full.WriteString("\n\n")
		}

		result := paginateText(full.String(), input.Offset, input.MaxLines, input.MaxChars)
		header := sourceHeader(sections[0], input.IncludeSubsections && len(sections) > 1, res.Archived)
		return prependLine(header, result), nil, nil
	}
}

// sourceHeader builds the provenance line prepended to every get_section page,
// e.g. "[Source: TS 23.501 v18.6.0 (Rel-18) — Section 5.1]". Archived versions
// say so, because get_references only covers the version the database was
// built with and would silently answer about a different one; images are
// downloaded on first use, but only when the image tools are told the version,
// so the header reminds the caller to pass it.
func sourceHeader(s db.Section, withSubsections, archived bool) string {
	h := fmt.Sprintf("[Source: %s — Section %s", specLabel(s), s.Number)
	if withSubsections {
		h += " (+subsections)"
	}
	if archived {
		h += " (archived version; cross-references unavailable; pass this version to get_image/list_images)"
	}
	return h + "]"
}

// versionSuffix names the version in a not-found message when one was resolved.
func versionSuffix(res Resolution) string {
	if res.Version == "" {
		return ""
	}
	return " v" + res.Version
}
