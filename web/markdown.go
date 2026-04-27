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

	var buf bytes.Buffer
	if err := md.Convert([]byte(content), &buf); err != nil {
		return "<p>Error rendering content</p>"
	}
	return buf.String()
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
