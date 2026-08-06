package web

import (
	"bytes"
	cryptorand "crypto/rand"
	"encoding/hex"
	"fmt"
	htmlpkg "html"
	"net/url"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/higebu/3gpp-mcp/db"
	"github.com/yuin/goldmark"
	highlighting "github.com/yuin/goldmark-highlighting/v2"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var (
	imageRE     = regexp.MustCompile(`!\[([^\]]*)\]\(image://([^?)]+)(?:\?w=(\d+)&h=(\d+))?\)`)
	htmlImageRE = regexp.MustCompile(`(<img\s+[^>]*?\bsrc=")image://([^"?]+)(?:\?[^"]*)?("[^>]*>)`)
	// mathRE matches LaTeX math emitted by the DOCX converter: display
	// ($$...$$) is tried before inline ($...$). Inline math may not span
	// lines and — following the usual convention for dollar-delimited math —
	// may not carry whitespace just inside its delimiters, so a sentence with
	// two spaced-out dollar signs is not swallowed whole. isInlineMath
	// rejects the rest.
	mathRE = regexp.MustCompile(`\$\$([^$]+)\$\$|\$([^\s$](?:[^$\n]*[^\s$])?)\$`)

	// fallbackTokenCount disambiguates math tokens minted in the same
	// nanosecond when crypto/rand is unavailable.
	fallbackTokenCount atomic.Uint64
)

var md goldmark.Markdown

func init() {
	md = goldmark.New(
		goldmark.WithExtensions(
			extension.Table,
			extension.Strikethrough,
			extension.TaskList,
			highlighting.NewHighlighting(
				highlighting.WithStyle("github"),
				highlighting.WithFormatOptions(
					chromahtml.WithClasses(true),
					chromahtml.WithLineNumbers(false),
				),
			),
		),
		goldmark.WithRendererOptions(
			// WithUnsafe is required because the DOCX converter emits raw HTML
			// on purpose (tables, <sub>/<sup>, <img>). It is safe only because
			// renderMarkdown escapes non-allowlisted tags before conversion and
			// sanitizeHTML allowlists the final output.
			html.WithUnsafe(),
		),
	)
}

// renderOpts configures renderMarkdown.
type renderOpts struct {
	specID  string
	version string // version carried on same-spec URLs; empty on database-version pages
	display string // displayed version of the current document, for unresolved-reference tooltips
	// bracketMap maps bracket numbers to spec IDs; nil skips bracket refs.
	bracketMap map[string]string
	// sectionExists gates bare same-document references ("clause 4.2");
	// nil skips them.
	sectionExists func(string) bool
	// targetInfo validates cross-spec section references; nil skips validation.
	targetInfo func(spec, section string) (exists bool, version string, ok bool)
}

// renderMarkdown converts Markdown content to HTML, rewriting image:// URLs
// and linkifying inline spec/RFC references. A non-empty o.version is carried
// on every image URL and every same-spec section link so an archived version
// serves its own images and stays within itself when followed.
func renderMarkdown(content string, o renderOpts) string {
	specID, version := o.specID, o.version
	currentLabel := specID
	if o.display != "" {
		currentLabel += " v" + o.display
	}
	// Linkify spec references before image/figure rewrites to avoid processing HTML attributes.
	content = db.LinkifyRefs(content, db.LinkifyRefsOpts{
		BracketMap: o.bracketMap,
		URLFor: func(spec, section string) string {
			if strings.HasPrefix(spec, "RFC ") {
				u := "https://www.rfc-editor.org/rfc/rfc" + strings.TrimPrefix(spec, "RFC ")
				if section != "" {
					u += "#section-" + section
				}
				return u
			}
			if spec == "" { // bare reference: the current spec
				spec = specID
			}
			u := "/specs/" + url.PathEscape(spec)
			if section != "" {
				u += "/sections/" + section
			}
			if version != "" && spec == specID {
				u += "?version=" + url.QueryEscape(version)
			}
			return u
		},
		SectionExists: o.sectionExists,
		CurrentLabel:  currentLabel,
		TargetInfo:    o.targetInfo,
	})
	escapedSpec := url.PathEscape(specID)
	imageURL := func(name string) string {
		src := "/specs/" + escapedSpec + "/images/" + url.PathEscape(name)
		if version != "" {
			src += "?version=" + url.QueryEscape(version)
		}
		return src
	}
	content = imageRE.ReplaceAllStringFunc(content, func(match string) string {
		sub := imageRE.FindStringSubmatch(match)
		alt, name := sub[1], sub[2]
		src := imageURL(name)
		if sub[3] != "" && sub[4] != "" {
			return fmt.Sprintf(`<img src="%s" alt="%s" width="%s" height="%s">`,
				src, htmlpkg.EscapeString(alt), sub[3], sub[4])
		}
		return fmt.Sprintf("![%s](%s)", alt, src)
	})
	content = htmlImageRE.ReplaceAllStringFunc(content, func(match string) string {
		sub := htmlImageRE.FindStringSubmatch(match)
		prefix, name, suffix := sub[1], sub[2], sub[3]
		return prefix + imageURL(name) + suffix
	})

	// Protect LaTeX math from goldmark, which would otherwise mangle backslash
	// sequences (e.g. \\, \{) and emphasis characters. Each math span is
	// replaced with an inert placeholder and re-injected after conversion as a
	// <span> that the client-side KaTeX renderer targets. The inner LaTeX is
	// normalized to single HTML-escaping so both raw (paragraph) and
	// pre-escaped (table cell) math produce correct textContent.
	//
	// Both math protection and raw-HTML escaping are applied only to text
	// outside fenced code blocks and inline code spans: a '$' inside a code
	// fence is code, not math, and goldmark escapes code content itself. The
	// placeholder token is random per render so literal text can never collide
	// with a placeholder.
	token := newMathToken()
	var mathSpans []string
	segs := splitCodeSegments(content)
	var sb strings.Builder
	sb.Grow(len(content))
	for _, seg := range segs {
		if seg.code {
			sb.WriteString(seg.text)
			continue
		}
		text := protectMath(seg.text, token, &mathSpans)
		sb.WriteString(escapeOutsideTables(text))
	}
	content = sb.String()

	var buf bytes.Buffer
	if err := md.Convert([]byte(content), &buf); err != nil {
		return "<p>Error rendering content</p>"
	}
	out := buf.String()
	for i, span := range mathSpans {
		out = strings.Replace(out, mathPlaceholder(token, i), span, 1)
	}
	return sanitizeHTML(out)
}

// tableOpenRE matches the start of a converter-emitted table region: the
// converter emits each table as its own block, so a real opener sits at the
// start of a line. The anchor and boundary character keep prose mentioning
// "<table>" mid-sentence — or words like "<tabletop" — from opening a region
// and letting the text up to a real table's closer bypass escaping.
var tableOpenRE = regexp.MustCompile(`(?m)^<table[\s>]`)

// escapeOutsideTables applies escapeUnknownHTML to text outside
// <table>...</table> regions and passes table regions through verbatim.
// Table markup is pipeline-generated — the DOCX converter's tags with cell
// text already entity-escaped at build time, plus db.LinkifyRefs's raw
// anchors — so escaping there would turn the anchors into visible text
// (and did, before this function existed). An opener with no closer is not
// treated as a region: the rest is escaped normally, which keeps document
// prose mentioning "<table>" from disabling escaping. sanitizeHTML still
// attribute-sanitizes everything afterwards.
func escapeOutsideTables(text string) string {
	var b strings.Builder
	for {
		loc := tableOpenRE.FindStringIndex(text)
		if loc == nil {
			if b.Len() == 0 {
				return escapeUnknownHTML(text)
			}
			b.WriteString(escapeUnknownHTML(text))
			return b.String()
		}
		rest := text[loc[0]:]
		end := strings.Index(rest, "</table>")
		if end < 0 {
			b.WriteString(escapeUnknownHTML(text))
			return b.String()
		}
		b.WriteString(escapeUnknownHTML(text[:loc[0]]))
		end += len("</table>")
		b.WriteString(rest[:end])
		text = rest[end:]
	}
}

// randRead is cryptorand.Read, injectable so tests can exercise the
// fallback branch.
var randRead = cryptorand.Read

// newMathToken returns a random alphanumeric token for this render's math
// placeholders. Randomness guarantees document text cannot contain a
// placeholder lookalike, so re-injection only ever hits real placeholders.
func newMathToken() string {
	var b [12]byte
	if _, err := randRead(b[:]); err != nil {
		return fallbackMathToken()
	}
	return "katexmath" + hex.EncodeToString(b[:])
}

// fallbackMathToken keeps the token unpredictable even without crypto/rand: a
// constant fallback would let literal document text collide with a
// placeholder on every render.
func fallbackMathToken() string {
	return fmt.Sprintf("katexmath%xt%dc", time.Now().UnixNano(), fallbackTokenCount.Add(1))
}

// mathPlaceholder returns an inert token that survives goldmark conversion
// unchanged (plain alphanumerics trigger no Markdown syntax). The trailing
// "x" delimits the index so no placeholder is a substring of another.
func mathPlaceholder(token string, i int) string {
	return fmt.Sprintf("%s%dx", token, i)
}

// protectMath extracts LaTeX math spans from text, replacing each with a
// placeholder built from token, and appends the <span> HTML to re-inject
// after conversion to spans. A $...$ span isInlineMath rejects is left alone,
// so its dollar signs stay visible text.
func protectMath(text, token string, spans *[]string) string {
	locs := mathRE.FindAllStringSubmatchIndex(text, -1)
	if locs == nil {
		return text
	}
	var b strings.Builder
	b.Grow(len(text))
	last := 0
	for _, loc := range locs {
		display := loc[2] >= 0
		class := "math-inline"
		var latex string
		if display {
			latex, class = text[loc[2]:loc[3]], "math-display"
		} else {
			latex = text[loc[4]:loc[5]]
			if !isInlineMath(text[last:loc[0]], latex) {
				continue // prose, not math: leave the dollars as text
			}
		}
		latex = htmlpkg.EscapeString(htmlpkg.UnescapeString(latex))
		i := len(*spans)
		*spans = append(*spans, fmt.Sprintf(`<span class="%s">%s</span>`, class, latex))
		b.WriteString(text[last:loc[0]])
		b.WriteString(mathPlaceholder(token, i))
		last = loc[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

// quoteBeforeRE matches a quote character — literal or entity — at the end of
// the text preceding a math span.
var quoteBeforeRE = regexp.MustCompile(`(?:["']|&(?:quot|apos|#34|#39);)$`)

// isInlineMath reports whether the $...$ span latex, preceded by before, is
// LaTeX rather than prose that happens to carry two dollar signs on one line.
// 3GPP prose quotes the JSON Schema "$ref" keyword — '$ref', "$ref:
// '#/components/schemas/X'" — and the text between two such mentions has no
// whitespace at its edges, so the delimiter convention alone lets a whole
// sentence render as math (TS 29.501 clause 5.3.9). Two shapes mark that
// prose: an opening delimiter that a quote introduces, and a double quote
// inside the span, which the converter's LaTeX never produces (a prime is an
// apostrophe, so apostrophes stay legal). Both are checked after unescaping
// because table-cell text reaches here already HTML-escaped.
func isInlineMath(before, latex string) bool {
	if quoteBeforeRE.MatchString(before) {
		return false
	}
	return !strings.Contains(htmlpkg.UnescapeString(latex), `"`)
}

// highlightYAML applies Chroma syntax highlighting to YAML content.
func highlightYAML(content string) string {
	lexer := lexers.Get("yaml")
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	formatter := chromahtml.New(chromahtml.WithClasses(true))
	style := styles.Get("github")

	iterator, err := lexer.Tokenise(nil, content)
	if err != nil {
		return "<pre><code>" + htmlpkg.EscapeString(content) + "</code></pre>"
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return "<pre><code>" + htmlpkg.EscapeString(content) + "</code></pre>"
	}
	// Same defense as renderMarkdown: the YAML comes from third-party spec
	// archives, so the highlighted HTML is reduced to the allowlist too.
	return sanitizeHTML(buf.String())
}
