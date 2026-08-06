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

// TestDownloadAndExtract_AllDocExtractionsFail verifies that a ZIP whose only
// .doc entry cannot be extracted is reported FAILED, not DOC_ONLY (#143): a
// DOC_ONLY status with nothing on disk to convert misdirects the operator
// toward "install LibreOffice and retry" for a spec that has nothing to
// retry.
func TestDownloadAndExtract_AllDocExtractionsFail(t *testing.T) {
	zipBytes := makeRawZipEntry(t, "../evil.doc", []byte("pwned"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipBytes)
	}))
	defer ts.Close()

	outDir := t.TempDir()
	spec := &SpecVersion{SpecID: "TS 99.005", URL: ts.URL + "/z2.zip"}
	result, err := DownloadAndExtract(context.Background(), ts.Client(), spec, outDir, 5*time.Second)
	if err == nil {
		t.Fatal("expected an error when no .doc could be extracted")
	}
	if result.Status != "FAILED" {
		t.Errorf("status = %q, want FAILED", result.Status)
	}
	if len(result.DocFiles) != 0 {
		t.Errorf("DocFiles = %v, want none", result.DocFiles)
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

// TestDownloadAndExtract_AllDocxExtractionsFail verifies that a ZIP whose only
// .docx entry cannot be extracted is not reported as OK. The entry name carries
// a path-traversal component, so extractFile rejects it and no file lands in the
// output directory.
func TestDownloadAndExtract_AllDocxExtractionsFail(t *testing.T) {
	zipBytes := makeRawZipEntry(t, "../evil.docx", []byte("pwned"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipBytes)
	}))
	defer ts.Close()

	outDir := t.TempDir()
	spec := &SpecVersion{SpecID: "TS 99.003", URL: ts.URL + "/z.zip"}
	result, err := DownloadAndExtract(context.Background(), ts.Client(), spec, outDir, 5*time.Second)
	if err == nil {
		t.Fatal("expected an error when no .docx could be extracted")
	}
	if result.Status != "FAILED" {
		t.Errorf("status = %q, want FAILED", result.Status)
	}
	if len(result.DocxFiles) != 0 {
		t.Errorf("DocxFiles = %v, want none", result.DocxFiles)
	}
	if _, statErr := os.Stat(filepath.Join(outDir, "evil.docx")); !os.IsNotExist(statErr) {
		t.Errorf("evil.docx should not exist: %v", statErr)
	}
}

// TestDownloadAndExtract_OK verifies the success path is unchanged: an
// extractable .docx yields OK, a nil error and the extracted path.
func TestDownloadAndExtract_OK(t *testing.T) {
	zipBytes := makeZipWithFile(t, "spec.docx", []byte("docx content"))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipBytes)
	}))
	defer ts.Close()

	outDir := t.TempDir()
	spec := &SpecVersion{SpecID: "TS 99.004", URL: ts.URL + "/ok.zip"}
	result, err := DownloadAndExtract(context.Background(), ts.Client(), spec, outDir, 5*time.Second)
	if err != nil {
		t.Fatalf("DownloadAndExtract: %v", err)
	}
	if result.Status != "OK" {
		t.Errorf("status = %q, want OK", result.Status)
	}
	want := []string{filepath.Join(outDir, "spec.docx")}
	if len(result.DocxFiles) != 1 || result.DocxFiles[0] != want[0] {
		t.Fatalf("DocxFiles = %v, want %v", result.DocxFiles, want)
	}
	if _, err := os.Stat(want[0]); err != nil {
		t.Errorf("expected spec.docx on disk: %v", err)
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

// writeFakeLibreOffice installs a fake "libreoffice" executable on PATH that
// mimics --convert-to docx --outdir DIR FILE without needing real
// LibreOffice: any input whose basename contains "fail" exits non-zero and
// writes nothing; every other input writes an empty same-name .docx into the
// --outdir directory, exactly where the real conversion would.
func writeFakeLibreOffice(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	script := `#!/bin/sh
# PATH is overridden to just this directory for the test, so this script
# must not rely on external utilities like basename -- pure shell parameter
# expansion only.
outdir=""
prev=""
last=""
for arg in "$@"; do
  if [ "$prev" = "--outdir" ]; then
    outdir="$arg"
  fi
  last="$arg"
  prev="$arg"
done
base="${last##*/}"
case "$base" in
  *fail*) exit 1 ;;
esac
name="${base%.*}"
echo "converted" > "$outdir/$name.docx"
exit 0
`
	path := filepath.Join(dir, "libreoffice")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
}

// TestDownloadSpecs_ConvertDocPerSpec verifies DOC_ONLY->OK promotion is
// attributed per spec instead of by a blind min(convertedFileCount,
// docOnlySpecCount) (#143). SpecA ships two .doc files that both convert
// (partial success still counts as OK); SpecB ships a single .doc file whose
// conversion fails outright and must stay DOC_ONLY. The old min()-based logic
// would promote both specs to OK here, because two files convert overall
// while only two specs are DOC_ONLY.
func TestDownloadSpecs_ConvertDocPerSpec(t *testing.T) {
	writeFakeLibreOffice(t)

	zipA := makeZipWithFiles(t, map[string][]byte{
		"specA-part1.doc": []byte("a1"),
		"specA-part2.doc": []byte("a2"),
	})
	zipB := makeZipWithFiles(t, map[string][]byte{
		"specB-fail.doc": []byte("b1"),
	})

	tsA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipA)
	}))
	defer tsA.Close()
	tsB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipB)
	}))
	defer tsB.Close()

	specs := []*SpecVersion{
		{SpecID: "TS 88.001", URL: tsA.URL + "/a.zip"},
		{SpecID: "TS 88.002", URL: tsB.URL + "/b.zip"},
	}

	outputDir := t.TempDir()
	stats := DownloadSpecs(context.Background(), &http.Client{}, specs, outputDir, 2, true, 10*time.Second)

	if stats["OK"] != 1 {
		t.Errorf("OK = %d, want 1 (only SpecA converted)", stats["OK"])
	}
	if stats["DOC_ONLY"] != 1 {
		t.Errorf("DOC_ONLY = %d, want 1 (SpecB's conversion failed)", stats["DOC_ONLY"])
	}
}

