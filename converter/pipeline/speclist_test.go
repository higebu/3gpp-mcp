package pipeline

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
		{"legacy version", "23_series/23.501/23501-300.zip", "23.501", 0},
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

func TestIsNewerVersion(t *testing.T) {
	tests := []struct {
		newVer, oldVer string
		want           bool
	}{
		{"k10", "j60", true},
		{"j60", "k10", false},
		{"k10", "k10", false},
		{"k20", "k10", true},
		{"a01", "", true},
		{"", "", true}, // oldVer=="" always returns true
		{"300", "200", true},
		{"200", "300", false},
	}

	for _, tt := range tests {
		t.Run(tt.newVer+"_vs_"+tt.oldVer, func(t *testing.T) {
			got := IsNewerVersion(tt.newVer, tt.oldVer)
			if got != tt.want {
				t.Errorf("IsNewerVersion(%q, %q) = %v, want %v", tt.newVer, tt.oldVer, got, tt.want)
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
		got := FilterSpecs(specs, 19, nil, "", false)
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
	})

	t.Run("filter by series", func(t *testing.T) {
		got := FilterSpecs(specs, 0, []string{"29"}, "", false)
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
	})

	t.Run("filter by spec ID", func(t *testing.T) {
		got := FilterSpecs(specs, 0, nil, "23.501", false)
		if len(got) != 2 {
			t.Fatalf("expected 2, got %d", len(got))
		}
	})

	t.Run("latest only", func(t *testing.T) {
		got := FilterSpecs(specs, 0, nil, "", true)
		if len(got) != 2 {
			t.Fatalf("expected 2 (one per spec), got %d", len(got))
		}
		for _, s := range got {
			if s.Release != 20 {
				t.Errorf("expected latest release 20 for %s, got %d", s.SpecID, s.Release)
			}
		}
	})

	t.Run("no match", func(t *testing.T) {
		got := FilterSpecs(specs, 99, nil, "", false)
		if len(got) != 0 {
			t.Fatalf("expected 0, got %d", len(got))
		}
	})

	t.Run("nil input", func(t *testing.T) {
		got := FilterSpecs(nil, 0, nil, "", false)
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
		{&SpecVersion{VersionLetter: "k", VersionMinor: 10}, "k10"},
		{&SpecVersion{VersionMinor: 300}, "300"},
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

	entries, err := FetchSpecList(context.Background(), client, nil, false)
	if err != nil {
		t.Fatalf("FetchSpecList: %v", err)
	}

	expected := len(series) * specsPerSeries * zipsPerSpec
	if len(entries) != expected {
		t.Errorf("expected %d entries, got %d", expected, len(entries))
	}
}
