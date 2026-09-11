package tdoc

import (
	"html"
	"regexp"
	"strings"
)

// A change request opens with the 3GPP CR cover sheet (CR-Form-v11 to v12:
// two tables — the spec, CR number, revision and current version, then the
// labelled fields), and a liaison statement with a block of "Label:\tvalue"
// lines. Both survive conversion in the preamble section, the CR form as
// HTML tables and the LS header as bold paragraphs. The parsers below turn
// them into structured metadata; the text is not changed.

// CRCover is the cover sheet of a change request.
type CRCover struct {
	Spec           string `json:"spec"`            // "38.213"
	CR             string `json:"cr"`              // "0748"
	Revision       string `json:"revision"`        // "-" for the first version
	CurrentVersion string `json:"current_version"` // "19.1.0"
	Title          string `json:"title"`
	SourceWG       string `json:"source_wg"`
	SourceTSG      string `json:"source_tsg"`
	WorkItem       string `json:"work_item"`
	Date           string `json:"date"`
	Category       string `json:"category"` // "F", "A", "B", "C", "D"
	Release        string `json:"release"`
	Reason         string `json:"reason"`
	Summary        string `json:"summary"`
	Consequences   string `json:"consequences"`
	Clauses        string `json:"clauses"` // "13", "7.9.4.2, 7.9.5.2"
	OtherComments  string `json:"other_comments"`
	History        string `json:"history"`
}

// SpecID is the specification as the database names it, without the
// TS/TR distinction the cover sheet does not make: "38.213".
func (c *CRCover) SpecID() string { return c.Spec }

// ClauseList splits the "Clauses affected" field into clause numbers.
// The field is free text ("7.9.4.2, 7.9.5.2", "13", "4.2.3 and 5.1,
// Annex B (new)"); only tokens shaped like a clause number are kept.
func (c *CRCover) ClauseList() []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range clauseRE.FindAllString(c.Clauses, -1) {
		m = strings.TrimRight(m, ".")
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

var clauseRE = regexp.MustCompile(`\b\d+(?:\.\d+)*[a-zA-Z]?\b`)

// LSHeader is the header of a liaison statement.
type LSHeader struct {
	Title       string `json:"title"`
	ResponseTo  string `json:"response_to"`
	Release     string `json:"release"`
	WorkItem    string `json:"work_item"`
	Source      string `json:"source"`
	To          string `json:"to"`
	Cc          string `json:"cc"`
	Contact     string `json:"contact"`
	Attachments string `json:"attachments"`
}

var (
	tableRE = regexp.MustCompile(`(?s)<table>.*?</table>`)
	trRE    = regexp.MustCompile(`(?s)<tr[^>]*>(.*?)</tr>`)
	tdRE    = regexp.MustCompile(`(?s)<t[dh][^>]*>(.*?)</t[dh]>`)
	tagsRE  = regexp.MustCompile(`<[^>]+>`)
	spaceRE = regexp.MustCompile(`[ \t\x{00a0}]+`)
)

// crFields maps a cover-sheet label, lower-cased and without its colon, to
// the field it fills.
var crFields = map[string]func(*CRCover, string){
	"title":                        func(c *CRCover, v string) { c.Title = v },
	"source to wg":                 func(c *CRCover, v string) { c.SourceWG = v },
	"source to tsg":                func(c *CRCover, v string) { c.SourceTSG = v },
	"source":                       func(c *CRCover, v string) { c.SourceWG = v },
	"work item code":               func(c *CRCover, v string) { c.WorkItem = v },
	"date":                         func(c *CRCover, v string) { c.Date = v },
	"category":                     func(c *CRCover, v string) { c.Category = v },
	"release":                      func(c *CRCover, v string) { c.Release = v },
	"reason for change":            func(c *CRCover, v string) { c.Reason = v },
	"summary of change":            func(c *CRCover, v string) { c.Summary = v },
	"consequences if not approved": func(c *CRCover, v string) { c.Consequences = v },
	"clauses affected":             func(c *CRCover, v string) { c.Clauses = v },
	"other comments":               func(c *CRCover, v string) { c.OtherComments = v },
	"this cr's revision history":   func(c *CRCover, v string) { c.History = v },
}

// ParseCRCover reads the CR cover sheet out of a preamble section. It
// returns nil when the section holds no "CHANGE REQUEST" form.
func ParseCRCover(preamble string) *CRCover {
	var c CRCover
	found := false
	for _, table := range tableRE.FindAllString(preamble, -1) {
		for _, row := range trRE.FindAllStringSubmatch(table, -1) {
			cells := cellTexts(row[1])
			if len(cells) == 0 {
				continue
			}
			// The identity row: "", "38.213", "CR", "0748", "rev", "-",
			// "Current version:", "19.1.0".
			for i := 0; i+1 < len(cells); i++ {
				switch strings.ToLower(strings.TrimSuffix(cells[i], ":")) {
				case "cr":
					if i > 0 && c.Spec == "" && specNumberRE.MatchString(cells[i-1]) {
						c.Spec, c.CR = cells[i-1], cells[i+1]
						found = true
					}
				case "rev":
					if c.Spec != "" && c.Revision == "" {
						c.Revision = cells[i+1]
					}
				case "current version":
					if c.Spec != "" && c.CurrentVersion == "" {
						c.CurrentVersion = cells[i+1]
					}
				}
			}
			// A labelled row: the label cell, then the value cell. Some
			// rows carry two pairs ("Work item code: ... Date: ...").
			for i := 0; i+1 < len(cells); i++ {
				label := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(cells[i], ":")))
				set, ok := crFields[label]
				if !ok || !strings.HasSuffix(strings.TrimSpace(cells[i]), ":") {
					continue
				}
				value := cells[i+1]
				// The value may sit in a later cell when an empty
				// spacer cell follows the label — but never in the next
				// label's cell: an empty field stays empty.
				for j := i + 2; j < len(cells) && value == ""; j++ {
					if strings.HasSuffix(strings.TrimSpace(cells[j]), ":") {
						break
					}
					value = cells[j]
				}
				if value != "" {
					set(&c, value)
					found = true
				}
			}
		}
	}
	if !found || c.Spec == "" {
		return nil
	}
	return &c
}

