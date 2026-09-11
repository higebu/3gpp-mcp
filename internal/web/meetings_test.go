package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/internal/tdoc"
	"github.com/higebu/3gpp-mcp/internal/tdocstore"
	"github.com/higebu/3gpp-mcp/internal/testutil"
	"github.com/higebu/3gpp-mcp/internal/tools"
)

var meetingEntries = []tdoc.Entry{
	{TDoc: "R1-2508300", Title: "Draft Agenda", Source: "RAN1 Chair", Type: "agenda", AgendaItem: "2", AgendaDescription: "Approval of Agenda", Status: "revised", RevisedTo: "R1-2509000"},
	{TDoc: "R1-2508303", Title: "Reply LS on 6Rx", Source: "RAN2, Qualcomm", Type: "LS in", AgendaItem: "5", AgendaDescription: "Incoming LSs", Status: "noted", Release: "Rel-19", To: "RAN4"},
	{TDoc: "R1-2509526", Title: "CR on ISAC <channel> model", Source: "Xiaomi, AT&T", Type: "CR", AgendaItem: "8.8", AgendaDescription: "Maintenance on others", Status: "agreed", Spec: "38.901", CR: "0033", CRRevision: "1", CRCategory: "F", Abstract: "An abstract", IsRevisionOf: "R1-2509000"},
	{TDoc: "R1-2509527", Title: "No agenda item", Source: "Apple", Type: "other", Status: "available"},
}

// setupMeetingServer serves the web viewer over a fake 3GPP site with one
// RAN1 meeting whose TDoc list is meetingEntries (or fails when entries is
// nil). listDelay holds the list download until released.
func setupMeetingServer(t *testing.T, entries []tdoc.Entry, listDelay chan struct{}) (*httptest.Server, *tools.Source) {
	t.Helper()
	d := testutil.SetupTestDB(t)
	store, err := tdocstore.Open(tdocstore.Options{
		Path: filepath.Join(t.TempDir(), "tdocs.db"), LimitBytes: -1, Fetcher: cannedTDoc,
		ListFetcher: func(_ context.Context, m tdoc.Meeting) ([]tdoc.Entry, string, error) {
			if listDelay != nil {
				<-listDelay
			}
			if entries == nil {
				return nil, "", fmt.Errorf("list download failed")
			}
			return entries, tdoc.TDocListPath(m), nil
		},
	})
	if err != nil {
		t.Fatalf("tdocstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	mux := http.NewServeMux()
	mux.HandleFunc("/dynareport", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("code") != "Meetings-R1.htm" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, testutil.DynaReportPage(
			testutil.DynaReportRow{Code: "R1-124", Title: "3GPPRAN1#124", Town: "Gothenburg", Start: "2026-02-09", End: "2026-02-13"},
			testutil.DynaReportRow{Code: "R1-123", Title: "3GPPRAN1#123", Town: "Dallas", Start: "2025-11-17", End: "2025-11-21", Dir: "tsg_ran/WG1_RL1/TSGR1_123", First: "R1-2508300", Last: "R1-2509718"},
		))
	})
	src := tools.NewSource(d)
	src.TDocs = store
	src.Client = testutil.FakeSite(t, mux)
	src.UseCache = false
	src.Budget = 5 * time.Second
	ts := httptest.NewServer(NewServer(src))
	t.Cleanup(ts.Close)
	return ts, src
}

