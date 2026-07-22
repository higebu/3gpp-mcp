package docx

import (
	"strings"
	"testing"
)

const wXMLNS = `xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"`

func TestParseParagraph_PlainText(t *testing.T) {
	xml := `<w:p ` + wXMLNS + `><w:r><w:t>hello world</w:t></w:r></w:p>`
	info := parseParagraph([]byte(xml))
	if info.Text != "hello world" {
		t.Errorf("Text = %q, want %q", info.Text, "hello world")
	}
	if len(info.Runs) != 1 {
		t.Fatalf("Runs = %d, want 1", len(info.Runs))
	}
	if info.Runs[0].Bold || info.Runs[0].Italic {
		t.Errorf("expected non-formatted run, got bold=%v italic=%v", info.Runs[0].Bold, info.Runs[0].Italic)
	}
	if info.IsCode {
		t.Error("expected IsCode=false for plain run")
	}
}

func TestParseParagraph_BoldItalicCombinations(t *testing.T) {
	xml := `<w:p ` + wXMLNS + `>` +
		`<w:r><w:rPr><w:b/></w:rPr><w:t>bold </w:t></w:r>` +
		`<w:r><w:rPr><w:i/></w:rPr><w:t>italic </w:t></w:r>` +
		`<w:r><w:rPr><w:b/><w:i/></w:rPr><w:t>both</w:t></w:r>` +
		`</w:p>`
	info := parseParagraph([]byte(xml))
	if len(info.Runs) != 3 {
		t.Fatalf("Runs = %d, want 3", len(info.Runs))
	}
	if !info.Runs[0].Bold || info.Runs[0].Italic {
		t.Errorf("run[0]: want bold only, got bold=%v italic=%v", info.Runs[0].Bold, info.Runs[0].Italic)
	}
	if info.Runs[1].Bold || !info.Runs[1].Italic {
		t.Errorf("run[1]: want italic only, got bold=%v italic=%v", info.Runs[1].Bold, info.Runs[1].Italic)
	}
	if !info.Runs[2].Bold || !info.Runs[2].Italic {
		t.Errorf("run[2]: want both, got bold=%v italic=%v", info.Runs[2].Bold, info.Runs[2].Italic)
	}
	if info.Text != "bold italic both" {
		t.Errorf("Text = %q, want %q", info.Text, "bold italic both")
	}
}

func TestParseParagraph_BoldFalseAttr(t *testing.T) {
	xml := `<w:p ` + wXMLNS + `>` +
		`<w:r><w:rPr><w:b w:val="false"/></w:rPr><w:t>not bold</w:t></w:r>` +
		`</w:p>`
	info := parseParagraph([]byte(xml))
	if len(info.Runs) != 1 {
		t.Fatalf("Runs = %d, want 1", len(info.Runs))
	}
	if info.Runs[0].Bold {
		t.Error("expected w:b val=false to disable bold")
	}
}

func TestParseParagraph_ItalicFalseAttr(t *testing.T) {
	xml := `<w:p ` + wXMLNS + `>` +
		`<w:r><w:rPr><w:i w:val="0"/></w:rPr><w:t>not italic</w:t></w:r>` +
		`</w:p>`
	info := parseParagraph([]byte(xml))
	if info.Runs[0].Italic {
		t.Error("expected w:i val=0 to disable italic")
	}
}

func TestParseParagraph_VertAlign(t *testing.T) {
	xml := `<w:p ` + wXMLNS + `>` +
		`<w:r><w:t>n_78</w:t></w:r>` +
		`<w:r><w:rPr><w:vertAlign w:val="superscript"/></w:rPr><w:t>1</w:t></w:r>` +
		`<w:r><w:rPr><w:vertAlign w:val="subscript"/></w:rPr><w:t>2</w:t></w:r>` +
		`</w:p>`
	info := parseParagraph([]byte(xml))
	if len(info.Runs) != 3 {
		t.Fatalf("Runs = %d, want 3", len(info.Runs))
	}
	if info.Runs[0].VertAlign != "" {
		t.Errorf("run[0]: VertAlign = %q, want empty", info.Runs[0].VertAlign)
	}
	if info.Runs[1].VertAlign != "superscript" {
		t.Errorf("run[1]: VertAlign = %q, want superscript", info.Runs[1].VertAlign)
	}
	if info.Runs[2].VertAlign != "subscript" {
		t.Errorf("run[2]: VertAlign = %q, want subscript", info.Runs[2].VertAlign)
	}
}

