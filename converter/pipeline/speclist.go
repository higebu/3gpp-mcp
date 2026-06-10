package pipeline

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// minConcurrency is the minimum worker count for I/O-bound parallel tasks,
// chosen to amortize network latency even on low-CPU machines.
const (
	minConcurrency = 8
	// maxPageSize is the upper limit for HTML directory listing responses.
	maxPageSize = 10 << 20 // 10 MB
)

// defaultConcurrency returns a sensible default for I/O-bound parallel tasks:
// at least minConcurrency, or NumCPU if higher.
func defaultConcurrency() int {
	if n := runtime.NumCPU(); n > minConcurrency {
		return n
	}
	return minConcurrency
}

// SpecVersion represents a specific version of a 3GPP spec available for download.
type SpecVersion struct {
	Series        string // e.g., "23"
	SpecID        string // e.g., "23.501" or "38.101-1"
	Filename      string // e.g., "23501-k10.zip"
	Version       string // raw version token, e.g. "k10", "f20", "fa0", "300"
	VersionLetter string // first version character if it is a letter, else ""
	VersionMinor  int    // base-36 value of the characters after the first (for sorting)
	Release       int    // e.g., 20 or 0 for legacy
	URL           string // full download URL
}

const baseURL = "https://www.3gpp.org/ftp/Specs/archive/"

var (
	seriesDirRE = regexp.MustCompile(`(\d+)_series$`)
	// specDirRE matches spec directory names, including the multi-part specs
	// that carry a numeric suffix such as "38.101-1" or "34.123-2".
	specDirRE = regexp.MustCompile(`^\d+\.\d+(-\d+)?$`)
	hrefRE    = regexp.MustCompile(`href="([^"]+)"`)
	// versionTokenRE matches a 3GPP version token. The version is a base-36
	// string where every character can be a digit or a letter, e.g. "k10",
	// "f20", "3a0" or "fb0" — not just a letter followed by digits.
	versionTokenRE = regexp.MustCompile(`^[0-9a-z]{2,}$`)
)

// base36Digit returns the base-36 value of a single version character
// ('0'-'9' -> 0-9, 'a'-'z' -> 10-35), or -1 if the character is invalid.
func base36Digit(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	default:
		return -1
	}
}

// versionValue converts a base-36 version token into a comparable integer.
// Returns 0 if the token contains an invalid character.
func versionValue(v string) int64 {
	v = strings.ToLower(strings.TrimSpace(v))
	var n int64
	for i := 0; i < len(v); i++ {
		d := base36Digit(v[i])
		if d < 0 {
			return 0
		}
		n = n*36 + int64(d)
	}
	return n
}

// SpecVersionString returns the raw version token for DB comparison/display.
func SpecVersionString(sv *SpecVersion) string {
	return sv.Version
}

// ParseSpecEntry parses a spec list entry like "23_series/23.501/23501-k10.zip"
// or "38_series/38.101-1/38101-1-j50.zip".
func ParseSpecEntry(entry string) *SpecVersion {
	entry = strings.TrimSpace(entry)
	if entry == "" || !strings.HasSuffix(entry, ".zip") {
		return nil
	}

	parts := strings.Split(entry, "/")
	if len(parts) != 3 {
		return nil
	}

	seriesDir, specDir, filename := parts[0], parts[1], parts[2]
	seriesMatch := seriesDirRE.FindStringSubmatch(seriesDir)
	if seriesMatch == nil {
		return nil
	}
	if !specDirRE.MatchString(specDir) {
		return nil
	}
	series := seriesMatch[1]
	specID := specDir

	// The version is the final hyphen-delimited token of the filename, e.g.
	// "k10" in "23501-k10.zip" or "j50" in "38101-1-j50.zip". Each character is
	// a base-36 digit; the first character encodes the release (a=10, k=20, ...,
	// and plain digits for legacy releases such as "300" -> release 3).
	base := strings.TrimSuffix(filename, ".zip")
	idx := strings.LastIndex(base, "-")
	if idx < 0 {
		return nil
	}
	token := strings.ToLower(base[idx+1:])
	if !versionTokenRE.MatchString(token) {
		return nil
	}

	letter := ""
	if token[0] >= 'a' && token[0] <= 'z' {
		letter = token[0:1]
	}

	return &SpecVersion{
		Series:        series,
		SpecID:        specID,
		Filename:      filename,
		Version:       token,
		VersionLetter: letter,
		VersionMinor:  int(versionValue(token[1:])),
		Release:       base36Digit(token[0]),
		URL:           baseURL + entry,
	}
}

// IsNewerVersion compares version strings (e.g., "k10" vs "j60").
func IsNewerVersion(newVer, oldVer string) bool {
	if oldVer == "" {
		return true
	}
	return versionValue(newVer) > versionValue(oldVer)
}

// FetchSpecZips fetches zip file entries for a single spec directly,
// avoiding the full 3-level directory scrape.
// specID should be in "23.501" format (with or without dot).
// If useCache is true, results are cached to disk with a 24h TTL.
func FetchSpecZips(ctx context.Context, client *http.Client, specID string, useCache bool) ([]string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	// Normalize: "23.501" -> series "23", spec dir "23.501"
	normalized := specID
	if !strings.Contains(normalized, ".") && len(normalized) >= 4 {
		// "23501" -> "23.501"
		normalized = normalized[:2] + "." + normalized[2:]
	}

	parts := strings.SplitN(normalized, ".", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid spec ID format: %s", specID)
	}

	// Check cache
	cacheKey := CacheKey("speczips", normalized)
	if useCache {
		if cached, _ := LoadCache(cacheKey, defaultCacheTTL); cached != nil {
			return cached, nil
		}
	}

	series := parts[0]
	seriesDir := series + "_series"
	specDir := normalized

	url := baseURL + seriesDir + "/" + specDir + "/"
	log.Printf("Fetching zip listing for %s ...", specDir)
	html, err := fetchPage(ctx, client, url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}

	links := extractLinks(html)
	var entries []string
	for _, name := range links {
		if strings.HasSuffix(name, ".zip") {
			entries = append(entries, seriesDir+"/"+specDir+"/"+name)
		}
	}
	log.Printf("Found %d versions for %s", len(entries), specDir)

	if useCache {
		if err := SaveCache(cacheKey, entries); err != nil {
			log.Printf("warning: failed to save cache: %v", err)
		}
	}
	return entries, nil
}

