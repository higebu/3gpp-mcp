// Package tdocstore caches meeting documents (TDocs) fetched on demand from
// the 3GPP FTP site.
//
// TDocs never enter the prebuilt database: its tables are keyed by
// specification and version, and its full-text index must cover exactly one
// version per specification. A meeting document is instead downloaded and
// converted on first use and kept here, in a separate size-bounded SQLite
// file next to the version cache, with no full-text index.
package tdocstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/higebu/3gpp-mcp/internal/converter/pipeline"
	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/ondemand"
	"github.com/higebu/3gpp-mcp/internal/tdoc"
	_ "modernc.org/sqlite"
)

// ErrInProgress reports that a fetch did not finish within the caller's
// budget. The fetch keeps running in the background, so repeating the same
// call later returns the content.
var ErrInProgress = ondemand.ErrInProgress

// DefaultLimitBytes is the default cache size limit.
const DefaultLimitBytes int64 = 512 << 20 // 512 MiB

// DefaultFileName is the cache file created inside the XDG cache directory.
const DefaultFileName = "tdocs.db"

// cacheSchemaVersion is stamped into PRAGMA user_version. Opening a cache
// file with a different generation drops every table and starts over:
// entries are re-downloadable, so a wipe is cheaper than migrating. Bump it
// when the stored content becomes incompatible — it tracks the converter,
// like versionstore.cacheSchemaVersion, and the tables here.
const cacheSchemaVersion = 1

const schema = `
CREATE TABLE IF NOT EXISTS tdocs (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    path TEXT NOT NULL,
    group_name TEXT NOT NULL DEFAULT '',
    meeting_code TEXT NOT NULL DEFAULT '',
    meeting_title TEXT NOT NULL DEFAULT '',
    meeting_dir TEXT NOT NULL DEFAULT '',
    main_file TEXT NOT NULL,
    files TEXT NOT NULL,
    bytes INTEGER NOT NULL,
    fetched_at INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tdocs_lru ON tdocs(last_used_at);

CREATE TABLE IF NOT EXISTS tdoc_sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tdoc_id TEXT NOT NULL,
    number TEXT NOT NULL,
    title TEXT NOT NULL,
    level INTEGER NOT NULL,
    parent_number TEXT,
    content TEXT NOT NULL,
    UNIQUE(tdoc_id, number)
);

CREATE TABLE IF NOT EXISTS tdoc_images (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tdoc_id TEXT NOT NULL,
    name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    data BLOB NOT NULL,
    llm_readable BOOLEAN NOT NULL DEFAULT 0,
    UNIQUE(tdoc_id, name)
);
`

// Document is a cached meeting document's record.
type Document struct {
	// ID is the TDoc number ("R1-2509715"), or the FTP path for a document
	// that was named by path.
	ID    string `json:"id"`
	Title string `json:"title"`
	// Path is the download location relative to https://www.3gpp.org/ftp/.
	Path string `json:"path"`
	// Group, MeetingCode, MeetingTitle and MeetingDir describe the meeting
	// the document belongs to; empty for a document named by path.
	Group        string `json:"group,omitempty"`
	MeetingCode  string `json:"meeting_code,omitempty"`
	MeetingTitle string `json:"meeting_title,omitempty"`
	MeetingDir   string `json:"meeting_dir,omitempty"`
	// MainFile is the file inside the download that was converted; Files
	// lists every file in it, attachments included.
	MainFile  string    `json:"main_file"`
	Files     []string  `json:"files"`
	FetchedAt time.Time `json:"fetched_at"`
}

// URL is the document's download URL.
func (d Document) URL() string {
	return tdoc.Document{Path: d.Path}.URL()
}

// Fetcher downloads and converts one document. Only tests set it; the zero
// value uses the real pipeline.
type Fetcher func(ctx context.Context, doc tdoc.Document) (*tdoc.Fetched, error)

