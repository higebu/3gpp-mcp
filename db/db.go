package db

import (
	"database/sql"
	"fmt"
	"log"
	"regexp"
	"strings"

	_ "modernc.org/sqlite"
)

type Spec struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Version string `json:"version,omitempty"`
	Release string `json:"release,omitempty"`
	Series  string `json:"series,omitempty"`
}

type Section struct {
	SpecID       string `json:"spec_id"`
	Number       string `json:"number"`
	Title        string `json:"title"`
	Level        int    `json:"level"`
	ParentNumber string `json:"parent_number,omitempty"`
	Content      string `json:"content,omitempty"`
}

type SearchResult struct {
	SpecID  string `json:"spec_id"`
	Number  string `json:"number"`
	Title   string `json:"title"`
	Snippet string `json:"snippet"`
}

// Image holds an embedded image from a specification.
type Image struct {
	SpecID      string `json:"spec_id"`
	Name        string `json:"name"`
	MIMEType    string `json:"mime_type"`
	Data        []byte `json:"-"`
	LLMReadable bool   `json:"llm_readable"`
}

// ImageInfo holds image metadata without the binary data.
type ImageInfo struct {
	SpecID      string `json:"spec_id"`
	Name        string `json:"name"`
	MIMEType    string `json:"mime_type"`
	LLMReadable bool   `json:"llm_readable"`
}

// Reference represents a cross-reference from one spec section to another spec.
type Reference struct {
	SourceSpecID  string `json:"source_spec_id"`
	SourceSection string `json:"source_section"`
	TargetSpec    string `json:"target_spec"`
	TargetSection string `json:"target_section,omitempty"`
	TargetTitle   string `json:"target_title,omitempty"`
	Context       string `json:"context"`
}

type DB struct {
	conn *sql.DB
}

func Open(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path+"?mode=ro")
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

// Schema is the SQL schema for the 3GPP database.
const Schema = `
CREATE TABLE IF NOT EXISTS specs (
    id TEXT PRIMARY KEY,
    title TEXT NOT NULL,
    version TEXT,
    release TEXT,
    series TEXT
);

CREATE TABLE IF NOT EXISTS sections (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    spec_id TEXT NOT NULL REFERENCES specs(id),
    number TEXT NOT NULL,
    title TEXT NOT NULL,
    level INTEGER NOT NULL,
    parent_number TEXT,
    content TEXT NOT NULL,
    UNIQUE(spec_id, number)
);

CREATE INDEX IF NOT EXISTS idx_sections_spec ON sections(spec_id);
CREATE INDEX IF NOT EXISTS idx_sections_number ON sections(spec_id, number);

CREATE VIRTUAL TABLE IF NOT EXISTS sections_fts USING fts5(
    spec_id, number, title, content,
    content=sections,
    content_rowid=id
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

CREATE TABLE IF NOT EXISTS images (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    spec_id TEXT NOT NULL REFERENCES specs(id),
    name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    data BLOB NOT NULL,
    llm_readable BOOLEAN NOT NULL DEFAULT 0,
    UNIQUE(spec_id, name)
);

CREATE INDEX IF NOT EXISTS idx_images_spec ON images(spec_id);

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
    source_section TEXT NOT NULL,
    target_spec TEXT NOT NULL,
    target_section TEXT NOT NULL DEFAULT '',
    context TEXT NOT NULL,
    UNIQUE(source_spec_id, source_section, target_spec, target_section)
);

CREATE INDEX IF NOT EXISTS idx_ref_source ON spec_references(source_spec_id, source_section);
CREATE INDEX IF NOT EXISTS idx_ref_target ON spec_references(target_spec);
`

// InitSchema creates the database tables and indexes.
func (d *DB) InitSchema() error {
	_, err := d.conn.Exec(Schema)
	return err
}

