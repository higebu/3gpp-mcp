// Package versionstore caches specification versions that the prebuilt
// database does not hold.
//
// A build imports one version per spec. Reading a past version therefore means
// downloading and converting it on demand, which takes seconds to minutes. The
// results go into a separate SQLite file so the prebuilt database stays
// read-only, its full-text index keeps covering exactly one version per spec,
// and the cache survives the weekly rebuild that replaces the main database.
package versionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/specver"
	_ "modernc.org/sqlite"
)

// ErrInProgress reports that a fetch did not finish within the caller's budget.
// The fetch keeps running in the background, so repeating the same call later
// returns the content.
var ErrInProgress = errors.New("version fetch still in progress")

// DefaultLimitBytes is the default cache size limit.
const DefaultLimitBytes int64 = 1024 << 20 // 1 GiB

// DefaultBudget is how long a caller waits for a fetch before being told to
// come back. MCP clients time out well before a large spec finishes.
const DefaultBudget = 60 * time.Second

// maxFetchDuration bounds a detached background fetch. Large specs take a few
// minutes to download and convert; anything beyond this is a stalled transfer.
const maxFetchDuration = 30 * time.Minute

// DefaultFileName is the cache file created inside the XDG cache directory.
const DefaultFileName = "versions.db"

// cacheSchemaVersion is stamped into PRAGMA user_version. Opening a cache
// file with a different generation drops every table and starts over: entries
// are re-downloadable, so a wipe is cheaper than migrating. Bump it when the
// stored content becomes incompatible — generation 2 unified the image
// reference notation (![Figure](image://...) for every format).
const cacheSchemaVersion = 2

// schema mirrors the spec, section and image tables of the main database,
// without the full-text index: cached versions must never appear in search
// results. images_fetched distinguishes "images fetched, none found" from
// "never fetched", so a figure-less version does not re-download its ZIP on
// every image call.
const schema = db.SpecTablesSchema + db.ImagesTableSchema + `
CREATE TABLE IF NOT EXISTS cache_entries (
    spec_id TEXT NOT NULL,
    version TEXT NOT NULL,
    bytes INTEGER NOT NULL,
    fetched_at INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    images_fetched INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (spec_id, version)
);

CREATE INDEX IF NOT EXISTS idx_cache_lru ON cache_entries(last_used_at);
`

// Store is a size-bounded cache of converted spec versions.
type Store struct {
	conn         *sql.DB
	limitBytes   int64
	client       *http.Client
	timeout      time.Duration
	fetcher      Fetcher
	imageFetcher ImageFetcher

	mu       sync.Mutex
	inflight map[string]*fetch
}

// Fetcher downloads and converts one archive entry. Only tests set it; the
// zero value uses the real pipeline.
type Fetcher func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error)

// ImageFetcher downloads one archive entry's images. Only tests set it; the
// zero value uses the real pipeline.
type ImageFetcher func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error)

// fetch tracks one in-progress download so concurrent callers asking for the
// same version share a single download instead of racing.
type fetch struct {
	done chan struct{}
	err  error
}

// Options configures a Store.
type Options struct {
	// Path is the cache file. Empty means the default location.
	Path string
	// LimitBytes caps the total size of cached section content. A negative
	// value disables eviction; zero keeps only the most recently fetched
	// version. Callers pass an explicit limit — DefaultLimitBytes is exported
	// for that — rather than relying on zero to mean "default", so that a user
	// asking for a limit of zero gets one.
	LimitBytes int64
	// Client is used for archive downloads. Nil means a default client.
	Client *http.Client
	// Timeout bounds a single archive download. Zero means the pipeline default.
	Timeout time.Duration
	// Fetcher replaces the download-and-convert step. Only tests set it.
	Fetcher Fetcher
	// ImageFetcher replaces the image download step. Only tests set it.
	ImageFetcher ImageFetcher
}

// DefaultPath returns the cache file location inside the XDG cache directory.
func DefaultPath() (string, error) {
	dir, err := pipeline.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DefaultFileName), nil
}