// Options configures a Store.
type Options struct {
	// Path is the cache file. Empty means the default location.
	Path string
	// LimitBytes caps the total size of cached content. A negative value
	// disables eviction; zero keeps only the most recently fetched document.
	LimitBytes int64
	// Client is used for downloads. Nil means a default client. Downloads
	// are bounded by the detached fetch's own deadline
	// (ondemand.DefaultMaxDuration), not by a client timeout.
	Client *http.Client
	// Fetcher replaces the download-and-convert step. Only tests set it.
	Fetcher Fetcher
	// ListFetcher replaces the TDoc list download. Only tests set it.
	ListFetcher ListFetcher
}

// Store is a size-bounded cache of converted meeting documents.
type Store struct {
	conn        *sql.DB
	limitBytes  int64
	client      *http.Client
	fetcher     Fetcher
	listFetcher ListFetcher
	group       ondemand.Group

	// mu serializes put against evict so a document is never evicted
	// between its insert and the eviction pass that must keep it.
	mu sync.Mutex
}

// DefaultPath returns the cache file location inside the XDG cache directory.
func DefaultPath() (string, error) {
	dir, err := pipeline.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, DefaultFileName), nil
}

// Open creates or opens the cache. It returns an error when the file cannot
// be written — a read-only or ephemeral filesystem, for instance — and
// callers are expected to carry on with TDoc fetching disabled rather than
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
		return nil, fmt.Errorf("open tdoc cache: %w", err)
	}
	// One connection serializes writes and avoids SQLITE_BUSY; several
	// processes may share this file.
	conn.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA auto_vacuum=INCREMENTAL",
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := conn.Exec(pragma); err != nil {
			log.Printf("warning: tdoc cache %s failed: %v", pragma, err)
		}
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping tdoc cache: %w", err)
	}
	if err := initSchema(conn); err != nil {
		_ = conn.Close()
		return nil, err
	}

	return &Store{
		conn:        conn,
		limitBytes:  opts.LimitBytes,
		client:      opts.Client,
		fetcher:     opts.Fetcher,
		listFetcher: opts.ListFetcher,
	}, nil
}

// initSchema checks the cache generation and creates the schema in one
// immediate transaction, so concurrent openers see either the complete old
// generation (and wipe it) or the complete new one.
func initSchema(conn *sql.DB) error {
	ctx := context.Background()
	c, err := conn.Conn(ctx)
	if err != nil {
		return fmt.Errorf("init tdoc cache: %w", err)
	}
	defer c.Close()

	if _, err := c.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("lock tdoc cache: %w", err)
	}
	if err := migrateAndCreate(ctx, c); err != nil {
		_, _ = c.ExecContext(ctx, "ROLLBACK")
		return err
	}
	if _, err := c.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit tdoc cache schema: %w", err)
	}
	return nil
}

func migrateAndCreate(ctx context.Context, c *sql.Conn) error {
	var generation int
	if err := c.QueryRowContext(ctx, "PRAGMA user_version").Scan(&generation); err != nil {
		return fmt.Errorf("read tdoc cache generation: %w", err)
	}
	if generation != cacheSchemaVersion {
		for _, table := range append(listTables, "tdoc_images", "tdoc_sections", "tdocs") {
			if _, err := c.ExecContext(ctx, "DROP TABLE IF EXISTS "+table); err != nil {
				return fmt.Errorf("reset tdoc cache: %w", err)
			}
		}
		if _, err := c.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", cacheSchemaVersion)); err != nil {
			return fmt.Errorf("stamp tdoc cache generation: %w", err)
		}
	}
	for _, ddl := range []string{schema, listSchema} {
		if _, err := c.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("create tdoc cache schema: %w", err)
		}
	}
	return nil
}

func (s *Store) Close() error {
	return s.conn.Close()
}

// Has reports whether a document is cached.
func (s *Store) Has(id string) (bool, error) {
	var one int
	err := s.conn.QueryRow("SELECT 1 FROM tdocs WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check tdoc cache: %w", err)
	}
	return true, nil
}

