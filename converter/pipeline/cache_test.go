package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCacheKey(t *testing.T) {
	tests := []struct {
		prefix string
		params []string
		want   string
	}{
		{"speclist", nil, "speclist.txt"},
		{"speclist", []string{"23"}, "speclist_23.txt"},
		{"speclist", []string{"29", "23"}, "speclist_23_29.txt"}, // sorted
		{"speczips", []string{"23.501"}, "speczips_23.501.txt"},
	}

	for _, tt := range tests {
		got := CacheKey(tt.prefix, tt.params...)
		if got != tt.want {
			t.Errorf("CacheKey(%q, %v) = %q, want %q", tt.prefix, tt.params, got, tt.want)
		}
	}
}

func TestSaveAndLoadCache(t *testing.T) {
	// Use a temp directory as cache home
	tmpDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	entries := []string{"23_series/23.501/23501-k10.zip", "29_series/29.510/29510-j60.zip"}
	key := CacheKey("test")

	// Save
	if err := SaveCache(key, entries); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}

	// Verify file exists
	cachePath := filepath.Join(tmpDir, "3gpp-mcp", key)
	if _, err := os.Stat(cachePath); err != nil {
		t.Fatalf("cache file not created: %v", err)
	}

	// Load
	loaded, err := LoadCache(key, 1*time.Hour)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if len(loaded) != len(entries) {
		t.Fatalf("expected %d entries, got %d", len(entries), len(loaded))
	}
	for i, e := range entries {
		if loaded[i] != e {
			t.Errorf("entry[%d] = %q, want %q", i, loaded[i], e)
		}
	}
}

func TestLoadCache_Expired(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	key := CacheKey("expired")
	if err := SaveCache(key, []string{"entry1"}); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}

	// Set modification time to 2 hours ago
	cachePath := filepath.Join(tmpDir, "3gpp-mcp", key)
	past := time.Now().Add(-2 * time.Hour)
	os.Chtimes(cachePath, past, past)

	// Load with 1 hour TTL - should miss
	loaded, _ := LoadCache(key, 1*time.Hour)
	if loaded != nil {
		t.Error("expected cache miss for expired entry")
	}
}

func TestLoadCache_Missing(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", tmpDir)

	loaded, err := LoadCache("nonexistent.txt", 1*time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if loaded != nil {
		t.Error("expected nil for missing cache")
	}
}

// TestCacheDir covers the two branches of cacheDir: XDG_CACHE_HOME set and
// unset (falls back to $HOME/.cache/3gpp-mcp).
func TestCacheDir(t *testing.T) {
	t.Run("with XDG_CACHE_HOME", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", "/tmp/xdg-test")
		dir, err := cacheDir()
		if err != nil {
			t.Fatalf("cacheDir: %v", err)
		}
		want := "/tmp/xdg-test/3gpp-mcp"
		if dir != want {
			t.Errorf("cacheDir = %q, want %q", dir, want)
		}
	})

	t.Run("falls back to home .cache", func(t *testing.T) {
		t.Setenv("XDG_CACHE_HOME", "")
		dir, err := cacheDir()
		if err != nil {
			// On systems where UserHomeDir fails the function should return
			// an error; either outcome is acceptable as long as we exercised
			// the code path.
			return
		}
		if !strings.HasSuffix(dir, "/3gpp-mcp") {
			t.Errorf("cacheDir = %q, want suffix /3gpp-mcp", dir)
		}
		if !strings.Contains(dir, ".cache") {
			t.Errorf("cacheDir = %q, want to contain .cache", dir)
		}
	})
}

// TestSaveCache_CreatesDir verifies SaveCache creates the cache directory
// even when the parent does not yet exist, exercising the mkdir path.
func TestSaveCache_CreatesDir(t *testing.T) {
	// Point cache at a brand-new subdirectory inside t.TempDir().
	root := filepath.Join(t.TempDir(), "freshxdg")
	t.Setenv("XDG_CACHE_HOME", root)

	if err := SaveCache(CacheKey("create"), []string{"only-entry"}); err != nil {
		t.Fatalf("SaveCache: %v", err)
	}

	cacheDirPath := filepath.Join(root, "3gpp-mcp")
	if _, err := os.Stat(cacheDirPath); err != nil {
		t.Errorf("expected cache dir created: %v", err)
	}

	loaded, err := LoadCache(CacheKey("create"), time.Hour)
	if err != nil {
		t.Fatalf("LoadCache: %v", err)
	}
	if len(loaded) != 1 || loaded[0] != "only-entry" {
		t.Errorf("round trip = %v, want [only-entry]", loaded)
	}
}
