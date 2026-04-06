package tools

import (
	"context"
	"fmt"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetImageInput struct {
	SpecID string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 23.501)"`
	Name   string `json:"name" jsonschema:"required,Image filename (e.g. image1.png)"`
}

var GetImageTool = &mcp.Tool{
	Name:        "get_image",
	Description: "Get an embedded image from a 3GPP specification. Returns the image as base64-encoded data that can be directly viewed by the LLM. Use list_images to discover available images for a spec.",
}

func HandleGetImage(d *db.DB) func(ctx context.Context, req *mcp.CallToolRequest, input GetImageInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetImageInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}
		if input.Name == "" {
			return errorResult("name is required"), nil, nil
		}

		img, err := d.GetImage(input.SpecID, input.Name)
		if err != nil {
			return errorResult(fmt.Sprintf("image not found: %v", err)), nil, nil
		}

		if !img.LLMReadable {
			return errorResult(fmt.Sprintf(
				"Image %q is in %s format which cannot be displayed. "+
					"Re-run the pipeline with --convert-image flag to convert EMF/WMF images to PNG.",
				img.Name, img.MIMEType,
			)), nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{
				&mcp.ImageContent{
					Data:     img.Data,
					MIMEType: img.MIMEType,
				},
			},
		}, nil, nil
	}
}
