package tdocstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/tdoc"
)

func testDoc(id string) tdoc.Document {
	g, _ := tdoc.GroupByCode("R1")
	return tdoc.Document{
		ID:      id,
		Path:    "tsg_ran/WG1_RL1/TSGR1_123/docs/" + id + ".zip",
		Group:   g,
		Meeting: tdoc.Meeting{Code: "R1-123", Title: "3GPPRAN1#123", Dir: "tsg_ran/WG1_RL1/TSGR1_123"},
	}
}

func fetchedDoc(id string, body string) *tdoc.Fetched {
	return &tdoc.Fetched{
		Title:    "Title of " + id,
		MainFile: id + ".docx",
		Files:    []string{id + ".docx", "attachment.xlsx"},
		Sections: []db.Section{
			{SpecID: id, Number: "", Title: "Preamble", Level: 1, Content: "# Preamble\n\nTitle: Title of " + id},
			{SpecID: id, Number: "1", Title: "Intro", Level: 1, Content: "# 1 Intro\n\n" + body},
			{SpecID: id, Number: "1.1", Title: "Detail", Level: 2, ParentNumber: "1", Content: "## 1.1 Detail\n\nmore"},
			{SpecID: id, Number: "Agreement", Title: "Agreement", Level: 1, Content: "# Agreement\n\nfirst"},
			{SpecID: id, Number: "Agreement", Title: "Agreement", Level: 1, Content: "# Agreement\n\nsecond"},
		},
		Images: []db.Image{{SpecID: id, Name: "image1.png", MIMEType: "image/png", Data: []byte("png"), LLMReadable: true}},
	}
}

