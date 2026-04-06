package pipeline

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// defaultCacheTTL is the default cache time-to-live.
// Override via THREEGPP_CACHE_TTL_HOURS environment variable.
var defaultCacheTTL = func() time.Duration {
	if v := os.Getenv("THREEGPP_CACHE_TTL_HOURS"); v != "" {
		if hours, err := strconv.Atoi(v); err == nil && hours > 0 {
			return time.Duration(hours) * time.Hour
		}
	}
	return 24 * time.Hour
}()

// cacheDir returns the cache directory path.
// Uses $XDG_CACHE_HOME/3gpp-mcp if set, otherwise ~/.cache/3gpp-mcp.
func cacheDir() (string, error) {
	base := os.Getenv("XDG_CACHE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("get home dir: %w", err)
		}
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "3gpp-mcp"), nil
}

// CacheKey generates a cache file name from a prefix and parameters.
// e.g., CacheKey("speclist", "23", "29") -> "speclist_23_29.txt"
func CacheKey(prefix string, params ...string) string {
	if len(params) == 0 {
		return prefix + ".txt"
	}
	sorted := make([]string, len(params))
	copy(sorted, params)
	sort.Strings(sorted)
	return prefix + "_" + strings.Join(sorted, "_") + ".txt"
}

// LoadCache reads cached entries from a file if it exists and is within TTL.
// Returns nil, nil if cache miss (no file or expired).
func LoadCache(key string, ttl time.Duration) ([]string, error) {
	dir, err := cacheDir()
	if err != nil {
		return nil, nil
	}

	path := filepath.Join(dir, key)
	info, err := os.Stat(path)
	if err != nil {
		return nil, nil // file doesn't exist
	}

	if time.Since(info.ModTime()) > ttl {
		return nil, nil // expired
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()

	var entries []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			entries = append(entries, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("cache read error for %s: %w", key, err)
	}

	log.Printf("Cache hit: %s (%d entries)", key, len(entries))
	return entries, nil
}

// SaveCache writes entries to a cache file atomically via rename.
func SaveCache(key string, entries []string) error {
	dir, err := cacheDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create cache dir: %w", err)
	}

	// Write to a temp file first, then rename for atomicity.
	tmp, err := os.CreateTemp(dir, key+".tmp.*")
	if err != nil {
		return fmt.Errorf("create temp cache file: %w", err)
	}
	tmpPath := tmp.Name()

	w := bufio.NewWriter(tmp)
	for _, e := range entries {
		fmt.Fprintln(w, e)
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}

	path := filepath.Join(dir, key)
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename cache file: %w", err)
	}

	log.Printf("Cache saved: %s (%d entries)", key, len(entries))
	return nil
}
