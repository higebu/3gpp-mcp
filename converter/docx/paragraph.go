package docx

import (
	"bytes"
	"encoding/xml"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// paragraphInfo holds extracted information from a w:p element.
type paragraphInfo struct {
	StyleID string
	Text    string
	Runs    []runInfo
	Images  []imageRef
	IsCode  bool // true if the paragraph uses a monospace/code font
	// SkippedDiagramLabels holds text-box labels found inside a grouped
	// vector diagram (VML v:group, or DrawingML wpg:wgp reached through
	// mc:AlternateContent) that had no embeddable raster image anywhere in
	// it. Such diagrams can't be rendered by this converter, and the raw XML
	// order of their labels rarely matches the visual reading order, so the
	// labels are kept out of Text/Runs to avoid emitting garbled prose; see
	// diagramPlaceholder in parser.go.
	SkippedDiagramLabels []string
}

// runInfo holds extracted information from a w:r element.
type runInfo struct {
	Text   string
	Bold   bool
	Italic bool
	IsCode bool // true if the run uses a monospace/code font
	// VertAlign holds the vertical alignment of the run, either
	// "superscript", "subscript", or "" (baseline).
	VertAlign string
}

// parseParagraph parses a w:p XML element from raw bytes into paragraphInfo.
func parseParagraph(data []byte) paragraphInfo {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := decoder.Token()
		if err != nil {
			return paragraphInfo{}
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == "p" {
			return parseParagraphFromDecoder(decoder, se)
		}
	}
}

// parseParagraphFromDecoder parses a w:p element from an existing decoder.
// The start element has already been consumed; this reads through the matching end element.
func parseParagraphFromDecoder(d *xml.Decoder, _ xml.StartElement) paragraphInfo {
	var info paragraphInfo
	var inPPr, inPPrRPr, inRPr, inR, inT bool
	var paragraphCodeFont bool
	var currentRun runInfo
	var runTexts []string
	var pendingWidthPx, pendingHeightPx int

	depth := 1
	for depth > 0 {
		tok, err := d.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			local := t.Name.Local
			// OMML math: convert the whole m:oMath / m:oMathPara subtree to
			// LaTeX and emit it as a run. ommlToLaTeX consumes through the
			// matching end element, so decrement depth to rebalance (mirrors
			// the nested-paragraph handling in parseTableFromDecoder).
			if isMathNS(t.Name.Space) && (local == "oMath" || local == "oMathPara") {
				latex := ommlToLaTeX(d, t)
				depth--
				if latex != "" {
					delim := "$"
					if local == "oMathPara" {
						delim = "$$"
					}
					info.Runs = append(info.Runs, runInfo{Text: delim + latex + delim})
				}
				continue
			}
			// Grouped vector diagrams (VML v:group, or DrawingML wpg:wgp
			// reached through mc:AlternateContent) are not embeddable
			// pictures: their text-box labels are ordinary WordprocessingML
			// runs with no ancestor markers, so without this interception
			// they'd flatten straight into info.Runs in raw XML document
			// order, which rarely matches the diagram's visual reading order
			// (see issue #25). scanDrawingSubtree consumes the whole subtree
			// itself, so rebalance depth afterwards like the OMML case above.
			if local == "group" || local == "AlternateContent" {
				imgs, labels, hasGroup, hasRaster := scanDrawingSubtree(d, t)
				depth--
				if local == "group" {
					hasGroup = true
				}
				info.Images = append(info.Images, imgs...)
				if hasGroup && !hasRaster {
					info.SkippedDiagramLabels = append(info.SkippedDiagramLabels, labels...)
				} else {
					// Not a pure-vector group diagram (either a plain
					// annotated shape with no grouping, or a group that also
					// contains a raster image): preserve the pre-existing
					// behavior of folding any text-box labels into the
					// paragraph's own text.
					for _, label := range labels {
						info.Runs = append(info.Runs, runInfo{Text: label})
					}
				}
				continue
			}
			switch local {
			case "pPr":
				inPPr = true
			case "pStyle":
				if inPPr {
					info.StyleID = getAttrVal(t, "val")
				}
			case "rPr":
				if inPPr && !inR {
					inPPrRPr = true
				} else if inR {
					inRPr = true
				}
			case "rFonts":
				if inPPrRPr {
					for _, attr := range t.Attr {
						if isMonospaceFont(attr.Value) {
							paragraphCodeFont = true
							break
						}
					}
				} else if inRPr && inR {
					for _, attr := range t.Attr {
						if isMonospaceFont(attr.Value) {
							currentRun.IsCode = true
							break
						}
					}
				}
			case "r":
				if !isWordNS(t.Name.Space) {
					break
				}
				inR = true
				currentRun = runInfo{}
				runTexts = nil
			case "b":
				if inRPr && inR {
					val := getAttrVal(t, "val")
					currentRun.Bold = val != "false" && val != "0"
				}
			case "i":
				if inRPr && inR {
					val := getAttrVal(t, "val")
					currentRun.Italic = val != "false" && val != "0"
				}
			case "vertAlign":
				if inRPr && inR {
					currentRun.VertAlign = getAttrVal(t, "val")
				}
			case "t":
				if !isWordNS(t.Name.Space) {
					break
				}
				inT = true
			case "tab":
				if inR {
					runTexts = append(runTexts, "\t")
				}
			case "extent":
				// DrawingML: <wp:extent cx="5486400" cy="3200400"/> (EMU units)
				if cx := getAttrVal(t, "cx"); cx != "" {
					pendingWidthPx = emuToPx(cx)
				}
				if cy := getAttrVal(t, "cy"); cy != "" {
					pendingHeightPx = emuToPx(cy)
				}
			case "shape":
				// VML: <v:shape style="width:453.6pt;height:340.25pt">
				if style := getAttrVal(t, "style"); style != "" {
					w, h := parseShapeStyle(style)
					if w > 0 {
						pendingWidthPx = w
					}
					if h > 0 {
						pendingHeightPx = h
					}
				}
			case "imagedata":
				// VML image: <v:imagedata r:id="rId9" o:title="..."/>
				rid := getAttrValNS(t, "id")
				if rid == "" {
					rid = getAttrVal(t, "id")
				}
				if rid != "" {
					ref := imageRef{
						RID:      rid,
						AltText:  getAttrVal(t, "title"),
						WidthPx:  pendingWidthPx,
						HeightPx: pendingHeightPx,
					}
					info.Images = append(info.Images, ref)
					pendingWidthPx, pendingHeightPx = 0, 0
				}
			case "blip":
				// DrawingML image: <a:blip r:embed="rId5"/>
				rid := getAttrValNS(t, "embed")
				if rid == "" {
					rid = getAttrVal(t, "embed")
				}
				if rid != "" {
					ref := imageRef{
						RID:      rid,
						WidthPx:  pendingWidthPx,
						HeightPx: pendingHeightPx,
					}
					info.Images = append(info.Images, ref)
					pendingWidthPx, pendingHeightPx = 0, 0
				}
			}
		case xml.EndElement:
			depth--
			local := t.Name.Local
			switch local {
			case "pPr":
				inPPr = false
				inPPrRPr = false
			case "rPr":
				inRPr = false
				inPPrRPr = false
			case "t":
				if isWordNS(t.Name.Space) {
					inT = false
				}
			case "r":
				if inR && isWordNS(t.Name.Space) {
					currentRun.Text = strings.Join(runTexts, "")
					if currentRun.Text != "" {
						info.Runs = append(info.Runs, currentRun)
					}
					inR = false
				}
			case "drawing", "pict":
				// Reset pending dimensions when leaving a drawing/pict scope
				// to prevent leaking to subsequent images.
				pendingWidthPx, pendingHeightPx = 0, 0
			}
		case xml.CharData:
			if inR && inT {
				runTexts = append(runTexts, string(t))
			}
		}
	}

	// Build full text
	var fullText []string
	for _, r := range info.Runs {
		fullText = append(fullText, r.Text)
	}
	info.Text = strings.Join(fullText, "")

	// Determine if the paragraph should be treated as a code paragraph.
	// Either the paragraph-level rPr declares a monospace font, or every
	// text-bearing run uses a monospace font.
	if paragraphCodeFont {
		info.IsCode = true
	} else if len(info.Runs) > 0 {
		allCode := true
		hasText := false
		for _, r := range info.Runs {
			if r.Text == "" {
				continue
			}
			hasText = true
			if !r.IsCode {
				allCode = false
				break
			}
		}
		if hasText && allCode {
			info.IsCode = true
		}
	}

	return info
}

