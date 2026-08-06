package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/higebu/3gpp-mcp/internal/specver"
	_ "modernc.org/sqlite"
)

// ErrNoVersion reports that a spec, or a specific version of it, is not in
// this database. Read methods that return a list translate it into an empty
// result so callers can fall back to another source.
var ErrNoVersion = errors.New("spec version not found")

type Spec struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Version is the canonical dotted form, e.g. "20.2.0".
	Version string `json:"version,omitempty"`
	// VersionToken is the base-36 form used in archive filenames, e.g. "k20".
	// It is kept alongside Version so an archive file stays resolvable even
	// though the dotted form is what users and documents refer to.
	VersionToken string `json:"version_token,omitempty"`
	Release      string `json:"release,omitempty"`
	Series       string `json:"series,omitempty"`
}

type Section struct {
	SpecID       string `json:"spec_id"`
	Version      string `json:"version,omitempty"`
	Release      string `json:"release,omitempty"`
	Number       string `json:"number"`
	Title        string `json:"title"`
	Level        int    `json:"level"`
	ParentNumber string `json:"parent_number,omitempty"`
	Content      string `json:"content,omitempty"`
}

type SearchResult struct {
	SpecID  string `json:"spec_id"`
	Version string `json:"version,omitempty"`
	Release string `json:"release,omitempty"`
	Number  string `json:"number"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// Image holds an embedded image from a specification.
type Image struct {
	SpecID      string `json:"spec_id"`
	Version     string `json:"version,omitempty"`
	Name        string `json:"name"`
	MIMEType    string `json:"mime_type"`
	Data        []byte `json:"-"`
	LLMReadable bool   `json:"llm_readable"`
}

// ImageInfo holds image metadata without the binary data.
type ImageInfo struct {
	SpecID      string `json:"spec_id"`
	Version     string `json:"version,omitempty"`
	Name        string `json:"name"`
	MIMEType    string `json:"mime_type"`
	LLMReadable bool   `json:"llm_readable"`
}

// Reference represents a cross-reference from one spec section to another spec.
// Only the source side carries a version: a reference names a target spec, not
// a particular version of it.
type Reference struct {
	SourceSpecID  string `json:"source_spec_id"`
	SourceVersion string `json:"source_version,omitempty"`
	SourceSection string `json:"source_section"`
	TargetSpec    string `json:"target_spec"`
	TargetSection string `json:"target_section,omitempty"`
	TargetTitle   string `json:"target_title,omitempty"`
	Context       string `json:"context"`
}

type DB struct {
	conn *sql.DB
}

// uriPathEscaper encodes the characters SQLite treats specially in a URI
// filename, so a literal ?, # or % in a database path is not reinterpreted
// as a query string, fragment or percent-escape.
var uriPathEscaper = strings.NewReplacer("%", "%25", "?", "%3F", "#", "%23")

func Open(path string) (*DB, error) {
	// The driver only honors URI query parameters on "file:" DSNs; on a bare
	// path it strips the query and opens READWRITE|CREATE, silently creating
	// an empty database when the path is wrong.
	conn, err := sql.Open("sqlite", "file:"+uriPathEscaper.Replace(path)+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// Limit open connections to avoid resource exhaustion under concurrent reads.
	conn.SetMaxOpenConns(4)
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{conn: conn}, nil
}

// OpenReadWrite opens a database in read-write mode. Intended for testing.
func OpenReadWrite(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// Limit to a single connection to serialize writes and avoid SQLITE_BUSY.
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec("PRAGMA journal_mode=WAL"); err != nil {
		log.Printf("warning: failed to set WAL mode: %v", err)
	}
	if _, err := conn.Exec("PRAGMA busy_timeout=5000"); err != nil {
		log.Printf("warning: failed to set busy_timeout: %v", err)
	}
	if err := conn.Ping(); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return &DB{conn: conn}, nil
}

// Exec executes a SQL statement on the database. Intended for testing.
func (d *DB) Exec(query string, args ...any) error {
	_, err := d.conn.Exec(query, args...)
	return err
}

// ExecScript executes multiple SQL statements. Intended for testing.
func (d *DB) ExecScript(script string) error {
	_, err := d.conn.Exec(script)
	return err
}

func (d *DB) Close() error {
	return d.conn.Close()
}

// VacuumInto creates a compact, consistent copy of the database at path.
func (d *DB) VacuumInto(path string) error {
	_, err := d.conn.Exec("VACUUM INTO ?", path)
	return err
}

// DefaultSearchLimit is the default number of results returned by Search.
const DefaultSearchLimit = 10

// MaxSearchLimit is the upper bound for search results.
const MaxSearchLimit = 200

// DefaultListSpecsLimit is the default number of specs returned by ListSpecs.
const DefaultListSpecsLimit = 20

// MaxListSpecsLimit is the upper bound for list specs results.
const MaxListSpecsLimit = 1000

// ListSpecsResult holds paginated results from ListSpecs.
type ListSpecsResult struct {
	Specs      []Spec `json:"specs"`
	TotalCount int    `json:"total_count"`
	Limit      int    `json:"limit"`
	Offset     int    `json:"offset"`
}

// SearchResults holds one page of search hits plus the total match count.
type SearchResults struct {
	Results    []SearchResult `json:"results"`
	TotalCount int            `json:"total_count"`
	Limit      int            `json:"limit"`
	Offset     int            `json:"offset"`
}

// SpecTablesSchema defines the tables that hold a specification's text, keyed
// by (spec_id, version). The version cache reuses it verbatim; the main
// database adds full-text search and the remaining tables on top.
const SpecTablesSchema = `
CREATE TABLE IF NOT EXISTS specs (
    id TEXT NOT NULL,
    version TEXT NOT NULL,
    version_token TEXT NOT NULL DEFAULT '',
    title TEXT NOT NULL,
    release TEXT,
    series TEXT,
    PRIMARY KEY (id, version)
);

CREATE INDEX IF NOT EXISTS idx_specs_id ON specs(id);

CREATE TABLE IF NOT EXISTS sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    spec_id TEXT NOT NULL,
    version TEXT NOT NULL,
    number TEXT NOT NULL,
    title TEXT NOT NULL,
    level INTEGER NOT NULL,
    parent_number TEXT,
    content TEXT NOT NULL,
    UNIQUE(spec_id, version, number)
);

CREATE INDEX IF NOT EXISTS idx_sections_spec ON sections(spec_id, version);
CREATE INDEX IF NOT EXISTS idx_sections_number ON sections(spec_id, version, number);
`

// ImagesTableSchema defines the table that holds embedded images, keyed by
// (spec_id, version, name). The version cache reuses it verbatim for images
// fetched on demand.
const ImagesTableSchema = `
CREATE TABLE IF NOT EXISTS images (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    spec_id TEXT NOT NULL,
    version TEXT NOT NULL,
    name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    data BLOB NOT NULL,
    llm_readable BOOLEAN NOT NULL DEFAULT 0,
    UNIQUE(spec_id, version, name)
);

CREATE INDEX IF NOT EXISTS idx_images_spec ON images(spec_id, version);
`

// Schema is the SQL schema for the 3GPP database.
//
// A build imports one version per spec, so the FTS index covers exactly that
// version. Versions fetched on demand are stored in a separate cache database
// that has no FTS tables, keeping cross-release rows out of search results.
//
// Porter stemming folds inflected forms together (handover matches
// handovers); spec_id and number tokenize to unstemmed digit runs, which
// porter leaves untouched. The DDL is IF NOT EXISTS, so the tokenizer applies
// to newly created databases only.
const Schema = SpecTablesSchema + ImagesTableSchema + `

-- Porter wraps unicode61 and passes the remaining arguments through to it.
-- tokenchars '-' keeps hyphenated ASN.1 identifiers (RRCSetup-IEs) as single
-- tokens so quoted and prefix queries match them exactly. '.' is deliberately
-- not included: it would glue sentence-final periods to the preceding word,
-- and dotted spec numbers already match via phrase quoting.
CREATE VIRTUAL TABLE IF NOT EXISTS sections_fts USING fts5(
    spec_id, number, title, content,
    content=sections,
    content_rowid=id,
    tokenize="porter unicode61 tokenchars '-'"
);

CREATE TRIGGER IF NOT EXISTS sections_ai AFTER INSERT ON sections BEGIN
    INSERT INTO sections_fts(rowid, spec_id, number, title, content)
    VALUES (new.id, new.spec_id, new.number, new.title, new.content);
END;

CREATE TRIGGER IF NOT EXISTS sections_ad AFTER DELETE ON sections BEGIN
    INSERT INTO sections_fts(sections_fts, rowid, spec_id, number, title, content)
    VALUES ('delete', old.id, old.spec_id, old.number, old.title, old.content);
END;

CREATE TRIGGER IF NOT EXISTS sections_au AFTER UPDATE ON sections BEGIN
    INSERT INTO sections_fts(sections_fts, rowid, spec_id, number, title, content)
    VALUES ('delete', old.id, old.spec_id, old.number, old.title, old.content);
    INSERT INTO sections_fts(rowid, spec_id, number, title, content)
    VALUES (new.id, new.spec_id, new.number, new.title, new.content);
