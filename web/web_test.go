package web

import (
	"context"
	"errors"
	"fmt"
	htmlpkg "html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/testutil"
	"github.com/higebu/3gpp-mcp/tools"
	"github.com/higebu/3gpp-mcp/versionstore"
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
			name:    "non-readable image rewrite",
			content: "![Figure](image://image3.emf?w=612&h=208)",
			specID:  "TS 23.501",
			want:    `<img src="/specs/TS%2023.501/images/image3.emf" alt="Figure" width="612" height="208">`,
		},
		{
			name:    "image with dimensions",
			content: "![diagram](image://fig1.png?w=600&h=400)",
			specID:  "TS 23.501",
			want:    `<img src="/specs/TS%2023.501/images/fig1.png" alt="diagram" width="600" height="400">`,
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
		{
			name:    "inline math preserved for katex",
			content: `subcarrier ${n}_{78}$ value`,
			specID:  "TS 38.211",
			want:    `<span class="math-inline">{n}_{78}</span>`,
		},
		{
			name:    "display math preserved for katex",
			content: `$$\frac{1}{2}$$`,
			specID:  "TS 38.211",
			want:    `<span class="math-display">\frac{1}{2}</span>`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := renderMarkdown(tt.content, tt.specID, "", nil)
			if !strings.Contains(result, tt.want) {
				t.Errorf("renderMarkdown() = %q, want to contain %q", result, tt.want)
			}
		})
	}
}

// TestRenderMarkdown_MathProtected verifies that LaTeX math survives goldmark
// conversion intact. Without protection, goldmark would strip/alter backslash
// sequences (\\ row separators, \frac) and treat & specially.
func TestRenderMarkdown_MathProtected(t *testing.T) {
	t.Run("paragraph matrix keeps backslashes", func(t *testing.T) {
		content := `$\begin{matrix} 1 & j \\ -1 & j \end{matrix}$`
		got := renderMarkdown(content, "TS 38.211", "", nil)
		want := `<span class="math-inline">\begin{matrix} 1 &amp; j \\ -1 &amp; j \end{matrix}</span>`
		if !strings.Contains(got, want) {
			t.Errorf("math not protected, got:\n%s", got)
		}
	})

	t.Run("pre-escaped table-cell math normalizes ampersand", func(t *testing.T) {
		// Table HTML from the docx converter has already HTML-escaped & → &amp;.
		content := `<table><tbody><tr><td>$1 &amp; 2$</td></tr></tbody></table>`
		got := renderMarkdown(content, "TS 38.211", "", nil)
		// The span's inner HTML must be single-escaped so textContent is "1 & 2".
		want := `<span class="math-inline">1 &amp; 2</span>`
		if !strings.Contains(got, want) {
			t.Errorf("table-cell math not normalized, got:\n%s", got)
		}
		if strings.Contains(got, "&amp;amp;") {
			t.Errorf("table-cell math double-escaped, got:\n%s", got)
		}
	})
}

// TestRenderMarkdown_MathSkipsCode verifies that '$' inside fenced code
// blocks and inline code spans is code, not math: no placeholder may be
// injected there, and the code must render verbatim.
func TestRenderMarkdown_MathSkipsCode(t *testing.T) {
	t.Run("fenced block keeps dollars", func(t *testing.T) {
		content := "```asn1\nprice ::= $100 and $200\n```\n"
		got := renderMarkdown(content, "TS 23.501", "", nil)
		// Chroma may tokenize around the dollars, so assert on the characters
		// rather than the contiguous string.
		if strings.Count(got, "$") != 2 {
			t.Errorf("both dollars must survive in the code block, got:\n%s", got)
		}
		if strings.Contains(got, "math-inline") {
			t.Errorf("no math span may be injected into a code block, got:\n%s", got)
		}
		// The asn1 fence must still be syntax-highlighted.
		if !strings.Contains(got, `class="chroma"`) {
			t.Errorf("expected Chroma highlighting to survive, got:\n%s", got)
		}
	})

	t.Run("inline code keeps dollars, surrounding math still works", func(t *testing.T) {
		content := "Set `$PATH` and `$HOME` first; the value $x^2$ is math."
		got := renderMarkdown(content, "TS 23.501", "", nil)
		if !strings.Contains(got, "<code>$PATH</code>") || !strings.Contains(got, "<code>$HOME</code>") {
			t.Errorf("inline code must keep its dollars, got:\n%s", got)
		}
		if !strings.Contains(got, `<span class="math-inline">x^2</span>`) {
			t.Errorf("math outside code must still be protected, got:\n%s", got)
		}
	})

	t.Run("angle brackets in code stay code", func(t *testing.T) {
		content := "```\na <SUPI> in code\n```\n\nand `<NSSAI>` inline."
		got := renderMarkdown(content, "TS 23.501", "", nil)
		if !strings.Contains(got, "&lt;SUPI&gt;") {
			t.Errorf("code block angle brackets must be goldmark-escaped, got:\n%s", got)
		}
		if !strings.Contains(got, "<code>&lt;NSSAI&gt;</code>") {
			t.Errorf("inline code angle brackets must be goldmark-escaped, got:\n%s", got)
		}
		if strings.Contains(got, "&amp;lt;") {
			t.Errorf("code content must not be double-escaped, got:\n%s", got)
		}
	})
}

