package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/versionstore"
)

// redirectTransport rewrites all request URLs to point at the test server,
// allowing tests to exercise code that uses the hardcoded pipeline baseURL.
type redirectTransport struct {
	base    http.RoundTripper
	testURL string
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, err := url.Parse(rt.testURL + req.URL.Path)
	if err != nil {
		return nil, err
	}
	req.URL = target
	return rt.base.RoundTrip(req)
}

// archiveClient serves a listing for TS 23.501 covering the seeded v18.6.0 plus
// two versions the database does not have.
func archiveClient(t *testing.T) *http.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/", func(w http.ResponseWriter, _ *http.Request) {
		for _, name := range []string{"23501-i60.zip", "23501-j50.zip", "23501-k20.zip"} {
			fmt.Fprintf(w, `<a href="%s">%s</a>`+"\n", name, name)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}
}

// sourceWithStore builds a Source backed by the seeded database plus a version
// cache whose fetcher returns canned sections instead of downloading.
func sourceWithStore(t *testing.T, d *db.DB, fetcher versionstore.Fetcher) *Source {
	t.Helper()
	store, err := versionstore.Open(versionstore.Options{
		Path:    filepath.Join(t.TempDir(), "versions.db"),
		Fetcher: fetcher,
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

func cannedFetcher(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
	return db.Spec{Title: "System architecture", Release: "19", Series: "23"},
		[]db.Section{{
			Number:  "5.1",
			Title:   "General",
			Level:   2,
			Content: "## 5.1 General\nArchived text.",
		}}, nil
}

// TestGetSectionUsesDatabaseVersion checks that asking for the version the
// build imported is served from the database, not fetched.
func TestGetSectionUsesDatabaseVersion(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithStore(t, d, func(context.Context, *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		t.Error("fetcher must not run for a version already in the database")
		return db.Spec{}, nil, nil
	})

	for _, request := range []string{"", "18.6.0", "i60"} {
		sections, res, err := src.GetSection(context.Background(), "TS 23.501", request, "1", false)
		if err != nil {
			t.Fatalf("GetSection(%q): %v", request, err)
		}
		if len(sections) != 1 {
			t.Fatalf("GetSection(%q) = %d sections, want 1", request, len(sections))
		}
		if res.archived {
			t.Errorf("GetSection(%q) reported archived, want database", request)
		}
	}
}

// TestGetSectionFetchesArchivedVersion covers the on-demand path end to end.
func TestGetSectionFetchesArchivedVersion(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithStore(t, d, cannedFetcher)

	sections, res, err := src.GetSection(context.Background(), "TS 23.501", "19.5.0", "5.1", false)
	if err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	if !res.archived || res.version != "19.5.0" {
		t.Fatalf("resolution = %+v, want archived v19.5.0", res)
	}
	if len(sections) != 1 || !strings.Contains(sections[0].Content, "Archived text") {
		t.Fatalf("GetSection = %+v, want the fetched content", sections)
	}
	if sections[0].SpecID != "TS 23.501" || sections[0].Version != "19.5.0" {
		t.Errorf("section identity = %s v%s, want TS 23.501 v19.5.0", sections[0].SpecID, sections[0].Version)
	}

	// The archive token names the same version, so the second call must hit the
	// cache rather than fetch again.
	toc, res, err := src.GetTOC(context.Background(), "TS 23.501", "j50")
	if err != nil {
		t.Fatalf("GetTOC: %v", err)
	}
	if !res.archived || len(toc) != 1 {
		t.Errorf("GetTOC = %+v, %+v; want one archived section", toc, res)
	}
}

// TestHandleGetSectionArchivedHeader checks that the provenance line names the
// version and warns that images and references are missing, on every page.
func TestHandleGetSectionArchivedHeader(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithStore(t, d, cannedFetcher)
	handler := HandleGetSection(src)

	result, _, err := handler(context.Background(), nil, GetSectionInput{
		SpecID:        "TS 23.501",
		SectionNumber: "5.1",
		Version:       "19.5.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := getTextContent(result)
	want := "[Source: TS 23.501 v19.5.0 (Rel-19) — Section 5.1 (archived version; images and cross-references unavailable)]"
	if !strings.HasPrefix(text, want) {
		t.Errorf("header = %q, want prefix %q", text, want)
	}

	// The same header must survive on a later page.
	page2, _, err := handler(context.Background(), nil, GetSectionInput{
		SpecID:        "TS 23.501",
		SectionNumber: "5.1",
		Version:       "19.5.0",
		Offset:        1,
		MaxLines:      1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(getTextContent(page2), want) {
		t.Errorf("page 2 lost the source header: %q", getTextContent(page2))
	}
}

// TestGetSectionWithoutStore checks the message when on-demand fetching is off.
func TestGetSectionWithoutStore(t *testing.T) {
	d := setupTestDB(t)
	src := NewSource(d)
	handler := HandleGetSection(src)

	result, _, err := handler(context.Background(), nil, GetSectionInput{
		SpecID:        "TS 23.501",
		SectionNumber: "5.1",
		Version:       "19.5.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected an error result")
	}
	if text := getTextContent(result); !strings.Contains(text, "on-demand fetching is disabled") {
		t.Errorf("message = %q, want it to say fetching is disabled", text)
	}
}

// TestGetSectionUnknownVersionListsAvailable checks that a bad version tells
// the caller which versions do exist.
func TestGetSectionUnknownVersionListsAvailable(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithStore(t, d, cannedFetcher)
	handler := HandleGetSection(src)

	result, _, err := handler(context.Background(), nil, GetSectionInput{
		SpecID:        "TS 23.501",
		SectionNumber: "5.1",
		Version:       "12.0.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := getTextContent(result)
	if !result.IsError {
		t.Fatalf("expected an error result, got %q", text)
	}
	for _, want := range []string{"20.2.0", "19.5.0", "18.6.0"} {
		if !strings.Contains(text, want) {
			t.Errorf("message %q should list available version %s", text, want)
		}
	}
}

// TestFetchStillRunningIsNotAnError checks that a fetch exceeding the budget
// returns a retry hint rather than a tool error, so the caller comes back.
func TestFetchStillRunningIsNotAnError(t *testing.T) {
	d := setupTestDB(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	src := sourceWithStore(t, d, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		<-release
		return cannedFetcher(ctx, sv)
	})
	src.Budget = 20 * time.Millisecond
	handler := HandleGetSection(src)

	result, _, err := handler(context.Background(), nil, GetSectionInput{
		SpecID:        "TS 23.501",
		SectionNumber: "5.1",
		Version:       "19.5.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("a fetch that is still running should not be reported as a tool error")
	}
	if text := getTextContent(result); !strings.Contains(text, "Call the same tool again") {
		t.Errorf("message = %q, want a retry hint", text)
	}
}

func TestHandleListVersions(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithStore(t, d, cannedFetcher)
	handler := HandleListVersions(src)

	// Cache one version so all three availability values appear.
	if _, _, err := src.GetSection(context.Background(), "TS 23.501", "19.5.0", "5.1", false); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	result, _, err := handler(context.Background(), nil, ListVersionsInput{SpecID: "TS 23.501"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := getTextContent(result)

	var out ListVersionsOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal %q: %v", text, err)
	}
	if len(out.Versions) != 3 {
		t.Fatalf("got %d versions, want 3: %+v", len(out.Versions), out.Versions)
	}
	want := []VersionInfo{
		{Version: "20.2.0", Release: "20", Token: "k20", Availability: availabilityArchive},
		{Version: "19.5.0", Release: "19", Token: "j50", Availability: availabilityCached},
		{Version: "18.6.0", Release: "18", Token: "i60", Availability: availabilityDatabase},
	}
	for i, w := range want {
		if out.Versions[i] != w {
			t.Errorf("versions[%d] = %+v, want %+v", i, out.Versions[i], w)
		}
	}
}

func TestHandleListVersionsRequiresSpecID(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleListVersions(NewSource(d))
	result, _, err := handler(context.Background(), nil, ListVersionsInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Error("expected an error result for a missing spec_id")
	}
}