END;

-- OpenAPI definitions are not keyed by spec version. They ship as standalone
-- YAML files carrying their own api version, and the pipeline imports them
-- under a spec ID that may have no specs row at all, so a spec version column
-- here could not be filled reliably.
CREATE TABLE IF NOT EXISTS openapi_specs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    spec_id TEXT NOT NULL,
    api_name TEXT NOT NULL,
    version TEXT,
    filename TEXT,
    content TEXT NOT NULL,
    UNIQUE(spec_id, api_name)
);

CREATE TABLE IF NOT EXISTS spec_references (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    source_spec_id TEXT NOT NULL,
    source_version TEXT NOT NULL DEFAULT '',
    source_section TEXT NOT NULL,
    target_spec TEXT NOT NULL,
    target_section TEXT NOT NULL DEFAULT '',
    context TEXT NOT NULL,
    UNIQUE(source_spec_id, source_version, source_section, target_spec, target_section)
);

CREATE INDEX IF NOT EXISTS idx_ref_source ON spec_references(source_spec_id, source_version, source_section);
CREATE INDEX IF NOT EXISTS idx_ref_target ON spec_references(target_spec);
`

// InitSchema creates the database tables and indexes.
func (d *DB) InitSchema() error {
	_, err := d.conn.Exec(Schema)
	return err
}

// ResolveVersion returns the version of a spec to read. An empty version means
// "whatever this database holds": a build imports one version per spec, so the
// newest row is the only row. It returns ErrNoVersion when the spec is absent
// or when an explicitly requested version is not stored.
func (d *DB) ResolveVersion(ctx context.Context, specID, version string) (string, error) {
	if version != "" {
		var found string
		err := d.conn.QueryRowContext(ctx, "SELECT version FROM specs WHERE id = ? AND version = ?", specID, version).Scan(&found)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNoVersion
		}
		if err != nil {
			return "", fmt.Errorf("resolve version: %w", err)
		}
		return found, nil
	}

	rows, err := d.conn.QueryContext(ctx, "SELECT version FROM specs WHERE id = ?", specID)
	if err != nil {
		return "", fmt.Errorf("resolve version: %w", err)
	}
	defer rows.Close()

	// An empty version is a legitimate stored value for a document whose
	// version could not be determined, so "found nothing" is tracked separately
	// from "found an empty string".
	best := ""
	found := false
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return "", fmt.Errorf("scan version: %w", err)
		}
		if !found || specver.Compare(v, best) > 0 {
			best, found = v, true
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("resolve version: iterate: %w", err)
	}
	if !found {
		return "", ErrNoVersion
	}
	return best, nil
}

// ListSpecVersions returns every version of a spec held in this database,
// newest first.
func (d *DB) ListSpecVersions(ctx context.Context, specID string) ([]Spec, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT id, version, COALESCE(version_token, ''), title, COALESCE(release, ''), COALESCE(series, '') FROM specs WHERE id = ?",
		specID,
	)
	if err != nil {
		return nil, fmt.Errorf("list spec versions: %w", err)
	}
	defer rows.Close()

	var specs []Spec
	for rows.Next() {
		var s Spec
		if err := rows.Scan(&s.ID, &s.Version, &s.VersionToken, &s.Title, &s.Release, &s.Series); err != nil {
			return nil, fmt.Errorf("scan spec version: %w", err)
		}
		specs = append(specs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list spec versions: iterate: %w", err)
	}
	sort.Slice(specs, func(i, j int) bool {
		return specver.Compare(specs[i].Version, specs[j].Version) > 0
	})
	return specs, nil
}

// UpsertSpec inserts or replaces a spec record.
func (d *DB) UpsertSpec(spec Spec) error {
	_, err := d.conn.Exec(
		"INSERT OR REPLACE INTO specs (id, version, version_token, title, release, series) VALUES (?, ?, ?, ?, ?, ?)",
		spec.ID, spec.Version, spec.VersionToken, spec.Title, spec.Release, spec.Series,
	)
	return err
}

// UpsertSection deletes then re-inserts a section to trigger FTS update.
func (d *DB) UpsertSection(section Section) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit per database/sql docs

	_, err = tx.Exec(
		"DELETE FROM sections WHERE spec_id = ? AND version = ? AND number = ?",
		section.SpecID, section.Version, section.Number,
	)
	if err != nil {
		return err
	}
	_, err = tx.Exec(
		"INSERT INTO sections (spec_id, version, number, title, level, parent_number, content) VALUES (?, ?, ?, ?, ?, ?, ?)",
		section.SpecID, section.Version, section.Number, section.Title, section.Level, section.ParentNumber, section.Content,
	)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertImage inserts or replaces an image record.
func (d *DB) UpsertImage(img Image) error {
	_, err := d.conn.Exec(
		"INSERT OR REPLACE INTO images (spec_id, version, name, mime_type, data, llm_readable) VALUES (?, ?, ?, ?, ?, ?)",
		img.SpecID, img.Version, img.Name, img.MIMEType, img.Data, img.LLMReadable,
	)
	return err
}

// GetImage retrieves a single image by spec ID, version and name, or nil when
// the spec holds no image of that name.
// An empty version resolves to the version this database holds.
func (d *DB) GetImage(ctx context.Context, specID, version, name string) (*Image, error) {
	version, err := d.ResolveVersion(ctx, specID, version)
	if err != nil {
		return nil, fmt.Errorf("get image: %w", err)
	}
	var img Image
	err = d.conn.QueryRowContext(ctx,
		"SELECT spec_id, version, name, mime_type, data, llm_readable FROM images WHERE spec_id = ? AND version = ? AND name = ?",
		specID, version, name,
	).Scan(&img.SpecID, &img.Version, &img.Name, &img.MIMEType, &img.Data, &img.LLMReadable)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get image: %w", err)
	}
	return &img, nil
}

// ListImages returns metadata for all images of a spec (without binary data).
// An empty version resolves to the version this database holds.
func (d *DB) ListImages(ctx context.Context, specID, version string) ([]ImageInfo, error) {
	version, err := d.ResolveVersion(ctx, specID, version)
	if errors.Is(err, ErrNoVersion) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	rows, err := d.conn.QueryContext(ctx,
		"SELECT spec_id, version, name, mime_type, llm_readable FROM images WHERE spec_id = ? AND version = ? ORDER BY name",
		specID, version,
	)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	defer rows.Close()

	var infos []ImageInfo
	for rows.Next() {
		var info ImageInfo
		if err := rows.Scan(&info.SpecID, &info.Version, &info.Name, &info.MIMEType, &info.LLMReadable); err != nil {
			return nil, fmt.Errorf("scan image info: %w", err)
		}
		infos = append(infos, info)
	}
	return infos, rows.Err()
}

// alternateDocTypeID returns the same spec ID under the other document-type
// prefix ("TS 21.905" -> "TR 21.905" and vice versa), and false when the ID
// carries no "TS "/"TR " prefix.
func alternateDocTypeID(id string) (string, bool) {
	if rest, ok := strings.CutPrefix(id, "TS "); ok {
		return "TR " + rest, true
	}
	if rest, ok := strings.CutPrefix(id, "TR "); ok {
		return "TS " + rest, true
	}
	return "", false
}

// InsertSpecWithSections inserts a spec and all its sections in a transaction.
func (d *DB) InsertSpecWithSections(spec Spec, sections []Section) error {
	return d.InsertSpecWithSectionsAndImages(spec, sections, nil)
}

// InsertSpecWithSectionsAndImages inserts a spec, sections, and images in a transaction.
func (d *DB) InsertSpecWithSectionsAndImages(spec Spec, sections []Section, images []Image) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op after Commit per database/sql docs

	_, err = tx.Exec(
		"INSERT OR REPLACE INTO specs (id, version, version_token, title, release, series) VALUES (?, ?, ?, ?, ?, ?)",
		spec.ID, spec.Version, spec.VersionToken, spec.Title, spec.Release, spec.Series,
	)
	if err != nil {
		return fmt.Errorf("upsert spec: %w", err)
	}

	// Use explicit DELETE + INSERT to ensure FTS5 triggers fire correctly.
	// INSERT OR REPLACE suppresses DELETE triggers unless recursive_triggers is
	// enabled, leaving stale FTS entries that cause "missing row" search errors.
	// Note: we delete individual sections rather than bulk-deleting all sections
	// for the spec, because multi-file specs (e.g. TS 36.133) call this function
	// once per DOCX file and a bulk DELETE would erase previously processed sections.
	delStmt, err := tx.Prepare(
		"DELETE FROM sections WHERE spec_id = ? AND version = ? AND number = ?",
	)
	if err != nil {
		return fmt.Errorf("prepare delete: %w", err)
	}
	defer delStmt.Close()

	insStmt, err := tx.Prepare(
		"INSERT INTO sections (spec_id, version, number, title, level, parent_number, content) VALUES (?, ?, ?, ?, ?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer insStmt.Close()

	refStmt, err := tx.Prepare(
		"INSERT OR REPLACE INTO spec_references (source_spec_id, source_version, source_section, target_spec, target_section, context) VALUES (?, ?, ?, ?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare ref insert: %w", err)
	}
	defer refStmt.Close()

	// Build bracketed reference map from the References section.
	var bracketMap map[string]string
	for _, s := range sections {
		if s.Number == "2" || strings.EqualFold(s.Title, "References") ||
			strings.EqualFold(s.Title, "Normative references") ||
			strings.EqualFold(s.Title, "Informative references") {
			bracketMap = ParseBracketedRefMap(s.Content)
			break
		}
	}

	// A build imports one version per spec, and search depends on that: the FTS
	// index has no version column, so a second version of the same spec would
	// double every hit. Superseding a version therefore drops the old one.
	// Multi-file specs call this once per DOCX with the same version, so this
	// only fires on the first call. A file whose version could not be
	// determined must not wipe correctly-versioned data, so the cleanup is
	// skipped for it.
	if spec.Version != "" {
		for _, table := range []string{"sections", "images", "spec_references"} {
			column, versionColumn := "spec_id", "version"
			if table == "spec_references" {
				column, versionColumn = "source_spec_id", "source_version"
			}
			q := fmt.Sprintf("DELETE FROM %s WHERE %s = ? AND %s <> ?", table, column, versionColumn)
			if _, err = tx.Exec(q, spec.ID, spec.Version); err != nil {
				return fmt.Errorf("drop superseded %s: %w", table, err)
			}
		}
		if _, err = tx.Exec("DELETE FROM specs WHERE id = ? AND version <> ?", spec.ID, spec.Version); err != nil {
			return fmt.Errorf("drop superseded spec versions: %w", err)
		}

		// A spec number names either a Technical Specification or a Technical
		// Report, never both, so any rows under the other document-type prefix
		// are the same spec under a stale label — databases built before TR
		// detection stored every spec as "TS ...". Drop them, or an update
		// would leave the spec duplicated under both labels and double every
		// search hit.
		if alt, ok := alternateDocTypeID(spec.ID); ok {
			for _, table := range []string{"sections", "images", "spec_references"} {
				column := "spec_id"
				if table == "spec_references" {
					column = "source_spec_id"
				}
				q := fmt.Sprintf("DELETE FROM %s WHERE %s = ?", table, column)
				if _, err = tx.Exec(q, alt); err != nil {
					return fmt.Errorf("drop relabeled %s: %w", table, err)
				}
			}
			if _, err = tx.Exec("DELETE FROM specs WHERE id = ?", alt); err != nil {
				return fmt.Errorf("drop relabeled spec: %w", err)
			}
		}
	}

	// The spec row is the authority on the version, so child rows take it from
	// there rather than from their own field.
	for _, s := range sections {
		if _, err = delStmt.Exec(s.SpecID, spec.Version, s.Number); err != nil {
			return fmt.Errorf("delete section: %w", err)
		}
		_, err = insStmt.Exec(s.SpecID, spec.Version, s.Number, s.Title, s.Level, s.ParentNumber, s.Content)
		if err != nil {
			return fmt.Errorf("insert section: %w", err)
		}

		refs := ExtractReferences(s.SpecID, s.Number, s.Content, bracketMap)
		for _, r := range refs {
			_, err = refStmt.Exec(r.SourceSpecID, spec.Version, r.SourceSection, r.TargetSpec, r.TargetSection, r.Context)
			if err != nil {
				return fmt.Errorf("insert reference: %w", err)
			}
		}
	}

	// Insert images if provided.
	if len(images) > 0 {
		imgStmt, err := tx.Prepare(
			"INSERT OR REPLACE INTO images (spec_id, version, name, mime_type, data, llm_readable) VALUES (?, ?, ?, ?, ?, ?)",
		)
		if err != nil {
			return fmt.Errorf("prepare image insert: %w", err)
		}
		defer imgStmt.Close()

		for _, img := range images {
			_, err = imgStmt.Exec(img.SpecID, spec.Version, img.Name, img.MIMEType, img.Data, img.LLMReadable)
			if err != nil {
				return fmt.Errorf("insert image: %w", err)
			}
		}
	}

	return tx.Commit()
}

// ListSpecs lists specs, optionally filtered by series and/or an ID prefix.
// query matches spec IDs that start with the given text, ignoring a leading
// "TS "/"TR " document-type prefix (e.g. query "38.21" matches "TS 38.211").
func (d *DB) ListSpecs(ctx context.Context, series, query string, limit, offset int) (*ListSpecsResult, error) {
	if offset < 0 {
		offset = 0
	}
	var conditions []string
	var filterArgs []any
	if series != "" {
		conditions = append(conditions, "series = ?")
		filterArgs = append(filterArgs, series)
	}
	if query != "" {
		// Match three ways a user might type the prefix: the bare number
		// (e.g. "23.501"), or with an explicit "TS "/"TR " document-type
		// prefix already included (e.g. "TS 23.501").
		pattern := EscapeLikePattern(query) + "%"
		conditions = append(conditions, "(id LIKE ? ESCAPE '\\' OR id LIKE 'TS ' || ? ESCAPE '\\' OR id LIKE 'TR ' || ? ESCAPE '\\')")
		filterArgs = append(filterArgs, pattern, pattern, pattern)
	}
	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	// Get total count.
	var totalCount int
	if err := d.conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM specs"+where, filterArgs...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("count specs: %w", err)
	}

	sqlQuery := "SELECT id, version, COALESCE(version_token, ''), title, COALESCE(release, ''), COALESCE(series, '') FROM specs" + where + " ORDER BY id, version"
	args := append([]any{}, filterArgs...)

	// limit == 0: use default; limit < 0: no limit (return all rows, internal use only).
	if limit == 0 {
		limit = DefaultListSpecsLimit
	}
	if limit > MaxListSpecsLimit {
		limit = MaxListSpecsLimit
	}
	if limit > 0 {
		sqlQuery += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}

	rows, err := d.conn.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("list specs: %w", err)
	}
	defer rows.Close()

	var specs []Spec
	for rows.Next() {
		var s Spec
		if err := rows.Scan(&s.ID, &s.Version, &s.VersionToken, &s.Title, &s.Release, &s.Series); err != nil {
			return nil, fmt.Errorf("scan spec: %w", err)
		}
		specs = append(specs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list specs: iterate: %w", err)
	}
	return &ListSpecsResult{Specs: specs, TotalCount: totalCount, Limit: limit, Offset: offset}, nil
}

// GetTOC returns the section structure of a spec. An empty version resolves to
// the version this database holds; a version it does not hold yields no rows.
func (d *DB) GetTOC(ctx context.Context, specID, version string) ([]Section, error) {
	version, err := d.ResolveVersion(ctx, specID, version)
	if errors.Is(err, ErrNoVersion) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get toc: %w", err)
	}
	rows, err := d.conn.QueryContext(ctx,
		"SELECT s.spec_id, s.version, s.number, s.title, s.level, COALESCE(s.parent_number, ''), COALESCE(p.release, '') FROM sections s LEFT JOIN specs p ON p.id = s.spec_id AND p.version = s.version WHERE s.spec_id = ? AND s.version = ? ORDER BY s.id",
		specID, version,
	)
	if err != nil {
		return nil, fmt.Errorf("get toc: %w", err)
	}
	defer rows.Close()

	var sections []Section
	for rows.Next() {
		var s Section
		if err := rows.Scan(&s.SpecID, &s.Version, &s.Number, &s.Title, &s.Level, &s.ParentNumber, &s.Release); err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		sections = append(sections, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get toc: iterate: %w", err)
	}
	return sections, nil
}

// AllSections returns every section of a spec version, content included, in
// document order. An empty version resolves to the version this database
// holds; a version it does not hold yields no rows.
// SectionNumbers returns the set of section numbers of the database version
// of a spec and that version — a light existence index for cross-reference
// validation, without loading titles or content. An unknown spec returns an
// empty set and no error.
func (d *DB) SectionNumbers(ctx context.Context, specID string) (map[string]bool, string, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT number, version FROM sections WHERE spec_id = ?", specID)
	if err != nil {
		return nil, "", fmt.Errorf("section numbers: %w", err)
	}
	defer rows.Close()

	numbers := make(map[string]bool)
	version := ""
	for rows.Next() {
		var n string
		if err := rows.Scan(&n, &version); err != nil {
			return nil, "", fmt.Errorf("scan section number: %w", err)
		}
		numbers[n] = true
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("section numbers: iterate: %w", err)
	}
	return numbers, version, nil
}

func (d *DB) AllSections(ctx context.Context, specID, version string) ([]Section, error) {
	version, err := d.ResolveVersion(ctx, specID, version)
	if errors.Is(err, ErrNoVersion) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("all sections: %w", err)
	}
	rows, err := d.conn.QueryContext(ctx,
		"SELECT s.spec_id, s.version, s.number, s.title, s.level, COALESCE(s.parent_number, ''), s.content, COALESCE(p.release, '') FROM sections s LEFT JOIN specs p ON p.id = s.spec_id AND p.version = s.version WHERE s.spec_id = ? AND s.version = ? ORDER BY s.id",
		specID, version,
	)
	if err != nil {
		return nil, fmt.Errorf("all sections: %w", err)
	}
	defer rows.Close()

	var sections []Section
	for rows.Next() {
		var s Section
		if err := rows.Scan(&s.SpecID, &s.Version, &s.Number, &s.Title, &s.Level, &s.ParentNumber, &s.Content, &s.Release); err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		sections = append(sections, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("all sections: iterate: %w", err)
	}
	return sections, nil
}

// EscapeLikePattern escapes SQLite LIKE wildcards (% and _) in a
// user-supplied string so it can be used as a literal prefix with an
// ESCAPE '\' clause.
func EscapeLikePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return r.Replace(s)
}

// FindSpecIDsByFamily returns the split multi-file part IDs belonging to a
// family spec ID (e.g. "TS 38.101" -> ["TS 38.101-1", "TS 38.101-2", ...]).
// Used to give a helpful error when a lookup tool is queried with a family
// ID instead of a specific part.
func (d *DB) FindSpecIDsByFamily(ctx context.Context, familyID string) ([]string, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT DISTINCT id FROM specs WHERE id LIKE ? || '-%' ESCAPE '\\' ORDER BY id",
		EscapeLikePattern(familyID),
	)
	if err != nil {
		return nil, fmt.Errorf("find spec ids by family: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan spec id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find spec ids by family: iterate: %w", err)
	}
	return ids, nil
}

// GetSection returns a section, optionally with its subsections. An empty
// version resolves to the version this database holds; a version it does not
// hold yields no rows.
func (d *DB) GetSection(ctx context.Context, specID, version, number string, includeSubsections bool) ([]Section, error) {
	version, err := d.ResolveVersion(ctx, specID, version)
	if errors.Is(err, ErrNoVersion) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get section: %w", err)
	}

	const projection = "SELECT s.spec_id, s.version, s.number, s.title, s.level, COALESCE(s.parent_number, ''), s.content, COALESCE(p.release, '') " +
		"FROM sections s LEFT JOIN specs p ON p.id = s.spec_id AND p.version = s.version WHERE s.spec_id = ? AND s.version = ?"

	var rows *sql.Rows
	if includeSubsections {
		rows, err = d.conn.QueryContext(ctx,
			projection+" AND (s.number = ? OR s.number LIKE ? || '.%' ESCAPE '\\') ORDER BY s.id",
			specID, version, number, EscapeLikePattern(number),
		)
	} else {
		rows, err = d.conn.QueryContext(ctx,
			projection+" AND s.number = ?",
			specID, version, number,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("get section: %w", err)
	}
	defer rows.Close()

	var sections []Section
	for rows.Next() {
		var s Section
		if err := rows.Scan(&s.SpecID, &s.Version, &s.Number, &s.Title, &s.Level, &s.ParentNumber, &s.Content, &s.Release); err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		sections = append(sections, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get section: iterate: %w", err)
	}
	return sections, nil
}

// GetBracketMap returns the bracket reference map for a spec by fetching the
// References section (number "2" or matching title) and parsing it.
// Returns nil, nil when no References section is found.
func (d *DB) GetBracketMap(ctx context.Context, specID, version string) (map[string]string, error) {
	version, err := d.ResolveVersion(ctx, specID, version)
	if errors.Is(err, ErrNoVersion) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get bracket map: %w", err)
	}
	rows, err := d.conn.QueryContext(ctx,
		`SELECT content FROM sections
		 WHERE spec_id = ? AND version = ? AND (
		   number = '2' OR
		   LOWER(title) = 'references' OR
		   LOWER(title) = 'normative references' OR
		   LOWER(title) = 'informative references'
		 ) ORDER BY id LIMIT 1`,
		specID, version,
	)
	if err != nil {
		return nil, fmt.Errorf("get bracket map: %w", err)
	}
	defer rows.Close()

	if rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return nil, fmt.Errorf("scan bracket map: %w", err)
		}
		return ParseBracketedRefMap(content), nil
	}
	return nil, rows.Err()
}

type OpenAPISpec struct {
	SpecID  string `json:"spec_id"`
	APIName string `json:"api_name"`
	Version string `json:"version,omitempty"`
}

func (d *DB) ListOpenAPI(ctx context.Context, specID string) ([]OpenAPISpec, error) {
	query := "SELECT spec_id, api_name, COALESCE(version, '') FROM openapi_specs"
	var args []any
	if specID != "" {
		query += " WHERE spec_id = ?"
		args = append(args, specID)
	}
	query += " ORDER BY spec_id, api_name"

	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list openapi: %w", err)
	}
	defer rows.Close()

	var specs []OpenAPISpec
	for rows.Next() {
		var s OpenAPISpec
		if err := rows.Scan(&s.SpecID, &s.APIName, &s.Version); err != nil {
			return nil, fmt.Errorf("scan openapi: %w", err)
		}
		specs = append(specs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list openapi: iterate: %w", err)
	}
	return specs, nil
}

func (d *DB) GetOpenAPI(ctx context.Context, specID, apiName string) (string, error) {
	var content string
	err := d.conn.QueryRowContext(ctx,
		"SELECT content FROM openapi_specs WHERE spec_id = ? AND api_name = ?",
		specID, apiName,
	).Scan(&content)
	if err != nil {
		return "", fmt.Errorf("get openapi: %w", err)
	}
	return content, nil
}

// UpsertOpenAPI inserts or replaces an OpenAPI spec.
func (d *DB) UpsertOpenAPI(specID, apiName, version, filename, content string) error {
	_, err := d.conn.Exec(
		"INSERT OR REPLACE INTO openapi_specs (spec_id, api_name, version, filename, content) VALUES (?, ?, ?, ?, ?)",
		specID, apiName, version, filename, content,
	)
	return err
}

// fts5Columns is the set of valid column names in the sections_fts table.
var fts5Columns = map[string]bool{
	"spec_id": true, "number": true, "title": true, "content": true,
}

// fts5Operators are FTS5 keywords that must not be quoted.
var fts5Operators = map[string]bool{
	"AND": true, "OR": true, "NOT": true,
}

// needsFTS5Quoting reports whether a bare token contains a character FTS5
// cannot parse as part of an unquoted bareword: a hyphen (misread as the
// column-filter/NOT operator), a period (e.g. spec numbers like "38.101",
// which FTS5 otherwise rejects with "syntax error near \".\""), a stray
// double quote, a parenthesis riding along inside the token (an unquoted
// "(38.331" or "SMF)" is a hard syntax error), a star (valid only as a
// trailing prefix operator; quoteFTS5Term keeps that meaning and quotes
// every other placement, so a bare "*" degrades to no results instead of
// "unknown special query"), a comma (only valid as the NEAR distance
// separator, so "AMF, SMF" is a syntax error), or a colon (valid only as
// the separator of a column filter naming a real column, which
// classifyToken checks before falling back here: "NOTE: mentions" would
// otherwise fail the whole match with "no such column: NOTE").
func needsFTS5Quoting(s string) bool {
	return strings.ContainsAny(s, `-."()*,:`)
}

