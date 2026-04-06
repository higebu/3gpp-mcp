package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListImagesInput struct {
	SpecID string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 23.501)"`
}

var ListImagesTool = &mcp.Tool{
	Name:        "list_images",
	Description: "List embedded images in a 3GPP specification. Returns image names, MIME types, and whether they are viewable by LLMs. Use get_image to retrieve a specific image.",
}

func HandleListImages(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input ListImagesInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListImagesInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}

		images, err := d.ListImages(input.SpecID)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to list images: %v", err)), nil, nil
		}

		if len(images) == 0 {
			return textResult(fmt.Sprintf("No images found for %s", input.SpecID)), nil, nil
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
