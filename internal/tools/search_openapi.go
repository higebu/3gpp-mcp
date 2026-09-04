package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type SearchOpenAPIInput struct {
	Query       string   `json:"query" jsonschema:"required,FTS5 query string: AND/OR/NOT (NFProfile AND heartbeat), a double-quoted phrase (\"nf instances\"), a prefix (subscri*) or a column filter (name:NFProfile, body:nfInstanceId, api_name:Nnrf_NFManagement). Hyphenated, dotted or underscored terms like nf-instances, 29.510 and Nnrf_NFManagement are auto-quoted. No stemming: identifiers match as written. '-', '.' and '_' split tokens, so Nnrf_NFManagement is indexed as nnrf and nfmanagement and /nf-instances as nf and instances — a partial name matches — but camelCase is not split (supportedFeatures is one token)."`
	SpecIDs     []string `json:"spec_ids,omitempty" jsonschema:"Limit the search to one or more specifications (e.g. [\"TS 29.510\", \"TS 29.518\"])."`
	APIName     string   `json:"api_name,omitempty" jsonschema:"Limit the search to a single API document (e.g. Nnrf_NFManagement). Use list_openapi to see the available names."`
	Kind        string   `json:"kind,omitempty" jsonschema:"Limit the search to one kind of definition: \"schema\" for data types or \"operation\" for endpoints. Both are searched when omitted."`
	IncludeBody bool     `json:"include_body,omitempty" jsonschema:"Return the full text of each matching definition instead of a snippet (default: false). Costs many more tokens; prefer the default and follow up with get_openapi."`
	Limit       int      `json:"limit,omitempty" jsonschema:"Maximum number of results per page (default: 10, max: 200)"`
	Offset      int      `json:"offset,omitempty" jsonschema:"Number of results to skip for pagination (default: 0). Combine with total_count in the response to page through all matches."`
}

var SearchOpenAPITool = &mcp.Tool{
	Name:        "search_openapi",
	Description: `Full-text search across the OpenAPI definitions of the 5G service-based interface APIs (TS 29.xxx series), using SQLite FTS5 syntax (see the query parameter). Use it when you need an API detail but do not know which API document holds it: a data type (NFProfile, SmContextCreateData) or an endpoint (/nf-instances) is found without guessing an api_name. One hit is one definition: a schema from components.schemas or one operation ("PUT /nf-instances/{nfInstanceID}"), reported as spec_id, api_name, kind, name and a snippet to pass to get_openapi for the full text. A single bare term ranks the definition of exactly that name first. A schema's text carries one level of $ref expansion, so referenced field names match but a type two hops away does not. Separate from search, which never returns OpenAPI content. Results come as {results, total_count, limit, offset}; page with limit (default 10, max 200) and offset.`,
}

func HandleSearchOpenAPI(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input SearchOpenAPIInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input SearchOpenAPIInput) (*mcp.CallToolResult, any, error) {
		if input.Query == "" {
			return errorResult("query is required"), nil, nil
		}

		offset := input.Offset
		if offset < 0 {
			offset = 0
		}

		results, err := d.SearchOpenAPI(ctx, input.Query, input.SpecIDs, input.APIName, input.Kind, input.IncludeBody, input.Limit, offset)
		if err != nil {
			if errors.Is(err, db.ErrNoOpenAPIIndex) {
				return errorResult("This database has no OpenAPI search index. It was built before search_openapi existed; rebuild it with \"3gpp-mcp build-openapi-index --db <path>\", or use list_openapi and get_openapi instead."), nil, nil
			}
			return errorResult(fmt.Sprintf("search failed: %v", err)), nil, nil
		}

		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal: %v", err)), nil, nil
		}

		return textResult(string(data)), nil, nil
	}
}
