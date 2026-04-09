package docx

import (
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

func TestParseParagraph_TabBetweenText(t *testing.T) {
	xml := `<w:p ` + wXMLNS + `>` +
		`<w:r><w:t>a</w:t><w:tab/><w:t>b</w:t></w:r>` +
		`</w:p>`
	info := parseParagraph([]byte(xml))
	if info.Text != "a\tb" {
		t.Errorf("Text = %q, want %q", info.Text, "a\tb")
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
			name:      "empty runs falls back to plain text",
			info:      paragraphInfo{Text: "fallback"},
			styleName: "Normal",
			want:      "fallback",
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
