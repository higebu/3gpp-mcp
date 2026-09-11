package tdocstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/higebu/3gpp-mcp/internal/converter/pipeline"
	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/tdoc"
)

// A meeting's TDoc list — the secretary's per-document metadata — is cached
// here beside the documents themselves, one list per meeting, with a small
// FTS5 index over its prose columns for meeting-scoped search. Lists are
// refreshed while a meeting is recent (its status column changes daily
// during the meeting) and kept as final afterwards.

const listSchema = `
CREATE TABLE IF NOT EXISTS tdoc_lists (
    meeting_dir TEXT PRIMARY KEY,
    group_name TEXT NOT NULL,
    meeting_code TEXT NOT NULL,
    meeting_title TEXT NOT NULL,
    meeting_end TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL,
    entries INTEGER NOT NULL,
    fetched_at INTEGER NOT NULL,
    last_used_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tdoc_entries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    meeting_dir TEXT NOT NULL,
    ordinal INTEGER NOT NULL,
    tdoc TEXT NOT NULL,
    title TEXT NOT NULL,
    source TEXT NOT NULL,
    type TEXT NOT NULL,
    purpose TEXT NOT NULL,
    abstract TEXT NOT NULL,
    agenda_item TEXT NOT NULL,
    agenda_desc TEXT NOT NULL,
    status TEXT NOT NULL,
    revision_of TEXT NOT NULL,
    revised_to TEXT NOT NULL,
    release TEXT NOT NULL,
    spec TEXT NOT NULL,
    version TEXT NOT NULL,
    work_items TEXT NOT NULL,
    cr TEXT NOT NULL,
    cr_revision TEXT NOT NULL,
    cr_category TEXT NOT NULL,
    clauses TEXT NOT NULL,
    reply_to TEXT NOT NULL,
    to_groups TEXT NOT NULL,
    cc_groups TEXT NOT NULL,
    original_ls TEXT NOT NULL,
    reply_in TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tdoc_entries_meeting ON tdoc_entries(meeting_dir, ordinal);
CREATE INDEX IF NOT EXISTS idx_tdoc_entries_tdoc ON tdoc_entries(tdoc);

CREATE VIRTUAL TABLE IF NOT EXISTS tdoc_entries_fts USING fts5(
    title, source, abstract, agenda_desc, work_items,
    content='tdoc_entries',
    content_rowid='id',
    tokenize="porter unicode61 tokenchars '-_'"
);

CREATE TRIGGER IF NOT EXISTS tdoc_entries_ai AFTER INSERT ON tdoc_entries BEGIN
    INSERT INTO tdoc_entries_fts(rowid, title, source, abstract, agenda_desc, work_items)
    VALUES (new.id, new.title, new.source, new.abstract, new.agenda_desc, new.work_items);
END;

CREATE TRIGGER IF NOT EXISTS tdoc_entries_ad AFTER DELETE ON tdoc_entries BEGIN
    INSERT INTO tdoc_entries_fts(tdoc_entries_fts, rowid, title, source, abstract, agenda_desc, work_items)
    VALUES ('delete', old.id, old.title, old.source, old.abstract, old.agenda_desc, old.work_items);
END;
`

// listTables are the list tables, dropped in this order on a cache reset.
var listTables = []string{"tdoc_entries_fts", "tdoc_entries", "tdoc_lists"}

// entryColumns names the tdoc_entries columns in tdoc.Entry field order.
const entryColumns = "tdoc, title, source, type, purpose, abstract, agenda_item, agenda_desc, status, revision_of, revised_to, release, spec, version, work_items, cr, cr_revision, cr_category, clauses, reply_to, to_groups, cc_groups, original_ls, reply_in"

// ftsColumns are the searchable columns, for column filters in a query.
var ftsColumns = []string{"title", "source", "abstract", "agenda_desc", "work_items"}

