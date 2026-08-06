package web

import (
	"regexp"
	"strings"

	"github.com/microcosm-cc/bluemonday"
)

// allowedTagRE matches an HTML tag the rendering pipeline legitimately
// produces before goldmark runs: the DOCX converter's table markup and list
// tags inside cells (converter/docx/table.go), <sub>/<sup> in body text, and
// the image:// rewrite's <img>. db.LinkifyRefs emits Markdown links in body
// text; its raw markup — table anchors and unresolved-reference markers —
// is handled separately (markerOpenRE/markerCloseRE and the table-region
// skip in renderMarkdown). Any other angle bracket in body text is document
// text — 3GPP prose is full of placeholders like <SUPI> — and must be
// escaped so it stays visible instead of being parsed as markup.
var allowedTagRE = regexp.MustCompile(
	`(?i)^</?(?:img|li|ol|p|sub|sup|table|tbody|td|th|thead|tr|ul)(?:\s[^<>]*)?/?>`)

// markerOpenRE and markerCloseRE match db.LinkifyRefs's unresolved-reference
// markers in body text — only the exact class="ref-unresolved" form, so a
// literal <span> or <a> in document prose still escapes to visible text.
// Closers are only admitted while a marker is open (see escapeUnknownHTML),
// so a stray literal </span> in prose stays visible too.
var (
	markerOpenRE  = regexp.MustCompile(`^<(span|a) class="ref-unresolved"[^<>]*>`)
	markerCloseRE = regexp.MustCompile(`^</(span|a)>`)
)

// escapeUnknownHTML escapes every '<' in text that does not start a tag the
// pipeline itself emits, so third-party document content cannot inject raw
// HTML (stored XSS) and angle-bracket placeholders survive as visible text.
// Allowlisted tags pass through here and are attribute-sanitized later by
// sanitizeHTML.
func escapeUnknownHTML(text string) string {
	var b strings.Builder
	open := map[string]int{}
	for {
		i := strings.IndexByte(text, '<')
		if i < 0 {
			if b.Len() == 0 {
				return text
			}
			b.WriteString(text)
			return b.String()
		}
		b.WriteString(text[:i])
		text = text[i:]
		if m := markerOpenRE.FindStringSubmatch(text); m != nil {
			open[m[1]]++
			b.WriteString(m[0])
			text = text[len(m[0]):]
			continue
		}
		if m := markerCloseRE.FindStringSubmatch(text); m != nil && open[m[1]] > 0 {
			open[m[1]]--
			b.WriteString(m[0])
			text = text[len(m[0]):]
			continue
		}
		if loc := allowedTagRE.FindStringIndex(text); loc != nil {
			b.WriteString(text[:loc[1]])
			text = text[loc[1]:]
			continue
		}
		b.WriteString("&lt;")
		text = text[1:]
	}
}

// sanitizePolicy is the allowlist every rendered HTML fragment passes through
// before being wrapped in template.HTML. It admits exactly what the pipeline
// produces: goldmark output, the converter's table HTML, Chroma highlight
// spans, KaTeX math spans, linkified anchors and rewritten images. It runs
// after math re-injection and syntax highlighting, which both add <span>
// elements to the converted output.
var sanitizePolicy = newSanitizePolicy()

func newSanitizePolicy() *bluemonday.Policy {
	p := bluemonday.NewPolicy()

	// Structure.
	p.AllowElements("p", "br", "hr", "ul", "ol", "li", "blockquote",
		"h1", "h2", "h3", "h4", "h5", "h6")
	// Tables: the converter's raw HTML (colspan/rowspan) and goldmark's Table
	// extension (text-align styles from delimiter-row alignment).
	p.AllowElements("table", "thead", "tbody", "tr", "th", "td")
	p.AllowAttrs("colspan", "rowspan").Matching(bluemonday.Integer).OnElements("th", "td")
	p.AllowAttrs("style").Matching(regexp.MustCompile(`^text-align:\s*(?:left|center|right);?$`)).OnElements("th", "td")
	// Inline markup, plus class-carrying span/pre/code for Chroma tokens and
	// KaTeX math targets.
	p.AllowElements("strong", "em", "del", "sub", "sup", "span", "pre", "code")
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^[a-zA-Z0-9 _-]+$`)).OnElements("span", "pre", "code")
	// Unresolved-reference markers (db.LinkifyRefs) carry their explanation
	// in a title tooltip.
	p.AllowAttrs("title").OnElements("span")
	// Links: the pipeline only produces site-relative anchors
	// (db.LinkifyRefs spec links) and https://www.rfc-editor.org RFC links.
	// The pattern rejects protocol-relative //host and every other absolute
	// URL, so document content cannot inject an external link into a spec
	// page.
	p.AllowAttrs("href").Matching(regexp.MustCompile(
		`^(?:#.*|/(?:[^/\\].*)?|https://www\.rfc-editor\.org/.*)$`)).OnElements("a")
	p.AllowAttrs("rel").Matching(regexp.MustCompile(`^[a-z ]+$`)).OnElements("a")
	p.AllowAttrs("title").OnElements("a")
	// Unresolved-reference anchors (db.LinkifyRefs) carry the marker class.
	p.AllowAttrs("class").Matching(regexp.MustCompile(`^ref-unresolved$`)).OnElements("a")
	// Images: only the site-relative URLs the image:// rewrite produces
	// (reject protocol-relative //host and any absolute URL).
	p.AllowAttrs("src").Matching(regexp.MustCompile(`^/[^/\\]`)).OnElements("img")
	p.AllowAttrs("alt").OnElements("img")
	p.AllowAttrs("width", "height").Matching(bluemonday.Integer).OnElements("img")

	p.AllowURLSchemes("https")
	p.AllowRelativeURLs(true)
	p.RequireParseableURLs(true)
	return p
}

// sanitizeHTML reduces rendered HTML to the allowlist above. It is the last
// step before template.HTML: nothing outside the allowlist — elements,
// attributes or URL schemes — reaches the browser.
func sanitizeHTML(s string) string {
	return sanitizePolicy.Sanitize(s)
}
