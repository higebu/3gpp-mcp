package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetTOCInput struct {
	SpecID  string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 23.501)"`
	Version string `json:"version,omitempty" jsonschema:"Specification version to read (e.g. 18.6.0). Also accepts an archive token (i60) or a release selector (Rel-18). Defaults to the version in the database. Use list_versions to see what exists."`
}

var GetTOCTool = &mcp.Tool{
	Name:        "get_toc",
	Description: "Get the table of contents (section structure) of a 3GPP specification. Pass `version` to see the structure of a past version, which is downloaded and converted on first use; section numbers often move between releases, so check the table of contents before reading a section of an older version.",
}

func HandleGetTOC(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input GetTOCInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetTOCInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}

		sections, res, err := src.GetTOC(ctx, input.SpecID, input.Version)
		if err != nil {
			return versionErrorResult(err, "failed to get TOC"), nil, nil
		}

		if len(sections) == 0 {
			if parts, partsErr := src.DB.FindSpecIDsByFamily(input.SpecID); partsErr == nil && len(parts) > 0 {
				return errorResult(fmt.Sprintf("%s has multiple parts: %s — specify one", input.SpecID, strings.Join(parts, ", "))), nil, nil
			}
			return errorResult(fmt.Sprintf("no sections found for %s%s", input.SpecID, versionSuffix(res))), nil, nil
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "# %s - Table of Contents\n\n", specLabel(sections[0]))
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