// quoteFTS5String wraps s in double quotes, doubling any quote already
// inside it (FTS5's string escape).
func quoteFTS5String(s string) string {
	return "\"" + strings.ReplaceAll(s, "\"", "\"\"") + "\""
}

// quoteFTS5Term quotes a search term, keeping a single trailing "*" outside
// the quotes so it keeps its prefix-match meaning: inside a quoted string FTS5
// treats "*" as an ordinary character, so "38.10*" matches nothing while
// "38.10"* matches every term starting with 38.10. A term that is nothing but
// stars, or that ends in several of them, has no valid prefix form and is
// quoted whole ("38.10"** is a syntax error).
func quoteFTS5Term(s string) string {
	if stem, ok := strings.CutSuffix(s, "*"); ok && stem != "" && !strings.HasSuffix(stem, "*") {
		return quoteFTS5String(stem) + "*"
	}
	return quoteFTS5String(s)
}

// classifyToken quotes a bareword or "col:val" column-filter token if it
// contains a character FTS5 cannot parse in an unquoted bareword. Only a
// prefix naming a real column of sections_fts is treated as a column filter
// (FTS5 matches column names case-insensitively, so the lookup does too);
// anything else before the colon is ordinary query text and is quoted along
// with the rest of the token, because FTS5 answers an unknown column with a
// hard "no such column" error that fails the entire search.
func classifyToken(token string) string {
	if colIdx := strings.IndexByte(token, ':'); colIdx > 0 {
		col := token[:colIdx]
		val := token[colIdx+1:]
		if fts5Columns[strings.ToLower(col)] {
			if val == "" {
				// "content:" with no value is a syntax error; treat the
				// whole token as a literal search term instead.
				return quoteFTS5String(token)
			}
			if strings.HasPrefix(val, "\"") {
				// Already phrase-quoted (content:"foo"), optionally with a
				// prefix "*" after the closing quote. Repair it if the
				// closing quote is missing.
				if body := strings.TrimSuffix(val, "*"); len(body) >= 2 && strings.HasSuffix(body, "\"") {
					return token
				}
				return col + ":" + quoteFTS5Term(strings.Trim(val, "\""))
			}
			if needsFTS5Quoting(val) {
				return col + ":" + quoteFTS5Term(val)
			}
			return token
		}
	}
	if needsFTS5Quoting(token) {
		return quoteFTS5Term(token)
	}
	return token
}

