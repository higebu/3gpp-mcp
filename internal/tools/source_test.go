package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/internal/converter/pipeline"
	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/versionstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
		// Tests read two versions side by side; the default zero limit keeps
		// only the newest fetch, making concurrent fetches evict each other.
		LimitBytes: -1,
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
		if res.Archived {
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
	if !res.Archived || res.Version != "19.5.0" {
		t.Fatalf("Resolution = %+v, want archived v19.5.0", res)
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
	if !res.Archived || len(toc) != 1 {
		t.Errorf("GetTOC = %+v, %+v; want one archived section", toc, res)
	}
}

// TestGetSectionArchivedMissingSection checks that a section the cached
// version genuinely does not hold stays a definitive versioned not-found —
// the eviction re-check must not turn it into a retry hint.
func TestGetSectionArchivedMissingSection(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithStore(t, d, cannedFetcher)
	handler := HandleGetSection(src)

	result, _, err := handler(context.Background(), nil, GetSectionInput{
		SpecID:        "TS 23.501",
		SectionNumber: "99",
		Version:       "19.5.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected an error result for a missing archived section")
	}
	text := getTextContent(result)
	if !strings.Contains(text, "section 99 not found in TS 23.501 v19.5.0") {
		t.Errorf("expected a versioned not-found message, got: %s", text)
	}
}

// TestArchivedReadAfterEviction covers the window between resolve and the
// store read: a fetch of another version can evict the resolved one there, so
// an empty read of an evicted version must become a retryable
// fetch-in-progress error rather than a definitive not-found.
func TestArchivedReadAfterEviction(t *testing.T) {
	d := setupTestDB(t)
	store, err := versionstore.Open(versionstore.Options{
		Path:    filepath.Join(t.TempDir(), "versions.db"),
		Fetcher: cannedFetcher,
		// Zero keeps only the newest fetch, so caching a second version
		// evicts the first through the real eviction path.
		LimitBytes: 0,
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

	if _, _, err := src.GetSection(context.Background(), "TS 23.501", "19.5.0", "5.1", false); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	// While cached, the whole-version reads serve the content.
	if sections, res, err := src.AllSections(context.Background(), "TS 23.501", "19.5.0"); err != nil || !res.Archived || len(sections) != 1 {
		t.Fatalf("AllSections while cached = %d sections, %+v, %v; want one archived section", len(sections), res, err)
	}

	// Fetching another version pushes 19.5.0 out of the zero-byte cache.
	if _, _, err := src.GetSection(context.Background(), "TS 23.501", "20.2.0", "5.1", false); err != nil {
		t.Fatalf("fetch evicting version: %v", err)
	}
	if cached, err := store.Has("TS 23.501", "19.5.0"); err != nil || cached {
		t.Fatalf("expected 19.5.0 to be evicted (cached=%v, err=%v)", cached, err)
	}

	// This is what raced whole-version and per-number reads see: no rows,
	// no error. Both must report a retryable in-progress fetch.
	res := Resolution{Version: "19.5.0", Archived: true}
	sections, err := src.archivedSections("TS 23.501", res, func() ([]db.Section, error) {
		return store.AllSections("TS 23.501", "19.5.0")
	})
	if len(sections) != 0 {
		t.Fatalf("read of an evicted version returned %d sections, want none", len(sections))
	}
	var inProgress *FetchInProgressError
	if !errors.As(err, &inProgress) {
		t.Fatalf("archivedSections after eviction = %v, want a FetchInProgressError", err)
	}
	if inProgress.Images {
		t.Error("the error must name the version's text, not its images")
	}

	if _, err := src.archivedSection("TS 23.501", res, "5.1", false, func() ([]db.Section, error) {
		return store.GetSection("TS 23.501", "19.5.0", "5.1", false)
	}); !errors.As(err, &inProgress) {
		t.Fatalf("archivedSection after eviction = %v, want a FetchInProgressError", err)
	}
}

// TestArchivedSectionStaleReadRecovers pins the eviction-and-restore race: the
// per-number read returns a stale empty result because an eviction hit it, but
// a fetch restored the version before the TOC snapshot. The snapshot proves
// the section exists, so the read runs again and returns the content instead
// of a definitive not-found.
func TestArchivedSectionStaleReadRecovers(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithStore(t, d, cannedFetcher)
	if _, _, err := src.GetSection(context.Background(), "TS 23.501", "19.5.0", "5.1", false); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	res := Resolution{Version: "19.5.0", Archived: true}
	reads := 0
	sections, err := src.archivedSection("TS 23.501", res, "5.1", false, func() ([]db.Section, error) {
		reads++
		if reads == 1 {
			// The eviction hit this read; the restoring fetch completed
			// before the snapshot below.
			return nil, nil
		}
		return src.Store.GetSection("TS 23.501", "19.5.0", "5.1", false)
	})
	if err != nil {
		t.Fatalf("archivedSection: %v", err)
	}
	if reads != 2 || len(sections) != 1 || !strings.Contains(sections[0].Content, "Archived text") {
		t.Fatalf("got %d reads, sections %+v; want the restored content", reads, sections)
	}
}

// TestArchivedSectionRacedTwiceStaysRetryable pins the exhaustion behavior:
// when evictions and restores race BOTH reads (each returns a stale empty
// result while the TOC snapshot shows the section exists), the caller gets a
// retryable fetch-in-progress error — existing content must never be reported
// as definitively missing.
func TestArchivedSectionRacedTwiceStaysRetryable(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithStore(t, d, cannedFetcher)
	if _, _, err := src.GetSection(context.Background(), "TS 23.501", "19.5.0", "5.1", false); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	res := Resolution{Version: "19.5.0", Archived: true}
	// Both reads race an eviction window; the version itself is restored each
	// time, so the TOC snapshot holds section 5.1 throughout. Also exercises
	// the subsection match: the read asks for parent section 5 with
	// subsections, which only 5.1 satisfies.
	reads := 0
	sections, err := src.archivedSection("TS 23.501", res, "5", true, func() ([]db.Section, error) {
		reads++
		return nil, nil
	})
	if reads != 2 || len(sections) != 0 {
		t.Fatalf("got %d reads, %d sections; want two raced empty reads", reads, len(sections))
	}
	var inProgress *FetchInProgressError
	if !errors.As(err, &inProgress) {
		t.Fatalf("archivedSection raced twice = %v, want a FetchInProgressError", err)
	}
}

// TestArchivedSectionEmptyStaysEmpty checks that a section the cached version
// genuinely lacks still reads as empty with no error — the TOC snapshot names
// every section the version has, so callers keep answering a definitive
// not-found — and that read failures propagate from both helpers.
func TestArchivedSectionEmptyStaysEmpty(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithStore(t, d, cannedFetcher)
	if _, _, err := src.GetSection(context.Background(), "TS 23.501", "19.5.0", "5.1", false); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	res := Resolution{Version: "19.5.0", Archived: true}
	sections, err := src.archivedSection("TS 23.501", res, "99", false, func() ([]db.Section, error) {
		return src.Store.GetSection("TS 23.501", "19.5.0", "99", false)
	})
	if err != nil || len(sections) != 0 {
		t.Fatalf("archivedSection = %+v, %v; want empty with no error", sections, err)
	}

	// A read failure propagates as-is from both helpers.
	readErr := errors.New("cache read blew up")
	failing := func() ([]db.Section, error) { return nil, readErr }
	if _, err := src.archivedSections("TS 23.501", res, failing); !errors.Is(err, readErr) {
		t.Fatalf("archivedSections with failing read = %v, want %v", err, readErr)
	}
	if _, err := src.archivedSection("TS 23.501", res, "5.1", false, failing); !errors.Is(err, readErr) {
		t.Fatalf("archivedSection with failing read = %v, want %v", err, readErr)
	}
}

// TestHandleGetSectionArchivedHeader checks that the provenance line names the
// version and warns that references are missing, on every page.
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
	want := "[Source: TS 23.501 v19.5.0 (Rel-19) — Section 5.1 (archived version; cross-references unavailable; pass this version to get_image/list_images)]"
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
		{Version: "20.2.0", Release: "20", Token: "k20", Availability: AvailabilityArchive},
		{Version: "19.5.0", Release: "19", Token: "j50", Availability: AvailabilityCached},
		{Version: "18.6.0", Release: "18", Token: "i60", Availability: AvailabilityDatabase},
	}
	for i, w := range want {
		if out.Versions[i] != w {
			t.Errorf("versions[%d] = %+v, want %+v", i, out.Versions[i], w)
		}
	}
}

// TestHandleListVersionsSurfacesArchiveFailure checks that a failed archive
// listing is reported alongside a partial version list — versions found via
// the database or cache must not be presented as if they were the complete
// set when the archive listing itself failed.
func TestHandleListVersionsSurfacesArchiveFailure(t *testing.T) {
	d := setupTestDB(t)
	src := NewSource(d)
	src.Client = unreachableArchiveClient(t)
	src.UseCache = false
	handler := HandleListVersions(src)

	result, _, err := handler(context.Background(), nil, ListVersionsInput{SpecID: "TS 23.501"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("a partial list with a warning should not be a tool error: %s", getTextContent(result))
	}

	// The JSON payload must still parse and hold the versions that were
	// found (from the database here), not be corrupted by the warning.
	var out ListVersionsOutput
	if err := json.Unmarshal([]byte(getTextContent(result)), &out); err != nil {
		t.Fatalf("payload is not valid JSON: %v; got %q", err, getTextContent(result))
	}
	if len(out.Versions) != 1 || out.Versions[0].Version != "18.6.0" {
		t.Fatalf("expected the one database version, got %+v", out.Versions)
	}

	if len(result.Content) != 2 {
		t.Fatalf("expected the archive-failure warning as a second content item, got %d items", len(result.Content))
	}
	warning := result.Content[1].(*mcp.TextContent).Text
	if !strings.Contains(warning, "failed to list archive versions") || !strings.Contains(warning, "TS 23.501") {
		t.Errorf("expected a warning naming the archive failure, got: %q", warning)
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

// TestGetSectionReleaseSelectorLandsOnDatabaseVersion checks that a release
// selector resolving to the version the build imported is served from the
// database — no fetch, no archived resolution.
func TestGetSectionReleaseSelectorLandsOnDatabaseVersion(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithStore(t, d, func(context.Context, *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		t.Error("fetcher must not run when the release selector lands on the database version")
		return db.Spec{}, nil, nil
	})

	// The archive listing names i60 (18.6.0) as Rel-18's newest version, and
	// that is exactly what the database holds.
	sections, res, err := src.GetSection(context.Background(), "TS 23.501", "Rel-18", "5.1", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Archived {
		t.Error("expected the database to serve the resolved release, not the cache")
	}
	if res.Version != "18.6.0" {
		t.Errorf("resolved version = %q, want 18.6.0", res.Version)
	}
	if len(sections) == 0 {
		t.Fatal("expected the database section to be returned")
	}
}

// TestHandleListVersionsFamilyIDSuggestsParts checks that a family ID with no
// versions anywhere — but with split parts in the database — names the parts.
func TestHandleListVersionsFamilyIDSuggestsParts(t *testing.T) {
	d := setupTestDB(t)
	if err := d.ExecScript(`INSERT INTO specs (id, version, version_token, title, release, series) VALUES
    ('TS 38.101-1', '18.6.0', 'i60', 'NR; UE radio transmission and reception; Part 1', '18', '38');`); err != nil {
		t.Fatalf("seed part: %v", err)
	}

	// The family directory exists but lists no zips, so the archive answer is
	// "no versions" with no error.
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/38_series/38.101/", func(http.ResponseWriter, *http.Request) {})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	src := NewSource(d)
	src.Client = &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}
	src.UseCache = false

	handler := HandleListVersions(src)
	result, _, err := handler(context.Background(), nil, ListVersionsInput{SpecID: "TS 38.101"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatalf("expected an error result, got: %q", getTextContent(result))
	}
	text := getTextContent(result)
	if !strings.Contains(text, "has multiple parts") || !strings.Contains(text, "TS 38.101-1") {
		t.Errorf("expected the parts hint naming TS 38.101-1, got: %q", text)
	}
}
