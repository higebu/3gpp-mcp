package pipeline

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSpecEntry(t *testing.T) {
	tests := []struct {
		name   string
		entry  string
		wantID string // empty means expect nil
		wantRe int    // expected Release
	}{
		{"modern version", "23_series/23.501/23501-k10.zip", "23.501", 20},
		// Legacy all-digit versions encode the release in the first digit:
		// "300" -> release 3 (Release 1999).
		{"legacy version", "23_series/23.501/23501-300.zip", "23.501", 3},
		// Suffixed (multi-part) spec directories must be accepted.
		{"suffix spec dir", "38_series/38.101-1/38101-1-j50.zip", "38.101-1", 19},
		{"suffix spec dir part 2", "38_series/38.521-1/38521-1-j40.zip", "38.521-1", 19},
		// Base-36 versions with letters in the 2nd/3rd position must parse.
		{"base36 digit-major", "34_series/34.108/34108-3a0.zip", "34.108", 3},
		{"base36 letter-major", "34_series/34.108/34108-fb0.zip", "34.108", 15},
		// Legacy directories mix case: 08_series/08.09 holds 0809-301.ZIP and
		// 11_series/11.20 holds 1120-3J0.ZIP (issue #211).
		{"uppercase extension", "08_series/08.09/0809-301.ZIP", "08.09", 3},
		{"uppercase version token", "11_series/11.20/1120-3J0.ZIP", "11.20", 3},
		{"empty string", "", "", 0},
		{"no zip suffix", "23_series/23.501/23501-k10.docx", "", 0},
		{"wrong parts count", "23501-k10.zip", "", 0},
		{"bad series dir", "foo/23.501/23501-k10.zip", "", 0},
		{"whitespace", "  23_series/23.501/23501-a01.zip  ", "23.501", 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sv := ParseSpecEntry(tt.entry)
			if tt.wantID == "" {
				if sv != nil {
					t.Errorf("expected nil, got %+v", sv)
				}
				return
			}
			if sv == nil {
				t.Fatal("expected non-nil SpecVersion")
			}
			if sv.SpecID != tt.wantID {
				t.Errorf("SpecID = %q, want %q", sv.SpecID, tt.wantID)
			}
			if sv.Release != tt.wantRe {
				t.Errorf("Release = %d, want %d", sv.Release, tt.wantRe)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	entry := func(t *testing.T, e string) *SpecVersion {
		t.Helper()
		sv := ParseSpecEntry(e)
		if sv == nil {
			t.Fatalf("ParseSpecEntry(%q) = nil", e)
		}
		return sv
	}
	tests := []struct {
		name string
		a, b string // archive entries
		want int    // sign of compareVersions(a, b)
	}{
		{"newer release wins", "23_series/23.501/23501-k10.zip", "23_series/23.501/23501-j60.zip", 1},
		{"older release loses", "23_series/23.501/23501-j60.zip", "23_series/23.501/23501-k10.zip", -1},
		{"same version ties", "23_series/23.501/23501-k10.zip", "23_series/23.501/23501-k10.zip", 0},
		{"minor decides within release", "23_series/23.501/23501-k20.zip", "23_series/23.501/23501-k10.zip", 1},
		{"legacy vs letter era", "37_series/37.571-5/37571-5-j10.zip", "37_series/37.571-5/37571-5-100.zip", 1},
		{"base36 minor", "34_series/34.108/34108-fa0.zip", "34_series/34.108/34108-f20.zip", 1},
		// A long token's remainder must stay inside the minor component
		// instead of bleeding into the release comparison: release 18 with a
		// huge minor ("izzz") still loses to release 20 ("k00").
		{"long token cannot outrank a newer release", "23_series/23.501/23501-izzz.zip", "23_series/23.501/23501-k00.zip", -1},
		// A remainder long enough to saturate versionValue must still order
		// by release rather than overflow.
		{"saturating token stays within its release", "23_series/23.501/23501-jzzzzzzzzzzzzzz.zip", "23_series/23.501/23501-k00.zip", -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareVersions(entry(t, tt.a), entry(t, tt.b))
			switch {
			case tt.want > 0 && got <= 0, tt.want < 0 && got >= 0, tt.want == 0 && got != 0:
				t.Errorf("compareVersions = %d, want sign %d", got, tt.want)
			}
		})
	}
}

func TestFilterSpecs(t *testing.T) {
	specs := []*SpecVersion{
		{Series: "23", SpecID: "23.501", Release: 19, VersionMinor: 10, VersionLetter: "j"},
		{Series: "23", SpecID: "23.501", Release: 20, VersionMinor: 5, VersionLetter: "k"},
		{Series: "29", SpecID: "29.510", Release: 19, VersionMinor: 30, VersionLetter: "j"},
		{Series: "29", SpecID: "29.510", Release: 20, VersionMinor: 10, VersionLetter: "k"},
	}

	t.Run("filter by release", func(t *testing.T) {
		got := FilterSpecs(specs, SpecFilter{Release: 19})
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
	})

	t.Run("filter by series", func(t *testing.T) {
		got := FilterSpecs(specs, SpecFilter{Series: []string{"29"}})
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
	})

	t.Run("filter by spec ID", func(t *testing.T) {
		got := FilterSpecs(specs, SpecFilter{SpecID: "23.501"})
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
	})

	t.Run("latest only", func(t *testing.T) {
		got := FilterSpecs(specs, SpecFilter{LatestOnly: true})
		if len(got) != 2 {
			t.Fatalf("expected 2 (one per spec), got %d", len(got))
		}
		for _, s := range got {
			if s.Release != 20 {
				t.Errorf("expected latest release 20 for %s, got %d", s.SpecID, s.Release)
			}
		}
	})

	t.Run("max release caps the latest version", func(t *testing.T) {
		got := FilterSpecs(specs, SpecFilter{MaxRelease: 19, LatestOnly: true})
		if len(got) != 2 {
			t.Fatalf("expected 2 (one per spec), got %d", len(got))
		}
		for _, s := range got {
			if s.Release != 19 {
				t.Errorf("expected release 19 for %s, got %d", s.SpecID, s.Release)
			}
		}
	})

	// The difference from Release: a spec with no version in the capped
	// release stays in the selection at its newest older version, instead of
	// dropping out of the build entirely.
	t.Run("max release keeps specs older than the cap", func(t *testing.T) {
		withOldSpec := append([]*SpecVersion{
			{Series: "34", SpecID: "34.108", Release: 18, VersionMinor: 40, VersionLetter: "i"},
		}, specs...)

		got := FilterSpecs(withOldSpec, SpecFilter{MaxRelease: 19, LatestOnly: true})
		if len(got) != 3 {
			t.Fatalf("expected 3 (one per spec), got %d", len(got))
		}
		var found *SpecVersion
		for _, s := range got {
			if s.SpecID == "34.108" {
				found = s
			}
		}
		if found == nil {
			t.Fatal("34.108 dropped by the cap; expected its release 18 version")
		}
		if found.Release != 18 {
			t.Errorf("expected release 18 for 34.108, got %d", found.Release)
		}

		// Same input, exact-release filter: 34.108 has no release 19 version.
		exact := FilterSpecs(withOldSpec, SpecFilter{Release: 19, LatestOnly: true})
		if len(exact) != 2 {
			t.Fatalf("expected 2 with Release: 19, got %d", len(exact))
		}
	})

	t.Run("max release below every version", func(t *testing.T) {
		got := FilterSpecs(specs, SpecFilter{MaxRelease: 18, LatestOnly: true})
		if len(got) != 0 {
			t.Fatalf("expected 0, got %d", len(got))
		}
	})

	// The CLI rejects the combination, but as a filter the two are ANDed.
	t.Run("release and max release combined", func(t *testing.T) {
		got := FilterSpecs(specs, SpecFilter{Release: 20, MaxRelease: 19})
		if len(got) != 0 {
			t.Fatalf("expected 0, got %d", len(got))
		}
	})

	t.Run("no match", func(t *testing.T) {
		got := FilterSpecs(specs, SpecFilter{Release: 99})
		if len(got) != 0 {
			t.Fatalf("expected 0, got %d", len(got))
		}
	})

	t.Run("nil input", func(t *testing.T) {
		got := FilterSpecs(nil, SpecFilter{})
		if len(got) != 0 {
			t.Fatalf("expected 0, got %d", len(got))
		}
	})
}

func TestSpecVersionString(t *testing.T) {
	tests := []struct {
		sv   *SpecVersion
		want string
	}{
		{&SpecVersion{Version: "k10"}, "k10"},
		{&SpecVersion{Version: "300"}, "300"},
		{&SpecVersion{Version: "fa0"}, "fa0"},
	}
	for _, tt := range tests {
		got := SpecVersionString(tt.sv)
		if got != tt.want {
			t.Errorf("SpecVersionString = %q, want %q", got, tt.want)
		}
	}
}

// redirectTransport rewrites all request URLs to point at the test server,
// allowing tests to exercise code that uses the hardcoded baseURL.
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

// TestLoadSpecList covers the file-loading branch: skips blank lines,
// trims whitespace, and surfaces scanner errors as they arise.
func TestLoadSpecList(t *testing.T) {
	t.Run("reads entries and skips blanks", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "list.txt")
		data := "23_series/23.501/23501-k10.zip\n" +
			"\n" +
			"  29_series/29.510/29510-k10.zip  \n" +
			"\t\n" +
			"36_series/36.133/36133-j40.zip\n"
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		entries, err := LoadSpecList(path)
		if err != nil {
			t.Fatalf("LoadSpecList: %v", err)
		}
		want := []string{
			"23_series/23.501/23501-k10.zip",
			"29_series/29.510/29510-k10.zip",
			"36_series/36.133/36133-j40.zip",
		}
		if len(entries) != len(want) {
			t.Fatalf("got %d entries, want %d: %v", len(entries), len(want), entries)
		}
		for i, e := range entries {
			if e != want[i] {
				t.Errorf("entries[%d] = %q, want %q", i, e, want[i])
			}
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		_, err := LoadSpecList(filepath.Join(t.TempDir(), "nope.txt"))
		if err == nil {
			t.Error("expected error for missing file")
		}
	})
}

// TestFetchSpecZips exercises the single-spec zip listing endpoint against a
// mock 3GPP archive, covering both the dotted and undotted specID shapes.
func TestFetchSpecZips(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23501-k10.zip">23501-k10.zip</a>`+"\n"+
			`<a href="23501-j60.zip">23501-j60.zip</a>`+"\n"+
			`<a href="README.txt">README.txt</a>`+"\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := &http.Client{
		Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL},
	}

	t.Run("dotted specID", func(t *testing.T) {
		entries, err := FetchSpecZips(context.Background(), client, "23.501", false)
		if err != nil {
			t.Fatalf("FetchSpecZips: %v", err)
		}
		if len(entries) != 2 {
			t.Fatalf("got %d zips, want 2: %v", len(entries), entries)
		}
		for _, e := range entries {
			if !strings.HasPrefix(e, "23_series/23.501/") {
				t.Errorf("entry missing series/spec prefix: %q", e)
			}
		}
	})

	t.Run("undotted specID normalized", func(t *testing.T) {
		entries, err := FetchSpecZips(context.Background(), client, "23501", false)
		if err != nil {
			t.Fatalf("FetchSpecZips: %v", err)
		}
		if len(entries) != 2 {
			t.Errorf("expected 2 entries, got %d", len(entries))
		}
	})

	t.Run("invalid specID", func(t *testing.T) {
		_, err := FetchSpecZips(context.Background(), client, "bogus", false)
		if err == nil {
			t.Error("expected error for malformed spec ID")
		}
	})

	t.Run("path traversal specID rejected", func(t *testing.T) {
		// The normalized ID becomes a cache filename and a URL path segment,
		// so traversal sequences must never pass validation.
		for _, id := range []string{
			"../../../../etc/passwd",
			"23.501/../../secret",
			"..\\..\\config",
			"23.%",
		} {
			if _, err := FetchSpecZips(context.Background(), client, id, true); err == nil {
				t.Errorf("expected error for spec ID %q", id)
			}
		}
	})
}

