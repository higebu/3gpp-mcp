package versionstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
)

// fakeSpec builds a spec plus sections of roughly size bytes of content.
func fakeSpec(specID, version string, size int) (db.Spec, []db.Section) {
	spec := db.Spec{ID: specID, Version: version, Title: specID, Release: "18", Series: "23"}
	sections := []db.Section{{
		SpecID:  specID,
		Version: version,
		Number:  "1",
		Title:   "Scope",
		Level:   1,
		Content: strings.Repeat("x", size),
	}, {
		SpecID:       specID,
		Version:      version,
		Number:       "1.1",
		Title:        "General",
		Level:        2,
		ParentNumber: "1",
		Content:      "general text",
	}}
	return spec, sections
}

func entry(specID string) *pipeline.SpecVersion {
	return &pipeline.SpecVersion{SpecID: specID, Version: "i60", Release: 18}
}

// openStore creates a store in a temp directory with a stub fetcher.
func openStore(t *testing.T, limit int64, fetcher Fetcher) *Store {
	t.Helper()
	s, err := Open(Options{
		Path:       filepath.Join(t.TempDir(), "versions.db"),
		LimitBytes: limit,
		Fetcher:    fetcher,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestEnsureAndRead(t *testing.T) {
	s := openStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		spec, sections := fakeSpec("ignored", "ignored", 16)
		return spec, sections, nil
	})

	if err := s.Ensure(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	// The caller's spec ID and version win over whatever the fetcher returned.
	has, err := s.Has("TS 23.501", "18.6.0")
	if err != nil || !has {
		t.Fatalf("Has = %v, %v; want true, nil", has, err)
	}

	sections, err := s.GetSection("TS 23.501", "18.6.0", "1", false)
	if err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	if len(sections) != 1 || sections[0].Number != "1" {
		t.Fatalf("GetSection = %+v, want one section 1", sections)
	}
	if sections[0].SpecID != "TS 23.501" || sections[0].Version != "18.6.0" {
		t.Errorf("section identity = %s v%s, want TS 23.501 v18.6.0", sections[0].SpecID, sections[0].Version)
	}
	if sections[0].Release != "18" {
		t.Errorf("Release = %q, want 18", sections[0].Release)
	}

	withSubs, err := s.GetSection("TS 23.501", "18.6.0", "1", true)
	if err != nil {
		t.Fatalf("GetSection subsections: %v", err)
	}
	if len(withSubs) != 2 {
		t.Errorf("GetSection with subsections = %d sections, want 2", len(withSubs))
	}

	toc, err := s.GetTOC("TS 23.501", "18.6.0")
	if err != nil {
		t.Fatalf("GetTOC: %v", err)
	}
	if len(toc) != 2 || toc[0].Content != "" {
		t.Errorf("GetTOC = %+v, want 2 sections without content", toc)
	}

	versions, err := s.CachedVersions("TS 23.501")
	if err != nil || len(versions) != 1 || versions[0] != "18.6.0" {
		t.Errorf("CachedVersions = %v, %v; want [18.6.0], nil", versions, err)
	}

	spec, err := s.GetSpec("TS 23.501", "18.6.0")
	if err != nil || spec == nil || spec.ID != "TS 23.501" {
		t.Errorf("GetSpec = %+v, %v", spec, err)
	}
	missing, err := s.GetSpec("TS 23.501", "1.0.0")
	if err != nil || missing != nil {
		t.Errorf("GetSpec(missing) = %+v, %v; want nil, nil", missing, err)
	}
}

func TestAllSections(t *testing.T) {
	s := openStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		spec, sections := fakeSpec("ignored", "ignored", 16)
		return spec, sections, nil
	})

	if err := s.Ensure(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	sections, err := s.AllSections("TS 23.501", "18.6.0")
	if err != nil {
		t.Fatalf("AllSections: %v", err)
	}
	if len(sections) != 2 || sections[0].Number != "1" || sections[1].Number != "1.1" {
		t.Fatalf("AllSections = %+v, want sections 1 and 1.1 in document order", sections)
	}
	if sections[0].Content == "" || sections[1].Content != "general text" {
		t.Errorf("AllSections must include content, got %+v", sections)
	}
	if sections[0].Release != "18" {
		t.Errorf("Release = %q, want 18", sections[0].Release)
	}
}

func TestEnsureIsCachedOnSecondCall(t *testing.T) {
	var calls atomic.Int32
	s := openStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		calls.Add(1)
		spec, sections := fakeSpec("TS 23.501", "18.6.0", 8)
		return spec, sections, nil
	})

	for range 3 {
		if err := s.Ensure(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
			t.Fatalf("Ensure: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("fetcher called %d times, want 1", got)
	}
}

// TestEnsureSingleflight checks that concurrent callers for the same version
// share one download instead of each starting their own.
func TestEnsureSingleflight(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	s := openStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		calls.Add(1)
		<-release
		spec, sections := fakeSpec("TS 23.501", "18.6.0", 8)
		return spec, sections, nil
	})

	const callers = 4
	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.Ensure(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute)
		}()
	}
	// Give every caller a chance to join the same in-flight fetch.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("fetcher called %d times, want 1", got)
	}
}