// splitFTS5Tokens splits s on whitespace, keeping a double-quoted phrase —
// including the spaces inside it — in a single token.
func splitFTS5Tokens(s string) []string {
	var tokens []string
	i, n := 0, len(s)
	for i < n {
		if s[i] == ' ' || s[i] == '\t' || s[i] == '\n' {
			i++
			continue
		}
		j := i
		for j < n && s[j] != ' ' && s[j] != '\t' && s[j] != '\n' {
			if s[j] == '"' {
				j++
				for j < n && s[j] != '"' {
					j++
				}
				if j < n {
					j++
				}
				continue
			}
			j++
		}
		tokens = append(tokens, s[i:j])
		i = j
	}
	return tokens
}

// isDecimal reports whether s is a non-empty run of ASCII digits.
func isDecimal(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// quoteNEAROperand applies to one operand of a NEAR group the same quoting a
// standalone term gets. An operand is a phrase or a bareword only: a column
// filter belongs outside the group (content:NEAR(a b)) and AND/OR/NOT are
// syntax errors inside it, so both are quoted as literal text.
func quoteNEAROperand(token string) string {
	if strings.HasPrefix(token, "\"") {
		body, star := token, ""
		if stem, ok := strings.CutSuffix(body, "*"); ok && len(stem) >= 2 && strings.HasSuffix(stem, "\"") {
			body, star = stem, "*"
		}
		if len(body) < 2 || !strings.HasSuffix(body, "\"") {
			// Unterminated phrase; supply the missing closing quote.
			body += "\""
		}
		return body + star
	}
	if fts5Operators[token] || needsFTS5Quoting(token) {
		return quoteFTS5Term(token)
	}
	return token
}

// sanitizeNEARGroup quotes the operands of a balanced NEAR(...) group. FTS5
// parses a NEAR operand exactly as it parses a standalone term, so a dotted
// or hyphenated bareword — NEAR(38.101 23.501), NEAR(IMS-AKA SMF) — is the
// same hard syntax error there, and it rejects the whole MATCH. The group's
// own syntax is preserved: the NEAR( ) wrapper and a trailing ", N" distance.
func sanitizeNEARGroup(run string) string {
	body := run[len("NEAR(") : len(run)-1]

	// FTS5 allows one trailing ", N" distance and nothing else after it, so
	// only a final all-digit tail is kept as the distance; any other comma is
	// query text and is quoted along with its token.
	distance := ""
	if idx := strings.LastIndexByte(body, ','); idx >= 0 {
		if tail := strings.TrimSpace(body[idx+1:]); isDecimal(tail) {
			body, distance = body[:idx], ", "+tail
		}
	}

	operands := splitFTS5Tokens(body)
	for i, token := range operands {
		operands[i] = quoteNEAROperand(token)
	}
	return "NEAR(" + strings.Join(operands, " ") + distance + ")"
}

// sanitizeFTS5Query wraps bare hyphenated or dotted tokens in double quotes
// so FTS5 does not misinterpret the hyphen as a column-filter separator or
// reject the period as invalid bareword syntax. It also rewrites a leading
// "-term" into a real "NOT term": FTS5 has no unary "-" exclusion operator
// (a lone "-x" is a hard syntax error, and "-col:x" parses but silently
// means something other than exclusion), so passing the hyphen through
// unchanged never actually excludes anything.
func sanitizeFTS5Query(query string) string {
	var result []string
	i := 0
	n := len(query)

	for i < n {
		if query[i] == ' ' || query[i] == '\t' || query[i] == '\n' {
			i++
			continue
		}

		if query[i] == '"' {
			j := i + 1
			for j < n && query[j] != '"' {
				j++
			}
			closed := j < n
			if closed {
				j++
			}
			run := query[i:j]
			if !closed {
				// An unterminated phrase would reach FTS5 as a syntax
				// error; supply the missing closing quote.
				run += "\""
			} else if j < n && query[j] == '*' {
				// "38.101"* is a prefix phrase. Splitting the star off into
				// its own token silently demoted it to an exact phrase
				// match, so keep it attached. A second star is left behind
				// as a separate token ("a"** is a syntax error).
				run += "*"
				j++
			}
			result = append(result, run)
			i = j
			continue
		}

		if i+5 <= n && query[i:i+5] == "NEAR(" {
			j := i + 5
			depth := 1
			for j < n && depth > 0 {
				switch query[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
				j++
			}
			run := query[i:j]
			if depth > 0 {
				// An unterminated NEAR( group is a hard syntax error; supply
				// the missing closing parentheses.
				run += strings.Repeat(")", depth)
			}
			// A NEAR group needs at least one operand — a bareword or a
			// quoted phrase — or FTS5 rejects it: "NEAR()" and
			// punctuation-only groups like "NEAR(,)" are hard syntax
			// errors. A group without one degrades to searching for the
			// literal text instead.
			if !strings.ContainsFunc(run[len("NEAR("):], func(r rune) bool {
				return r == '"' || unicode.IsLetter(r) || unicode.IsDigit(r)
			}) {
				run = quoteFTS5String(query[i:j])
			} else {
				run = sanitizeNEARGroup(run)
			}
			result = append(result, run)
			i = j
			continue
		}

		j := i
		for j < n && query[j] != ' ' && query[j] != '\t' && query[j] != '\n' {
			if query[j] == '"' {
				// A quoted phrase riding inside a token (content:"foo bar")
				// carries its own spaces. Consume it whole, otherwise the
				// split on whitespace tears the phrase in two and the halves
				// end up as separate, unrelated match terms.
				j++
				for j < n && query[j] != '"' {
					j++
				}
				if j < n {
					j++
				}
				continue
			}
			j++
		}
		token := query[i:j]
		i = j

		if fts5Operators[token] {
			// An operator keyword needs an operand on its left: at the start
			// of the query or right after another operator FTS5 rejects it
			// ("syntax error near AND"), so treat it as a literal term there.
			// A dangling operator at the end is quoted after the loop.
			if len(result) == 0 || fts5Operators[result[len(result)-1]] {
				result = append(result, quoteFTS5String(token))
			} else {
				result = append(result, token)
			}
			continue
		}

		if len(token) > 1 && token[0] == '-' {
			// A real NOT needs a preceding phrase as its left operand, and
			// that phrase can't itself be a bare operator keyword (FTS5
			// rejects e.g. "AND NOT" as adjacent operators). When neither
			// holds, there is no valid way to express the exclusion, so
			// fall back to quoting the token literally: it matches
			// nothing, but at least stays valid, non-erroring FTS5 syntax
			// (an unquoted leading hyphen is invalid FTS5 syntax on its own,
			// even without any other special character in the token).
			canNegate := len(result) > 0 && !fts5Operators[result[len(result)-1]]
			if canNegate {
				result = append(result, "NOT", classifyToken(token[1:]))
			} else {
				result = append(result, quoteFTS5String(token))
			}
			continue
		}

		result = append(result, classifyToken(token))
	}

	// An operator keyword at the end of the query has no right operand, which
	// FTS5 rejects; demote it to a literal term.
	if len(result) > 0 && fts5Operators[result[len(result)-1]] {
		result[len(result)-1] = quoteFTS5String(result[len(result)-1])
	}

	return strings.Join(result, " ")
}

// bm25Weights orders search hits: title matches dominate, spec_id barely
// counts (a spec-number query matches the spec_id column of every section of
// that spec, which would otherwise flood the ranking). Column order is
// spec_id, number, title, content. bm25() returns negative scores, smaller =
// better, so ORDER BY stays ascending. Weighting must happen in the query:
// setting the table's rank option needs a write, and serve opens read-only.
const bm25Weights = "bm25(sections_fts, 0.5, 1.0, 5.0, 1.0)"

func (d *DB) Search(ctx context.Context, query string, specIDs []string, limit, offset int) (*SearchResults, error) {
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	if offset < 0 {
		offset = 0
	}

	query = sanitizeFTS5Query(query)

	// sections_fts has no version column, so the version comes from the backing
	// sections row (sections_fts.rowid is sections.id). Joining specs on the
	// pair keeps one row per hit even when a database holds several versions.
	fromWhere := "FROM sections_fts JOIN sections s ON s.id = sections_fts.rowid LEFT JOIN specs p ON p.id = s.spec_id AND p.version = s.version WHERE sections_fts MATCH ?"
	filterArgs := []any{query}

	if len(specIDs) > 0 {
		// Each spec ID also matches its split multi-file parts (e.g. "TS 38.101"
		// matches "TS 38.101-1", "TS 38.101-2", ...). Spec IDs are always in the
		// "TS dd.ddd(-d)?" form, so this can't false-positive-match unrelated specs.
		conds := make([]string, len(specIDs))
		for i, id := range specIDs {
			conds[i] = "sections_fts.spec_id = ? OR sections_fts.spec_id LIKE ? ESCAPE '\\'"
			filterArgs = append(filterArgs, id, EscapeLikePattern(id)+"-%")
		}
		fromWhere += " AND (" + strings.Join(conds, " OR ") + ")"
	}

	// A window count(*) OVER () cannot report the total when offset lands past
	// the last row, so the total comes from its own query sharing the filter.
	var totalCount int
	if err := d.conn.QueryRowContext(ctx, "SELECT count(*) "+fromWhere, filterArgs...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("invalid search query %q: %w", query, err)
	}

	// Column -1 lets FTS5 pick the best-matching column for the snippet, so a
	// title-only hit shows the marked title instead of an unmarked content head.
	// The rowid tie-breaker keeps equal-scoring rows in a stable order, so
	// paging with OFFSET never duplicates or drops a hit.
	sqlQuery := "SELECT sections_fts.spec_id, sections_fts.number, sections_fts.title, snippet(sections_fts, -1, '<mark>', '</mark>', '...', 32), s.version, COALESCE(p.release, '') " +
		fromWhere + " ORDER BY " + bm25Weights + ", sections_fts.rowid LIMIT ? OFFSET ?"
	args := append(append([]any{}, filterArgs...), limit, offset)

	rows, err := d.conn.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("invalid search query %q: %w", query, err)
	}
	defer rows.Close()

	results := []SearchResult{}
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.SpecID, &r.Number, &r.Title, &r.Snippet, &r.Version, &r.Release); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: iterate: %w", err)
	}
	return &SearchResults{Results: results, TotalCount: totalCount, Limit: limit, Offset: offset}, nil
}

