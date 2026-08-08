package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/testutil"
	"github.com/higebu/3gpp-mcp/tools"
)

func TestRunListSpecs(t *testing.T) {
	d := testutil.SetupTestDB(t)
	var out bytes.Buffer
	if err := runListSpecs(t.Context(), &out, d, "", "", 0, 0); err != nil {
		t.Fatalf("runListSpecs: %v", err)
	}
	var result db.ListSpecsResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if result.TotalCount != 3 {
		t.Errorf("expected 3 specs, got %d", result.TotalCount)
	}

	out.Reset()
	if err := runListSpecs(t.Context(), &out, d, "29", "", 0, 0); err != nil {
		t.Fatalf("runListSpecs series filter: %v", err)
	}
	if !strings.Contains(out.String(), "TS 29.510") || strings.Contains(out.String(), "TS 23.501") {
		t.Errorf("series filter not applied:\n%s", out.String())
	}
}

func TestRunSearch(t *testing.T) {
	d := testutil.SetupTestDB(t)
	var out bytes.Buffer
	if err := runSearch(t.Context(), &out, d, "architecture", "", 0, 0); err != nil {
		t.Fatalf("runSearch: %v", err)
	}
	var results db.SearchResults
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if results.TotalCount == 0 {
		t.Error("expected search hits for 'architecture'")
	}

	// The comma-separated spec filter must narrow the search.
	out.Reset()
	if err := runSearch(t.Context(), &out, d, "Scope", "TS 29.510, ", 0, 0); err != nil {
		t.Fatalf("runSearch with spec filter: %v", err)
	}
	if strings.Contains(out.String(), "TS 23.501") {
		t.Errorf("spec filter not applied:\n%s", out.String())
	}
}

func TestRunGetTOC(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	var out, errOut bytes.Buffer
	if err := runGetTOC(t.Context(), &out, &errOut, src, "TS 23.501", ""); err != nil {
		t.Fatalf("runGetTOC: %v", err)
	}
	for _, want := range []string{"Table of Contents", "5.1.1 Overview", "TS 23.501 v18.6.0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out.String())
		}
	}

	if err := runGetTOC(t.Context(), &out, &errOut, src, "TS 99.999", ""); err == nil {
		t.Error("expected error for unknown spec")
	}
}