// TestRenderMarkdown_PlaceholderLiteralUntouched verifies that literal text
// resembling a math placeholder is never hit by re-injection: placeholders
// are random per render, so only the exact spans protectMath extracted are
// substituted.
func TestRenderMarkdown_PlaceholderLiteralUntouched(t *testing.T) {
	content := "Literal token xxkatexmathxx0xxkatexmathxx in prose.\n\nAnd math $a+b$ after it."
	got := renderMarkdown(content, "TS 23.501", "", nil)
	if !strings.Contains(got, "xxkatexmathxx0xxkatexmathxx") {
		t.Errorf("literal placeholder-lookalike text must survive verbatim, got:\n%s", got)
	}
	if want := `<span class="math-inline">a+b</span>`; !strings.Contains(got, want) {
		t.Errorf("expected the real math span %s, got:\n%s", want, got)
	}
	if strings.Count(got, "math-inline") != 1 {
		t.Errorf("exactly one math span expected, got:\n%s", got)
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
	srv := NewServer(tools.NewSource(d))
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

func TestHandleIndexWithQueryFilter(t *testing.T) {
	ts, _ := setupTestServer(t)

	t.Run("query prefix alone", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/?q=23.5")
		if err != nil {
			t.Fatalf("GET /?q=23.5 error: %v", err)
		}
		defer resp.Body.Close()

		body := readBody(t, resp)
		if !strings.Contains(body, "TS 23.501") {
			t.Error("should contain TS 23.501")
		}
		if strings.Contains(body, "TS 29.510") || strings.Contains(body, "TS 24.229") {
			t.Error("should not contain non-matching specs")
		}
	})

	t.Run("query prefix combined with series", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/?series=23&q=23.5")
		if err != nil {
			t.Fatalf("GET /?series=23&q=23.5 error: %v", err)
		}
		defer resp.Body.Close()

		body := readBody(t, resp)
		if !strings.Contains(body, "TS 23.501") {
			t.Error("should contain TS 23.501")
		}
	})

	t.Run("index navbar search has no spec scope", func(t *testing.T) {
		resp, err := http.Get(ts.URL + "/")
		if err != nil {
			t.Fatalf("GET / error: %v", err)
		}
		defer resp.Body.Close()

		body := readBody(t, resp)
		if !strings.Contains(body, `name="spec_id" value=""`) {
			t.Errorf("expected empty navbar spec_id field, got:\n%s", body)
		}
	})
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
	if !strings.Contains(body, `name="spec_id" value="TS 23.501"`) {
		t.Errorf("expected navbar search to be pre-filled with the current spec ID, got:\n%s", body)
	}
	if !strings.Contains(body, `<span class="spec-header-version">v18.6.0 (Rel-18)</span>`) {
		t.Errorf("expected the spec header to name the spec version, got:\n%s", body)
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
	// The reader must be able to tell which version the text came from,
	// matching the "[Source: ...]" line the get_section MCP tool prepends.
	if !strings.Contains(body, "Source: TS 23.501 v18.6.0 (Rel-18)") {
		t.Errorf("expected the section to name its spec version, got:\n%s", body)
	}
}

// TestHandleSection_RawHTMLEscaped is the end-to-end check for stored XSS: a
// section whose stored Markdown carries raw HTML must reach the browser with
// that HTML escaped, while angle-bracket placeholders stay visible.
func TestHandleSection_RawHTMLEscaped(t *testing.T) {
	ts, d := setupTestServer(t)

	if err := d.UpsertSection(db.Section{
		SpecID:  "TS 23.501",
		Version: "18.6.0",
		Number:  "9",
		Title:   "Injected",
		Level:   1,
		Content: "The <SUPI> identifier and <script>alert(1)</script> here.",
	}); err != nil {
		t.Fatalf("UpsertSection: %v", err)
	}

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/sections/9")
	if err != nil {
		t.Fatalf("GET section error: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if strings.Contains(body, "<script>alert(1)</script>") {
		t.Error("raw script tag from section content must not reach the page")
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("expected the script tag as escaped text, got:\n%s", body)
	}
	if !strings.Contains(body, "&lt;SUPI&gt;") {
		t.Errorf("expected the <SUPI> placeholder to stay visible, got:\n%s", body)
	}
}

func TestHandleSection_PrevNext(t *testing.T) {
	ts, _ := setupTestServer(t)

	// TS 23.501 seed TOC in document order: 1 (Scope), 5 (Architecture),
	// 5.1 (General), 5.1.1 (Overview).
	resp, err := http.Get(ts.URL + "/specs/TS 23.501/sections/5.1")
	if err != nil {
		t.Fatalf("GET section error: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, `href="/specs/TS%2023.501/sections/5"`) {
		t.Errorf("expected prev link to section 5, got:\n%s", body)
	}
	if !strings.Contains(body, `href="/specs/TS%2023.501/sections/5.1.1"`) {
		t.Errorf("expected next link to section 5.1.1, got:\n%s", body)
	}
	if !strings.Contains(body, "section-nav-prev") || !strings.Contains(body, "section-nav-next") {
		t.Errorf("expected both prev and next nav links, got:\n%s", body)
	}
}

func TestHandleSection_PrevNextAtBoundaries(t *testing.T) {
	ts, _ := setupTestServer(t)

	// First section: no previous link.
	resp, err := http.Get(ts.URL + "/specs/TS 23.501/sections/1")
	if err != nil {
		t.Fatalf("GET section error: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	if strings.Contains(body, "section-nav-prev") {
		t.Errorf("first section should have no prev link, got:\n%s", body)
	}
	if !strings.Contains(body, "section-nav-next") {
		t.Errorf("first section should have a next link, got:\n%s", body)
	}

	// Last section: no next link.
	resp2, err := http.Get(ts.URL + "/specs/TS 23.501/sections/5.1.1")
	if err != nil {
		t.Fatalf("GET section error: %v", err)
	}
	defer resp2.Body.Close()
	body2 := readBody(t, resp2)
	if strings.Contains(body2, "section-nav-next") {
		t.Errorf("last section should have no next link, got:\n%s", body2)
	}
	if !strings.Contains(body2, "section-nav-prev") {
		t.Errorf("last section should have a prev link, got:\n%s", body2)
	}
}

func TestAdjacentSections(t *testing.T) {
	toc := []db.Section{
		{Number: "1", Title: "Scope"},
		{Number: "5", Title: "Architecture"},
		{Number: "5.1", Title: "General"},
		{Number: "5.1.1", Title: "Overview"},
	}

	prev, next := adjacentSections(toc, "5.1")
	if prev == nil || prev.Number != "5" {
		t.Errorf("prev = %v, want section 5", prev)
	}
	if next == nil || next.Number != "5.1.1" {
		t.Errorf("next = %v, want section 5.1.1", next)
	}

	prev, next = adjacentSections(toc, "1")
	if prev != nil {
		t.Errorf("prev = %v, want nil at first section", prev)
	}
	if next == nil || next.Number != "5" {
		t.Errorf("next = %v, want section 5", next)
	}

	prev, next = adjacentSections(toc, "5.1.1")
	if next != nil {
		t.Errorf("next = %v, want nil at last section", next)
	}
	if prev == nil || prev.Number != "5.1" {
		t.Errorf("prev = %v, want section 5.1", prev)
	}

	prev, next = adjacentSections(toc, "nonexistent")
	if prev != nil || next != nil {
		t.Errorf("prev, next = %v, %v, want nil, nil for unknown number", prev, next)
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
	if !strings.Contains(body, `name="q" value="architecture"`) {
		t.Errorf("expected navbar search to retain the submitted query, got:\n%s", body)
	}

	// Search scoped to a spec
	resp3, err := http.Get(ts.URL + "/search?q=architecture&spec_id=TS+23.501")
	if err != nil {
		t.Fatalf("GET /search?q=architecture&spec_id=TS+23.501 error: %v", err)
	}
	defer resp3.Body.Close()

	body3 := readBody(t, resp3)
	if !strings.Contains(body3, `name="spec_id" value="TS 23.501"`) {
		t.Errorf("expected navbar search to retain the submitted spec_id, got:\n%s", body3)
	}
}

// TestHandleSearch_SnippetEscaped verifies that raw HTML inside section
// content is escaped in search snippets (it would otherwise execute in the
// viewer's origin), while the <mark> highlight delimiters survive.
func TestHandleSearch_SnippetEscaped(t *testing.T) {
	ts, d := setupTestServer(t)

	if err := d.UpsertSection(db.Section{
		SpecID:  "TS 23.501",
		Version: "18.6.0",
		Number:  "9",
		Title:   "Injected",
		Level:   1,
		Content: `The xssprobe placeholder <img src=x onerror=alert(1)> and <SUPI> markers.`,
	}); err != nil {
		t.Fatalf("UpsertSection: %v", err)
	}

	resp, err := http.Get(ts.URL + "/search?q=xssprobe")
	if err != nil {
		t.Fatalf("GET /search error: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if strings.Contains(body, "<img src=x") {
		t.Error("raw HTML from section content must not reach the page unescaped")
	}
	if !strings.Contains(body, "&lt;img src=x onerror=alert(1)&gt;") {
		t.Errorf("expected the injected tag to be escaped, got:\n%s", body)
	}
	if !strings.Contains(body, "<mark>xssprobe</mark>") {
		t.Errorf("expected the match highlight to survive escaping, got:\n%s", body)
	}
}

// TestHandleSearch_TotalCountAndVersion verifies the results header shows the
// total match count (not the page size) and each hit names its version.
func TestHandleSearch_TotalCountAndVersion(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/search?q=architecture")
	if err != nil {
		t.Fatalf("GET /search error: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, `results-count`) {
		t.Errorf("expected a results count line, got:\n%s", body)
	}
	if !strings.Contains(body, "v18.6.0") {
		t.Errorf("expected hits to name their version, got:\n%s", body)
	}
	if !strings.Contains(body, "Rel-18") {
		t.Errorf("expected hits to name their release, got:\n%s", body)
	}
}

// TestHandleSearch_Pagination seeds more matches than one page holds and
// walks to the second page.
func TestHandleSearch_Pagination(t *testing.T) {
	ts, d := setupTestServer(t)

	for i := 0; i < 55; i++ {
		if err := d.UpsertSection(db.Section{
			SpecID:  "TS 23.501",
			Version: "18.6.0",
			Number:  fmt.Sprintf("9.%d", i),
			Title:   fmt.Sprintf("Filler %d", i),
			Level:   2,
			Content: "pagitest content",
		}); err != nil {
			t.Fatalf("UpsertSection: %v", err)
		}
	}

	resp, err := http.Get(ts.URL + "/search?q=pagitest")
	if err != nil {
		t.Fatalf("GET /search error: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, "55 results") {
		t.Errorf("expected the total count 55, got:\n%s", body)
	}
	if !strings.Contains(body, "Page 1 of 2") {
		t.Errorf("expected pagination info, got:\n%s", body)
	}
	if !strings.Contains(body, "/search?q=pagitest&page=2") {
		t.Errorf("expected a link to page 2, got:\n%s", body)
	}

	resp2, err := http.Get(ts.URL + "/search?q=pagitest&page=2")
	if err != nil {
		t.Fatalf("GET /search page 2 error: %v", err)
	}
	defer resp2.Body.Close()

	body2 := readBody(t, resp2)
	if !strings.Contains(body2, "Page 2 of 2") {
		t.Errorf("expected page 2 info, got:\n%s", body2)
	}
	if strings.Count(body2, `class="search-result"`) != 5 {
		t.Errorf("expected the 5 remaining hits on page 2, got:\n%s", body2)
	}
}

// TestHandleSearch_ErrorBanner verifies a failing search renders an error
// banner instead of silently claiming zero results.
func TestHandleSearch_ErrorBanner(t *testing.T) {
	ts, d := setupTestServer(t)

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	resp, err := http.Get(ts.URL + "/search?q=architecture")
	if err != nil {
		t.Fatalf("GET /search error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Search failed") {
		t.Errorf("expected an error banner, got:\n%s", body)
	}
	if strings.Contains(body, "0 results") {
		t.Errorf("a failed search must not claim zero results, got:\n%s", body)
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
		Version:     "18.6.0",
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

	t.Run("non-renderable format serves SVG placeholder", func(t *testing.T) {
		if err := d.UpsertImage(db.Image{
			SpecID:      "TS 23.501",
			Version:     "18.6.0",
			Name:        "fig2.emf",
			MIMEType:    "image/x-emf",
			Data:        []byte("emf-bytes"),
			LLMReadable: false,
		}); err != nil {
			t.Fatalf("seed EMF image: %v", err)
		}
		resp, err := http.Get(ts.URL + "/specs/TS 23.501/images/fig2.emf")
		if err != nil {
			t.Fatalf("GET image error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "image/svg+xml" {
			t.Errorf("Content-Type = %q, want image/svg+xml", ct)
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store (a later fetch may convert the image)", cc)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !strings.Contains(string(body), "Figure not converted") {
			t.Errorf("expected placeholder SVG, got: %s", body)
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

	for _, path := range []string{
		"/static/style.css",
		"/static/app.js",
		"/static/katex/katex.min.css",
		"/static/katex/katex.min.js",
		"/static/katex/fonts/KaTeX_Main-Regular.woff2",
	} {
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
	got := renderMarkdown(content, "TS 23.501", "", nil)
	if strings.Contains(got, `alt"onload`) {
		t.Errorf("alt text should be HTML-escaped, got:\n%s", got)
	}
	if !strings.Contains(got, `&#34;`) && !strings.Contains(got, `&quot;`) {
		t.Errorf("expected escaped quotation mark in alt text, got:\n%s", got)
	}
}

// TestRenderMarkdown_RawHTMLEscaped verifies that raw HTML in section content
// that the pipeline does not itself emit is escaped rather than rendered:
// spec content comes from third-party DOCX files, so an embedded <script>
// must reach the browser as visible text, never as markup.
func TestRenderMarkdown_RawHTMLEscaped(t *testing.T) {
	content := "Inline <b>bold</b> and <script>alert(1)</script> here."
	got := renderMarkdown(content, "TS 23.501", "", nil)
	if strings.Contains(got, "<script>") {
		t.Errorf("raw <script> must not pass through, got:\n%s", got)
	}
	if !strings.Contains(got, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("expected the script tag to survive as escaped text, got:\n%s", got)
	}
	// <b> is not in the converter's vocabulary (it emits <strong>), so it is
	// document text too.
	if !strings.Contains(got, "&lt;b&gt;bold&lt;/b&gt;") {
		t.Errorf("expected <b> to survive as escaped text, got:\n%s", got)
	}
}

// TestRenderMarkdown_AngleBracketPlaceholderVisible covers the non-malicious
// half of the same defect: 3GPP prose is full of placeholders like <SUPI>,
// which browsers silently drop when passed through as unknown raw HTML. They
// must stay visible as text.
func TestRenderMarkdown_AngleBracketPlaceholderVisible(t *testing.T) {
	content := "The identifier <SUPI> is used here."
	got := renderMarkdown(content, "TS 23.501", "", nil)
	if !strings.Contains(got, "&lt;SUPI&gt;") {
		t.Errorf("expected <SUPI> to survive as escaped text, got:\n%s", got)
	}
	if strings.Contains(got, "<SUPI>") {
		t.Errorf("<SUPI> must not reach the page as a raw tag, got:\n%s", got)
	}
}

// TestRenderMarkdown_EventHandlerStripped verifies the sanitizer half of the
// defense: a tag that is in the allowlist (img) still cannot smuggle script
// through attributes or a non-relative src.
func TestRenderMarkdown_EventHandlerStripped(t *testing.T) {
	content := `Before <img src=x onerror=alert(1)> after, and <a href="javascript:alert(2)">link</a>.`
	got := renderMarkdown(content, "TS 23.501", "", nil)
	if strings.Contains(got, "onerror") {
		t.Errorf("event handler attribute must be stripped, got:\n%s", got)
	}
	if strings.Contains(got, `src="x"`) {
		t.Errorf("non-site-relative img src must be stripped, got:\n%s", got)
	}
	if strings.Contains(got, `href="javascript:`) {
		t.Errorf("no anchor may carry a javascript: URL, got:\n%s", got)
	}
}

// TestRenderMarkdown_ExternalHrefStripped pins the anchor policy: a raw <a>
// in document text is escaped to visible text, no anchor with an external or
// protocol-relative href reaches the page, and the two link forms the
// pipeline produces — site-relative spec links and rfc-editor.org RFC links —
// survive.
func TestRenderMarkdown_ExternalHrefStripped(t *testing.T) {
	content := `<a href="https://evil.example/p">x</a> <a href="//evil.example/p">y</a> ` +
		`<a href="http://evil.example/p">z</a> ` +
		"See RFC 3748 and TS 23.501 for details."
	got := renderMarkdown(content, "TS 23.501", "", nil)
	if strings.Contains(got, `href="https://evil`) || strings.Contains(got, `href="//`) ||
		strings.Contains(got, `href="http://`) {
		t.Errorf("no anchor may carry an external href, got:\n%s", got)
	}
	if !strings.Contains(got, "&lt;a href=") {
		t.Errorf("raw anchors in document text must be escaped to visible text, got:\n%s", got)
	}
	if !strings.Contains(got, `href="https://www.rfc-editor.org/rfc/rfc3748"`) {
		t.Errorf("rfc-editor link must survive, got:\n%s", got)
	}
	if !strings.Contains(got, "/specs/TS%2023.501") {
		t.Errorf("site-relative spec link must survive, got:\n%s", got)
	}
}

// TestSanitizeHTML_HrefPolicy pins the sanitizer's own href gate,
// independent of escapeUnknownHTML: only site-relative and rfc-editor.org
// targets keep their href.
func TestSanitizeHTML_HrefPolicy(t *testing.T) {
	for _, tt := range []struct {
		in   string
		keep bool
	}{
		{`<a href="/specs/TS%2023.501">s</a>`, true},
		{`<a href="https://www.rfc-editor.org/rfc/rfc3748">r</a>`, true},
		{`<a href="#frag">f</a>`, true},
		{`<a href="https://evil.example/p">x</a>`, false},
		{`<a href="//evil.example/p">y</a>`, false},
		{`<a href="http://evil.example/p">z</a>`, false},
	} {
		got := sanitizeHTML(tt.in)
		if kept := strings.Contains(got, "href="); kept != tt.keep {
			t.Errorf("sanitizeHTML(%q) href kept = %v, want %v (got %q)", tt.in, kept, tt.keep, got)
		}
	}
}

// TestSplitInlineCode_Edges pins CommonMark edge cases in code-span
// detection: an escaped backtick cannot open a span, and a span cannot
// contain a whitespace-only blank line.
func TestSplitInlineCode_Edges(t *testing.T) {
	for _, tt := range []struct {
		name, in string
		wantCode bool
	}{
		{"escaped backtick is not an opener", `\` + "`$x$" + `\` + "`", false},
		{"double backslash before backtick still opens", `\\` + "`code`", true},
		{"whitespace-only line breaks a span", "`foo\n \nbar`", false},
		{"newline inside a span is fine", "`foo\nbar`", true},
		{"closer on the next line is a span", "`foo\n`", true},
		{"trailing spaces before the closer are fine", "`foo   `", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			segs := splitInlineCode(tt.in)
			gotCode := false
			for _, s := range segs {
				if s.code {
					gotCode = true
				}
			}
			if gotCode != tt.wantCode {
				t.Errorf("splitInlineCode(%q) code segment = %v, want %v (segments: %+v)",
					tt.in, gotCode, tt.wantCode, segs)
			}
		})
	}
}

// TestSplitCodeSegments_Edges pins fence-recognition edges: an unterminated
// fence runs to end of input, an indented or too-short marker line is not a
// fence, and a backtick run of a different length inside a span is content.
func TestSplitCodeSegments_Edges(t *testing.T) {
	for _, tt := range []struct {
		name, in     string
		wantCodeSegs int
	}{
		{"unterminated fence runs to EOF", "text\n```\ncode $x$", 1},
		{"indented marker line is not a fence", "    ```\nnot code $x$\n", 0},
		// Not a fence (marker too short), but the two-backtick runs form a
		// valid inline code span per CommonMark.
		{"two backticks are an inline span, not a fence", "``\ntext\n``\n", 1},
		{"shorter backtick run inside a span is content", "`a``b`x", 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := 0
			joined := ""
			for _, s := range splitCodeSegments(tt.in) {
				joined += s.text
				if s.code {
					got++
				}
			}
			if got != tt.wantCodeSegs {
				t.Errorf("splitCodeSegments(%q) code segments = %d, want %d", tt.in, got, tt.wantCodeSegs)
			}
			if joined != tt.in {
				t.Errorf("segments do not reproduce input: %q -> %q", tt.in, joined)
			}
		})
	}
}

// TestFallbackMathToken pins that the no-crypto/rand fallback still mints
// unique, well-formed tokens.
func TestFallbackMathToken(t *testing.T) {
	a, b := fallbackMathToken(), fallbackMathToken()
	if a == b {
		t.Errorf("fallback tokens must be unique, got %q twice", a)
	}
	if !strings.HasPrefix(a, "katexmath") {
		t.Errorf("fallback token %q must keep the katexmath prefix", a)
	}
}

// TestNewMathToken_RandFailure pins that a crypto/rand failure falls back to
// a unique token and math still renders.
func TestNewMathToken_RandFailure(t *testing.T) {
	orig := randRead
	randRead = func([]byte) (int, error) { return 0, errors.New("no entropy") }
	defer func() { randRead = orig }()

	a, b := newMathToken(), newMathToken()
	if a == b {
		t.Errorf("fallback tokens must be unique, got %q twice", a)
	}
	got := renderMarkdown("Inline $x_1$ math.", "TS 23.501", "", nil)
	if !strings.Contains(got, "katex-math") && !strings.Contains(got, "math") {
		t.Errorf("math must still render under the fallback token, got:\n%s", got)
	}
	if strings.Contains(got, "katexmath") {
		t.Errorf("no placeholder token may leak into the output, got:\n%s", got)
	}
}

// TestRenderMarkdown_UnknownLanguageFence pins the chroma fallback lexer path.
func TestRenderMarkdown_UnknownLanguageFence(t *testing.T) {
	got := renderMarkdown("```zzznotalanguage\nplain <text>\n```\n", "TS 23.501", "", nil)
	if !strings.Contains(got, "plain") {
		t.Errorf("unknown-language fence must still render its content, got:\n%s", got)
	}
	if strings.Contains(got, "<text>") {
		t.Errorf("code content must be escaped, got:\n%s", got)
	}
}

// TestRenderMarkdown_TableMarkupSurvivesSanitizer pins that the converter's
// legitimate raw HTML — merged table cells and inline images — passes the
// sanitizer intact.
func TestRenderMarkdown_TableMarkupSurvivesSanitizer(t *testing.T) {
	content := `<table><thead><tr><th colspan="2">Head</th></tr></thead>` +
		`<tbody><tr><td rowspan="2">A</td><td><img src="image://fig.png?w=200&h=100" alt="diag" width="200" height="100"></td></tr></tbody></table>`
	got := renderMarkdown(content, "TS 23.501", "", nil)
	if !strings.Contains(got, `colspan="2"`) {
		t.Errorf("expected colspan to survive, got:\n%s", got)
	}
	if !strings.Contains(got, `rowspan="2"`) {
		t.Errorf("expected rowspan to survive, got:\n%s", got)
	}
	if !strings.Contains(got, `src="/specs/TS%2023.501/images/fig.png"`) {
		t.Errorf("expected the rewritten inline image to survive, got:\n%s", got)
	}
}

// TestRenderMarkdown_SubSupPassthrough verifies that the <sub>/<sup> tags
// emitted by the docx converter for subscript/superscript runs survive
// rendering, so 3GPP notation like n_78 with a superscript note mark renders
// correctly in the web viewer.
func TestRenderMarkdown_SubSupPassthrough(t *testing.T) {
	content := "n_78<sup>1</sup> and H<sub>2</sub>O"
	got := renderMarkdown(content, "TS 23.501", "", nil)
	if !strings.Contains(got, "<sup>1</sup>") {
		t.Errorf("expected <sup> to pass through, got:\n%s", got)
	}
	if !strings.Contains(got, "<sub>2</sub>") {
		t.Errorf("expected <sub> to pass through, got:\n%s", got)
	}
}

// TestRenderMarkdown_HTMLImageRewrite verifies that <img src="image://...">
// tags embedded in raw HTML (used inside HTML tables emitted by the docx
// converter) are rewritten to a real /specs/<id>/images/<name> URL.
func TestRenderMarkdown_HTMLImageRewrite(t *testing.T) {
	content := `<table><tbody><tr><td><img src="image://fig.png?w=200&h=100" alt="diag" width="200" height="100"></td></tr></tbody></table>`
	got := renderMarkdown(content, "TS 23.501", "", nil)
	if !strings.Contains(got, `src="/specs/TS%2023.501/images/fig.png"`) {
		t.Errorf("expected image:// to be rewritten to spec-relative URL, got:\n%s", got)
	}
	if strings.Contains(got, "image://") {
		t.Errorf("expected no remaining image:// URL, got:\n%s", got)
	}
}

// TestRenderMarkdown_HTMLImageRewriteSpecialChars pins the end-to-end contract
// for image basenames carrying characters that are legal in a filename but
// meaningful in HTML. htmlImageRE captures the name straight out of the src
// attribute and passes it to url.PathEscape without decoding HTML entities, so
// the converter must not entity-encode "&" or "'". The sanitizer then
// re-serializes the attribute (HTML-escaping "&" and percent-encoding "'"),
// which a browser undoes before requesting: HTML-unescape + path-unescape of
// the served attribute has to round-trip back to the stored basename.
func TestRenderMarkdown_HTMLImageRewriteSpecialChars(t *testing.T) {
	content := `<table><tbody><tr><td><img src="image://Figure A&B's diagram.png?w=200&h=100" alt="diag"></td></tr></tbody></table>`
	got := renderMarkdown(content, "TS 23.501", "", nil)

	const want = "Figure A&B's diagram.png"
	prefix := `src="/specs/TS%2023.501/images/`
	i := strings.Index(got, prefix)
	if i < 0 {
		t.Fatalf("expected a rewritten image URL, got:\n%s", got)
	}
	rest := got[i+len(prefix):]
	encoded := rest[:strings.IndexByte(rest, '"')]
	decoded, err := url.PathUnescape(htmlpkg.UnescapeString(encoded))
	if err != nil {
		t.Fatalf("rewritten image URL is not valid percent-encoding (%q): %v", encoded, err)
	}
	if decoded != want {
		t.Errorf("image lookup key = %q, want %q (encoded form was %q)", decoded, want, encoded)
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

// TestHandleIndex_HugePage verifies that an absurd page number cannot
// overflow the offset computation; the server must still answer.
func TestHandleIndex_HugePage(t *testing.T) {
	ts, _ := setupTestServer(t)

	resp, err := http.Get(ts.URL + "/?page=400000000000000000")
	if err != nil {
		t.Fatalf("GET /?page=...: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

// --- Versioned-server scaffolding, mirroring tools/source_test.go. Promote to
// internal/testutil if a third consumer appears. ---

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

// archiveClient serves a listing for TS 23.501 covering the seeded v18.6.0
// plus two versions the database does not have.
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

// setupVersionedServer builds a web server whose Source can fetch archived
// versions through canned fetchers instead of the real archive.
func setupVersionedServer(t *testing.T, fetcher versionstore.Fetcher, imageFetcher versionstore.ImageFetcher) (*httptest.Server, *tools.Source) {
	t.Helper()
	d := testutil.SetupTestDB(t)
	store, err := versionstore.Open(versionstore.Options{
		Path:         filepath.Join(t.TempDir(), "versions.db"),
		Fetcher:      fetcher,
		ImageFetcher: imageFetcher,
		// Tests read two versions side by side; the default zero limit keeps
		// only the newest fetch, making concurrent fetches evict each other.
		LimitBytes: -1,
	})
	if err != nil {
		t.Fatalf("versionstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	src := tools.NewSource(d)
	src.Store = store
	src.Client = archiveClient(t)
	src.UseCache = false
	src.Budget = 5 * time.Second

	ts := httptest.NewServer(NewServer(src))
	t.Cleanup(ts.Close)
	return ts, src
}

func cannedFetcher(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
	return db.Spec{Title: "System architecture", Release: "19", Series: "23"},
		[]db.Section{{
			Number:  "5.1",
			Title:   "General",
			Level:   2,
			Content: "## 5.1 General\nArchived text.\n![diagram](image://arch.png?w=100&h=80)",
		}}, nil
}

func cannedImageFetcher(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
	return []db.Image{{
		Name:        "arch.png",
		MIMEType:    "image/png",
		Data:        []byte("png-bytes"),
		LLMReadable: true,
	}}, nil
}

// TestHandleSection_ArchivedVersion covers browsing a version the database
// does not hold: the content is fetched on demand, every generated link
// carries the version, and database-only features degrade gracefully.
func TestHandleSection_ArchivedVersion(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, cannedImageFetcher)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/sections/5.1?version=19.5.0")
	if err != nil {
		t.Fatalf("GET archived section: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Archived text") {
		t.Errorf("expected the fetched content, got:\n%s", body)
	}
	if !strings.Contains(body, "archived version; cross-references unavailable") {
		t.Errorf("expected the archived note, got:\n%s", body)
	}
	if !strings.Contains(body, "?version=19.5.0") {
		t.Errorf("expected TOC links to carry the version, got:\n%s", body)
	}
	if !strings.Contains(body, "/images/arch.png?version=19.5.0") {
		t.Errorf("expected image URLs to carry the version, got:\n%s", body)
	}
	if strings.Contains(body, "References from this section") {
		t.Errorf("an archived page must not show database-version references, got:\n%s", body)
	}
	if strings.Contains(body, "OpenAPI Definitions") {
		t.Errorf("an archived page must not show database-version OpenAPI links, got:\n%s", body)
	}
}

// TestHandleSection_FetchInProgress checks that a fetch outliving the budget
// answers 202 with a page that refreshes itself.
func TestHandleSection_FetchInProgress(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	ts, src := setupVersionedServer(t, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		<-release
		return cannedFetcher(ctx, sv)
	}, nil)
	src.Budget = 20 * time.Millisecond

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/sections/5.1?version=19.5.0")
	if err != nil {
		t.Fatalf("GET archived section: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `http-equiv="refresh"`) {
		t.Errorf("expected an auto-refreshing page, got:\n%s", body)
	}
	if !strings.Contains(body, "TS 23.501 v19.5.0") {
		t.Errorf("expected the page to name the version being fetched, got:\n%s", body)
	}
}

// TestHandleSection_UnknownVersion checks that a version that exists nowhere
// yields 404 naming the versions that do exist.
func TestHandleSection_UnknownVersion(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/sections/5.1?version=12.0.0")
	if err != nil {
		t.Fatalf("GET unknown version: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	body := readBody(t, resp)
	for _, want := range []string{"20.2.0", "19.5.0", "18.6.0"} {
		if !strings.Contains(body, want) {
			t.Errorf("expected the error page to list version %s, got:\n%s", want, body)
		}
	}
}

// TestHandleSection_DatabaseVersionParam checks that naming the database
// version explicitly renders the canonical page.
func TestHandleSection_DatabaseVersionParam(t *testing.T) {
	ts, _ := setupVersionedServer(t, func(context.Context, *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		t.Error("fetcher must not run for a version already in the database")
		return db.Spec{}, nil, nil
	}, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/sections/5.1?version=18.6.0")
	if err != nil {
		t.Fatalf("GET database version: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if strings.Contains(body, "?version=") {
		t.Errorf("database-version links must stay canonical, got:\n%s", body)
	}
	if strings.Contains(body, "archived version") {
		t.Errorf("the database version must not be labeled archived, got:\n%s", body)
	}
}

// TestHandleImage_ArchivedVersion covers the lazy image fetch: the first read
// downloads the version's images, later reads serve them.
func TestHandleImage_ArchivedVersion(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, cannedImageFetcher)

	// Prime the text cache so the image read resolves an archived version.
	if resp, err := http.Get(ts.URL + "/specs/TS 23.501/sections/5.1?version=19.5.0"); err == nil {
		resp.Body.Close()
	}

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/images/arch.png?version=19.5.0")
	if err != nil {
		t.Fatalf("GET archived image: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := readBody(t, resp); got != "png-bytes" {
		t.Errorf("image bytes = %q, want the fetched image", got)
	}
}

// TestHandleImage_FetchInProgress checks that a still-running image download
// answers 202 with a retry hint instead of a cacheable 404.
func TestHandleImage_FetchInProgress(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	ts, src := setupVersionedServer(t, cannedFetcher, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		<-release
		return cannedImageFetcher(ctx, sv)
	})

	// Prime the text cache with the normal budget, then shrink it so the
	// blocked image download exceeds it.
	if resp, err := http.Get(ts.URL + "/specs/TS 23.501/sections/5.1?version=19.5.0"); err == nil {
		resp.Body.Close()
	}
	src.Budget = 20 * time.Millisecond

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/images/arch.png?version=19.5.0")
	if err != nil {
		t.Fatalf("GET archived image: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("expected a Retry-After header on the in-progress answer")
	}
}

// TestHandleVersions lists all three availability classes with their badges
// and action links.
func TestHandleVersions(t *testing.T) {
	ts, src := setupVersionedServer(t, cannedFetcher, nil)

	// Cache one version so all three availability values appear.
	if _, _, err := src.GetSection(context.Background(), "TS 23.501", "19.5.0", "5.1", false); err != nil {
		t.Fatalf("prime cache: %v", err)
	}

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/versions")
	if err != nil {
		t.Fatalf("GET versions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	for _, want := range []string{
		"20.2.0", "19.5.0", "18.6.0",
		"badge-archive", "badge-cached", "badge-database",
		"?version=20.2.0",
		"/compare?old=20.2.0",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("versions page should contain %q, got:\n%s", want, body)
		}
	}
}

// TestHandleVersions_ArchiveUnreachable still lists the database version and
// warns that the archive listing failed.
func TestHandleVersions_ArchiveUnreachable(t *testing.T) {
	// archiveClient only serves the TS 23.501 listing, so any other spec's
	// archive lookup fails while its database row remains.
	ts, _ := setupVersionedServer(t, cannedFetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 24.229/versions")
	if err != nil {
		t.Fatalf("GET versions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "archive listing could not be loaded") {
		t.Errorf("expected an archive warning, got:\n%s", body)
	}
	if !strings.Contains(body, "badge-database") {
		t.Errorf("expected the database version row to survive, got:\n%s", body)
	}
}

// TestHandleVersions_UnknownSpec answers 404 for a spec that exists nowhere.
func TestHandleVersions_UnknownSpec(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 99.999/versions")
	if err != nil {
		t.Fatalf("GET versions: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestHandleCompare_Form shows the version-picker form when no old version is
// given.
func TestHandleCompare_Form(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/compare")
	if err != nil {
		t.Fatalf("GET compare form: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, `name="old"`) {
		t.Errorf("expected the compare form, got:\n%s", body)
	}
}

// TestHandleCompare_Structure renders the structural summary between an
// archived version and the database version, linking changed sections to
// their text diff.
func TestHandleCompare_Structure(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/compare?old=19.5.0")
	if err != nil {
		t.Fatalf("GET compare: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "v19.5.0 (Rel-19, archived)") {
		t.Errorf("expected the old-side label, got:\n%s", body)
	}
	if !strings.Contains(body, "Content changed") {
		t.Errorf("expected a content-changed category, got:\n%s", body)
	}
	if !strings.Contains(body, "section=5.1") {
		t.Errorf("expected changed sections to link to their diff, got:\n%s", body)
	}
	if !strings.Contains(body, "Added in") {
		t.Errorf("expected database-only sections to be listed as added, got:\n%s", body)
	}
}

// TestHandleCompare_SectionDiff renders a colored unified diff of one section.
func TestHandleCompare_SectionDiff(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/compare?old=19.5.0&section=5.1")
	if err != nil {
		t.Fatalf("GET compare section: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "diff-del") || !strings.Contains(body, "diff-add") {
		t.Errorf("expected added and removed diff lines, got:\n%s", body)
	}
	if !strings.Contains(body, "diff-hunk") {
		t.Errorf("expected a hunk header line, got:\n%s", body)
	}
	if !strings.Contains(body, "Archived text") {
		t.Errorf("expected the old side's text in the diff, got:\n%s", body)
	}
	if !strings.Contains(body, "Structural summary") {
		t.Errorf("expected a back link to the structural summary, got:\n%s", body)
	}
}

// TestHandleCompare_SameVersion answers with a notice, not an error page.
func TestHandleCompare_SameVersion(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/compare?old=18.6.0")
	if err != nil {
		t.Fatalf("GET compare same version: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "nothing to compare") {
		t.Errorf("expected a same-version notice, got:\n%s", body)
	}
}

// TestHandleCompare_FetchInProgress answers 202 with the auto-refreshing page
// while the old side downloads.
func TestHandleCompare_FetchInProgress(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	ts, src := setupVersionedServer(t, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		<-release
		return cannedFetcher(ctx, sv)
	}, nil)
	src.Budget = 20 * time.Millisecond

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/compare?old=19.5.0")
	if err != nil {
		t.Fatalf("GET compare: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, `http-equiv="refresh"`) {
		t.Errorf("expected an auto-refreshing page, got:\n%s", body)
	}
}

// TestHandleCompare_RenumberedSectionDiff checks that a section that was both
// renumbered and edited links to a diff that reads each side under its own
// number (old_section), instead of failing the old-side lookup.
func TestHandleCompare_RenumberedSectionDiff(t *testing.T) {
	// Two archived versions whose only section moved from 7 to 5.1.1 and was
	// edited on the way.
	fetcher := func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		spec := db.Spec{Title: "System architecture", Series: "23"}
		if sv.Version == "j50" { // 19.5.0
			spec.Release = "19"
			return spec, []db.Section{{
				Number:  "7",
				Title:   "Overview",
				Level:   1,
				Content: "# 7 Overview\nOld body.",
			}}, nil
		}
		spec.Release = "20"
		return spec, []db.Section{{
			Number:  "5.1.1",
			Title:   "Overview",
			Level:   3,
			Content: "## 5.1.1 Overview\nNew body.",
		}}, nil
	}
	ts, _ := setupVersionedServer(t, fetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/compare?old=19.5.0&new=20.2.0")
	if err != nil {
		t.Fatalf("GET compare: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	wantLink := "section=5.1.1&amp;old_section=7"
	if !strings.Contains(body, wantLink) {
		t.Fatalf("expected the diff link to carry the old number (%s), got:\n%s", wantLink, body)
	}

	resp2, err := http.Get(ts.URL + "/specs/TS 23.501/compare?old=19.5.0&new=20.2.0&section=5.1.1&old_section=7")
	if err != nil {
		t.Fatalf("GET renumbered diff: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}
	body2 := readBody(t, resp2)
	if strings.Contains(body2, "does not exist") {
		t.Fatalf("the old side must resolve under its old number, got:\n%s", body2)
	}
	if !strings.Contains(body2, "Old body.") || !strings.Contains(body2, "New body.") {
		t.Errorf("expected both sides in the diff, got:\n%s", body2)
	}
	if !strings.Contains(body2, "7 &rarr; 5.1.1") {
		t.Errorf("expected the header to show the renumbering, got:\n%s", body2)
	}
}

// TestHandleCompare_IdenticalSection reports identity instead of an empty diff.
func TestHandleCompare_IdenticalSection(t *testing.T) {
	fetcher := func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		return db.Spec{Title: "System architecture", Release: "19", Series: "23"},
			[]db.Section{{
				Number:  "5.1",
				Title:   "General",
				Level:   2,
				Content: "## 5.1 General\nSame body in both versions.",
			}}, nil
	}
	ts, _ := setupVersionedServer(t, fetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/compare?old=19.5.0&new=20.2.0&section=5.1")
	if err != nil {
		t.Fatalf("GET identical diff: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "is identical between") {
		t.Errorf("expected an identical notice, got:\n%s", body)
	}
	if strings.Contains(body, "diff-line") {
		t.Errorf("an identical section must not render a diff, got:\n%s", body)
	}
}

// TestHandleCompare_SectionMissingOneSide points at the structural summary
// instead of failing when the section exists in one version only.
func TestHandleCompare_SectionMissingOneSide(t *testing.T) {
	fetcher := func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		spec := db.Spec{Title: "System architecture", Series: "23"}
		sections := []db.Section{{
			Number:  "5.1",
			Title:   "General",
			Level:   2,
			Content: "## 5.1 General\nShared.",
		}}
		if sv.Version == "k20" { // 20.2.0 grew a section 9
			sections = append(sections, db.Section{
				Number:  "9",
				Title:   "Brand new",
				Level:   1,
				Content: "# 9 Brand new\nOnly here.",
			})
		}
		return spec, sections, nil
	}
	ts, _ := setupVersionedServer(t, fetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/compare?old=19.5.0&new=20.2.0&section=9")
	if err != nil {
		t.Fatalf("GET one-sided diff: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "does not exist in v19.5.0") {
		t.Errorf("expected the missing-side notice, got:\n%s", body)
	}
	if !strings.Contains(body, "Structural summary") {
		t.Errorf("expected a pointer to the structural summary, got:\n%s", body)
	}
}

// TestHandleCompare_UnknownOldVersion surfaces a single-side resolution
// failure as the usual version error page.
func TestHandleCompare_UnknownOldVersion(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/compare?old=12.0.0")
	if err != nil {
		t.Fatalf("GET compare: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
	if body := readBody(t, resp); !strings.Contains(body, "20.2.0") {
		t.Errorf("expected the error page to list archive versions, got:\n%s", body)
	}
}

// TestHandleSpec_VersionResolveError maps an unexpected resolution failure to
// a plain 500, not a fetching or not-found page.
func TestHandleSpec_VersionResolveError(t *testing.T) {
	ts, src := setupVersionedServer(t, cannedFetcher, nil)
	if err := src.DB.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	resp, err := http.Get(ts.URL + "/specs/TS 23.501?version=19.5.0")
	if err != nil {
		t.Fatalf("GET spec: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
}

// TestHandleCompare_UnknownNewVersion surfaces a new-side resolution failure
// the same way as an old-side one.
func TestHandleCompare_UnknownNewVersion(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/compare?old=19.5.0&new=12.0.0")
	if err != nil {
		t.Fatalf("GET compare: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

// TestSpecHeaderTabs verifies the Document / Versions / Compare tabs are on
// every spec-scoped page with the current page marked, and that only the
// document page carries the contents drawer.
func TestSpecHeaderTabs(t *testing.T) {
	ts, _ := setupTestServer(t)

	pages := []struct {
		path       string
		hasDrawer  bool
		activeName string
	}{
		{"/specs/TS 23.501/sections/5.1", true, ">Document</a>"},
		{"/specs/TS 23.501/versions", false, ">Versions</a>"},
		{"/specs/TS 23.501/compare", false, ">Compare</a>"},
	}
	for _, p := range pages {
		resp, err := http.Get(ts.URL + p.path)
		if err != nil {
			t.Fatalf("GET %s: %v", p.path, err)
		}
		body := readBody(t, resp)
		resp.Body.Close()

		if !strings.Contains(body, `class="spec-tabs"`) {
			t.Errorf("%s: expected the spec tabs, got:\n%s", p.path, body)
		}
		if !strings.Contains(body, `aria-current="page"`+" "+p.activeName) &&
			!strings.Contains(body, `aria-current="page" href`) {
			t.Errorf("%s: expected an active tab, got:\n%s", p.path, body)
		}
		for _, item := range []string{">Document</a>", ">Versions</a>", ">Compare</a>"} {
			if !strings.Contains(body, item) {
				t.Errorf("%s: expected tab %s, got:\n%s", p.path, item, body)
			}
		}
		if got := strings.Contains(body, `id="toc-close"`); got != p.hasDrawer {
			t.Errorf("%s: drawer close button present = %v, want %v", p.path, got, p.hasDrawer)
		}
	}
}

// TestSpecHeaderTabs_ArchivedVersionCarried keeps the tabs on the archived
// version: Document and Compare carry the version being browsed.
func TestSpecHeaderTabs_ArchivedVersionCarried(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/sections/5.1?version=19.5.0")
	if err != nil {
		t.Fatalf("GET archived section: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, `?version=19.5.0">Document</a>`) {
		t.Errorf("expected the Document tab to carry the version, got:\n%s", body)
	}
	if !strings.Contains(body, `/compare?old=19.5.0">Compare</a>`) {
		t.Errorf("expected the Compare tab to preset the version, got:\n%s", body)
	}
}

// TestSpecHeaderTabs_CompareKeepsOldVersion keeps the compared old version in
// the header tabs: Document opens it, Compare keeps it selected.
func TestSpecHeaderTabs_CompareKeepsOldVersion(t *testing.T) {
	ts, _ := setupVersionedServer(t, cannedFetcher, nil)

	resp, err := http.Get(ts.URL + "/specs/TS 23.501/compare?old=19.5.0")
	if err != nil {
		t.Fatalf("GET compare: %v", err)
	}
	defer resp.Body.Close()

	body := readBody(t, resp)
	if !strings.Contains(body, `?version=19.5.0">Document</a>`) {
		t.Errorf("expected the Document tab to open the compared version, got:\n%s", body)
	}
	if !strings.Contains(body, `/compare?old=19.5.0">Compare</a>`) {
		t.Errorf("expected the Compare tab to keep the old version, got:\n%s", body)
	}
}

// TestHandleCompare_FamilyIDSuggestsParts checks that comparing a multi-part
// family ID, which has no sections of its own on either side, names the parts
// instead of a bare "no sections found".
func TestHandleCompare_FamilyIDSuggestsParts(t *testing.T) {
	// The fetcher returns no sections, mirroring a family ID whose archive
	// entry holds nothing readable.
	ts, src := setupVersionedServer(t, func(context.Context, *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		return db.Spec{Title: "family"}, nil, nil
	}, nil)
	if err := src.DB.ExecScript(`INSERT INTO specs (id, version, version_token, title, release, series) VALUES
    ('TS 38.101-1', '18.6.0', 'i60', 'NR; UE radio transmission and reception; Part 1', '18', '38');`); err != nil {
		t.Fatalf("seed part: %v", err)
	}
	// Pre-cache both versions so resolve takes the cached fast path and the
	// archive listing (which only serves TS 23.501) is never consulted.
	for _, v := range []string{"17.0.0", "18.0.0"} {
		if err := src.Store.Ensure(context.Background(), "TS 38.101", v, &pipeline.SpecVersion{SpecID: "38.101"}, time.Minute); err != nil {
			t.Fatalf("prime cache for v%s: %v", v, err)
		}
	}

	resp, err := http.Get(ts.URL + "/specs/TS 38.101/compare?old=17.0.0&new=18.0.0&section=5.1")
	if err != nil {
		t.Fatalf("GET compare: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "has multiple parts") || !strings.Contains(body, "TS 38.101-1") {
		t.Errorf("expected the parts hint naming TS 38.101-1, got:\n%s", body)
	}
}
