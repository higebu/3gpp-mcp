package db

import (
	htmlpkg "html"
	"regexp"
	"sort"
	"strings"
)

// existingLinkRE matches Markdown link syntax [text](url) to avoid double-linking.
var existingLinkRE = regexp.MustCompile(`\[[^\]]*\]\([^)]*\)`)

// tableRegionOpenRE matches a converter-emitted table opener: each table is
// its own block, so a real opener sits at the start of a line.
var tableRegionOpenRE = regexp.MustCompile(`(?m)^<table[\s>]`)

// region is a half-open byte range [start, end) within a content string.
type region struct{ start, end int }

// fencedCodeRegions returns the byte ranges of fenced code blocks: from a line
// whose first non-blank characters open a backtick or tilde fence through the
// line that closes it, CommonMark style: the closer uses the same marker, is
// at least as long as the opener, and carries nothing but trailing whitespace
// (so "```go" inside an open fence is content, not a closer, and a ```` fence
// is not closed by ```). An unclosed fence runs to the end of the content.
func fencedCodeRegions(content string) []region {
	var regions []region
	fenceStart := -1
	var fenceChar byte
	fenceLen := 0
	for lineStart := 0; lineStart < len(content); {
		lineEnd := strings.IndexByte(content[lineStart:], '\n')
		if lineEnd < 0 {
			lineEnd = len(content)
		} else {
			lineEnd += lineStart
		}
		line := strings.TrimLeft(content[lineStart:lineEnd], " \t")
		switch {
		case fenceStart < 0 && (strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")):
			fenceStart = lineStart
			fenceChar = line[0]
			fenceLen = markerRunLen(line, fenceChar)
		case fenceStart >= 0 && markerRunLen(line, fenceChar) >= fenceLen &&
			strings.TrimRight(line[markerRunLen(line, fenceChar):], " \t") == "":
			regions = append(regions, region{fenceStart, lineEnd})
			fenceStart = -1
		}
		lineStart = lineEnd + 1
	}
	if fenceStart >= 0 {
		regions = append(regions, region{fenceStart, len(content)})
	}
	return regions
}

// markerRunLen returns the length of the run of marker bytes at the start of
// line.
func markerRunLen(line string, marker byte) int {
	n := 0
	for n < len(line) && line[n] == marker {
		n++
	}
	return n
}

// inlineCodeRegions returns the byte ranges of inline code spans outside the
// given fenced-code regions: a run of backticks closed by the next run of the
// same length, CommonMark style. A span never crosses a blank line or a fence
// boundary; an opener with no closer is not a span.
func inlineCodeRegions(content string, fenced []region) []region {
	var regions []region
	i := 0
	for i < len(content) {
		if inRegion(fenced, i) {
			i = regionEnd(fenced, i)
			continue
		}
		if content[i] != '`' {
			i++
			continue
		}
		open := i
		for i < len(content) && content[i] == '`' {
			i++
		}
		n := i - open

		// The span cannot cross a blank line or run into a fenced block.
		limit := len(content)
		if idx := strings.Index(content[i:], "\n\n"); idx >= 0 {
			limit = i + idx
		}
		for _, r := range fenced {
			if r.start >= i && r.start < limit {
				limit = r.start
			}
		}

		for j := i; j < limit; {
			if content[j] != '`' {
				j++
				continue
			}
			runStart := j
			for j < limit && content[j] == '`' {
				j++
			}
			if j-runStart == n {
				regions = append(regions, region{open, j})
				i = j
				break
			}
		}
	}
	return regions
}

// inRegion reports whether pos falls inside any of the regions.
func inRegion(regions []region, pos int) bool {
	for _, r := range regions {
		if pos >= r.start && pos < r.end {
			return true
		}
	}
	return false
}

// regionEnd returns the end of the region containing pos, or pos+1 when none does.
func regionEnd(regions []region, pos int) int {
	for _, r := range regions {
		if pos >= r.start && pos < r.end {
			return r.end
		}
	}
	return pos + 1
}

// mdLink renders a Markdown link.
func mdLink(text, url string) string {
	return "[" + text + "](" + url + ")"
}

// htmlLink renders an HTML anchor. Used inside raw HTML blocks (e.g. tables)
// where goldmark would not process Markdown link syntax.
func htmlLink(text, url string) string {
	return `<a href="` + htmlpkg.EscapeString(url) + `">` + htmlpkg.EscapeString(text) + `</a>`
}

// markerText escapes reference text for use inside an unresolved-reference
// marker, folding newlines to spaces: the reference regexes can match across
// a line break (sp includes \s), but the web sanitizer relies on markers
// never spanning lines, so the invariant is enforced here at generation.
func markerText(text string) string {
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	return htmlpkg.EscapeString(text)
}

// unresolvedLink renders an anchor for a reference whose target section does
// not exist in the stored version of the target spec: the URL degrades to the
// spec's top page and the title explains why. Emitted as raw HTML in both
// body text and tables — the class carries the visual marker, which Markdown
// link syntax cannot express — and allowlisted by the web sanitizer in
// exactly this form.
func unresolvedLink(text, url, title string) string {
	return `<a class="ref-unresolved" href="` + htmlpkg.EscapeString(url) +
		`" title="` + htmlpkg.EscapeString(title) + `">` + markerText(text) + `</a>`
}

// unresolvedSpan marks reference text whose target section does not exist,
// with a tooltip explaining why. Emitted as raw HTML in both body text and
// tables: goldmark passes inline <span> through and the sanitizer allowlists
// class and title on span.
func unresolvedSpan(text, title string) string {
	return `<span class="ref-unresolved" title="` + htmlpkg.EscapeString(title) + `">` +
		markerText(text) + `</span>`
}

// crossRefTitle explains a cross-spec reference whose section is missing from
// the database's version of the target spec.
func crossRefTitle(spec, section, version string) string {
	label := spec
	if version != "" {
		label += " v" + version
	}
	return "Section " + section + " does not exist in " + label +
		" — the text may reference a different version of " + spec + "; linked to the specification instead"
}

// bareRefTitle explains a bare same-document reference whose section does not
// exist in the current document.
func bareRefTitle(section, label string) string {
	if label == "" {
		label = "this document"
	}
	return "Section " + section + " does not exist in " + label +
		" — possibly a stale or incorrect reference in the source text"
}

// LinkifyRefsOpts configures LinkifyRefs.
type LinkifyRefsOpts struct {
	// BracketMap maps bracket numbers (e.g. "19") to spec IDs (e.g.
	// "TS 33.203"); nil skips bracket references.
	BracketMap map[string]string
	// URLFor returns the URL for (targetSpec, targetSection). An empty
	// targetSpec means the current spec (bare references); an empty
	// targetSection means the spec itself.
	URLFor func(spec, section string) string
	// SectionExists reports whether the current document has a section.
	// nil disables bare same-document references ("clause 4.2" with no spec
	// designator) entirely. A bare reference whose section exists links via
	// URLFor("", section); one whose section is missing is marked with
	// unresolvedSpan instead — a bare reference is same-document by
	// construction, so a missing target is an error in the source text.
	SectionExists func(section string) bool
	// CurrentLabel names the current document in unresolved bare-reference
	// tooltips (e.g. "TS 23.501 v20.2.0"). Empty means "this document".
	CurrentLabel string
	// TargetInfo reports whether targetSpec contains targetSection, and
	// targetSpec's display version. ok=false means the spec cannot be
	// validated (not in the database) and the reference links as-is.
	// nil disables cross-spec validation. A reference whose target section is
	// missing links to the spec's top page with an explanatory tooltip: the
	// database holds one version per spec, so section numbers in the citing
	// text can lag or lead the stored version of the target spec.
	TargetInfo func(spec, section string) (exists bool, version string, ok bool)
}

// resolveTarget returns the URL and tooltip title for a reference to
// (spec, section), degrading to the spec's top page when the section does not
// exist in the database's version of the target spec. RFC references are
// never validated.
func (o *LinkifyRefsOpts) resolveTarget(spec, section string) (url, title string) {
	url = o.URLFor(spec, section)
	if o.TargetInfo == nil || section == "" || strings.HasPrefix(spec, "RFC ") {
		return url, ""
	}
	exists, version, ok := o.TargetInfo(spec, section)
	if !ok || exists {
		return url, ""
	}
	return o.URLFor(spec, ""), crossRefTitle(spec, section, version)
}

// LinkifyRefs replaces spec/RFC/bracket references in Markdown content with
// Markdown links (HTML anchors inside raw HTML table blocks, where goldmark
// would not process Markdown link syntax).
// References inside existing Markdown links, fenced code blocks and inline
// code spans are not replaced: goldmark renders code verbatim, so a rewritten
// reference there would show up as literal link syntax.
func LinkifyRefs(content string, opts LinkifyRefsOpts) string {
	if opts.URLFor == nil { // URLFor is the one mandatory option
		return content
	}
	bracketMap, urlFor, sectionExists := opts.BracketMap, opts.URLFor, opts.SectionExists
	// Build list of excluded regions: existing Markdown links, fenced code
	// blocks and inline code spans.
	var excluded []region
	for _, m := range existingLinkRE.FindAllStringIndex(content, -1) {
		excluded = append(excluded, region{m[0], m[1]})
	}
	fenced := fencedCodeRegions(content)
	excluded = append(excluded, fenced...)
	excluded = append(excluded, inlineCodeRegions(content, fenced)...)

	isExcluded := func(start, end int) bool {
		for _, r := range excluded {
			if start >= r.start && end <= r.end {
				return true
			}
		}
		return false
	}

	// Build list of raw-HTML block regions (tables). goldmark does not process
	// Markdown link syntax inside raw HTML blocks, so references in these regions
	// must be emitted as HTML anchors instead of Markdown links. The DOCX→HTML
	// pipeline always emits lowercase <table>/</table> tags with each table as
	// its own block, so a real opener sits at the start of a line (see
	// tableRegionOpenRE) — prose mentioning "<table>" mid-sentence does not
	// open a region. Search content directly: lowercasing first could shift
	// byte offsets for rare Unicode characters whose lowercase form has a
	// different byte length.
	var htmlRegions []region
	for i := 0; i < len(content); {
		loc := tableRegionOpenRE.FindStringIndex(content[i:])
		if loc == nil {
			break
		}
		open := i + loc[0]
		rel := strings.Index(content[open:], "</table>")
		if rel < 0 {
			htmlRegions = append(htmlRegions, region{open, len(content)})
			break
		}
		end := open + rel + len("</table>")
		htmlRegions = append(htmlRegions, region{open, end})
		i = end
	}

	linkFor := func(start, end int) func(text, url string) string {
		for _, r := range htmlRegions {
			if start >= r.start && end <= r.end {
				return htmlLink
			}
		}
		return mdLink
	}

	type candidate struct {
		start, end int
		// idx is the collection order, which encodes pattern precedence:
		// it breaks ties between candidates covering the exact same range.
		idx  int
		text string
	}
	var candidates []candidate
	addCandidate := func(start, end int, text string) {
		candidates = append(candidates, candidate{start: start, end: end, idx: len(candidates), text: text})
	}

	// Multi-section patterns (produce multiple links per match, checked first).
	multiPatterns := []struct {
		re      *regexp.Regexp
		extract multiRefExtractor
	}{
		{tsCoordPrefixRefRE, tsCoordPrefixMRExtractor},
		{tsMultiPrefixRefRE, tsMultiPrefixMRExtractor},
		{tsMultiRefRE, tsMultiMRExtractor},
	}
	for _, pat := range multiPatterns {
		for _, m := range pat.re.FindAllStringSubmatchIndex(content, -1) {
			if isExcluded(m[0], m[1]) {
				continue
			}
			text, ok := pat.extract(m, content, &opts, linkFor(m[0], m[1]))
			if !ok {
				continue
			}
			addCandidate(m[0], m[1], text)
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
			u, title := opts.resolveTarget(targetSpec, targetSection)
			matchText := content[m[0]:m[1]]
			text := ""
			if title != "" {
				text = unresolvedLink(matchText, u, title)
			} else {
				text = linkFor(m[0], m[1])(matchText, u)
			}
			addCandidate(m[0], m[1], text)
		}
	}

	// Bare same-document references, lowest priority: only where no qualified
	// candidate matched and the context does not tie the reference to another
	// document.
	if sectionExists != nil {
		overlapsAny := func(start, end int) bool {
			for _, c := range candidates {
				if start < c.end && c.start < end {
					return true
				}
			}
			return false
		}
		qualifiedElsewhere := func(start, end int) bool {
			tail := content[end:]
			if bareTrailingQualRE.MatchString(tail) && !barePresentDocRE.MatchString(tail) {
				return true
			}
			if bareTrailingParenSpecRE.MatchString(tail) {
				return true
			}
			head := content[:start]
			// bareLeadingSpecRE is anchored at $; a bounded window keeps the
			// scan cheap. The bound is best-effort: a coordinated list longer
			// than the window hides its designator and the trailing elements
			// linkify as same-document, but no realistic list comes close.
			if len(head) > 512 {
				head = head[len(head)-512:]
			}
			return bareLeadingSpecRE.MatchString(head)
		}
		for _, m := range bareMultiRefRE.FindAllStringSubmatchIndex(content, -1) {
			if isExcluded(m[0], m[1]) || overlapsAny(m[0], m[1]) || qualifiedElsewhere(m[0], m[1]) {
				continue
			}
			mkLink := linkFor(m[0], m[1])
			linked := secNumListRE.ReplaceAllStringFunc(content[m[2]:m[3]], func(sec string) string {
				if !sectionExists(sec) {
					return unresolvedSpan(sec, bareRefTitle(sec, opts.CurrentLabel))
				}
				return mkLink(sec, urlFor("", sec))
			})
			addCandidate(m[0], m[1], content[m[0]:m[2]]+linked)
		}
		for _, m := range bareRefRE.FindAllStringSubmatchIndex(content, -1) {
			if isExcluded(m[0], m[1]) || overlapsAny(m[0], m[1]) || qualifiedElsewhere(m[0], m[1]) {
				continue
			}
			sec := content[m[2]:m[3]]
			matchText := content[m[0]:m[1]]
			text := ""
			if sectionExists(sec) {
				text = linkFor(m[0], m[1])(matchText, urlFor("", sec))
			} else {
				// A bare reference is same-document by construction, so a
				// missing section is worth surfacing: mark it instead of
				// leaving silently ambiguous plain text.
				text = unresolvedSpan(matchText, bareRefTitle(sec, opts.CurrentLabel))
			}
			addCandidate(m[0], m[1], text)
		}
	}

	if len(candidates) == 0 {
		return content
	}

	// Sort by start position; on a tie the longer match wins, then the
	// earlier-collected candidate (pattern precedence) — fully deterministic.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].start != candidates[j].start {
			return candidates[i].start < candidates[j].start
		}
		if candidates[i].end != candidates[j].end {
			return candidates[i].end > candidates[j].end
		}
		return candidates[i].idx < candidates[j].idx
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