func TestHandleMeetingGroups(t *testing.T) {
	ts, src := setupMeetingServer(t, meetingEntries, nil)
	resp, body := get(t, ts.URL+"/meetings")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, want := range []string{"<h1>3GPP Meetings</h1>", `href="/meetings/r1"`, `href="/meetings/s2"`, ">RAN1</a>", `href="/meetings" class="nav-link"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q", want)
		}
	}
	src.TDocs = nil
	_, body = get(t, ts.URL+"/meetings")
	if !strings.Contains(body, "disabled on this server") || strings.Contains(body, `href="/meetings/r1"`) {
		t.Errorf("disabled page:\n%s", body)
	}
}

func TestHandleMeetings(t *testing.T) {
	ts, _ := setupMeetingServer(t, meetingEntries, nil)
	resp, body := get(t, ts.URL+"/meetings/R1")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	for _, want := range []string{
		"<h1>RAN1 meetings</h1>",
		"2 meetings of RAN1",
		`class="group-link active">RAN1</a>`,
		`<a href="/meetings/r1/R1-123">R1-123</a>`,
		"3GPPRAN1#123",
		"R1-2508300 – R1-2509718",
		`href="https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/"`,
		"<td>R1-124</td>", // no folder, no link
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, `class="pagination"`) {
		t.Error("pagination shown for one page")
	}
	// Paging past the end clamps to the last page.
	resp, body = get(t, ts.URL+"/meetings/r1?page=9")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "R1-123") {
		t.Errorf("clamped page: %d", resp.StatusCode)
	}
	resp, _ = get(t, ts.URL+"/meetings/xx")
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown group: status %d", resp.StatusCode)
	}
	resp, _ = get(t, ts.URL+"/meetings/s2") // the fake site has no SA2 page
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unavailable index: status %d", resp.StatusCode)
	}
}

func TestHandleMeetingsPagination(t *testing.T) {
	// A group with more meetings than one page holds.
	d := testutil.SetupTestDB(t)
	store, err := tdocstore.Open(tdocstore.Options{Path: filepath.Join(t.TempDir(), "tdocs.db"), LimitBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	rows := make([]testutil.DynaReportRow, meetingsPerPage+1)
	for i := range rows {
		rows[i] = testutil.DynaReportRow{Code: fmt.Sprintf("S2-%d", 200-i), Title: fmt.Sprintf("3GPPSA2#%d", 200-i), Start: "2025-01-01", End: "2025-01-05"}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/dynareport", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, testutil.DynaReportPage(rows...)) })
	src := tools.NewSource(d)
	src.TDocs = store
	src.Client = testutil.FakeSite(t, mux)
	src.UseCache = false
	ts := httptest.NewServer(NewServer(src))
	t.Cleanup(ts.Close)

	_, body := get(t, ts.URL+"/meetings/s2")
	if !strings.Contains(body, "Page 1 of 2") || !strings.Contains(body, `href="?page=2"`) || strings.Contains(body, "S2-150") {
		t.Errorf("page 1:\n%s", body)
	}
	_, body = get(t, ts.URL+"/meetings/s2?page=2")
	if !strings.Contains(body, "Page 2 of 2") || !strings.Contains(body, `href="?page=1"`) || !strings.Contains(body, "S2-150") || strings.Contains(body, "S2-200") {
		t.Errorf("page 2:\n%s", body)
	}
}

func TestHandleMeeting(t *testing.T) {
	ts, _ := setupMeetingServer(t, meetingEntries, nil)
	resp, body := get(t, ts.URL+"/meetings/r1/R1-123")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d:\n%s", resp.StatusCode, body)
	}
	for _, want := range []string{
		`<h1 class="spec-header-title">3GPPRAN1#123</h1>`,
		`<a href="/meetings/r1">RAN1</a>`,
		"4 TDocs from the",
		`href="https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/docs/TDoc_List_Meeting_RAN1%23123.xlsx"`,
		`<option value="8.8" >8.8 Maintenance on others (1)</option>`,
		"<option value=\"\">All</option>",
		`<option value="CR" >CR</option>`,
		`<option value="agreed" >agreed</option>`,
		`<a href="/tdocs/R1-2509526?meeting=R1-123">R1-2509526</a>`,
		"CR on ISAC &lt;channel&gt; model",
		`<p class="tdoc-abstract">An abstract</p>`,
		"<span>38.901 CR 0033r1 cat F</span>",
		`revision of <a href="/tdocs/R1-2509000?meeting=R1-123">R1-2509000</a>`,
		`revised to <a href="/tdocs/R1-2509000?meeting=R1-123">R1-2509000</a>`,
		"<span>to RAN4</span>",
		`<p class="subtitle">4 TDocs</p>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}

	if strings.Contains(body, `<option value="" >`) {
		t.Error("the documents without an agenda item are offered as an option that resets the filter")
	}

	// Filters narrow the table and are kept in the form and the paging links.
	_, body = get(t, ts.URL+"/meetings/r1/R1-123?type=CR&status=agreed&q=isac&source=xiaomi&spec=38.901&agenda_item=8")
	for _, want := range []string{
		`<p class="subtitle">1 matching agenda item 8, type CR, status agreed, source xiaomi, spec 38.901, query isac</p>`,
		`<option value="CR" selected>CR</option>`,
		`<option value="agreed" selected>agreed</option>`,
		`value="xiaomi"`,
		`value="isac"`,
		"R1-2509526",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in:\n%s", want, body)
		}
	}
	if strings.Contains(body, "R1-2508303") {
		t.Error("filtered-out row shown")
	}
	_, body = get(t, ts.URL+"/meetings/r1/R1-123?q=nothing")
	if !strings.Contains(body, "No documents match.") {
		t.Errorf("empty result:\n%s", body)
	}

	// The #-form works too; a bare folder name needs the group, which the
	// URL supplies.
	for _, p := range []string{"/meetings/r1/RAN1%23123", "/meetings/r1/TSGR1_123"} {
		if resp, _ := get(t, ts.URL+p); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", p, resp.StatusCode)
		}
	}
	for _, p := range []string{"/meetings/r1/R1-999", "/meetings/r1/R1-124", "/meetings/xx/R1-123"} {
		if resp, _ := get(t, ts.URL+p); resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s: status %d", p, resp.StatusCode)
		}
	}

	// A document page links back to its meeting.
	_, body = get(t, ts.URL+"/tdocs/R1-2509526")
	if !strings.Contains(body, `<a href="/meetings/r1/R1-123">3GPPRAN1#123 (R1-123)</a>`) {
		t.Errorf("document page without a meeting link:\n%s", body)
	}
}