// MaxLists caps how many meetings' lists are kept; the least recently used
// is dropped past it. A list is a few hundred kilobytes, so this is a
// bound on clutter rather than on size.
const MaxLists = 64

// ListFreshFor is how long after a meeting ends its list is still
// re-downloaded when older than the listing cache TTL. Statuses and
// revisions settle in the weeks after a meeting; a list older than this is
// final.
const ListFreshFor = 30 * 24 * time.Hour

// List is a cached meeting TDoc list's record.
type List struct {
	Group        string `json:"group"`
	MeetingCode  string `json:"meeting_code"`
	MeetingTitle string `json:"meeting_title"`
	MeetingDir   string `json:"meeting_dir"`
	// Path is where the list was downloaded from, relative to the FTP root.
	Path      string    `json:"path"`
	Entries   int       `json:"entries"`
	FetchedAt time.Time `json:"fetched_at"`
}

// URL is the list's download URL.
func (l List) URL() string {
	return tdoc.Document{Path: l.Path}.URL()
}

// ListFetcher downloads and parses one meeting's list. Only tests set it.
type ListFetcher func(ctx context.Context, m tdoc.Meeting) ([]tdoc.Entry, string, error)

// Filter narrows a list. Every set field must match; the zero Filter
// matches every entry.
type Filter struct {
	// AgendaItem matches the item and its sub-items: "9.1" takes "9.1",
	// "9.1.1", ... but not "9.10".
	AgendaItem string
	// Type and Status match case-insensitively and exactly.
	Type   string
	Status string
	// Source matches case-insensitively as a substring, so one company of
	// a multi-source contribution is found.
	Source string
	// Spec matches the spec number of a CR or draft, "38.331".
	Spec string
	// Query is a full-text query over title, source, abstract, agenda item
	// description and work items.
	Query  string
	Limit  int
	Offset int
}

// IsEmpty reports whether the filter narrows nothing.
func (f Filter) IsEmpty() bool {
	return f.AgendaItem == "" && f.Type == "" && f.Status == "" && f.Source == "" && f.Spec == "" && f.Query == ""
}

// String describes the filter for a reader: "agenda item 9.1, type CR".
func (f Filter) String() string {
	var parts []string
	for _, kv := range [][2]string{
		{"agenda item", f.AgendaItem}, {"type", f.Type}, {"status", f.Status},
		{"source", f.Source}, {"spec", f.Spec}, {"query", f.Query},
	} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+" "+kv[1])
		}
	}
	return strings.Join(parts, ", ")
}

// ListResult is one page of a filtered list.
type ListResult struct {
	Entries []tdoc.Entry
	// Total is the number of entries matching the filter; Count the number
	// in the whole list.
	Total int
	Count int
}

// AgendaItem is one agenda item of a meeting with its document count.
type AgendaItem struct {
	Item        string `json:"item"`
	Description string `json:"description"`
	Count       int    `json:"count"`
}

// EnsureList makes a meeting's TDoc list available, downloading it when it
// is missing and refreshing it when stale. A stale list is served as it is
// when the refresh fails or outlives budget, since an outdated status
// column beats no list; a missing one reports the failure. Like Ensure, a
// download that outlives budget continues detached and ErrInProgress asks
// the caller to retry.
func (s *Store) EnsureList(ctx context.Context, g tdoc.Group, m tdoc.Meeting, budget time.Duration) error {
	if m.Dir == "" {
		return fmt.Errorf("%w: %s has no meeting folder yet", tdoc.ErrNoTDocList, m.Code)
	}
	fetchedAt, cached, err := s.listAge(m.Dir)
	if err != nil {
		return err
	}
	if cached && !listStale(fetchedAt, m.End, time.Now()) {
		return nil
	}
	key := "list:" + m.Dir
	err = s.group.Do(ctx, key, budget,
		func() (bool, error) {
			at, ok, err := s.listAge(m.Dir)
			return ok && !listStale(at, m.End, time.Now()), err
		},
		func(ctx context.Context) error {
			entries, path, err := s.fetchList(ctx, m)
			if err != nil {
				return err
			}
			return s.putList(g, m, path, entries)
		})
	if err != nil && cached {
		if !errors.Is(err, ErrInProgress) {
			log.Printf("warning: TDoc list of %s not refreshed, serving the cached one: %v", m.Code, err)
		}
		return nil
	}
	return err
}

