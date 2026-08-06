package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SearchInput struct {
	Query string `json:"query" jsonschema:"required,FTS5 query string. Hyphenated or dotted terms like IMS-AKA and 38.101 are auto-quoted. Use AND/OR/NOT operators and double-quoted phrases for exact matches (e.g. '\"core network\" AND AMF')."`
	// Deprecated: use SpecIDs instead. Ignored when SpecIDs is non-empty.
	SpecID  string   `json:"spec_id,omitempty" jsonschema:"Limit search to a single specification (e.g. TS 23.501). Ignored when spec_ids is provided."`
	SpecIDs []string `json:"spec_ids,omitempty" jsonschema:"Limit search to one or more specifications (e.g. [\"TS 23.501\", \"TS 23.502\"]). Takes precedence over spec_id."`
	Limit   int      `json:"limit,omitempty" jsonschema:"Maximum number of results per page (default: 10, max: 200)"`
	Offset  int      `json:"offset,omitempty" jsonschema:"Number of results to skip for pagination (default: 0). Combine with total_count in the response to page through all matches."`
}

var SearchTool = &mcp.Tool{
	Name: "search",
	Description: `Full-text search across 3GPP specifications using SQLite FTS5 syntax.

Query syntax:
- AND/OR/NOT:    AMF AND authentication
- Phrase:        "service based interface"
- Prefix:        handov*
- Column filter: title:authentication  or  content:handover
- Proximity:     NEAR(AMF UE, 5)
- Hyphenated or dotted terms (e.g. IMS-AKA, sec-agree, 38.101) are auto-quoted to avoid FTS5 syntax errors.
- After a positive term, "-term" excludes that term (e.g. AMF -SMF), same as NOT. An exclusion cannot begin a query or immediately follow AND/OR.

Stemming:
- The index uses porter stemming: inflected English forms match each other (handover finds handovers).
- Prefix and phrase queries operate on stemmed forms, so they can match a bit more broadly than the exact surface text.

Pagination:
- Results come as {results, total_count, limit, offset}; total_count is the full match count.
- Use limit (default 10, max 200) and offset to page through matches beyond the first page.

Tips:
- Use exact 3GPP terms (AMF, SMF, gNB, UE, NRF, PCF, etc.)
- Phrase search improves precision for multi-word concepts
- title:term restricts matches to section headings only
- Use spec_ids to search across multiple specifications at once`,
}

func HandleSearch(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input SearchInput) (*mcp.CallToolResult, any, error) {
		if input.Query == "" {
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
