package pipeline

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/testutil"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.InitSchema(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func makeZipWithFile(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	return makeZipWithFiles(t, map[string][]byte{name: content})
}

func makeZipWithFiles(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func downloadTestZip(t *testing.T, url string) []byte {
	t.Helper()
	return testutil.DownloadTestZip(t, url)
}

func testdataDocxPath(t *testing.T) string {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "docx", "testdata", "23274-i20.docx")
}

// TestConvertDir_Race exercises ConvertDir with multiple workers to detect
// race conditions in the concurrent parse-and-collect pipeline.
func TestConvertDir_Race(t *testing.T) {
	docxData, err := os.ReadFile(testdataDocxPath(t))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	// Create multiple copies to force concurrent parsing.
	for i := range 5 {
		name := filepath.Join(dir, "spec"+string(rune('A'+i))+".docx")
		if err := os.WriteFile(name, docxData, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	d := setupTestDB(t)

	if err := ConvertDir(context.Background(), d, dir, 4, false); err != nil {
		t.Fatalf("ConvertDir: %v", err)
	}

	// Verify at least one spec was inserted.
	result, err := d.ListSpecs("", -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Specs) == 0 {
		t.Error("expected at least one spec in DB after ConvertDir")
	}
}

// TestPipelineRun_MultiFileZip exercises the multi-file split pattern using a
// real TS 36.133 ZIP downloaded from the 3GPP archive. TS 36.133 ships as
// multiple DOCX files (_cover + several _sXX content files) in a single ZIP.
func TestPipelineRun_MultiFileZip(t *testing.T) {
	zipData := downloadTestZip(t, "https://www.3gpp.org/ftp/Specs/archive/36_series/36.133/36133-j40.zip")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipData)
	}))
	defer ts.Close()

	d := setupTestDB(t)

	specs := []*SpecVersion{{
		Series:        "36",
		SpecID:        "36.133",
		Filename:      "36133-j40.zip",
		VersionLetter: "j",
		VersionMinor:  40,
		Release:       19,
		URL:           ts.URL + "/36133-j40.zip",
	}}

	p := &Pipeline{DB: d, Client: ts.Client(), Workers: 1, Timeout: 60 * time.Second}
	if err := p.Run(context.Background(), specs); err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}

	sections, err := d.GetTOC("TS 36.133")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) < 5000 {
		t.Errorf("expected at least 5000 sections, got %d", len(sections))
	}
	t.Logf("Parsed %d sections from TS 36.133 (multi-file zip)", len(sections))
}

// TestPipelineRun_OpenAPIYAML exercises the OpenAPI YAML import pattern using
// a real TS 29.510 ZIP downloaded from the 3GPP archive. TS 29.510 ships with
// multiple OpenAPI YAML files alongside the DOCX.
func TestPipelineRun_OpenAPIYAML(t *testing.T) {
	zipData := downloadTestZip(t, "https://www.3gpp.org/ftp/Specs/archive/29_series/29.510/29510-j60.zip")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipData)
	}))
	defer ts.Close()

	d := setupTestDB(t)

	specs := []*SpecVersion{{
		Series:        "29",
		SpecID:        "29.510",
		Filename:      "29510-j60.zip",
		VersionLetter: "j",
		VersionMinor:  60,
		Release:       17,
		URL:           ts.URL + "/29510-j60.zip",
	}}

	p := &Pipeline{DB: d, Client: ts.Client(), Workers: 1, Timeout: 30 * time.Second}
	if err := p.Run(context.Background(), specs); err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}

	// Verify OpenAPI specs were imported.
	apis, err := d.ListOpenAPI("TS 29.510")
	if err != nil {
		t.Fatal(err)
	}
	if len(apis) == 0 {
		t.Fatal("expected OpenAPI specs to be imported from TS 29.510 YAML files")
	}
	t.Logf("Imported %d OpenAPI specs from TS 29.510", len(apis))

	// Verify sections were also parsed.
	sections, err := d.GetTOC("TS 29.510")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) == 0 {
		t.Error("expected sections for TS 29.510")
	}
}