// TestFetchSpecZips_EmptyListingNotCached verifies that a directory listing
// which yields no .zip at all is never written to the cache. An empty cache
// file is a hit rather than a miss (see TestLoadCache_EmptyResultIsCacheHit),
// so caching one unusable response -- an error page served as 200, a redirect,
// a truncated body -- would pin the spec to "no versions available" for the
// full 24h TTL with no way to re-fetch.
func TestFetchSpecZips_EmptyListingNotCached(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	var zipsServed bool
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/", func(w http.ResponseWriter, r *http.Request) {
		if !zipsServed {
			// First response looks fine to HTTP but lists no archive.
			fmt.Fprint(w, `<a href="index.html">index.html</a>`+"\n")
			return
		}
		fmt.Fprint(w, `<a href="23501-k10.zip">23501-k10.zip</a>`+"\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := &http.Client{
		Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL},
	}

	entries, err := FetchSpecZips(context.Background(), client, "23.501", true)
	if err != nil {
		t.Fatalf("FetchSpecZips: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %d entries, want 0: %v", len(entries), entries)
	}

	// The next call must reach the server again instead of replaying the
	// empty result from cache.
	zipsServed = true
	entries, err = FetchSpecZips(context.Background(), client, "23.501", true)
	if err != nil {
		t.Fatalf("FetchSpecZips (retry): %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("got %d entries, want 1: the empty listing must not have been cached", len(entries))
	}
}

