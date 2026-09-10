package web

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/tdoc"
	"github.com/higebu/3gpp-mcp/internal/tdocstore"
	"github.com/higebu/3gpp-mcp/internal/testutil"
	"github.com/higebu/3gpp-mcp/internal/tools"
)

// reportPath names a document by FTP path, which resolves without the
// network; a bare TDoc number would consult the meeting listing.
const reportPath = "tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1#123_v100.zip"

func cannedTDoc(_ context.Context, doc tdoc.Document) (*tdoc.Fetched, error) {
	return &tdoc.Fetched{
		Title:    "Final Report of RAN1#123",
		MainFile: "Final_Minutes_report_RAN1#123_v100.docx",
		Files:    []string{"Final_Minutes_report_RAN1#123_v100.docx", "TDoc_List.xlsx"},
		Sections: []db.Section{
			{SpecID: doc.ID, Number: "", Title: "Preamble", Level: 1, Content: "# Preamble\n\nTitle: Final Report\n\n![Figure](image://image1.png?w=10&h=10)"},
			{SpecID: doc.ID, Number: "1", Title: "Opening", Level: 1, Content: "# 1 Opening\n\nThe chair opened the meeting, see TS 23.501 clause 5.1."},
			{SpecID: doc.ID, Number: "2", Title: "Agreements", Level: 1, Content: "# 2 Agreements\n\nAgreed <b>bold</b>."},
		},
		Images: []db.Image{
			{SpecID: doc.ID, Name: "image1.png", MIMEType: "image/png", Data: []byte("png-bytes"), LLMReadable: true},
			{SpecID: doc.ID, Name: "image2.emf", MIMEType: "image/x-emf", Data: []byte("emf"), LLMReadable: false},
		},
	}, nil
}

func setupTDocServer(t *testing.T, fetcher tdocstore.Fetcher) (*httptest.Server, *tools.Source) {
	t.Helper()
	d := testutil.SetupTestDB(t)
	store, err := tdocstore.Open(tdocstore.Options{Path: filepath.Join(t.TempDir(), "tdocs.db"), LimitBytes: -1, Fetcher: fetcher})
	if err != nil {
		t.Fatalf("tdocstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	src := tools.NewSource(d)
	src.TDocs = store
	src.UseCache = false
	src.Budget = 5 * time.Second
	ts := httptest.NewServer(NewServer(src))
	t.Cleanup(ts.Close)
	return ts, src
}

func get(t *testing.T, rawURL string) (*http.Response, string) {
	t.Helper()
	resp, err := http.Get(rawURL)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	defer resp.Body.Close()
	return resp, readBody(t, resp)
}

func TestHandleTDocLookup(t *testing.T) {
	ts, _ := setupTDocServer(t, cannedTDoc)

	resp, body := get(t, ts.URL+"/tdocs")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	for _, want := range []string{`name="id"`, "RAN1 (<code>R1</code>)", `href="/tdocs"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}

	// A lookup redirects to the document's own URL.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	r, err := client.Get(ts.URL + "/tdocs?id=r1-2509715&meeting=RAN1%23123")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/tdocs/R1-2509715?meeting=RAN1%23123" {
		t.Errorf("status = %d, location = %q", r.StatusCode, r.Header.Get("Location"))
	}
	r, err = client.Get(ts.URL + "/tdocs?id=" + url.QueryEscape("https://www.3gpp.org/ftp/"+reportPath))
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != http.StatusSeeOther || r.Header.Get("Location") != "/tdocs/"+url.PathEscape(reportPath) {
		t.Errorf("path lookup: status = %d, location = %q", r.StatusCode, r.Header.Get("Location"))
	}

	resp, body = get(t, ts.URL+"/tdocs?id=TS+23.501")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "neither a TDoc number") {
		t.Errorf("bad id: status = %d, body:\n%s", resp.StatusCode, body)
	}
}

func TestHandleTDocLookup_Disabled(t *testing.T) {
	ts, _ := setupTestServer(t)
	resp, body := get(t, ts.URL+"/tdocs?id=R1-2509715")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "disabled") {
		t.Errorf("status = %d, body:\n%s", resp.StatusCode, body)
	}
	resp, _ = get(t, ts.URL+"/tdocs/R1-2509715")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("document page without a store: status = %d, want 404", resp.StatusCode)
	}
}

func TestHandleTDoc(t *testing.T) {
	ts, _ := setupTDocServer(t, cannedTDoc)
	docURL := ts.URL + "/tdocs/" + url.PathEscape(reportPath)

	resp, body := get(t, docURL)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200:\n%s", resp.StatusCode, body)
	}
	checks := map[string]bool{
		`<h1 class="spec-header-title">` + reportPath + `</h1>`:                                                     true,
		`<p class="tdoc-title">Final Report of RAN1#123</p>`:                                                        true,
		`href="https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1%23123_v100.zip"`: true,
		"<strong>Final_Minutes_report_RAN1#123_v100.docx</strong>":                                                  true,
		"TDoc_List.xlsx":            true,
		"Preamble":                  true,
		"Title: Final Report":       true, // the first section (preamble) is shown by default
		"The chair opened":          false,
		`<span class="toc-number">`: true, // clause 1 and 2 carry numbers in the TOC
		`/tdocs/` + url.PathEscape(reportPath) + `/images/image1.png`: true,
		"3GPPRAN1#123": false,
	}
	for want, present := range checks {
		if strings.Contains(body, want) != present {
			t.Errorf("%q present = %v, want %v in:\n%s", want, !present, present, body)
		}
	}

	resp, body = get(t, docURL+"/sections/1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("section: status = %d", resp.StatusCode)
	}
	if !strings.Contains(body, "The chair opened") || strings.Contains(body, "Title: Final Report") {
		t.Errorf("section 1 page:\n%s", body)
	}
	if !strings.Contains(body, `href="/specs/TS%2023.501/sections/5.1"`) {
		t.Errorf("expected the spec reference to link into the database, got:\n%s", body)
	}
	if !strings.Contains(body, `href="/tdocs/`+url.PathEscape(reportPath)+`/sections/"`) {
		t.Errorf("expected a previous link to the preamble (empty number), got:\n%s", body)
	}

	resp, body = get(t, docURL+"/sections/")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Title: Final Report") {
		t.Errorf("preamble by empty number: status = %d:\n%s", resp.StatusCode, body)
	}

	_, body = get(t, docURL+"/sections/2")
	if !strings.Contains(body, "Agreed &lt;b&gt;bold&lt;/b&gt;.") {
		t.Errorf("raw HTML in content must be escaped, got:\n%s", body)
	}

	resp, _ = get(t, docURL+"/sections/9")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing section: status = %d, want 404", resp.StatusCode)
	}
}

