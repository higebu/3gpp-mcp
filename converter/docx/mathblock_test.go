package docx

import (
	"strings"
	"testing"
)

// mathPara builds a paragraph carrying one formula, with the given runs
// before and after it, and fills Text the way parseParagraph would.
func mathPara(styleID, latex string, display bool, before, after []string) paragraphInfo {
	info := paragraphInfo{StyleID: styleID}
	for _, t := range before {
		info.Runs = append(info.Runs, runInfo{Text: t})
	}
	info.Runs = append(info.Runs, runInfo{Text: latex, Math: true, MathDisplay: display})
	for _, t := range after {
		info.Runs = append(info.Runs, runInfo{Text: t})
	}
	var sb strings.Builder
	for _, r := range info.Runs {
		sb.WriteString(r.markdownText())
	}
	info.Text = sb.String()
	return info
}

func TestMathFenceBody(t *testing.T) {
	tests := []struct {
		name    string
		info    paragraphInfo
		style   string
		want    string
		wantOK  bool
		comment string
	}{
		{
			name:   "display math alone",
			info:   mathPara("", `\frac{1}{2}`, true, nil, nil),
			want:   `\frac{1}{2}`,
			wantOK: true,
		},
		{
			name:   "inline math alone",
			info:   mathPara("", `x=y`, false, nil, nil),
			want:   `x=y`,
			wantOK: true,
		},
		{
			name:   "centering tab before the formula",
			info:   mathPara("EQ", `x=y`, false, []string{"\t"}, nil),
			style:  "EQ",
			want:   `x=y`,
			wantOK: true,
		},
		{
			name:   "equation number after a tab",
			info:   mathPara("EQ", `x=y`, false, []string{"\t"}, []string{"\t", "(7.3-1)"}),
			style:  "EQ",
			want:   `x=y \tag{7.3-1}`,
			wantOK: true,
		},
		{
			name:   "punctuation then equation number",
			info:   mathPara("EQ", `x=y`, false, nil, []string{",", "\t", "(7.3-3a)"}),
			style:  "EQ",
			want:   `x=y, \tag{7.3-3a}`,
			wantOK: true,
		},
		{
			name:   "trailing punctuation only",
			info:   mathPara("", `x=y`, false, nil, []string{"."}),
			want:   `x=y.`,
			wantOK: true,
		},
		{
			name:   "lettered annex equation number",
			info:   mathPara("EQ", `x=y`, false, nil, []string{"\t(A.4-2)"}),
			style:  "EQ",
			want:   `x=y \tag{A.4-2}`,
			wantOK: true,
		},
		{
			name:   "non-breaking space before the formula",
			info:   mathPara("", `x=y`, false, []string{"\u00a0 "}, nil),
			want:   `x=y`,
			wantOK: true,
		},
		{
			name:    "prose before the formula",
			info:    mathPara("", `x=y`, false, []string{"where "}, nil),
			comment: "a formula in a sentence stays inline",
		},
		{
			name:    "prose after the formula",
			info:    mathPara("", `x=y`, false, nil, []string{" applies."}),
			comment: "a trailing clause is not an equation number",
		},
		{
			name:    "parenthesised aside is not an equation number",
			info:    mathPara("", `x=y`, false, nil, []string{"\t(see below)"}),
			comment: "an equation number is a single token",
		},
		{
			name:    "trailing unit is not an equation number",
			info:    mathPara("", `x=y`, false, nil, []string{"\t(dB)"}),
			comment: "an equation number carries a . or - separator",
		},
		{
			name:    "list marker is not an equation number",
			info:    mathPara("", `x=y`, false, nil, []string{"\t(i)"}),
			comment: "an equation number carries a . or - separator",
		},
		{
			name:    "abbreviation is not an equation number",
			info:    mathPara("", `x=y`, false, nil, []string{"\t(i.e)"}),
			comment: "an equation number contains a digit",
		},
		{
			name:   "multi-level clause equation number",
			info:   mathPara("EQ", `x=y`, false, nil, []string{"\t(5.2.3-4)"}),
			style:  "EQ",
			want:   `x=y \tag{5.2.3-4}`,
			wantOK: true,
		},
		{
			// TR 38.901 clause 7.6.9 misprints this one; it is an equation
			// number all the same.
			name:   "misprinted equation number",
			info:   mathPara("EQ", `x=y`, false, nil, []string{"\t(7.6.-43)"}),
			style:  "EQ",
			want:   `x=y \tag{7.6.-43}`,
			wantOK: true,
		},
		{
			name:  "list item",
			info:  mathPara("", `x=y`, true, nil, nil),
			style: "List Bullet",
		},
		{
			name:  "code style",
			info:  mathPara("", `x=y`, true, nil, nil),
			style: "Macro",
		},
		{
			name: "two formulas",
			info: paragraphInfo{Runs: []runInfo{
				{Text: `x=y`, Math: true},
				{Text: `a=b`, Math: true},
			}},
			comment: "two equations in one paragraph are not one block",
		},
		{
			name: "no math at all",
			info: paragraphInfo{Text: "plain", Runs: []runInfo{{Text: "plain"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := mathFenceBody(tt.info, tt.style)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (%s)", ok, tt.wantOK, tt.comment)
			}
			if got != tt.want {
				t.Errorf("body = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMathFenceBody_ImageKeepsParagraphInline(t *testing.T) {
	info := mathPara("", `x=y`, true, nil, nil)
	info.Images = []imageRef{{RID: "rId9"}}
	if _, ok := mathFenceBody(info, ""); ok {
		t.Error("a paragraph carrying an image must not become a fence: the image would be dropped")
	}

	inline := mathPara("", `x=y`, true, nil, nil)
	inline.Runs = append(inline.Runs, runInfo{Image: &imageRef{RID: "rId9"}})
	if _, ok := mathFenceBody(inline, ""); ok {
		t.Error("an inline image run must not become a fence")
	}
}

func TestParseSections_StandaloneEquationsBecomeLatexFences(t *testing.T) {
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "7 Channel models",
			Runs: []runInfo{{Text: "7 Channel models"}},
		}},
		{Tag: "p", Paragraph: mathPara("", `PL=32.4`, false, []string{"\t"}, []string{"\t", "(7.3-1)"})},
		{Tag: "p", Paragraph: mathPara("", `SF=0`, true, nil, nil)},
		{Tag: "p", Paragraph: mathPara("", `d_{2D}`, false, []string{"where "}, []string{" is the distance."})},
	}
	styleMap := map[string]string{"Heading1": "Heading 1"}
	sections := parseSections(elements, styleMap, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("sections = %d, want 1", len(sections))
	}
	content := sections[0].Content

	want := []string{
		"```latex\nPL=32.4 \\tag{7.3-1}\n```",
		"```latex\nSF=0\n```",
		"where $d_{2D}$ is the distance.",
	}
	if len(content) != len(want) {
		t.Fatalf("content = %d entries, want %d:\n%v", len(content), len(want), content)
	}
	for i, w := range want {
		if content[i] != w {
			t.Errorf("content[%d] = %q, want %q", i, content[i], w)
		}
	}
}

// An equation inside a code listing belongs to the listing: the capture
// states are consulted before the promotion.
func TestParseSections_EquationInsideCodeBlockStaysInline(t *testing.T) {
	code := mathPara("", `x=y`, true, nil, nil)
	code.IsCode = true
	for i := range code.Runs {
		code.Runs[i].IsCode = true
	}
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "1 Test",
			Runs: []runInfo{{Text: "1 Test"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "value:", IsCode: true,
			Runs: []runInfo{{Text: "value:", IsCode: true}},
		}},
		{Tag: "p", Paragraph: code},
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	all := strings.Join(sections[0].Content, "\n")
	if strings.Contains(all, "```latex") {
		t.Errorf("equation inside a code listing was promoted:\n%s", all)
	}
	if !strings.Contains(all, "$$x=y$$") {
		t.Errorf("equation missing from the code listing:\n%s", all)
	}
}
