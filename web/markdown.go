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
	// ($$...$$) is tried before inline ($...$). Inline math may not span lines.
	mathRE = regexp.MustCompile(`\$\$([^$]+)\$\$|\$([^$\n]+)\$`)
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

// renderMarkdown converts Markdown content to HTML, rewriting image:// URLs
// and linkifying inline spec/RFC references. A non-empty version is carried
// on every image URL so an archived version serves its own images.
func renderMarkdown(content, specID, version string, bracketMap map[string]string) string {
	// Linkify spec references before image/figure rewrites to avoid processing HTML attributes.
	content = db.LinkifyRefs(content, bracketMap, func(spec, section string) string {
		if strings.HasPrefix(spec, "RFC ") {
			u := "https://www.rfc-editor.org/rfc/rfc" + strings.TrimPrefix(spec, "RFC ")
			if section != "" {
				u += "#section-" + section
			}
			return u
		}
		u := "/specs/" + url.PathEscape(spec)
		if section != "" {
			u += "/sections/" + section
		}
		return u
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
		sb.WriteString(escapeUnknownHTML(text))
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

// newMathToken returns a random alphanumeric token for this render's math
// placeholders. Randomness guarantees document text cannot contain a
// placeholder lookalike, so re-injection only ever hits real placeholders.
func newMathToken() string {
	var b [12]byte
	if _, err := cryptorand.Read(b[:]); err != nil {
		// Never happens in practice; a collision here only misrenders math.
		return "katexmathfallbacktoken"
	}
	return "katexmath" + hex.EncodeToString(b[:])
}

// mathPlaceholder returns an inert token that survives goldmark conversion
// unchanged (plain alphanumerics trigger no Markdown syntax). The trailing
// "x" delimits the index so no placeholder is a substring of another.
func mathPlaceholder(token string, i int) string {
	return fmt.Sprintf("%s%dx", token, i)
}

// protectMath extracts LaTeX math spans from text, replacing each with a
// placeholder built from token, and appends the <span> HTML to re-inject
// after conversion to spans.
func protectMath(text, token string, spans *[]string) string {
	return mathRE.ReplaceAllStringFunc(text, func(match string) string {
		sub := mathRE.FindStringSubmatch(match)
		display := sub[1] != ""
		latex, class := sub[2], "math-inline"
		if display {
			latex, class = sub[1], "math-display"
		}
		latex = htmlpkg.EscapeString(htmlpkg.UnescapeString(latex))
		i := len(*spans)
		*spans = append(*spans, fmt.Sprintf(`<span class="%s">%s</span>`, class, latex))
		return mathPlaceholder(token, i)
	})
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
