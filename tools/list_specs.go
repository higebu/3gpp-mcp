package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListSpecsInput struct {
	Series string `json:"series,omitempty" jsonschema:"Filter by series number (e.g. 23 for TS 23.xxx)"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of results to return (default: 20)"`
	Offset int    `json:"offset,omitempty" jsonschema:"Number of results to skip for pagination (default: 0)"`
}

var ListSpecsTool = &mcp.Tool{
	Name:        "list_specs",
	Description: "List available 3GPP specifications. Optionally filter by series number. Results are paginated (default 20 per page); use limit and offset to navigate.",
}

func HandleListSpecs(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input ListSpecsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListSpecsInput) (*mcp.CallToolResult, any, error) {
		result, err := d.ListSpecs(input.Series, input.Limit, input.Offset)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to list specs: %v", err)), nil, nil
		}

		data, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal: %v", err)), nil, nil
		}

		return textResult(string(data)), nil, nil
	}
}