// TestEnsureBudgetExceeded checks that a slow fetch yields ErrInProgress and
// still completes in the background, so a later call finds the content.
func TestEnsureBudgetExceeded(t *testing.T) {
	release := make(chan struct{})
	s := openStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		<-release
		spec, sections := fakeSpec("TS 23.501", "18.6.0", 8)
		return spec, sections, nil
	})

	err := s.Ensure(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), 20*time.Millisecond)
	if !errors.Is(err, ErrInProgress) {
		t.Fatalf("Ensure = %v, want ErrInProgress", err)
	}

	close(release)
	// The background fetch keeps running; a second call joins it and succeeds.
	if err := s.Ensure(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
		t.Fatalf("Ensure after budget: %v", err)
	}
	has, err := s.Has("TS 23.501", "18.6.0")
	if err != nil || !has {
		t.Errorf("Has after background completion = %v, %v; want true, nil", has, err)
	}
}

// TestEnsureSurvivesCallerCancellation checks that a cancelled request does not
// throw away the download it started.
func TestEnsureSurvivesCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	s := openStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		close(started)
		if err := ctx.Err(); err != nil {
			return db.Spec{}, nil, err
		}
		time.Sleep(30 * time.Millisecond)
		if err := ctx.Err(); err != nil {
			return db.Spec{}, nil, err
		}
		spec, sections := fakeSpec("TS 23.501", "18.6.0", 8)
		return spec, sections, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	if err := s.Ensure(ctx, "TS 23.501", "18.6.0", entry("23.501"), time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("Ensure = %v, want context.Canceled", err)
	}

	// The detached fetch should still land in the cache.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if has, _ := s.Has("TS 23.501", "18.6.0"); has {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("fetch did not complete after the caller was cancelled")
}

func TestEnsurePropagatesFetchError(t *testing.T) {
	want := errors.New("boom")
	s := openStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		return db.Spec{}, nil, want
	})
	if err := s.Ensure(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); !errors.Is(err, want) {
		t.Errorf("Ensure = %v, want %v", err, want)
	}
	// A failed fetch must not leave a cache entry behind.
	if has, _ := s.Has("TS 23.501", "18.6.0"); has {
		t.Error("failed fetch left a cache entry")
	}
}

// TestEnsureRecoversFetcherPanic checks that a panic on the detached fetch
// goroutine is reported as an error instead of killing the process: the
// pipeline parses untrusted binaries, and no per-request recovery applies to
// a goroutine deliberately detached from the request.
func TestEnsureRecoversFetcherPanic(t *testing.T) {
	var calls atomic.Int32
	s := openStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		if calls.Add(1) == 1 {
			panic("slice bounds out of range")
		}
		spec, sections := fakeSpec("TS 23.501", "18.6.0", 8)
		return spec, sections, nil
	})

	err := s.Ensure(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("Ensure = %v, want an error reporting the panic", err)
	}
	// The panic must not leave a cache entry claiming content that never landed.
	if has, _ := s.Has("TS 23.501", "18.6.0"); has {
		t.Error("a panicked fetch left a cache entry")
	}
	// The inflight slot must be released so a later call starts a fresh fetch.
	if err := s.Ensure(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
		t.Fatalf("Ensure retry after panic: %v", err)
	}
	if has, _ := s.Has("TS 23.501", "18.6.0"); !has {
		t.Error("retry after a panicked fetch did not cache the version")
	}
}

// TestEvictionDropsLeastRecentlyUsed fills the cache past its limit and checks
// that the oldest entry goes and the newest stays.
func TestEvictionDropsLeastRecentlyUsed(t *testing.T) {
	const size = 4096
	// An entry counts the content and title of both of its sections.
	entryBytes := int64(size + len("Scope") + len("general text") + len("General"))
	// Two entries fit exactly; the third must push one out. Entries written in
	// the same second tie on last_used_at and are broken by insertion order, so
	// the oldest is unambiguous without slowing the test down.
	s := openStore(t, 2*entryBytes, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		spec, sections := fakeSpec("placeholder", "placeholder", size)
		return spec, sections, nil
	})

	specs := []string{"TS 23.501", "TS 23.502", "TS 23.503"}
	for _, id := range specs {
		if err := s.Ensure(context.Background(), id, "18.6.0", entry(id), time.Minute); err != nil {
			t.Fatalf("Ensure %s: %v", id, err)
		}
	}

	if has, _ := s.Has("TS 23.501", "18.6.0"); has {
		t.Error("oldest entry TS 23.501 should have been evicted")
	}
	for _, id := range specs[1:] {
		if has, _ := s.Has(id, "18.6.0"); !has {
			t.Errorf("%s should still be cached", id)
		}
	}

	// Evicted sections must go with the entry, not linger.
	sections, err := s.GetSection("TS 23.501", "18.6.0", "1", false)
	if err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	if len(sections) != 0 {
		t.Errorf("evicted spec still has %d sections", len(sections))
	}
}

