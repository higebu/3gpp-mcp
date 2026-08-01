package structdiff

import (
	"testing"

	"github.com/higebu/3gpp-mcp/db"
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
