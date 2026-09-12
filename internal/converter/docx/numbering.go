package docx

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// Word numbers headings in one of two ways: the number is typed into the
// heading text ("4.2.3\tTitle", every 3GPP specification), or the heading
// style carries a numbering definition (w:numPr) and Word computes the
// number when it lays the document out. Meeting reports use the second
// way, so their headings arrive without a number. numbering.xml holds the
// definitions: an abstract numbering (one lvl per depth, with a start
// value, a format and a text template such as "%1.%2") and the concrete
// numbering instances (w:num) that refer to them, possibly overriding a
// level's start.

// numLevel is one depth of a numbering definition.
type numLevel struct {
	start  int
	format string // "decimal", "lowerLetter", "upperRoman", "bullet", "none", ...
	text   string // "%1.%2"
}

// numInstance is a w:num: an abstract numbering plus per-level overrides.
type numInstance struct {
	abstractID string
	overrides  map[int]numLevel
}

// numberingDefs is the content of word/numbering.xml.
type numberingDefs struct {
	abstracts map[string][]numLevel
	nums      map[string]numInstance
}

const maxNumLevels = 9

// parseNumbering reads word/numbering.xml. A malformed file yields the
// definitions read up to the error together with the error, so the caller
// can warn and still number what it can.
func parseNumbering(data []byte) (*numberingDefs, error) {
	defs := &numberingDefs{abstracts: map[string][]numLevel{}, nums: map[string]numInstance{}}
	if len(data) == 0 {
		return defs, nil
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var (
		abstractID string
		levels     []numLevel
		numID      string
		num        numInstance
		lvl        *numLevel
		lvlIndex   int
		inOverride bool
		overIndex  int
	)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return defs, fmt.Errorf("parse numbering.xml: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "abstractNum":
				abstractID = getAttrVal(t, "abstractNumId")
				levels = make([]numLevel, maxNumLevels)
				for i := range levels {
					levels[i] = numLevel{start: 1, format: "decimal"}
				}
			case "num":
				numID = getAttrVal(t, "numId")
				num = numInstance{overrides: map[int]numLevel{}}
			case "abstractNumId":
				if numID != "" {
					num.abstractID = getAttrVal(t, "val")
				}
			case "lvlOverride":
				// Only meaningful inside a w:num; a stray one in a
				// malformed file must not write into a nil map.
				if numID != "" {
					inOverride = true
					overIndex, _ = strconv.Atoi(getAttrVal(t, "ilvl"))
				}
			case "startOverride":
				if inOverride {
					if v, err := strconv.Atoi(getAttrVal(t, "val")); err == nil {
						o := num.overrides[overIndex]
						o.start = v
						num.overrides[overIndex] = o
					}
				}
			case "lvl":
				lvlIndex, _ = strconv.Atoi(getAttrVal(t, "ilvl"))
				l := numLevel{start: 1, format: "decimal"}
				lvl = &l
			case "start":
				if lvl != nil {
					if v, err := strconv.Atoi(getAttrVal(t, "val")); err == nil {
						lvl.start = v
					}
				}
			case "numFmt":
				if lvl != nil {
					lvl.format = getAttrVal(t, "val")
				}
			case "lvlText":
				if lvl != nil {
					lvl.text = getAttrVal(t, "val")
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "lvl":
				if lvl != nil && lvlIndex >= 0 && lvlIndex < maxNumLevels {
					switch {
					case inOverride:
						num.overrides[overIndex] = *lvl
					case levels != nil:
						levels[lvlIndex] = *lvl
					}
				}
				lvl = nil
			case "lvlOverride":
				inOverride = false
			case "abstractNum":
				if abstractID != "" {
					defs.abstracts[abstractID] = levels
				}
				abstractID, levels = "", nil
			case "num":
				if numID != "" {
					defs.nums[numID] = num
				}
				numID = ""
			}
		}
	}
	return defs, nil
}

// level returns the definition of one depth of a numbering instance, the
// instance's override applied.
func (d *numberingDefs) level(numID string, ilvl int) (numLevel, bool) {
	num, ok := d.nums[numID]
	if !ok || ilvl < 0 || ilvl >= maxNumLevels {
		return numLevel{}, false
	}
	levels, ok := d.abstracts[num.abstractID]
	if !ok {
		return numLevel{}, false
	}
	l := levels[ilvl]
	if o, ok := num.overrides[ilvl]; ok {
		if o.start > 0 {
			l.start = o.start
		}
		if o.text != "" {
			l.format, l.text = o.format, o.text
		}
	}
	return l, true
}

// numberer computes the numbers of a document's numbered paragraphs in
// reading order, the way Word does when it lays the document out.
type numberer struct {
	defs     *numberingDefs
	styles   map[string]styleNumbering
	counters map[string][]int
}

func newNumberer(defs *numberingDefs, styles map[string]styleNumbering) *numberer {
	return &numberer{defs: defs, styles: styles, counters: map[string][]int{}}
}

