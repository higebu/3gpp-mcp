// Package structdiff classifies the structural differences between two
// versions of a specification: sections added, removed, renumbered, retitled
// and whose body changed. It is shared by the compare_versions MCP tool and
// the web viewer's compare page.
package structdiff

import (
	"strings"

	"github.com/higebu/3gpp-mcp/internal/db"
)

// Renumbering pairs a section that moved to a new number between versions.
type Renumbering struct {
	OldNumber, NewNumber, Title string
}

// Retitle records a section whose title changed while its number stayed.
type Retitle struct {
	Number, OldTitle, NewTitle string
}

// ContentChange records a section whose body text differs between versions.
// OldNumber names the section on the old side; it differs from Number only
// when the change rode along with a renumbering.
type ContentChange struct {
	Number, OldNumber, Title string
	OldLines, NewLines       int
}

// Result classifies every difference between two versions' section lists.
// Slices keep document order.
type Result struct {
	Added, Removed []db.Section
	Renumbered     []Renumbering
	Retitled       []Retitle
	ContentChanged []ContentChange
	Unchanged      int
	OldCount       int
	NewCount       int
}

// Diff matches two versions' sections by number and classifies every
// difference.
func Diff(oldSecs, newSecs []db.Section) Result {
	d := Result{OldCount: len(oldSecs), NewCount: len(newSecs)}

	oldByNum := make(map[string]*db.Section, len(oldSecs))
	for i := range oldSecs {
		oldByNum[oldSecs[i].Number] = &oldSecs[i]
	}
	newByNum := make(map[string]*db.Section, len(newSecs))
	for i := range newSecs {
		newByNum[newSecs[i].Number] = &newSecs[i]
	}

	for i := range newSecs {
		n := &newSecs[i]
		o, ok := oldByNum[n.Number]
		if !ok {
			d.Added = append(d.Added, *n)
			continue
		}
		// The leading markdown heading restates number and title, so it is
		// stripped before comparing: a pure retitle must not also count as a
		// content change.
		bodyChanged := bodyKey(o.Content) != bodyKey(n.Content)
		if o.Title != n.Title {
			d.Retitled = append(d.Retitled, Retitle{n.Number, o.Title, n.Title})
		}
		switch {
		case bodyChanged:
			d.ContentChanged = append(d.ContentChanged, ContentChange{
				Number:    n.Number,
				OldNumber: n.Number,
				Title:     n.Title,
				OldLines:  lineCount(o.Content),
				NewLines:  lineCount(n.Content),
			})
		case o.Title == n.Title:
			d.Unchanged++
		}
	}
	for i := range oldSecs {
		if _, ok := newByNum[oldSecs[i].Number]; !ok {
			d.Removed = append(d.Removed, oldSecs[i])
		}
	}

	d.promoteRenumbered()
	return d
}

// SectionLines joins section contents the way get_section renders them and
// splits into diffable lines, without trailing blank-line noise: sections with
// no content contribute nothing — not even a separator — and empty input
// yields nil rather than a phantom blank line, so an empty side diffs cleanly
// against a non-empty one.
func SectionLines(secs []db.Section) []string {
	var parts []string
	for _, s := range secs {
		if c := strings.TrimRight(s.Content, "\n"); c != "" {
			parts = append(parts, c)
		}
	}
	if len(parts) == 0 {
		return nil
	}
	return strings.Split(strings.Join(parts, "\n\n"), "\n")
}

// promoteRenumbered pairs removed and added sections whose title is unique on
// both sides, the cheap and safe answer to clause numbers shifting between
// releases. Similarity-based matching is deliberately out of scope.
func (d *Result) promoteRenumbered() {
	countTitles := func(secs []db.Section) map[string]int {
		counts := make(map[string]int, len(secs))
		for _, s := range secs {
			if t := normalizeTitle(s.Title); t != "" {
				counts[t]++
			}
		}
		return counts
	}
	removedCounts, addedCounts := countTitles(d.Removed), countTitles(d.Added)

	addedByTitle := make(map[string]db.Section, len(d.Added))
	for _, s := range d.Added {
		addedByTitle[normalizeTitle(s.Title)] = s
	}

	var removed []db.Section
	moved := make(map[string]bool)
	for _, o := range d.Removed {
		t := normalizeTitle(o.Title)
		if t != "" && removedCounts[t] == 1 && addedCounts[t] == 1 {
			n := addedByTitle[t]
			d.Renumbered = append(d.Renumbered, Renumbering{o.Number, n.Number, n.Title})
			moved[n.Number] = true
			// A renumbered section may have been edited too; that must not
			// hide behind the move.
			if bodyKey(o.Content) != bodyKey(n.Content) {
				d.ContentChanged = append(d.ContentChanged, ContentChange{
					Number:    n.Number,
					OldNumber: o.Number,
					Title:     n.Title,
					OldLines:  lineCount(o.Content),
					NewLines:  lineCount(n.Content),
				})
			}
			continue
		}
		removed = append(removed, o)
	}
	d.Removed = removed

	var added []db.Section
	for _, s := range d.Added {
		if !moved[s.Number] {
			added = append(added, s)
		}
	}
	d.Added = added
}

// bodyKey is the comparison key of a section body: the heading stripped, and
// image references normalized so the two conversion paths' spellings of the
// same figure do not count as a content change.
func bodyKey(content string) string {
	content = stripHeadingLine(content)
	if !strings.Contains(content, "image://") {
		return content
	}
	lines := strings.Split(content, "\n")
	for i, l := range lines {
		lines[i] = NormalizeImageRefs(l)
	}
	return strings.Join(lines, "\n")
}

// stripHeadingLine removes the leading markdown heading of a section body.
func stripHeadingLine(content string) string {
	if !strings.HasPrefix(content, "#") {
		return content
	}
	if i := strings.IndexByte(content, '\n'); i >= 0 {
		return content[i+1:]
	}
	return ""
}

func normalizeTitle(title string) string {
	return strings.ToLower(strings.TrimSpace(title))
}

func lineCount(content string) int {
	return strings.Count(content, "\n") + 1
}