// Ensure makes a document available in the cache, downloading and
// converting it when necessary. A fetch that outlives budget keeps running
// detached and Ensure returns ErrInProgress; repeating the call later joins
// it. Concurrent callers asking for the same document share one download.
func (s *Store) Ensure(ctx context.Context, doc tdoc.Document, budget time.Duration) error {
	cached, err := s.Has(doc.ID)
	if err != nil || cached {
		return err
	}
	return s.group.Do(ctx, doc.ID, budget, func() (bool, error) { return s.Has(doc.ID) }, func(ctx context.Context) error {
		fetched, err := s.fetch(ctx, doc)
		if err != nil {
			return err
		}
		return s.put(doc, fetched)
	})
}

func (s *Store) fetch(ctx context.Context, doc tdoc.Document) (*tdoc.Fetched, error) {
	if s.fetcher != nil {
		return s.fetcher(ctx, doc)
	}
	return tdoc.Fetch(ctx, s.client, doc)
}

// put writes a fetched document into the cache and enforces the size limit.
func (s *Store) put(doc tdoc.Document, f *tdoc.Fetched) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin cache transaction: %w", err)
	}
	defer tx.Rollback() // no-op after Commit per database/sql docs

	for _, q := range []string{
		"DELETE FROM tdoc_sections WHERE tdoc_id = ?",
		"DELETE FROM tdoc_images WHERE tdoc_id = ?",
	} {
		if _, err := tx.Exec(q, doc.ID); err != nil {
			return fmt.Errorf("clear cached tdoc: %w", err)
		}
	}

	var bytes int64
	sectionStmt, err := tx.Prepare("INSERT INTO tdoc_sections (tdoc_id, number, title, level, parent_number, content) VALUES (?, ?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare section insert: %w", err)
	}
	defer sectionStmt.Close()
	for _, sec := range dedupeNumbers(f.Sections) {
		if _, err := sectionStmt.Exec(doc.ID, sec.Number, sec.Title, sec.Level, sec.ParentNumber, sec.Content); err != nil {
			return fmt.Errorf("cache section: %w", err)
		}
		bytes += int64(len(sec.Content)) + int64(len(sec.Title))
	}

	imageStmt, err := tx.Prepare("INSERT INTO tdoc_images (tdoc_id, name, mime_type, data, llm_readable) VALUES (?, ?, ?, ?, ?)")
	if err != nil {
		return fmt.Errorf("prepare image insert: %w", err)
	}
	defer imageStmt.Close()
	for _, img := range f.Images {
		if _, err := imageStmt.Exec(doc.ID, img.Name, img.MIMEType, img.Data, img.LLMReadable); err != nil {
			return fmt.Errorf("cache image: %w", err)
		}
		bytes += int64(len(img.Data)) + int64(len(img.Name))
	}

	// A document is always shown under some title; the ID is the fallback.
	title := f.Title
	if title == "" {
		title = doc.ID
	}
	now := time.Now().Unix()
	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO tdocs (id, title, path, group_name, meeting_code, meeting_title, meeting_dir, main_file, files, bytes, fetched_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		doc.ID, title, doc.Path, doc.Group.Name, doc.Meeting.Code, doc.Meeting.Title, doc.Meeting.Dir,
		f.MainFile, strings.Join(f.Files, "\n"), bytes, now, now,
	); err != nil {
		return fmt.Errorf("record cache entry: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cache transaction: %w", err)
	}
	// The document is cached and readable at this point, so an eviction
	// failure must not be reported as a failed fetch.
	if err := s.evict(doc.ID); err != nil {
		log.Printf("warning: tdoc cache eviction failed: %v", err)
	}
	return nil
}

