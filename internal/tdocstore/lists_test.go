package tdocstore

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/internal/tdoc"
)

var ran1 = func() tdoc.Group { g, _ := tdoc.GroupByCode("R1"); return g }()

func testMeeting(code string) tdoc.Meeting {
	return tdoc.Meeting{Code: code, Title: "3GPPRAN1#" + strings.TrimPrefix(code, "R1-"), Town: "Dallas", Start: "2025-11-17", End: "2025-11-21",
		Dir: "tsg_ran/WG1_RL1/TSGR1_" + strings.TrimPrefix(code, "R1-"), DocsDir: "tsg_ran/WG1_RL1/TSGR1_" + strings.TrimPrefix(code, "R1-") + "/docs"}
}

var sampleEntries = []tdoc.Entry{
	{TDoc: "R1-2508300", Title: "Draft Agenda", Source: "RAN1 Chair", Type: "agenda", AgendaItem: "2", AgendaDescription: "Approval of Agenda", Status: "revised", RevisedTo: "R1-2509000"},
	{TDoc: "R1-2508303", Title: "Reply LS on 6Rx", Source: "RAN2, Qualcomm", Type: "LS in", AgendaItem: "5", AgendaDescription: "Incoming LSs", Status: "noted", To: "RAN4", Cc: "RAN1", ReplyTo: "R4-2511898"},
	{TDoc: "R1-2508500", Title: "Positioning accuracy enhancement", Source: "Ericsson", Type: "discussion", AgendaItem: "9.1", AgendaDescription: "Positioning", Status: "noted", Abstract: "Proposals on positioning", RelatedWIs: "NR_pos_enh3"},
	{TDoc: "R1-2508501", Title: "On carrier phase positioning", Source: "Huawei, HiSilicon", Type: "discussion", AgendaItem: "9.1.1", AgendaDescription: "Carrier phase", Status: "noted"},
	{TDoc: "R1-2508502", Title: "Beam management for 9.10", Source: "Nokia", Type: "discussion", AgendaItem: "9.10", AgendaDescription: "Beam management", Status: "withdrawn"},
	{TDoc: "R1-2509526", Title: "CR on ISAC channel model", Source: "Xiaomi, AT&T", Type: "CR", AgendaItem: "8.8", AgendaDescription: "Maintenance on others", Status: "agreed", Spec: "38.901", CR: "0033", CRCategory: "F", Release: "Rel-19", IsRevisionOf: "R1-2509000"},
	{TDoc: "R1-2509527", Title: "Untitled item", Source: "Apple", Type: "other", Status: "available"},
}

