package docx

import (
	"strings"
)

// maxATXLevel is the deepest heading CommonMark accepts: seven or more "#"
// characters are plain text, not a heading. Styles "Heading 7" to "Heading 9"
// and deep annex numbering (dot count + 1) both produce deeper levels, so the
// emitted prefix is clamped while Section.Level keeps the real depth for the
// table of contents and nesting. No depth information is lost in the markdown
// either: the heading text still carries the clause number (issue #139).
const maxATXLevel = 6

// SectionToMarkdown converts a single section's content to a markdown string.
func SectionToMarkdown(section *Section) string {
	level := section.Level
	if level > maxATXLevel {
		level = maxATXLevel
	}
	if level < 1 {
		level = 1
	}
	headingPrefix := strings.Repeat("#", level)
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
