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
		{"a~b", "a\\textasciitilde b"},
		{"a^b", "a\\textasciicircum b"},
		{"$x", "\\$x"},
		{"β≥γ", "\\beta \\geq \\gamma "},
		{"a×b", "a\\times b"},
	}
	for _, tt := range tests {
		if got := escapeMathText(tt.in); got != tt.want {
			t.Errorf("escapeMathText(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
