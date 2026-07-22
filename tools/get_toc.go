package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetTOCInput struct {
	SpecID string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 23.501)"`
}

var GetTOCTool = &mcp.Tool{
	Name:        "get_toc",
	Description: "Get the table of contents (section structure) of a 3GPP specification.",
}

func HandleGetTOC(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input GetTOCInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetTOCInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}

		sections, err := d.GetTOC(input.SpecID)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get TOC: %v", err)), nil, nil
		}

		if len(sections) == 0 {
			if parts, partsErr := d.FindSpecIDsByFamily(input.SpecID); partsErr == nil && len(parts) > 0 {
				return errorResult(fmt.Sprintf("%s has multiple parts: %s — specify one", input.SpecID, strings.Join(parts, ", "))), nil, nil
			}
			return errorResult(fmt.Sprintf("no sections found for %s", input.SpecID)), nil, nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "# %s - Table of Contents\n\n", input.SpecID)
		for _, s := range sections {
			indent := strings.Repeat("  ", s.Level-1)
			if s.Number != "" && s.Number != s.Title {
				fmt.Fprintf(&sb, "%s- %s %s\n", indent, s.Number, s.Title)
			} else {
				fmt.Fprintf(&sb, "%s- %s\n", indent, s.Title)
			}
		}

		return textResult(sb.String()), nil, nil
	}
}