// Direction constants for GetReferences.
const (
	DirectionOutgoing = "outgoing"
	DirectionIncoming = "incoming"
)

// Compiled regex patterns for extracting cross-references from section content.
// sp matches ASCII whitespace plus NO-BREAK SPACE (U+00A0) and DEGREE SIGN (U+00B0)
// which appear in 3GPP DOCX documents as word separators.
const sp = `[\s\x{00a0}\x{00b0}]`

// secNum matches section numbers: digit-first (5.1.2a, 5.3A.2) or letter-first for annexes (H, A.1).
// A letter suffix is allowed on every segment to handle mid-number letters such as 5.3A.2 or 4.2.2.2A.1.
const secNum = `([A-Z](?:\.\d+[A-Za-z]?)*|\d+[A-Za-z]?(?:\.\d+[A-Za-z]?)*)`

// secNumRaw is secNum without capture group, used for multi-section list matching.
const secNumRaw = `(?:[A-Z](?:\.\d+[A-Za-z]?)*|\d+[A-Za-z]?(?:\.\d+[A-Za-z]?)*)`

// coordElemTail matches the reference part of one coordinated-list element:
// an optional preposition-plus-keyword or bare keyword, then a section
// number. A preposition requires a keyword after it ("and in clause 4.12.2a"
// but not "and in 2024") so a bare number after "of"/"in" is never read as a
// section reference.
const coordElemTail = `(?:(?:of|in)` + sp + `+(?:[Cc]lauses?|[Ss]ections?|[Ss]ubclauses?|[Aa]nnexe?s?)` + sp + `+|(?:(?:[Cc]lauses?|[Ss]ections?|[Ss]ubclauses?|[Aa]nnexe?s?)` + sp + `+)?)` + secNumRaw

