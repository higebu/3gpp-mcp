package pipeline

import (
	"os"
	"path/filepath"
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
