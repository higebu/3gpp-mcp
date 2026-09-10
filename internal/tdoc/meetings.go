package tdoc

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"regexp"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/converter/pipeline"
)

// Meeting is one row of a group's DynaReport meeting page.
type Meeting struct {
	Code  string // "R1-123"
	Title string // "3GPPRAN1#123"
	Town  string
	Start string // "2025-11-17"
	End   string
	// Dir is the meeting folder relative to /ftp/, e.g.
	// "tsg_ran/WG1_RL1/TSGR1_123". Empty when the page links no folder
	// (future meetings, some legacy ones).
	Dir string
	// DocsDir is the folder holding the TDoc zips, relative to /ftp/. The
	// page links it next to the TDoc range; when it does not, Dir + "/Docs"
	// is assumed.
	DocsDir string
	// FirstTDoc and LastTDoc bound the TDoc numbers allocated to the
	// meeting. Empty when the meeting has no documents yet.
	FirstTDoc ID
	LastTDoc  ID
}

// Folder is the last path element of Dir, the name the meeting folder has
// on the FTP site ("TSGR1_123").
func (m Meeting) Folder() string {
	if i := strings.LastIndex(m.Dir, "/"); i >= 0 {
		return m.Dir[i+1:]
	}
	return m.Dir
}

// Holds reports whether a TDoc number falls in the meeting's range.
func (m Meeting) Holds(id ID) bool {
	if m.FirstTDoc.Prefix == "" || id.Prefix != m.FirstTDoc.Prefix {
		return false
	}
	return id.Number >= m.FirstTDoc.Number && id.Number <= m.LastTDoc.Number
}

// dynaReportURL is the DynaReport meeting page of a group. The page is a
// server-rendered table of every meeting the group ever held, newest first.
func dynaReportURL(g Group) string {
	return "https://www.3gpp.org/dynareport?code=Meetings-" + g.Code + ".htm"
}

// FetchMeetings returns a group's meetings, newest first, from the group's
// DynaReport page. With useCache the parsed rows are kept on disk for the
// listing cache TTL, like the archive listings.
func FetchMeetings(ctx context.Context, client *http.Client, g Group, useCache bool) ([]Meeting, error) {
	if client == nil {
		client = &http.Client{}
	}
	key := pipeline.CacheKey("meetings", g.Code)
	if useCache {
		if lines, _ := pipeline.LoadCache(key, pipeline.CacheTTL()); lines != nil {
			if ms, err := decodeMeetings(lines); err == nil {
				return ms, nil
			}
		}
	}

	url := dynaReportURL(g)
	log.Printf("Fetching meeting list for %s ...", g.Name)
	page, err := pipeline.FetchPage(ctx, client, url)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", url, err)
	}
	meetings := ParseMeetings(page)
	if len(meetings) == 0 {
		return nil, fmt.Errorf("%s lists no meetings; the page format may have changed", url)
	}
	if useCache {
		if err := pipeline.SaveCache(key, encodeMeetings(meetings)); err != nil {
			log.Printf("warning: failed to save meeting cache: %v", err)
		}
	}
	return meetings, nil
}

