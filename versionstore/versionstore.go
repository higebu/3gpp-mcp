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
	"sync"
	"time"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
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

// DefaultFileName is the cache file created inside the XDG cache directory.
const DefaultFileName = "versions.db"

// schema mirrors the spec and section tables of the main database, without the
// full-text index: cached versions must never appear in search results.
const schema = db.SpecTablesSchema + `
CREATE TABLE IF NOT EXISTS cache_entries (
    spec_id TEXT NOT NULL,
    version TEXT NOT NULL,
    bytes INTEGER NOT NULL,
    fetched_at INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL,
    PRIMARY KEY (spec_id, version)
);

CREATE INDEX IF NOT EXISTS idx_cache_lru ON cache_entries(last_used_at);
`

// Store is a size-bounded cache of converted spec versions.
type Store struct {
	conn       *sql.DB
	limitBytes int64
	client     *http.Client
	timeout    time.Duration
	fetcher    Fetcher

	mu       sync.Mutex
	inflight map[string]*fetch
}

// Fetcher downloads and converts one archive entry. Only tests set it; the
// zero value uses the real pipeline.
type Fetcher func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error)

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
	if _, err := conn.Exec(schema); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("create version cache schema: %w", err)
	}

	return &Store{
		conn:       conn,
		limitBytes: opts.LimitBytes,
		client:     opts.Client,
		timeout:    opts.Timeout,
		fetcher:    opts.Fetcher,
		inflight:   map[string]*fetch{},
	}, nil
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

// CachedVersions returns the versions of a spec held in the cache.
func (s *Store) CachedVersions(specID string) ([]string, error) {
	rows, err := s.conn.Query(
		"SELECT version FROM cache_entries WHERE spec_id = ? ORDER BY version",
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
	return versions, rows.Err()
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

// GetSection returns a cached section, optionally with its subsections.
func (s *Store) GetSection(specID, version, number string, includeSubsections bool) ([]db.Section, error) {
	s.touch(specID, version)
	const projection = "SELECT s.spec_id, s.version, s.number, s.title, s.level, COALESCE(s.parent_number, ''), s.content, COALESCE(p.release, '') " +
		"FROM sections s LEFT JOIN specs p ON p.id = s.spec_id AND p.version = s.version WHERE s.spec_id = ? AND s.version = ?"
	if includeSubsections {
		return s.querySections(
			projection+" AND (s.number = ? OR s.number LIKE ? || '.%') ORDER BY s.id",
			specID, version, number, number,
		)
	}
	return s.querySections(projection+" AND s.number = ?", specID, version, number)
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
		f = &fetch{done: make(chan struct{})}
		s.inflight[key] = f
		// The fetch outlives this request on purpose: a caller that gives up
		// waiting should not throw away minutes of download and conversion.
		go s.run(context.WithoutCancel(ctx), key, specID, version, sv, f)
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
	return s.evict(spec.ID, spec.Version)
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
