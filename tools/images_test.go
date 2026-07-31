package tools

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/versionstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func seedImages(t *testing.T, d *db.DB) {
	t.Helper()
	images := []db.Image{
		{
			SpecID:      "TS 23.501",
			Version:     "18.6.0",
			Name:        "image1.png",
			MIMEType:    "image/png",
			Data:        []byte("\x89PNG\r\n\x1a\nfake-png-data"),
			LLMReadable: true,
		},
		{
			SpecID:      "TS 23.501",
			Version:     "18.6.0",
			Name:        "image2.emf",
			MIMEType:    "image/x-emf",
			Data:        []byte("fake-emf-data"),
			LLMReadable: false,
		},
		{
			SpecID:      "TS 29.510",
			Version:     "18.5.0",
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
	handler := HandleListImages(NewSource(d))

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

	t.Run("family spec id with multiple parts", func(t *testing.T) {
		if err := d.ExecScript(`INSERT INTO specs (id, version, version_token, title, release, series) VALUES
    ('TS 38.101-1', '18.6.0', 'i60', 'Part 1', '18', '38'),
    ('TS 38.101-2', '18.6.0', 'i60', 'Part 2', '18', '38');`); err != nil {
			t.Fatalf("failed to insert test data: %v", err)
		}

		result, _, err := handler(context.Background(), nil, ListImagesInput{SpecID: "TS 38.101"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "TS 38.101-1") || !strings.Contains(text, "TS 38.101-2") {
			t.Errorf("expected both parts listed, got: %s", text)
		}
	})
}

func TestHandleGetImage(t *testing.T) {
	d := setupTestDB(t)
	seedImages(t, d)
	handler := HandleGetImage(NewSource(d))

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

// sourceWithImageStore builds a Source whose version cache serves canned
// sections and images instead of downloading.
func sourceWithImageStore(t *testing.T, d *db.DB, imageFetcher versionstore.ImageFetcher) *Source {
	t.Helper()
	store, err := versionstore.Open(versionstore.Options{
		Path:         filepath.Join(t.TempDir(), "versions.db"),
		Fetcher:      cannedFetcher,
		ImageFetcher: imageFetcher,
	})
	if err != nil {
		t.Fatalf("versionstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	src := NewSource(d)
	src.Store = store
	src.Client = archiveClient(t)
	src.UseCache = false
	src.Budget = 5 * time.Second
	return src
}

// TestGetImageArchivedVersion covers the lazy image path end to end: the first
// request downloads and caches every image of the version, later requests hit
// the cache, and the original EMF name still finds the converted PNG.
func TestGetImageArchivedVersion(t *testing.T) {
	d := setupTestDB(t)
	seedImages(t, d)
	var calls atomic.Int32
	src := sourceWithImageStore(t, d, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		calls.Add(1)
		return []db.Image{{Name: "figure1.png", MIMEType: "image/png", Data: []byte("archived-png"), LLMReadable: true}}, nil
	})
	handler := HandleGetImage(src)

	for _, name := range []string{"figure1.png", "figure1.emf"} {
		result, _, err := handler(context.Background(), nil, GetImageInput{
			SpecID:  "TS 23.501",
			Name:    name,
			Version: "19.5.0",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("GetImage(%s): unexpected error result: %s", name, getTextContent(result))
		}
		ic, ok := result.Content[0].(*mcp.ImageContent)
		if !ok {
			t.Fatalf("expected ImageContent, got %T", result.Content[0])
		}
		if string(ic.Data) != "archived-png" {
			t.Errorf("unexpected image data: %q", string(ic.Data))
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("image fetcher called %d times, want 1", got)
	}

	// The database version must keep answering from the database.
	result, _, err := handler(context.Background(), nil, GetImageInput{SpecID: "TS 23.501", Name: "image1.png"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", getTextContent(result))
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("image fetcher ran for the database version (%d calls)", got)
	}
}

// TestListImagesArchivedVersion checks listing and the empty-result message on
// the archived path.
func TestListImagesArchivedVersion(t *testing.T) {
	d := setupTestDB(t)
	seedImages(t, d)
	src := sourceWithImageStore(t, d, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		return []db.Image{{Name: "figure1.png", MIMEType: "image/png", Data: []byte("x"), LLMReadable: true}}, nil
	})
	handler := HandleListImages(src)

	result, _, err := handler(context.Background(), nil, ListImagesInput{SpecID: "TS 23.501", Version: "19.5.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", getTextContent(result))
	}
	text := getTextContent(result)
	if !strings.Contains(text, "figure1.png") || !strings.Contains(text, `"count":1`) {
		t.Errorf("expected the archived image listed, got: %s", text)
	}
	if strings.Contains(text, "image1.png") {
		t.Errorf("archived listing leaked database images: %s", text)
	}
}

func TestListImagesArchivedVersionEmpty(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithImageStore(t, d, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		return nil, nil
	})
	handler := HandleListImages(src)

	result, _, err := handler(context.Background(), nil, ListImagesInput{SpecID: "TS 23.501", Version: "19.5.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", getTextContent(result))
	}
	if text := getTextContent(result); !strings.Contains(text, "No images found for TS 23.501 v19.5.0") {
		t.Errorf("expected a versioned no-images message, got: %s", text)
	}
}

// TestGetImageArchivedInProgress checks that an image fetch exceeding the
// budget returns a retry hint rather than a tool error.
func TestGetImageArchivedInProgress(t *testing.T) {
	d := setupTestDB(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	src := sourceWithImageStore(t, d, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		<-release
		return nil, nil
	})
	src.Budget = 20 * time.Millisecond
	handler := HandleGetImage(src)

	// Prime the section cache so only the image fetch is outstanding.
	if err := src.Store.Ensure(context.Background(), "TS 23.501", "19.5.0", &pipeline.SpecVersion{SpecID: "23.501", Version: "j50", Release: 19}, time.Minute); err != nil {
		t.Fatalf("prime section cache: %v", err)
	}

	result, _, err := handler(context.Background(), nil, GetImageInput{
		SpecID:  "TS 23.501",
		Name:    "figure1.png",
		Version: "19.5.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("an image fetch that is still running should not be reported as a tool error")
	}
	text := getTextContent(result)
	if !strings.Contains(text, "Images for TS 23.501 v19.5.0 are being downloaded") || !strings.Contains(text, "Call the same tool again") {
		t.Errorf("message = %q, want an image retry hint", text)
	}
}

// TestGetImageArchivedFetchError checks that a failed image download surfaces
// as a version-unavailable error result.
func TestGetImageArchivedFetchError(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithImageStore(t, d, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		return nil, errors.New("download blew up")
	})
	handler := HandleGetImage(src)

	result, _, err := handler(context.Background(), nil, GetImageInput{
		SpecID:  "TS 23.501",
		Name:    "figure1.png",
		Version: "19.5.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected an error result for a failed image fetch")
	}
	if text := getTextContent(result); !strings.Contains(text, "download blew up") {
		t.Errorf("expected the fetch failure reason, got: %s", text)
	}
}

// TestListImagesArchivedReResolveFails covers the cached fast path: the
// version's sections are already cached (so resolve carries no archive entry)
// and the archive listing has become unreachable, which must fail the image
// call with a version-unavailable error rather than a panic or a silent miss.
func TestListImagesArchivedReResolveFails(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithImageStore(t, d, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		t.Error("image fetcher must not run when the archive entry cannot be resolved")
		return nil, nil
	})
	if err := src.Store.Ensure(context.Background(), "TS 23.501", "19.5.0", &pipeline.SpecVersion{SpecID: "23.501", Version: "j50", Release: 19}, time.Minute); err != nil {
		t.Fatalf("prime section cache: %v", err)
	}
	src.Client = unreachableArchiveClient(t)
	handler := HandleListImages(src)

	result, _, err := handler(context.Background(), nil, ListImagesInput{SpecID: "TS 23.501", Version: "19.5.0"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected an error result when the archive listing is unreachable")
	}
	if text := getTextContent(result); !strings.Contains(text, "not available") {
		t.Errorf("expected a version-unavailable message, got: %s", text)
	}
}

// unreachableArchiveClient serves 404 for every archive request.
func unreachableArchiveClient(t *testing.T) *http.Client {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	t.Cleanup(ts.Close)
	return &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}
}

// TestGetImageArchivedNonReadable checks the error for an archived EMF/WMF
// image that could not be converted.
func TestGetImageArchivedNonReadable(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithImageStore(t, d, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		return []db.Image{{Name: "figure1.emf", MIMEType: "image/x-emf", Data: []byte("emf")}}, nil
	})
	handler := HandleGetImage(src)

	result, _, err := handler(context.Background(), nil, GetImageInput{
		SpecID:  "TS 23.501",
		Name:    "figure1.emf",
		Version: "19.5.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected an error result for an unconverted EMF image")
	}
	if text := getTextContent(result); !strings.Contains(text, "soffice") {
		t.Errorf("expected a LibreOffice hint, got: %s", text)
	}
}
