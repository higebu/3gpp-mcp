package pipeline

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// makeRawZipEntry constructs a zip archive bytes containing a single entry
// whose name may include path traversal or absolute path characters. Used to
// exercise the extractFile path-traversal guard.
func makeRawZipEntry(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.CreateHeader(&zip.FileHeader{Name: name})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestExtractFile_PathTraversal verifies that extractFile rejects zip entries
// whose names contain "..", preventing path-traversal writes outside outputDir.
func TestExtractFile_PathTraversal(t *testing.T) {
	zipBytes := makeRawZipEntry(t, "../evil.docx", []byte("pwned"))
	r, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "out.docx")
	err = extractFile(r.File[0], target)
	if err == nil {
		t.Fatal("expected path traversal error")
	}
	if !strings.Contains(err.Error(), "suspicious") {
		t.Errorf("error = %v, want 'suspicious'", err)
	}
}

// TestExtractFile_Normal covers the happy path of extractFile.
func TestExtractFile_Normal(t *testing.T) {
	zipBytes := makeRawZipEntry(t, "ok.docx", []byte("contents"))
	r, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "ok.docx")
	if err := extractFile(r.File[0], target); err != nil {
		t.Fatalf("extractFile: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "contents" {
		t.Errorf("file content = %q, want 'contents'", data)
	}
}

// TestDownloadAndExtract_DocOnly verifies the DOC_ONLY status path: the ZIP
// contains a .doc file but no .docx file.
func TestDownloadAndExtract_DocOnly(t *testing.T) {
	zipBytes := makeZipWithFile(t, "spec.doc", []byte("legacy doc content"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipBytes)
	}))
	defer ts.Close()

	outDir := t.TempDir()
	spec := &SpecVersion{SpecID: "TS 99.001", URL: ts.URL + "/x.zip"}
	result, err := DownloadAndExtract(context.Background(), ts.Client(), spec, outDir, 5*time.Second)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if result.Status != "DOC_ONLY" {
		t.Errorf("status = %q, want DOC_ONLY", result.Status)
	}
	if _, err := os.Stat(filepath.Join(outDir, "_doc_files", "spec.doc")); err != nil {
		t.Errorf("expected spec.doc in _doc_files: %v", err)
	}
}

// TestDownloadAndExtract_NoDoc verifies the NO_DOC status when the ZIP holds
// only irrelevant files.
func TestDownloadAndExtract_NoDoc(t *testing.T) {
	zipBytes := makeZipWithFile(t, "readme.txt", []byte("just metadata"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipBytes)
	}))
	defer ts.Close()

	spec := &SpecVersion{SpecID: "TS 99.002", URL: ts.URL + "/y.zip"}
	result, err := DownloadAndExtract(context.Background(), ts.Client(), spec, t.TempDir(), 5*time.Second)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if result.Status != "NO_DOC" {
		t.Errorf("status = %q, want NO_DOC", result.Status)
	}
}

// TestDownloadZip_TooLargeContentLength verifies the early rejection path when
// the server advertises a Content-Length that exceeds maxZipSize.
func TestDownloadZip_TooLargeContentLength(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(maxZipSize+1, 10))
		w.Header().Set("Content-Type", "application/zip")
		// Body intentionally empty; we expect early rejection via header.
	}))
	defer ts.Close()

	_, err := downloadZip(context.Background(), ts.Client(), ts.URL)
	if err == nil {
		t.Fatal("expected error for oversized content-length")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error = %v, want 'too large'", err)
	}
}

// TestDownloadZip_HTTPError verifies the non-200 response path returns a clean
// error without retrying at this layer.
func TestDownloadZip_HTTPError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()

	_, err := downloadZip(context.Background(), ts.Client(), ts.URL)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("error = %v, want 'HTTP 500'", err)
	}
}

// TestConvertDocFiles_NoDocFiles verifies ConvertDocFiles returns (0, nil)
// when the directory contains no .doc files (happy but trivial path).
func TestConvertDocFiles_NoDocFiles(t *testing.T) {
	dir := t.TempDir()
	// Add a non-doc file to prove it's ignored.
	if err := os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	n, err := ConvertDocFiles(context.Background(), dir, dir)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != 0 {
		t.Errorf("converted = %d, want 0", n)
	}
}

// TestConvertDocFiles_MissingDir verifies the error path when the input
// directory does not exist.
func TestConvertDocFiles_MissingDir(t *testing.T) {
	_, err := ConvertDocFiles(context.Background(), filepath.Join(t.TempDir(), "nope"), t.TempDir())
	if err == nil {
		t.Fatal("expected error for missing directory")
	}
}