// scanDrawingSubtree walks a VML v:group/v:shape subtree — or an
// mc:AlternateContent wrapper around one — looking for embedded raster
// images and text-box labels. It recurses into every unrecognized child so
// the decoder always stays balanced, regardless of how deeply the shapes
// are nested (3GPP diagrams commonly nest v:group three or four levels
// deep). hasGroup reports whether a v:group was found anywhere in the
// subtree (including the root, if the caller already knows that); hasRaster
// reports whether any embeddable picture was found.
//
// mc:Choice is always skipped via d.Skip(): Word emits it alongside an
// equivalent mc:Fallback in the legacy VML form for compatibility, and this
// converter only understands VML shapes, so processing both would double
// every label and image.
func scanDrawingSubtree(d *xml.Decoder, _ xml.StartElement) (imgs []imageRef, labels []string, hasGroup, hasRaster bool) {
	for {
		tok, err := d.Token()
		if err != nil {
			return
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "Choice":
				_ = d.Skip()
			case "imagedata":
				// VML image: <v:imagedata r:id="rId9" o:title="..."/>
				rid := getAttrValNS(t, "id")
				if rid == "" {
					rid = getAttrVal(t, "id")
				}
				if rid != "" {
					imgs = append(imgs, imageRef{RID: rid, AltText: getAttrVal(t, "title")})
					hasRaster = true
				}
				_ = d.Skip()
			case "blip":
				// DrawingML image: <a:blip r:embed="rId5"/>
				rid := getAttrValNS(t, "embed")
				if rid == "" {
					rid = getAttrVal(t, "embed")
				}
				if rid != "" {
					imgs = append(imgs, imageRef{RID: rid})
					hasRaster = true
				}
				_ = d.Skip()
			case "txbxContent":
				labels = append(labels, scanTextBoxLabels(d)...)
			default:
				if t.Name.Local == "group" {
					hasGroup = true
				}
				subImgs, subLabels, subHasGroup, subHasRaster := scanDrawingSubtree(d, t)
				imgs = append(imgs, subImgs...)
				labels = append(labels, subLabels...)
				hasGroup = hasGroup || subHasGroup
				hasRaster = hasRaster || subHasRaster
			}
		case xml.EndElement:
			return
		}
	}
}