// dedupeNumbers makes section numbers unique within a document. Meeting
// documents repeat unnumbered headings ("Agreement", "Conclusion"), whose
// number is their title; a repeat gets " (2)", " (3)", ... appended so every
// section stays addressable. The suffixed number is checked against every
// number already taken — a heading literally titled "Note (2)" exists in the
// wild — so the UNIQUE(tdoc_id, number) constraint never fails the insert.
func dedupeNumbers(sections []db.Section) []db.Section {
	used := make(map[string]bool, len(sections))
	out := make([]db.Section, len(sections))
	for i, sec := range sections {
		if used[sec.Number] {
			for n := 2; ; n++ {
				candidate := fmt.Sprintf("%s (%d)", sec.Number, n)
				if !used[candidate] {
					sec.Number = candidate
					break
				}
			}
		}
		used[sec.Number] = true
		out[i] = sec
	}
	return out
}

// Get returns a cached document's record, or nil when it is not cached.
func (s *Store) Get(id string) (*Document, error) {
	s.touch(id)
	var d Document
	var files string
	var fetched int64
	err := s.conn.QueryRow(
		"SELECT id, title, path, group_name, meeting_code, meeting_title, meeting_dir, main_file, files, fetched_at FROM tdocs WHERE id = ?", id,
	).Scan(&d.ID, &d.Title, &d.Path, &d.Group, &d.MeetingCode, &d.MeetingTitle, &d.MeetingDir, &d.MainFile, &files, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached tdoc: %w", err)
	}
	if files != "" {
		d.Files = strings.Split(files, "\n")
	}
	d.FetchedAt = time.Unix(fetched, 0)
	return &d, nil
}

const sectionColumns = "tdoc_id, number, title, level, COALESCE(parent_number, ''), content"

// GetTOC returns a cached document's section structure, content omitted.
func (s *Store) GetTOC(id string) ([]db.Section, error) {
	s.touch(id)
	return s.querySections("SELECT tdoc_id, number, title, level, COALESCE(parent_number, ''), '' FROM tdoc_sections WHERE tdoc_id = ? ORDER BY id", id)
}

// AllSections returns every section of a cached document, in document order.
func (s *Store) AllSections(id string) ([]db.Section, error) {
	s.touch(id)
	return s.querySections("SELECT "+sectionColumns+" FROM tdoc_sections WHERE tdoc_id = ? ORDER BY id", id)
}

// GetSection returns one section, optionally with its subsections.
func (s *Store) GetSection(id, number string, includeSubsections bool) ([]db.Section, error) {
	s.touch(id)
	if includeSubsections && number != "" {
		return s.querySections(
			"SELECT "+sectionColumns+" FROM tdoc_sections WHERE tdoc_id = ? AND (number = ? OR number LIKE ? || '.%' ESCAPE '\\') ORDER BY id",
			id, number, db.EscapeLikePattern(number),
		)
	}
	return s.querySections("SELECT "+sectionColumns+" FROM tdoc_sections WHERE tdoc_id = ? AND number = ?", id, number)
}

func (s *Store) querySections(query string, args ...any) ([]db.Section, error) {
	rows, err := s.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query cached tdoc sections: %w", err)
	}
	defer rows.Close()

	var sections []db.Section
	for rows.Next() {
		var sec db.Section
		if err := rows.Scan(&sec.SpecID, &sec.Number, &sec.Title, &sec.Level, &sec.ParentNumber, &sec.Content); err != nil {
			return nil, fmt.Errorf("scan cached tdoc section: %w", err)
		}
		sections = append(sections, sec)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query cached tdoc sections: iterate: %w", err)
	}
	return sections, nil
}

