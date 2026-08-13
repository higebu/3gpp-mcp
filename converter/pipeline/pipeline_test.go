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
	"strings"
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

// TestImportYAML exercises the _yaml import directly: matching files are
// upserted with the version read from the document, and non-matching names
// are skipped.
func TestImportYAML(t *testing.T) {
	d := setupTestDB(t)
	tmpDir := t.TempDir()
	yamlDir := filepath.Join(tmpDir, "_yaml")
	if err := os.MkdirAll(yamlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	yaml := "openapi: 3.0.0\ninfo:\n  version: '1.2.3'\n  title: Nnrf_NFManagement\n"
	if err := os.WriteFile(filepath.Join(yamlDir, "TS29510_Nnrf_NFManagement.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(yamlDir, "readme.txt"), []byte("not yaml"), 0o600); err != nil {
		t.Fatal(err)
	}

	p := &Pipeline{DB: d}
	p.importYAML(tmpDir)

	apis, err := d.ListOpenAPI(t.Context(), "TS 29.510")
	if err != nil {
		t.Fatal(err)
	}
	if len(apis) != 1 {
		t.Fatalf("imported %d OpenAPI specs, want 1", len(apis))
	}
	if apis[0].APIName != "Nnrf_NFManagement" || apis[0].Version != "1.2.3" {
		t.Errorf("imported %q version %q, want Nnrf_NFManagement version 1.2.3", apis[0].APIName, apis[0].Version)
	}

	// A spec directory without _yaml is a no-op.
	p.importYAML(t.TempDir())
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

	if err := ConvertDir(context.Background(), d, dir, 4, false, false); err != nil {
		t.Fatalf("ConvertDir: %v", err)
	}

	// Verify at least one spec was inserted.
	result, err := d.ListSpecs(t.Context(), "", "", -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Specs) == 0 {
		t.Error("expected at least one spec in DB after ConvertDir")
	}
}

// makeMinimalDocx builds an in-memory .docx containing only word/document.xml
// with the given body XML. ParseDocx needs nothing else.
func makeMinimalDocx(t *testing.T, bodyXML string) []byte {
	t.Helper()
	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:body>` + bodyXML + `</w:body>
</w:document>`
	return makeZipWithFile(t, "word/document.xml", []byte(doc))
}

func TestDocTypeID(t *testing.T) {
	cases := []struct {
		id, docType, want string
	}{
		{"TS 21.905", "TR", "TR 21.905"},
		{"TR 21.905", "TS", "TS 21.905"},
		{"TS 23.501", "TS", "TS 23.501"},
		{"TS 23.501", "", "TS 23.501"},
		{"weirdname", "TR", "weirdname"},
	}
	for _, tt := range cases {
		if got := docTypeID(tt.id, tt.docType); got != tt.want {
			t.Errorf("docTypeID(%q, %q) = %q, want %q", tt.id, tt.docType, got, tt.want)
		}
	}
}

// TestApplyDocType covers the record rewrite: the spec row, every section and
// every image must move to the new label together, and a matching or unknown
// type leaves everything alone.
func TestApplyDocType(t *testing.T) {
	spec := db.Spec{ID: "TS 21.905"}
	sections := []db.Section{{SpecID: "TS 21.905", Number: "1"}}
	images := []db.Image{{SpecID: "TS 21.905", Name: "img.png"}}

	applyDocType(&spec, sections, images, "TR")
	if spec.ID != "TR 21.905" {
		t.Errorf("spec.ID = %q, want %q", spec.ID, "TR 21.905")
	}
	if sections[0].SpecID != "TR 21.905" {
		t.Errorf("section SpecID = %q, want %q", sections[0].SpecID, "TR 21.905")
	}
	if images[0].SpecID != "TR 21.905" {
		t.Errorf("image SpecID = %q, want %q", images[0].SpecID, "TR 21.905")
	}

	// A type that already matches is a no-op.
	applyDocType(&spec, sections, images, "TR")
	if spec.ID != "TR 21.905" || sections[0].SpecID != "TR 21.905" || images[0].SpecID != "TR 21.905" {
		t.Errorf("matching type must not change records: %q %q %q", spec.ID, sections[0].SpecID, images[0].SpecID)
	}

	// An unknown type keeps whatever was parsed.
	applyDocType(&spec, sections, images, "")
	if spec.ID != "TR 21.905" {
		t.Errorf("empty type must not change records, got %q", spec.ID)
	}
}

// TestPipelineRun_DBInsertError verifies that a failing database write makes
// the spec fail instead of reporting OK. The database has no schema, so the
// insert after a successful download and parse cannot succeed.
func TestPipelineRun_DBInsertError(t *testing.T) {
	docxData := makeMinimalDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="Heading 1"/></w:pPr><w:r><w:t>5 Definitions</w:t></w:r></w:p>`)
	zipData := makeZipWithFile(t, "21905-h20.docx", docxData)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipData)
	}))
	defer ts.Close()

	// A database with no schema: every insert fails with "no such table".
	d, err := db.OpenReadWrite(filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	specs := []*SpecVersion{{
		Series:   "21",
		SpecID:   "21.905",
		Filename: "21905-h20.zip",
		Version:  "h20",
		Release:  17,
		URL:      ts.URL + "/21905-h20.zip",
	}}
	p := &Pipeline{DB: d, Client: ts.Client(), Workers: 1, Timeout: 10 * time.Second}
	if err := p.Run(context.Background(), specs); err == nil {
		t.Error("expected an error when the database write fails")
	}
}