var specNumberRE = regexp.MustCompile(`^\d{2}\.\d{3}(?:-\d+)?$`)

// cellTexts strips a table row to the text of its cells.
func cellTexts(row string) []string {
	var cells []string
	for _, m := range tdRE.FindAllStringSubmatch(row, -1) {
		cells = append(cells, cleanText(m[1]))
	}
	return cells
}

// cleanText drops tags and Markdown emphasis, decodes entities and
// collapses whitespace.
func cleanText(s string) string {
	// Paragraph and line breaks inside a cell stay line breaks.
	s = strings.ReplaceAll(s, "</p>", "\n")
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = strings.ReplaceAll(s, "<br/>", "\n")
	s = tagsRE.ReplaceAllString(s, " ")
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "*", "")
	s = spaceRE.ReplaceAllString(s, " ")
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSpace(l)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

// lsFields maps an LS header label, lower-cased, to the field it fills.
var lsFields = map[string]func(*LSHeader, string){
	"title":          func(h *LSHeader, v string) { h.Title = v },
	"response to":    func(h *LSHeader, v string) { h.ResponseTo = v },
	"reply to":       func(h *LSHeader, v string) { h.ResponseTo = v },
	"release":        func(h *LSHeader, v string) { h.Release = v },
	"work item":      func(h *LSHeader, v string) { h.WorkItem = v },
	"work items":     func(h *LSHeader, v string) { h.WorkItem = v },
	"source":         func(h *LSHeader, v string) { h.Source = v },
	"to":             func(h *LSHeader, v string) { h.To = v },
	"cc":             func(h *LSHeader, v string) { h.Cc = v },
	"contact person": func(h *LSHeader, v string) { h.Contact = v },
	"attachments":    func(h *LSHeader, v string) { h.Attachments = v },
	"attachment":     func(h *LSHeader, v string) { h.Attachments = v },
}

var lsLineRE = regexp.MustCompile(`^\*{0,3}([A-Za-z][A-Za-z ]{1,20}?):\*{0,3}[ \t\x{00a0}]*(.*?)\*{0,3}$`)

// ParseLSHeader reads the header of a liaison statement out of a preamble
// section: the "Label:\tvalue" lines up to the first numbered clause. It
// returns nil when the section is not addressed to a group — a report or an
// agenda also opens with "Source:" and "Title:" lines — or looks like a CR
// cover sheet instead.
func ParseLSHeader(preamble string) *LSHeader {
	if strings.Contains(preamble, "CHANGE REQUEST") {
		return nil
	}
	var h LSHeader
	found := 0
	lines := strings.Split(preamble, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		m := lsLineRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		set, ok := lsFields[strings.ToLower(strings.TrimSpace(m[1]))]
		if !ok {
			continue
		}
		value := cleanText(m[2])
		if strings.EqualFold(m[1], "contact person") && value == "" {
			// The name and e-mail follow on their own lines.
			value = contactLines(lines[i+1:])
		}
		set(&h, value)
		found++
	}
	// A meeting report or an agenda also opens with "Source:" and
	// "Title:" lines; only a liaison statement is addressed to someone.
	if found < 3 || h.To == "" {
		return nil
	}
	return &h
}

// contactLines gathers the "Name:" and "E-mail:" lines that follow a
// "Contact Person:" label into one line.
func contactLines(lines []string) string {
	var parts []string
	for _, line := range lines {
		line = cleanText(line)
		if line == "" {
			if len(parts) > 0 && len(parts) >= 2 {
				break
			}
			continue
		}
		lower := strings.ToLower(line)
		switch {
		case strings.HasPrefix(lower, "name:"), strings.HasPrefix(lower, "e-mail:"), strings.HasPrefix(lower, "email:"), strings.HasPrefix(lower, "tel:"):
			if v := strings.TrimSpace(line[strings.Index(line, ":")+1:]); v != "" {
				parts = append(parts, v)
			}
		default:
			return strings.Join(parts, ", ")
		}
	}
	return strings.Join(parts, ", ")
}