func TestParseParagraph_TabBetweenText(t *testing.T) {
	xml := `<w:p ` + wXMLNS + `>` +
		`<w:r><w:t>a</w:t><w:tab/><w:t>b</w:t></w:r>` +
		`</w:p>`
	info := parseParagraph([]byte(xml))
	if info.Text != "a\tb" {
		t.Errorf("Text = %q, want %q", info.Text, "a\tb")
	}
}

func TestParseParagraph_InlineMath(t *testing.T) {
	xml := `<w:p ` + wXMLNS + ` ` + mXMLNS + `>` +
		`<w:r><w:t>value </w:t></w:r>` +
		`<m:oMath><m:sSub>` +
		`<m:e><m:r><m:t>n</m:t></m:r></m:e>` +
		`<m:sub><m:r><m:t>78</m:t></m:r></m:sub>` +
		`</m:sSub></m:oMath>` +
		`</w:p>`
	info := parseParagraph([]byte(xml))
	if len(info.Runs) != 2 {
		t.Fatalf("Runs = %d, want 2 (%+v)", len(info.Runs), info.Runs)
	}
	if info.Runs[0].Text != "value " {
		t.Errorf("run[0].Text = %q, want %q", info.Runs[0].Text, "value ")
	}
	if info.Runs[1].Text != "${n}_{78}$" {
		t.Errorf("run[1].Text = %q, want %q", info.Runs[1].Text, "${n}_{78}$")
	}
	// The math m:t must not leak as its own plain run.
	for _, r := range info.Runs {
		if r.Text == "n" || r.Text == "78" || r.Text == "n78" {
			t.Errorf("math text leaked as plain run: %q", r.Text)
		}
	}
}

func TestParseParagraph_DisplayMath(t *testing.T) {
	xml := `<w:p ` + wXMLNS + ` ` + mXMLNS + `>` +
		`<m:oMathPara><m:oMath>` +
		`<m:f><m:num><m:r><m:t>1</m:t></m:r></m:num>` +
		`<m:den><m:r><m:t>2</m:t></m:r></m:den></m:f>` +
		`</m:oMath></m:oMathPara>` +
		`</w:p>`
	info := parseParagraph([]byte(xml))
	if len(info.Runs) != 1 {
		t.Fatalf("Runs = %d, want 1 (%+v)", len(info.Runs), info.Runs)
	}
	if info.Runs[0].Text != "$$\\frac{1}{2}$$" {
		t.Errorf("run[0].Text = %q, want %q", info.Runs[0].Text, "$$\\frac{1}{2}$$")
	}
}

func TestParseParagraph_EmptyOnInvalidInput(t *testing.T) {
	info := parseParagraph([]byte("not xml"))
	if info.Text != "" || len(info.Runs) != 0 {
		t.Errorf("expected empty info on invalid input, got %+v", info)
	}
}

func TestParseParagraph_NoRunsEmptyText(t *testing.T) {
	xml := `<w:p ` + wXMLNS + `><w:pPr><w:pStyle w:val="Normal"/></w:pPr></w:p>`
	info := parseParagraph([]byte(xml))
	if info.Text != "" {
		t.Errorf("Text = %q, want empty", info.Text)
	}
	if info.StyleID != "Normal" {
		t.Errorf("StyleID = %q, want Normal", info.StyleID)
	}
}

