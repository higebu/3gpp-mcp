package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// MaxReferences bounds a single get_references response.
const MaxReferences = 500

type GetReferencesInput struct {
	SpecID             string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 23.501)"`
	SectionNumber      string `json:"section_number,omitempty" jsonschema:"Section number (e.g. 5.1.2). Required for outgoing direction."`
	Direction          string `json:"direction,omitempty" jsonschema:"outgoing (default): references FROM this section to other specs. incoming: references TO this spec/section from other specs."`
	IncludeSubsections bool   `json:"include_subsections,omitempty" jsonschema:"Include subsections when collecting outgoing references (default: false)"`
	Offset             int    `json:"offset,omitempty" jsonschema:"Number of references to skip, for paging past a truncated response (default: 0)"`
}

var GetReferencesTool = &mcp.Tool{
	Name: "get_references",
	Description: `Get cross-references between 3GPP specifications and RFCs.

Directions:
- outgoing (default): Find all specs/RFCs referenced by a given section.
  Requires spec_id and section_number. Use include_subsections to also gather refs from child sections.
- incoming: Find all sections that reference a given spec (and optionally a specific section).
  Requires spec_id. section_number is optional.

Returns structured reference data including target spec, section, title (if available in DB), and context snippet.
Responses are capped at 500 references; a separate notice reports the total, and offset pages through the rest.`,
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

		// Cross-references are only extracted for the version the database was
		// built with, so this always answers about that version.
		refs, err := d.GetReferences(ctx, input.SpecID, "", input.SectionNumber, direction, input.IncludeSubsections)
		if err != nil {
			return errorResult(fmt.Sprintf("get references failed: %v", err)), nil, nil
		}

		if len(refs) == 0 {
			return textResult("[]"), nil, nil
		}

		// A heavily-cited spec can have tens of thousands of incoming
		// references; without a cap a single call would serialize them all.
		// offset makes the rows beyond the cap reachable.
		total := len(refs)
		offset := input.Offset
		if offset < 0 {
			offset = 0
		}
		if offset >= total {
			return referencesResult("[]",
				fmt.Sprintf("[No references at offset %d. Total references: %d]", offset, total)), nil, nil
		}
		end := offset + MaxReferences
		if end > total {
			end = total
		}
		refs = refs[offset:end]

		data, err := json.MarshalIndent(refs, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal: %v", err)), nil, nil
		}

		// The notice travels as its own content item so the JSON payload
		// stays parseable even when the result is truncated.
		notice := ""
		if offset > 0 || end < total {
			notice = fmt.Sprintf("[Showing references %d-%d of %d.", offset+1, end, total)
			if end < total {
				notice += fmt.Sprintf(" Use offset=%d to continue, or narrow the query with section_number.", end)
			}
			notice += "]"
		}
		return referencesResult(string(data), notice), nil, nil
	}
}

// referencesResult builds a result whose first content item is the JSON
// payload; a non-empty notice is appended as a separate item so it never
// corrupts the JSON.
func referencesResult(payload, notice string) *mcp.CallToolResult {
	res := textResult(payload)
	if notice != "" {
		res.Content = append(res.Content, &mcp.TextContent{Text: notice})
	}
	return res
}
