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
	Query       string   `json:"query" jsonschema:"required,FTS5 query string. Hyphenated or dotted terms like nf-instances and 29.510 are auto-quoted. Use AND/OR/NOT operators and double-quoted phrases for exact matches."`
	SpecIDs     []string `json:"spec_ids,omitempty" jsonschema:"Limit the search to one or more specifications (e.g. [\"TS 29.510\", \"TS 29.518\"])."`
	APIName     string   `json:"api_name,omitempty" jsonschema:"Limit the search to a single API document (e.g. Nnrf_NFManagement). Use list_openapi to see the available names."`
	Kind        string   `json:"kind,omitempty" jsonschema:"Limit the search to one kind of definition: \"schema\" for data types or \"operation\" for endpoints. Both are searched when omitted."`
	IncludeBody bool     `json:"include_body,omitempty" jsonschema:"Return the full text of each matching definition instead of a snippet (default: false). Costs many more tokens; prefer the default and follow up with get_openapi."`
	Limit       int      `json:"limit,omitempty" jsonschema:"Maximum number of results per page (default: 10, max: 200)"`
	Offset      int      `json:"offset,omitempty" jsonschema:"Number of results to skip for pagination (default: 0). Combine with total_count in the response to page through all matches."`
}

var SearchOpenAPITool = &mcp.Tool{
	Name: "search_openapi",
	Description: `Full-text search across the OpenAPI definitions of the 5G service-based interface APIs (TS 29.xxx series), using SQLite FTS5 syntax.

Use this when you need an API detail but do not know which API document holds it — searching for a data type (NFProfile, SmContextCreateData) or an endpoint (/nf-instances, subscriptions) finds it without guessing an api_name first. When you already know the document, get_openapi reads it directly.

This is a separate index from the search tool: search covers specification clause text and never returns OpenAPI content, and this tool covers OpenAPI content only.

Results:
- One hit is one definition, not one document: either a schema (a data type from components.schemas) or an operation (one HTTP method of one path, named like "PUT /nf-instances/{nfInstanceID}").
- A query that is a single bare term ranks a definition of exactly that name first, so searching NFProfile returns the NFProfile schema itself ahead of the schemas that merely reference it.
- Each hit reports spec_id, api_name, kind, name and a snippet. Pass those to get_openapi (with its schema or path parameter) to read the full definition.
- A schema's text carries one level of $ref expansion, so referenced field names are searchable, but a type two hops away is not. Follow it up with get_openapi.
- Set include_body to get the matched definition's full text inline. It is much larger than a snippet.

Query syntax:
- AND/OR/NOT:    NFProfile AND heartbeat
- Phrase:        "nf instances"
- Prefix:        subscri*
- Column filter: name:NFProfile  or  body:nfInstanceId  or  api_name:Nnrf_NFManagement
- Hyphenated, dotted or underscored terms (e.g. nf-instances, 29.510, Nnrf_NFManagement) are auto-quoted to avoid FTS5 syntax errors.

Tokenization:
- Unlike search, this index applies no stemming: identifiers are matched as written, and inflected English forms do not fold together.
- '-', '.' and '_' split tokens, so Nnrf_NFManagement is indexed as "nnrf" and "nfmanagement" and /nf-instances as "nf" and "instances" — a partial name matches, but camelCase is not split (supportedFeatures is one token).

Pagination:
- Results come as {results, total_count, limit, offset}; total_count is the full match count.
- Use limit (default 10, max 200) and offset to page through matches beyond the first page.`,
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