func openStore(t *testing.T, limit int64, fetcher Fetcher) *Store {
	t.Helper()
	s, err := Open(Options{Path: filepath.Join(t.TempDir(), "tdocs.db"), LimitBytes: limit, Fetcher: fetcher})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestEnsureAndRead(t *testing.T) {
	calls := 0
	s := openStore(t, DefaultLimitBytes, func(_ context.Context, d tdoc.Document) (*tdoc.Fetched, error) {
		calls++
		return fetchedDoc(d.ID, "body"), nil
	})
	doc := testDoc("R1-2509715")
	if err := s.Ensure(context.Background(), doc, time.Second); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := s.Ensure(context.Background(), doc, time.Second); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if calls != 1 {
		t.Errorf("fetcher ran %d times, want 1", calls)
	}

	rec, err := s.Get("R1-2509715")
	if err != nil || rec == nil {
		t.Fatalf("Get: %v, %v", rec, err)
	}
	if rec.Title != "Title of R1-2509715" || rec.MeetingCode != "R1-123" || rec.Group != "RAN1" || rec.MainFile != "R1-2509715.docx" || len(rec.Files) != 2 {
		t.Errorf("record = %+v", rec)
	}
	if rec.URL() != "https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509715.zip" {
		t.Errorf("URL() = %s", rec.URL())
	}
	if missing, err := s.Get("R1-0000000"); err != nil || missing != nil {
		t.Errorf("Get(missing) = %v, %v", missing, err)
	}

	toc, err := s.GetTOC("R1-2509715")
	if err != nil {
		t.Fatal(err)
	}
	var numbers []string
	for _, sec := range toc {
		numbers = append(numbers, sec.Number)
		if sec.Content != "" {
			t.Errorf("TOC carries content for %q", sec.Number)
		}
	}
	if got := strings.Join(numbers, "|"); got != "|1|1.1|Agreement|Agreement (2)" {
		t.Errorf("TOC numbers = %q", got)
	}

	all, err := s.AllSections("R1-2509715")
	if err != nil || len(all) != 5 || !strings.Contains(all[1].Content, "body") || all[1].SpecID != "R1-2509715" {
		t.Errorf("AllSections = %+v, %v", all, err)
	}
	one, err := s.GetSection("R1-2509715", "1", false)
	if err != nil || len(one) != 1 || one[0].Number != "1" {
		t.Errorf("GetSection(1) = %+v, %v", one, err)
	}
	sub, err := s.GetSection("R1-2509715", "1", true)
	if err != nil || len(sub) != 2 {
		t.Errorf("GetSection(1, subsections) = %+v, %v", sub, err)
	}
	pre, err := s.GetSection("R1-2509715", "", false)
	if err != nil || len(pre) != 1 || pre[0].Title != "Preamble" {
		t.Errorf("GetSection(preamble) = %+v, %v", pre, err)
	}
	second, err := s.GetSection("R1-2509715", "Agreement (2)", false)
	if err != nil || len(second) != 1 || !strings.Contains(second[0].Content, "second") {
		t.Errorf("GetSection(Agreement (2)) = %+v, %v", second, err)
	}

	img, err := s.GetImage("R1-2509715", "image1.png")
	if err != nil || img == nil || string(img.Data) != "png" {
		t.Errorf("GetImage = %+v, %v", img, err)
	}
	if img, err := s.GetImage("R1-2509715", "image1.emf"); err != nil || img == nil {
		t.Errorf("GetImage by base name = %+v, %v", img, err)
	}
	if img, err := s.GetImage("R1-2509715", "nope.png"); err != nil || img != nil {
		t.Errorf("GetImage(missing) = %+v, %v", img, err)
	}
	infos, err := s.ListImages("R1-2509715")
	if err != nil || len(infos) != 1 || infos[0].Name != "image1.png" {
		t.Errorf("ListImages = %+v, %v", infos, err)
	}
}

// TestDedupeNumbersAvoidsLiteralCollision covers a repeated heading whose
// generated suffix already exists as a literal heading: every number must
// still be unique, or the UNIQUE constraint fails the whole insert.
func TestDedupeNumbersAvoidsLiteralCollision(t *testing.T) {
	in := []db.Section{{Number: "Note"}, {Number: "Note (2)"}, {Number: "Note"}, {Number: "Note"}, {Number: "Note (2)"}}
	var got []string
	for _, s := range dedupeNumbers(in) {
		got = append(got, s.Number)
	}
	if want := "Note|Note (2)|Note (3)|Note (4)|Note (2) (2)"; strings.Join(got, "|") != want {
		t.Errorf("got %q, want %q", strings.Join(got, "|"), want)
	}
}

func TestEnsureBudgetAndJoin(t *testing.T) {
	release := make(chan struct{})
	s := openStore(t, DefaultLimitBytes, func(_ context.Context, d tdoc.Document) (*tdoc.Fetched, error) {
		<-release
		return fetchedDoc(d.ID, "slow"), nil
	})
	doc := testDoc("R1-2509715")
	err := s.Ensure(context.Background(), doc, 20*time.Millisecond)
	if !errors.Is(err, ErrInProgress) {
		t.Fatalf("err = %v, want ErrInProgress", err)
	}
	close(release)
	if err := s.Ensure(context.Background(), doc, time.Second); err != nil {
		t.Fatalf("join: %v", err)
	}
	if ok, _ := s.Has("R1-2509715"); !ok {
		t.Error("document not cached after the fetch finished")
	}
}

func TestEnsureSingleflight(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})
	s := openStore(t, DefaultLimitBytes, func(_ context.Context, d tdoc.Document) (*tdoc.Fetched, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		<-release
		return fetchedDoc(d.ID, "x"), nil
	})
	doc := testDoc("R1-2509715")
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := s.Ensure(context.Background(), doc, 5*time.Second); err != nil {
				t.Errorf("Ensure: %v", err)
			}
		}()
	}
	time.Sleep(30 * time.Millisecond)
	close(release)
	wg.Wait()
	if calls != 1 {
		t.Errorf("fetcher ran %d times, want 1", calls)
	}
}

