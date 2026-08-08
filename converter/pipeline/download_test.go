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

// TestDownloadSpecs_ConvertDocIgnoresStaleOutput covers collision matrix
// case 5 (this run's converted output vs. a stale leftover from an earlier
// invocation): a leftover .docx from an earlier invocation of this command
// does not get mistaken for evidence that this run's conversion succeeded
// (ocr review finding on #143's fix). outputDir is the caller's persistent
// --output-dir and is never cleared between runs, so a stale file can
// already sit at a spec's expected output path before conversion even
// starts this time.
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

// TestDownloadSpecs_ConvertDocDirectVsConvertedCollision_PreservesExisting
// covers collision matrix case 2 (direct .docx vs. converted .doc->.docx,
// same batch): a converted file must never overwrite another spec's real
// output that happens to share its basename (Greptile review finding on an
// earlier version of this fix, which let the rename through as long as it
// happened "this run" -- landing at the right path wasn't enough, it also
// must not destroy someone else's file already there). SpecD's archive
// ships an already-converted "shared.docx" directly (its DownloadAndExtract
// call returns OK), landing in outputDir before SpecE's .doc conversion
// runs. SpecE's own .doc file happens to convert to that very path
// ("shared.doc" -> "shared.docx"); SpecE must lose that race and stay
// DOC_ONLY, and SpecD's original content must survive untouched.
func TestDownloadSpecs_ConvertDocDirectVsConvertedCollision_PreservesExisting(t *testing.T) {
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

	if stats["OK"] != 1 {
		t.Errorf("OK = %d, want 1 (only SpecD's own direct .docx)", stats["OK"])
	}
	if stats["DOC_ONLY"] != 1 {
		t.Errorf("DOC_ONLY = %d, want 1 (SpecE loses the publish race and stays DOC_ONLY)", stats["DOC_ONLY"])
	}

	got, err := os.ReadFile(filepath.Join(outputDir, "shared.docx"))
	if err != nil {
		t.Fatalf("read shared.docx: %v", err)
	}
	if string(got) != "already a docx" {
		t.Errorf("shared.docx content = %q, want %q (SpecE's conversion must not have overwritten it)", got, "already a docx")
	}
}

// TestDownloadSpecs_ConvertDocConvertedVsConvertedCollision_NeitherPromoted
// covers collision matrix case 3 (converted .doc vs. converted .doc, two
// different specs): two DOC_ONLY specs whose .doc files share a basename
// must never both be promoted to OK (Greptile review finding). Both extract
// into the same path in the shared, flat _doc_files directory, so one
// extraction silently overwrites the other and only one spec's content
// survives to be converted; an existence check on the single resulting
// .docx cannot tell which spec it actually belongs to, so neither should be
// credited. The converted file must also never reach outputDir at all: an
// earlier version of this fix skipped promotion but still published the
// ambiguous result, leaving an ownerless .docx that neither spec was
// credited for (a later Greptile review finding on this same test).
func TestDownloadSpecs_ConvertDocConvertedVsConvertedCollision_NeitherPromoted(t *testing.T) {
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

	if _, err := os.Stat(filepath.Join(outputDir, "collide.docx")); !os.IsNotExist(err) {
		t.Errorf("collide.docx stat = %v, want it absent from outputDir (ambiguous ownership must not be published)", err)
	}
}

// TestDownloadSpecs_ConvertDocDuplicateBasenameWithinOneSpec covers
// collision matrix case 4 (duplicate basenames within one spec's OWN
// archive): a single spec's zip has two different .doc entries under
// different subfolders that both flatten to the same basename on
// extraction ("partA/dup.doc" and "partB/dup.doc" both become "dup.doc").
// Before this fix, docPathClaimants counted the spec's own repeated claim
// on that one path as if two different specs had claimed it, wrongly
// treating the spec as colliding with itself and leaving it at DOC_ONLY
// even though its own (single surviving) .doc file converts and publishes
// cleanly with no other spec involved at all.
func TestDownloadSpecs_ConvertDocDuplicateBasenameWithinOneSpec(t *testing.T) {
	writeFakeLibreOffice(t)

	zip := makeZipWithFiles(t, map[string][]byte{
		"partA/dup.doc": []byte("from partA"),
		"partB/dup.doc": []byte("from partB"),
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zip)
	}))
	defer ts.Close()

	specs := []*SpecVersion{
		{SpecID: "TS 88.009", URL: ts.URL + "/i.zip"},
	}

	outputDir := t.TempDir()
	stats := DownloadSpecs(context.Background(), &http.Client{}, specs, outputDir, 2, true, 10*time.Second)

	if stats["OK"] != 1 {
		t.Errorf("OK = %d, want 1 (the spec's own duplicate .doc basenames must not look like a cross-spec collision)", stats["OK"])
	}
	if stats["DOC_ONLY"] != 0 {
		t.Errorf("DOC_ONLY = %d, want 0", stats["DOC_ONLY"])
	}
}