// TestDownloadSpecs_ConvertDocIgnoresStaleOutput verifies that a leftover
// .docx from an earlier invocation of this command does not get mistaken for
// evidence that this run's conversion succeeded (ocr review finding on
// #143's fix). outputDir is the caller's persistent --output-dir and is
// never cleared between runs, so a stale file can already sit at a spec's
// expected output path before conversion even starts this time.
func TestDownloadSpecs_ConvertDocIgnoresStaleOutput(t *testing.T) {
	writeFakeLibreOffice(t)

	zip := makeZipWithFiles(t, map[string][]byte{
		"specC-fail.doc": []byte("c1"),
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zip)
	}))
	defer ts.Close()

	specs := []*SpecVersion{
		{SpecID: "TS 88.003", URL: ts.URL + "/c.zip"},
	}

	outputDir := t.TempDir()
	// Plant a stale .docx at the exact path this spec's .doc file would
	// convert to, simulating leftover output from an earlier run.
	if err := os.WriteFile(filepath.Join(outputDir, "specC-fail.docx"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	stats := DownloadSpecs(context.Background(), &http.Client{}, specs, outputDir, 2, true, 10*time.Second)

	if stats["OK"] != 0 {
		t.Errorf("OK = %d, want 0 (this run's conversion failed; the stale .docx must not count)", stats["OK"])
	}
	if stats["DOC_ONLY"] != 1 {
		t.Errorf("DOC_ONLY = %d, want 1", stats["DOC_ONLY"])
	}
}

// TestDownloadSpecs_ConvertDocSameBatchBasenameCollision verifies that a
// same-batch, different spec's freshly-extracted .docx does not block a
// DOC_ONLY spec's own promotion just because it happens to share a basename
// (Greptile review finding on the stale-output fix above). SpecD's archive
// ships an already-converted "shared.docx" directly (its DownloadAndExtract
// call returns OK), landing in outputDir before SpecE's .doc conversion
// runs. SpecE's own .doc file happens to convert to that very path
// ("shared.doc" -> "shared.docx"); an existence check alone cannot tell that
// apart from a stale leftover and would wrongly leave SpecE at DOC_ONLY, so
// the promotion must be based on whether the file was written at or after
// conversion started, not merely on whether it exists.
func TestDownloadSpecs_ConvertDocSameBatchBasenameCollision(t *testing.T) {
	writeFakeLibreOffice(t)

	zipD := makeZipWithFiles(t, map[string][]byte{
		"shared.docx": []byte("already a docx"),
	})
	zipE := makeZipWithFiles(t, map[string][]byte{
		"shared.doc": []byte("needs conversion"),
	})

	tsD := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipD)
	}))
	defer tsD.Close()
	tsE := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipE)
	}))
	defer tsE.Close()

	specs := []*SpecVersion{
		{SpecID: "TS 88.004", URL: tsD.URL + "/d.zip"},
		{SpecID: "TS 88.005", URL: tsE.URL + "/e.zip"},
	}

	outputDir := t.TempDir()
	stats := DownloadSpecs(context.Background(), &http.Client{}, specs, outputDir, 2, true, 10*time.Second)

	if stats["OK"] != 2 {
		t.Errorf("OK = %d, want 2 (SpecD's own .docx plus SpecE's converted .doc)", stats["OK"])
	}
	if stats["DOC_ONLY"] != 0 {
		t.Errorf("DOC_ONLY = %d, want 0 (SpecE's conversion succeeded despite the basename collision)", stats["DOC_ONLY"])
	}
}

// TestDownloadSpecs_ConvertDocSameBasenameAcrossSpecsStaysDocOnly verifies
// that two DOC_ONLY specs whose .doc files share a basename are never both
// promoted to OK (2nd Greptile review finding). Both extract into the same
// path in the shared, flat _doc_files directory, so one extraction silently
// overwrites the other and only one spec's content survives to be
// converted; an existence check on the single resulting .docx cannot tell
// which spec it actually belongs to, so neither should be credited.
func TestDownloadSpecs_ConvertDocSameBasenameAcrossSpecsStaysDocOnly(t *testing.T) {
	writeFakeLibreOffice(t)

	zipF := makeZipWithFiles(t, map[string][]byte{
		"collide.doc": []byte("from spec F"),
	})
	zipG := makeZipWithFiles(t, map[string][]byte{
		"collide.doc": []byte("from spec G"),
	})

	tsF := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipF)
	}))
	defer tsF.Close()
	tsG := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zipG)
	}))
	defer tsG.Close()

	specs := []*SpecVersion{
		{SpecID: "TS 88.006", URL: tsF.URL + "/f.zip"},
		{SpecID: "TS 88.007", URL: tsG.URL + "/g.zip"},
	}

	outputDir := t.TempDir()
	stats := DownloadSpecs(context.Background(), &http.Client{}, specs, outputDir, 2, true, 10*time.Second)

	if stats["OK"] != 0 {
		t.Errorf("OK = %d, want 0 (the surviving .doc can't be safely attributed to either spec)", stats["OK"])
	}
	if stats["DOC_ONLY"] != 2 {
		t.Errorf("DOC_ONLY = %d, want 2 (both specs stay DOC_ONLY rather than risk a false OK)", stats["DOC_ONLY"])
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
