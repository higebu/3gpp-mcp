package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SearchInput struct {
	Query string `json:"query" jsonschema:"required,FTS5 query string: AND/OR/NOT (AMF AND authentication), a double-quoted phrase (\"service based interface\"), a prefix (handov*), a column filter (title:authentication or content:handover) or proximity (NEAR(AMF UE, 5)). Hyphenated or dotted terms like IMS-AKA and 38.101 are auto-quoted. After a positive term, -term excludes it (AMF -SMF), same as NOT; an exclusion cannot begin a query or follow AND/OR."`
	// Deprecated: use SpecIDs instead. Ignored when SpecIDs is non-empty.
	SpecID  string   `json:"spec_id,omitempty" jsonschema:"Limit search to a single specification (e.g. TS 23.501). Ignored when spec_ids is provided."`
	SpecIDs []string `json:"spec_ids,omitempty" jsonschema:"Limit search to one or more specifications (e.g. [\"TS 23.501\", \"TS 23.502\"]). Takes precedence over spec_id."`
	Limit   int      `json:"limit,omitempty" jsonschema:"Maximum number of results per page (default: 10, max: 200)"`
	Offset  int      `json:"offset,omitempty" jsonschema:"Number of results to skip for pagination (default: 0). Combine with total_count in the response to page through all matches."`
}

var SearchTool = &mcp.Tool{
	Name:        "search",
	Description: `Full-text search across 3GPP specification text using SQLite FTS5 syntax (AND/OR/NOT, "quoted phrases", prefix*, title:/content: column filters, NEAR(); see the query parameter). The index applies porter stemming, so inflected English forms match each other (handover finds handovers) and prefix and phrase queries match a bit more broadly than the exact surface text. Use exact 3GPP terms (AMF, SMF, gNB, UE, NRF, PCF), a phrase for a multi-word concept, title:term to match section headings only, and spec_ids to search several specifications at once. Results come as {results, total_count, limit, offset}; page with limit (default 10, max 200) and offset. This index covers specification clause text only: OpenAPI content is in search_openapi.`,
}

func HandleSearch(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(input.Query) == "" {
			return errorResult("query is required"), nil, nil
		}

		limit := input.Limit
		if limit <= 0 {
			limit = db.DefaultSearchLimit
		}

		specIDs := input.SpecIDs
		if len(specIDs) == 0 && input.SpecID != "" {
			specIDs = []string{input.SpecID}
		}

		offset := input.Offset
		if offset < 0 {
			offset = 0
		}

		results, err := d.Search(ctx, input.Query, specIDs, limit, offset)
		if err != nil {
			return errorResult(fmt.Sprintf("search failed: %v", err)), nil, nil
		}

		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal: %v", err)), nil, nil
		}

		return textResult(string(data)), nil, nil
	}
}
