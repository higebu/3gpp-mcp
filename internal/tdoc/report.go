package tdoc

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/converter/pipeline"
)

// Where a meeting's report lives differs by group. A working group's
// secretary uploads the final minutes to the meeting's Report/ folder
// ("Final_Minutes_report_RAN1#123_v100.zip", "S2-172_Draft_Report_v003.zip",
// or just a TDoc number); a plenary's Report/ folder holds only drafts under
// History/. The approved report is then submitted to the following meeting
// as a TDoc of type "report" ("Report of RAN1#123 meeting", "Draft report
// of meeting RAN #110"). Callers try the following meeting's list first and
// fall back to the folder.

// MeetingNumber is the number part of a meeting title: "123" for
// "3GPPRAN1#123", "122-bis" for "3GPPRAN1#122-bis". Empty when the title
// carries none.
func MeetingNumber(m Meeting) string {
	if i := strings.LastIndex(m.Title, "#"); i >= 0 {
		return m.Title[i+1:]
	}
	return ""
}

// titleNumberRE finds the meeting numbers a title carries: the digits
// after "#" with any suffix ("122bis", "122-bis", "100-bis-e").
var titleNumberRE = regexp.MustCompile(`(?i)#\s*(\d+(?:[-\s]?(?:bis|ter|e|ah|adhoc|electronic|li))*)\b`)

// ReportTitleMatches reports whether a document title names meeting m as
// the meeting reported on: it carries the group's name and the meeting
// number, in any of the spellings the secretaries use ("RAN1#123",
// "RAN #110", "SA WG2 meeting #172", "RAN1#122bis"). The number must
// match whole: the report of RAN1#122-bis is not the report of RAN1#122.
func ReportTitleMatches(title string, g Group, m Meeting) bool {
	num := MeetingNumber(m)
	if num == "" {
		return false
	}
	numbered := false
	for _, match := range titleNumberRE.FindAllStringSubmatch(title, -1) {
		if squash(match[1]) == squash(num) {
			numbered = true
			break
		}
	}
	if !numbered {
		return false
	}
	t := squash(title)
	// The group name, with "WG" and spaces removed: "sawg2" is "sa2".
	name := squash(g.Name)
	if strings.Contains(t, name) {
		return true
	}
	// "SA WG2" -> "sawg2" appears as "sa2" only after the "wg" is dropped.
	return strings.Contains(strings.ReplaceAll(t, "wg", ""), name)
}

// squash lower-cases and drops spaces and hyphens, so "122-bis", "122bis"
// and "RAN #110" compare equal to their other spellings.
func squash(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, " ", "")
	s = strings.ReplaceAll(s, "-", "")
	s = strings.ReplaceAll(s, "_", "")
	return s
}

var (
	reportHrefRE = regexp.MustCompile(`(?i)href="([^"]+\.(?:zip|docx?))"`)
	versionRE    = regexp.MustCompile(`(?i)[_-]v(\d+)`)
)

// FindReportFile lists a meeting's Report/ folder and returns the path of
// the file most likely to be the report, relative to BaseURL: the final
// minutes over a draft, the highest version of a name, and never the
// participant list. ErrNotFound when the folder holds no document.
func FindReportFile(ctx context.Context, client *http.Client, m Meeting) (string, error) {
	if m.Dir == "" {
		return "", fmt.Errorf("%w: %s has no meeting folder", ErrNotFound, m.Code)
	}
	if client == nil {
		client = &http.Client{}
	}
	listing := Document{Path: m.Dir + "/Report"}.URL() + "/"
	page, err := pipeline.FetchPage(ctx, client, listing)
	if err != nil {
		return "", fmt.Errorf("list %s: %w", listing, err)
	}
	type candidate struct {
		path  string
		score int
	}
	var cands []candidate
	prefix := strings.ToLower(m.Dir) + "/report/"
	for _, match := range reportHrefRE.FindAllStringSubmatch(page, -1) {
		// The href goes to documentPath whole, as in findTDocList: a file
		// name carries "#", which url.Parse would read as a fragment.
		p, ok := documentPath(html.UnescapeString(match[1]))
		if !ok || !strings.HasPrefix(strings.ToLower(p), prefix) {
			continue
		}
		// Only files directly in the folder: History/ and Archive/ hold
		// the drafts.
		rel := p[len(prefix):]
		if strings.Contains(rel, "/") {
			continue
		}
		cands = append(cands, candidate{path: p, score: reportScore(rel)})
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("%w: no report in %s", ErrNotFound, listing)
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })
	if cands[0].score < 0 {
		return "", fmt.Errorf("%w: no report in %s", ErrNotFound, listing)
	}
	return cands[0].path, nil
}

// reportScore ranks a document file name in a Report/ folder (reportHrefRE
// has already kept only zip and Word files). A participant list is not a
// report; "final" beats "draft"; a higher version wins among the rest.
func reportScore(name string) int {
	lower := strings.ToLower(name)
	if strings.Contains(lower, "partlist") || strings.Contains(lower, "participant") {
		return -1
	}
	score := 0
	if strings.Contains(lower, "final") {
		score += 1000
	}
	if strings.Contains(lower, "report") || strings.Contains(lower, "minutes") {
		score += 100
	}
	if strings.Contains(lower, "draft") {
		score -= 50
	}
	if m := versionRE.FindStringSubmatch(lower); m != nil {
		if v, err := strconv.Atoi(m[1]); err == nil && v < 1000 {
			score += v
		}
	}
	return score
}
