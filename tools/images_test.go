package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func seedImages(t *testing.T, d *db.DB) {
	t.Helper()
	images := []db.Image{
		{
			SpecID:      "TS 23.501",
			Name:        "image1.png",
			MIMEType:    "image/png",
			Data:        []byte("\x89PNG\r\n\x1a\nfake-png-data"),
			LLMReadable: true,
		},
		{
			SpecID:      "TS 23.501",
			Name:        "image2.emf",
			MIMEType:    "image/x-emf",
			Data:        []byte("fake-emf-data"),
			LLMReadable: false,
		},
		{
			SpecID:      "TS 29.510",
			Name:        "diagram.png",
			MIMEType:    "image/png",
			Data:        []byte("\x89PNG\r\n\x1a\nother-spec-data"),
			LLMReadable: true,
		},
	}
	for _, img := range images {
		if err := d.UpsertImage(img); err != nil {
			t.Fatalf("UpsertImage(%s): %v", img.Name, err)
		}
	}
}

func TestHandleListImages(t *testing.T) {
	d := setupTestDB(t)
	seedImages(t, d)
	handler := HandleListImages(d)

	t.Run("valid spec returns images", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListImagesInput{SpecID: "TS 23.501"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		if !strings.Contains(text, "image1.png") {
			t.Errorf("expected image1.png in output, got: %s", text)
		}
		if !strings.Contains(text, "image2.emf") {
			t.Errorf("expected image2.emf in output, got: %s", text)
		}
		if !strings.Contains(text, `"count":2`) {
			t.Errorf("expected count of 2, got: %s", text)
		}
	})

	t.Run("filters by spec_id", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListImagesInput{SpecID: "TS 29.510"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "diagram.png") {
			t.Errorf("expected diagram.png, got: %s", text)
		}
		if strings.Contains(text, "image1.png") {
			t.Errorf("should not contain images from other specs, got: %s", text)
		}
	})

	t.Run("empty spec_id", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListImagesInput{SpecID: ""})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for empty spec_id")
		}
	})

	t.Run("spec without images", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListImagesInput{SpecID: "TS 24.229"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "No images found") {
			t.Errorf("expected 'No images found' message, got: %s", text)
		}
	})
}

func TestHandleGetImage(t *testing.T) {
	d := setupTestDB(t)
	seedImages(t, d)
	handler := HandleGetImage(d)

	t.Run("returns image content", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetImageInput{
			SpecID: "TS 23.501",
			Name:   "image1.png",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		if len(result.Content) != 1 {
			t.Fatalf("expected one content item, got %d", len(result.Content))
		}
		ic, ok := result.Content[0].(*mcp.ImageContent)
		if !ok {
			t.Fatalf("expected ImageContent, got %T", result.Content[0])
		}
		if ic.MIMEType != "image/png" {
			t.Errorf("MIMEType = %q, want %q", ic.MIMEType, "image/png")
		}
		if !strings.Contains(string(ic.Data), "fake-png-data") {
			t.Errorf("unexpected image data: %q", string(ic.Data))
		}
	})

	t.Run("non-readable format returns error", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetImageInput{
			SpecID: "TS 23.501",
			Name:   "image2.emf",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for EMF image")
		}
		text := getTextContent(result)
		if !strings.Contains(text, "convert-image") {
			t.Errorf("expected hint about --convert-image, got: %s", text)
		}
	})

	t.Run("missing image", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetImageInput{
			SpecID: "TS 23.501",
			Name:   "nonexistent.png",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for missing image")
		}
	})

	t.Run("empty spec_id", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetImageInput{Name: "image1.png"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for empty spec_id")
		}
	})

	t.Run("empty name", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetImageInput{SpecID: "TS 23.501"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for empty name")
		}
	})
}
