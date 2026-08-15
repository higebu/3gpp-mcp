package structdiff

import (
	"testing"

	"github.com/higebu/3gpp-mcp/internal/db"
)

// TestDiffRenumberedWithContentChange checks that a section that was
// renumbered and edited shows up in both lists — the move must not hide the
// edit.
func TestDiffRenumberedWithContentChange(t *testing.T) {
	oldSecs := []db.Section{
		{Number: "7", Title: "Overview", Content: "# 7 Overview\nOld body."},
	}
	newSecs := []db.Section{
		{Number: "5.1.1", Title: "Overview", Content: "## 5.1.1 Overview\nNew body.\nWith an extra line."},
	}

	d := Diff(oldSecs, newSecs)
	if len(d.Renumbered) != 1 || d.Renumbered[0].OldNumber != "7" || d.Renumbered[0].NewNumber != "5.1.1" {
		t.Fatalf("Renumbered = %+v, want 7 → 5.1.1", d.Renumbered)
	}
	if len(d.ContentChanged) != 1 || d.ContentChanged[0].Number != "5.1.1" {
		t.Fatalf("ContentChanged = %+v, want the renumbered section listed under its new number", d.ContentChanged)
	}
	if d.ContentChanged[0].OldNumber != "7" {
		t.Errorf("ContentChanged OldNumber = %q, want the old number 7", d.ContentChanged[0].OldNumber)
	}
	if c := d.ContentChanged[0]; c.OldLines != 2 || c.NewLines != 3 {
		t.Errorf("ContentChanged line counts = %d → %d, want 2 → 3", c.OldLines, c.NewLines)
	}

	// A renumbered section with an unchanged body stays out of ContentChanged.
	sameBody := []db.Section{
		{Number: "5.1.1", Title: "Overview", Content: "## 5.1.1 Overview\nOld body."},
	}
	d = Diff(oldSecs, sameBody)
	if len(d.Renumbered) != 1 || len(d.ContentChanged) != 0 {
		t.Errorf("Renumbered/ContentChanged = %d/%d, want 1/0 for an unchanged body", len(d.Renumbered), len(d.ContentChanged))
	}
}

func TestDiffRenumberRequiresUniqueTitle(t *testing.T) {
	oldSecs := []db.Section{
		{Number: "6", Title: "Overview", Content: "# 6 Overview\nA."},
		{Number: "7", Title: "Overview", Content: "# 7 Overview\nB."},
	}
	newSecs := []db.Section{
		{Number: "8", Title: "Overview", Content: "# 8 Overview\nA."},
	}

	d := Diff(oldSecs, newSecs)
	if len(d.Renumbered) != 0 {
		t.Errorf("an ambiguous title must not be promoted to renumbered: %+v", d.Renumbered)
	}
	if len(d.Removed) != 2 || len(d.Added) != 1 {
		t.Errorf("Removed/Added = %d/%d, want 2/1", len(d.Removed), len(d.Added))
	}
}

// TestDiffClassifiesRetitleUnchangedAndContentChange covers the by-number
// classification: unchanged, retitled (heading stripped so it is not a content
// change), content changed, and unmatched added/removed sections whose titles
// differ, which must not be promoted to renumberings.
func TestDiffClassifiesRetitleUnchangedAndContentChange(t *testing.T) {
	oldSecs := []db.Section{
		{Number: "1", Title: "Scope", Content: "# 1 Scope\nSame."},
		{Number: "2", Title: "Old title", Content: "# 2 Old title\nSame body."},
		{Number: "3", Title: "Definitions", Content: "# 3 Definitions\nOld."},
		{Number: "4", Title: "Gone", Content: "no heading here"},
	}
	newSecs := []db.Section{
		{Number: "1", Title: "Scope", Content: "# 1 Scope\nSame."},
		{Number: "2", Title: "New title", Content: "# 2 New title\nSame body."},
		{Number: "3", Title: "Definitions", Content: "# 3 Definitions\nNew."},
		{Number: "5", Title: "Fresh", Content: "#"},
	}

	d := Diff(oldSecs, newSecs)
	if d.Unchanged != 1 {
		t.Errorf("Unchanged = %d, want 1", d.Unchanged)
	}
	if len(d.Retitled) != 1 || d.Retitled[0].OldTitle != "Old title" || d.Retitled[0].NewTitle != "New title" {
		t.Errorf("Retitled = %+v, want the section 2 retitle", d.Retitled)
	}
	if len(d.ContentChanged) != 1 || d.ContentChanged[0].Number != "3" {
		t.Errorf("ContentChanged = %+v, want section 3 alone (a pure retitle is not a content change)", d.ContentChanged)
	}
	if len(d.Removed) != 1 || d.Removed[0].Number != "4" {
		t.Errorf("Removed = %+v, want section 4", d.Removed)
	}
	if len(d.Added) != 1 || d.Added[0].Number != "5" {
		t.Errorf("Added = %+v, want section 5", d.Added)
	}
	if len(d.Renumbered) != 0 {
		t.Errorf("different titles must not pair as a renumbering: %+v", d.Renumbered)
	}
	if d.OldCount != 4 || d.NewCount != 4 {
		t.Errorf("counts = %d/%d, want 4/4", d.OldCount, d.NewCount)
	}
}

