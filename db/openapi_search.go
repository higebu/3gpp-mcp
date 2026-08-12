package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Chunk kinds stored in openapi_chunks.kind.
const (
	// OpenAPIKindSchema is one entry of components.schemas.
	OpenAPIKindSchema = "schema"
	// OpenAPIKindOperation is one method of one path, named "METHOD /path".
	OpenAPIKindOperation = "operation"
)

// ErrNoOpenAPIIndex reports that this database predates the OpenAPI search
// index. The tables are created by InitSchema, but serve opens the database
// read-only, so a database built by an older version cannot grow them on the
// fly — it has to be rebuilt with build-openapi-index.
var ErrNoOpenAPIIndex = errors.New("openapi search index not built in this database")

// OpenAPIDoc is one row of openapi_specs, as the index builder reads it.
type OpenAPIDoc struct {
	SpecID   string
	APIName  string
	Filename string
	Content  string
}

// OpenAPIChunk is one indexed unit of an OpenAPI definition: a schema, or an
// operation. Body is the rendered text that gets searched.
type OpenAPIChunk struct {
	SpecID   string
	APIName  string
	Filename string
	Kind     string
	Name     string
	Body     string
}

// OpenAPISearchResult is one hit of SearchOpenAPI. Body is only filled when
// the caller asks for it, because a chunk body is far longer than a snippet.
type OpenAPISearchResult struct {
	SpecID  string `json:"spec_id"`
	APIName string `json:"api_name"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Snippet string `json:"snippet"`
	Body    string `json:"body,omitempty"`
}

// OpenAPISearchResults mirrors SearchResults so both search tools page the
// same way.
type OpenAPISearchResults struct {
	Results    []OpenAPISearchResult `json:"results"`
	TotalCount int                   `json:"total_count"`
	Limit      int                   `json:"limit"`
	Offset     int                   `json:"offset"`
}

// AllOpenAPIDocs returns every stored OpenAPI document. The index builder
// needs all of them at once: most $refs in the 5G SBI definitions point into
// another file, so a chunk cannot be rendered from its own document alone.
// Ordering by filename makes a rebuild reproducible.
func (d *DB) AllOpenAPIDocs(ctx context.Context) ([]OpenAPIDoc, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT spec_id, api_name, COALESCE(filename, ''), content FROM openapi_specs ORDER BY filename, spec_id, api_name",
	)
	if err != nil {
		return nil, fmt.Errorf("list openapi documents: %w", err)
	}
	defer rows.Close()

	var docs []OpenAPIDoc
	for rows.Next() {
		var doc OpenAPIDoc
		if err := rows.Scan(&doc.SpecID, &doc.APIName, &doc.Filename, &doc.Content); err != nil {
			return nil, fmt.Errorf("scan openapi document: %w", err)
		}
		docs = append(docs, doc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list openapi documents: iterate: %w", err)
	}
	return docs, nil
}

// ReplaceOpenAPIChunks swaps the whole index for chunks in one transaction.
// The index is derived data with no incremental story — a $ref that starts
// resolving because another document arrived changes chunks in files that
// were not themselves re-imported — so it is always rebuilt wholesale.
func (d *DB) ReplaceOpenAPIChunks(chunks []OpenAPIChunk) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op after Commit per database/sql docs

	// An explicit DELETE keeps the FTS5 delete triggers firing, which
	// INSERT OR REPLACE would skip without recursive_triggers, leaving stale
	// index entries behind that fail later searches with "missing row".
	if _, err := tx.Exec("DELETE FROM openapi_chunks"); err != nil {
		return fmt.Errorf("clear openapi chunks: %w", err)
	}

	stmt, err := tx.Prepare(
		"INSERT INTO openapi_chunks (spec_id, api_name, filename, kind, name, body) VALUES (?, ?, ?, ?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare openapi chunk insert: %w", err)
	}
	defer stmt.Close()

	for _, c := range chunks {
		if _, err := stmt.Exec(c.SpecID, c.APIName, c.Filename, c.Kind, c.Name, c.Body); err != nil {
			return fmt.Errorf("insert openapi chunk %s %s: %w", c.APIName, c.Name, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit openapi chunks: %w", err)
	}
	return nil
}

// HasOpenAPIIndex reports whether this database carries the OpenAPI search
// index tables at all.
func (d *DB) HasOpenAPIIndex(ctx context.Context) (bool, error) {
	var n int
	err := d.conn.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE name IN ('openapi_chunks', 'openapi_chunks_fts')",
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check openapi index: %w", err)
	}
	return n == 2, nil
}

// openapiBM25Weights orders OpenAPI hits: a name match dominates, because the
// question that reaches this index almost always names the thing it wants
// (NFProfile, POST /nf-instances), and api_name barely counts so that querying
// an API's own name does not float every chunk of it above better body
// matches. Column order is api_name, name, body. bm25() returns negative
// scores, smaller = better, so ORDER BY stays ascending. Weighting must happen
// in the query: setting the table's rank option needs a write, and serve opens
// read-only.
const openapiBM25Weights = "bm25(openapi_chunks_fts, 0.5, 5.0, 1.0)"

// openapiNameRank sorts a hit whose name is what the query asked for ahead of
// everything else, before bm25 gets a say.
//
// bm25 alone cannot do this here. It normalizes by document length, and these
// chunks differ in size by three orders of magnitude — the NFProfile schema of
// TS 29.510 renders to ~39 KB once its properties are expanded, while the
// schemas that merely mention NFProfile in a description are a few hundred
// bytes. Weighting the name column does not close that gap, so a search for
// NFProfile returned NFProfile eighth. Whatever else it ranks, this index has
// to answer "the thing called X" with X.
const openapiNameRank = "CASE WHEN lower(c.name) = lower(?) THEN 0 WHEN instr(lower(c.name), lower(?)) > 0 THEN 1 ELSE 2 END, "

// nameHint returns the part of a query that can be compared against a chunk
// name: the query itself when it is one bare term, and nothing when it carries
// FTS5 syntax. A query with operators, phrases or column filters is not a name
// anybody typed, so ranking by name equality against it would be noise.
func nameHint(query string) string {
	q := strings.TrimSpace(query)
	if q == "" || strings.ContainsAny(q, " \t\n\"()*:") {
		return ""
	}
	return q
}

// SearchOpenAPI runs a full-text search over the OpenAPI index. One hit is one
// schema or one operation, never a whole document. kind, when set, must be
// OpenAPIKindSchema or OpenAPIKindOperation.
func (d *DB) SearchOpenAPI(ctx context.Context, query string, specIDs []string, apiName, kind string, includeBody bool, limit, offset int) (*OpenAPISearchResults, error) {
	if limit <= 0 {
		limit = DefaultSearchLimit
	}
	if limit > MaxSearchLimit {
		limit = MaxSearchLimit
	}
	if offset < 0 {
		offset = 0
	}

	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "" && kind != OpenAPIKindSchema && kind != OpenAPIKindOperation {
		return nil, fmt.Errorf("invalid kind %q: want %q or %q", kind, OpenAPIKindSchema, OpenAPIKindOperation)
	}

	// Reported before the query runs, so an old database gets the actionable
	// "rebuild the index" message instead of a bare "no such table".
	ok, err := d.HasOpenAPIIndex(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoOpenAPIIndex
	}

	hint := nameHint(query)
	query = sanitizeFTS5QueryCols(query, openapiFTS5Columns)

	// spec_id and kind live on the backing table only, so every filter is
	// applied through the join rather than as an FTS column.
	fromWhere := "FROM openapi_chunks_fts JOIN openapi_chunks c ON c.id = openapi_chunks_fts.rowid WHERE openapi_chunks_fts MATCH ?"
	filterArgs := []any{query}

	if len(specIDs) > 0 {
		// Each spec ID also matches its split multi-file parts, exactly as in
		// Search: "TS 29.510" covers "TS 29.510-1" and friends.
		conds := make([]string, len(specIDs))
		for i, id := range specIDs {
			conds[i] = "c.spec_id = ? OR c.spec_id LIKE ? ESCAPE '\\'"
			filterArgs = append(filterArgs, id, EscapeLikePattern(id)+"-%")
		}
		fromWhere += " AND (" + strings.Join(conds, " OR ") + ")"
	}
	if apiName != "" {
		// Case-insensitive, because an API name is copied out of prose as
		// often as out of list_openapi and "nnrf_nfmanagement" would
		// otherwise return an empty result indistinguishable from no matches.
		fromWhere += " AND LOWER(c.api_name) = LOWER(?)"
		filterArgs = append(filterArgs, apiName)
	}
	if kind != "" {
		fromWhere += " AND c.kind = ?"
		filterArgs = append(filterArgs, kind)
	}

	// As in Search, the total comes from its own query sharing the filter, so
	// an offset past the last row still reports how many matches there are.
	var totalCount int
	if err := d.conn.QueryRowContext(ctx, "SELECT count(*) "+fromWhere, filterArgs...).Scan(&totalCount); err != nil {
		return nil, fmt.Errorf("invalid search query %q: %w", query, err)
	}

	// Column -1 lets FTS5 pick the best-matching column for the snippet, so a
	// name-only hit shows the marked name. The rowid tie-breaker keeps equal
	// scoring rows in a stable order, so paging never duplicates or drops one.
	body := "''"
	if includeBody {
		body = "c.body"
	}
	// The name rank is dropped entirely rather than passed an empty hint, so a
	// query that is not a name leaves the ordering to bm25 alone.
	rank, rankArgs := "", []any(nil)
	if hint != "" {
		rank, rankArgs = openapiNameRank, []any{hint, hint}
	}
	sqlQuery := "SELECT c.spec_id, c.api_name, c.kind, c.name, " +
		"snippet(openapi_chunks_fts, -1, '<mark>', '</mark>', '...', 32), " + body + " " +
		fromWhere + " ORDER BY " + rank + openapiBM25Weights + ", openapi_chunks_fts.rowid LIMIT ? OFFSET ?"
	args := append(append([]any{}, filterArgs...), rankArgs...)
	args = append(args, limit, offset)

	rows, err := d.conn.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("invalid search query %q: %w", query, err)
	}
	defer rows.Close()

	results := []OpenAPISearchResult{}
	for rows.Next() {
		var r OpenAPISearchResult
		if err := rows.Scan(&r.SpecID, &r.APIName, &r.Kind, &r.Name, &r.Snippet, &r.Body); err != nil {
			return nil, fmt.Errorf("scan openapi search result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("search openapi: iterate: %w", err)
	}
	return &OpenAPISearchResults{Results: results, TotalCount: totalCount, Limit: limit, Offset: offset}, nil
}