// TestFetchSpecList_Race exercises FetchSpecList with a mock 3GPP directory
// structure to detect race conditions in the two-phase goroutine fan-out.
func TestFetchSpecList_Race(t *testing.T) {
	series := []string{"21", "23", "29"}
	specsPerSeries := 3
	zipsPerSpec := 2

	// baseURL is "https://www.3gpp.org/ftp/Specs/archive/" so after redirect
	// all requests arrive at the test server with path /ftp/Specs/archive/...
	archivePath := "/ftp/Specs/archive/"

	mux := http.NewServeMux()

	// Root: series directory listing.
	var rootHTML string
	for _, s := range series {
		rootHTML += fmt.Sprintf(`<a href="%s_series/">%s_series</a>`+"\n", s, s)
	}
	mux.HandleFunc(archivePath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != archivePath {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, rootHTML)
	})

	// Series dirs: spec directory listings.
	for _, s := range series {
		s := s
		var seriesHTML string
		for j := 1; j <= specsPerSeries; j++ {
			specDir := fmt.Sprintf("%s.%03d", s, j)
			seriesHTML += fmt.Sprintf(`<a href="%s/">%s</a>`+"\n", specDir, specDir)
		}
		mux.HandleFunc(archivePath+s+"_series/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, seriesHTML)
		})

		// Spec dirs: zip file listings.
		for j := 1; j <= specsPerSeries; j++ {
			var specHTML string
			specNum := fmt.Sprintf("%s%03d", s, j)
			for k := 1; k <= zipsPerSpec; k++ {
				letter := string(rune('a' + k - 1))
				zipName := fmt.Sprintf("%s-%s%02d.zip", specNum, letter, k*10)
				specHTML += fmt.Sprintf(`<a href="%s">%s</a>`+"\n", zipName, zipName)
			}
			specDir := fmt.Sprintf("%s.%03d", s, j)
			path := archivePath + s + "_series/" + specDir + "/"
			html := specHTML
			mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
				fmt.Fprint(w, html)
			})
		}
	}

	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := &http.Client{
		Transport: &redirectTransport{
			base:    http.DefaultTransport,
			testURL: ts.URL,
		},
	}

	entries, err := FetchSpecList(context.Background(), client, nil, false, 0)
	if err != nil {
		t.Fatalf("FetchSpecList: %v", err)
	}

	expected := len(series) * specsPerSeries * zipsPerSpec
	if len(entries) != expected {
		t.Errorf("expected %d entries, got %d", expected, len(entries))
	}
}