// listStale reports whether a list fetched at fetchedAt needs re-download:
// it is older than the listing cache TTL and the meeting (ending on end,
// "2006-01-02"; an unknown end counts as ongoing) ended recently enough for
// the list to still change.
func listStale(fetchedAt time.Time, end string, now time.Time) bool {
	if now.Sub(fetchedAt) < pipeline.CacheTTL() {
		return false
	}
	ended, err := time.Parse("2006-01-02", end)
	if err != nil {
		return true
	}
	return now.Before(ended.Add(ListFreshFor))
}

func (s *Store) listAge(meetingDir string) (time.Time, bool, error) {
	var fetched int64
	err := s.conn.QueryRow("SELECT fetched_at FROM tdoc_lists WHERE meeting_dir = ?", meetingDir).Scan(&fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, fmt.Errorf("check tdoc list cache: %w", err)
	}
	return time.Unix(fetched, 0), true, nil
}

func (s *Store) fetchList(ctx context.Context, m tdoc.Meeting) ([]tdoc.Entry, string, error) {
	if s.listFetcher != nil {
		return s.listFetcher(ctx, m)
	}
	return tdoc.FetchTDocList(ctx, s.client, m)
}

// putList replaces a meeting's cached list and drops the least recently
// used lists past MaxLists.
func (s *Store) putList(g tdoc.Group, m tdoc.Meeting, path string, entries []tdoc.Entry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin cache transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM tdoc_entries WHERE meeting_dir = ?", m.Dir); err != nil {
		return fmt.Errorf("clear cached tdoc list: %w", err)
	}
	stmt, err := tx.Prepare("INSERT INTO tdoc_entries (meeting_dir, ordinal, " + entryColumns + ") VALUES (?, ?" + strings.Repeat(", ?", strings.Count(entryColumns, ",")+1) + ")")
	if err != nil {
		return fmt.Errorf("prepare tdoc list insert: %w", err)
	}
	defer stmt.Close()
	for i, e := range entries {
		if _, err := stmt.Exec(m.Dir, i,
			e.TDoc, e.Title, e.Source, e.Type, e.For, e.Abstract, e.AgendaItem, e.AgendaDescription, e.Status,
			e.IsRevisionOf, e.RevisedTo, e.Release, e.Spec, e.Version, e.RelatedWIs, e.CR, e.CRRevision, e.CRCategory,
			e.ClausesAffected, e.ReplyTo, e.To, e.Cc, e.OriginalLS, e.ReplyIn,
		); err != nil {
			return fmt.Errorf("cache tdoc list entry: %w", err)
		}
	}
	now := time.Now().Unix()
	if _, err := tx.Exec(
		"INSERT OR REPLACE INTO tdoc_lists (meeting_dir, group_name, meeting_code, meeting_title, meeting_end, path, entries, fetched_at, last_used_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		m.Dir, g.Name, m.Code, m.Title, m.End, path, len(entries), now, now,
	); err != nil {
		return fmt.Errorf("record cached tdoc list: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit cache transaction: %w", err)
	}
	if err := s.evictLists(m.Dir); err != nil {
		log.Printf("warning: tdoc list eviction failed: %v", err)
	}
	return nil
}

