package web

import (
	"bytes"
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
	figureRE    = regexp.MustCompile(`\[Figure:\s*([^(]+?)\s*\(([^,]+),\s*use get_image to retrieve(?:,\s*(\d+)x(\d+))?\)\]`)
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
			html.WithUnsafe(),
		),
	)
}

// renderMarkdown converts Markdown content to HTML, rewriting image:// URLs
// and linkifying inline spec/RFC references.
func renderMarkdown(content, specID string, bracketMap map[string]string) string {
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
	content = imageRE.ReplaceAllStringFunc(content, func(match string) string {
		sub := imageRE.FindStringSubmatch(match)
		alt, name := sub[1], sub[2]
		src := "/specs/" + escapedSpec + "/images/" + url.PathEscape(name)
		if sub[3] != "" && sub[4] != "" {
			return fmt.Sprintf(`<img src="%s" alt="%s" width="%s" height="%s">`,
				src, htmlpkg.EscapeString(alt), sub[3], sub[4])
		}
		return fmt.Sprintf("![%s](%s)", alt, src)
	})
	content = htmlImageRE.ReplaceAllStringFunc(content, func(match string) string {
		sub := htmlImageRE.FindStringSubmatch(match)
		prefix, name, suffix := sub[1], sub[2], sub[3]
		src := "/specs/" + escapedSpec + "/images/" + url.PathEscape(name)
		return prefix + src + suffix
	})
	content = figureRE.ReplaceAllStringFunc(content, func(match string) string {
		sub := figureRE.FindStringSubmatch(match)
		alt, name := sub[1], sub[2]
		src := "/specs/" + escapedSpec + "/images/" + url.PathEscape(name)
		escapedAlt := htmlpkg.EscapeString(alt)
		dimAttrs := ""
		if len(sub) >= 5 && sub[3] != "" && sub[4] != "" {
			dimAttrs = fmt.Sprintf(` width="%s" height="%s"`, sub[3], sub[4])
		}
		return fmt.Sprintf(`<figure><img src="%s" alt="%s"%s><figcaption>%s</figcaption></figure>`,
			src, escapedAlt, dimAttrs, escapedAlt)
	})

	// Protect LaTeX math from goldmark, which would otherwise mangle backslash
	// sequences (e.g. \\, \{) and emphasis characters. Each math span is
	// replaced with an inert placeholder and re-injected after conversion as a
	// <span> that the client-side KaTeX renderer targets. The inner LaTeX is
	// normalized to single HTML-escaping so both raw (paragraph) and
	// pre-escaped (table cell) math produce correct textContent.
	content, mathSpans := protectMath(content)

	var buf bytes.Buffer
	if err := md.Convert([]byte(content), &buf); err != nil {
		return "<p>Error rendering content</p>"
	}
	out := buf.String()
	for i, span := range mathSpans {
		out = strings.ReplaceAll(out, mathPlaceholder(i), span)
	}
	return out
}

// mathPlaceholder returns an inert token that survives goldmark conversion
// unchanged (plain alphanumerics trigger no Markdown syntax).
func mathPlaceholder(i int) string {
	return fmt.Sprintf("xxkatexmathxx%dxxkatexmathxx", i)
}

// protectMath extracts LaTeX math spans, replacing each with a placeholder, and
// returns the rewritten content plus the <span> HTML to re-inject afterwards.
func protectMath(content string) (string, []string) {
	var spans []string
	rewritten := mathRE.ReplaceAllStringFunc(content, func(match string) string {
		sub := mathRE.FindStringSubmatch(match)
		display := sub[1] != ""
		latex, class := sub[2], "math-inline"
		if display {
			latex, class = sub[1], "math-display"
		}
		latex = htmlpkg.EscapeString(htmlpkg.UnescapeString(latex))
		i := len(spans)
		spans = append(spans, fmt.Sprintf(`<span class="%s">%s</span>`, class, latex))
		return mathPlaceholder(i)
	})
	return rewritten, spans
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
		return "<pre><code>" + content + "</code></pre>"
	}

	var buf bytes.Buffer
	if err := formatter.Format(&buf, style, iterator); err != nil {
		return "<pre><code>" + content + "</code></pre>"
	}
	return buf.String()
}