// TestHandleTDoc_MeetingCarried checks that a meeting named on the request
// is carried on every in-page link, so a document that resolves only with
// its meeting stays reachable after the cache evicts it.
func TestHandleTDoc_MeetingCarried(t *testing.T) {
	ts, _ := setupTDocServer(t, cannedTDoc)
	_, body := get(t, ts.URL+"/tdocs/"+url.PathEscape(reportPath)+"/sections/1?meeting=RAN1%23123")
	for _, want := range []string{
		`/sections/2?meeting=RAN1%23123"`,
		`/sections/?meeting=RAN1%23123"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	_, body = get(t, ts.URL+"/tdocs/"+url.PathEscape(reportPath)+"/sections/?meeting=RAN1%23123")
	if !strings.Contains(body, `/images/image1.png?meeting=RAN1%23123"`) {
		t.Errorf("image URL must carry the meeting, got:\n%s", body)
	}
}

func TestHandleTDocImage(t *testing.T) {
	ts, _ := setupTDocServer(t, cannedTDoc)
	base := ts.URL + "/tdocs/" + url.PathEscape(reportPath) + "/images/"

	resp, body := get(t, base+"image1.png")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/png" || body != "png-bytes" {
		t.Errorf("status = %d, type = %s, body = %q", resp.StatusCode, resp.Header.Get("Content-Type"), body)
	}
	resp, _ = get(t, base+"image2.emf")
	if resp.StatusCode != http.StatusOK || resp.Header.Get("Content-Type") != "image/svg+xml" {
		t.Errorf("non-renderable image: status = %d, type = %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}
	resp, _ = get(t, base+"nope.png")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing image: status = %d", resp.StatusCode)
	}
}

func TestHandleTDoc_FetchInProgress(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	ts, src := setupTDocServer(t, func(ctx context.Context, doc tdoc.Document) (*tdoc.Fetched, error) {
		<-release
		return cannedTDoc(ctx, doc)
	})
	src.Budget = 20 * time.Millisecond

	resp, body := get(t, ts.URL+"/tdocs/"+url.PathEscape(reportPath))
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", resp.StatusCode)
	}
	if !strings.Contains(body, `http-equiv="refresh"`) || !strings.Contains(body, "Preparing "+reportPath+"</h1>") {
		t.Errorf("expected an auto-refreshing page naming the document, got:\n%s", body)
	}
	resp, _ = get(t, ts.URL+"/tdocs/"+url.PathEscape(reportPath)+"/images/image1.png")
	if resp.StatusCode != http.StatusAccepted || resp.Header.Get("Retry-After") == "" {
		t.Errorf("image while fetching: status = %d, Retry-After = %q", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
}

func TestHandleTDoc_Unavailable(t *testing.T) {
	ts, _ := setupTDocServer(t, func(context.Context, tdoc.Document) (*tdoc.Fetched, error) {
		return nil, errors.New("no convertible document in x.zip (files: slides.pptx)")
	})
	resp, body := get(t, ts.URL+"/tdocs/"+url.PathEscape(reportPath))
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "slides.pptx") {
		t.Errorf("status = %d, body:\n%s", resp.StatusCode, body)
	}
	// A request that is neither a number nor a path never reaches the fetcher.
	resp, _ = get(t, ts.URL+"/tdocs/TS%2023.501")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("bad id: status = %d, want 404", resp.StatusCode)
	}
}