// Open creates or opens the version cache. It returns an error when the file
// cannot be written — a read-only or ephemeral filesystem, for instance — and
// callers are expected to carry on with on-demand fetching disabled rather than
// to treat that as fatal.
func Open(opts Options) (*Store, error) {
	path := opts.Path
	if path == "" {
		p, err := DefaultPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create cache dir: %w", err)
		}
	}

	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open version cache: %w", err)
	}
	// One connection serializes writes and avoids SQLITE_BUSY; the stdio server
	// runs as one process per project, so several may share this file.
	conn.SetMaxOpenConns(1)
	// auto_vacuum only takes effect on a database with no tables yet, so it has
	// to be set before the schema is created.
	for _, pragma := range []string{
		"PRAGMA auto_vacuum=INCREMENTAL",
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := conn.Exec(pragma); err != nil {
			log.Printf("warning: version cache %s failed: %v", pragma, err)
		}
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping version cache: %w", err)
	}
	if err := initSchema(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &Store{
		conn:         conn,
		limitBytes:   opts.LimitBytes,
		client:       opts.Client,
		timeout:      opts.Timeout,
		fetcher:      opts.Fetcher,
		imageFetcher: opts.ImageFetcher,
		inflight:     map[string]*fetch{},
	}, nil
}

// initSchema checks the cache generation and creates the schema in a single
// immediate transaction: several processes may share the file, and taking the
// write lock before reading user_version means each one sees either the
// complete old generation (and wipes it) or the complete, already-stamped new
// one — never the half-dropped state in between.
func initSchema(conn *sql.DB) error {
	ctx := context.Background()
	c, err := conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("init version cache: %w", err)
	}
	defer c.Close()

	if _, err := c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("lock version cache: %w", err)
	}
	if err := migrateAndCreate(ctx, c); err != nil {
		_, _ = c.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if _, err := c.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit version cache schema: %w", err)
	}
	return nil
}

func migrateAndCreate(ctx context.Context, c *sql.Conn) error {
	var generation int
	if err := c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&generation); err != nil {
		return fmt.Errorf("read version cache generation: %w", err)
	}
	if generation != cacheSchemaVersion {
		// A fresh file reads 0 and has nothing to drop; an older-generation
		// file holds content in an incompatible format and starts over.
		for _, table := range []string{"cache_entries", "images", "sections", "specs"} {
			if _, err := c.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
				return fmt.Errorf("reset version cache: %w", err)
			}
		}
		if _, err := c.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", cacheSchemaVersion)); err != nil {
			return fmt.Errorf("stamp version cache generation: %w", err)
		}
	}
	if _, err := c.ExecContext(ctx, schema); err != nil {
		return fmt.Errorf("create version cache schema: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	return s.conn.Close()
}

// Has reports whether a version is already cached.
func (s *Store) Has(specID, version string) (bool, error) {
	var one int
	err := s.conn.QueryRow(
		"SELECT 1 FROM cache_entries WHERE spec_id = ? AND version = ?",
		specID, version,
	).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check version cache: %w", err)
	}
	return true, nil
}

// HasImages reports whether a cached version's images have been fetched. False
// with a nil error also covers a version that is not cached at all.
func (s *Store) HasImages(specID, version string) (bool, error) {
	var fetched int
	err := s.conn.QueryRow(
		"SELECT images_fetched FROM cache_entries WHERE spec_id = ? AND version = ?",
		specID, version,
	).Scan(&fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check image cache: %w", err)
	}
	return fetched != 0, nil
}

// CachedVersions returns the versions of a spec held in the cache, newest
// first.
func (s *Store) CachedVersions(specID string) ([]string, error) {
	rows, err := s.conn.Query(
		"SELECT version FROM cache_entries WHERE spec_id = ?",
		specID,
	)
	if err != nil {
		return nil, fmt.Errorf("list cached versions: %w", err)
	}
	defer rows.Close()

	var versions []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan cached version: %w", err)
		}
		versions = append(versions, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Dotted versions do not order correctly as text ("18.10.0" would sort
	// before "18.6.0"), so sort semantically.
	sort.Slice(versions, func(i, j int) bool {
		return specver.Compare(versions[i], versions[j]) > 0
	})
	return versions, nil
}

// GetSpec returns the cached spec record for a version.
func (s *Store) GetSpec(specID, version string) (*db.Spec, error) {
	var spec db.Spec
	err := s.conn.QueryRow(
		"SELECT id, version, COALESCE(version_token, ''), title, COALESCE(release, ''), COALESCE(series, '') FROM specs WHERE id = ? AND version = ?",
		specID, version,
	).Scan(&spec.ID, &spec.Version, &spec.VersionToken, &spec.Title, &spec.Release, &spec.Series)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached spec: %w", err)
	}
	return &spec, nil
}