// TestDownloadSpecs_ConvertDocPublishFailureStaysDocOnly verifies that a spec
// is never reported OK when its converted .docx fails to publish into
// outputDir (3rd Greptile review finding). Before this fix, promotion was
// decided from mere existence in the scratch conversion directory: if the
// subsequent os.Rename into outputDir then failed, the spec had already been
// counted OK, and the scratch-dir cleanup that follows would delete the only
// copy of the file that had ever existed -- leaving an "OK" spec with no
// .docx anywhere. This plants a directory at the exact path the converted
// .docx would publish to, which makes the publish fail deterministically
// (neither linking nor renaming a regular file onto a directory can succeed),
// and checks the spec stays DOC_ONLY.
//
// The scratch directory is kept in that case: an unpublishable converted file
// is the only copy that exists, so deleting it destroys the conversion's only
// result on any publish failure that is not the deliberate no-clobber skip.
func TestDownloadSpecs_ConvertDocPublishFailureStaysDocOnly(t *testing.T) {
	writeFakeLibreOffice(t)

	zip := makeZipWithFiles(t, map[string][]byte{
		"blocked.doc": []byte("h1"),
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zip)
	}))
	defer ts.Close()

	specs := []*SpecVersion{
		{SpecID: "TS 88.008", URL: ts.URL + "/h.zip"},
	}

	outputDir := t.TempDir()
	// A directory at the expected publish path makes the rename fail: you
	// cannot rename a regular file onto an existing directory.
	if err := os.MkdirAll(filepath.Join(outputDir, "blocked.docx"), 0o755); err != nil {
		t.Fatal(err)
	}

	stats := DownloadSpecs(context.Background(), &http.Client{}, specs, outputDir, 2, true, 10*time.Second)

	if stats["OK"] != 0 {
		t.Errorf("OK = %d, want 0 (the converted .docx never published into outputDir)", stats["OK"])
	}
	if stats["DOC_ONLY"] != 1 {
		t.Errorf("DOC_ONLY = %d, want 1", stats["DOC_ONLY"])
	}

	// The blocking directory must still be the untouched directory it was:
	// the publish must not have partially clobbered it.
	info, err := os.Stat(filepath.Join(outputDir, "blocked.docx"))
	if err != nil || !info.IsDir() {
		t.Errorf("blocked.docx = %v (isDir=%v), want the original directory intact", err, info != nil && info.IsDir())
	}

	// The converted file is unreachable from outputDir, so the scratch copy
	// is all there is; the cleanup must have left it alone.
	if !scratchDocxExists(t, outputDir, "blocked.docx") {
		t.Error("converted blocked.docx not found in any scratch dir: an unpublishable conversion result must not be deleted")
	}
}

// scratchDocxExists reports whether a converted file with the given basename
// survives in one of the .doc-convert-* scratch directories under outputDir.
func scratchDocxExists(t *testing.T, outputDir, name string) bool {
	t.Helper()
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		t.Fatalf("read outputDir: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), ".doc-convert-") {
			continue
		}
		if _, err := os.Stat(filepath.Join(outputDir, e.Name(), name)); err == nil {
			return true
		}
	}
	return false
}

// TestDownloadSpecs_ConvertDocRepeatRunPromotes verifies that running the same
// download --convert-doc into the same persistent --output-dir twice reports
// the spec OK both times. The publish step deliberately refuses to clobber an
// existing destination, but the file a second run collides with is the first
// run's own converted output: treating that as a lost race left the spec
// reported DOC_ONLY forever, with the freshly converted .docx thrown away by
// the scratch-dir cleanup even though the spec was fully converted on disk.
func TestDownloadSpecs_ConvertDocRepeatRunPromotes(t *testing.T) {
	writeFakeLibreOffice(t)

	zip := makeZipWithFiles(t, map[string][]byte{
		"specJ.doc": []byte("j1"),
	})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(zip)
	}))
	defer ts.Close()

	specs := []*SpecVersion{
		{SpecID: "TS 88.010", URL: ts.URL + "/j.zip"},
	}

	outputDir := t.TempDir()
	if stats := DownloadSpecs(context.Background(), &http.Client{}, specs, outputDir, 2, true, 10*time.Second); stats["OK"] != 1 {
		t.Fatalf("first run: OK = %d, want 1", stats["OK"])
	}

	// A converted .doc must not be left in the shared _doc_files directory:
	// it is converted wholesale on every run, so a finished file left behind
	// is re-run through LibreOffice on every later invocation.
	if _, err := os.Stat(filepath.Join(outputDir, "_doc_files", "specJ.doc")); !os.IsNotExist(err) {
		t.Errorf("specJ.doc stat = %v, want it removed after a successful conversion", err)
	}

	stats := DownloadSpecs(context.Background(), &http.Client{}, specs, outputDir, 2, true, 10*time.Second)
	if stats["OK"] != 1 {
		t.Errorf("second run: OK = %d, want 1 (the existing .docx is this spec's own output from the first run)", stats["OK"])
	}
	if stats["DOC_ONLY"] != 0 {
		t.Errorf("second run: DOC_ONLY = %d, want 0", stats["DOC_ONLY"])
	}
	if scratchDocxExists(t, outputDir, "specJ.docx") {
		t.Error("scratch dir kept after a successful publish: the converted file did reach outputDir")
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