func openListStore(t *testing.T, fetcher ListFetcher) *Store {
	t.Helper()
	s, err := Open(Options{Path: filepath.Join(t.TempDir(), "tdocs.db"), LimitBytes: -1, ListFetcher: fetcher})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func cannedList(entries []tdoc.Entry) ListFetcher {
	return func(_ context.Context, m tdoc.Meeting) ([]tdoc.Entry, string, error) {
		return entries, tdoc.TDocListPath(m), nil
	}
}

func TestEnsureListAndQuery(t *testing.T) {
	var fetches atomic.Int32
	s := openListStore(t, func(ctx context.Context, m tdoc.Meeting) ([]tdoc.Entry, string, error) {
		fetches.Add(1)
		return cannedList(sampleEntries)(ctx, m)
	})
	m := testMeeting("R1-123")
	ctx := context.Background()
	if err := s.EnsureList(ctx, ran1, m, time.Second); err != nil {
		t.Fatalf("EnsureList: %v", err)
	}
	if err := s.EnsureList(ctx, ran1, m, time.Second); err != nil {
		t.Fatalf("EnsureList again: %v", err)
	}
	if n := fetches.Load(); n != 1 {
		t.Errorf("fetched %d times, want 1 (fresh list re-fetched)", n)
	}

	l, err := s.GetList(m.Dir)
	if err != nil || l == nil {
		t.Fatalf("GetList: %v, %v", l, err)
	}
	if l.Group != "RAN1" || l.MeetingCode != "R1-123" || l.MeetingTitle != "3GPPRAN1#123" || l.Entries != len(sampleEntries) || l.Path != "tsg_ran/WG1_RL1/TSGR1_123/docs/TDoc_List_Meeting_RAN1#123.xlsx" {
		t.Errorf("list record = %+v", l)
	}
	if l.URL() != "https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/docs/TDoc_List_Meeting_RAN1%23123.xlsx" {
		t.Errorf("URL = %s", l.URL())
	}
	if l, err := s.GetList("nowhere"); err != nil || l != nil {
		t.Errorf("GetList(unknown) = %v, %v", l, err)
	}

	ids := func(res *ListResult) string {
		var out []string
		for _, e := range res.Entries {
			out = append(out, strings.TrimPrefix(e.TDoc, "R1-250"))
		}
		return strings.Join(out, ",")
	}
	for _, tt := range []struct {
		name string
		f    Filter
		want string
	}{
		{"all", Filter{}, "8300,8303,8500,8501,8502,9526,9527"},
		{"agenda prefix", Filter{AgendaItem: "9.1"}, "8500,8501"},
		{"agenda exact", Filter{AgendaItem: "9.10"}, "8502"},
		{"type ci", Filter{Type: "ls IN"}, "8303"},
		{"status ci", Filter{Status: "AGREED"}, "9526"},
		{"source substring ci", Filter{Source: "hisilicon"}, "8501"},
		{"source escapes like", Filter{Source: "%"}, ""},
		{"spec", Filter{Spec: "38.901"}, "9526"},
		{"query title", Filter{Query: "positioning"}, "8500,8501"},
		{"query work item", Filter{Query: "NR_pos_enh3"}, "8500"},
		{"query column", Filter{Query: "source:ericsson"}, "8500"},
		{"query hyphen", Filter{Query: "Rel-19"}, ""},
		{"combined", Filter{Type: "discussion", Status: "noted", AgendaItem: "9"}, "8500,8501"},
		{"paged", Filter{Limit: 2, Offset: 1}, "8303,8500"},
		{"past the end", Filter{Limit: 2, Offset: 10}, ""},
		{"negative offset", Filter{Limit: 1, Offset: -3}, "8300"},
	} {
		res, err := s.ListEntries(m.Dir, tt.f)
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if got := ids(res); got != tt.want {
			t.Errorf("%s: got %s, want %s", tt.name, got, tt.want)
		}
		if res.Count != len(sampleEntries) {
			t.Errorf("%s: Count = %d", tt.name, res.Count)
		}
		if tt.f.Limit == 0 && res.Total != len(res.Entries) {
			t.Errorf("%s: Total = %d, %d entries", tt.name, res.Total, len(res.Entries))
		}
	}
	res, err := s.ListEntries(m.Dir, Filter{Limit: 2, Offset: 1})
	if err != nil || res.Total != len(sampleEntries) {
		t.Errorf("paged Total = %d, %v", res.Total, err)
	}
	// Every field round-trips.
	res, _ = s.ListEntries(m.Dir, Filter{Spec: "38.901"})
	if res.Entries[0] != sampleEntries[5] {
		t.Errorf("round trip:\n got %+v\nwant %+v", res.Entries[0], sampleEntries[5])
	}
	res, _ = s.ListEntries(m.Dir, Filter{Type: "LS in"})
	if res.Entries[0] != sampleEntries[1] {
		t.Errorf("round trip:\n got %+v\nwant %+v", res.Entries[0], sampleEntries[1])
	}
	if res, err := s.ListEntries("nowhere", Filter{}); err != nil || res.Count != 0 || res.Total != 0 || len(res.Entries) != 0 {
		t.Errorf("unknown meeting: %+v, %v", res, err)
	}

	agenda, err := s.AgendaItems(m.Dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, it := range agenda {
		got = append(got, fmt.Sprintf("%s=%s/%d", it.Item, it.Description, it.Count))
	}
	want := "2=Approval of Agenda/1 5=Incoming LSs/1 8.8=Maintenance on others/1 9.1=Positioning/1 9.1.1=Carrier phase/1 9.10=Beam management/1 =/1"
	if strings.Join(got, " ") != want {
		t.Errorf("agenda items:\n got %s\nwant %s", strings.Join(got, " "), want)
	}

	types, err := s.Values(m.Dir, "type")
	if err != nil || strings.Join(types, ",") != "discussion,CR,LS in,agenda,other" {
		t.Errorf("types = %v, %v", types, err)
	}
	statuses, err := s.Values(m.Dir, "status")
	if err != nil || statuses[0] != "noted" || len(statuses) != 5 {
		t.Errorf("statuses = %v, %v", statuses, err)
	}
	if _, err := s.Values(m.Dir, "title"); err == nil {
		t.Error("Values accepted an arbitrary column")
	}
}

func TestListRefresh(t *testing.T) {
	var fetches atomic.Int32
	var fail atomic.Bool
	s := openListStore(t, func(ctx context.Context, m tdoc.Meeting) ([]tdoc.Entry, string, error) {
		fetches.Add(1)
		if fail.Load() {
			return nil, "", errors.New("site down")
		}
		return cannedList(sampleEntries[:int(fetches.Load())])(ctx, m)
	})
	m := testMeeting("R1-123")
	m.End = time.Now().AddDate(0, 0, -2).Format("2006-01-02") // recent: refreshed
	ctx := context.Background()
	if err := s.EnsureList(ctx, ran1, m, time.Second); err != nil {
		t.Fatal(err)
	}
	age := func(d time.Duration) {
		if _, err := s.conn.Exec("UPDATE tdoc_lists SET fetched_at = ? WHERE meeting_dir = ?", time.Now().Add(-d).Unix(), m.Dir); err != nil {
			t.Fatal(err)
		}
	}
	age(48 * time.Hour)
	if err := s.EnsureList(ctx, ran1, m, time.Second); err != nil {
		t.Fatal(err)
	}
	if n := fetches.Load(); n != 2 {
		t.Errorf("stale recent list fetched %d times, want 2", n)
	}
	if l, _ := s.GetList(m.Dir); l.Entries != 2 {
		t.Errorf("refreshed list has %d entries, want 2", l.Entries)
	}

	// A refresh that fails keeps serving the cached list.
	age(48 * time.Hour)
	fail.Store(true)
	if err := s.EnsureList(ctx, ran1, m, time.Second); err != nil {
		t.Errorf("failed refresh reported: %v", err)
	}
	if l, _ := s.GetList(m.Dir); l == nil || l.Entries != 2 {
		t.Errorf("cached list lost on failed refresh: %+v", l)
	}
	// A missing list reports the failure.
	if err := s.EnsureList(ctx, ran1, testMeeting("R1-124"), time.Second); err == nil || !strings.Contains(err.Error(), "site down") {
		t.Errorf("missing list: err = %v", err)
	}
	fail.Store(false)

	// An old meeting's list is final: stale by age, never re-fetched.
	m.End = "2015-02-13"
	age(48 * time.Hour)
	before := fetches.Load()
	if err := s.EnsureList(ctx, ran1, m, time.Second); err != nil {
		t.Fatal(err)
	}
	if fetches.Load() != before {
		t.Error("final list re-fetched")
	}
	// A meeting without a folder has no list to fetch.
	if err := s.EnsureList(ctx, ran1, tdoc.Meeting{Code: "R1-135"}, time.Second); !errors.Is(err, tdoc.ErrNoTDocList) {
		t.Errorf("no folder: err = %v", err)
	}
}

func TestListStale(t *testing.T) {
	now := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		age  time.Duration
		end  string
		want bool
	}{
		{time.Hour, "2026-02-20", false},      // fresh
		{48 * time.Hour, "2026-02-20", true},  // recent meeting, old fetch
		{48 * time.Hour, "", true},            // unknown end: ongoing
		{48 * time.Hour, "2026-03-10", true},  // future meeting (early uploads)
		{48 * time.Hour, "2025-12-01", false}, // long over
		{48 * time.Hour, "not a date", true},  // unparsable: ongoing
		{48 * time.Hour, "2026-01-31", true},  // 29 days after the end
		{48 * time.Hour, "2026-01-29", false}, // 31 days after the end
	} {
		if got := listStale(now.Add(-tt.age), tt.end, now); got != tt.want {
			t.Errorf("listStale(age %v, end %q) = %v, want %v", tt.age, tt.end, got, tt.want)
		}
	}
}

func TestListBudgetAndEviction(t *testing.T) {
	release := make(chan struct{})
	s := openListStore(t, func(ctx context.Context, m tdoc.Meeting) ([]tdoc.Entry, string, error) {
		if m.Code == "R1-1" {
			<-release
		}
		return cannedList(sampleEntries[:1])(ctx, m)
	})
	ctx := context.Background()
	err := s.EnsureList(ctx, ran1, testMeeting("R1-1"), 20*time.Millisecond)
	if !errors.Is(err, ErrInProgress) {
		t.Fatalf("err = %v, want ErrInProgress", err)
	}
	close(release)
	if err := s.EnsureList(ctx, ran1, testMeeting("R1-1"), time.Second); err != nil {
		t.Fatalf("join: %v", err)
	}
	for i := 2; i <= MaxLists+1; i++ {
		if err := s.EnsureList(ctx, ran1, testMeeting(fmt.Sprintf("R1-%d", i)), time.Second); err != nil {
			t.Fatal(err)
		}
	}
	// The first list, least recently used, is gone; the newest stays.
	if l, _ := s.GetList(testMeeting("R1-1").Dir); l != nil {
		t.Error("oldest list survived eviction")
	}
	if l, _ := s.GetList(testMeeting(fmt.Sprintf("R1-%d", MaxLists+1)).Dir); l == nil {
		t.Error("newest list evicted")
	}
	var n int
	if err := s.conn.QueryRow("SELECT COUNT(*) FROM tdoc_lists").Scan(&n); err != nil || n != MaxLists {
		t.Errorf("%d lists cached, want %d (%v)", n, MaxLists, err)
	}
	if err := s.conn.QueryRow("SELECT COUNT(*) FROM tdoc_entries WHERE meeting_dir = ?", testMeeting("R1-1").Dir).Scan(&n); err != nil || n != 0 {
		t.Errorf("evicted list left %d entries (%v)", n, err)
	}
}

func TestFilterString(t *testing.T) {
	if !(Filter{}).IsEmpty() || (Filter{Spec: "x"}).IsEmpty() {
		t.Error("IsEmpty")
	}
	f := Filter{AgendaItem: "9.1", Type: "CR", Status: "agreed", Source: "Nokia", Spec: "38.331", Query: "beam", Limit: 5}
	if got, want := f.String(), "agenda item 9.1, type CR, status agreed, source Nokia, spec 38.331, query beam"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if (Filter{}).String() != "" {
		t.Error("empty filter describes itself")
	}
}

func TestSortAgendaItems(t *testing.T) {
	items := []AgendaItem{{Item: "10"}, {Item: "9.10"}, {Item: "9.2"}, {Item: "9.2.1"}, {Item: "9.2a"}, {Item: "9"}, {Item: ""}, {Item: "A"}}
	sortAgendaItems(items)
	var got []string
	for _, it := range items {
		got = append(got, it.Item)
	}
	if want := "9,9.2,9.2.1,9.10,9.2a,10,,A"; strings.Join(got, ",") != want {
		t.Errorf("order = %s, want %s", strings.Join(got, ","), want)
	}
}

func TestClosedStoreListErrors(t *testing.T) {
	s := openListStore(t, cannedList(sampleEntries))
	m := testMeeting("R1-123")
	if err := s.EnsureList(context.Background(), ran1, m, time.Second); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if err := s.EnsureList(context.Background(), ran1, m, time.Second); err == nil {
		t.Error("EnsureList on a closed store succeeded")
	}
	if _, err := s.GetList(m.Dir); err == nil {
		t.Error("GetList on a closed store succeeded")
	}
	if _, err := s.ListEntries(m.Dir, Filter{}); err == nil {
		t.Error("ListEntries on a closed store succeeded")
	}
	if _, err := s.AgendaItems(m.Dir); err == nil {
		t.Error("AgendaItems on a closed store succeeded")
	}
	if _, err := s.Values(m.Dir, "type"); err == nil {
		t.Error("Values on a closed store succeeded")
	}
	if err := s.putList(ran1, m, "p", nil); err == nil {
		t.Error("putList on a closed store succeeded")
	}
	if err := s.evictLists(""); err == nil {
		t.Error("evictLists on a closed store succeeded")
	}
	if err := s.deleteList(m.Dir); err == nil {
		t.Error("deleteList on a closed store succeeded")
	}
}