// GetTOC returns the section structure of a cached version.
func (s *Store) GetTOC(specID, version string) ([]db.Section, error) {
	s.touch(specID, version)
	return s.querySections(
		"SELECT s.spec_id, s.version, s.number, s.title, s.level, COALESCE(s.parent_number, ''), '', COALESCE(p.release, '') "+
			"FROM sections s LEFT JOIN specs p ON p.id = s.spec_id AND p.version = s.version "+
			"WHERE s.spec_id = ? AND s.version = ? ORDER BY s.id",
		specID, version,
	)
}

// AllSections returns every section of a cached version, content included, in
// document order.
func (s *Store) AllSections(specID, version string) ([]db.Section, error) {
	s.touch(specID, version)
	return s.querySections(
		"SELECT s.spec_id, s.version, s.number, s.title, s.level, COALESCE(s.parent_number, ''), s.content, COALESCE(p.release, '') "+
			"FROM sections s LEFT JOIN specs p ON p.id = s.spec_id AND p.version = s.version "+
			"WHERE s.spec_id = ? AND s.version = ? ORDER BY s.id",
		specID, version,
	)
}

// GetSection returns a cached section, optionally with its subsections.
func (s *Store) GetSection(specID, version, number string, includeSubsections bool) ([]db.Section, error) {
	s.touch(specID, version)
	const projection = "SELECT s.spec_id, s.version, s.number, s.title, s.level, COALESCE(s.parent_number, ''), s.content, COALESCE(p.release, '') " +
		"FROM sections s LEFT JOIN specs p ON p.id = s.spec_id AND p.version = s.version WHERE s.spec_id = ? AND s.version = ?"
	if includeSubsections {
		return s.querySections(
			projection+" AND (s.number = ? OR s.number LIKE ? || '.%' ESCAPE '\\') ORDER BY s.id",
			specID, version, number, db.EscapeLikePattern(number),
		)
	}
	return s.querySections(projection+" AND s.number = ?", specID, version, number)
}

// GetImage returns a cached image, or nil when the version holds no image of
// that name. EMF/WMF images are renamed to .png when they are converted during
// the fetch, while cached section Markdown keeps naming the original file, so
// a missed exact match falls back to matching the name without its extension.
func (s *Store) GetImage(specID, version, name string) (*db.Image, error) {
	s.touch(specID, version)
	const projection = "SELECT spec_id, version, name, mime_type, data, llm_readable FROM images WHERE spec_id = ? AND version = ?"

	var img db.Image
	err := s.conn.QueryRow(projection+" AND name = ?", specID, version, name).
		Scan(&img.SpecID, &img.Version, &img.Name, &img.MIMEType, &img.Data, &img.LLMReadable)
	if err == nil {
		return &img, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get cached image: %w", err)
	}

	base := strings.TrimSuffix(name, filepath.Ext(name))
	if base == "" || base == name {
		return nil, nil
	}
	// Prefer a readable row, then order by name, so several images sharing a
	// base name resolve the same way on every call.
	err = s.conn.QueryRow(
		projection+" AND name LIKE ? ESCAPE '\\' ORDER BY llm_readable DESC, name LIMIT 1",
		specID, version, db.EscapeLikePattern(base)+".%",
	).Scan(&img.SpecID, &img.Version, &img.Name, &img.MIMEType, &img.Data, &img.LLMReadable)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached image: %w", err)
	}
	return &img, nil
}