// TestFetchSpecList_PartialNotCached verifies that a spec list assembled
// while some directory listings failed to fetch is not written to the cache —
// every build within the TTL would otherwise silently miss those specs — and
// that the caller is told the list is incomplete instead of seeing a success:
// a build from a silently partial list produces an incomplete database.
func TestFetchSpecList_PartialNotCached(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("THREEGPP_LISTING_RETRY_MS", "0")

	archivePath := "/ftp/Specs/archive/"
	mux := http.NewServeMux()
	mux.HandleFunc(archivePath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != archivePath {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<a href="23_series/">23_series</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"23_series/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23.001/">23.001</a>`+"\n"+`<a href="23.002/">23.002</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"23_series/23.001/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23001-a00.zip">23001-a00.zip</a>`+"\n")
	})
	// 23.002 fails: its listing is missing from the assembled list.
	mux.HandleFunc(archivePath+"23_series/23.002/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	entries, err := FetchSpecList(context.Background(), client, nil, true, 2)
	var partial *PartialSpecListError
	if !errors.As(err, &partial) {
		t.Fatalf("FetchSpecList err = %v, want *PartialSpecListError", err)
	}
	if partial.FailedListings != 1 {
		t.Errorf("FailedListings = %d, want 1", partial.FailedListings)
	}
	if msg := partial.Error(); !strings.Contains(msg, "1 directory listing(s)") {
		t.Errorf("Error() = %q, want it to name the failed listing count", msg)
	}
	// The healthy entries still come back so a caller may choose to proceed.
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry from the healthy spec, got %d", len(entries))
	}

	cachePath := filepath.Join(cacheHome, "3gpp-mcp", CacheKey("speclist"))
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("partial spec list must not be cached; stat err = %v", err)
	}
}

// TestFetchSpecList_CompleteListIsCached verifies that a scrape with no
// failed listings still saves its result to the cache: only a partial list is
// barred from it.
func TestFetchSpecList_CompleteListIsCached(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	archivePath := "/ftp/Specs/archive/"
	mux := http.NewServeMux()
	mux.HandleFunc(archivePath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != archivePath {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<a href="23_series/">23_series</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"23_series/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23.001/">23.001</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"23_series/23.001/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23001-a00.zip">23001-a00.zip</a>`+"\n")
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	entries, err := FetchSpecList(context.Background(), client, nil, true, 2)
	if err != nil {
		t.Fatalf("FetchSpecList: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}

	cachePath := filepath.Join(cacheHome, "3gpp-mcp", CacheKey("speclist"))
	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("complete spec list should be cached; stat err = %v", err)
	}
}

// TestFetchSpecList_CancelReturnsContextError verifies that a scrape aborted
// by context cancellation reports ctx.Err() instead of funnelling the failed
// fetches into a partial-list result.
func TestFetchSpecList_CancelReturnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	archivePath := "/ftp/Specs/archive/"
	mux := http.NewServeMux()
	mux.HandleFunc(archivePath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != archivePath {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<a href="23_series/">23_series</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"23_series/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23.001/">23.001</a>`+"\n")
	})
	// The zip listing cancels the scrape mid-flight, as Ctrl-C would.
	mux.HandleFunc(archivePath+"23_series/23.001/", func(w http.ResponseWriter, r *http.Request) {
		cancel()
		http.Error(w, "shutting down", http.StatusInternalServerError)
	})

	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	if _, err := FetchSpecList(ctx, client, nil, false, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("FetchSpecList err = %v, want context.Canceled", err)
	}
}

// issueArchive serves a mock archive shaped like the TS 37.571-5 incident
// (issue #206): one healthy spec directory and one whose listing the given
// handler controls.
func issueArchive(t *testing.T, spec5 http.HandlerFunc) *httptest.Server {
	t.Helper()
	archivePath := "/ftp/Specs/archive/"
	mux := http.NewServeMux()
	mux.HandleFunc(archivePath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != archivePath {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<a href="37_series/">37_series</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"37_series/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="37.571-1/">37.571-1</a>`+"\n"+`<a href="37.571-5/">37.571-5</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"37_series/37.571-1/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="37571-1-j10.zip">37571-1-j10.zip</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"37_series/37.571-5/", spec5)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// TestFetchSpecList_TruncatedListingIsFailure reproduces issue #206: a spec
// directory listing that lost its tail mid-transfer keeps only the
// name-ascending first zip — the spec's oldest version. Such a body must be
// treated like a failed fetch, not accepted as a complete one-version listing
// and cached for the TTL.
func TestFetchSpecList_TruncatedListingIsFailure(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("THREEGPP_LISTING_RETRY_MS", "0")

	ts := issueArchive(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><table><a href="37571-5-100.zip">37571-5-100.zip</a>`)
	})
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	entries, err := FetchSpecList(context.Background(), client, nil, true, 2)
	var partial *PartialSpecListError
	if !errors.As(err, &partial) {
		t.Fatalf("FetchSpecList err = %v, want *PartialSpecListError", err)
	}
	if partial.FailedListings != 1 {
		t.Errorf("FailedListings = %d, want 1", partial.FailedListings)
	}
	for _, e := range entries {
		if strings.Contains(e, "37571-5-100.zip") {
			t.Errorf("truncated listing leaked its oldest version into the result: %v", entries)
		}
	}
	cachePath := filepath.Join(cacheHome, "3gpp-mcp", CacheKey("speclist"))
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("partial spec list must not be cached; stat err = %v", err)
	}
}