// TestEvictionKeepsAnOversizedEntry checks that a spec larger than the whole
// limit is kept rather than deleted immediately after being fetched.
func TestEvictionKeepsAnOversizedEntry(t *testing.T) {
	s := openStore(t, 16, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		spec, sections := fakeSpec("placeholder", "placeholder", 4096)
		return spec, sections, nil
	})
	if err := s.Ensure(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if has, _ := s.Has("TS 23.501", "18.6.0"); !has {
		t.Error("an entry larger than the limit should be kept, not evicted on arrival")
	}
}

func TestNegativeLimitDisablesEviction(t *testing.T) {
	s := openStore(t, -1, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		spec, sections := fakeSpec("placeholder", "placeholder", 4096)
		return spec, sections, nil
	})
	for i := range 3 {
		id := fmt.Sprintf("TS 23.50%d", i)
		if err := s.Ensure(context.Background(), id, "18.6.0", entry(id), time.Minute); err != nil {
			t.Fatalf("Ensure %s: %v", id, err)
		}
	}
	for i := range 3 {
		id := fmt.Sprintf("TS 23.50%d", i)
		if has, _ := s.Has(id, "18.6.0"); !has {
			t.Errorf("%s evicted despite eviction being disabled", id)
		}
	}
}

// TestZeroLimitKeepsOnlyTheNewest checks that an explicit limit of zero is
// honoured rather than silently replaced by the default.
func TestZeroLimitKeepsOnlyTheNewest(t *testing.T) {
	s := openStore(t, 0, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		spec, sections := fakeSpec("placeholder", "placeholder", 512)
		return spec, sections, nil
	})
	for _, id := range []string{"TS 23.501", "TS 23.502"} {
		if err := s.Ensure(context.Background(), id, "18.6.0", entry(id), time.Minute); err != nil {
			t.Fatalf("Ensure %s: %v", id, err)
		}
	}
	if has, _ := s.Has("TS 23.501", "18.6.0"); has {
		t.Error("a zero limit should have evicted the older entry")
	}
	if has, _ := s.Has("TS 23.502", "18.6.0"); !has {
		t.Error("the most recently fetched entry should be kept even at a zero limit")
	}
}

func TestOpenRejectsUnwritablePath(t *testing.T) {
	// A path whose parent is an existing file cannot be created.
	file := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := Open(Options{Path: filepath.Join(file, "versions.db")}); err == nil {
		t.Error("Open on an unwritable path should fail so the caller can disable fetching")
	}
}

// TestCachedVersionsSemanticOrder checks that versions are ordered by their
// numeric components, not lexicographically ("18.10.0" after "18.6.0" as
// text, but newer semantically).
func TestCachedVersionsSemanticOrder(t *testing.T) {
	s := openStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		spec, sections := fakeSpec("placeholder", "placeholder", 8)
		return spec, sections, nil
	})
	for _, v := range []string{"18.6.0", "18.10.0", "17.9.0"} {
		if err := s.Ensure(context.Background(), "TS 23.501", v, entry("23.501"), time.Minute); err != nil {
			t.Fatalf("Ensure %s: %v", v, err)
		}
	}
	versions, err := s.CachedVersions("TS 23.501")
	if err != nil {
		t.Fatalf("CachedVersions: %v", err)
	}
	want := []string{"18.10.0", "18.6.0", "17.9.0"}
	if len(versions) != len(want) {
		t.Fatalf("CachedVersions = %v, want %v", versions, want)
	}
	for i := range want {
		if versions[i] != want[i] {
			t.Fatalf("CachedVersions = %v, want %v", versions, want)
		}
	}
}

// TestGetSectionWildcardsAreLiteral checks that LIKE metacharacters in a
// section number do not act as wildcards against the cache.
func TestGetSectionWildcardsAreLiteral(t *testing.T) {
	s := openStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		spec, sections := fakeSpec("placeholder", "placeholder", 8)
		return spec, sections, nil
	})
	if err := s.Ensure(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, number := range []string{"%", "1_1", "_"} {
		sections, err := s.GetSection("TS 23.501", "18.6.0", number, true)
		if err != nil {
			t.Fatalf("GetSection(%q): %v", number, err)
		}
		if len(sections) != 0 {
			t.Errorf("GetSection(%q) matched %d sections, want 0", number, len(sections))
		}
	}
}