// scanTextBoxLabels consumes a w:txbxContent element (the start element has
// already been consumed by the caller) and returns the trimmed text of each
// paragraph inside it, in document order.
func scanTextBoxLabels(d *xml.Decoder) []string {
	var labels []string
	for {
		tok, err := d.Token()
		if err != nil {
			return labels
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "p" {
				pInfo := parseParagraphFromDecoder(d, t)
				if text := strings.TrimSpace(pInfo.Text); text != "" {
					labels = append(labels, text)
				}
			} else {
				_ = d.Skip()
			}
		case xml.EndElement:
			return labels
		}
	}
}

// getAttrValNS returns the value of a namespaced attribute by its local name,
// ignoring the namespace prefix. This is needed for attributes like r:id or r:embed
// where the namespace varies.
func getAttrValNS(elem xml.StartElement, localName string) string {
	for _, a := range elem.Attr {
		if a.Name.Local == localName && a.Name.Space != "" {
			return a.Value
		}
	}
	return ""
}

func getAttrVal(elem xml.StartElement, localName string) string {
	for _, a := range elem.Attr {
		if a.Name.Local == localName {
			return a.Value
		}
	}
	return ""
}

// mergeAdjacentRuns coalesces consecutive runs that share identical
// formatting (ignoring Text) into a single run. DOCX frequently splits one
// visually-continuous span of text across multiple <w:r> elements for
// reasons unrelated to formatting (bookmarks, proofing-error ranges,
// revisions); without merging, each fragment gets wrapped in its own
// markdown emphasis delimiters, producing stray/unbalanced asterisks
// mid-word.
func mergeAdjacentRuns(runs []runInfo) []runInfo {
	var merged []runInfo
	for _, run := range runs {
		if run.Text == "" {
			continue
		}
		if n := len(merged); n > 0 {
			last := &merged[n-1]
			if last.Bold == run.Bold && last.Italic == run.Italic &&
				last.VertAlign == run.VertAlign && last.IsCode == run.IsCode {
				last.Text += run.Text
				continue
			}
		}
		merged = append(merged, run)
	}
	return merged
}