func TestEnsurePropagatesFetchError(t *testing.T) {
	want := errors.New("HTTP 404")
	s := openStore(t, DefaultLimitBytes, func(context.Context, tdoc.Document) (*tdoc.Fetched, error) { return nil, want })
	if err := s.Ensure(context.Background(), testDoc("R1-1"), time.Second); !errors.Is(err, want) {
		t.Errorf("err = %v", err)
	}
	if ok, _ := s.Has("R1-1"); ok {
		t.Error("a failed fetch must not be cached")
	}
}

func TestEvictionDropsLeastRecentlyUsed(t *testing.T) {
	s := openStore(t, 0, func(_ context.Context, d tdoc.Document) (*tdoc.Fetched, error) {
		return fetchedDoc(d.ID, "body"), nil
	})
	// A zero limit keeps only the newest document.
	if err := s.Ensure(context.Background(), testDoc("R1-1"), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.Ensure(context.Background(), testDoc("R1-2"), time.Second); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Has("R1-1"); ok {
		t.Error("R1-1 should have been evicted")
	}
	if ok, _ := s.Has("R1-2"); !ok {
		t.Error("the just-fetched document must survive")
	}
	if secs, _ := s.AllSections("R1-1"); len(secs) != 0 {
		t.Errorf("evicted document still has %d sections", len(secs))
	}
	if imgs, _ := s.ListImages("R1-1"); len(imgs) != 0 {
		t.Errorf("evicted document still has %d images", len(imgs))
	}
}

func TestEvictionRespectsTouch(t *testing.T) {
	var size int64
	s := openStore(t, 0, func(_ context.Context, d tdoc.Document) (*tdoc.Fetched, error) {
		return fetchedDoc(d.ID, "body"), nil
	})
	if err := s.Ensure(context.Background(), testDoc("R1-1"), time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.conn.QueryRow("SELECT bytes FROM tdocs WHERE id = 'R1-1'").Scan(&size); err != nil {
		t.Fatal(err)
	}
	// Room for exactly two documents.
	s.limitBytes = 2 * size
	if err := s.Ensure(context.Background(), testDoc("R1-2"), time.Second); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond) // last_used_at has second resolution
	s.touch("R1-1")
	if err := s.Ensure(context.Background(), testDoc("R1-3"), time.Second); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.Has("R1-2"); ok {
		t.Error("R1-2, the least recently used, should have been evicted")
	}
	if ok, _ := s.Has("R1-1"); !ok {
		t.Error("R1-1 was touched and should survive")
	}
}

func TestNegativeLimitDisablesEviction(t *testing.T) {
	s := openStore(t, -1, func(_ context.Context, d tdoc.Document) (*tdoc.Fetched, error) {
		return fetchedDoc(d.ID, "body"), nil
	})
	for _, id := range []string{"R1-1", "R1-2", "R1-3"} {
		if err := s.Ensure(context.Background(), testDoc(id), time.Second); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"R1-1", "R1-2", "R1-3"} {
		if ok, _ := s.Has(id); !ok {
			t.Errorf("%s evicted with eviction disabled", id)
		}
	}
}

func TestSchemaGenerationWipe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tdocs.db")
	s, err := Open(Options{Path: path, LimitBytes: -1, Fetcher: func(_ context.Context, d tdoc.Document) (*tdoc.Fetched, error) {
		return fetchedDoc(d.ID, "body"), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Ensure(context.Background(), testDoc("R1-1"), time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := s.conn.Exec("PRAGMA user_version = 0"); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s, err = Open(Options{Path: path, LimitBytes: -1})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ok, _ := s.Has("R1-1"); ok {
		t.Error("an old-generation cache must be wiped on open")
	}
}

func TestOpenRejectsUnwritablePath(t *testing.T) {
	// A path whose parent is an existing file cannot be created.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := Open(Options{Path: filepath.Join(file, "tdocs.db")}); err == nil {
		t.Error("Open on an unwritable path should fail so the caller can disable fetching")
	}
}