// UpsertSpec inserts or replaces a spec record.
func (d *DB) UpsertSpec(spec Spec) error {
	_, err := d.conn.Exec(
		"INSERT OR REPLACE INTO specs (id, title, version, release, series) VALUES (?, ?, ?, ?, ?)",
		spec.ID, spec.Title, spec.Version, spec.Release, spec.Series,
	)
	return err
}

// UpsertSection deletes then re-inserts a section to trigger FTS update.
func (d *DB) UpsertSection(section Section) error {
	_, err := d.conn.Exec(
		"DELETE FROM sections WHERE spec_id = ? AND number = ?",
		section.SpecID, section.Number,
	)
	if err != nil {
		return err
	}
	_, err = d.conn.Exec(
		"INSERT INTO sections (spec_id, number, title, level, parent_number, content) VALUES (?, ?, ?, ?, ?, ?)",
		section.SpecID, section.Number, section.Title, section.Level, section.ParentNumber, section.Content,
	)
	return err
}

// UpsertImage inserts or replaces an image record.
func (d *DB) UpsertImage(img Image) error {
	_, err := d.conn.Exec(
		"INSERT OR REPLACE INTO images (spec_id, name, mime_type, data, llm_readable) VALUES (?, ?, ?, ?, ?)",
		img.SpecID, img.Name, img.MIMEType, img.Data, img.LLMReadable,
	)
	return err
}

// GetImage retrieves a single image by spec ID and name.
func (d *DB) GetImage(specID, name string) (*Image, error) {
	var img Image
	err := d.conn.QueryRow(
		"SELECT spec_id, name, mime_type, data, llm_readable FROM images WHERE spec_id = ? AND name = ?",
		specID, name,
	).Scan(&img.SpecID, &img.Name, &img.MIMEType, &img.Data, &img.LLMReadable)
	if err != nil {
		return nil, fmt.Errorf("get image: %w", err)
	}
	return &img, nil
}