// TestSectionLines checks the join used for diffing: trailing blank lines are
// trimmed, sections are separated by one empty line, and empty input or empty
// sections contribute no phantom or trailing blank lines (#102) — those would
// surface as spurious added/removed lines in compare_versions diffs.
func TestSectionLines(t *testing.T) {
	tests := []struct {
		name string
		secs []db.Section
		want []string
	}{
		{
			name: "two sections joined by one blank line",
			secs: []db.Section{
				{Content: "# A\nline1\n\n"},
				{Content: "# B\nline2"},
			},
			want: []string{"# A", "line1", "", "# B", "line2"},
		},
		{
			name: "nil input yields nil, not a phantom blank line",
			secs: nil,
			want: nil,
		},
		{
			name: "a single empty section yields nil",
			secs: []db.Section{{Number: "1", Title: "A"}},
			want: nil,
		},
		{
			name: "a trailing empty section leaves no trailing blanks",
			secs: []db.Section{
				{Number: "1", Content: "x"},
				{Number: "2"},
			},
			want: []string{"x"},
		},
		{
			name: "a leading empty section adds no separator",
			secs: []db.Section{
				{Number: "1", Content: "\n"},
				{Number: "2", Content: "y"},
			},
			want: []string{"y"},
		},
		{
			name: "single non-empty section",
			secs: []db.Section{{Number: "1", Content: "x"}},
			want: []string{"x"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SectionLines(tt.secs)
			if len(got) != len(tt.want) {
				t.Fatalf("SectionLines = %q, want %q", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("SectionLines[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestDiffImageNotationIsNotAContentChange checks that sections whose bodies
// differ only in image reference spelling (converted PNG vs original EMF,
// filename alt vs "Figure") classify as unchanged, while a real edit next to
// such a difference still counts.
func TestDiffImageNotationIsNotAContentChange(t *testing.T) {
	oldSecs := []db.Section{
		{Number: "1", Title: "Arch", Content: "# 1 Arch\nIntro.\n![Figure](image://image3.emf?w=612&h=208)\nOutro."},
		{Number: "2", Title: "Cells", Content: "# 2 Cells\n<table><tr><td><img src=\"image://image1.emf?w=100&h=50\" alt=\"image1.emf\" width=\"100\" height=\"50\"></td></tr></table>"},
		{Number: "3", Title: "Edited", Content: "# 3 Edited\nOld sentence.\n![Figure](image://image5.emf?w=10&h=20)"},
	}
	newSecs := []db.Section{
		{Number: "1", Title: "Arch", Content: "# 1 Arch\nIntro.\n![Figure](image://image3.png?w=612&h=208)\nOutro."},
		{Number: "2", Title: "Cells", Content: "# 2 Cells\n<table><tr><td><img src=\"image://image1.png?w=100&h=50\" alt=\"Figure\" width=\"100\" height=\"50\"></td></tr></table>"},
		{Number: "3", Title: "Edited", Content: "# 3 Edited\nNew sentence.\n![Figure](image://image5.png?w=10&h=20)"},
	}

	d := Diff(oldSecs, newSecs)
	if d.Unchanged != 2 {
		t.Errorf("Unchanged = %d, want 2 (image notation only)", d.Unchanged)
	}
	if len(d.ContentChanged) != 1 || d.ContentChanged[0].Number != "3" {
		t.Errorf("ContentChanged = %+v, want section 3 alone", d.ContentChanged)
	}
}
