package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetReferencesInput struct {
	SpecID             string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 23.501)"`
	SectionNumber      string `json:"section_number,omitempty" jsonschema:"Section number (e.g. 5.1.2). Required for outgoing direction."`
	Direction          string `json:"direction,omitempty" jsonschema:"outgoing (default): references FROM this section to other specs. incoming: references TO this spec/section from other specs."`
	IncludeSubsections bool   `json:"include_subsections,omitempty" jsonschema:"Include subsections when collecting outgoing references (default: false)"`
}

var GetReferencesTool = &mcp.Tool{
	Name: "get_references",
	Description: `Get cross-references between 3GPP specifications and RFCs.

Directions:
- outgoing (default): Find all specs/RFCs referenced by a given section.
  Requires spec_id and section_number. Use include_subsections to also gather refs from child sections.
- incoming: Find all sections that reference a given spec (and optionally a specific section).
  Requires spec_id. section_number is optional.

Returns structured reference data including target spec, section, title (if available in DB), and context snippet.`,
}

func HandleGetReferences(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input GetReferencesInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetReferencesInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}

		direction := input.Direction
		if direction == "" {
			direction = db.DirectionOutgoing
		}

		if direction == db.DirectionOutgoing && input.SectionNumber == "" {
			return errorResult("section_number is required for outgoing direction"), nil, nil
		}

		refs, err := d.GetReferences(input.SpecID, input.SectionNumber, direction, input.IncludeSubsections)
		if err != nil {
			return errorResult(fmt.Sprintf("get references failed: %v", err)), nil, nil
		}

		if len(refs) == 0 {
			return textResult("[]"), nil, nil
		}

		data, err := json.MarshalIndent(refs, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal: %v", err)), nil, nil
		}

		return textResult(string(data)), nil, nil
	}
}
