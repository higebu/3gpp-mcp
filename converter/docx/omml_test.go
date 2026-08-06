package docx

import (
	"encoding/xml"
	"strings"
	"testing"
)

const mXMLNS = `xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math"`

// ommlLaTeX drives the OMML→LaTeX converter from a raw XML fixture by scanning
// for the first m:oMath / m:oMathPara start element.
func ommlLaTeX(t *testing.T, x string) string {
	t.Helper()
	d := xml.NewDecoder(strings.NewReader(x))
	for {
		tok, err := d.Token()
		if err != nil {
			t.Fatalf("no oMath found: %v", err)
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Space == mNS &&
			(se.Name.Local == "oMath" || se.Name.Local == "oMathPara") {
			return ommlToLaTeX(d, se)
		}
	}
}

// mrun wraps text in an OMML run element.
func mrun(s string) string {
	return `<m:r><m:t>` + s + `</m:t></m:r>`
}

func TestOMMLToLaTeX(t *testing.T) {
	tests := []struct {
		name string
		xml  string
		want string
	}{
		{
			name: "plain run",
			xml:  `<m:oMath ` + mXMLNS + `>` + mrun("x+1") + `</m:oMath>`,
			want: "x+1",
		},
		{
			name: "subscript",
			xml: `<m:oMath ` + mXMLNS + `><m:sSub>` +
				`<m:e>` + mrun("n") + `</m:e><m:sub>` + mrun("78") + `</m:sub>` +
				`</m:sSub></m:oMath>`,
			want: "{n}_{78}",
		},
		{
			name: "superscript",
			xml: `<m:oMath ` + mXMLNS + `><m:sSup>` +
				`<m:e>` + mrun("x") + `</m:e><m:sup>` + mrun("2") + `</m:sup>` +
				`</m:sSup></m:oMath>`,
			want: "{x}^{2}",
		},
		{
			name: "subsuperscript",
			xml: `<m:oMath ` + mXMLNS + `><m:sSubSup>` +
				`<m:e>` + mrun("x") + `</m:e>` +
				`<m:sub>` + mrun("i") + `</m:sub>` +
				`<m:sup>` + mrun("2") + `</m:sup>` +
				`</m:sSubSup></m:oMath>`,
			want: "{x}_{i}^{2}",
		},
		{
			name: "fraction",
			xml: `<m:oMath ` + mXMLNS + `><m:f>` +
				`<m:num>` + mrun("1") + `</m:num><m:den>` + mrun("2") + `</m:den>` +
				`</m:f></m:oMath>`,
			want: "\\frac{1}{2}",
		},
		{
			name: "linear fraction",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num>` + mrun("a") + `</m:num><m:den>` + mrun("b") + `</m:den>` +
				`</m:f></m:oMath>`,
			want: "a/b",
		},
		{
			// "/" is a plain character in LaTeX, so an ungrouped "a+b/c+d"
			// would read as a+(b/c)+d (issue #141).
			name: "linear fraction fences compound operands",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num>` + mrun("a+b") + `</m:num><m:den>` + mrun("c+d") + `</m:den>` +
				`</m:f></m:oMath>`,
			want: "\\left(a+b\\right)/\\left(c+d\\right)",
		},
		{
			name: "linear fraction keeps a leading sign unfenced",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num>` + mrun("-1") + `</m:num><m:den>` + mrun("2") + `</m:den>` +
				`</m:f></m:oMath>`,
			want: "-1/2",
		},
		{
			name: "linear fraction does not re-fence a delimited operand",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num><m:d><m:e>` + mrun("a+b") + `</m:e></m:d></m:num>` +
				`<m:den>` + mrun("c") + `</m:den></m:f></m:oMath>`,
			want: "\\left(a+b\\right)/c",
		},
		{
			// A nested division in the denominator is the other way "/" can
			// re-associate: "a/b/c" would read as (a/b)/c.
			name: "linear fraction fences a nested linear fraction",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num>` + mrun("a") + `</m:num><m:den>` +
				`<m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num>` + mrun("b") + `</m:num><m:den>` + mrun("c") + `</m:den></m:f>` +
				`</m:den></m:f></m:oMath>`,
			want: "a/\\left(b/c\\right)",
		},
		{
			// "\leftarrow" must not be mistaken for a "\left" group opener,
			// which would hide the top-level "+" from the scanner.
			name: "linear fraction fences an operand holding an arrow command",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num>` + mrun("a←b+c") + `</m:num><m:den>` + mrun("d") + `</m:den>` +
				`</m:f></m:oMath>`,
			want: "\\left(a\\leftarrow b+c\\right)/d",
		},
		{
			// The operator reaches needsLinGroup as the command "\pm", not as
			// a literal character.
			name: "linear fraction fences an operand joined by a symbol command",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num>` + mrun("a±b") + `</m:num><m:den>` + mrun("c") + `</m:den>` +
				`</m:f></m:oMath>`,
			want: "\\left(a\\pm b\\right)/c",
		},
		{
			// Multiplication re-associates in a denominator: "a/b×c" reads as
			// (a/b)×c, not a/(b×c).
			name: "linear fraction fences a multiplied denominator",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num>` + mrun("a") + `</m:num><m:den>` + mrun("b×c") + `</m:den>` +
				`</m:f></m:oMath>`,
			want: "a/\\left(b\\times c\\right)",
		},
		{
			// U+2217 is mapped to a literal "*", so the multiplication is
			// invisible to a command-based check: "a/b*c" would read as
			// (a/b)*c.
			name: "linear fraction fences a denominator multiplied with an asterisk",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num>` + mrun("a") + `</m:num><m:den>` + mrun("b∗c") + `</m:den>` +
				`</m:f></m:oMath>`,
			want: "a/\\left(b*c\\right)",
		},
		{
			name: "linear fraction fences an operand holding a relation",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num>` + mrun("a=b") + `</m:num><m:den>` + mrun("c") + `</m:den>` +
				`</m:f></m:oMath>`,
			want: "\\left(a=b\\right)/c",
		},
		{
			// Braces already group, and the \frac itself is a single atom.
			name: "linear fraction with a bar fraction operand needs no fence",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:fPr><m:type m:val="lin"/></m:fPr>` +
				`<m:num>` + mrun("x") + `</m:num><m:den><m:f>` +
				`<m:num>` + mrun("a+b") + `</m:num><m:den>` + mrun("2") + `</m:den>` +
				`</m:f></m:den></m:f></m:oMath>`,
			want: "x/\\frac{a+b}{2}",
		},
		{
			name: "matrix",
			xml: `<m:oMath ` + mXMLNS + `><m:m>` +
				`<m:mr><m:e>` + mrun("1") + `</m:e><m:e>` + mrun("j") + `</m:e></m:mr>` +
				`<m:mr><m:e>` + mrun("-1") + `</m:e><m:e>` + mrun("j") + `</m:e></m:mr>` +
				`</m:m></m:oMath>`,
			want: "\\begin{matrix} 1 & j \\\\ -1 & j \\end{matrix}",
		},
		{
			name: "radical with degree",
			xml: `<m:oMath ` + mXMLNS + `><m:rad>` +
				`<m:deg>` + mrun("3") + `</m:deg><m:e>` + mrun("x") + `</m:e>` +
				`</m:rad></m:oMath>`,
			want: "\\sqrt[3]{x}",
		},
		{
			name: "radical without degree",
			xml: `<m:oMath ` + mXMLNS + `><m:rad><m:radPr><m:degHide m:val="1"/></m:radPr>` +
				`<m:deg/><m:e>` + mrun("x") + `</m:e>` +
				`</m:rad></m:oMath>`,
			want: "\\sqrt{x}",
		},
		{
			name: "nary sum",
			xml: `<m:oMath ` + mXMLNS + `><m:nary><m:naryPr><m:chr m:val="∑"/></m:naryPr>` +
				`<m:sub>` + mrun("i=1") + `</m:sub><m:sup>` + mrun("n") + `</m:sup>` +
				`<m:e>` + mrun("i") + `</m:e>` +
				`</m:nary></m:oMath>`,
			want: "\\sum_{i=1}^{n}i",
		},
		{
			// Without limits to close it, the operator command would run into
			// the operand and form an undefined control sequence ("\intx").
			name: "nary without limits separates operator from operand",
			xml: `<m:oMath ` + mXMLNS + `><m:nary><m:naryPr><m:chr m:val="∫"/></m:naryPr>` +
				`<m:e>` + mrun("x") + `</m:e></m:nary></m:oMath>`,
			want: "\\int x",
		},
		{
			name: "nary with both limits hidden separates operator from operand",
			xml: `<m:oMath ` + mXMLNS + `><m:nary>` +
				`<m:naryPr><m:chr m:val="∑"/><m:subHide m:val="1"/><m:supHide m:val="1"/></m:naryPr>` +
				`<m:sub>` + mrun("i") + `</m:sub><m:sup>` + mrun("n") + `</m:sup>` +
				`<m:e>` + mrun("a") + `</m:e></m:nary></m:oMath>`,
			want: "\\sum a",
		},
		{
			// naryOp passes an unmapped character through verbatim; it is not
			// a command, so it needs no separator.
			name: "nary with literal operator character needs no separator",
			xml: `<m:oMath ` + mXMLNS + `><m:nary><m:naryPr><m:chr m:val="⨄"/></m:naryPr>` +
				`<m:e>` + mrun("x") + `</m:e></m:nary></m:oMath>`,
			want: "⨄x",
		},
		{
			name: "delimiter parens",
			xml: `<m:oMath ` + mXMLNS + `><m:d>` +
				`<m:e>` + mrun("x") + `</m:e>` +
				`</m:d></m:oMath>`,
			want: "\\left(x\\right)",
		},
		{
			name: "delimiter custom bars",
			xml: `<m:oMath ` + mXMLNS + `><m:d>` +
				`<m:dPr><m:begChr m:val="|"/><m:endChr m:val="|"/></m:dPr>` +
				`<m:e>` + mrun("x") + `</m:e></m:d></m:oMath>`,
			want: "\\left|x\\right|",
		},
		{
			name: "delimiter multi-element uses default separator",
			xml: `<m:oMath ` + mXMLNS + `><m:d>` +
				`<m:e>` + mrun("a") + `</m:e><m:e>` + mrun("b") + `</m:e>` +
				`</m:d></m:oMath>`,
			want: "\\left(a|b\\right)",
		},
		{
			name: "function with known name",
			xml: `<m:oMath ` + mXMLNS + `><m:func>` +
				`<m:fName>` + mrun("sin") + `</m:fName>` +
				`<m:e>` + mrun("x") + `</m:e></m:func></m:oMath>`,
			want: "\\sin x",
		},
		{
			name: "function with unknown name",
			xml: `<m:oMath ` + mXMLNS + `><m:func>` +
				`<m:fName>` + mrun("erf") + `</m:fName>` +
				`<m:e>` + mrun("x") + `</m:e></m:func></m:oMath>`,
			want: "\\operatorname{erf} x",
		},
		{
			name: "accent hat",
			xml: `<m:oMath ` + mXMLNS + `><m:acc>` +
				`<m:accPr><m:chr m:val="^"/></m:accPr>` +
				`<m:e>` + mrun("x") + `</m:e></m:acc></m:oMath>`,
			want: "\\hat{x}",
		},
		{
			name: "accent default bar",
			xml: `<m:oMath ` + mXMLNS + `><m:acc>` +
				`<m:e>` + mrun("y") + `</m:e></m:acc></m:oMath>`,
			want: "\\bar{y}",
		},
		{
			name: "nary product default int when no chr",
			xml: `<m:oMath ` + mXMLNS + `><m:nary>` +
				`<m:sub>` + mrun("a") + `</m:sub><m:sup>` + mrun("b") + `</m:sup>` +
				`<m:e>` + mrun("f") + `</m:e></m:nary></m:oMath>`,
			want: "\\int_{a}^{b}f",
		},
		{
			name: "nary product with supHide",
			xml: `<m:oMath ` + mXMLNS + `><m:nary>` +
				`<m:naryPr><m:chr m:val="∏"/><m:supHide m:val="1"/></m:naryPr>` +
				`<m:sub>` + mrun("k") + `</m:sub><m:sup>` + mrun("n") + `</m:sup>` +
				`<m:e>` + mrun("k") + `</m:e></m:nary></m:oMath>`,
			want: "\\prod_{k}k",
		},
		{
			name: "nested fraction over subscript",
			xml: `<m:oMath ` + mXMLNS + `><m:f><m:num><m:sSub>` +
				`<m:e>` + mrun("n") + `</m:e><m:sub>` + mrun("78") + `</m:sub>` +
				`</m:sSub></m:num><m:den>` + mrun("2") + `</m:den></m:f></m:oMath>`,
			want: "\\frac{{n}_{78}}{2}",
		},
		{
			name: "unknown element falls back to inner text",
			xml:  `<m:oMath ` + mXMLNS + `><m:xyz>` + mrun("q") + `</m:xyz></m:oMath>`,
			want: "q",
		},
		{
			name: "empty oMath",
			xml:  `<m:oMath ` + mXMLNS + `></m:oMath>`,
			want: "",
		},
		{
			name: "oMathPara with single equation renders unwrapped",
			xml: `<m:oMathPara ` + mXMLNS + `><m:oMath>` + mrun("x=1") +
				`</m:oMath></m:oMathPara>`,
			want: "x=1",
		},
		{
			name: "oMathPara with multiple equations separates each line",
			xml: `<m:oMathPara ` + mXMLNS + `>` +
				`<m:oMath>` + mrun("a=1") + `</m:oMath>` +
				`<m:oMath>` + mrun("b=2") + `</m:oMath>` +
				`<m:oMath>` + mrun("c=3") + `</m:oMath>` +
				`</m:oMathPara>`,
			want: "\\begin{gathered} a=1 \\\\ b=2 \\\\ c=3 \\end{gathered}",
		},
		{
			// Math mode drops ordinary spaces, so "n mod 2" would otherwise
			// render as "nmod2" (issue #140).
			name: "spaces inside a run survive math mode",
			xml:  `<m:oMath ` + mXMLNS + `>` + mrun("n mod 2") + `</m:oMath>`,
			want: "n\\text{ }mod\\text{ }2",
		},
		{
			name: "padding around a formula is trimmed, not protected",
			xml:  `<m:oMath ` + mXMLNS + `>` + mrun(" x+1 ") + `</m:oMath>`,
			want: "x+1",
		},
		{
			// A padded function name still has to match the operator table.
			name: "function name with surrounding spaces",
			xml: `<m:oMath ` + mXMLNS + `><m:func>` +
				`<m:fName>` + mrun(" cos ") + `</m:fName>` +
				`<m:e>` + mrun("x") + `</m:e></m:func></m:oMath>`,
			want: "\\cos x",
		},
		{
			name: "radical with blank degree hides the degree",
			xml: `<m:oMath ` + mXMLNS + `><m:rad>` +
				`<m:deg>` + mrun(" ") + `</m:deg><m:e>` + mrun("x") + `</m:e>` +
				`</m:rad></m:oMath>`,
			want: "\\sqrt{x}",
		},
		{
			name: "greek and relation symbols",
			xml:  `<m:oMath ` + mXMLNS + `>` + mrun("α≤β") + `</m:oMath>`,
			want: "\\alpha \\leq \\beta",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ommlLaTeX(t, tt.xml)
			if got != tt.want {
				t.Errorf("ommlToLaTeX =\n  %q\nwant\n  %q", got, tt.want)
			}
		})
	}
}

func TestEscapeMathText(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"a_b", "a\\_b"},
		{"100%", "100\\%"},
		{"x&y", "x\\&y"},
		{"−5", "-5"}, // U+2212 minus → ASCII hyphen
		{"plain", "plain"},
		{"#1", "\\#1"},
		{`a\b`, "a\\backslash b"},
		{"{x}", "\\{x\\}"},
		// Callers wrap the result in $...$, so the text-mode escapes for "~"
		// and "^" need their own \text{} group; bare \textasciitilde is an
		// undefined control sequence in math mode.
		{"a~b", "a\\text{\\textasciitilde}b"},
		{"a^b", "a\\text{\\textasciicircum}b"},
		{"$x", "\\$x"},
		{"β≥γ", "\\beta \\geq \\gamma "},
		// Spaces need the same treatment: math mode would collapse them and
		// turn "n mod 2" into "nmod2".
		{"n mod 2", "n\\text{ }mod\\text{ }2"},
		{"a  b", "a\\text{ }\\text{ }b"},
		{"a×b", "a\\times b"},
		{"a∗b", "a*b"}, // U+2217 asterisk operator → ASCII "*"
	}
	for _, tt := range tests {
		if got := escapeMathText(tt.in); got != tt.want {
			t.Errorf("escapeMathText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestParseOMMLNode_DepthCap verifies that absurdly nested OMML cannot
// exhaust the stack: children past the cap are consumed but not retained.
func TestParseOMMLNode_DepthCap(t *testing.T) {
	depth := maxOMMLDepth + 50
	var sb strings.Builder
	sb.WriteString(`<m:oMath xmlns:m="http://schemas.openxmlformats.org/officeDocument/2006/math">`)
	for range depth {
		sb.WriteString("<m:e>")
	}
	sb.WriteString(`<m:t>x</m:t>`)
	for range depth {
		sb.WriteString("</m:e>")
	}
	sb.WriteString(`</m:oMath>`)

	d := xml.NewDecoder(strings.NewReader(sb.String()))
	tok, err := d.Token()
	if err != nil {
		t.Fatal(err)
	}
	start := tok.(xml.StartElement)
	// Must terminate without a stack overflow and leave the decoder balanced.
	_ = ommlToLaTeX(d, start)
	if _, err := d.Token(); err == nil {
		t.Error("expected the decoder to be fully consumed")
	}
}
