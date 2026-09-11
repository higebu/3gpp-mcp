package tdoc

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/converter/pipeline"
)

// Entry is one row of a meeting's TDoc list: the metadata the secretary
// records for every document, as published in the meeting's
// TDoc_List_Meeting_<group>#<n>.xlsx. Empty fields are the norm — a
// discussion paper has no CR number, an LS no agenda item description.
type Entry struct {
	TDoc   string `json:"tdoc"`
	Title  string `json:"title"`
	Source string `json:"source"`
	// Type is the document kind as the secretary classes it: "CR",
	// "draftCR", "pCR", "discussion", "LS in", "LS out", "agenda",
	// "report", "WID new", "SID new", "other", ...
	Type string `json:"type,omitempty"`
	// For is the decision sought: "Approval", "Agreement", "Decision",
	// "Discussion", "Information", "Endorsement".
	For               string `json:"for,omitempty"`
	Abstract          string `json:"abstract,omitempty"`
	AgendaItem        string `json:"agenda_item,omitempty"`
	AgendaDescription string `json:"agenda_description,omitempty"`
	// Status is the outcome: "available", "revised", "agreed", "approved",
	// "noted", "withdrawn", "postponed", "merged", "not treated", "reserved",
	// "endorsed", "rejected", "treated", "replied to", ...
	Status       string `json:"status,omitempty"`
	IsRevisionOf string `json:"is_revision_of,omitempty"`
	RevisedTo    string `json:"revised_to,omitempty"`
	Release      string `json:"release,omitempty"`
	// Spec, Version, CR, CRRevision, CRCategory and ClausesAffected are
	// filled for a change request.
	Spec            string `json:"spec,omitempty"`
	Version         string `json:"version,omitempty"`
	RelatedWIs      string `json:"related_wis,omitempty"`
	CR              string `json:"cr,omitempty"`
	CRRevision      string `json:"cr_revision,omitempty"`
	CRCategory      string `json:"cr_category,omitempty"`
	ClausesAffected string `json:"clauses_affected,omitempty"`
	// ReplyTo, To, Cc, OriginalLS and ReplyIn are filled for a liaison
	// statement.
	ReplyTo    string `json:"reply_to,omitempty"`
	To         string `json:"to,omitempty"`
	Cc         string `json:"cc,omitempty"`
	OriginalLS string `json:"original_ls,omitempty"`
	ReplyIn    string `json:"reply_in,omitempty"`
}

// tdocListSheet is the worksheet holding the list; the workbook also carries
// a CR pack sheet and a hidden parameter sheet.
const tdocListSheet = "TDoc_List"

// columns maps the list's header cells to Entry fields. Matching is by
// header text, not position: the column set has grown over the years
// (a 2015 list has 30 columns, a 2025 one 36).
var columns = map[string]func(*Entry, string){
	"tdoc":                    func(e *Entry, v string) { e.TDoc = v },
	"title":                   func(e *Entry, v string) { e.Title = v },
	"source":                  func(e *Entry, v string) { e.Source = v },
	"type":                    func(e *Entry, v string) { e.Type = v },
	"for":                     func(e *Entry, v string) { e.For = v },
	"abstract":                func(e *Entry, v string) { e.Abstract = v },
	"agenda item":             func(e *Entry, v string) { e.AgendaItem = v },
	"agenda item description": func(e *Entry, v string) { e.AgendaDescription = v },
	"tdoc status":             func(e *Entry, v string) { e.Status = v },
	"is revision of":          func(e *Entry, v string) { e.IsRevisionOf = v },
	"revised to":              func(e *Entry, v string) { e.RevisedTo = v },
	"release":                 func(e *Entry, v string) { e.Release = v },
	"spec":                    func(e *Entry, v string) { e.Spec = v },
	"version":                 func(e *Entry, v string) { e.Version = v },
	"related wis":             func(e *Entry, v string) { e.RelatedWIs = v },
	"cr":                      func(e *Entry, v string) { e.CR = v },
	"cr revision":             func(e *Entry, v string) { e.CRRevision = v },
	"cr category":             func(e *Entry, v string) { e.CRCategory = v },
	"clauses affected":        func(e *Entry, v string) { e.ClausesAffected = v },
	"reply to":                func(e *Entry, v string) { e.ReplyTo = v },
	"to":                      func(e *Entry, v string) { e.To = v },
	"cc":                      func(e *Entry, v string) { e.Cc = v },
	"original ls":             func(e *Entry, v string) { e.OriginalLS = v },
	"reply in":                func(e *Entry, v string) { e.ReplyIn = v },
}

// ParseTDocList reads a meeting's TDoc list workbook. Rows without a TDoc
// number are skipped; the sheet's row order, which is TDoc number order, is
// kept.
func ParseTDocList(data []byte) ([]Entry, error) {
	rows, err := readSheet(data, tdocListSheet)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("TDoc list sheet is empty")
	}
	setters := make([]func(*Entry, string), len(rows[0]))
	known := 0
	for i, h := range rows[0] {
		if set, ok := columns[strings.ToLower(strings.TrimSpace(h))]; ok {
			setters[i] = set
			known++
		}
	}
	if known < 3 {
		return nil, fmt.Errorf("TDoc list sheet has no recognizable header row")
	}
	var entries []Entry
	for _, row := range rows[1:] {
		var e Entry
		for i, v := range row {
			if i < len(setters) && setters[i] != nil {
				setters[i](&e, strings.TrimSpace(v))
			}
		}
		if e.TDoc == "" {
			continue
		}
		entries = append(entries, e)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("TDoc list has no rows")
	}
	return entries, nil
}