// TestPipelineRun_DocConversion exercises the .doc→.docx LibreOffice conversion
// path using a real TS 24.229 ZIP downloaded from the 3GPP archive.
// TS 24.229 ships only as a .doc file, so ConvertDoc must be true.
// Skipped when -short is set or LibreOffice is not installed.
func TestPipelineRun_DocConversion(t *testing.T) {
	if _, err := exec.LookPath("libreoffice"); err != nil {
		t.Skip("skipping: libreoffice not found")
	}
	zipData := downloadTestZip(t, "https://www.3gpp.org/ftp/Specs/archive/24_series/24.229/24229-j60.zip")

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipData)
	}))
	defer ts.Close()

	d := setupTestDB(t)

	specs := []*SpecVersion{{
		Series:        "24",
		SpecID:        "24.229",
		Filename:      "24229-j60.zip",
		VersionLetter: "j",
		VersionMinor:  60,
		Release:       18,
		URL:           ts.URL + "/24229-j60.zip",
	}}

	p := &Pipeline{
		DB:         d,
		Client:     ts.Client(),
		Workers:    1,
		ConvertDoc: true,
		Timeout:    5 * time.Minute,
	}
	if err := p.Run(context.Background(), specs); err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}

	sections, err := d.GetTOC("TS 24.229")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) < 1000 {
		t.Errorf("expected at least 1000 sections, got %d", len(sections))
	}
	t.Logf("Parsed %d sections from TS 24.229 (doc conversion)", len(sections))
}

// TestPipelineRun_Race exercises Pipeline.Run with multiple workers and an
// httptest server serving valid ZIP archives containing a real docx file.
func TestPipelineRun_Race(t *testing.T) {
	docxData, err := os.ReadFile(testdataDocxPath(t))
	if err != nil {
		t.Fatal(err)
	}

	zipData := makeZipWithFile(t, "23274-i20.docx", docxData)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipData)
	}))
	defer ts.Close()

	d := setupTestDB(t)

	specs := make([]*SpecVersion, 6)
	for i := range specs {
		specs[i] = &SpecVersion{
			Series:        "23",
			SpecID:        "23.274",
			Filename:      "23274-i20.zip",
			VersionLetter: "i",
			VersionMinor:  20,
			Release:       18,
			URL:           ts.URL + "/23274-i20.zip",
		}
	}

	p := &Pipeline{
		DB:      d,
		Client:  ts.Client(),
		Workers: 3,
		Timeout: 10 * time.Second,
	}

	if err := p.Run(context.Background(), specs); err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}

	// Verify spec was inserted.
	sections, err := d.GetTOC("TS 23.274")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) == 0 {
		t.Error("expected sections for TS 23.274 after pipeline run")
	}
}

func TestSortCoverLast(t *testing.T) {
	files := []string{
		"/tmp/spec_cover.docx",
		"/tmp/spec_s01.docx",
		"/tmp/spec_s02.docx",
		"/tmp/another_cover.docx",
		"/tmp/aaa.docx",
	}

	sortCoverLast(files)

	// Non-cover files should come first, sorted alphabetically
	if filepath.Base(files[0]) != "aaa.docx" {
		t.Errorf("files[0] = %q, want aaa.docx", filepath.Base(files[0]))
	}
	if filepath.Base(files[1]) != "spec_s01.docx" {
		t.Errorf("files[1] = %q, want spec_s01.docx", filepath.Base(files[1]))
	}
	if filepath.Base(files[2]) != "spec_s02.docx" {
		t.Errorf("files[2] = %q, want spec_s02.docx", filepath.Base(files[2]))
	}

	// Cover files should come last, sorted alphabetically
	if filepath.Base(files[3]) != "another_cover.docx" {
		t.Errorf("files[3] = %q, want another_cover.docx", filepath.Base(files[3]))
	}
	if filepath.Base(files[4]) != "spec_cover.docx" {
		t.Errorf("files[4] = %q, want spec_cover.docx", filepath.Base(files[4]))
	}

	// Edge case: empty slice
	sortCoverLast(nil)
	sortCoverLast([]string{})
}