// TestFetchSpecList_TruncatedListingRetrySucceeds verifies that a transient
// truncation is healed by the retry: the second, complete listing is used, the
// list is cached, and latest-only selection picks the newest version across
// the legacy digit and letter token eras.
func TestFetchSpecList_TruncatedListingRetrySucceeds(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("THREEGPP_LISTING_RETRY_MS", "0")

	var calls int
	ts := issueArchive(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `<html><body><table><a href="37571-5-100.zip">37571-5-100.zip</a>`)
			return
		}
		for _, v := range []string{"100", "200", "g50", "i10", "j10"} {
			fmt.Fprintf(w, `<a href="37571-5-%s.zip">37571-5-%s.zip</a>`+"\n", v, v)
		}
	})
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	entries, err := FetchSpecList(context.Background(), client, nil, true, 2)
	if err != nil {
		t.Fatalf("FetchSpecList: %v", err)
	}
	if len(entries) != 6 {
		t.Fatalf("expected 6 entries (1 + 5), got %d: %v", len(entries), entries)
	}

	var specs []*SpecVersion
	for _, e := range entries {
		if sv := ParseSpecEntry(e); sv != nil {
			specs = append(specs, sv)
		}
	}
	latest := FilterSpecs(specs, SpecFilter{LatestOnly: true})
	var got string
	for _, s := range latest {
		if s.SpecID == "37.571-5" {
			got = s.Version
		}
	}
	if got != "j10" {
		t.Errorf("latest version for 37.571-5 = %q, want %q", got, "j10")
	}

	cachePath := filepath.Join(cacheHome, "3gpp-mcp", CacheKey("speclist"))
	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("recovered complete spec list should be cached; stat err = %v", err)
	}
}