// ListImages returns metadata for a cached version's images, without the
// binary data.
func (s *Store) ListImages(specID, version string) ([]db.ImageInfo, error) {
	s.touch(specID, version)
	rows, err := s.conn.Query(
		"SELECT spec_id, version, name, mime_type, llm_readable FROM images WHERE spec_id = ? AND version = ? ORDER BY name",
		specID, version,
	)
	if err != nil {
		return nil, fmt.Errorf("list cached images: %w", err)
	}
	defer rows.Close()

	var infos []db.ImageInfo
	for rows.Next() {
		var info db.ImageInfo
		if err := rows.Scan(&info.SpecID, &info.Version, &info.Name, &info.MIMEType, &info.LLMReadable); err != nil {
			return nil, fmt.Errorf("scan cached image info: %w", err)
		}
		infos = append(infos, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list cached images: iterate: %w", err)
	}
	return infos, nil
}

func (s *Store) querySections(query string, args ...any) ([]db.Section, error) {
	rows, err := s.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query cached sections: %w", err)
	}
	defer rows.Close()

	var sections []db.Section
	for rows.Next() {
		var sec db.Section
		if err := rows.Scan(&sec.SpecID, &sec.Version, &sec.Number, &sec.Title, &sec.Level, &sec.ParentNumber, &sec.Content, &sec.Release); err != nil {
			return nil, fmt.Errorf("scan cached section: %w", err)
		}
		sections = append(sections, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query cached sections: iterate: %w", err)
	}
	return sections, nil
}

// touch records a use for LRU ordering. A failure here only costs eviction
// accuracy, so it is logged rather than propagated to the read path.
func (s *Store) touch(specID, version string) {
	_, err := s.conn.Exec(
		"UPDATE cache_entries SET last_used_at = ? WHERE spec_id = ? AND version = ?",
		time.Now().Unix(), specID, version,
	)
	if err != nil {
		log.Printf("warning: version cache touch failed for %s v%s: %v", specID, version, err)
	}
}

// Ensure makes a version available in the cache, downloading and converting it
// when necessary.
//
// A fetch that outlives budget keeps running on a context detached from the
// caller's, and Ensure returns ErrInProgress. Repeating the call later joins
// the same fetch and eventually returns the cached content. Concurrent callers
// asking for the same version share one download.
// specID and version are the keys callers will look the result up by; they win
// over whatever the downloaded document says about itself, which for legacy
// specs is often wrong or missing.
func (s *Store) Ensure(ctx context.Context, specID, version string, sv *pipeline.SpecVersion, budget time.Duration) error {
	cached, err := s.Has(specID, version)
	if err != nil {
		return err
	}
	if cached {
		return nil
	}
	if budget <= 0 {
		budget = DefaultBudget
	}

	key := specID + "@" + version
	s.mu.Lock()
	f, running := s.inflight[key]
	if !running {
		// Re-check under the lock: a fetch that completed between the Has
		// call above and here has already been removed from inflight, and
		// starting a fresh download for it would repeat minutes of work.
		if cached, err := s.Has(specID, version); err != nil || cached {
			s.mu.Unlock()
			return err
		}
		f = &fetch{done: make(chan struct{})}
		s.inflight[key] = f
		// The fetch outlives this request on purpose: a caller that gives up
		// waiting should not throw away minutes of download and conversion.
		// It still gets a generous deadline: the download path has no overall
		// timeout of its own, so a stalled connection would otherwise keep
		// this inflight entry - and every future caller - stuck forever.
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), maxFetchDuration)
		go func() {
			defer cancel()
			s.run(fetchCtx, key, specID, version, sv, f)
		}()
	}
	s.mu.Unlock()

	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-f.done:
		return f.err
	case <-timer.C:
		return fmt.Errorf("%w: %s v%s", ErrInProgress, specID, version)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run performs one fetch and publishes its outcome to everyone waiting on it.
func (s *Store) run(ctx context.Context, key, specID, version string, sv *pipeline.SpecVersion, f *fetch) {
	defer func() {
		s.mu.Lock()
		delete(s.inflight, key)
		s.mu.Unlock()
		close(f.done)
	}()

	spec, sections, err := s.fetch(ctx, sv)
	if err != nil {
		f.err = err
		return
	}
	spec.ID, spec.Version = specID, version
	for i := range sections {
		sections[i].SpecID = specID
		sections[i].Version = version
	}
	if err := s.put(spec, sections); err != nil {
		f.err = err
	}
}

// fetch downloads and converts one archive entry. It is a field so tests can
// supply documents without reaching the network.
func (s *Store) fetch(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
	if s.fetcher != nil {
		return s.fetcher(ctx, sv)
	}
	return pipeline.FetchVersion(ctx, s.client, sv, s.timeout)
}

