package docx

import (
	"regexp"
	"strings"
)

// eqNumberRE matches a 3GPP equation number as it appears at the end of an
// equation paragraph: "(7.3-1)", "(7.3-3a)", "(A.4-2)", "(4.1)". The leading
// character is required to be alphanumeric so that a parenthesised aside —
// "(see below)" — is not mistaken for one.
var eqNumberRE = regexp.MustCompile(`^\(\s*[0-9A-Za-z][0-9A-Za-z.\-]*\s*\)$`)

// eqTrailingPunct is the punctuation an equation paragraph may carry between
// the formula and its number, as in "… , (7.3-1)".
const eqTrailingPunct = ",.;:"

// hasMath reports whether the paragraph contains a formula converted from
// OMML. The block detectors use it to keep equations out of content-based
// code-fence capture: a formula is neither XML nor a SIP message, however its
// LaTeX happens to read.
func hasMath(info paragraphInfo) bool {
	for _, r := range info.Runs {
		if r.Math {
			return true
		}
	}
	return false
}

// mathFenceBody reports whether the paragraph is a standalone equation and, if
// so, returns the body of the ```latex fence to emit for it.
//
// The decision is made on the paragraph's shape rather than on m:oMath vs
// m:oMathPara or on the style name, because neither identifies a standalone
// equation in real specifications: in TS 38.211 the great majority of
// m:oMathPara sits inside table cells (which never reach this code, and where
// a fence cannot go anyway), 613 of its 918 m:oMathPara paragraphs carry no
// style at all, and in TS 38.901 the numbered display equations are plain
// m:oMath in a paragraph shaped "<w:tab/> formula <w:tab/> (7.3-1)".
//
// So a paragraph is promoted when it holds exactly one formula, nothing but
// whitespace before it, and nothing after it but optional punctuation and an
// equation number. Anything else — a formula mixed with prose, a formula in a
// list item — stays inline "$...$" and is rendered as part of its paragraph.
func mathFenceBody(info paragraphInfo, styleName string) (string, bool) {
	// A fence cannot live inside a list item, and a paragraph carrying an
	// image or a skipped-diagram placeholder has content the fence would
	// drop.
	if strings.HasPrefix(styleName, "List") || isCodeStyleName(styleName) || info.IsCode {
		return "", false
	}
	if len(info.Images) > 0 || len(info.SkippedDiagramLabels) > 0 {
		return "", false
	}

	var math *runInfo
	var after []string
	for i := range info.Runs {
		r := info.Runs[i]
		if r.Image != nil {
			return "", false
		}
		if r.Math {
			if math != nil {
				return "", false // two formulas: not a single equation
			}
			math = &info.Runs[i]
			continue
		}
		if math == nil {
			// Text before the formula may only be layout: the centering tab
			// of the EQ style, spaces, a non-breaking space.
			if strings.TrimSpace(strings.ReplaceAll(r.Text, " ", " ")) != "" {
				return "", false
			}
			continue
		}
		after = append(after, r.Text)
	}
	if math == nil {
		return "", false
	}

	punct, number, ok := splitEquationTail(strings.Join(after, ""))
	if !ok {
		return "", false
	}

	body := math.Text + punct
	if number != "" {
		// \tag renders the number right-aligned in display mode, the way the
		// printed specification sets it, and keeps it attached to the formula
		// and machine-readable instead of stranded as loose text.
		body += " \\tag{" + number + "}"
	}
	return body, true
}

// splitEquationTail parses the text following a formula in an equation
// paragraph into the punctuation that belongs to the sentence and the
// equation number, with the number's parentheses stripped. ok is false when
// the text is anything else, which means the paragraph is prose around a
// formula rather than a standalone equation.
func splitEquationTail(tail string) (punct, number string, ok bool) {
	// Tabs separate the formula from its number under the EQ style; they are
	// layout, not content.
	rest := strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(tail, "\t", " "), " ", " "))
	if rest == "" {
		return "", "", true
	}
	if i := strings.IndexAny(rest, eqTrailingPunct); i == 0 {
		punct = rest[:1]
		rest = strings.TrimSpace(rest[1:])
	}
	if rest == "" {
		return punct, "", true
	}
	if !eqNumberRE.MatchString(rest) {
		return "", "", false
	}
	return punct, strings.TrimSpace(rest[1 : len(rest)-1]), true
}