// TestFetchSpecList_ZeroZipListingIsFailure verifies that a well-formed page
// with no .zip links at all — a WAF block page or error page served as 200 —
// counts as a failed listing. Every spec directory in the archive holds at
// least one version, so "no versions" is never a real answer.
func TestFetchSpecList_ZeroZipListingIsFailure(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("THREEGPP_LISTING_RETRY_MS", "0")

	ts := issueArchive(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>This transfer is blocked.</body></html>`)
	})
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	_, err := FetchSpecList(context.Background(), client, nil, true, 2)
	var partial *PartialSpecListError
	if !errors.As(err, &partial) {
		t.Fatalf("FetchSpecList err = %v, want *PartialSpecListError", err)
	}
	cachePath := filepath.Join(cacheHome, "3gpp-mcp", CacheKey("speclist"))
	if _, err := os.Stat(cachePath); !os.IsNotExist(err) {
		t.Errorf("partial spec list must not be cached; stat err = %v", err)
	}
}

// TestFetchSpecList_ZeroZipListingRetrySucceeds verifies that a transient
// WAF block page is healed by the retry: the validation failure counts as a
// failed attempt, so the next attempt sees the real listing.
func TestFetchSpecList_ZeroZipListingRetrySucceeds(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("THREEGPP_LISTING_RETRY_MS", "0")

	var calls int
	ts := issueArchive(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			fmt.Fprint(w, `<html><body>This transfer is blocked.</body></html>`)
			return
		}
		fmt.Fprint(w, `<a href="37571-5-j10.zip">37571-5-j10.zip</a>`+"\n")
	})
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	entries, err := FetchSpecList(context.Background(), client, nil, false, 2)
	if err != nil {
		t.Fatalf("FetchSpecList: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(entries), entries)
	}
}

// TestFetchSpecList_EmptySeriesListingIsFailure verifies that a series page
// listing no spec directories is treated as a failed listing rather than a
// series that silently contributes nothing.
func TestFetchSpecList_EmptySeriesListingIsFailure(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("THREEGPP_LISTING_RETRY_MS", "0")

	archivePath := "/ftp/Specs/archive/"
	mux := http.NewServeMux()
	mux.HandleFunc(archivePath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != archivePath {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<a href="23_series/">23_series</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"23_series/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>This transfer is blocked.</body></html>`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	_, err := FetchSpecList(context.Background(), client, nil, false, 2)
	var partial *PartialSpecListError
	if !errors.As(err, &partial) {
		t.Fatalf("FetchSpecList err = %v, want *PartialSpecListError", err)
	}
}

// TestFetchSpecList_EmptyRootFails verifies that an archive root listing
// without any *_series directory is a hard error: there is nothing usable to
// scrape, partial or otherwise.
func TestFetchSpecList_EmptyRootFails(t *testing.T) {
	t.Setenv("THREEGPP_LISTING_RETRY_MS", "0")

	archivePath := "/ftp/Specs/archive/"
	mux := http.NewServeMux()
	mux.HandleFunc(archivePath, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body>This transfer is blocked.</body></html>`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	_, err := FetchSpecList(context.Background(), client, nil, false, 2)
	if err == nil {
		t.Fatal("expected error for a root listing with no series directories")
	}
	var partial *PartialSpecListError
	if errors.As(err, &partial) {
		t.Fatalf("err = %v; an unusable root is a hard error, not a partial list", err)
	}
}

// TestFetchSpecZips_TruncatedListingFails verifies the single-spec listing
// path rejects a truncated body instead of returning the few entries that
// survived the cut.
func TestFetchSpecZips_TruncatedListingFails(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)

	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/37_series/37.571-5/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<html><body><table><a href="37571-5-100.zip">37571-5-100.zip</a>`)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	if _, err := FetchSpecZips(context.Background(), client, "37.571-5", true); err == nil {
		t.Fatal("expected error for a truncated listing")
	}
	if names, _ := filepath.Glob(filepath.Join(cacheHome, "3gpp-mcp", "*")); len(names) != 0 {
		t.Errorf("nothing should be cached for a truncated listing, found %v", names)
	}
}

// TestFilterSpecs_LatestAcrossTokenEras pits legacy all-digit tokens against
// letter-era tokens through the real parser, in several input orders: the
// newest version must win regardless of how the archive listed the files.
func TestFilterSpecs_LatestAcrossTokenEras(t *testing.T) {
	entries := []string{
		"37_series/37.571-5/37571-5-100.zip",
		"37_series/37.571-5/37571-5-200.zip",
		"37_series/37.571-5/37571-5-g50.zip",
		"37_series/37.571-5/37571-5-i10.zip",
		"37_series/37.571-5/37571-5-j10.zip",
	}
	orders := [][]int{
		{0, 1, 2, 3, 4},
		{4, 3, 2, 1, 0},
		{2, 4, 0, 3, 1},
	}
	for _, order := range orders {
		var specs []*SpecVersion
		for _, i := range order {
			sv := ParseSpecEntry(entries[i])
			if sv == nil {
				t.Fatalf("ParseSpecEntry(%q) = nil", entries[i])
			}
			specs = append(specs, sv)
		}
		got := FilterSpecs(specs, SpecFilter{LatestOnly: true})
		if len(got) != 1 {
			t.Fatalf("order %v: expected 1 spec, got %d", order, len(got))
		}
		if got[0].Version != "j10" {
			t.Errorf("order %v: latest = %q, want %q", order, got[0].Version, "j10")
		}
	}
}

// TestFetchPage_BodyAtLimitFails verifies that a listing body reaching
// maxPageSize is rejected: the limit reader would silently drop the tail, so
// hitting it means the listing cannot be trusted.
func TestFetchPage_BodyAtLimitFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chunk := strings.Repeat("x", 1<<20)
		for written := 0; written <= maxPageSize; written += len(chunk) {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	defer ts.Close()

	_, err := fetchPage(context.Background(), http.DefaultClient, ts.URL)
	if err == nil || !strings.Contains(err.Error(), "refusing truncated body") {
		t.Fatalf("err = %v, want the over-limit refusal", err)
	}
}

// chromeOnly is what a real, complete archive listing looks like when the
// directory is empty: the breadcrumb and sort anchors are always present, the
// entries are simply absent. Verified against the live archive for issue #211
// (00_series/00.02 and six more); the WAF block page by contrast carries no
// anchors at all.
const chromeOnly = `<html><body>` +
	`<a href="https://www.3gpp.org/ftp/Specs/archive/">archive</a>` + "\n" +
	`<a href="?sortby=name">Name</a>` + "\n" +
	`<a href="?sortby=date">Date</a>` + "\n" +
	`</body></html>`

// TestFetchSpecList_EmptySpecDirSkipped verifies that a genuinely empty spec
// directory — a complete listing page with its navigation anchors but no zip
// entries — contributes nothing without failing the build, and that the
// resulting list still counts as complete and is cached.
func TestFetchSpecList_EmptySpecDirSkipped(t *testing.T) {
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	t.Setenv("THREEGPP_LISTING_RETRY_MS", "0")

	ts := issueArchive(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chromeOnly)
	})
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	entries, err := FetchSpecList(context.Background(), client, nil, true, 2)
	if err != nil {
		t.Fatalf("FetchSpecList: %v", err)
	}
	if len(entries) != 1 || !strings.Contains(entries[0], "37571-1-j10.zip") {
		t.Fatalf("entries = %v, want only 37.571-1's zip", entries)
	}
	cachePath := filepath.Join(cacheHome, "3gpp-mcp", CacheKey("speclist"))
	if _, err := os.Stat(cachePath); err != nil {
		t.Errorf("a list with a genuinely empty directory is complete and must be cached; stat err = %v", err)
	}
}