// GetImage returns a cached image, or nil when the document holds no image
// of that name. As in the version cache, an EMF/WMF figure converted to PNG
// is found by its original name too.
func (s *Store) GetImage(id, name string) (*db.Image, error) {
	s.touch(id)
	const projection = "SELECT tdoc_id, name, mime_type, data, llm_readable FROM tdoc_images WHERE tdoc_id = ?"
	var img db.Image
	err := s.conn.QueryRow(projection+" AND name = ?", id, name).
		Scan(&img.SpecID, &img.Name, &img.MIMEType, &img.Data, &img.LLMReadable)
	if err == nil {
		return &img, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("get cached tdoc image: %w", err)
	}
	base := strings.TrimSuffix(name, filepath.Ext(name))
	if base == "" || base == name {
		return nil, nil
	}
	err = s.conn.QueryRow(
		projection+" AND name LIKE ? ESCAPE '\\' ORDER BY llm_readable DESC, name LIMIT 1",
		id, db.EscapeLikePattern(base)+".%",
	).Scan(&img.SpecID, &img.Name, &img.MIMEType, &img.Data, &img.LLMReadable)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached tdoc image: %w", err)
	}
	return &img, nil
}

// ListImages returns a cached document's image metadata.
func (s *Store) ListImages(id string) ([]db.ImageInfo, error) {
	s.touch(id)
	rows, err := s.conn.Query("SELECT tdoc_id, name, mime_type, llm_readable FROM tdoc_images WHERE tdoc_id = ? ORDER BY name", id)
	if err != nil {
		return nil, fmt.Errorf("list cached tdoc images: %w", err)
	}
	defer rows.Close()
	var infos []db.ImageInfo
	for rows.Next() {
		var info db.ImageInfo
		if err := rows.Scan(&info.SpecID, &info.Name, &info.MIMEType, &info.LLMReadable); err != nil {
			return nil, fmt.Errorf("scan cached tdoc image: %w", err)
		}
		infos = append(infos, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list cached tdoc images: iterate: %w", err)
	}
	return infos, nil
}

// touch records a use for LRU ordering. A failure only costs eviction
// accuracy, so it is logged rather than propagated.
func (s *Store) touch(id string) {
	if _, err := s.conn.Exec("UPDATE tdocs SET last_used_at = ? WHERE id = ?", time.Now().Unix(), id); err != nil {
		log.Printf("warning: tdoc cache touch failed for %s: %v", id, err)
	}
}

// evict drops least-recently-used documents until the cache fits its limit.
// keepID, just fetched, is never dropped: evicting it would make the fetch
// pointless when one document is larger than the whole limit.
func (s *Store) evict(keepID string) error {
	if s.limitBytes < 0 {
		return nil
	}
	for {
		var total sql.NullInt64
		if err := s.conn.QueryRow("SELECT SUM(bytes) FROM tdocs").Scan(&total); err != nil {
			return fmt.Errorf("measure tdoc cache: %w", err)
		}
		if !total.Valid || total.Int64 <= s.limitBytes {
			return nil
		}
		var victim string
		err := s.conn.QueryRow("SELECT id FROM tdocs WHERE id != ? ORDER BY last_used_at, rowid LIMIT 1", keepID).Scan(&victim)
		if errors.Is(err, sql.ErrNoRows) {
			log.Printf("tdoc cache: %s alone exceeds the %d byte limit; keeping it", keepID, s.limitBytes)
			return nil
		}
		if err != nil {
			return fmt.Errorf("pick eviction victim: %w", err)
		}
		if err := s.delete(victim); err != nil {
			return err
		}
		log.Printf("tdoc cache: evicted %s", victim)
	}
}

// delete removes one cached document and returns its pages to the filesystem.
func (s *Store) delete(id string) error {
	tx, err := s.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin eviction: %w", err)
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM tdoc_sections WHERE tdoc_id = ?",
		"DELETE FROM tdoc_images WHERE tdoc_id = ?",
		"DELETE FROM tdocs WHERE id = ?",
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return fmt.Errorf("evict %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit eviction: %w", err)
	}
	if _, err := s.conn.Exec("PRAGMA incremental_vacuum"); err != nil {
		log.Printf("warning: tdoc cache incremental_vacuum failed: %v", err)
	}
	return nil
}