// put writes a fetched version into the cache and enforces the size limit.
func (s *Store) put(spec db.Spec, sections []db.Section) error {
	tx, err := s.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin cache transaction: %w", err)
	}
	defer tx.Rollback() // no-op after Commit per database/sql docs

	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO specs (id, version, version_token, title, release, series) VALUES (?, ?, ?, ?, ?, ?)",
		spec.ID, spec.Version, spec.VersionToken, spec.Title, spec.Release, spec.Series,
	); err != nil {
		return fmt.Errorf("cache spec: %w", err)
	}

	if _, err := tx.Exec("DELETE FROM sections WHERE spec_id = ? AND version = ?", spec.ID, spec.Version); err != nil {
		return fmt.Errorf("clear cached sections: %w", err)
	}
	// The REPLACE of cache_entries below resets images_fetched to zero, so any
	// image rows from an earlier life of this version — possible when several
	// processes share the cache file — must go with it, or their bytes would
	// escape the account and the flag would deny they exist.
	if _, err := tx.Exec("DELETE FROM images WHERE spec_id = ? AND version = ?", spec.ID, spec.Version); err != nil {
		return fmt.Errorf("clear cached images: %w", err)
	}
	stmt, err := tx.Prepare(
		"INSERT INTO sections (spec_id, version, number, title, level, parent_number, content) VALUES (?, ?, ?, ?, ?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare cache insert: %w", err)
	}
	defer stmt.Close()

	var bytes int64
	for _, sec := range sections {
		if _, err := stmt.Exec(sec.SpecID, sec.Version, sec.Number, sec.Title, sec.Level, sec.ParentNumber, sec.Content); err != nil {
			return fmt.Errorf("cache section: %w", err)
		}
		bytes += int64(len(sec.Content)) + int64(len(sec.Title))
	}

	now := time.Now().Unix()
	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO cache_entries (spec_id, version, bytes, fetched_at, last_used_at) VALUES (?, ?, ?, ?, ?)",
		spec.ID, spec.Version, bytes, now, now,
	); err != nil {
		return fmt.Errorf("record cache entry: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cache transaction: %w", err)
	}
	// The version is cached and readable at this point, so an eviction
	// failure must not be reported as a failed fetch: it only costs cache
	// size accuracy.
	if err := s.evict(spec.ID, spec.Version); err != nil {
		log.Printf("warning: version cache eviction failed: %v", err)
	}
	return nil
}