// TestFetchSpecList_EmptySeriesDirSkipped verifies the same for a series
// directory with no spec directories: 47_series exists in the archive and
// holds nothing.
func TestFetchSpecList_EmptySeriesDirSkipped(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("THREEGPP_LISTING_RETRY_MS", "0")

	archivePath := "/ftp/Specs/archive/"
	mux := http.NewServeMux()
	mux.HandleFunc(archivePath, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != archivePath {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<a href="23_series/">23_series</a>`+"\n"+`<a href="47_series/">47_series</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"23_series/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23.001/">23.001</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"23_series/23.001/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23001-a00.zip">23001-a00.zip</a>`+"\n")
	})
	mux.HandleFunc(archivePath+"47_series/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, chromeOnly)
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	entries, err := FetchSpecList(context.Background(), client, nil, false, 2)
	if err != nil {
		t.Fatalf("FetchSpecList: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %v, want only 23.001's zip", entries)
	}
}

// TestFetchSpecList_UppercaseZipIncluded verifies that entries with an
// uppercase extension are collected: 08_series/08.09 writes its one version
// as 0809-301.ZIP, and a case-sensitive match dropped such specs from every
// build (issue #211).
func TestFetchSpecList_UppercaseZipIncluded(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("THREEGPP_LISTING_RETRY_MS", "0")

	ts := issueArchive(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="37571-5-3J0.ZIP">37571-5-3J0.ZIP</a>`+"\n")
	})
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	entries, err := FetchSpecList(context.Background(), client, nil, false, 2)
	if err != nil {
		t.Fatalf("FetchSpecList: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %v, want 2 including the uppercase .ZIP", entries)
	}
	var upper string
	for _, e := range entries {
		if strings.Contains(e, "37571-5") {
			upper = e
		}
	}
	sv := ParseSpecEntry(upper)
	if sv == nil {
		t.Fatalf("ParseSpecEntry(%q) = nil", upper)
	}
	if sv.Version != "3j0" || sv.Release != 3 {
		t.Errorf("Version = %q Release = %d, want 3j0 / 3", sv.Version, sv.Release)
	}
	if !strings.HasSuffix(sv.URL, "37571-5-3J0.ZIP") {
		t.Errorf("URL = %q, want the original uppercase filename preserved", sv.URL)
	}
}

// TestFetchSpecZips_UppercaseZipIncluded verifies the single-spec listing
// path collects uppercase entries too.
func TestFetchSpecZips_UppercaseZipIncluded(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/08_series/08.09/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="0809-301.ZIP">0809-301.ZIP</a>`+"\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	entries, err := FetchSpecZips(context.Background(), client, "08.09", false)
	if err != nil {
		t.Fatalf("FetchSpecZips: %v", err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0], "0809-301.ZIP") {
		t.Fatalf("entries = %v, want the uppercase .ZIP entry", entries)
	}
}
