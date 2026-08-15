package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/db"
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
		specs, err := d.ListOpenAPI(ctx, input.SpecID)
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
	MaxLines int    `json:"max_lines,omitempty" jsonschema:"Maximum lines to return (default: 200)"`
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

		content, err := d.GetOpenAPIResolved(ctx, input.SpecID, input.APIName)
		if errors.Is(err, db.ErrOpenAPINotFound) {
			return errorResult(OpenAPINotFoundMessage(ctx, d, input.SpecID, input.APIName, input.Schema)), nil, nil
		}
		if err != nil {
			return errorResult(fmt.Sprintf("failed to get openapi: %v", err)), nil, nil
		}

		// If path or schema filter is specified, parse YAML and extract
		if input.Path != "" || input.Schema != "" {
			filtered, err := FilterOpenAPI(content, input.Path, input.Schema, func(schema string) string {
				return OpenAPISchemaHint(ctx, d, schema)
			})
			if err != nil {
				return errorResult(fmt.Sprintf("failed to filter openapi: %v", err)), nil, nil
			}
			content = filtered
		}

		return paginateText(content, input.Offset, input.MaxLines, 0), nil, nil
	}
}

// OpenAPINotFoundMessage explains a (spec_id, api_name) miss with what to do
// next instead of a bare SQL error: which specification actually provides the
// requested api_name when the guess named the wrong document, which document
// defines the requested schema (answered from the openapi_chunks index, in the
// crossSpecHint manner of get_asn1), and which api_name values the spec does
// carry. A hint that cannot be answered — no index, a query error — is
// omitted rather than turning the not-found into a failure. Shared with the
// CLI's get-openapi command.
func OpenAPINotFoundMessage(ctx context.Context, d *db.DB, specID, apiName, schema string) string {
	msg := fmt.Sprintf("OpenAPI definition %q not found in %s.", apiName, specID)

	// The wrong-document guess: the api_name exists, under another spec.
	if specs, err := d.FindOpenAPIByAPIName(ctx, apiName); err == nil && len(specs) > 0 {
		msg += fmt.Sprintf(" %s is provided by %s — call get_openapi with that spec_id and api_name.",
			apiName, joinOpenAPISources(specs))
	}

	// A schema name pins the defining document more precisely than the
	// api_name guess does, so answer it directly from the index.
	if schema != "" {
		msg += OpenAPISchemaHint(ctx, d, schema)
	}

	if avail, err := d.ListOpenAPI(ctx, specID); err == nil {
		if len(avail) > 0 {
			names := make([]string, len(avail))
			for i, s := range avail {
				names[i] = s.APIName
			}
			msg += fmt.Sprintf(" Available api_name values in %s: %s.", specID, strings.Join(names, ", "))
		} else {
			msg += fmt.Sprintf(" %s has no OpenAPI definitions in the database — use list_openapi to see which specifications have them.", specID)
		}
	}
	return msg
}

// OpenAPISchemaHint reports which documents define the named schema, answered
// from the openapi_chunks index, as a sentence that can be appended to a
// not-found message. On a database without the index, or a schema the index
// does not know, it is "" rather than an error, in the crossSpecHint manner
// of get_asn1. Shared with the CLI's get-openapi command.
func OpenAPISchemaHint(ctx context.Context, d *db.DB, schema string) string {
	sources, err := d.OpenAPISchemaSources(ctx, schema)
	if err != nil || len(sources) == 0 {
		return ""
	}
	return fmt.Sprintf(" Schema %q is defined in %s — call get_openapi there, or use search_openapi to find definitions by name.",
		schema, joinOpenAPISources(sources))
}

// joinOpenAPISources renders OpenAPI documents as "spec_id / api_name" pairs,
// the two arguments a follow-up get_openapi call needs.
func joinOpenAPISources(specs []db.OpenAPISpec) string {
	parts := make([]string, len(specs))
	for i, s := range specs {
		parts[i] = s.SpecID + " / " + s.APIName
	}
	return strings.Join(parts, ", ")
}

// FilterOpenAPI narrows an OpenAPI YAML document to a path and/or schema
// subset. schemaHint, when non-nil, is consulted on a schema the document
// does not define, so the miss can say where the schema actually lives (the
// 5G SBI documents keep their common types in TS 29.571) instead of only
// listing this document's schemas. It is shared with the CLI's get-openapi
// command.
func FilterOpenAPI(content, pathFilter, schemaFilter string, schemaHint func(string) string) (string, error) {
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
				fmt.Fprintf(&sb, "Schema %q not found in this document.", schemaFilter)
				if schemaHint != nil {
					sb.WriteString(schemaHint(schemaFilter))
				}
				sb.WriteString(" Available schemas:\n")
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
