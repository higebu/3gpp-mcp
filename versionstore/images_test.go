package versionstore

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
)

// fakeImages builds an image set of roughly size bytes of data each.
func fakeImages(size int) []db.Image {
	return []db.Image{{
		SpecID:      "ignored",
		Version:     "ignored",
		Name:        "image1.png",
		MIMEType:    "image/png",
		Data:        []byte(strings.Repeat("p", size)),
		LLMReadable: true,
	}, {
		SpecID:   "ignored",
		Version:  "ignored",
		Name:     "image2.emf",
		MIMEType: "image/x-emf",
		Data:     []byte(strings.Repeat("e", size)),
	}}
}

// openImageStore creates a store whose section fetcher returns a small canned
// spec, with the given image fetcher.
func openImageStore(t *testing.T, limit int64, imageFetcher ImageFetcher) *Store {
	t.Helper()
	s, err := Open(Options{
		Path:       filepath.Join(t.TempDir(), "versions.db"),
		LimitBytes: limit,
		Fetcher: func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
			spec, sections := fakeSpec("placeholder", "placeholder", 16)
			return spec, sections, nil
		},
		ImageFetcher: imageFetcher,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// ensureVersion caches one version's sections so image calls have an entry to
// attach to.
func ensureVersion(t *testing.T, s *Store, specID, version string) {
	t.Helper()
	if err := s.Ensure(context.Background(), specID, version, entry(specID), time.Minute); err != nil {
		t.Fatalf("Ensure %s v%s: %v", specID, version, err)
	}
}

func TestEnsureImagesAndRead(t *testing.T) {
	var calls atomic.Int32
	s := openImageStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		calls.Add(1)
		return fakeImages(32), nil
	})
	ensureVersion(t, s, "TS 23.501", "18.6.0")

	if fetched, err := s.HasImages("TS 23.501", "18.6.0"); err != nil || fetched {
		t.Fatalf("HasImages before fetch = %v, %v; want false, nil", fetched, err)
	}

	for range 3 {
		if err := s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
			t.Fatalf("EnsureImages: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("image fetcher called %d times, want 1", got)
	}

	if fetched, err := s.HasImages("TS 23.501", "18.6.0"); err != nil || !fetched {
		t.Fatalf("HasImages after fetch = %v, %v; want true, nil", fetched, err)
	}

	// The caller's spec ID and version win over whatever the fetcher returned.
	img, err := s.GetImage("TS 23.501", "18.6.0", "image1.png")
	if err != nil || img == nil {
		t.Fatalf("GetImage = %+v, %v; want an image", img, err)
	}
	if img.SpecID != "TS 23.501" || img.Version != "18.6.0" {
		t.Errorf("image identity = %s v%s, want TS 23.501 v18.6.0", img.SpecID, img.Version)
	}
	if !img.LLMReadable || img.MIMEType != "image/png" {
		t.Errorf("image = %+v, want a readable PNG", img)
	}

	missing, err := s.GetImage("TS 23.501", "18.6.0", "nonexistent.png")
	if err != nil || missing != nil {
		t.Errorf("GetImage(missing) = %+v, %v; want nil, nil", missing, err)
	}

	infos, err := s.ListImages("TS 23.501", "18.6.0")
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(infos) != 2 || infos[0].Name != "image1.png" || infos[1].Name != "image2.emf" {
		t.Fatalf("ListImages = %+v, want image1.png and image2.emf", infos)
	}
}

// TestEnsureImagesRemembersEmptyResult checks that a spec without figures does
// not re-download its archive on every image call.
func TestEnsureImagesRemembersEmptyResult(t *testing.T) {
	var calls atomic.Int32
	s := openImageStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		calls.Add(1)
		return nil, nil
	})
	ensureVersion(t, s, "TS 23.501", "18.6.0")

	for range 2 {
		if err := s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
			t.Fatalf("EnsureImages: %v", err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("image fetcher called %d times, want 1", got)
	}
	if fetched, err := s.HasImages("TS 23.501", "18.6.0"); err != nil || !fetched {
		t.Errorf("HasImages = %v, %v; want true after an empty fetch", fetched, err)
	}
}

// TestGetImageBaseNameFallback checks that a lookup by the original EMF/WMF
// name finds the converted PNG: cached section Markdown keeps the original
// filename while conversion renames the stored image.
func TestGetImageBaseNameFallback(t *testing.T) {
	s := openImageStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		return []db.Image{{Name: "image1.png", MIMEType: "image/png", Data: []byte("png"), LLMReadable: true}}, nil
	})
	ensureVersion(t, s, "TS 23.501", "18.6.0")
	if err := s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
		t.Fatalf("EnsureImages: %v", err)
	}

	img, err := s.GetImage("TS 23.501", "18.6.0", "image1.emf")
	if err != nil || img == nil {
		t.Fatalf("GetImage(image1.emf) = %+v, %v; want the converted PNG", img, err)
	}
	if img.Name != "image1.png" {
		t.Errorf("Name = %q, want image1.png", img.Name)
	}

	// The fallback must not treat LIKE metacharacters in the name as wildcards.
	if img, err := s.GetImage("TS 23.501", "18.6.0", "image_.emf"); err != nil || img != nil {
		t.Errorf("GetImage(image_.emf) = %+v, %v; want nil, nil", img, err)
	}

	// A name without an extension has nothing to fall back on.
	if img, err := s.GetImage("TS 23.501", "18.6.0", "image1"); err != nil || img != nil {
		t.Errorf("GetImage(image1) = %+v, %v; want nil, nil", img, err)
	}
}

