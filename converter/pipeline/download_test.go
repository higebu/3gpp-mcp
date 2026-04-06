package pipeline

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestDownloadSpecs_Race exercises DownloadSpecs with multiple parallel workers
// to detect race conditions in the goroutine fan-out and stats aggregation.
func TestDownloadSpecs_Race(t *testing.T) {
	// A minimal ZIP containing a file named "test.docx".
	// DownloadSpecs only downloads and extracts; it does not parse docx content.
	zipData := makeZipWithFile(t, "test.docx", []byte("fake docx content"))

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipData)
	}))
	defer ts.Close()

	specs := make([]*SpecVersion, 8)
	for i := range specs {
		specs[i] = &SpecVersion{
			Series:        "23",
			SpecID:        "23.001",
			Filename:      "23001-a10.zip",
			VersionLetter: "a",
			VersionMinor:  10,
			Release:       10,
			URL:           ts.URL + "/23001-a10.zip",
		}
	}

	outputDir := t.TempDir()

	stats := DownloadSpecs(context.Background(), ts.Client(), specs, outputDir, 4, false, 10*time.Second)

	total := 0
	for _, count := range stats {
		total += count
	}
	if total != len(specs) {
		t.Errorf("expected %d total results, got %d (stats: %v)", len(specs), total, stats)
	}
	if stats["OK"] == 0 {
		t.Errorf("expected at least one OK result, got stats: %v", stats)
	}
}
