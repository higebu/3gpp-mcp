package web

import (
	"io"
	"strings"

	"github.com/alecthomas/chroma/v2"
	chromahtml "github.com/alecthomas/chroma/v2/formatters/html"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
	"github.com/yuin/goldmark/v2/ast"
	"github.com/yuin/goldmark/v2/renderer"
	"github.com/yuin/goldmark/v2/renderer/html"
)

// highlightExtension renders a fenced code block whose info string names a
// language Chroma knows — including the asn1, diameter and sip lexers this
// package registers — as Chroma HTML with CSS classes. Other code blocks fall
// through to goldmark's renderer. It stands in for goldmark-highlighting,
// whose goldmark/v2 release requires Chroma v3.
type highlightExtension struct {
	style     *chroma.Style
	formatter *chromahtml.Formatter
}

func newHighlightExtension() html.Extension {
	style := styles.Get("github")
	if style == nil {
		style = styles.Fallback
	}
	return &highlightExtension{
		style:     style,
		formatter: chromahtml.New(chromahtml.WithClasses(true), chromahtml.WithLineNumbers(false)),
	}
}

func (e *highlightExtension) RendererOptions(_ *html.Config) []html.Option {
	decorator := func(next html.NodeRenderer) html.NodeRenderer {
		return html.NodeRendererFunc(func(w io.Writer, source []byte, node ast.Node,
			entering bool, rc renderer.Context) (ast.WalkStatus, error) {
			n := node.(*ast.CodeBlock)
			if n.CodeBlockKind != ast.CodeBlockKindFenced {
				return next.Render(w, source, node, entering, rc)
			}
			language, ok := n.Language(source)
			var lexer chroma.Lexer
			if ok {
				lexer = lexers.Get(language)
			}
			if lexer == nil {
				return next.Render(w, source, node, entering, rc)
			}
			if !entering {
				return ast.WalkContinue, nil
			}
			var code strings.Builder
			_, _ = n.Value.WriteTo(&code, source)
			iterator, err := chroma.Coalesce(lexer).Tokenise(nil, code.String())
			if err != nil {
				_, _ = next.Render(w, source, node, true, rc)
				return next.Render(w, source, node, false, rc)
			}
			if err := e.formatter.Format(w, e.style, iterator); err != nil {
				return ast.WalkStop, err
			}
			return ast.WalkContinue, nil
		})
	}
	return []html.Option{html.WithNodeRendererDecorator(ast.KindCodeBlock, decorator)}
}
