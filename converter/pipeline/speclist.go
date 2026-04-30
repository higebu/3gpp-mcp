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
	"strconv"
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
	SpecID        string // e.g., "23.501"
	Filename      string // e.g., "23501-k10.zip"
	VersionLetter string // e.g., "k" or "" for legacy
	VersionMinor  int    // e.g., 10
	Release       int    // e.g., 20 or 0 for legacy
	URL           string // full download URL
}

const baseURL = "https://www.3gpp.org/ftp/Specs/archive/"

var (
	versionRE   = regexp.MustCompile(`(?i).+-([a-z])(\d+)\.zip$`)
	legacyRE    = regexp.MustCompile(`.+-(\d{3,})\.zip$`)
	seriesDirRE = regexp.MustCompile(`(\d+)_series$`)
	specDirRE   = regexp.MustCompile(`^\d+\.\d+$`)
	hrefRE      = regexp.MustCompile(`href="([^"]+)"`)
	verLetterRE = regexp.MustCompile(`(?i)^([a-zA-Z])(\d+)$`)
	verNumRE    = regexp.MustCompile(`^(\d+)$`)
)

// letterToRelease converts version letter to release number: a=10, b=11, ..., j=19, k=20, ...
func letterToRelease(letter string) int {
	if len(letter) == 0 {
		return 0
	}
	ch := strings.ToLower(letter)[0]
	return int(ch-'a') + 10
}

// SpecVersionString returns the version string for DB comparison.
func SpecVersionString(sv *SpecVersion) string {
	if sv.VersionLetter != "" {
		return fmt.Sprintf("%s%d", sv.VersionLetter, sv.VersionMinor)
	}
	return fmt.Sprintf("%d", sv.VersionMinor)
}

// ParseSpecEntry parses a spec list entry like "23_series/23.501/23501-k10.zip".
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
	series := seriesMatch[1]
	specID := specDir

	if match := versionRE.FindStringSubmatch(filename); match != nil {
		letter := strings.ToLower(match[1])
		minor, err := strconv.Atoi(match[2])
		if err != nil {
			return nil
		}
		return &SpecVersion{
			Series:        series,
			SpecID:        specID,
			Filename:      filename,
			VersionLetter: letter,
			VersionMinor:  minor,
			Release:       letterToRelease(letter),
			URL:           baseURL + entry,
		}
	}

	if match := legacyRE.FindStringSubmatch(filename); match != nil {
		minor, err := strconv.Atoi(match[1])
		if err != nil {
			return nil
		}
		return &SpecVersion{
			Series:       series,
			SpecID:       specID,
			Filename:     filename,
			VersionMinor: minor,
			URL:          baseURL + entry,
		}
	}

	return nil
}

// IsNewerVersion compares version strings (e.g., "k10" vs "j60").
func IsNewerVersion(newVer, oldVer string) bool {
	if oldVer == "" {
		return true
	}
	return parseVer(newVer) > parseVer(oldVer)
}

func parseVer(v string) int64 {
	if m := verLetterRE.FindStringSubmatch(v); m != nil {
		major := int64(strings.ToLower(m[1])[0]-'a') + 10
		minor, err := strconv.ParseInt(m[2], 10, 64)
		if err != nil {
			return 0
		}
		return major*10000 + minor
	}
	if m := verNumRE.FindStringSubmatch(v); m != nil {
		n, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
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
