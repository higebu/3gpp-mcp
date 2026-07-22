package docx

import (
	"strings"
)

// SectionToMarkdown converts a single section's content to a markdown string.
func SectionToMarkdown(section *Section) string {
	headingPrefix := strings.Repeat("#", section.Level)
	heading := section.Title
	if section.Number != "" && section.Number != section.Title {
		heading = section.Number + " " + section.Title
	}
	lines := []string{headingPrefix + " " + heading}
	lines = append(lines, section.Content...)
	return strings.Join(lines, "\n\n")
}

// SectionsToMarkdown converts all sections to a single markdown document.
func SectionsToMarkdown(sections []*Section) string {
	parts := make([]string, len(sections))
	for i, s := range sections {
		parts[i] = SectionToMarkdown(s)
	}
	return strings.Join(parts, "\n\n")
}
