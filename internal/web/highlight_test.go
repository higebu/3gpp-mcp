package web

import (
	"strings"
	"testing"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
)

// TestRenderMarkdown_CodeBlockHighlighting pins which code blocks the
// highlight extension takes over: only a fenced block naming a language
// Chroma knows. The rest keep goldmark's escaped <pre><code> rendering.
func TestRenderMarkdown_CodeBlockHighlighting(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"known language", "```asn1\nA ::= INTEGER\n```\n", `<pre class="chroma"`},
		{"unknown language", "```zzznotalanguage\na <b>\n```\n", "<pre><code class=\"language-zzznotalanguage\">a &lt;b&gt;\n</code></pre>"},
		{"no language", "```\na <b>\n```\n", "<pre><code>a &lt;b&gt;\n</code></pre>"},
		{"indented", "para\n\n    a = b\n", "<pre><code>a = b\n</code></pre>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := renderMarkdown(tt.content, renderOpts{specID: "TS 23.501"})
			if !strings.Contains(got, tt.want) {
				t.Errorf("renderMarkdown(%q) = %q, want it to contain %q", tt.content, got, tt.want)
			}
		})
	}
}

// TestRenderMarkdown_HighlightError pins that a lexer failing to tokenise
// fails the render instead of emitting partial output. Chroma compiles a
// lexer's rules on first use, so an invalid pattern surfaces in Tokenise.
func TestRenderMarkdown_HighlightError(t *testing.T) {
	lexers.Register(chroma.MustNewLexer(
		&chroma.Config{Name: "zzzbrokenlexer", Aliases: []string{"zzzbrokenlexer"}},
		func() chroma.Rules {
			return chroma.Rules{"root": {{Pattern: `(`, Type: chroma.Text}}}
		},
	))
	got := renderMarkdown("```zzzbrokenlexer\nx\n```\n", renderOpts{specID: "TS 23.501"})
	if got != "<p>Error rendering content</p>" {
		t.Errorf("renderMarkdown() = %q, want the render error page", got)
	}
}
