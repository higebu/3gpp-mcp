package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"gopkg.in/yaml.v3"
)

// list_openapi

type ListOpenAPIInput struct {
	SpecID string `json:"spec_id,omitempty" jsonschema:"Filter by specification ID (e.g. TS 29.510)"`
}

var ListOpenAPITool = &mcp.Tool{
	Name:        "list_openapi",
	Description: "List available OpenAPI definitions from 3GPP specifications (TS 29.xxx series). Use this to discover API names before calling get_openapi. Optionally filter by spec ID.",
}

func HandleListOpenAPI(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input ListOpenAPIInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListOpenAPIInput) (*mcp.CallToolResult, any, error) {
		specs, err := d.ListOpenAPI(input.SpecID)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to list openapi: %v", err)), nil, nil
		}

		if len(specs) == 0 {
			return textResult("No OpenAPI definitions found."), nil, nil
		}

		data, err := json.MarshalIndent(specs, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal: %v", err)), nil, nil
		}

		return textResult(string(data)), nil, nil
	}
}

// get_openapi

type GetOpenAPIInput struct {
	SpecID   string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 29.510)"`
	APIName  string `json:"api_name" jsonschema:"required,API name (e.g. Nnrf_NFManagement)"`
	Path     string `json:"path,omitempty" jsonschema:"Filter by API path (e.g. /nf-instances)"`
	Schema   string `json:"schema,omitempty" jsonschema:"Filter by schema name (e.g. NFProfile)"`
	Offset   int    `json:"offset,omitempty" jsonschema:"Start line number for pagination (0-based, default: 0)"`
	MaxLines int    `json:"max_lines,omitempty" jsonschema:"Maximum lines to return (default: 200, 0 = all)"`
}

var GetOpenAPITool = &mcp.Tool{
	Name:        "get_openapi",
	Description: "Get OpenAPI definition content for 5G service-based interface APIs (TS 29.xxx series). Use this tool to look up HTTP request/response details, API paths, parameters, request bodies, response schemas, and data type definitions. Use the path parameter to filter by API endpoint (e.g. /nf-instances) or the schema parameter to filter by data type (e.g. NFProfile). Use list_openapi first to discover available API names.",
}

func HandleGetOpenAPI(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input GetOpenAPIInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetOpenAPIInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}
		if input.APIName == "" {
			return errorResult("api_name is required"), nil, nil
		}

		content, err := d.GetOpenAPI(input.SpecID, input.APIName)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get openapi: %v", err)), nil, nil
		}

		// If path or schema filter is specified, parse YAML and extract
		if input.Path != "" || input.Schema != "" {
			filtered, err := filterOpenAPI(content, input.Path, input.Schema)
			if err != nil {
				return errorResult(fmt.Sprintf("failed to filter openapi: %v", err)), nil, nil
			}
			return textResult(filtered), nil, nil
		}

		// No filter: paginate raw content
		return paginateText(content, input.Offset, input.MaxLines, 0), nil, nil
	}
}

func filterOpenAPI(content, pathFilter, schemaFilter string) (string, error) {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		return "", fmt.Errorf("parse yaml: %w", err)
	}

	var sb strings.Builder

	if pathFilter != "" {
		paths, ok := doc["paths"].(map[string]any)
		if !ok {
			sb.WriteString("No paths found in this OpenAPI definition.\n")
		} else {
			filtered := make(map[string]any)
			for p, v := range paths {
				if p == pathFilter || strings.HasPrefix(p, pathFilter+"/") {
					filtered[p] = v
				}
			}

			if len(filtered) == 0 {
				var available []string
				for p := range paths {
					available = append(available, p)
				}
				sort.Strings(available)
				fmt.Fprintf(&sb, "Path %q not found. Available paths:\n", pathFilter)
				for _, p := range available {
					fmt.Fprintf(&sb, "  %s\n", p)
				}
			} else {
				out, err := yaml.Marshal(map[string]any{"paths": filtered})
				if err != nil {
					return "", fmt.Errorf("marshal yaml: %w", err)
				}
				sb.Write(out)
			}
		}
	}

	if schemaFilter != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n---\n\n")
		}

		components, _ := doc["components"].(map[string]any)
		var schemas map[string]any
		if components != nil {
			schemas, _ = components["schemas"].(map[string]any)
		}

		if schemas == nil {
			sb.WriteString("No schemas found in this OpenAPI definition.\n")
		} else {
			schema, ok := schemas[schemaFilter]
			if !ok {
				var available []string
				for name := range schemas {
					available = append(available, name)
				}
				sort.Strings(available)
				fmt.Fprintf(&sb, "Schema %q not found. Available schemas:\n", schemaFilter)
				for _, s := range available {
					fmt.Fprintf(&sb, "  %s\n", s)
				}
			} else {
				out, err := yaml.Marshal(map[string]any{
					"components": map[string]any{
						"schemas": map[string]any{
							schemaFilter: schema,
						},
					},
				})
				if err != nil {
					return "", fmt.Errorf("marshal yaml: %w", err)
				}
				sb.Write(out)
			}
		}
	}

	return sb.String(), nil
}