// bareRefChain matches zero or more coordinated references (", clause 4.3",
// " and 4.4", ", and 4.4", "; or Annex B", " and in clause 4.12.2a") so
// bareTrailingQualRE and barePresentDocRE can see through a list to the
// "of"/"in" that qualifies its every element.
const bareRefChain = `(?:` + sp + `*(?:[,;]` + sp + `*(?:and|or)?|and|or)` + sp + `*` + coordElemTail + `)*`

var (
	// "TS 23.501 clause 5.1" or "3GPP TS 33.203 Annex H"
	tsRefRE = regexp.MustCompile(`(?:3GPP` + sp + `+)?(TS|TR)` + sp + `+(\d+\.\d+)(?:` + sp + `*[,;]?` + sp + `*(?:clause|section|subclause|[Aa]nnex)` + sp + `+` + secNum + `)?`)
	// "Annex H of 3GPP TS 33.203" or "subclause 5.1 of TS 23.228"
	tsPrefixRefRE = regexp.MustCompile(`(?:clause|section|subclause|[Aa]nnex)` + sp + `+` + secNum + sp + `+of` + sp + `+(?:3GPP` + sp + `+)?(TS|TR)` + sp + `+(\d+\.\d+)`)
	rfcRefRE      = regexp.MustCompile(`(?:IETF` + sp + `+)?RFC` + sp + `+(\d+)(?:` + sp + `*[,;]?` + sp + `*(?:section|clause)` + sp + `+(\d+(?:\.\d+)*))?`)

	// bracketMapRE extracts [N] -> TS/TR XX.YYY mappings from the References section.
	bracketMapRE = regexp.MustCompile(`\[(\d+[A-Za-z]*)\]` + sp + `+(?:3GPP` + sp + `+)?(TS|TR)` + sp + `+(\d+\.\d+)`)
	// bracketRefRE matches "[N] clause/section/subclause/annex X" patterns.
	bracketRefRE = regexp.MustCompile(`\[(\d+[A-Za-z]*)\]` + sp + `*(?:,` + sp + `*)?(?:clause|section|subclause|[Aa]nnex)` + sp + `+` + secNum)

	// tsMultiPrefixRefRE matches "clauses 8.2 and 16.11 of TS 23.402" with optional trailing "[N]".
	// Groups: 1=keyword, 2=section-list, 3=TS|TR, 4=spec-number, 5=optional bracket number.
	tsMultiPrefixRefRE = regexp.MustCompile(
		`(clauses?|subclauses?|sections?|[Aa]nnexe?s?)` + sp + `+` +
			`(` + secNumRaw + `(?:(?:,` + sp + `*` + secNumRaw + `)*` + sp + `+and` + sp + `+` + secNumRaw + `))\b` + sp + `+` +
			`of` + sp + `+(?:3GPP` + sp + `+)?(TS|TR)` + sp + `+(\d+\.\d+)` +
			`(?:` + sp + `*\[(\d+[A-Za-z]*)\])?`)

	// tsMultiRefRE matches "TS 23.402 clauses 8.2 and 16.11" (spec before multi-section list).
	// Groups: 1=TS|TR, 2=spec-number, 3=keyword, 4=section-list.
	tsMultiRefRE = regexp.MustCompile(
		`(?:3GPP` + sp + `+)?(TS|TR)` + sp + `+(\d+\.\d+)` + sp + `+` +
			`(clauses?|subclauses?|sections?|[Aa]nnexe?s?)` + sp + `+` +
			`(` + secNumRaw + `(?:(?:,` + sp + `*` + secNumRaw + `)*` + sp + `+and` + sp + `+` + secNumRaw + `))\b`)

	// tsCoordPrefixRefRE matches a coordinated list whose elements may repeat
	// the keyword and a preposition before naming the spec —
	// "clause 4.12.2 and in clause 4.12.2a of TS 23.502",
	// "clause 8.2 and in clauses 8.3 and 8.4 of TS 23.402" — which
	// tsMultiPrefixRefRE (bare-number lists) does not cover. Every element
	// belongs to the named spec. Where both patterns match the same range,
	// candidate collection order decides (this pattern first).
	// Groups: 1=reference-list, 2=TS|TR, 3=spec-number.
	tsCoordPrefixRefRE = regexp.MustCompile(
		`((?:[Cc]lauses?|[Ss]ubclauses?|[Ss]ections?|[Aa]nnexe?s?)` + sp + `+` + secNumRaw +
			`(?:` + sp + `*(?:,|and|or)` + sp + `*` + coordElemTail + `)+)` +
			sp + `+(?:of|in)` + sp + `+(?:3GPP` + sp + `+)?(TS|TR)` + sp + `+(\d+\.\d+)`)

	// secNumListRE extracts individual section numbers from a comma/and-separated list.
	secNumListRE = regexp.MustCompile(secNumRaw)

	// coordElemRE extracts one element of a coordinated list matched by
	// tsCoordPrefixRefRE, sharing its keyword classes (plural forms included,
	// unlike bareRefRE; the keyword is optional, as in the list pattern).
	// Group 1 = section number.
	coordElemRE = regexp.MustCompile(`\b(?:(?:[Cc]lauses?|[Ss]ubclauses?|[Ss]ections?|[Aa]nnexe?s?)` + sp + `+)?` + secNum)

	// bareRefRE matches a reference with no spec designator: "clause 4.2",
	// "Subclause 5.15.2", "Annex B". Such a reference means the current
	// document, so it is linkified only when it does not overlap a qualified
	// match and the surrounding context does not tie it to another document
	// (bareLeadingSpecRE, bareTrailingQualRE). Sentence-initial capitals are
	// common in this form, so both cases are accepted.
	bareRefRE = regexp.MustCompile(`\b(?:[Cc]lause|[Ss]ection|[Ss]ubclause|[Aa]nnex)` + sp + `+` + secNum)

	// bareMultiRefRE matches a bare multi-section list: "clauses 4.2, 4.3 and 4.4".
	// Groups: 1=section-list.
	bareMultiRefRE = regexp.MustCompile(
		`\b(?:[Cc]lauses|[Ss]ubclauses|[Ss]ections|[Aa]nnexe?s)` + sp + `+` +
			`(` + secNumRaw + `(?:,` + sp + `*` + secNumRaw + `)*` + sp + `+and` + sp + `+` + secNumRaw + `)\b`)

	// bareTrailingQualRE matches an "of"/"in" continuation after a bare
	// reference, which usually names another document ("clause 5.1 of
	// TS 23.402", "clause 4.2 of [26]", "clause 4 of ITU-T Recommendation
	// X.509"). The chain prefix looks through coordinated references so the
	// first element of "clause 4.2 and clause 4.3 of TS 23.402" is rejected
	// too. RE2 has no lookahead, so these are applied to the text after a
	// bare match instead of being part of bareRefRE.
	bareTrailingQualRE = regexp.MustCompile(`^` + bareRefChain + sp + `+(?:of|in)` + sp + `+`)

	// barePresentDocRE matches the continuations that still mean the current
	// document, re-allowing a bare reference bareTrailingQualRE would reject.
	barePresentDocRE = regexp.MustCompile(`^` + bareRefChain + sp + `+(?:of|in)` + sp +
		`+(?:the` + sp + `+present` + sp + `+(?:document|specification)|this` + sp + `+(?:specification|document))`)

	// bareTrailingParenSpecRE matches a parenthesized designator right after a
	// bare reference ("clause 4.2 (TS 23.402)"), tying it to that document.
	bareTrailingParenSpecRE = regexp.MustCompile(`^` + sp + `*\(` + sp + `*(?:3GPP` + sp +
		`+)?(?:(?:TS|TR)` + sp + `+\d+\.\d+|RFC` + sp + `+\d+|\[\d+[A-Za-z]*\])`)

	// bareLeadingSpecRE matches a spec designator or bracket reference before
	// a bare reference — directly ("TS 23.402 Clause 5.1", "RFC 3748
	// Section 3.1", "[19] Clause 6") or across a coordinated list
	// ("TS 23.402 clause 4.2 and clause 5.1") — qualified territory even when
	// the lowercase-only qualified patterns (and thus the overlap gate) do
	// not cover the element itself.
	bareLeadingSpecRE = regexp.MustCompile(
		`(?:(?:TS|TR)` + sp + `+\d+\.\d+|RFC` + sp + `+\d+|\[\d+[A-Za-z]*\])` +
			`(?:` + sp + `+(?:(?:[Cc]lauses?|[Ss]ections?|[Ss]ubclauses?|[Aa]nnexe?s?)` + sp + `+)?` + secNumRaw + bareRefChain + `)?` +
			sp + `*[,;:]?` + sp + `*(?:(?:and|or)` + sp + `+)?$`)
)