func (s *Store) evictLists(keepDir string) error {
	for {
		var n int
		if err := s.conn.QueryRow("SELECT COUNT(*) FROM tdoc_lists").Scan(&n); err != nil {
			return fmt.Errorf("count tdoc lists: %w", err)
		}
		if n <= MaxLists {
			return nil
		}
		var victim string
		err := s.conn.QueryRow("SELECT meeting_dir FROM tdoc_lists WHERE meeting_dir != ? ORDER BY last_used_at, rowid LIMIT 1", keepDir).Scan(&victim)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("pick tdoc list to evict: %w", err)
		}
		if err := s.deleteList(victim); err != nil {
			return err
		}
		log.Printf("tdoc cache: evicted TDoc list of %s", victim)
	}
}

func (s *Store) deleteList(meetingDir string) error {
	tx, err := s.conn.Begin()
	if err != nil {
		return fmt.Errorf("begin eviction: %w", err)
	}
	defer tx.Rollback()
	for _, q := range []string{
		"DELETE FROM tdoc_entries WHERE meeting_dir = ?",
		"DELETE FROM tdoc_lists WHERE meeting_dir = ?",
	} {
		if _, err := tx.Exec(q, meetingDir); err != nil {
			return fmt.Errorf("evict list %s: %w", meetingDir, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit eviction: %w", err)
	}
	return nil
}

// GetList returns a cached list's record, or nil when the meeting's list is
// not cached.
func (s *Store) GetList(meetingDir string) (*List, error) {
	s.touchList(meetingDir)
	var l List
	var fetched int64
	err := s.conn.QueryRow(
		"SELECT group_name, meeting_code, meeting_title, meeting_dir, path, entries, fetched_at FROM tdoc_lists WHERE meeting_dir = ?", meetingDir,
	).Scan(&l.Group, &l.MeetingCode, &l.MeetingTitle, &l.MeetingDir, &l.Path, &l.Entries, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get cached tdoc list: %w", err)
	}
	l.FetchedAt = time.Unix(fetched, 0)
	return &l, nil
}

func (s *Store) touchList(meetingDir string) {
	if _, err := s.conn.Exec("UPDATE tdoc_lists SET last_used_at = ? WHERE meeting_dir = ?", time.Now().Unix(), meetingDir); err != nil {
		log.Printf("warning: tdoc cache touch failed for list %s: %v", meetingDir, err)
	}
}

// ListEntries returns one page of a meeting's cached list, in list order,
// narrowed by f. Count is 0 when the meeting's list is not cached.
func (s *Store) ListEntries(meetingDir string, f Filter) (*ListResult, error) {
	where := []string{"meeting_dir = ?"}
	args := []any{meetingDir}
	if v := strings.TrimSpace(f.AgendaItem); v != "" {
		where = append(where, "(agenda_item = ? OR agenda_item LIKE ? || '.%' ESCAPE '\\')")
		args = append(args, v, db.EscapeLikePattern(v))
	}
	if v := strings.TrimSpace(f.Type); v != "" {
		where = append(where, "type = ? COLLATE NOCASE")
		args = append(args, v)
	}
	if v := strings.TrimSpace(f.Status); v != "" {
		where = append(where, "status = ? COLLATE NOCASE")
		args = append(args, v)
	}
	if v := strings.TrimSpace(f.Source); v != "" {
		where = append(where, "source LIKE '%' || ? || '%' ESCAPE '\\' COLLATE NOCASE")
		args = append(args, db.EscapeLikePattern(v))
	}
	if v := strings.TrimSpace(f.Spec); v != "" {
		where = append(where, "spec = ?")
		args = append(args, v)
	}
	if v := strings.TrimSpace(f.Query); v != "" {
		where = append(where, "id IN (SELECT rowid FROM tdoc_entries_fts WHERE tdoc_entries_fts MATCH ?)")
		args = append(args, db.SanitizeFTS5Query(v, ftsColumns))
	}
	cond := strings.Join(where, " AND ")

	res := &ListResult{}
	if err := s.conn.QueryRow("SELECT COUNT(*) FROM tdoc_entries WHERE meeting_dir = ?", meetingDir).Scan(&res.Count); err != nil {
		return nil, fmt.Errorf("count cached tdoc list: %w", err)
	}
	if err := s.conn.QueryRow("SELECT COUNT(*) FROM tdoc_entries WHERE "+cond, args...).Scan(&res.Total); err != nil {
		return nil, fmt.Errorf("count matching tdocs: %w", err)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = -1
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}
	rows, err := s.conn.Query("SELECT "+entryColumns+" FROM tdoc_entries WHERE "+cond+" ORDER BY ordinal LIMIT ? OFFSET ?", append(args, limit, offset)...)
	if err != nil {
		return nil, fmt.Errorf("query cached tdoc list: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var e tdoc.Entry
		if err := rows.Scan(&e.TDoc, &e.Title, &e.Source, &e.Type, &e.For, &e.Abstract, &e.AgendaItem, &e.AgendaDescription, &e.Status,
			&e.IsRevisionOf, &e.RevisedTo, &e.Release, &e.Spec, &e.Version, &e.RelatedWIs, &e.CR, &e.CRRevision, &e.CRCategory,
			&e.ClausesAffected, &e.ReplyTo, &e.To, &e.Cc, &e.OriginalLS, &e.ReplyIn); err != nil {
			return nil, fmt.Errorf("scan cached tdoc list entry: %w", err)
		}
		res.Entries = append(res.Entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query cached tdoc list: iterate: %w", err)
	}
	return res, nil
}

// AgendaItems returns a meeting's agenda items with their document counts,
// in agenda order.
func (s *Store) AgendaItems(meetingDir string) ([]AgendaItem, error) {
	rows, err := s.conn.Query(
		"SELECT agenda_item, MAX(agenda_desc), COUNT(*) FROM tdoc_entries WHERE meeting_dir = ? GROUP BY agenda_item", meetingDir)
	if err != nil {
		return nil, fmt.Errorf("query agenda items: %w", err)
	}
	defer rows.Close()
	var items []AgendaItem
	for rows.Next() {
		var it AgendaItem
		if err := rows.Scan(&it.Item, &it.Description, &it.Count); err != nil {
			return nil, fmt.Errorf("scan agenda item: %w", err)
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query agenda items: iterate: %w", err)
	}
	sortAgendaItems(items)
	return items, nil
}

// Values returns the distinct values of a list column — "type" or
// "status" — for a meeting, most frequent first, so a filter form can offer
// them.
func (s *Store) Values(meetingDir, column string) ([]string, error) {
	if column != "type" && column != "status" {
		return nil, fmt.Errorf("no such list column %q", column)
	}
	rows, err := s.conn.Query(
		"SELECT "+column+" FROM tdoc_entries WHERE meeting_dir = ? AND "+column+" != '' GROUP BY "+column+" ORDER BY COUNT(*) DESC, "+column, meetingDir)
	if err != nil {
		return nil, fmt.Errorf("query list %s values: %w", column, err)
	}
	defer rows.Close()
	var values []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, fmt.Errorf("scan list %s value: %w", column, err)
		}
		values = append(values, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query list %s values: iterate: %w", column, err)
	}
	return values, nil
}

// sortAgendaItems orders items numerically segment by segment, so 9.2
// precedes 9.10 and both precede 10; a non-numeric segment sorts after the
// numbers, alphabetically, which puts the documents without an agenda item
// (an empty item) last.
func sortAgendaItems(items []AgendaItem) {
	less := func(a, b string) bool {
		as, bs := strings.Split(a, "."), strings.Split(b, ".")
		for i := 0; i < len(as) && i < len(bs); i++ {
			if as[i] == bs[i] {
				continue
			}
			an, aerr := strconv.Atoi(as[i])
			bn, berr := strconv.Atoi(bs[i])
			switch {
			case aerr == nil && berr == nil:
				return an < bn
			case aerr == nil:
				return true
			case berr == nil:
				return false
			default:
				return as[i] < bs[i]
			}
		}
		return len(as) < len(bs)
	}
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && less(items[j].Item, items[j-1].Item); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}
