package web

import (
	"strings"
	"testing"
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
