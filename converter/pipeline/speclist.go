package pipeline

import (
	"bufio"
	"cmp"
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
	"sync/atomic"
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
// Returns -1 if the token contains an invalid character, so garbage never
// compares equal to a legitimate all-zero token.
func versionValue(v string) int64 {
	v = strings.ToLower(strings.TrimSpace(v))
	var n int64
	for i := 0; i < len(v); i++ {
		d := base36Digit(v[i])
		if d < 0 {
			return -1
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

// FetchSpecZips fetches zip file entries for a single spec directly,
// avoiding the full 3-level directory scrape.
// specID should be in "23.501" format (with or without dot).
// If useCache is true, results are cached to disk with a 24h TTL.
func FetchSpecZips(ctx context.Context, client *http.Client, specID string, useCache bool) ([]string, error) {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}

	// Normalize: "TS 23.501" or "23501" -> series "23", spec dir "23.501".
	// Callers hand over IDs in either the archive form or the database form.
	normalized := strings.TrimSpace(specID)
	for _, prefix := range []string{"TS ", "TR ", "ts ", "tr "} {
		if rest, ok := strings.CutPrefix(normalized, prefix); ok {
			normalized = strings.TrimSpace(rest)
			break
		}
	}
	if !strings.Contains(normalized, ".") && len(normalized) >= 4 {
		// "23501" -> "23.501"
		normalized = normalized[:2] + "." + normalized[2:]
	}

	// The normalized ID becomes both a cache filename component and a URL
	// path segment, so anything that is not a plain spec directory name
	// (path separators, "..", wildcards) must be rejected here.
	if !specDirRE.MatchString(normalized) {
		return nil, fmt.Errorf("invalid spec ID format: %s", specID)
	}

	parts := strings.SplitN(normalized, ".", 2)

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
		// A listing with no .zip in it is not a real answer: every spec
		// directory in the archive holds at least one version, so an empty
		// result means the response was unusable (an error page served as
		// 200, a redirect, a truncated body). Caching it would pin the spec
		// to "no versions available" for the whole TTL, because an empty
		// cache file counts as a hit and never triggers a re-fetch.
		// FetchSpecList refuses to cache its own partial results for the
		// same reason.
		if len(entries) == 0 {
			log.Printf("warning: no .zip entries found for %s; not caching this empty listing", specDir)
		} else if err := SaveCache(cacheKey, entries); err != nil {
			log.Printf("warning: failed to save cache: %v", err)
		}
	}
	return entries, nil
}

// PartialSpecListError reports that a spec list was assembled while some
// directory listings failed to fetch. The entries returned alongside it are
// valid but incomplete: every spec under a failed directory is missing, so a
// database built from them would silently lack those specs. Callers decide
// whether that is fatal — a build should abort, while an update may proceed
// and merely miss some updates until the next run.
type PartialSpecListError struct {
	// FailedListings is the number of directory listings that failed to fetch.
	FailedListings int
}

func (e *PartialSpecListError) Error() string {
	return fmt.Sprintf("spec list is incomplete: %d directory listing(s) failed to fetch", e.FailedListings)
}

// FetchSpecList scrapes the 3GPP FTP directory for all spec zip files.
// If useCache is true, results are cached to disk with a 24h TTL.
// scrapeConcurrency controls parallel HTTP requests; 0 uses defaultConcurrency().
//
// When some directory listings fail, the entries assembled so far are returned
// together with a *PartialSpecListError, so callers can tell a complete list
// from one that is silently missing specs. A cancelled context is reported as
// ctx.Err() rather than as a partial list.
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
	var fetchFailures atomic.Int64
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
				fetchFailures.Add(1)
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
				fetchFailures.Add(1)
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

	// A list missing the specs whose listings failed to fetch must not be
	// cached: every build within the TTL would silently skip them. The caller
	// gets the partial entries plus an error naming the gap, so it can decide
	// whether to proceed.
	if failed := fetchFailures.Load(); failed > 0 {
		// A cancelled context fails every remaining fetch; that is an abort,
		// not a partial scrape.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		log.Printf("warning: %d directory listing(s) failed to fetch; not caching this partial spec list", failed)
		return entries, &PartialSpecListError{FailedListings: int(failed)}
	}
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

// SpecFilter selects which archive entries a build or download processes.
// The zero value selects everything.
type SpecFilter struct {
	// Release keeps only that release; 0 keeps every release.
	Release int
	// MaxRelease keeps releases at or below it; 0 leaves the selection
	// uncapped. Unlike Release it does not drop a spec that has no version in
	// the named release — combined with LatestOnly the spec falls back to its
	// newest version below the cap.
	MaxRelease int
	Series     []string
	SpecID     string
	// LatestOnly keeps a single version per spec: the newest one that passed
	// the filters above.
	LatestOnly bool
}

// FilterSpecs filters specs by release/series/spec_id and optionally keeps only the latest version.
func FilterSpecs(specs []*SpecVersion, f SpecFilter) []*SpecVersion {
	var filtered []*SpecVersion

	for _, s := range specs {
		if f.Release > 0 && s.Release != f.Release {
			continue
		}
		if f.MaxRelease > 0 && s.Release > f.MaxRelease {
			continue
		}
		if len(f.Series) > 0 && !contains(f.Series, s.Series) {
			continue
		}
		if f.SpecID != "" {
			normalized := strings.ReplaceAll(f.SpecID, ".", "")
			if strings.ReplaceAll(s.SpecID, ".", "") != normalized {
				continue
			}
		}
		filtered = append(filtered, s)
	}

	if f.LatestOnly {
		best := make(map[string]*SpecVersion)
		for _, s := range filtered {
			key := s.SpecID
			if existing, ok := best[key]; !ok || compareVersions(s, existing) > 0 {
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

// compareVersions orders two versions of the same spec: release first, then
// the base-36 value of the token's remainder. Comparing the components keeps
// a large VersionMinor — possible because versionTokenRE puts no upper bound
// on token length — from bleeding into the release, which the previous
// Release*10000+VersionMinor key allowed. The raw token is the final
// tie-break so ties resolve deterministically regardless of input order.
func compareVersions(a, b *SpecVersion) int {
	if c := cmp.Compare(a.Release, b.Release); c != 0 {
		return c
	}
	if c := cmp.Compare(a.VersionMinor, b.VersionMinor); c != 0 {
		return c
	}
	return strings.Compare(a.Version, b.Version)
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
