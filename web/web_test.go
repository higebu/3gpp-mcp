package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/testutil"
)

func TestRenderMarkdown(t *testing.T) {
	tests := []struct {
		name    string
		content string
		specID  string
		want    string
	}{
		{
			name:    "image rewrite",
			content: "![diagram](image://fig1.png)",
			specID:  "TS 23.501",
			want:    `/specs/TS%2023.501/images/fig1.png`,
		},
		{
			name:    "figure rewrite",
			content: "[Figure: Network (image1.png, use get_image to retrieve)]",
			specID:  "TS 23.501",
			want:    `/specs/TS%2023.501/images/image1.png`,
		},
		{
			name:    "image with dimensions",
			content: "![diagram](image://fig1.png?w=600&h=400)",
			specID:  "TS 23.501",
			want:    `<img src="/specs/TS%2023.501/images/fig1.png" alt="diagram" width="600" height="400">`,
		},
		{
			name:    "figure with dimensions",
			content: "[Figure: Network (image1.png, use get_image to retrieve, 576x432)]",
			specID:  "TS 23.501",
			want:    `width="576" height="432"`,
		},
		{
			name:    "basic markdown",
			content: "**bold** text",
			specID:  "TS 23.501",
			want:    "<strong>bold</strong>",
		},
		{
			name:    "table",
			content: "| A | B |\n|---|---|\n| 1 | 2 |",
			specID:  "TS 23.501",
			want:    "<table>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderMarkdown(tt.content, tt.specID, nil)
			if !strings.Contains(result, tt.want) {
				t.Errorf("renderMarkdown() = %q, want to contain %q", result, tt.want)
			}
		})
	}
}

func TestRefURL(t *testing.T) {
	tests := []struct {
		name string
		ref  db.Reference
		want string
	}{
		{
			name: "3GPP spec",
			ref:  db.Reference{TargetSpec: "TS 23.501", TargetSection: "5.1"},
			want: "/specs/TS%2023.501/sections/5.1",
		},
		{
			name: "3GPP spec no section",
			ref:  db.Reference{TargetSpec: "TS 29.510"},
			want: "/specs/TS%2029.510",
		},
		{
			name: "RFC",
			ref:  db.Reference{TargetSpec: "RFC 3261", TargetSection: "10.2"},
			want: "https://www.rfc-editor.org/rfc/rfc3261#section-10.2",
		},
		{
			name: "RFC no section",
			ref:  db.Reference{TargetSpec: "RFC 3327"},
			want: "https://www.rfc-editor.org/rfc/rfc3327",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := refURL(tt.ref)
			if got != tt.want {
				t.Errorf("refURL() = %q, want %q", got, tt.want)
			}
		})
	}
}

func setupTestServer(t *testing.T) (*httptest.Server, *db.DB) {
	t.Helper()
	d := testutil.SetupTestDB(t)
	srv := NewServer(d)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, d
}

func TestHandleIndex(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET / error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / status = %d, want 200", resp.StatusCode)
	}

	body := readBody(t, resp)
	if !strings.Contains(body, "TS 23.501") {
		t.Error("GET / should contain TS 23.501")
	}
	if !strings.Contains(body, "TS 29.510") {
		t.Error("GET / should contain TS 29.510")
	}
}

func TestHandleIndexWithSeriesFilter(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/?series=23")
	if err != nil {
		t.Fatalf("GET /?series=23 error: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, "TS 23.501") {
		t.Error("should contain TS 23.501")
	}
}

func TestHandleSpec(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501")
	if err != nil {
		t.Fatalf("GET /specs/TS%%2023.501 error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	body := readBody(t, resp)
	if !strings.Contains(body, "Scope") {
		t.Error("should contain TOC entry 'Scope'")
	}
}

func TestHandleSection(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/sections/5.1")
	if err != nil {
		t.Fatalf("GET section error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	body := readBody(t, resp)
	if !strings.Contains(body, "General") {
		t.Error("should contain section title 'General'")
	}
}

func TestHandleSpecNotFound(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/specs/NONEXISTENT")
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleSearch(t *testing.T) {
	ts, _ := setupTestServer(t)

	// Empty search
	resp, err := http.Get(ts.URL + "/search")
	if err != nil {
		t.Fatalf("GET /search error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /search status = %d, want 200", resp.StatusCode)
	}

	// Search with query
	resp2, err := http.Get(ts.URL + "/search?q=architecture")
	if err != nil {
		t.Fatalf("GET /search?q=architecture error: %v", err)
	}
	defer resp2.Body.Close()

	body := readBody(t, resp2)
	if !strings.Contains(body, "TS 23.501") {
		t.Error("search for 'architecture' should return TS 23.501")
	}
}

func TestHandleOpenAPIList(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/specs/TS 29.510/openapi")
	if err != nil {
		t.Fatalf("GET openapi list error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	body := readBody(t, resp)
	if !strings.Contains(body, "Nnrf_NFManagement") {
		t.Error("should list Nnrf_NFManagement API")
	}
}

func TestHandleOpenAPI(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/specs/TS 29.510/openapi/Nnrf_NFManagement")
	if err != nil {
		t.Fatalf("GET openapi error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	body := readBody(t, resp)
	// Chroma wraps tokens in spans, so check for the key parts
	if !strings.Contains(body, "openapi") || !strings.Contains(body, "3.0.0") {
		t.Error("should contain OpenAPI content")
	}
}

func TestHandleReferences(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/specs/TS 24.229/sections/5.1")
	if err != nil {
		t.Fatalf("GET section error: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, "TS 23.228") {
		t.Error("should contain reference to TS 23.228")
	}
	if !strings.Contains(body, "RFC 3261") {
		t.Error("should contain reference to RFC 3261")
	}
}

func TestStaticFiles(t *testing.T) {
	ts, _ := setupTestServer(t)

	for _, path := range []string{"/static/style.css", "/static/app.js"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s error: %v", path, err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestIsExternalRef(t *testing.T) {
	if !isExternalRef(db.Reference{TargetSpec: "RFC 3261"}) {
		t.Error("RFC should be external")
	}
	if isExternalRef(db.Reference{TargetSpec: "TS 23.501"}) {
		t.Error("TS should not be external")
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
