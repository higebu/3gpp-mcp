package tools

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/internal/tdoc"
	"github.com/higebu/3gpp-mcp/internal/tdocstore"
	"github.com/higebu/3gpp-mcp/internal/testutil"
)

var meetingEntries = []tdoc.Entry{
	{TDoc: "R1-2508300", Title: "Draft Agenda", Source: "RAN1 Chair", Type: "agenda", For: "Approval", AgendaItem: "2", AgendaDescription: "Approval of Agenda", Status: "revised", RevisedTo: "R1-2509000", Abstract: "Revised\nto v2"},
	{TDoc: "R1-2508303", Title: "Reply LS on 6Rx", Source: "RAN2, Qualcomm", Type: "LS in", AgendaItem: "5", AgendaDescription: "Incoming LSs", Status: "noted", Release: "Rel-19", To: "RAN4", Cc: "RAN1", ReplyTo: "R4-2511898", OriginalLS: "R2-2507743", ReplyIn: "R1-2509196"},
	{TDoc: "R1-2509526", Title: "CR on ISAC channel model", Source: "Xiaomi, AT&T", Type: "CR", AgendaItem: "8.8", AgendaDescription: "Maintenance on others", Status: "agreed", Spec: "38.901", Version: "19.1.0", CR: "0033", CRRevision: "1", CRCategory: "F", Release: "Rel-19", IsRevisionOf: "R1-2509000", RelatedWIs: "FS_Sensing_NR", ClausesAffected: "7.9.4.2"},
}

