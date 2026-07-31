package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListImagesInput struct {
	SpecID  string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 23.501)"`
	Version string `json:"version,omitempty" jsonschema:"Specification version to read (e.g. 18.6.0). Also accepts an archive token (i60) or a release selector (Rel-18). Defaults to the version in the database. Use list_versions to see what exists."`
}

var ListImagesTool = &mcp.Tool{
	Name:        "list_images",
	Description: "List embedded images in a 3GPP specification. Returns image names, MIME types, and whether they are viewable by LLMs. Use get_image to retrieve a specific image. Pass `version` to list a past version's images; the images of an archived version are downloaded on first use, which takes up to a few minutes.",
}

func HandleListImages(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input ListImagesInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListImagesInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}

		images, res, err := src.ListImages(ctx, input.SpecID, input.Version)
		if err != nil {
			return versionErrorResult(err, "failed to list images"), nil, nil
		}

		if len(images) == 0 {
			if !res.archived {
				if parts, partsErr := src.DB.FindSpecIDsByFamily(input.SpecID); partsErr == nil && len(parts) > 0 {
					return errorResult(fmt.Sprintf("%s has multiple parts: %s — specify one", input.SpecID, strings.Join(parts, ", "))), nil, nil
				}
			}
			return textResult(fmt.Sprintf("No images found for %s%s", input.SpecID, versionSuffix(res))), nil, nil
		}

		data, err := json.Marshal(struct {
			Images []db.ImageInfo `json:"images"`
			Count  int            `json:"count"`
		}{
			Images: images,
			Count:  len(images),
		})
		if err != nil {
			return errorResult(fmt.Sprintf("marshal error: %v", err)), nil, nil
		}

		return textResult(string(data)), nil, nil
	}
}