func TestParagraphToMarkdown(t *testing.T) {
	tests := []struct {
		name      string
		info      paragraphInfo
		styleName string
		want      string
	}{
		{
			name:      "blank text returns empty",
			info:      paragraphInfo{Text: "  "},
			styleName: "Normal",
			want:      "",
		},
		{
			name:      "plain run",
			info:      paragraphInfo{Text: "hello", Runs: []runInfo{{Text: "hello"}}},
			styleName: "Normal",
			want:      "hello",
		},
		{
			name: "mixed bold and italic runs",
			info: paragraphInfo{
				Text: "Bold and italic",
				Runs: []runInfo{
					{Text: "Bold", Bold: true},
					{Text: " and "},
					{Text: "italic", Italic: true},
				},
			},
			styleName: "Normal",
			want:      "**Bold** and *italic*",
		},
		{
			name: "bold italic combined",
			info: paragraphInfo{
				Text: "both",
				Runs: []runInfo{{Text: "both", Bold: true, Italic: true}},
			},
			styleName: "Normal",
			want:      "***both***",
		},
		{
			name: "superscript note mark stays separate",
			info: paragraphInfo{
				Text: "n_781",
				Runs: []runInfo{
					{Text: "n_78"},
					{Text: "1", VertAlign: "superscript"},
				},
			},
			styleName: "Normal",
			want:      "n_78<sup>1</sup>",
		},
		{
			name: "subscript run",
			info: paragraphInfo{
				Text: "H2O",
				Runs: []runInfo{
					{Text: "H"},
					{Text: "2", VertAlign: "subscript"},
					{Text: "O"},
				},
			},
			styleName: "Normal",
			want:      "H<sub>2</sub>O",
		},
		{
			name: "bold superscript combined",
			info: paragraphInfo{
				Text: "x2",
				Runs: []runInfo{
					{Text: "x"},
					{Text: "2", Bold: true, VertAlign: "superscript"},
				},
			},
			styleName: "Normal",
			want:      "x<sup>**2**</sup>",
		},
		{
			name:      "list bullet style",
			info:      paragraphInfo{Text: "item", Runs: []runInfo{{Text: "item"}}},
			styleName: "ListBullet",
			want:      "- item",
		},
		{
			name:      "list number style",
			info:      paragraphInfo{Text: "first", Runs: []runInfo{{Text: "first"}}},
			styleName: "ListNumber",
			want:      "1. first",
		},
		{
			name:      "unknown list style defaults to bullet",
			info:      paragraphInfo{Text: "x", Runs: []runInfo{{Text: "x"}}},
			styleName: "ListSomething",
			want:      "- x",
		},
		{
			name:      "nested list bullet level 2 style is indented",
			info:      paragraphInfo{Text: "item", Runs: []runInfo{{Text: "item"}}},
			styleName: "List Bullet 2",
			want:      "    - item",
		},
		{
			name:      "nested list bullet level 3 style is indented",
			info:      paragraphInfo{Text: "item", Runs: []runInfo{{Text: "item"}}},
			styleName: "List Bullet 3",
			want:      "        - item",
		},
		{
			name:      "nested list number level 2 style is indented",
			info:      paragraphInfo{Text: "first", Runs: []runInfo{{Text: "first"}}},
			styleName: "List Number 2",
			want:      "    1. first",
		},
		{
			name:      "nested list style without space before level digit is indented",
			info:      paragraphInfo{Text: "item", Runs: []runInfo{{Text: "item"}}},
			styleName: "ListBullet2",
			want:      "    - item",
		},
		{
			name:      "empty runs falls back to plain text",
			info:      paragraphInfo{Text: "fallback"},
			styleName: "Normal",
			want:      "fallback",
		},
		{
			name: "word split across adjacent italic runs merges into one emphasis",
			info: paragraphInfo{
				Text: "mpe-Reporting-FR2",
				Runs: []runInfo{
					{Text: "mpe-Reporting", Italic: true},
					{Text: "-FR2", Italic: true},
				},
			},
			styleName: "Normal",
			want:      "*mpe-Reporting-FR2*",
		},
		{
			name: "italic fragment split across runs surrounded by plain text",
			info: paragraphInfo{
				Text: "preAABBpost",
				Runs: []runInfo{
					{Text: "pre"},
					{Text: "AA", Italic: true},
					{Text: "BB", Italic: true},
					{Text: "post"},
				},
			},
			styleName: "Normal",
			want:      "pre*AABB*post",
		},
		{
			name: "adjacent bold+italic runs merge into one wrapper",
			info: paragraphInfo{
				Text: "ab",
				Runs: []runInfo{
					{Text: "a", Bold: true, Italic: true},
					{Text: "b", Bold: true, Italic: true},
				},
			},
			styleName: "Normal",
			want:      "***ab***",
		},
		{
			name: "adjacent same-superscript runs merge into one tag",
			info: paragraphInfo{
				Text: "ab",
				Runs: []runInfo{
					{Text: "a", VertAlign: "superscript"},
					{Text: "b", VertAlign: "superscript"},
				},
			},
			styleName: "Normal",
			want:      "<sup>ab</sup>",
		},
		{
			name: "empty run between same-styled italic runs still merges",
			info: paragraphInfo{
				Text: "ab",
				Runs: []runInfo{
					{Text: "a", Italic: true},
					{Text: ""},
					{Text: "b", Italic: true},
				},
			},
			styleName: "Normal",
			want:      "*ab*",
		},
		{
			name: "adjacent runs differing only in VertAlign do not merge",
			info: paragraphInfo{
				Text: "ab",
				Runs: []runInfo{
					{Text: "a", VertAlign: "superscript"},
					{Text: "b", VertAlign: "subscript"},
				},
			},
			styleName: "Normal",
			want:      "<sup>a</sup><sub>b</sub>",
		},
		{
			name: "italic run with trailing space keeps space outside the delimiter",
			info: paragraphInfo{
				Text: "term [15]",
				Runs: []runInfo{
					{Text: "term ", Italic: true},
					{Text: "[15]"},
				},
			},
			styleName: "Normal",
			want:      "*term* [15]",
		},
		{
			name: "italic run with leading space keeps space outside the delimiter",
			info: paragraphInfo{
				Text: " term",
				Runs: []runInfo{
					{Text: "pre"},
					{Text: " term", Italic: true},
				},
			},
			styleName: "Normal",
			want:      "pre *term*",
		},
		{
			name: "whitespace-only italic run is left unwrapped",
			info: paragraphInfo{
				Text: "a b",
				Runs: []runInfo{
					{Text: "a"},
					{Text: " ", Italic: true},
					{Text: "b"},
				},
			},
			styleName: "Normal",
			want:      "a b",
		},
		{
			name: "bold run with trailing space keeps space outside the delimiter",
			info: paragraphInfo{
				Text: "label: value",
				Runs: []runInfo{
					{Text: "label: ", Bold: true},
					{Text: "value"},
				},
			},
			styleName: "Normal",
			want:      "**label:** value",
		},
		{
			name: "bold run with leading space keeps space outside the delimiter",
			info: paragraphInfo{
				Text: "pre label",
				Runs: []runInfo{
					{Text: "pre"},
					{Text: " label", Bold: true},
				},
			},
			styleName: "Normal",
			want:      "pre **label**",
		},
		{
			name: "bold-italic run with trailing space keeps space outside the delimiter",
			info: paragraphInfo{
				Text: "term [1]",
				Runs: []runInfo{
					{Text: "term ", Bold: true, Italic: true},
					{Text: "[1]"},
				},
			},
			styleName: "Normal",
			want:      "***term*** [1]",
		},
		{
			name: "bold-italic run with leading and trailing space keeps spaces outside the delimiter",
			info: paragraphInfo{
				Text: "pre term post",
				Runs: []runInfo{
					{Text: "pre"},
					{Text: " term ", Bold: true, Italic: true},
					{Text: "post"},
				},
			},
			styleName: "Normal",
			want:      "pre ***term*** post",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := paragraphToMarkdown(tt.info, tt.styleName)
			if got != tt.want {
				t.Errorf("paragraphToMarkdown = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsMonospaceFont(t *testing.T) {
	cases := map[string]bool{
		"Courier New":     true,
		"Courier":         true,
		"Consolas":        true,
		"Lucida Console":  true,
		"Monaco":          true,
		"Menlo":           true,
		"Times New Roman": false,
		"Arial":           false,
		"":                false,
	}
	for font, want := range cases {
		if got := isMonospaceFont(font); got != want {
			t.Errorf("isMonospaceFont(%q) = %v, want %v", font, got, want)
		}
	}
}

func TestListStyleLevel(t *testing.T) {
	cases := map[string]int{
		"List Bullet":   1,
		"List Bullet 2": 2,
		"List Bullet 3": 3,
		"List Number":   1,
		"List Number 2": 2,
		"ListBullet":    1,
		"ListBullet2":   2,
		"ListNumber3":   3,
		"List":          1,
	}
	for styleName, want := range cases {
		if got := listStyleLevel(styleName); got != want {
			t.Errorf("listStyleLevel(%q) = %d, want %d", styleName, got, want)
		}
	}
}

func TestParagraphToMarkdown_LeadingTabTrimmed(t *testing.T) {
	// A leading <w:tab/> (e.g. used to center an equation via a paragraph
	// style's tab stops) must not survive into the returned markdown: a
	// line starting with a tab is parsed as an indented code block by
	// CommonMark, inside which raw HTML tags like <sub> are never
	// interpreted and show up as literal text (issue #25).
	info := paragraphInfo{
		Text: "\tBW= F",
		Runs: []runInfo{
			{Text: "\t"},
			{Text: "BW"},
			{Text: "Channel_CA", VertAlign: "subscript"},
			{Text: " ", VertAlign: "subscript"},
			{Text: "= F"},
		},
	}
	got := paragraphToMarkdown(info, "Normal")
	want := "BW<sub>Channel_CA </sub>= F"
	if got != want {
		t.Errorf("paragraphToMarkdown = %q, want %q", got, want)
	}
	if strings.HasPrefix(got, "\t") || strings.HasPrefix(got, " ") {
		t.Errorf("expected no leading whitespace, got %q", got)
	}
}

func TestParseParagraph_GroupShapeNoImage_SkipsLabels(t *testing.T) {
	// A grouped VML diagram (v:group containing several v:shape/v:textbox
	// labels) with no embedded picture anywhere inside it must not have its
	// labels flattened into the paragraph's own text/runs.
	xml := `<w:p ` + wXMLNS + `>` +
		`<w:r><w:pict><group>` +
		`<shape><textbox><txbxContent><w:p><w:r><w:t>Lower Edge</w:t></w:r></w:p></txbxContent></textbox></shape>` +
		`<shape><textbox><txbxContent><w:p><w:r><w:t>Resource block</w:t></w:r></w:p></txbxContent></textbox></shape>` +
		`</group></w:pict></w:r>` +
		`</w:p>`
	info := parseParagraph([]byte(xml))
	if strings.Contains(info.Text, "Lower Edge") || strings.Contains(info.Text, "Resource block") {
		t.Errorf("expected group-shape labels excluded from Text, got %q", info.Text)
	}
	if len(info.Images) != 0 {
		t.Errorf("expected no images, got %d", len(info.Images))
	}
	if len(info.SkippedDiagramLabels) != 2 {
		t.Fatalf("SkippedDiagramLabels = %v, want 2 entries", info.SkippedDiagramLabels)
	}
	if info.SkippedDiagramLabels[0] != "Lower Edge" || info.SkippedDiagramLabels[1] != "Resource block" {
		t.Errorf("SkippedDiagramLabels = %v, want [Lower Edge, Resource block]", info.SkippedDiagramLabels)
	}
}

func TestParseParagraph_GroupShapeWithImage_KeepsImageDropsLabels(t *testing.T) {
	// A group that mixes a raster picture with textbox callouts should keep
	// extracting the image (existing behavior), and should not classify the
	// group as an unrenderable diagram.
	xml := `<w:p ` + wXMLNS + `>` +
		`<w:r><w:pict><group>` +
		`<shape><imagedata r:id="rId9"/></shape>` +
		`<shape><textbox><txbxContent><w:p><w:r><w:t>Callout</w:t></w:r></w:p></txbxContent></textbox></shape>` +
		`</group></w:pict></w:r>` +
		`</w:p>`
	info := parseParagraph([]byte(xml))
	if len(info.Images) != 1 || info.Images[0].RID != "rId9" {
		t.Fatalf("expected 1 image with RID rId9, got %+v", info.Images)
	}
	if info.SkippedDiagramLabels != nil {
		t.Errorf("expected no SkippedDiagramLabels when a raster image is present, got %v", info.SkippedDiagramLabels)
	}
}

func TestParseParagraph_AlternateContentGroupFallback(t *testing.T) {
	// mc:AlternateContent wrapping a DrawingML wpg:wgp Choice and an
	// equivalent VML v:group Fallback must only process the Fallback side —
	// otherwise labels would be duplicated (once per side).
	xml := `<w:p ` + wXMLNS + `>` +
		`<w:r><AlternateContent>` +
		`<Choice><wgp><shape><textbox><txbxContent><w:p><w:r><w:t>DrawingML Label</w:t></w:r></w:p></txbxContent></textbox></shape></wgp></Choice>` +
		`<Fallback><pict><group>` +
		`<shape><textbox><txbxContent><w:p><w:r><w:t>Lower Edge</w:t></w:r></w:p></txbxContent></textbox></shape>` +
		`</group></pict></Fallback>` +
		`</AlternateContent></w:r>` +
		`</w:p>`
	info := parseParagraph([]byte(xml))
	if strings.Contains(info.Text, "DrawingML Label") {
		t.Errorf("expected mc:Choice content to be skipped, got Text = %q", info.Text)
	}
	if len(info.SkippedDiagramLabels) != 1 || info.SkippedDiagramLabels[0] != "Lower Edge" {
		t.Fatalf("SkippedDiagramLabels = %v, want [Lower Edge]", info.SkippedDiagramLabels)
	}
}

func TestParseShapeStyle(t *testing.T) {
	tests := []struct {
		name string
		in   string
		w, h int
	}{
		{"px units", "width:96px;height:48px", 96, 48},
		{"inch units with whitespace", "width: 1in ; height: 0.5in", 96, 48},
		{"only width", "width:100px", 100, 0},
		{"unrelated declaration", "color:red", 0, 0},
		{"empty", "", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, h := parseShapeStyle(tt.in)
			if w != tt.w || h != tt.h {
				t.Errorf("parseShapeStyle(%q) = (%d, %d), want (%d, %d)", tt.in, w, h, tt.w, tt.h)
			}
		})
	}
}
