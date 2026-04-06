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
	IncludeSubsections bool   `json:"include_subsections,omitempty" jsonschema:"Include all subsections (default: false)"`
	Offset             int    `json:"offset,omitempty" jsonschema:"Start line number (0-based, default: 0)"`
	MaxLines           int    `json:"max_lines,omitempty" jsonschema:"Maximum number of lines to return (default: 200, 0 = all)"`
	MaxChars           int    `json:"max_chars,omitempty" jsonschema:"Maximum number of characters to return (can be combined with max_lines)"`
}

var GetSectionTool = &mcp.Tool{
	Name:        "get_section",
	Description: "Get the markdown content of a specific section in a 3GPP specification. This tool is for reading specification document text (architecture, procedures, requirements). For API details such as HTTP request/response bodies, paths, and data models of 5G service-based interfaces (TS 29.xxx series), use get_openapi instead. Specify the section number with the `section_number` parameter (e.g. 5.1.2). Large sections are paginated (default 200 lines). Use offset and max_lines to navigate.",
}

func HandleGetSection(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input GetSectionInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetSectionInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}
		if input.SectionNumber == "" {
			return errorResult("section_number is required"), nil, nil
		}

		sections, err := d.GetSection(input.SpecID, input.SectionNumber, input.IncludeSubsections)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get section: %v", err)), nil, nil
		}

		if len(sections) == 0 {
			return errorResult(fmt.Sprintf("section %s not found in %s", input.SectionNumber, input.SpecID)), nil, nil
		}

		// Combine all section content
		var full strings.Builder
		for _, s := range sections {
			full.WriteString(s.Content)
			full.WriteString("\n\n")
		}

		return paginateText(full.String(), input.Offset, input.MaxLines, input.MaxChars), nil, nil
	}
}
