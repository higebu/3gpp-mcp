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

func TestSectionsToMarkdown_Empty(t *testing.T) {
	if got := SectionsToMarkdown(nil); got != "" {
		t.Errorf("expected empty string for nil sections, got: %q", got)
	}
	if got := SectionsToMarkdown([]*Section{}); got != "" {
		t.Errorf("expected empty string for empty slice, got: %q", got)
	}
}