// TestPipelineRun_TRDocType verifies that a Technical Report is stored under a
// "TR " spec ID, and that the part files of a split spec — which carry no
// cover page and cannot tell a TS from a TR — follow the cover file's verdict
// instead of splitting the spec across "TS x" and "TR x" rows (#110).
func TestPipelineRun_TRDocType(t *testing.T) {
	partDocx := makeMinimalDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="Heading 1"/></w:pPr><w:r><w:t>5 Definitions</w:t></w:r></w:p>`+
			`<w:p><w:r><w:t>Vocabulary body text.</w:t></w:r></w:p>`)
	coverDocx := makeMinimalDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="ZA"/></w:pPr><w:r><w:t>3GPP TR 21.905 V17.2.0 (2022-03)</w:t></w:r></w:p>`)
	zipData := makeZipWithFiles(t, map[string][]byte{
		"21905-h20_s01.docx":   partDocx,
		"21905-h20_cover.docx": coverDocx,
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(zipData)
	}))
	defer ts.Close()

	d := setupTestDB(t)

	specs := []*SpecVersion{{
		Series:        "21",
		SpecID:        "21.905",
		Filename:      "21905-h20.zip",
		Version:       "h20",
		VersionLetter: "h",
		VersionMinor:  20,
		Release:       17,
		URL:           ts.URL + "/21905-h20.zip",
	}}

	p := &Pipeline{DB: d, Client: ts.Client(), Workers: 1, Timeout: 10 * time.Second}
	if err := p.Run(context.Background(), specs); err != nil {
		t.Fatalf("Pipeline.Run: %v", err)
	}

	result, err := d.ListSpecs(t.Context(), "", "21.905", -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Specs) != 1 {
		t.Fatalf("got %d specs for 21.905, want exactly 1 (no TS/TR split): %+v", len(result.Specs), result.Specs)
	}
	if result.Specs[0].ID != "TR 21.905" {
		t.Errorf("spec ID = %q, want %q", result.Specs[0].ID, "TR 21.905")
	}

	// The part file's sections must be stored under the cover's TR label.
	sections, err := d.GetTOC(t.Context(), "TR 21.905", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) == 0 {
		t.Error("expected the part file's sections under TR 21.905")
	}
}

