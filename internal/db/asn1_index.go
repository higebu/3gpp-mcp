package db

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoASN1Index reports that this database predates the ASN.1 name index.
// serve opens the database read-only and cannot create it; build-asn1-index
// adds it in place.
var ErrNoASN1Index = errors.New("asn1 name index not built in this database")

// ASN1IndexSchema defines the ASN.1 name index: one row per top-level
// assignment extracted from the ```asn1 fences, written at build time by
// internal/asn1index. Unlike the OpenAPI index it needs no FTS twin — lookups
// are by name or by the separator-folded key, both equality matches.
const ASN1IndexSchema = `
CREATE TABLE IF NOT EXISTS asn1_defs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    key TEXT NOT NULL,
    spec_id TEXT NOT NULL,
    version TEXT NOT NULL,
    section_number TEXT NOT NULL,
    section_title TEXT NOT NULL,
    body TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_asn1_defs_name ON asn1_defs(name);
CREATE INDEX IF NOT EXISTS idx_asn1_defs_key ON asn1_defs(key);
CREATE INDEX IF NOT EXISTS idx_asn1_defs_spec ON asn1_defs(spec_id);
`

// ASN1Def is one indexed ASN.1 assignment. Release is joined from specs on
// read and empty on write; Body is empty in listing results.
type ASN1Def struct {
	Name          string
	Key           string
	SpecID        string
	Version       string
	Release       string
	SectionNumber string
	SectionTitle  string
	Body          string
}

// HasASN1Index reports whether this database carries the ASN.1 name index
// table at all. An empty table is an index that was built over a corpus
// without ASN.1, not a missing one.
func (d *DB) HasASN1Index(ctx context.Context) (bool, error) {
	var n int
	err := d.conn.QueryRowContext(ctx,
		"SELECT count(*) FROM sqlite_master WHERE name = 'asn1_defs'",
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check asn1 index: %w", err)
	}
	return n == 1, nil
}

// ReplaceASN1Defs swaps the whole index for defs in one transaction, so a
// rebuild that does not finish changes nothing.
func (d *DB) ReplaceASN1Defs(defs []ASN1Def) error {
	tx, err := d.conn.Begin()
	if err != nil {
		return fmt.Errorf("replace asn1 defs: begin transaction: %w", err)
	}
	defer tx.Rollback() // no-op after Commit per database/sql docs

	if _, err := tx.Exec("DELETE FROM asn1_defs"); err != nil {
		return fmt.Errorf("clear asn1 defs: %w", err)
	}

	stmt, err := tx.Prepare(
		"INSERT INTO asn1_defs (name, key, spec_id, version, section_number, section_title, body) VALUES (?, ?, ?, ?, ?, ?, ?)",
	)
	if err != nil {
		return fmt.Errorf("prepare asn1 def insert: %w", err)
	}
	defer stmt.Close()

	for _, def := range defs {
		if _, err := stmt.Exec(def.Name, def.Key, def.SpecID, def.Version, def.SectionNumber, def.SectionTitle, def.Body); err != nil {
			return fmt.Errorf("insert asn1 def %s (%s): %w", def.Name, def.SpecID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit asn1 defs: %w", err)
	}
	return nil
}

// DropASN1Index removes the index table. It is the recovery for a rebuild
// that failed after sections changed: a stale index answers with definitions
// the corpus no longer holds, while a missing one is visibly missing and
// rebuildable with build-asn1-index. InitSchema recreates the table.
func (d *DB) DropASN1Index() error {
	if _, err := d.conn.Exec("DROP TABLE IF EXISTS asn1_defs"); err != nil {
		return fmt.Errorf("drop asn1 index: %w", err)
	}
	return nil
}

const asn1DefColumns = `a.name, a.key, a.spec_id, a.version, COALESCE(p.release, ''), a.section_number, a.section_title, a.body
	FROM asn1_defs a LEFT JOIN specs p ON p.id = a.spec_id AND p.version = a.version`

// LookupASN1 resolves an assignment name: every definition of the exact name
// first, and failing that, definitions whose separator- and case-folded key
// matches — pinned to the first such name in document order, so a fuzzy key
// collision resolves to one name rather than a mixture. A non-empty specID
// restricts the lookup to that specification.
func (d *DB) LookupASN1(ctx context.Context, name, key, specID string) ([]ASN1Def, error) {
	if ok, err := d.HasASN1Index(ctx); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNoASN1Index
	}

	defs, err := d.queryASN1(ctx, "a.name = ?", name, specID)
	if err != nil || len(defs) > 0 {
		return defs, err
	}
	if key == "" {
		return nil, nil
	}
	fuzzy, err := d.queryASN1(ctx, "a.key = ?", key, specID)
	if err != nil || len(fuzzy) == 0 {
		return nil, err
	}
	first := fuzzy[0].Name
	defs = fuzzy[:0]
	for _, def := range fuzzy {
		if def.Name == first {
			defs = append(defs, def)
		}
	}
	return defs, nil
}

func (d *DB) queryASN1(ctx context.Context, cond, value, specID string) ([]ASN1Def, error) {
	query := "SELECT " + asn1DefColumns + " WHERE " + cond
	args := []any{value}
	if specID != "" {
		query += " AND a.spec_id = ?"
		args = append(args, specID)
	}
	query += " ORDER BY a.id"

	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lookup asn1: %w", err)
	}
	defer rows.Close()

	var defs []ASN1Def
	for rows.Next() {
		var def ASN1Def
		if err := rows.Scan(&def.Name, &def.Key, &def.SpecID, &def.Version, &def.Release, &def.SectionNumber, &def.SectionTitle, &def.Body); err != nil {
			return nil, fmt.Errorf("scan asn1 def: %w", err)
		}
		defs = append(defs, def)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lookup asn1: iterate: %w", err)
	}
	return defs, nil
}