// FetchSpecList scrapes the 3GPP FTP directory for all spec zip files.
// If useCache is true, results are cached to disk with a 24h TTL.
// scrapeConcurrency controls parallel HTTP requests; 0 uses defaultConcurrency().
func FetchSpecList(ctx context.Context, client *http.Client, seriesFilter []string, useCache bool, scrapeConcurrency int) ([]string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	if scrapeConcurrency <= 0 {
		scrapeConcurrency = defaultConcurrency()
	}

	// Check cache
	cacheKey := CacheKey("speclist", seriesFilter...)
	if useCache {
		if cached, _ := LoadCache(cacheKey, defaultCacheTTL); cached != nil {
			return cached, nil
		}
	}

	// Level 1: Get series directories
	log.Println("Fetching series list...")
	html, err := fetchPage(ctx, client, baseURL)
	if err != nil {
		return nil, fmt.Errorf("fetch archive root: %w", err)
	}

	seriesDirs := extractLinks(html)
	var filtered []string
	for _, name := range seriesDirs {
		if !seriesDirRE.MatchString(name) {
			continue
		}
		if len(seriesFilter) > 0 {
			m := seriesDirRE.FindStringSubmatch(name)
			if !contains(seriesFilter, m[1]) {
				continue
			}
		}
		filtered = append(filtered, name)
	}
	seriesDirs = filtered

	log.Printf("Found %d series", len(seriesDirs))

	// Level 2: Get spec directories per series (parallel)
	type specPair struct {
		seriesDir string
		specDir   string
	}
	var allSpecPairs []specPair
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, scrapeConcurrency)

	for _, sd := range seriesDirs {
		sd := sd
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			html, err := fetchPage(ctx, client, baseURL+sd+"/")
			if err != nil {
				log.Printf("Failed to fetch %s: %v", sd, err)
				return
			}
			links := extractLinks(html)
			mu.Lock()
			for _, name := range links {
				if specDirRE.MatchString(name) {
					allSpecPairs = append(allSpecPairs, specPair{sd, name})
				}
			}
			log.Printf("%s: %d specs", sd, len(links))
			mu.Unlock()
		}()
	}
	wg.Wait()

	// Level 3: Get zip files per spec (parallel with bounded concurrency)
	log.Printf("Fetching zip listings for %d specs...", len(allSpecPairs))
	var entries []string

	for _, pair := range allSpecPairs {
		pair := pair
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			html, err := fetchPage(ctx, client, baseURL+pair.seriesDir+"/"+pair.specDir+"/")
			if err != nil {
				log.Printf("Failed to fetch %s/%s: %v", pair.seriesDir, pair.specDir, err)
				return
			}
			links := extractLinks(html)
			mu.Lock()
			for _, name := range links {
				if strings.HasSuffix(name, ".zip") {
					entries = append(entries, pair.seriesDir+"/"+pair.specDir+"/"+name)
				}
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	log.Printf("Total: %d spec versions found", len(entries))

	if useCache {
		if err := SaveCache(cacheKey, entries); err != nil {
			log.Printf("warning: failed to save cache: %v", err)
		}
	}
	return entries, nil
}

// LoadSpecList loads spec list from a text file.
func LoadSpecList(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
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
	return entries, scanner.Err()
}

// FilterSpecs filters specs by release/series/spec_id and optionally keeps only the latest version.
func FilterSpecs(specs []*SpecVersion, release int, series []string, specID string, latestOnly bool) []*SpecVersion {
	var filtered []*SpecVersion

	for _, s := range specs {
		if release > 0 && s.Release != release {
			continue
		}
		if len(series) > 0 && !contains(series, s.Series) {
			continue
		}
		if specID != "" {
			normalized := strings.ReplaceAll(specID, ".", "")
			if strings.ReplaceAll(s.SpecID, ".", "") != normalized {
				continue
			}
		}
		filtered = append(filtered, s)
	}

	if latestOnly {
		best := make(map[string]*SpecVersion)
		for _, s := range filtered {
			key := s.SpecID
			if existing, ok := best[key]; !ok || versionKey(s) > versionKey(existing) {
				best[key] = s
			}
		}
		filtered = nil
		for _, s := range best {
			filtered = append(filtered, s)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].SpecID < filtered[j].SpecID
	})

	return filtered
}

func versionKey(s *SpecVersion) int64 {
	return int64(s.Release)*10000 + int64(s.VersionMinor)
}

func fetchPage(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "3gpp-converter/1.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d for %s", resp.StatusCode, url)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageSize))
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func extractLinks(html string) []string {
	matches := hrefRE.FindAllStringSubmatch(html, -1)
	var names []string
	for _, m := range matches {
		link := m[1]
		link = strings.TrimRight(link, "/")
		if idx := strings.LastIndex(link, "/"); idx >= 0 {
			link = link[idx+1:]
		}
		names = append(names, link)
	}
	return names
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