// paragraphNumber advances the numbering a paragraph belongs to and returns
// the paragraph's number ("2.1"), or "" when the paragraph is not numbered
// or its level renders no number (a bullet). The paragraph's own w:numPr
// wins over its style's; a numId of 0 turns numbering off.
func (n *numberer) paragraphNumber(p paragraphInfo) string {
	if n == nil {
		return ""
	}
	numID, ilvl := "", 0
	if p.HasNumPr {
		numID, ilvl = p.NumID, p.ILvl
	}
	if s, ok := resolveStyleNumbering(p.StyleID, n.styles); ok {
		if numID == "" {
			numID = s.numID
		}
		if !p.HasNumPr || !p.HasILvl {
			ilvl = s.ilvl
		}
	}
	if numID == "" || numID == "0" {
		return ""
	}
	if _, ok := n.defs.level(numID, ilvl); !ok {
		return ""
	}
	counters, ok := n.counters[numID]
	if !ok {
		counters = make([]int, maxNumLevels)
		for i := range counters {
			l, _ := n.defs.level(numID, i)
			counters[i] = l.start - 1
		}
		n.counters[numID] = counters
	}
	counters[ilvl]++
	for i := ilvl + 1; i < maxNumLevels; i++ {
		l, _ := n.defs.level(numID, i)
		counters[i] = l.start - 1
	}
	return n.label(numID, ilvl, counters)
}

// label renders a level's text template with the current counters.
func (n *numberer) label(numID string, ilvl int, counters []int) string {
	l, _ := n.defs.level(numID, ilvl)
	switch l.format {
	case "bullet", "none":
		return ""
	}
	text := l.text
	if text == "" {
		text = "%" + strconv.Itoa(ilvl+1)
	}
	var sb strings.Builder
	for i := 0; i < len(text); i++ {
		if text[i] == '%' && i+1 < len(text) && text[i+1] >= '1' && text[i+1] <= '9' {
			k := int(text[i+1] - '1')
			lk, _ := n.defs.level(numID, k)
			sb.WriteString(formatNumber(counters[k], lk.format))
			i++
			continue
		}
		sb.WriteByte(text[i])
	}
	return strings.TrimSpace(sb.String())
}

// formatNumber renders a counter in a w:numFmt.
func formatNumber(v int, format string) string {
	switch format {
	case "lowerLetter":
		return letters(v, 'a')
	case "upperLetter":
		return letters(v, 'A')
	case "lowerRoman":
		return strings.ToLower(roman(v))
	case "upperRoman":
		return roman(v)
	case "decimalZero":
		return fmt.Sprintf("%02d", v)
	default:
		return strconv.Itoa(v)
	}
}

// letters renders 1 as "a", 26 as "z" and 27 as "aa", as Word does.
func letters(v int, base byte) string {
	if v < 1 {
		return strconv.Itoa(v)
	}
	c := string(base + byte((v-1)%26))
	return strings.Repeat(c, (v-1)/26+1)
}

func roman(v int) string {
	if v < 1 || v > 3999 {
		return strconv.Itoa(v)
	}
	var sb strings.Builder
	for _, p := range []struct {
		n int
		s string
	}{{1000, "M"}, {900, "CM"}, {500, "D"}, {400, "CD"}, {100, "C"}, {90, "XC"}, {50, "L"}, {40, "XL"}, {10, "X"}, {9, "IX"}, {5, "V"}, {4, "IV"}, {1, "I"}} {
		for v >= p.n {
			sb.WriteString(p.s)
			v -= p.n
		}
	}
	return sb.String()
}

// styleNumbering is the w:numPr a style declares, each part possibly
// inherited from the style it is based on.
type styleNumbering struct {
	basedOn string
	numID   string
	ilvl    int
	hasNum  bool
	hasLvl  bool
}

// parseStyleNumbering reads the numbering each style in word/styles.xml
// declares.
func parseStyleNumbering(data []byte) map[string]styleNumbering {
	styles := map[string]styleNumbering{}
	if len(data) == 0 {
		return styles
	}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var (
		id      string
		cur     styleNumbering
		inStyle bool
		inNumPr bool
	)
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "style":
				inStyle = true
				id = getAttrVal(t, "styleId")
				cur = styleNumbering{}
			case "basedOn":
				if inStyle {
					cur.basedOn = getAttrVal(t, "val")
				}
			case "numPr":
				inNumPr = inStyle
			case "numId":
				if inNumPr {
					cur.numID, cur.hasNum = getAttrVal(t, "val"), true
				}
			case "ilvl":
				if inNumPr {
					if v, err := strconv.Atoi(getAttrVal(t, "val")); err == nil {
						cur.ilvl, cur.hasLvl = v, true
					}
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "numPr":
				inNumPr = false
			case "style":
				if inStyle && id != "" {
					styles[id] = cur
				}
				inStyle = false
			}
		}
	}
	return styles
}

// resolveStyleNumbering follows a style's w:basedOn chain for the numbering
// it inherits: numId and ilvl each come from the nearest style that sets
// them, as Word resolves paragraph properties.
func resolveStyleNumbering(styleID string, styles map[string]styleNumbering) (styleNumbering, bool) {
	var out styleNumbering
	visited := map[string]bool{}
	for id := styleID; id != "" && !visited[id]; {
		visited[id] = true
		s, ok := styles[id]
		if !ok {
			break
		}
		if !out.hasNum && s.hasNum {
			out.numID, out.hasNum = s.numID, true
		}
		if !out.hasLvl && s.hasLvl {
			out.ilvl, out.hasLvl = s.ilvl, true
		}
		id = s.basedOn
	}
	return out, out.hasNum
}