func TestHandleMeetingPagination(t *testing.T) {
	entries := make([]tdoc.Entry, tdocsPerPage+1)
	for i := range entries {
		entries[i] = tdoc.Entry{TDoc: fmt.Sprintf("R1-25%05d", i), Title: "t", Type: "discussion", AgendaItem: "9"}
	}
	ts, _ := setupMeetingServer(t, entries, nil)
	_, body := get(t, ts.URL+"/meetings/r1/R1-123?type=discussion")
	if !strings.Contains(body, "Page 1 of 2") || !strings.Contains(body, `href="?page=2&amp;type=discussion"`) || strings.Contains(body, fmt.Sprintf("R1-25%05d", tdocsPerPage)) {
		t.Errorf("page 1:\n%s", body)
	}
	_, body = get(t, ts.URL+"/meetings/r1/R1-123?type=discussion&page=2")
	if !strings.Contains(body, "Page 2 of 2") || !strings.Contains(body, fmt.Sprintf("R1-25%05d", tdocsPerPage)) || strings.Contains(body, "R1-2500000<") {
		t.Errorf("page 2:\n%s", body)
	}
	// A page past the end, or one too large to multiply, clamps to the last.
	for _, p := range []string{"?page=99", "?page=92233720368547758"} {
		_, body = get(t, ts.URL+"/meetings/r1/R1-123"+p)
		if !strings.Contains(body, "Page 2 of 2") || !strings.Contains(body, fmt.Sprintf("R1-25%05d", tdocsPerPage)) {
			t.Errorf("%s:\n%s", p, body)
		}
	}
}

func TestHandleMeetingErrors(t *testing.T) {
	ts, _ := setupMeetingServer(t, nil, nil)
	resp, body := get(t, ts.URL+"/meetings/r1/R1-123")
	if resp.StatusCode != http.StatusNotFound || !strings.Contains(body, "list download failed") {
		t.Errorf("download failure: %d\n%s", resp.StatusCode, body)
	}

	release := make(chan struct{})
	ts, src := setupMeetingServer(t, meetingEntries, release)
	src.Budget = 10 * time.Millisecond
	resp, body = get(t, ts.URL+"/meetings/r1/R1-123")
	if resp.StatusCode != http.StatusAccepted || !strings.Contains(body, "Preparing the TDoc list of 3GPPRAN1#123") || !strings.Contains(body, `http-equiv="refresh"`) {
		t.Errorf("in progress: %d\n%s", resp.StatusCode, body)
	}
	close(release)
	src.Budget = 5 * time.Second
	if resp, _ := get(t, ts.URL+"/meetings/r1/R1-123"); resp.StatusCode != http.StatusOK {
		t.Errorf("after the fetch: %d", resp.StatusCode)
	}
}

func TestMeetingURL(t *testing.T) {
	for _, tt := range []struct{ group, code, want string }{
		{"RAN1", "R1-123", "/meetings/r1/R1-123"},
		{"r1", "R1-122-bis", "/meetings/r1/R1-122-bis"},
		{"", "R1-123", ""},
		{"RAN1", "", ""},
		{"nope", "R1-123", ""},
	} {
		if got := meetingURL(tt.group, tt.code); got != tt.want {
			t.Errorf("meetingURL(%q, %q) = %q, want %q", tt.group, tt.code, got, tt.want)
		}
	}
	if got := filterQuery(tdocstore.Filter{Type: "LS in", Query: "a&b"}); got != "&q=a%26b&type=LS+in" {
		t.Errorf("filterQuery = %q", got)
	}
	if filterQuery(tdocstore.Filter{}) != "" {
		t.Error("empty filter renders a query")
	}
}