// TDocListPath is where a meeting's TDoc list is expected, relative to
// BaseURL: the file is named after the meeting title with its "3GPP" prefix
// dropped ("TDoc_List_Meeting_RAN1#123.xlsx" in the Docs folder). Empty when
// the meeting has no Docs folder yet.
func TDocListPath(m Meeting) string {
	if m.DocsDir == "" {
		return ""
	}
	return m.DocsDir + "/TDoc_List_Meeting_" + strings.TrimPrefix(m.Title, "3GPP") + ".xlsx"
}

var xlsxHrefRE = regexp.MustCompile(`(?i)href="([^"]*tdoc_list[^"]*\.xlsx)"`)

// ErrNoTDocList reports that a meeting publishes no TDoc list: its Docs
// folder is missing or holds no list workbook.
var ErrNoTDocList = errors.New("meeting has no TDoc list")

// FetchTDocList downloads and parses a meeting's TDoc list. The list is
// tried at TDocListPath first; when that is not there — the file is named
// after the title only most of the time — the Docs folder is listed and its
// list workbook taken. The path the list was read from is returned with it.
func FetchTDocList(ctx context.Context, client *http.Client, m Meeting) ([]Entry, string, error) {
	if client == nil {
		client = &http.Client{}
	}
	p := TDocListPath(m)
	if p == "" {
		return nil, "", fmt.Errorf("%w: %s has no Docs folder", ErrNoTDocList, m.Code)
	}
	log.Printf("Fetching TDoc list of %s ...", m.Title)
	data, err := pipeline.DownloadZip(ctx, client, Document{Path: p}.URL())
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", err
		}
		alt, lerr := findTDocList(ctx, client, m)
		if errors.Is(lerr, ErrNoTDocList) {
			return nil, "", fmt.Errorf("%w (not at %s either: %v)", lerr, p, err)
		}
		if lerr != nil {
			return nil, "", fmt.Errorf("download %s: %w (and %v)", p, err, lerr)
		}
		p = alt
		if data, err = pipeline.DownloadZip(ctx, client, Document{Path: p}.URL()); err != nil {
			return nil, "", fmt.Errorf("download %s: %w", p, err)
		}
	}
	entries, err := ParseTDocList(data)
	if err != nil {
		return nil, "", fmt.Errorf("parse %s: %w", p, err)
	}
	return entries, p, nil
}

// findTDocList lists a meeting's Docs folder for its TDoc list workbook.
func findTDocList(ctx context.Context, client *http.Client, m Meeting) (string, error) {
	listing := Document{Path: m.DocsDir}.URL() + "/"
	page, err := pipeline.FetchPage(ctx, client, listing)
	if err != nil {
		return "", fmt.Errorf("list %s: %w", listing, err)
	}
	for _, match := range xlsxHrefRE.FindAllStringSubmatch(page, -1) {
		// documentPath takes the href whole: a list name carries "#", which
		// url.Parse would otherwise split off as a fragment.
		if p, ok := documentPath(html.UnescapeString(match[1])); ok {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: no list workbook in %s", ErrNoTDocList, listing)
}

var meetingCodeRE = regexp.MustCompile(`^(?i)([A-Z][A-Z0-9])-\d`)

// GroupForMeeting infers the group from a meeting name: the DynaReport code
// ("R1-123"), the "#"-form ("RAN1#123", "3GPPRAN1#123"), or a folder path
// under a group's directory ("tsg_ran/WG1_RL1/TSGR1_123"). A bare folder
// name ("TSGR1_123") or number names no group.
func GroupForMeeting(request string) (Group, bool) {
	r := strings.TrimSpace(request)
	if i := strings.Index(r, "#"); i > 0 {
		return GroupByCode(strings.TrimPrefix(strings.ToUpper(r[:i]), "3GPP"))
	}
	if m := meetingCodeRE.FindStringSubmatch(r); m != nil {
		return GroupByCode(m[1])
	}
	if p := strings.ReplaceAll(r, `\`, "/"); strings.Contains(p, "/") {
		p = strings.TrimPrefix(strings.ToLower(strings.Trim(p, "/")), "ftp/")
		for _, g := range Groups() {
			if strings.HasPrefix(p, strings.ToLower(g.Dir)+"/") {
				return g, true
			}
		}
	}
	return Group{}, false
}

// ResolveMeeting finds the meeting a request names. group optionally names
// the group ("R1", "RAN1") and is required when the request does not imply
// one (see GroupForMeeting).
func ResolveMeeting(ctx context.Context, client *http.Client, request, group string, useCache bool) (Group, Meeting, error) {
	request = strings.TrimSpace(request)
	if request == "" {
		return Group{}, Meeting{}, fmt.Errorf("%w: meeting is required", ErrNotFound)
	}
	var g Group
	var ok bool
	if group != "" {
		if g, ok = GroupByCode(group); !ok {
			return Group{}, Meeting{}, fmt.Errorf("%w: unknown group %q", ErrNotFound, group)
		}
	} else if g, ok = GroupForMeeting(request); !ok {
		return Group{}, Meeting{}, fmt.Errorf("%w: %q names no group; use a meeting code (R1-123), the #-form (RAN1#123), or name the group", ErrNotFound, request)
	}
	meetings, err := FetchMeetings(ctx, client, g, useCache)
	if err != nil {
		return Group{}, Meeting{}, err
	}
	m, ok := FindMeeting(meetings, request)
	if !ok {
		return Group{}, Meeting{}, fmt.Errorf("%w: %s lists no meeting %q", ErrNotFound, g.Name, request)
	}
	return g, m, nil
}
