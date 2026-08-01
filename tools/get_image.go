package tools

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetImageInput struct {
	SpecID  string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 23.501)"`
	Name    string `json:"name" jsonschema:"required,Image filename (e.g. image1.png)"`
	Version string `json:"version,omitempty" jsonschema:"Specification version to read (e.g. 18.6.0). Also accepts an archive token (i60) or a release selector (Rel-18). Defaults to the version in the database. Use list_versions to see what exists."`
}

var GetImageTool = &mcp.Tool{
	Name:        "get_image",
	Description: "Get an embedded image from a 3GPP specification. Returns the image as base64-encoded data that can be directly viewed by the LLM. Use list_images to discover available images for a spec. Pass `version` to read a past version's image; the images of an archived version are downloaded on first use, which takes up to a few minutes.",
}

func HandleGetImage(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input GetImageInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetImageInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}
		if input.Name == "" {
			return errorResult("name is required"), nil, nil
		}

		img, res, err := src.GetImage(ctx, input.SpecID, input.Version, input.Name)
		if err != nil {
			return versionErrorResult(err, "image not found"), nil, nil
		}
		if img == nil {
			return errorResult(fmt.Sprintf("image %q not found in %s%s", input.Name, input.SpecID, versionSuffix(res))), nil, nil
		}

		if !img.LLMReadable {
			hint := "Re-run the pipeline with --convert-image flag to convert EMF/WMF images to PNG."
			if res.Archived {
				hint = "Converting it needs LibreOffice (soffice), which was missing or failed to convert this image when this version's images were fetched."
			}
			return errorResult(fmt.Sprintf(
				"Image %q is in %s format which cannot be displayed. %s",
				img.Name, img.MIMEType, hint,
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