// EnsureImages makes a cached version's images available, downloading them when
// necessary. The version's sections must already be cached — image fetching is
// only reachable through a resolved version — and like Ensure, a fetch that
// outlives budget keeps running detached and reports ErrInProgress, so
// repeating the call later returns the cached images.
func (s *Store) EnsureImages(ctx context.Context, specID, version string, sv *pipeline.SpecVersion, budget time.Duration) error {
	fetched, err := s.HasImages(specID, version)
	if err != nil {
		return err
	}
	if fetched {
		return nil
	}
	if budget <= 0 {
		budget = DefaultBudget
	}

	// The "#" keeps this key disjoint from Ensure's section keys, so a section
	// fetch and an image fetch of the same version never share an entry.
	key := specID + "@" + version + "#images"
	s.mu.Lock()
	f, running := s.inflight[key]
	if !running {
		// Re-check under the lock: a fetch that completed between the HasImages
		// call above and here has already been removed from inflight, and
		// starting a fresh download for it would repeat minutes of work.
		if fetched, err := s.HasImages(specID, version); err != nil || fetched {
			s.mu.Unlock()
			return err
		}
		f = &fetch{done: make(chan struct{})}
		s.inflight[key] = f
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), maxFetchDuration)
		go func() {
			defer cancel()
			s.runImages(fetchCtx, key, specID, version, sv, f)
		}()
	}
	s.mu.Unlock()

	timer := time.NewTimer(budget)
	defer timer.Stop()
	select {
	case <-f.done:
		return f.err
	case <-timer.C:
		return fmt.Errorf("%w: images for %s v%s", ErrInProgress, specID, version)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// runImages performs one image fetch and publishes its outcome to everyone
// waiting on it.
func (s *Store) runImages(ctx context.Context, key, specID, version string, sv *pipeline.SpecVersion, f *fetch) {
	defer func() {
		s.mu.Lock()
		delete(s.inflight, key)
		s.mu.Unlock()
		close(f.done)
	}()

	images, err := s.fetchImages(ctx, sv)
	if err != nil {
		f.err = err
		return
	}
	for i := range images {
		images[i].SpecID = specID
		images[i].Version = version
	}
	if err := s.putImages(specID, version, images); err != nil {
		f.err = err
	}
}

// fetchImages downloads one archive entry's images. It is a field so tests can
// supply images without reaching the network.
func (s *Store) fetchImages(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
	if s.imageFetcher != nil {
		return s.imageFetcher(ctx, sv)
	}
	return pipeline.FetchVersionImages(ctx, s.client, sv, s.timeout)
}

// putImages writes a version's images into the cache and enforces the size
// limit. Image bytes count against the same cache entry as the sections, so
// eviction drops a version's text and figures as one unit.
func (s *Store) putImages(specID, version string, images []db.Image) error {
	tx, err := s.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin cache transaction: %w", err)
	}
	defer tx.Rollback() // no-op after Commit per database/sql docs

	// Replacing an earlier image set must not leave its bytes in the account.
	var oldBytes int64
	if err := tx.QueryRow(
		"SELECT COALESCE(SUM(LENGTH(data) + LENGTH(name)), 0) FROM images WHERE spec_id = ? AND version = ?",
		specID, version,
	).Scan(&oldBytes); err != nil {
		return fmt.Errorf("measure cached images: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM images WHERE spec_id = ? AND version = ?", specID, version); err != nil {
		return fmt.Errorf("clear cached images: %w", err)
	}

	stmt, err := tx.Prepare(
		"INSERT INTO images (spec_id, version, name, mime_type, data, llm_readable) VALUES (?, ?, ?, ?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare image insert: %w", err)
	}
	defer stmt.Close()

	var bytes int64
	for _, img := range images {
		if _, err := stmt.Exec(img.SpecID, img.Version, img.Name, img.MIMEType, img.Data, img.LLMReadable); err != nil {
			return fmt.Errorf("cache image: %w", err)
		}
		bytes += int64(len(img.Data)) + int64(len(img.Name))
	}

	res, err := tx.Exec(
		"UPDATE cache_entries SET bytes = bytes + ?, images_fetched = 1 WHERE spec_id = ? AND version = ?",
		bytes-oldBytes, specID, version,
	)
	if err != nil {
		return fmt.Errorf("record image bytes: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("record image bytes: %w", err)
	} else if n == 0 {
		// The version was evicted while its images downloaded. Committing would
		// orphan the blobs and break the invariant that a cache entry always
		// accompanies cached rows, so give up; the next call re-fetches the
		// sections first.
		return fmt.Errorf("%s v%s was evicted while its images downloaded; call the tool again", specID, version)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cache transaction: %w", err)
	}
	// The images are cached and readable at this point, so an eviction failure
	// must not be reported as a failed fetch: it only costs cache size accuracy.
	if err := s.evict(specID, version); err != nil {
		log.Printf("warning: version cache eviction failed: %v", err)
	}
	return nil
}

// evict drops least-recently-used versions until the cache fits its limit. The
// entry named by keepSpecID/keepVersion is never dropped: it was just fetched,
// and evicting it would make the fetch pointless when a single spec is larger
// than the whole limit.
func (s *Store) evict(keepSpecID, keepVersion string) error {
	if s.limitBytes < 0 {
		return nil
	}

	for {
		var total sql.NullInt64
		if err := s.conn.QueryRow("SELECT SUM(bytes) FROM cache_entries").Scan(&total); err != nil {
			return fmt.Errorf("measure version cache: %w", err)
		}
		if !total.Valid || total.Int64 <= s.limitBytes {
			return nil
		}

		var specID, version string
		err := s.conn.QueryRow(
			"SELECT spec_id, version FROM cache_entries WHERE NOT (spec_id = ? AND version = ?) ORDER BY last_used_at, rowid LIMIT 1",
			keepSpecID, keepVersion,
		).Scan(&specID, &version)
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("version cache: %s v%s alone exceeds the %d byte limit; keeping it", keepSpecID, keepVersion, s.limitBytes)
			return nil
		}
		if err != nil {
			return fmt.Errorf("pick eviction victim: %w", err)
		}
		if err := s.delete(specID, version); err != nil {
			return err
		}
		log.Printf("version cache: evicted %s v%s", specID, version)
	}
}

// delete removes one cached version and returns its pages to the filesystem.
func (s *Store) delete(specID, version string) error {
	tx, err := s.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin eviction: %w", err)
	}
	defer tx.Rollback()

	for _, q := range []string{
		"DELETE FROM sections WHERE spec_id = ? AND version = ?",
		"DELETE FROM images WHERE spec_id = ? AND version = ?",
		"DELETE FROM specs WHERE id = ? AND version = ?",
		"DELETE FROM cache_entries WHERE spec_id = ? AND version = ?",
	} {
		if _, err := tx.Exec(q, specID, version); err != nil {
			return fmt.Errorf("evict %s v%s: %w", specID, version, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit eviction: %w", err)
	}

	// Without this the file keeps the freed pages and never shrinks.
	if _, err := s.conn.Exec("PRAGMA incremental_vacuum"); err != nil {
		log.Printf("warning: version cache incremental_vacuum failed: %v", err)
	}
	return nil
}