// ListImages returns metadata for all images of a spec (without binary data).
func (d *DB) ListImages(specID string) ([]ImageInfo, error) {
	rows, err := d.conn.Query(
		"SELECT spec_id, name, mime_type, llm_readable FROM images WHERE spec_id = ? ORDER BY name",
		specID,
	)
	if err != nil {
		return nil, fmt.Errorf("list images: %w", err)
	}
	defer rows.Close()

	var infos []ImageInfo
	for rows.Next() {
		var info ImageInfo
		if err := rows.Scan(&info.SpecID, &info.Name, &info.MIMEType, &info.LLMReadable); err != nil {
			return nil, fmt.Errorf("scan image info: %w", err)
		}
		infos = append(infos, info)
	}
	return infos, rows.Err()
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
		"INSERT OR REPLACE INTO specs (id, title, version, release, series) VALUES (?, ?, ?, ?, ?)",
		spec.ID, spec.Title, spec.Version, spec.Release, spec.Series,
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
		"DELETE FROM sections WHERE spec_id = ? AND number = ?",
	)
	if err != nil {
		return fmt.Errorf("prepare delete: %w", err)
	}
	defer delStmt.Close()

	insStmt, err := tx.Prepare(
		"INSERT INTO sections (spec_id, number, title, level, parent_number, content) VALUES (?, ?, ?, ?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer insStmt.Close()

	refStmt, err := tx.Prepare(
		"INSERT OR REPLACE INTO spec_references (source_spec_id, source_section, target_spec, target_section, context) VALUES (?, ?, ?, ?, ?)",
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

	for _, s := range sections {
		if _, err = delStmt.Exec(s.SpecID, s.Number); err != nil {
			return fmt.Errorf("delete section: %w", err)
		}
		_, err = insStmt.Exec(s.SpecID, s.Number, s.Title, s.Level, s.ParentNumber, s.Content)
		if err != nil {
			return fmt.Errorf("insert section: %w", err)
		}

		refs := ExtractReferences(s.SpecID, s.Number, s.Content, bracketMap)
		for _, r := range refs {
			_, err = refStmt.Exec(r.SourceSpecID, r.SourceSection, r.TargetSpec, r.TargetSection, r.Context)
			if err != nil {
				return fmt.Errorf("insert reference: %w", err)
			}
		}
	}

	// Insert images if provided.
	if len(images) > 0 {
		imgStmt, err := tx.Prepare(
			"INSERT OR REPLACE INTO images (spec_id, name, mime_type, data, llm_readable) VALUES (?, ?, ?, ?, ?)",
		)
		if err != nil {
			return fmt.Errorf("prepare image insert: %w", err)
		}
		defer imgStmt.Close()

		for _, img := range images {
			_, err = imgStmt.Exec(img.SpecID, img.Name, img.MIMEType, img.Data, img.LLMReadable)
			if err != nil {
				return fmt.Errorf("insert image: %w", err)
			}
		}
	}

	return tx.Commit()
}

func (d *DB) ListSpecs(series string, limit, offset int) (*ListSpecsResult, error) {
	if offset < 0 {
		offset = 0
	}
	where := ""
	var filterArgs []any
	if series != "" {
		where = " WHERE series = ?"
		filterArgs = append(filterArgs, series)
	}

	// Get total count.
	var totalCount int
	if err := d.conn.QueryRow("SELECT COUNT(*) FROM specs"+where, filterArgs...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("count specs: %w", err)
	}

	query := "SELECT id, title, COALESCE(version, ''), COALESCE(release, ''), COALESCE(series, '') FROM specs" + where + " ORDER BY id"
	args := append([]any{}, filterArgs...)

	// limit == 0: use default; limit < 0: no limit (return all rows, internal use only).
	if limit == 0 {
		limit = DefaultListSpecsLimit
	}
	if limit > MaxListSpecsLimit {
		limit = MaxListSpecsLimit
	}
	if limit > 0 {
		query += " LIMIT ? OFFSET ?"
		args = append(args, limit, offset)
	}

	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("list specs: %w", err)
	}
	defer rows.Close()

	var specs []Spec
	for rows.Next() {
		var s Spec
		if err := rows.Scan(&s.ID, &s.Title, &s.Version, &s.Release, &s.Series); err != nil {
			return nil, fmt.Errorf("scan spec: %w", err)
		}
		specs = append(specs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list specs: iterate: %w", err)
	}
	return &ListSpecsResult{Specs: specs, TotalCount: totalCount, Limit: limit, Offset: offset}, nil
}

func (d *DB) GetTOC(specID string) ([]Section, error) {
	rows, err := d.conn.Query(
		"SELECT spec_id, number, title, level, COALESCE(parent_number, '') FROM sections WHERE spec_id = ? ORDER BY id",
		specID,
	)
	if err != nil {
		return nil, fmt.Errorf("get toc: %w", err)
	}
	defer rows.Close()

	var sections []Section
	for rows.Next() {
		var s Section
		if err := rows.Scan(&s.SpecID, &s.Number, &s.Title, &s.Level, &s.ParentNumber); err != nil {
			return nil, fmt.Errorf("scan section: %w", err)
		}
		sections = append(sections, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get toc: iterate: %w", err)
	}
	return sections, nil
}

func (d *DB) GetSection(specID, number string, includeSubsections bool) ([]Section, error) {
	var rows *sql.Rows
	var err error

	if includeSubsections {
		rows, err = d.conn.Query(
			"SELECT spec_id, number, title, level, COALESCE(parent_number, ''), content FROM sections WHERE spec_id = ? AND (number = ? OR number LIKE ? || '.%') ORDER BY id",
			specID, number, number,
		)
	} else {
		rows, err = d.conn.Query(
			"SELECT spec_id, number, title, level, COALESCE(parent_number, ''), content FROM sections WHERE spec_id = ? AND number = ?",
			specID, number,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("get section: %w", err)
	}
	defer rows.Close()

	var sections []Section
	for rows.Next() {
		var s Section
		if err := rows.Scan(&s.SpecID, &s.Number, &s.Title, &s.Level, &s.ParentNumber, &s.Content); err != nil {
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
func (d *DB) GetBracketMap(specID string) (map[string]string, error) {
	rows, err := d.conn.Query(
		`SELECT content FROM sections
		 WHERE spec_id = ? AND (
		   number = '2' OR
		   LOWER(title) = 'references' OR
		   LOWER(title) = 'normative references' OR
		   LOWER(title) = 'informative references'
		 ) ORDER BY id LIMIT 1`,
		specID,
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

func (d *DB) ListOpenAPI(specID string) ([]OpenAPISpec, error) {
	query := "SELECT spec_id, api_name, COALESCE(version, '') FROM openapi_specs"
	var args []any
	if specID != "" {
		query += " WHERE spec_id = ?"
		args = append(args, specID)
	}
	query += " ORDER BY spec_id, api_name"

	rows, err := d.conn.Query(query, args...)
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

func (d *DB) GetOpenAPI(specID, apiName string) (string, error) {
	var content string
	err := d.conn.QueryRow(
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

// sanitizeFTS5Query wraps bare hyphenated tokens in double quotes so FTS5
// does not misinterpret the hyphen as a column-filter separator.
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
			if j < n {
				j++
			}
			result = append(result, query[i:j])
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
			result = append(result, query[i:j])
			i = j
			continue
		}

		j := i
		for j < n && query[j] != ' ' && query[j] != '\t' && query[j] != '\n' {
			j++
		}
		token := query[i:j]
		i = j

		if fts5Operators[token] {
			result = append(result, token)
			continue
		}

		if colIdx := strings.IndexByte(token, ':'); colIdx > 0 {
			col := token[:colIdx]
			val := token[colIdx+1:]
			if fts5Columns[col] {
				if strings.ContainsRune(val, '-') && !strings.HasPrefix(val, "\"") {
					result = append(result, col+":\""+val+"\"")
				} else {
					result = append(result, token)
				}
				continue
			}
		}

		// A leading hyphen is FTS5 NOT shorthand — leave it alone unless
		// there are additional hyphens in the rest of the token.
		if strings.ContainsRune(token, '-') {
			if token[0] == '-' {
				rest := token[1:]
				if strings.ContainsRune(rest, '-') {
					result = append(result, "\""+token+"\"")
				} else {
					result = append(result, token)
				}
			} else {
				result = append(result, "\""+token+"\"")
			}
			continue
		}

		result = append(result, token)
	}

	return strings.Join(result, " ")
}

func (d *DB) Search(query string, specIDs []string, limit int) ([]SearchResult, error) {
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}

	query = sanitizeFTS5Query(query)

	sqlQuery := "SELECT spec_id, number, title, snippet(sections_fts, 3, '<mark>', '</mark>', '...', 32) FROM sections_fts WHERE sections_fts MATCH ?"
	args := []any{query}

	if len(specIDs) == 1 {
		sqlQuery += " AND spec_id = ?"
		args = append(args, specIDs[0])
	} else if len(specIDs) > 1 {
		placeholders := strings.Repeat("?,", len(specIDs))
		sqlQuery += " AND spec_id IN (" + placeholders[:len(placeholders)-1] + ")"
		for _, id := range specIDs {
			args = append(args, id)
		}
	}
	sqlQuery += " ORDER BY rank LIMIT ?"
	args = append(args, limit)

	rows, err := d.conn.Query(sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("invalid search query %q: %w", query, err)
	}
	defer rows.Close()

	var results []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.SpecID, &r.Number, &r.Title, &r.Snippet); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search: iterate: %w", err)
	}
	return results, nil
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

// secNum matches section numbers: digit-first (5.1.2a) or letter-first for annexes (H, A.1).
const secNum = `([A-Z](?:\.\d+)*|\d+(?:\.\d+)*[A-Za-z]?)`

// secNumRaw is secNum without capture group, used for multi-section list matching.
const secNumRaw = `(?:[A-Z](?:\.\d+)*|\d+(?:\.\d+)*[A-Za-z]?)`

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

	// secNumListRE extracts individual section numbers from a comma/and-separated list.
	secNumListRE = regexp.MustCompile(secNumRaw)
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
// urlFor is called with (targetSpec, targetSection) and returns a URL string.
// Returns (replacementText, ok). When ok is false, the match is skipped.
type multiRefExtractor func(m []int, content string, urlFor func(string, string) string) (string, bool)

// multiRefSpecSections extracts (spec, []sections) from a multi-section regex match.
// Returns ("", nil, false) if fewer than 2 sections are found.
type multiRefSpecSections func(m []int, content string) (string, []string, bool)

// tsMultiPrefixMRExtractor handles "clauses 8.2 and 16.11 of TS 23.402 [45]".
func tsMultiPrefixMRExtractor(m []int, content string, urlFor func(string, string) string) (string, bool) {
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
		return "[" + sec + "](" + urlFor(spec, sec) + ")"
	})
	specLink := "[" + specType + " " + specNum + "](" + urlFor(spec, "") + ")"
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

// tsMultiMRExtractor handles "TS 23.402 clauses 8.2 and 16.11".
func tsMultiMRExtractor(m []int, content string, urlFor func(string, string) string) (string, bool) {
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
		return "[" + sec + "](" + urlFor(spec, sec) + ")"
	})
	specLink := "[" + specType + " " + specNum + "](" + urlFor(spec, "") + ")"
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

const refBaseQuery = `SELECT r.source_spec_id, r.source_section, r.target_spec, r.target_section, r.context,
	COALESCE(s.title, '')
	FROM spec_references r
	LEFT JOIN sections s ON r.target_spec = s.spec_id AND r.target_section = s.number`

// GetReferences retrieves cross-references in the given direction.
func (d *DB) GetReferences(specID, sectionNumber, direction string, includeSubsections bool) ([]Reference, error) {
	if direction == "" {
		direction = DirectionOutgoing
	}

	var where string
	var args []any
	switch direction {
	case DirectionOutgoing:
		if includeSubsections {
			where = ` WHERE r.source_spec_id = ? AND (r.source_section = ? OR r.source_section LIKE ? || '.%')
				ORDER BY r.source_section, r.target_spec, r.target_section`
			args = []any{specID, sectionNumber, sectionNumber}
		} else {
			where = ` WHERE r.source_spec_id = ? AND r.source_section = ?
				ORDER BY r.target_spec, r.target_section`
			args = []any{specID, sectionNumber}
		}
	case DirectionIncoming:
		if sectionNumber != "" {
			where = ` WHERE r.target_spec = ? AND (r.target_section = ? OR r.target_section LIKE ? || '.%' OR r.target_section = '')
				ORDER BY r.source_spec_id, r.source_section`
			args = []any{specID, sectionNumber, sectionNumber}
		} else {
			where = ` WHERE r.target_spec = ?
				ORDER BY r.source_spec_id, r.source_section`
			args = []any{specID}
		}
	default:
		return nil, fmt.Errorf("invalid direction %q: must be %s or %s", direction, DirectionOutgoing, DirectionIncoming)
	}

	return d.queryReferences(refBaseQuery+where, args)
}

func (d *DB) queryReferences(query string, args []any) ([]Reference, error) {
	rows, err := d.conn.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query references: %w", err)
	}
	defer rows.Close()

	var refs []Reference
	for rows.Next() {
		var r Reference
		if err := rows.Scan(&r.SourceSpecID, &r.SourceSection, &r.TargetSpec, &r.TargetSection, &r.Context, &r.TargetTitle); err != nil {
			return nil, fmt.Errorf("scan reference: %w", err)
		}
		refs = append(refs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get references: iterate: %w", err)
	}
	return refs, nil
}