// TestGetImageFallbackPrefersReadable checks that when several cached images
// share a base name, the fallback deterministically picks the readable one.
func TestGetImageFallbackPrefersReadable(t *testing.T) {
	s := openImageStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		return []db.Image{
			{Name: "image1.emf", MIMEType: "image/x-emf", Data: []byte("emf")},
			{Name: "image1.png", MIMEType: "image/png", Data: []byte("png"), LLMReadable: true},
		}, nil
	})
	ensureVersion(t, s, "TS 23.501", "18.6.0")
	if err := s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
		t.Fatalf("EnsureImages: %v", err)
	}

	img, err := s.GetImage("TS 23.501", "18.6.0", "image1.wmf")
	if err != nil || img == nil {
		t.Fatalf("GetImage(image1.wmf) = %+v, %v; want a fallback match", img, err)
	}
	if img.Name != "image1.png" || !img.LLMReadable {
		t.Errorf("fallback picked %q, want the readable image1.png", img.Name)
	}
}

// TestPutClearsStaleImages checks that re-caching a version's sections drops
// image rows from an earlier life of the entry: the REPLACE of cache_entries
// resets images_fetched, so keeping the blobs would corrupt the account.
func TestPutClearsStaleImages(t *testing.T) {
	s := openImageStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		return fakeImages(8), nil
	})
	ensureVersion(t, s, "TS 23.501", "18.6.0")
	if err := s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
		t.Fatalf("EnsureImages: %v", err)
	}

	// Another process re-caching the same version calls put directly.
	spec, sections := fakeSpec("TS 23.501", "18.6.0", 16)
	if err := s.put(spec, sections); err != nil {
		t.Fatalf("put: %v", err)
	}

	if fetched, _ := s.HasImages("TS 23.501", "18.6.0"); fetched {
		t.Error("images_fetched survived a re-put; the flag must reset with the entry")
	}
	if img, err := s.GetImage("TS 23.501", "18.6.0", "image1.png"); err != nil || img != nil {
		t.Errorf("stale image row survived a re-put: %+v, %v", img, err)
	}
}

// TestEnsureImagesSingleflight checks that concurrent callers for the same
// version share one image download.
func TestEnsureImagesSingleflight(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	s := openImageStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		calls.Add(1)
		<-release
		return fakeImages(8), nil
	})
	ensureVersion(t, s, "TS 23.501", "18.6.0")

	const callers = 4
	var wg sync.WaitGroup
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute)
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
		t.Errorf("image fetcher called %d times, want 1", got)
	}
}

// TestEnsureImagesBudgetExceeded checks that a slow image fetch yields
// ErrInProgress and still completes in the background.
func TestEnsureImagesBudgetExceeded(t *testing.T) {
	release := make(chan struct{})
	s := openImageStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		<-release
		return fakeImages(8), nil
	})
	ensureVersion(t, s, "TS 23.501", "18.6.0")

	err := s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), 20*time.Millisecond)
	if !errors.Is(err, ErrInProgress) {
		t.Fatalf("EnsureImages = %v, want ErrInProgress", err)
	}

	close(release)
	if err := s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
		t.Fatalf("EnsureImages after budget: %v", err)
	}
	if fetched, _ := s.HasImages("TS 23.501", "18.6.0"); !fetched {
		t.Error("images did not land after the background fetch completed")
	}
}

// TestEnsureImagesSurvivesCallerCancellation checks that a cancelled request
// does not throw away the image download it started.
func TestEnsureImagesSurvivesCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	s := openImageStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		close(started)
		time.Sleep(30 * time.Millisecond)
		return fakeImages(8), nil
	})
	ensureVersion(t, s, "TS 23.501", "18.6.0")

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	if err := s.EnsureImages(ctx, "TS 23.501", "18.6.0", entry("23.501"), time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureImages = %v, want context.Canceled", err)
	}

	// The detached fetch should still land in the cache. A zero budget also
	// exercises the default-budget fallback.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fetched, _ := s.HasImages("TS 23.501", "18.6.0"); fetched {
			if err := s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), 0); err != nil {
				t.Fatalf("EnsureImages after completion: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Error("image fetch did not complete after the caller was cancelled")
}

// TestEnsureImagesPropagatesFetchError checks that a failed fetch reaches the
// caller and leaves the version marked as not fetched.
func TestEnsureImagesPropagatesFetchError(t *testing.T) {
	want := errors.New("boom")
	s := openImageStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		return nil, want
	})
	ensureVersion(t, s, "TS 23.501", "18.6.0")

	if err := s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); !errors.Is(err, want) {
		t.Errorf("EnsureImages = %v, want %v", err, want)
	}
	if fetched, _ := s.HasImages("TS 23.501", "18.6.0"); fetched {
		t.Error("a failed fetch must not mark images as fetched")
	}
}