// TestConvertDir_TRDocType checks the same TS/TR unification for directory
// imports: the cover file's document type wins for every file of the spec.
func TestConvertDir_TRDocType(t *testing.T) {
	partDocx := makeMinimalDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="Heading 1"/></w:pPr><w:r><w:t>5 Definitions</w:t></w:r></w:p>`+
			`<w:p><w:r><w:t>Vocabulary body text.</w:t></w:r></w:p>`)
	coverDocx := makeMinimalDocx(t,
		`<w:p><w:pPr><w:pStyle w:val="ZA"/></w:pPr><w:r><w:t>3GPP TR 21.905 V17.2.0 (2022-03)</w:t></w:r></w:p>`)

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "21905-h20_s01.docx"), partDocx, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "21905-h20_cover.docx"), coverDocx, 0o644); err != nil {
		t.Fatal(err)
	}

	d := setupTestDB(t)
	if err := ConvertDir(context.Background(), d, dir, 2, false, false); err != nil {
		t.Fatalf("ConvertDir: %v", err)
	}

	result, err := d.ListSpecs(t.Context(), "", "21.905", -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Specs) != 1 || result.Specs[0].ID != "TR 21.905" {
		t.Fatalf("specs = %+v, want exactly one TR 21.905", result.Specs)
	}
	sections, err := d.GetTOC(t.Context(), "TR 21.905", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) == 0 {
		t.Error("expected the part file's sections under TR 21.905")
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

	sections, err := d.GetTOC(t.Context(), "TS 36.133", "")
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
	apis, err := d.ListOpenAPI(t.Context(), "TS 29.510")
	if err != nil {
		t.Fatal(err)
	}
	if len(apis) == 0 {
		t.Fatal("expected OpenAPI specs to be imported from TS 29.510 YAML files")
	}
	t.Logf("Imported %d OpenAPI specs from TS 29.510", len(apis))

	// Verify sections were also parsed.
	sections, err := d.GetTOC(t.Context(), "TS 29.510", "")
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

	sections, err := d.GetTOC(t.Context(), "TS 24.229", "")
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
	sections, err := d.GetTOC(t.Context(), "TS 23.274", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) == 0 {
		t.Error("expected sections for TS 23.274 after pipeline run")
	}
}

// TestPipelineRun_ContextCancel verifies Pipeline.Run honors context
// cancellation. The fake server blocks indefinitely on the download request,
// the test cancels the context, and Pipeline.Run must return promptly.
func TestPipelineRun_ContextCancel(t *testing.T) {
	requested := make(chan struct{}, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requested <- struct{}{}:
		default:
		}
		// Block until the client cancels the request.
		<-r.Context().Done()
	}))
	defer ts.Close()

	d := setupTestDB(t)

	specs := []*SpecVersion{{
		Series:        "23",
		SpecID:        "23.274",
		Filename:      "23274-i20.zip",
		VersionLetter: "i",
		VersionMinor:  20,
		Release:       18,
		URL:           ts.URL + "/blocking.zip",
	}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := &Pipeline{
		DB:      d,
		Client:  ts.Client(),
		Workers: 1,
		Timeout: 30 * time.Second,
	}

	done := make(chan error, 1)
	go func() {
		done <- p.Run(ctx, specs)
	}()

	// Wait until the server has actually started serving the request, then
	// cancel.
	select {
	case <-requested:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("server never received a request")
	}
	cancel()

	select {
	case err := <-done:
		// Pipeline.Run swallows individual goroutine cancel errors and returns
		// nil; the only contract is that it returns promptly.
		_ = err
	case <-time.After(10 * time.Second):
		t.Fatal("Pipeline.Run did not return within 10s after cancel")
	}
}

// TestConvertDir_ConvertDocNoDocFiles verifies that passing convertDoc=true
// to ConvertDir does not break the normal .docx-only import path (#63):
// ConvertDocFiles is a no-op when the directory has no .doc files, so this
// does not require LibreOffice.
func TestConvertDir_ConvertDocNoDocFiles(t *testing.T) {
	docxData, err := os.ReadFile(testdataDocxPath(t))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.docx"), docxData, 0o644); err != nil {
		t.Fatal(err)
	}

	d := setupTestDB(t)

	if err := ConvertDir(context.Background(), d, dir, 1, true, false); err != nil {
		t.Fatalf("ConvertDir with convertDoc=true: %v", err)
	}

	result, err := d.ListSpecs(t.Context(), "", "", -1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Specs) == 0 {
		t.Error("expected at least one spec in DB after ConvertDir")
	}
}

// TestConvertDir_ConvertDocFailurePropagates verifies that a .doc conversion
// failure is surfaced as a non-nil error from ConvertDir instead of being
// silently logged (#64 review), while a valid pre-existing .docx in the same
// directory is still imported. PATH is overridden so the libreoffice
// invocation fails deterministically regardless of whether LibreOffice is
// actually installed on the host.
func TestConvertDir_ConvertDocFailurePropagates(t *testing.T) {
	docxData, err := os.ReadFile(testdataDocxPath(t))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "good.docx"), docxData, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bad.doc"), []byte("not a real doc"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", t.TempDir())

	d := setupTestDB(t)

	err = ConvertDir(context.Background(), d, dir, 1, true, false)
	if err == nil {
		t.Fatal("expected error when .doc conversion fails")
	}

	result, listErr := d.ListSpecs(t.Context(), "", "", -1, 0)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(result.Specs) == 0 {
		t.Error("expected the pre-existing .docx to still be imported despite the conversion error")
	}
}

// TestConvertDir_EmptyDirError verifies ConvertDir returns an error when no
// .docx files are present.
func TestConvertDir_EmptyDirError(t *testing.T) {
	dir := t.TempDir()
	d := setupTestDB(t)
	err := ConvertDir(context.Background(), d, dir, 1, false, false)
	if err == nil {
		t.Fatal("expected error for empty directory")
	}
	if !strings.Contains(err.Error(), "no .docx files") {
		t.Errorf("error = %v, want 'no .docx files'", err)
	}
}

// TestConvertDir_MissingDir verifies ConvertDir surfaces filesystem errors
// when the directory does not exist.
func TestConvertDir_MissingDir(t *testing.T) {
	d := setupTestDB(t)
	err := ConvertDir(context.Background(), d, filepath.Join(t.TempDir(), "nope"), 1, false, false)
	if err == nil {
		t.Fatal("expected error for missing directory")
	}
}

// TestConvertSingleFile exercises the ConvertSingleFile public API end to end
// using the shared testdata .docx file, verifying that the spec, sections,
// and derived release number all land in the target database.
func TestConvertSingleFile(t *testing.T) {
	docxPath := testdataDocxPath(t)
	if _, err := os.Stat(docxPath); err != nil {
		t.Skipf("testdata docx not available: %v", err)
	}
	d := setupTestDB(t)

	if err := ConvertSingleFile(context.Background(), d, docxPath, false); err != nil {
		t.Fatalf("ConvertSingleFile: %v", err)
	}

	// Parser pulls spec ID from metadata; testdata file is TS 23.274.
	sections, err := d.GetTOC(t.Context(), "TS 23.274", "")
	if err != nil {
		t.Fatalf("GetTOC: %v", err)
	}
	if len(sections) == 0 {
		t.Error("expected sections for TS 23.274 after ConvertSingleFile")
	}

	specs, err := d.ListSpecs(t.Context(), "", "", -1, 0)
	if err != nil {
		t.Fatalf("ListSpecs: %v", err)
	}
	found := false
	for _, s := range specs.Specs {
		if s.ID == "TS 23.274" {
			found = true
			// releaseFromDocxFilename derives release from the i-prefix.
			if s.Release != "18" {
				t.Errorf("Release = %q, want 18", s.Release)
			}
			break
		}
	}
	if !found {
		t.Error("TS 23.274 missing from ListSpecs result")
	}
}

// TestConvertSingleFile_BadPath verifies the error path when the docx file
// cannot be parsed (missing file).
func TestConvertSingleFile_BadPath(t *testing.T) {
	d := setupTestDB(t)
	err := ConvertSingleFile(context.Background(), d, filepath.Join(t.TempDir(), "missing.docx"), false)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "parse") {
		t.Errorf("error = %v, want to contain 'parse'", err)
	}
}

// TestReleaseFromDocxFilename pins down the version-letter → release mapping
// used to populate the Release column when importing single files.
func TestReleaseFromDocxFilename(t *testing.T) {
	cases := map[string]int{
		"23501-i30.docx":   18,
		"24229-h50.docx":   17,
		"29510-f60.docx":   15,
		"weirdname.docx":   0,
		"23501.docx":       0,
		"38101-1-j50.docx": 19,
		// #131: a split-spec chunk suffix ("_s00-11") must not be mistaken
		// for the version token — the real token ("920") sits right after
		// the spec number, and its first digit (9) is the release.
		"36133-920_s00-11.docx":     9,
		"38101-1-k00_s00-05.docx":   20,
		"38101-1-k00_sAnnexes.docx": 20,
	}
	for in, want := range cases {
		if got := releaseFromDocxFilename(in); got != want {
			t.Errorf("releaseFromDocxFilename(%q) = %d, want %d", in, got, want)
		}
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

// TestPipelineRun_CanceledContext verifies that a cancelled context stops the
// run with the context error after waiting for workers.
func TestPipelineRun_CanceledContext(t *testing.T) {
	d := setupTestDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := &Pipeline{DB: d, Workers: 1, Timeout: time.Second}
	specs := []*SpecVersion{{SpecID: "23.501", URL: "http://192.0.2.1/none.zip"}}
	if err := p.Run(ctx, specs); err == nil {
		t.Error("expected the context error, got nil")
	}
}

// TestPipelineRun_AllFailed verifies that a run in which every spec failed
// reports an error instead of success, so cron/CI builds cannot silently
// produce an empty database.
func TestPipelineRun_AllFailed(t *testing.T) {
	// A valid ZIP whose only .docx cannot be parsed makes processOne fail
	// quickly, without the download retry backoff.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("bad.docx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("not a docx")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(buf.Bytes())
	}))
	defer ts.Close()

	d := setupTestDB(t)
	p := &Pipeline{DB: d, Client: ts.Client(), Workers: 1, Timeout: 10 * time.Second}
	specs := []*SpecVersion{{SpecID: "23.501", Filename: "23501-i60.zip", URL: ts.URL + "/23501-i60.zip"}}
	if err := p.Run(context.Background(), specs); err == nil {
		t.Error("expected an error when every spec failed")
	}
}

// TestYAMLVersionRE checks that the OpenAPI version capture stops at the end of
// the line. The character class must exclude a newline, and must not exclude
// the letter "n" or a backslash.
func TestYAMLVersionRE(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "unquoted followed by more yaml",
			content: "info:\n  version: 1.2.3\n  title: Nnrf_NFManagement\n",
			want:    "1.2.3",
		},
		{
			name:    "single quoted",
			content: "info:\n  version: '1.2.0-alpha.1'\n  title: Nnrf_NFManagement\n",
			want:    "1.2.0-alpha.1",
		},
		{
			name:    "double quoted",
			content: "info:\n  version: \"1.2.0-alpha.1\"\n  title: Nnrf_NFManagement\n",
			want:    "1.2.0-alpha.1",
		},
		{
			name:    "unquoted containing the letter n",
			content: "info:\n  version: 1.0.0-nightly\n  title: 3gpp-ue-context-transfer\n",
			want:    "1.0.0-nightly",
		},
		{
			name:    "unquoted on the last line without a trailing newline",
			content: "info:\n  version: 1.0.0",
			want:    "1.0.0",
		},
		{
			name:    "crlf line ending",
			content: "info:\r\n  version: 1.2.3\r\n  title: Nnrf_NFManagement\r\n",
			want:    "1.2.3",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := yamlVersionRE.FindSubmatch([]byte(tc.content))
			if m == nil {
				t.Fatalf("no match for %q", tc.content)
			}
			// processOne applies the same TrimSpace to the capture.
			if got := strings.TrimSpace(string(m[1])); got != tc.want {
				t.Errorf("version = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestConvertDir_DBErrorPropagates verifies that a failing database write is
// surfaced as a non-nil error instead of being logged and forgotten. Without
// it, an import that stored nothing at all still exited 0.
func TestConvertDir_DBErrorPropagates(t *testing.T) {
	docxData, err := os.ReadFile(testdataDocxPath(t))
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "spec.docx"), docxData, 0o644); err != nil {
		t.Fatal(err)
	}

	// A database with no schema: every insert fails with "no such table".
	d, err := db.OpenReadWrite(filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })

	if err := ConvertDir(context.Background(), d, dir, 1, false, false); err == nil {
		t.Fatal("expected an error when the database write fails")
	}
}