// refExtractor converts regex submatch indices into (targetSpec, targetSection, ok).
// When ok is false, the match should be skipped (e.g. unresolved bracket reference).
type refExtractor func(m []int, content string) (string, string, bool)

func tsExtractor(m []int, content string) (string, string, bool) {
	targetSpec := content[m[2]:m[3]] + " " + content[m[4]:m[5]]
	var targetSection string
	if m[6] >= 0 {
		targetSection = content[m[6]:m[7]]
	}
	return targetSpec, targetSection, true
}

// tsPrefixExtractor handles "clause X of TS YY.ZZZ" where section comes before spec.
// Groups: 1=section, 2=TS|TR, 3=number
func tsPrefixExtractor(m []int, content string) (string, string, bool) {
	targetSpec := content[m[4]:m[5]] + " " + content[m[6]:m[7]]
	targetSection := content[m[2]:m[3]]
	return targetSpec, targetSection, true
}

func rfcExtractor(m []int, content string) (string, string, bool) {
	targetSpec := "RFC " + content[m[2]:m[3]]
	var targetSection string
	if m[4] >= 0 {
		targetSection = content[m[4]:m[5]]
	}
	return targetSpec, targetSection, true
}

// multiRefExtractor converts regex submatch indices into replacement text containing multiple links.
// opts resolves target URLs and validates target sections (see
// LinkifyRefsOpts.resolveTarget). mkLink renders a single link from
// (linkText, url); it lets the caller choose Markdown or HTML link syntax
// depending on the surrounding context. Elements whose target section is
// missing render with unresolvedLink instead.
// Returns (replacementText, ok). When ok is false, the match is skipped.
type multiRefExtractor func(m []int, content string, opts *LinkifyRefsOpts, mkLink func(text, url string) string) (string, bool)

// multiRefSpecSections extracts (spec, []sections) from a multi-section regex match.
// Returns ("", nil, false) if fewer than 2 sections are found.
type multiRefSpecSections func(m []int, content string) (string, []string, bool)

// tsMultiPrefixMRExtractor handles "clauses 8.2 and 16.11 of TS 23.402 [45]".
func tsMultiPrefixMRExtractor(m []int, content string, opts *LinkifyRefsOpts, mkLink func(text, url string) string) (string, bool) {
	keyword := content[m[2]:m[3]]
	secList := content[m[4]:m[5]]
	specType := content[m[6]:m[7]]
	specNum := content[m[8]:m[9]]
	spec := specType + " " + specNum

	sections := secNumListRE.FindAllString(secList, -1)
	if len(sections) < 2 {
		return "", false
	}

	linkedSecList := secNumListRE.ReplaceAllStringFunc(secList, func(sec string) string {
		u, title := opts.resolveTarget(spec, sec)
		if title != "" {
			return unresolvedLink(sec, u, title)
		}
		return mkLink(sec, u)
	})
	specLink := mkLink(specType+" "+specNum, opts.URLFor(spec, ""))
	result := keyword + " " + linkedSecList + " of " + specLink

	if m[10] >= 0 {
		result += " [" + content[m[10]:m[11]] + "]"
	}
	return result, true
}

// tsMultiPrefixSpecSections extracts spec and sections for ExtractReferences.
func tsMultiPrefixSpecSections(m []int, content string) (string, []string, bool) {
	specType := content[m[6]:m[7]]
	specNum := content[m[8]:m[9]]
	spec := specType + " " + specNum
	secList := content[m[4]:m[5]]
	sections := secNumListRE.FindAllString(secList, -1)
	if len(sections) < 2 {
		return "", nil, false
	}
	return spec, sections, true
}

// tsCoordPrefixMRExtractor handles "clause 4.12.2 and in clause 4.12.2a of
// TS 23.502": each keyword-prefixed element links to the named spec. The
// original text — separators, prepositions, the optional 3GPP prefix — is
// kept intact around the links.
func tsCoordPrefixMRExtractor(m []int, content string, opts *LinkifyRefsOpts, mkLink func(text, url string) string) (string, bool) {
	spec := content[m[4]:m[5]] + " " + content[m[6]:m[7]]
	linked := coordElemRE.ReplaceAllStringFunc(content[m[2]:m[3]], func(ref string) string {
		sec := coordElemRE.FindStringSubmatch(ref)[1]
		u, title := opts.resolveTarget(spec, sec)
		if title != "" {
			return ref[:len(ref)-len(sec)] + unresolvedLink(sec, u, title)
		}
		return ref[:len(ref)-len(sec)] + mkLink(sec, u)
	})
	specLink := mkLink(content[m[4]:m[7]], opts.URLFor(spec, ""))
	return linked + content[m[3]:m[4]] + specLink, true
}

// tsCoordPrefixSpecSections extracts spec and sections from a coordinated
// keyword-per-element list for ExtractReferences.
func tsCoordPrefixSpecSections(m []int, content string) (string, []string, bool) {
	spec := content[m[4]:m[5]] + " " + content[m[6]:m[7]]
	list := content[m[2]:m[3]]
	var sections []string
	for _, em := range coordElemRE.FindAllStringSubmatchIndex(list, -1) {
		sections = append(sections, list[em[2]:em[3]])
	}
	if len(sections) < 2 {
		return "", nil, false
	}
	return spec, sections, true
}

