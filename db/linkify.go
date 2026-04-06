package db

import (
	"regexp"
	"sort"
	"strings"
)

// existingLinkRE matches Markdown link syntax [text](url) to avoid double-linking.
var existingLinkRE = regexp.MustCompile(`\[[^\]]*\]\([^)]*\)`)

// LinkifyRefs replaces spec/RFC/bracket references in Markdown content with Markdown links.
// bracketMap maps bracket numbers (e.g. "19") to spec IDs (e.g. "TS 33.203"); pass nil to skip.
// urlFor is called with (targetSpec, targetSection) and returns a URL string.
// References inside existing Markdown links are not replaced.
func LinkifyRefs(content string, bracketMap map[string]string, urlFor func(spec, section string) string) string {
	// Build list of excluded regions (existing Markdown links).
	type region struct{ start, end int }
	var excluded []region
	for _, m := range existingLinkRE.FindAllStringIndex(content, -1) {
		excluded = append(excluded, region{m[0], m[1]})
	}

	isExcluded := func(start, end int) bool {
		for _, r := range excluded {
			if start >= r.start && end <= r.end {
				return true
			}
		}
		return false
	}

	type candidate struct {
		start, end int
		text       string
	}
	var candidates []candidate

	// Multi-section patterns (produce multiple links per match, checked first).
	multiPatterns := []struct {
		re      *regexp.Regexp
		extract multiRefExtractor
	}{
		{tsMultiPrefixRefRE, tsMultiPrefixMRExtractor},
		{tsMultiRefRE, tsMultiMRExtractor},
	}
	for _, pat := range multiPatterns {
		for _, m := range pat.re.FindAllStringSubmatchIndex(content, -1) {
			if isExcluded(m[0], m[1]) {
				continue
			}
			text, ok := pat.extract(m, content, urlFor)
			if !ok {
				continue
			}
			candidates = append(candidates, candidate{
				start: m[0],
				end:   m[1],
				text:  text,
			})
		}
	}

	// Single-section patterns.
	patterns := []struct {
		re      *regexp.Regexp
		extract refExtractor
	}{
		{tsPrefixRefRE, tsPrefixExtractor},
		{tsRefRE, tsExtractor},
		{rfcRefRE, rfcExtractor},
	}
	if bracketMap != nil {
		patterns = append(patterns, struct {
			re      *regexp.Regexp
			extract refExtractor
		}{bracketRefRE, bracketExtractor(bracketMap)})
	}

	for _, pat := range patterns {
		for _, m := range pat.re.FindAllStringSubmatchIndex(content, -1) {
			targetSpec, targetSection, ok := pat.extract(m, content)
			if !ok {
				continue
			}
			if isExcluded(m[0], m[1]) {
				continue
			}
			u := urlFor(targetSpec, targetSection)
			matchText := content[m[0]:m[1]]
			candidates = append(candidates, candidate{
				start: m[0],
				end:   m[1],
				text:  "[" + matchText + "](" + u + ")",
			})
		}
	}

	if len(candidates) == 0 {
		return content
	}

	// Sort by start position.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].start < candidates[j].start
	})

	// Remove overlapping candidates (keep first/earliest).
	filtered := candidates[:1]
	for _, c := range candidates[1:] {
		last := filtered[len(filtered)-1]
		if c.start >= last.end {
			filtered = append(filtered, c)
		}
	}

	// Build result.
	var buf strings.Builder
	pos := 0
	for _, c := range filtered {
		buf.WriteString(content[pos:c.start])
		buf.WriteString(c.text)
		pos = c.end
	}
	buf.WriteString(content[pos:])

	return buf.String()
}