// wrapEmphasis wraps text in the given markdown emphasis delimiter (e.g.
// "*", "**", "***"), moving any leading/trailing whitespace outside the
// delimiters. CommonMark requires emphasis delimiters to hug their content
// (no whitespace immediately inside), so a run like "word " wrapped naively
// as "*word *" fails to parse as emphasis and the asterisks show up as
// literal text; a run that is only whitespace has no content to emphasize
// at all and is returned unchanged (see issue #33).
func wrapEmphasis(text, delim string) string {
	mid := strings.TrimFunc(text, unicode.IsSpace)
	if mid == "" {
		return text
	}
	i := strings.Index(text, mid)
	lead, trail := text[:i], text[i+len(mid):]
	return lead + delim + mid + delim + trail
}

// paragraphToMarkdown converts a paragraph to markdown text.
func paragraphToMarkdown(info paragraphInfo, styleName string) string {
	text := strings.TrimSpace(info.Text)
	if text == "" {
		return ""
	}

	// Handle list items
	if strings.HasPrefix(styleName, "List") {
		if strings.Contains(styleName, "Bullet") {
			return "- " + text
		}
		if strings.Contains(styleName, "Number") {
			return "1. " + text
		}
		return "- " + text // default to bullet for unknown list styles
	}

	// Handle bold/italic at run level
	if len(info.Runs) > 0 {
		var parts []string
		for _, run := range mergeAdjacentRuns(info.Runs) {
			runText := run.Text
			if run.Bold && run.Italic {
				runText = wrapEmphasis(runText, "***")
			} else if run.Bold {
				runText = wrapEmphasis(runText, "**")
			} else if run.Italic {
				runText = wrapEmphasis(runText, "*")
			}
			switch run.VertAlign {
			case "superscript":
				runText = "<sup>" + runText + "</sup>"
			case "subscript":
				runText = "<sub>" + runText + "</sub>"
			}
			parts = append(parts, runText)
		}
		if len(parts) > 0 {
			// Trim leading/trailing whitespace (e.g. a leading <w:tab/> used
			// to center an equation via tab stops): a line that starts with
			// a tab or 4+ spaces is parsed as an indented code block by
			// CommonMark, inside which HTML tags like <sub> are never
			// interpreted and show up as literal text (see issue #25).
			return strings.TrimSpace(strings.Join(parts, ""))
		}
	}

	return text
}

// emuToPx converts an EMU (English Metric Unit) string value to CSS pixels.
// 1 inch = 914400 EMU, 1 inch = 96 CSS px, so 1 px = 9525 EMU.
func emuToPx(emu string) int {
	v, err := strconv.ParseInt(emu, 10, 64)
	if err != nil || v <= 0 {
		return 0
	}
	return int(math.Round(float64(v) / 9525.0))
}

// cssLengthRE matches a CSS length value like "453.6pt", "6in", "15.24cm".
var cssLengthRE = regexp.MustCompile(`^([\d.]+)(pt|in|cm|mm|px)?$`)

// parseCSSLength converts a CSS length string to CSS pixels.
func parseCSSLength(s string) int {
	s = strings.TrimSpace(s)
	m := cssLengthRE.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil || val <= 0 {
		return 0
	}
	unit := m[2]
	switch unit {
	case "pt":
		return int(math.Round(val * 96.0 / 72.0))
	case "in":
		return int(math.Round(val * 96.0))
	case "cm":
		return int(math.Round(val * 96.0 / 2.54))
	case "mm":
		return int(math.Round(val * 96.0 / 25.4))
	case "px", "":
		return int(math.Round(val))
	}
	return 0
}

// monospaceFonts lists font names that indicate code/preformatted content in
// 3GPP DOCX files. Paragraphs using these fonts are converted into fenced
// code blocks instead of regular Markdown paragraphs.
var monospaceFonts = map[string]bool{
	"Courier New":    true,
	"Courier":        true,
	"Consolas":       true,
	"Lucida Console": true,
	"Monaco":         true,
	"Menlo":          true,
}

// isMonospaceFont returns true if the font name is a known monospace font.
func isMonospaceFont(name string) bool {
	return monospaceFonts[name]
}

// parseShapeStyle extracts width and height in CSS pixels from a VML shape style attribute.
// Example: "width:453.6pt;height:340.25pt" → (605, 454)
func parseShapeStyle(style string) (widthPx, heightPx int) {
	for _, part := range strings.Split(style, ";") {
		part = strings.TrimSpace(part)
		if k, v, ok := strings.Cut(part, ":"); ok {
			k = strings.TrimSpace(k)
			v = strings.TrimSpace(v)
			switch k {
			case "width":
				widthPx = parseCSSLength(v)
			case "height":
				heightPx = parseCSSLength(v)
			}
		}
	}
	return
}