// meetingSource returns a Source whose meeting index is served by a fake
// site with one RAN1 meeting and whose TDoc list comes from entries.
func meetingSource(t *testing.T, entries []tdoc.Entry) *Source {
	t.Helper()
	d := setupTestDB(t)
	store, err := tdocstore.Open(tdocstore.Options{
		Path: filepath.Join(t.TempDir(), "tdocs.db"), LimitBytes: -1,
		ListFetcher: func(_ context.Context, m tdoc.Meeting) ([]tdoc.Entry, string, error) {
			if entries == nil {
				return nil, "", fmt.Errorf("list download failed")
			}
			return entries, tdoc.TDocListPath(m), nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
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
	src := NewSource(d)
	src.TDocs = store
	src.Client = testutil.FakeSite(t, mux)
	src.UseCache = false
	src.Budget = time.Second
	return src
}

func TestHandleListMeetings(t *testing.T) {
	src := meetingSource(t, meetingEntries)
	handler := HandleListMeetings(src)
	ctx := context.Background()

	t.Run("groups", func(t *testing.T) {
		result, _, _ := handler(ctx, nil, ListMeetingsInput{})
		text := getTextContent(result)
		if result.IsError || !strings.Contains(text, "RAN1 (R1) — https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/") || !strings.Contains(text, "SA2 (S2)") {
			t.Errorf("groups:\n%s", text)
		}
	})

	t.Run("meetings", func(t *testing.T) {
		result, _, _ := handler(ctx, nil, ListMeetingsInput{Group: "ran1"})
		text := getTextContent(result)
		for _, want := range []string{
			"[RAN1 (R1): 2 meetings, newest first; showing 1-2.",
			"R1-124 | 3GPPRAN1#124 | Gothenburg | 2026-02-09..2026-02-13 | no TDocs yet | no folder yet",
			"R1-123 | 3GPPRAN1#123 | Dallas | 2025-11-17..2025-11-21 | R1-2508300..R1-2509718 | tsg_ran/WG1_RL1/TSGR1_123",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in:\n%s", want, text)
			}
		}
		if result.IsError || strings.Contains(text, "more;") {
			t.Errorf("unexpected:\n%s", text)
		}
	})

	t.Run("paged", func(t *testing.T) {
		result, _, _ := handler(ctx, nil, ListMeetingsInput{Group: "R1", Limit: 1})
		text := getTextContent(result)
		if !strings.Contains(text, "showing 1-1") || !strings.Contains(text, "[1 more; call again with offset=1]") || strings.Contains(text, "R1-123") {
			t.Errorf("page 1:\n%s", text)
		}
		result, _, _ = handler(ctx, nil, ListMeetingsInput{Group: "R1", Limit: 1, Offset: 1})
		text = getTextContent(result)
		if !strings.Contains(text, "showing 2-2") || !strings.Contains(text, "R1-123") || strings.Contains(text, "more;") {
			t.Errorf("page 2:\n%s", text)
		}
		result, _, _ = handler(ctx, nil, ListMeetingsInput{Group: "R1", Offset: 5})
		if text := getTextContent(result); !strings.Contains(text, "none at offset 5") {
			t.Errorf("past the end:\n%s", text)
		}
	})

	t.Run("errors", func(t *testing.T) {
		result, _, _ := handler(ctx, nil, ListMeetingsInput{Group: "XX"})
		if text := getTextContent(result); !result.IsError || !strings.Contains(text, `unknown group "XX"`) {
			t.Errorf("unknown group:\n%s", text)
		}
		// The fake site serves no SA2 page.
		result, _, _ = handler(ctx, nil, ListMeetingsInput{Group: "SA2"})
		if text := getTextContent(result); !result.IsError || !strings.Contains(text, "SA2 is not available") {
			t.Errorf("unavailable index:\n%s", text)
		}
		src.TDocs = nil
		result, _, _ = handler(ctx, nil, ListMeetingsInput{Group: "R1"})
		if text := getTextContent(result); !result.IsError || !strings.Contains(text, "disabled") {
			t.Errorf("disabled:\n%s", text)
		}
	})
}

func TestHandleListTDocs(t *testing.T) {
	src := meetingSource(t, meetingEntries)
	handler := HandleListTDocs(src)
	ctx := context.Background()

	t.Run("unfiltered", func(t *testing.T) {
		result, _, _ := handler(ctx, nil, ListTDocsInput{Meeting: "RAN1#123"})
		text := getTextContent(result)
		if result.IsError {
			t.Fatalf("error: %s", text)
		}
		for _, want := range []string{
			"[Meeting: 3GPPRAN1#123 (R1-123), Dallas, 2025-11-17..2025-11-21 — https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/docs/TDoc_List_Meeting_RAN1%23123.xlsx — 3 TDocs; showing 1-3]",
			"Agenda items (documents): 2 Approval of Agenda (1); 5 Incoming LSs (1); 8.8 Maintenance on others (1)",
			"Columns: TDoc | type | status | source | agenda item | title | CR or LS details | revision chain",
			"R1-2508300 | agenda | revised | RAN1 Chair | 2 | Draft Agenda | revised to R1-2509000",
			"R1-2508303 | LS in | noted | RAN2, Qualcomm | 5 | Reply LS on 6Rx | Rel-19, to RAN4, reply to R4-2511898, original LS R2-2507743 | replied in R1-2509196",
			"R1-2509526 | CR | agreed | Xiaomi, AT&T | 8.8 | CR on ISAC channel model | 38.901 CR 0033r1 cat F v19.1.0, Rel-19 | revision of R1-2509000",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in:\n%s", want, text)
			}
		}
		if strings.Contains(text, "Abstract:") || strings.Contains(text, "more;") {
			t.Errorf("unexpected detail or paging:\n%s", text)
		}
	})

	t.Run("filtered with details", func(t *testing.T) {
		result, _, _ := handler(ctx, nil, ListTDocsInput{Meeting: "R1-123", Type: "cr", Details: true})
		text := getTextContent(result)
		for _, want := range []string{
			"3 TDocs; 1 match type cr; showing 1-1]",
			"    Work items: FS_Sensing_NR",
			"    Clauses affected: 7.9.4.2",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in:\n%s", want, text)
			}
		}
		if strings.Contains(text, "Agenda items") || strings.Contains(text, "R1-2508303") {
			t.Errorf("filtered output:\n%s", text)
		}
		result, _, _ = handler(ctx, nil, ListTDocsInput{Meeting: "R1-123", Details: true, Limit: 1})
		text = getTextContent(result)
		if !strings.Contains(text, "    Abstract: Revised to v2") || !strings.Contains(text, "    For: Approval") {
			t.Errorf("details:\n%s", text)
		}
		result, _, _ = handler(ctx, nil, ListTDocsInput{Meeting: "R1-123", Details: true, Limit: 1, Offset: 1})
		text = getTextContent(result)
		if !strings.Contains(text, "    Cc: RAN1") || !strings.Contains(text, "showing 2-2]") || !strings.Contains(text, "[1 more; call again with offset=2]") || strings.Contains(text, "Agenda items") {
			t.Errorf("details page 2:\n%s", text)
		}
	})

	t.Run("no match", func(t *testing.T) {
		result, _, _ := handler(ctx, nil, ListTDocsInput{Meeting: "R1-123", Query: "nothing"})
		text := getTextContent(result)
		if result.IsError || !strings.Contains(text, "0 match query nothing; showing none (total 0)]") {
			t.Errorf("no match:\n%s", text)
		}
	})

	t.Run("errors", func(t *testing.T) {
		for _, tt := range []struct {
			in   ListTDocsInput
			want string
		}{
			{ListTDocsInput{}, "meeting is required"},
			{ListTDocsInput{Meeting: "TSGR1_123"}, "names no group"},
			{ListTDocsInput{Meeting: "R1-999"}, `lists no meeting "R1-999"`},
			{ListTDocsInput{Meeting: "R1-124"}, "has no meeting folder yet"},
		} {
			result, _, _ := handler(ctx, nil, tt.in)
			if text := getTextContent(result); !result.IsError || !strings.Contains(text, tt.want) {
				t.Errorf("%+v: want %q, got:\n%s", tt.in, tt.want, text)
			}
		}
	})

	t.Run("download failure", func(t *testing.T) {
		src := meetingSource(t, nil)
		result, _, _ := HandleListTDocs(src)(ctx, nil, ListTDocsInput{Meeting: "R1-123"})
		if text := getTextContent(result); !result.IsError || !strings.Contains(text, "list download failed") {
			t.Errorf("download failure:\n%s", text)
		}
		src.TDocs = nil
		result, _, _ = HandleListTDocs(src)(ctx, nil, ListTDocsInput{Meeting: "R1-123"})
		if text := getTextContent(result); !result.IsError || !strings.Contains(text, "disabled") {
			t.Errorf("disabled:\n%s", text)
		}
	})

	t.Run("in progress", func(t *testing.T) {
		release := make(chan struct{})
		d := setupTestDB(t)
		store, err := tdocstore.Open(tdocstore.Options{
			Path: filepath.Join(t.TempDir(), "tdocs.db"), LimitBytes: -1,
			ListFetcher: func(_ context.Context, m tdoc.Meeting) ([]tdoc.Entry, string, error) {
				<-release
				return meetingEntries, tdoc.TDocListPath(m), nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { store.Close() })
		slow := NewSource(d)
		slow.TDocs = store
		slow.Client = src.Client
		slow.Budget = 10 * time.Millisecond
		result, _, _ := HandleListTDocs(slow)(ctx, nil, ListTDocsInput{Meeting: "R1-123"})
		if text := getTextContent(result); result.IsError || !strings.Contains(text, "The TDoc list of 3GPPRAN1#123 is being downloaded. This takes a few seconds. Call the same tool again to get the list.") {
			t.Errorf("in progress:\n%s", text)
		}
		close(release)
		slow.Budget = time.Second
		result, _, _ = HandleListTDocs(slow)(ctx, nil, ListTDocsInput{Meeting: "R1-123"})
		if text := getTextContent(result); result.IsError || !strings.Contains(text, "3 TDocs") {
			t.Errorf("after the fetch:\n%s", text)
		}
	})
}
