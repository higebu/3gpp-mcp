package docx

import (
	"strings"
	"testing"
)

// TestSectionsToMarkdown covers the slice-level helper; the single-section
// counterpart is tested in parser_test.go.
func TestSectionsToMarkdown(t *testing.T) {
	sections := []*Section{
		{Number: "1", Title: "Scope", Level: 1, Content: []string{"Scope body."}},
		{Number: "5", Title: "Architecture", Level: 1, Content: []string{"Arch body."}},
		{Number: "5.1", Title: "General", Level: 2, Content: []string{"General body."}},
	}
	got := SectionsToMarkdown(sections)
	for _, want := range []string{
		"# 1 Scope",
		"Scope body.",
		"# 5 Architecture",
		"## 5.1 General",
		"General body.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, got)
		}
	}
}

// CommonMark stops recognizing ATX headings at six "#", so deeper levels —
// "Heading 7" to "Heading 9" styles, or deep annex numbering — are clamped
// instead of emitted as plain text (issue #139).
func TestSectionToMarkdown_ClampsDeepHeadings(t *testing.T) {
	tests := []struct {
		level      int
		wantPrefix string
	}{
		{1, "# "},
		{6, "###### "},
		{7, "###### "},
		{9, "###### "},
		{0, "# "},
	}
	for _, tt := range tests {
		section := &Section{Number: "A.1.2.3.4.5.6.7", Title: "Deep clause", Level: tt.level}
		got := SectionToMarkdown(section)
		want := tt.wantPrefix + "A.1.2.3.4.5.6.7 Deep clause"
		if got != want {
			t.Errorf("level %d: SectionToMarkdown = %q, want %q", tt.level, got, want)
		}
		if strings.HasPrefix(got, strings.Repeat("#", maxATXLevel+1)) {
			t.Errorf("level %d: emitted more than %d hashes: %q", tt.level, maxATXLevel, got)
		}
	}
}

func TestSectionsToMarkdown_Empty(t *testing.T) {
	if got := SectionsToMarkdown(nil); got != "" {
		t.Errorf("expected empty string for nil sections, got: %q", got)
	}
	if got := SectionsToMarkdown([]*Section{}); got != "" {
		t.Errorf("expected empty string for empty slice, got: %q", got)
	}
}