func TestRunGetSection(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	var out, errOut bytes.Buffer
	if err := runGetSection(t.Context(), &out, &errOut, src, "TS 23.501", "5.1", "", false); err != nil {
		t.Fatalf("runGetSection: %v", err)
	}
	if !strings.Contains(out.String(), "[Source: TS 23.501 v18.6.0 (Rel-18) — Section 5.1]") {
		t.Errorf("missing provenance header:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "General architecture description") {
		t.Errorf("missing section content:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Overview of the architecture") {
		t.Errorf("subsection content leaked without -subsections:\n%s", out.String())
	}

	out.Reset()
	if err := runGetSection(t.Context(), &out, &errOut, src, "TS 23.501", "5.1", "", true); err != nil {
		t.Fatalf("runGetSection subsections: %v", err)
	}
	if !strings.Contains(out.String(), "Overview of the architecture") {
		t.Errorf("expected subsection content with -subsections:\n%s", out.String())
	}

	if err := runGetSection(t.Context(), &out, &errOut, src, "TS 23.501", "9.9", "", false); err == nil {
		t.Error("expected error for missing section")
	}
}

func TestRunCompareVersions_SameVersion(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	var out, errOut bytes.Buffer
	// -old resolves in the database and -new defaults to the database
	// version, so both sides land on 18.6.0: an informational answer.
	err := runCompareVersions(t.Context(), &out, &errOut, src, "TS 23.501", "18.6.0", "", "", false, 0)
	if err != nil {
		t.Fatalf("runCompareVersions: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to compare") {
		t.Errorf("expected same-version notice, got:\n%s", out.String())
	}
}

func TestRunCompareVersions_UnavailableVersion(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d) // no Store: archived versions cannot be fetched
	var out, errOut bytes.Buffer
	err := runCompareVersions(t.Context(), &out, &errOut, src, "TS 23.501", "17.9.0", "", "", false, 0)
	if err == nil {
		t.Fatal("expected error for a version that cannot be fetched")
	}
	var unavailable *tools.VersionUnavailableError
	if !errors.As(err, &unavailable) {
		t.Errorf("expected VersionUnavailableError, got: %v", err)
	}
}

func TestRunGetReferences(t *testing.T) {
	d := testutil.SetupTestDB(t)
	var out bytes.Buffer
	if err := runGetReferences(t.Context(), &out, d, "TS 24.229", "5.1", db.DirectionOutgoing, false); err != nil {
		t.Fatalf("runGetReferences: %v", err)
	}
	var refs []db.Reference
	if err := json.Unmarshal(out.Bytes(), &refs); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(refs) != 5 {
		t.Errorf("expected 5 outgoing references, got %d", len(refs))
	}

	out.Reset()
	if err := runGetReferences(t.Context(), &out, d, "TS 33.203", "", db.DirectionIncoming, false); err != nil {
		t.Fatalf("runGetReferences incoming: %v", err)
	}
	refs = nil
	if err := json.Unmarshal(out.Bytes(), &refs); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(refs) != 2 {
		t.Errorf("expected 2 incoming references, got %d", len(refs))
	}

	// Outgoing without a section number cannot be answered.
	if err := runGetReferences(t.Context(), &out, d, "TS 24.229", "", db.DirectionOutgoing, false); err == nil {
		t.Error("expected error for outgoing direction without section number")
	}

	// No matches must still print valid JSON.
	out.Reset()
	if err := runGetReferences(t.Context(), &out, d, "TS 99.999", "", db.DirectionIncoming, false); err != nil {
		t.Fatalf("runGetReferences no matches: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("expected [] for no matches, got:\n%s", out.String())
	}
}

func TestRunListOpenAPI(t *testing.T) {
	d := testutil.SetupTestDB(t)
	var out bytes.Buffer
	if err := runListOpenAPI(t.Context(), &out, d, ""); err != nil {
		t.Fatalf("runListOpenAPI: %v", err)
	}
	var specs []db.OpenAPISpec
	if err := json.Unmarshal(out.Bytes(), &specs); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(specs) != 1 || specs[0].APIName != "Nnrf_NFManagement" {
		t.Errorf("unexpected listing: %+v", specs)
	}

	out.Reset()
	if err := runListOpenAPI(t.Context(), &out, d, "TS 23.501"); err != nil {
		t.Fatalf("runListOpenAPI filtered: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("expected [] for a spec without OpenAPI, got:\n%s", out.String())
	}
}

func TestRunGetOpenAPI(t *testing.T) {
	d := testutil.SetupTestDB(t)
	var out bytes.Buffer
	if err := runGetOpenAPI(t.Context(), &out, d, "TS 29.510", "Nnrf_NFManagement", "", ""); err != nil {
		t.Fatalf("runGetOpenAPI: %v", err)
	}
	if !strings.Contains(out.String(), "openapi: 3.0.0") {
		t.Errorf("expected full YAML output, got:\n%s", out.String())
	}

	out.Reset()
	if err := runGetOpenAPI(t.Context(), &out, d, "TS 29.510", "Nnrf_NFManagement", "", "NFProfile"); err != nil {
		t.Fatalf("runGetOpenAPI schema filter: %v", err)
	}
	if !strings.Contains(out.String(), "NFProfile") || strings.Contains(out.String(), "/nf-instances") {
		t.Errorf("schema filter not applied:\n%s", out.String())
	}

	out.Reset()
	if err := runGetOpenAPI(t.Context(), &out, d, "TS 29.510", "Nnrf_NFManagement", "/nf-instances", ""); err != nil {
		t.Fatalf("runGetOpenAPI path filter: %v", err)
	}
	if !strings.Contains(out.String(), "/nf-instances") {
		t.Errorf("path filter not applied:\n%s", out.String())
	}
}

// seedTestImage inserts one PNG image row for TS 23.501.
func seedTestImage(t *testing.T, d *db.DB) []byte {
	t.Helper()
	data := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	err := d.Exec(
		"INSERT INTO images (spec_id, version, name, mime_type, data, llm_readable) VALUES (?, ?, ?, ?, ?, 1)",
		"TS 23.501", "18.6.0", "figure1.png", "image/png", data,
	)
	if err != nil {
		t.Fatalf("seed image: %v", err)
	}
	return data
}

func TestRunListImages(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	var out, errOut bytes.Buffer

	// No images must still print valid JSON with a zero count.
	if err := runListImages(t.Context(), &out, &errOut, src, "TS 23.501", ""); err != nil {
		t.Fatalf("runListImages empty: %v", err)
	}
	var listing struct {
		Images []db.ImageInfo `json:"images"`
		Count  int            `json:"count"`
	}
	if err := json.Unmarshal(out.Bytes(), &listing); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if listing.Count != 0 || listing.Images == nil {
		t.Errorf("expected empty listing with non-null images array, got:\n%s", out.String())
	}

	seedTestImage(t, d)
	out.Reset()
	if err := runListImages(t.Context(), &out, &errOut, src, "TS 23.501", ""); err != nil {
		t.Fatalf("runListImages: %v", err)
	}
	if err := json.Unmarshal(out.Bytes(), &listing); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if listing.Count != 1 || listing.Images[0].Name != "figure1.png" {
		t.Errorf("unexpected listing:\n%s", out.String())
	}
}

func TestRunGetImage(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	data := seedTestImage(t, d)

	var out, errOut bytes.Buffer
	if err := runGetImage(t.Context(), &out, &errOut, src, "TS 23.501", "figure1.png", "", ""); err != nil {
		t.Fatalf("runGetImage: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("stdout bytes differ from stored image")
	}
	if !strings.Contains(errOut.String(), "image/png") {
		t.Errorf("expected MIME type on stderr, got: %s", errOut.String())
	}

	outFile := filepath.Join(t.TempDir(), "img.png")
	if err := runGetImage(t.Context(), &out, &errOut, src, "TS 23.501", "figure1.png", "", outFile); err != nil {
		t.Fatalf("runGetImage -o: %v", err)
	}
	written, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !bytes.Equal(written, data) {
		t.Errorf("file bytes differ from stored image")
	}

	if err := runGetImage(t.Context(), &out, &errOut, src, "TS 23.501", "nope.png", "", ""); err == nil {
		t.Error("expected error for missing image")
	}
}

// errorRoundTripper fails every request, so the archive listing errors without
// touching the network.
type errorRoundTripper struct{}

func (errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("no network in tests")
}

func TestRunListVersions_ArchiveUnreachable(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	src.UseCache = false
	src.Client = &http.Client{Transport: errorRoundTripper{}}

	var out, errOut bytes.Buffer
	if err := runListVersions(t.Context(), &out, &errOut, src, "TS 23.501"); err != nil {
		t.Fatalf("runListVersions: %v", err)
	}
	var listing tools.ListVersionsOutput
	if err := json.Unmarshal(out.Bytes(), &listing); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(listing.Versions) != 1 || listing.Versions[0].Version != "18.6.0" {
		t.Errorf("expected the database version, got:\n%s", out.String())
	}
	if listing.Versions[0].Availability != tools.AvailabilityDatabase {
		t.Errorf("expected database availability, got %q", listing.Versions[0].Availability)
	}
	// The incomplete listing must be flagged on stderr, not silently returned.
	if !strings.Contains(errOut.String(), "WARNING") {
		t.Errorf("expected archive warning on stderr, got: %s", errOut.String())
	}
}

// TestVersionCacheExists covers the guard that keeps list-versions from
// creating a cache that is not already on disk.
func TestVersionCacheExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions.db")
	qf := &queryFlags{versionCache: path}
	if qf.versionCacheExists() {
		t.Error("expected false for a missing cache file")
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !qf.versionCacheExists() {
		t.Error("expected true once the cache file exists")
	}
}

func TestWaitForFetch(t *testing.T) {
	orig := fetchPollInterval
	fetchPollInterval = time.Millisecond
	defer func() { fetchPollInterval = orig }()

	calls := 0
	var errOut bytes.Buffer
	err := waitForFetch(t.Context(), &errOut, func() error {
		calls++
		if calls < 3 {
			return &tools.FetchInProgressError{SpecID: "TS 23.501", Version: "17.9.0"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("waitForFetch: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
	// The progress note is announced once, not on every poll.
	if got := strings.Count(errOut.String(), "Downloading"); got != 1 {
		t.Errorf("expected one announcement, got %d: %s", got, errOut.String())
	}

	// A terminal error passes through unchanged.
	sentinel := errors.New("boom")
	if err := waitForFetch(t.Context(), &errOut, func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}

	// Cancellation stops the polling.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = waitForFetch(ctx, &errOut, func() error {
		return &tools.FetchInProgressError{SpecID: "TS 23.501", Version: "17.9.0"}
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// TestCmdGetSection_ArgOrder covers the options-before-positionals guard via
// subprocess, mirroring TestCmdCompletion_UnknownShell.
func TestCmdGetSection_ArgOrder(t *testing.T) {
	if os.Getenv("CMD_GET_SECTION_ARGS_HELPER") == "1" {
		cmdGetSection([]string{"TS 23.501"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCmdGetSection_ArgOrder")
	cmd.Env = append(os.Environ(), "CMD_GET_SECTION_ARGS_HELPER=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit for missing section number")
	}
	if !strings.Contains(stderr.String(), "Options must come before positional arguments.") {
		t.Errorf("expected usage reminder on stderr, got: %s", stderr.String())
	}
}
