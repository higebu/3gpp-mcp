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

func TestHandleImage(t *testing.T) {
	ts, d := setupTestServer(t)

	imgData := []byte("\x89PNG\r\n\x1a\nfake-png-bytes")
	if err := d.UpsertImage(db.Image{
		SpecID:      "TS 23.501",
		Name:        "fig1.png",
		MIMEType:    "image/png",
		Data:        imgData,
		LLMReadable: true,
	}); err != nil {
		t.Fatalf("seed image: %v", err)
	}

	t.Run("returns image bytes with headers", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/specs/TS 23.501/images/fig1.png")
		if err != nil {
			t.Fatalf("GET image error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
			t.Errorf("Content-Type = %q, want image/png", ct)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "public, max-age=86400" {
			t.Errorf("Cache-Control = %q, want public, max-age=86400", cc)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if string(body) != string(imgData) {
			t.Errorf("body length = %d, want %d", len(body), len(imgData))
		}
	})

	t.Run("not found", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/specs/TS 23.501/images/missing.png")
		if err != nil {
			t.Fatalf("GET error: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("not found for unknown spec", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/specs/NONEXISTENT/images/fig1.png")
		if err != nil {
			t.Fatalf("GET error: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})
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

// TestRenderMarkdown_ImageAltEscaped verifies the alt-text escaping inside the
// custom image:// rewrite path. This covers the one place where user-provided
// text flows through htmlpkg.EscapeString before reaching the template.
func TestRenderMarkdown_ImageAltEscaped(t *testing.T) {
	content := `![alt"onload=x("y")](image://fig.png?w=600&h=400)`
	got := renderMarkdown(content, "TS 23.501", nil)
	if strings.Contains(got, `alt"onload`) {
		t.Errorf("alt text should be HTML-escaped, got:\n%s", got)
	}
	if !strings.Contains(got, `&#34;`) && !strings.Contains(got, `&quot;`) {
		t.Errorf("expected escaped quotation mark in alt text, got:\n%s", got)
	}
}

// TestRenderMarkdown_FigureAltEscaped exercises the figure-syntax alt escaping.
func TestRenderMarkdown_FigureAltEscaped(t *testing.T) {
	content := `[Figure: <script>x</script> Network (fig.png, use get_image to retrieve, 100x100)]`
	got := renderMarkdown(content, "TS 23.501", nil)
	if strings.Contains(got, "<script>x</script> Network") {
		t.Errorf("figure alt text should be escaped, got:\n%s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;") {
		t.Errorf("expected escaped <script> in figure alt, got:\n%s", got)
	}
}

// TestRenderMarkdown_RawHTMLPassthrough pins down the current behaviour of the
// markdown renderer: goldmark is configured with html.WithUnsafe(), so raw
// HTML embedded in section content is passed through verbatim. 3GPP specs are
// officially published documents (not user-controlled input), so this is
// considered acceptable today, but any change that would start rendering
// user-controlled content through this path MUST first add HTML sanitization.
// This test fails if that trust assumption silently changes.
func TestRenderMarkdown_RawHTMLPassthrough(t *testing.T) {
	content := "Inline <b>bold</b> and <script>alert(1)</script> here."
	got := renderMarkdown(content, "TS 23.501", nil)
	// Pins the current unsafe behaviour. If this ever starts escaping, it
	// almost certainly means goldmark's WithUnsafe() was removed — verify the
	// change is intentional before updating this expectation.
	if !strings.Contains(got, "<b>bold</b>") {
		t.Errorf("expected raw <b> to pass through (current behaviour), got:\n%s", got)
	}
	if !strings.Contains(got, "<script>alert(1)</script>") {
		t.Errorf("expected raw <script> to pass through (current behaviour), got:\n%s", got)
	}
}

// TestHandleOpenAPI_NotFound verifies the error path when requesting a missing
// OpenAPI spec returns 404 rather than 500.
func TestHandleOpenAPI_NotFound(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/specs/TS 29.510/openapi/DoesNotExist")
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestHandleOpenAPIList_EmptySpec verifies the empty-list branch when a valid
// spec has no OpenAPI definitions registered.
func TestHandleOpenAPIList_EmptySpec(t *testing.T) {
	ts, _ := setupTestServer(t)

	// TS 23.501 seed data contains no openapi_specs rows.
	resp, err := http.Get(ts.URL + "/specs/TS 23.501/openapi")
	if err != nil {
		t.Fatalf("GET error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// TestHandleSearch_Malformed exercises the query-with-only-punctuation path,
// which the FTS5 sanitizer rewrites into a quoted token. The server must
// either return results or a clean error page, never a 500.
func TestHandleSearch_Malformed(t *testing.T) {
	ts, _ := setupTestServer(t)

	for _, q := range []string{`"`, `()`, strings.Repeat("a", 10000)} {
		resp, err := http.Get(ts.URL + "/search?q=" + urlEncode(q))
		if err != nil {
			t.Fatalf("GET error: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode >= 500 {
			t.Errorf("query %q produced HTTP %d, want < 500", q, resp.StatusCode)
		}
	}
}

func urlEncode(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r == ' ' {
			b.WriteByte('+')
			continue
		}
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			continue
		}
		_, _ = b.WriteString("%")
		const hex = "0123456789ABCDEF"
		b.WriteByte(hex[byte(r)>>4])
		b.WriteByte(hex[byte(r)&0x0F])
	}
	return b.String()
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