// ASN1SpecListing returns a specification's assignments in document order,
// names and defining sections only — bodies stay in the database, a listing
// of a large protocol spec runs to thousands of rows.
func (d *DB) ASN1SpecListing(ctx context.Context, specID string) ([]ASN1Def, error) {
	if ok, err := d.HasASN1Index(ctx); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNoASN1Index
	}

	rows, err := d.conn.QueryContext(ctx,
		`SELECT a.name, a.spec_id, a.version, COALESCE(p.release, ''), a.section_number, a.section_title
		 FROM asn1_defs a LEFT JOIN specs p ON p.id = a.spec_id AND p.version = a.version
		 WHERE a.spec_id = ? ORDER BY a.id`, specID)
	if err != nil {
		return nil, fmt.Errorf("list asn1: %w", err)
	}
	defer rows.Close()

	var defs []ASN1Def
	for rows.Next() {
		var def ASN1Def
		if err := rows.Scan(&def.Name, &def.SpecID, &def.Version, &def.Release, &def.SectionNumber, &def.SectionTitle); err != nil {
			return nil, fmt.Errorf("scan asn1 listing: %w", err)
		}
		defs = append(defs, def)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list asn1: iterate: %w", err)
	}
	return defs, nil
}

// ASN1NameSuggestions lists names related to a key that resolved nothing:
// names whose key contains it, or that it contains — the query may as well be
// more specific than the defined name. Deduplicated, in document order,
// capped. A non-empty specID restricts the suggestions to that specification.
func (d *DB) ASN1NameSuggestions(ctx context.Context, key, specID string, limit int) ([]string, error) {
	if key == "" {
		return nil, nil
	}
	if ok, err := d.HasASN1Index(ctx); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNoASN1Index
	}

	query := "SELECT name FROM asn1_defs WHERE (instr(key, ?) > 0 OR instr(?, key) > 0)"
	args := []any{key, key}
	if specID != "" {
		query += " AND spec_id = ?"
		args = append(args, specID)
	}
	query += " GROUP BY name ORDER BY MIN(id) LIMIT ?"
	args = append(args, limit)

	rows, err := d.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("asn1 suggestions: %w", err)
	}
	defer rows.Close()

	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan asn1 suggestion: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("asn1 suggestions: iterate: %w", err)
	}
	return names, nil
}

// ASN1DefSpecs dedupes the specifications of a lookup result, in order.
func ASN1DefSpecs(defs []ASN1Def) []string {
	var specs []string
	seen := make(map[string]bool)
	for _, def := range defs {
		if !seen[def.SpecID] {
			seen[def.SpecID] = true
			specs = append(specs, def.SpecID)
		}
	}
	return specs
}

// ASN1IndexStats summarizes an index for the rebuild report.
func ASN1IndexStats(defs []ASN1Def) string {
	specs := make(map[string]bool)
	sections := make(map[[2]string]bool)
	for _, def := range defs {
		specs[def.SpecID] = true
		sections[[2]string{def.SpecID, def.SectionNumber}] = true
	}
	return fmt.Sprintf("%d assignments from %d sections across %d specs", len(defs), len(sections), len(specs))
}