// tsMultiMRExtractor handles "TS 23.402 clauses 8.2 and 16.11".
func tsMultiMRExtractor(m []int, content string, opts *LinkifyRefsOpts, mkLink func(text, url string) string) (string, bool) {
	specType := content[m[2]:m[3]]
	specNum := content[m[4]:m[5]]
	keyword := content[m[6]:m[7]]
	secList := content[m[8]:m[9]]
	spec := specType + " " + specNum

	sections := secNumListRE.FindAllString(secList, -1)
	if len(sections) < 2 {
		return "", false
	}

	linkedSecList := secNumListRE.ReplaceAllStringFunc(secList, func(sec string) string {
		u, title := opts.resolveTarget(spec, sec)
		if title != "" {
			return unresolvedLink(sec, u, title)
		}
		return mkLink(sec, u)
	})
	specLink := mkLink(specType+" "+specNum, opts.URLFor(spec, ""))
	return specLink + " " + keyword + " " + linkedSecList, true
}

// tsMultiSpecSections extracts spec and sections for ExtractReferences.
func tsMultiSpecSections(m []int, content string) (string, []string, bool) {
	specType := content[m[2]:m[3]]
	specNum := content[m[4]:m[5]]
	spec := specType + " " + specNum
	secList := content[m[8]:m[9]]
	sections := secNumListRE.FindAllString(secList, -1)
	if len(sections) < 2 {
		return "", nil, false
	}
	return spec, sections, true
}

func bracketExtractor(bracketMap map[string]string) refExtractor {
	return func(m []int, content string) (string, string, bool) {
		bracketNum := content[m[2]:m[3]]
		targetSpec, ok := bracketMap[bracketNum]
		if !ok {
			return "", "", false
		}
		return targetSpec, content[m[4]:m[5]], true
	}
}

// ParseBracketedRefMap extracts [N] -> "TS XX.YYY" or "TR XX.YYY" mappings
// from a references section (typically section 2). Returns nil if no mappings found.
func ParseBracketedRefMap(content string) map[string]string {
	matches := bracketMapRE.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return nil
	}
	m := make(map[string]string, len(matches))
	for _, match := range matches {
		// match[1]=bracket number, match[2]=TS|TR, match[3]=spec number
		m[match[1]] = match[2] + " " + match[3]
	}
	return m
}

// ExtractReferences parses content and returns cross-references to other specs.
// Self-references (matching sourceSpecID) are excluded.
// bracketMap maps bracketed reference numbers (e.g. "19") to spec IDs (e.g. "TS 33.203").
// Pass nil to skip bracketed reference extraction.
func ExtractReferences(sourceSpecID, sectionNumber, content string, bracketMap map[string]string) []Reference {
	seen := make(map[string]bool)
	var refs []Reference

	// Multi-section patterns (produce multiple references per match).
	multiPatterns := []struct {
		re      *regexp.Regexp
		extract multiRefSpecSections
	}{
		{tsCoordPrefixRefRE, tsCoordPrefixSpecSections},
		{tsMultiPrefixRefRE, tsMultiPrefixSpecSections},
		{tsMultiRefRE, tsMultiSpecSections},
	}
	for _, pat := range multiPatterns {
		for _, m := range pat.re.FindAllStringSubmatchIndex(content, -1) {
			spec, sections, ok := pat.extract(m, content)
			if !ok || spec == sourceSpecID {
				continue
			}
			ctx := extractContext(content, m[0], m[1])
			for _, sec := range sections {
				key := spec + "#" + sec
				if seen[key] {
					continue
				}
				seen[key] = true
				refs = append(refs, Reference{
					SourceSpecID:  sourceSpecID,
					SourceSection: sectionNumber,
					TargetSpec:    spec,
					TargetSection: sec,
					Context:       ctx,
				})
			}
		}
	}

	// Single-section patterns.
	patterns := []struct {
		re      *regexp.Regexp
		extract refExtractor
	}{
		{tsPrefixRefRE, tsPrefixExtractor},
		{tsRefRE, tsExtractor},
		{rfcRefRE, rfcExtractor},
	}
	if bracketMap != nil {
		patterns = append(patterns, struct {
			re      *regexp.Regexp
			extract refExtractor
		}{bracketRefRE, bracketExtractor(bracketMap)})
	}

	for _, pat := range patterns {
		for _, m := range pat.re.FindAllStringSubmatchIndex(content, -1) {
			targetSpec, targetSection, ok := pat.extract(m, content)
			if !ok || targetSpec == sourceSpecID {
				continue
			}
			key := targetSpec + "#" + targetSection
			if seen[key] {
				continue
			}
			seen[key] = true
			refs = append(refs, Reference{
				SourceSpecID:  sourceSpecID,
				SourceSection: sectionNumber,
				TargetSpec:    targetSpec,
				TargetSection: targetSection,
				Context:       extractContext(content, m[0], m[1]),
			})
		}
	}
	return refs
}

// extractContext returns a snippet of content around the match [start, end),
// snapping to word boundaries to avoid splitting words or multi-byte characters.
func extractContext(content string, start, end int) string {
	const window = 50
	ctxStart := start - window
	if ctxStart < 0 {
		ctxStart = 0
	}
	if ctxStart > 0 {
		if idx := strings.IndexByte(content[ctxStart:start], ' '); idx >= 0 {
			ctxStart += idx + 1
		}
	}

	ctxEnd := end + window
	if ctxEnd > len(content) {
		ctxEnd = len(content)
	}
	if ctxEnd < len(content) {
		if idx := strings.LastIndexByte(content[end:ctxEnd], ' '); idx >= 0 {
			ctxEnd = end + idx
		}
	}

	// The window and the snapping above work on raw byte offsets, so both ends
	// can still sit inside a multi-byte rune whenever no ASCII space falls in
	// the window (CJK text, no-break spaces, long unbroken tokens). Pull each
	// end onto the nearest rune boundary, inwards, before slicing. The match
	// itself is rune-aligned, so neither loop can cross it and ctxStart <=
	// ctxEnd is preserved.
	for ctxStart < start && !utf8.RuneStart(content[ctxStart]) {
		ctxStart++
	}
	for ctxEnd > end && ctxEnd < len(content) && !utf8.RuneStart(content[ctxEnd]) {
		ctxEnd--
	}

	var b strings.Builder
	if ctxStart > 0 {
		b.WriteString("...")
	}
	b.WriteString(content[ctxStart:ctxEnd])
	if ctxEnd < len(content) {
		b.WriteString("...")
	}
	return b.String()
}

// refBaseQuery takes the target title from whichever version of the target
// spec this database holds; a reference names a spec, not a version of it. The
// title is a scalar subquery rather than a join so a database holding several
// versions of the target cannot duplicate the reference row.
const refBaseQuery = `SELECT r.source_spec_id, r.source_version, r.source_section, r.target_spec, r.target_section, r.context,
	COALESCE((SELECT s.title FROM sections s
	          WHERE s.spec_id = r.target_spec AND s.number = r.target_section
	          ORDER BY s.id LIMIT 1), '')
	FROM spec_references r`

// GetReferences retrieves cross-references in the given direction. For the
// outgoing direction an empty version resolves to the version this database
// holds; incoming references are not version-scoped on the target side.
func (d *DB) GetReferences(ctx context.Context, specID, version, sectionNumber, direction string, includeSubsections bool) ([]Reference, error) {
	if direction == "" {
		direction = DirectionOutgoing
	}

	var where string
	var args []any
	switch direction {
	case DirectionOutgoing:
		resolved, err := d.ResolveVersion(ctx, specID, version)
		if errors.Is(err, ErrNoVersion) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("get references: %w", err)
		}
		if includeSubsections {
			where = ` WHERE r.source_spec_id = ? AND r.source_version = ? AND (r.source_section = ? OR r.source_section LIKE ? || '.%' ESCAPE '\')
				ORDER BY r.source_section, r.target_spec, r.target_section`
			args = []any{specID, resolved, sectionNumber, EscapeLikePattern(sectionNumber)}
		} else {
			where = ` WHERE r.source_spec_id = ? AND r.source_version = ? AND r.source_section = ?
				ORDER BY r.target_spec, r.target_section`
			args = []any{specID, resolved, sectionNumber}
		}
	case DirectionIncoming:
		if sectionNumber != "" {
			where = ` WHERE r.target_spec = ? AND (r.target_section = ? OR r.target_section LIKE ? || '.%' ESCAPE '\' OR r.target_section = '')
				ORDER BY r.source_spec_id, r.source_section`
			args = []any{specID, sectionNumber, EscapeLikePattern(sectionNumber)}
		} else {
			where = ` WHERE r.target_spec = ?
				ORDER BY r.source_spec_id, r.source_section`
			args = []any{specID}
		}
	default:
		return nil, fmt.Errorf("invalid direction %q: must be %s or %s", direction, DirectionOutgoing, DirectionIncoming)
	}

	return d.queryReferences(ctx, refBaseQuery+where, args)
}

func (d *DB) queryReferences(ctx context.Context, query string, args []any) ([]Reference, error) {
	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query references: %w", err)
	}
	defer rows.Close()

	var refs []Reference
	for rows.Next() {
		var r Reference
		if err := rows.Scan(&r.SourceSpecID, &r.SourceVersion, &r.SourceSection, &r.TargetSpec, &r.TargetSection, &r.Context, &r.TargetTitle); err != nil {
			return nil, fmt.Errorf("scan reference: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get references: iterate: %w", err)
	}
	return refs, nil
}