// TestImageBytesCountTowardLimit checks that image blobs join the eviction
// account and that an evicted version takes its images with it.
func TestImageBytesCountTowardLimit(t *testing.T) {
	const size = 4096
	// Sections are tiny next to the images, so a limit of roughly one image set
	// forces the older of two versions out once both hold images.
	s := openImageStore(t, 3*size, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		return fakeImages(size), nil
	})

	ensureVersion(t, s, "TS 23.501", "18.6.0")
	if err := s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute); err != nil {
		t.Fatalf("EnsureImages: %v", err)
	}
	ensureVersion(t, s, "TS 23.502", "18.6.0")
	if err := s.EnsureImages(context.Background(), "TS 23.502", "18.6.0", entry("23.502"), time.Minute); err != nil {
		t.Fatalf("EnsureImages: %v", err)
	}

	if has, _ := s.Has("TS 23.501", "18.6.0"); has {
		t.Error("TS 23.501 should have been evicted once image bytes exceeded the limit")
	}
	if img, err := s.GetImage("TS 23.501", "18.6.0", "image1.png"); err != nil || img != nil {
		t.Errorf("evicted version still serves images: %+v, %v", img, err)
	}
	if has, _ := s.Has("TS 23.502", "18.6.0"); !has {
		t.Error("the most recently fetched version should be kept")
	}
	if img, err := s.GetImage("TS 23.502", "18.6.0", "image1.png"); err != nil || img == nil {
		t.Errorf("kept version lost its images: %+v, %v", img, err)
	}
}

// TestPutImagesAfterEviction checks that an image fetch whose version was
// evicted mid-flight fails without leaving orphaned blobs behind.
func TestPutImagesAfterEviction(t *testing.T) {
	var s *Store
	s = openImageStore(t, DefaultLimitBytes, func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
		// Simulate the LRU dropping this version while its images download.
		if err := s.delete("TS 23.501", "18.6.0"); err != nil {
			t.Errorf("delete: %v", err)
		}
		return fakeImages(8), nil
	})
	ensureVersion(t, s, "TS 23.501", "18.6.0")

	err := s.EnsureImages(context.Background(), "TS 23.501", "18.6.0", entry("23.501"), time.Minute)
	if err == nil || !strings.Contains(err.Error(), "evicted") {
		t.Fatalf("EnsureImages = %v, want an eviction error", err)
	}
	if img, err := s.GetImage("TS 23.501", "18.6.0", "image1.png"); err != nil || img != nil {
		t.Errorf("orphaned image survived: %+v, %v", img, err)
	}
	if fetched, _ := s.HasImages("TS 23.501", "18.6.0"); fetched {
		t.Error("HasImages reports true for a version that is gone")
	}
}

// TestOpenMigratesPreImageCache checks that a cache file created before images
// were cached opens cleanly and gains the images_fetched column.
func TestOpenMigratesPreImageCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions.db")
	const oldSchema = db.SpecTablesSchema + `
CREATE TABLE IF NOT EXISTS cache_entries (
    spec_id TEXT NOT NULL,
    version TEXT NOT NULL,
    bytes INTEGER NOT NULL,
    fetched_at INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    PRIMARY KEY (spec_id, version)
);
`
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open old cache: %v", err)
	}
	if _, err := conn.Exec(oldSchema); err != nil {
		t.Fatalf("create old schema: %v", err)
	}
	if _, err := conn.Exec(
		"INSERT INTO cache_entries (spec_id, version, bytes, fetched_at, last_used_at) VALUES ('TS 23.501', '18.6.0', 8, 1, 1)",
	); err != nil {
		t.Fatalf("seed old cache: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatalf("close old cache: %v", err)
	}

	s, err := Open(Options{Path: path, LimitBytes: DefaultLimitBytes})
	if err != nil {
		t.Fatalf("Open on a pre-image cache: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if has, err := s.Has("TS 23.501", "18.6.0"); err != nil || !has {
		t.Errorf("Has = %v, %v; want the migrated entry", has, err)
	}
	if fetched, err := s.HasImages("TS 23.501", "18.6.0"); err != nil || fetched {
		t.Errorf("HasImages = %v, %v; want false for a migrated entry", fetched, err)
	}

	// Reopening an already-migrated cache must tolerate the existing column.
	if err := s.Close(); err != nil {
		t.Fatalf("close migrated cache: %v", err)
	}
	reopened, err := Open(Options{Path: path, LimitBytes: DefaultLimitBytes})
	if err != nil {
		t.Fatalf("Open on an already-migrated cache: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if has, err := reopened.Has("TS 23.501", "18.6.0"); err != nil || !has {
		t.Errorf("Has after reopen = %v, %v; want the entry intact", has, err)
	}
}