var (
	rowRE  = regexp.MustCompile(`(?s)<tr[^>]*>.*?</tr>`)
	cellRE = regexp.MustCompile(`(?s)<td[^>]*>(.*?)</td>`)
	tagRE  = regexp.MustCompile(`<[^>]+>`)
	hrefRE = regexp.MustCompile(`href="([^"]+)"`)
	// rangeRE matches the "First & Last tdoc" cell, "R1-2508300 - R1-2509718".
	rangeRE = regexp.MustCompile(`([A-Z][A-Z0-9]-\d{5,8})\s*-\s*([A-Z][A-Z0-9]-\d{5,8})`)
	dateRE  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

// ParseMeetings extracts the meeting rows from a DynaReport meeting page.
// The columns, in order: meeting code, title, town (linking the Invitation
// folder), start date (linking Agenda), end date (linking Report), TDoc range
// (linking the Docs folder), then registration and calendar links, one of
// which ("Files") links the meeting folder itself. Rows without a code are
// skipped; a meeting whose folder is not linked yet is kept with an empty
// Dir, so callers can still name it.
func ParseMeetings(page string) []Meeting {
	var meetings []Meeting
	for _, row := range rowRE.FindAllString(page, -1) {
		cells := cellRE.FindAllStringSubmatch(row, -1)
		if len(cells) < 6 {
			continue
		}
		code := cellText(cells[0][1])
		if code == "" || strings.Contains(code, " ") {
			continue
		}
		m := Meeting{
			Code:  code,
			Title: cellText(cells[1][1]),
			Town:  cellText(cells[2][1]),
		}
		if d := cellText(cells[3][1]); dateRE.MatchString(d) {
			m.Start = d
		}
		if d := cellText(cells[4][1]); dateRE.MatchString(d) {
			m.End = d
		}
		if m.Start == "" {
			// Every real row carries a start date; a header row or a
			// legend does not.
			continue
		}
		if r := rangeRE.FindStringSubmatch(cellText(cells[5][1])); r != nil {
			first, err1 := ParseID(r[1])
			last, err2 := ParseID(r[2])
			if err1 == nil && err2 == nil && first.Prefix == last.Prefix {
				m.FirstTDoc, m.LastTDoc = first, last
			}
		}
		m.Dir, m.DocsDir = meetingDirs(row, cells)
		meetings = append(meetings, m)
	}
	return meetings
}

// meetingDirs derives the meeting folder and its Docs folder from the row's
// FTP links. The "Files" link names the folder itself; failing that, the
// Invitation/Agenda/Report links each sit one level below it. The Docs link
// is the one in the TDoc range cell.
func meetingDirs(row string, cells [][]string) (dir, docsDir string) {
	for _, m := range hrefRE.FindAllStringSubmatch(cells[5][1], -1) {
		if p := ftpPath(m[1]); p != "" {
			docsDir = p
			break
		}
	}
	for _, m := range hrefRE.FindAllStringSubmatch(row, -1) {
		p := ftpPath(m[1])
		if p == "" {
			continue
		}
		lower := strings.ToLower(p)
		switch {
		case strings.HasSuffix(lower, "/invitation"), strings.HasSuffix(lower, "/agenda"), strings.HasSuffix(lower, "/report"):
			if dir == "" {
				dir = p[:strings.LastIndex(p, "/")]
			}
		case p == docsDir:
		default:
			// A bare meeting folder: the "Files" link. It is authoritative.
			dir = p
		}
	}
	if docsDir == "" && dir != "" {
		docsDir = dir + "/Docs"
	}
	return dir, docsDir
}

// ftpPath normalizes a DynaReport link into a path relative to /ftp/. The
// page writes its FTP links with backslashes and a relative prefix
// ("/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_123\\docs\"), and sometimes doubles
// a separator. Anything not under /ftp/ yields "".
func ftpPath(href string) string {
	p := strings.ReplaceAll(html.UnescapeString(href), `\`, "/")
	for strings.Contains(p, "//") {
		p = strings.ReplaceAll(p, "//", "/")
	}
	i := strings.Index(strings.ToLower(p), "/ftp/")
	if i < 0 {
		return ""
	}
	p = strings.Trim(p[i+len("/ftp/"):], "/")
	if p == "" || strings.Contains(p, "..") {
		return ""
	}
	return p
}

// cellText strips a cell to its text: tags removed, entities decoded, the
// page's non-breaking hyphens (U+2011) turned into plain ones so dates and
// TDoc ranges parse.
func cellText(cell string) string {
	t := html.UnescapeString(tagRE.ReplaceAllString(cell, ""))
	t = strings.ReplaceAll(t, "‑", "-")
	t = strings.ReplaceAll(t, " ", " ")
	return strings.TrimSpace(t)
}

// The disk cache holds one meeting per line as JSON. The line-oriented
// cache trims whitespace off every line, so a delimiter-separated encoding
// would lose the trailing empty fields of a meeting without a folder.
func encodeMeetings(ms []Meeting) []string {
	lines := make([]string, 0, len(ms))
	for _, m := range ms {
		b, err := json.Marshal(m)
		if err != nil {
			continue
		}
		lines = append(lines, string(b))
	}
	return lines
}

func decodeMeetings(lines []string) ([]Meeting, error) {
	ms := make([]Meeting, 0, len(lines))
	for _, line := range lines {
		var m Meeting
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			return nil, fmt.Errorf("malformed meeting cache line %q: %w", line, err)
		}
		ms = append(ms, m)
	}
	if len(ms) == 0 {
		return nil, fmt.Errorf("empty meeting cache")
	}
	return ms, nil
}

// FindMeeting picks the meeting a request names. The request may be the
// DynaReport code ("R1-123"), the "#"-form ("RAN1#123", "#123"), or the FTP
// folder name ("TSGR1_123"), matched case-insensitively.
func FindMeeting(meetings []Meeting, request string) (Meeting, bool) {
	want := strings.ToLower(strings.TrimSpace(request))
	if want == "" {
		return Meeting{}, false
	}
	for _, m := range meetings {
		switch {
		case strings.ToLower(m.Code) == want,
			strings.ToLower(m.Folder()) == want,
			strings.ToLower(m.Dir) == want,
			// "RAN1#123", "#123" and the full "3GPPRAN1#123" all end the
			// title; "RAN2#123" does not end "3gppran1#123".
			strings.Contains(want, "#") && strings.HasSuffix(strings.ToLower(m.Title), want):
			return m, true
		}
	}
	// "123" or "122-bis" alone: the meeting number.
	if !strings.Contains(want, "#") {
		for _, m := range meetings {
			if strings.HasSuffix(strings.ToLower(m.Title), "#"+want) {
				return m, true
			}
		}
	}
	return Meeting{}, false
}
