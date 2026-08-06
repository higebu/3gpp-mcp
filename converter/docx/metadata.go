package docx

import (
	"bytes"
	"encoding/xml"
	"regexp"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/specver"
)

const (
	// maxHeadingSearchDepth limits how many body elements to scan for a title heading.
	maxHeadingSearchDepth = 20
	// maxVersionSearchDepth limits how many body elements to scan for version/release info.
	maxVersionSearchDepth = 50
	// maxCoverPageParas limits the number of paragraphs collected from the cover page.
	maxCoverPageParas = 60
	// maxTitleLength is the maximum length for a Normal-style paragraph to be considered part of the title.
	maxTitleLength = 80
)

var (
	// The trailing group is the base-36 archive version token: exactly three
	// characters, each a digit or letter. Legacy releases 1-9 have tokens
	// that start with a digit ("920" is 9.2.0), so the first character must
	// not be restricted to letters.
	filenameRE = regexp.MustCompile(`^(\d{2})(\d{3})(?:-(\d{1,2}))?-?([0-9a-z]{3})`)
	// The word boundary keeps document-text scans from matching a token
	// glued to the end of another word; filename stems keep the old
	// permissive pattern so names like "draftTS23.501" still normalize.
	specPatternRE     = regexp.MustCompile(`(?i)\b(TS|TR)\s*(\d+)\.(\d+)`)
	stemSpecPatternRE = regexp.MustCompile(`(?i)(TS|TR)\s*(\d+)\.(\d+)`)
	versionRE         = regexp.MustCompile(`V(\d+\.\d+\.\d+)`)
	releaseRE         = regexp.MustCompile(`Release\s+(\d+)`)
	sectionPartRE     = regexp.MustCompile(`_s[A-Z0-9]`)
)

// coreProperties represents docProps/core.xml.
type coreProperties struct {
	Subject string
	Title   string
}

// parseCoreProperties parses docProps/core.xml content.
func parseCoreProperties(data []byte) coreProperties {
	// Use streaming XML parser to handle namespaced elements robustly
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var props coreProperties
	var currentElement string

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			currentElement = t.Name.Local
		case xml.EndElement:
			currentElement = ""
		case xml.CharData:
			text := strings.TrimSpace(string(t))
			if text == "" {
				continue
			}
			switch currentElement {
			case "title":
				if props.Title == "" {
					props.Title = text
				}
			case "subject":
				if props.Subject == "" {
					props.Subject = text
				}
			}
		}
	}

	return props
}

// isTemplateValue checks if a document property contains a 3GPP template placeholder.
func isTemplateValue(text string) bool {
	if text == "" {
		return true
	}
	return strings.Contains(text, "<Title") || strings.Contains(text, "ab.cde")
}

// extractMetadata extracts spec metadata from document properties, body content, and filename.
func extractMetadata(filename string, props coreProperties, bodyElements []bodyElement, styleMap map[string]string) *SpecMetadata {
	// Remove extension
	stem := filename
	if idx := strings.LastIndex(stem, "."); idx >= 0 {
		stem = stem[:idx]
	}
	// Remove multi-part suffixes like _cover, _s00-11
	baseStem := stem

	var specID, docType, title, version, versionToken, release string

	// Parse from filename. The trailing group is the base-36 archive token
	// (e.g. "i60"), which is normalized to the dotted form used everywhere else.
	if match := filenameRE.FindStringSubmatch(stem); match != nil {
		series, num, part, ver := match[1], match[2], match[3], match[4]
		// The archive filename never distinguishes a Technical Specification
		// from a Technical Report, so only the document itself can say. When
		// it does not (part files of a split spec have no cover page), the
		// prefix defaults to "TS" and the caller may correct it from a
		// sibling file that does know.
		docType = detectDocType(series, num, props, collectCoverPageParagraphs(bodyElements, styleMap))
		prefix := docType
		if prefix == "" {
			prefix = "TS"
		}
		specID = prefix + " " + series + "." + num
		if part != "" {
			specID += "-" + part
		}
		versionToken = strings.ToLower(ver)
		if dotted, ok := specver.TokenToDotted(versionToken); ok {
			version = dotted
		}
	} else if match := stemSpecPatternRE.FindStringSubmatch(stem); match != nil {
		docType = strings.ToUpper(match[1])
		specID = docType + " " + match[2] + "." + match[3]
	} else {
		specID = stem
	}

	// Try to get title from document properties
	if props.Subject != "" && !isTemplateValue(props.Subject) {
		// The release marker is usually parenthesised ("...; (Release 18)"),
		// but releaseRE does not require the parenthesis. Cut the title at
		// wherever the marker actually starts, otherwise a subject that spells
		// the release without parentheses yields no title at all and the spec
		// falls back to its first heading.
		if relMatch := releaseRE.FindStringSubmatchIndex(props.Subject); relMatch != nil {
			release = props.Subject[relMatch[2]:relMatch[3]]
			title = strings.TrimRight(props.Subject[:relMatch[0]], "; (")
		} else {
			title = props.Subject
		}
	} else if props.Title != "" && !isTemplateValue(props.Title) {
		title = props.Title
	} else {
		// Extract from body (ZA/ZT styles)
		bodyTitle, bodyRelease := extractMetadataFromBody(bodyElements, styleMap)
		if bodyTitle != "" {
			title = bodyTitle
		}
		if bodyRelease != "" {
			release = bodyRelease
		}
	}

	// If still no title, try first heading (but not for section-part files)
	isSectionPart := sectionPartRE.MatchString(baseStem)
	if title == "" && !isSectionPart {
		for i, elem := range bodyElements {
			if i >= maxHeadingSearchDepth {
				break
			}
			if elem.Tag != "p" {
				continue
			}
			styleName := resolveStyleName(elem.Paragraph.StyleID, styleMap)
			if _, ok := headingStyles[styleName]; ok {
				title = strings.TrimSpace(elem.Paragraph.Text)
				break
			}
		}
	}
	if title == "" {
		title = specID
	}

	// Try to extract version/release from body paragraphs
	for i, elem := range bodyElements {
		if i >= maxVersionSearchDepth {
			break
		}
		if elem.Tag != "p" {
			continue
		}
		text := strings.TrimSpace(elem.Paragraph.Text)

		if version == "" {
			if verMatch := versionRE.FindStringSubmatch(text); verMatch != nil {
				version = verMatch[1]
			}
		}
		if release == "" {
			if relMatch := releaseRE.FindStringSubmatch(text); relMatch != nil {
				release = relMatch[1]
			}
		}
		if version != "" && release != "" {
			break
		}
	}

	// Fill in whichever version form is still missing. A legacy filename whose
	// token does not parse leaves only the body-scanned dotted version, and a
	// filename token that has no dotted counterpart leaves only the token.
	if version != "" && versionToken == "" {
		if token, ok := specver.DottedToToken(version); ok {
			versionToken = token
		}
	}
	// The first version component is the release, so a document that never
	// spells out "Release N" still gets one.
	if release == "" {
		release = specver.ReleaseOf(version)
	}

	return &SpecMetadata{
		SpecID:       specID,
		DocType:      docType,
		Title:        title,
		Version:      version,
		VersionToken: versionToken,
		Release:      release,
	}
}

// detectDocType returns "TS" or "TR" when the document names its own type,
// and "" when it does not. The strongest signal is an explicit marker naming
// this very document ("3GPP TR 21.905 V17.2.0" on the cover page, or the
// spec number in the docProps title/subject); markers whose number differs
// are references to other documents and are ignored. Failing that, the 3GPP
// cover template spells the type as a standalone "Technical Specification" /
// "Technical Report" line — matched by exact text, so the TSG name
// ("Technical Specification Group ...") never counts.
func detectDocType(series, num string, props coreProperties, coverParas []coverParagraph) string {
	texts := make([]string, 0, len(coverParas)+2)
	for _, cp := range coverParas {
		texts = append(texts, cp.text)
	}
	texts = append(texts, props.Title, props.Subject)

	for _, text := range texts {
		for _, m := range specPatternRE.FindAllStringSubmatch(text, -1) {
			if m[2] == series && m[3] == num {
				return strings.ToUpper(m[1])
			}
		}
	}
	for _, cp := range coverParas {
		line := strings.Trim(cp.text, " \t;:.,")
		switch {
		case strings.EqualFold(line, "Technical Specification"):
			return "TS"
		case strings.EqualFold(line, "Technical Report"):
			return "TR"
		}
	}
	return ""
}

// extractMetadataFromBody extracts title and release from document body using ZA/ZT styles.
func extractMetadataFromBody(elements []bodyElement, styleMap map[string]string) (string, string) {
	var titleParts []string
	var release string
	seenZT := false

	skipPrefixes := []string{
		"3rd Generation Partnership Project",
		"Technical Specification Group",
		"Technical Report Group",
	}

	coverParas := collectCoverPageParagraphs(elements, styleMap)

loop:
	for _, cp := range coverParas {
		styleName := cp.styleName
		text := strings.TrimRight(strings.TrimSpace(cp.text), ";")
		if text == "" {
			continue
		}

		switch styleName {
		case "ZA", "ZB":
			continue
		case "ZT":
			seenZT = true
			if relMatch := releaseRE.FindStringSubmatch(text); relMatch != nil {
				release = relMatch[1]
				continue
			}
			skip := false
			for _, prefix := range skipPrefixes {
				if strings.HasPrefix(text, prefix) {
					skip = true
					break
				}
			}
			if skip {
				continue
			}
			titleParts = append(titleParts, text)
		default:
			if isCoverPageEnd(styleName) {
				break loop
			}
			if styleName == "Normal" && seenZT && len(titleParts) == 0 {
				titleParts = append(titleParts, text)
			} else if styleName == "Normal" && seenZT && len(titleParts) > 0 {
				if len(text) < maxTitleLength && !strings.Contains(text, ".") {
					titleParts = append(titleParts, text)
				} else {
					break loop
				}
			}
		}
	}

	title := strings.Join(titleParts, "; ")
	return title, release
}

// isCoverPageEnd returns true if the style indicates the end of the cover page.
func isCoverPageEnd(styleName string) bool {
	if styleName == "TT" || styleName == "TOC heading" || styleName == "FP" ||
		strings.HasPrefix(styleName, "toc ") || strings.HasPrefix(styleName, "TOC") {
		return true
	}
	_, ok := headingStyles[styleName]
	return ok
}

type coverParagraph struct {
	styleName string
	text      string
}

// collectCoverPageParagraphs collects paragraphs from cover page, including those inside tables.
func collectCoverPageParagraphs(elements []bodyElement, styleMap map[string]string) []coverParagraph {
	var paras []coverParagraph

loop:
	for _, elem := range elements {
		if len(paras) > maxCoverPageParas {
			break
		}

		switch elem.Tag {
		case "tbl":
			// Cover page tables contain ZA/ZT styled paragraphs
			for _, cp := range elem.Table.CellParas {
				styleName := resolveStyleName(cp.StyleID, styleMap)
				paras = append(paras, coverParagraph{
					styleName: styleName,
					text:      cp.Text,
				})
			}
		case "p":
			styleName := resolveStyleName(elem.Paragraph.StyleID, styleMap)
			paras = append(paras, coverParagraph{
				styleName: styleName,
				text:      elem.Paragraph.Text,
			})

			// Stop at TOC or main content
			if styleName == "TT" || styleName == "TOC heading" || strings.HasPrefix(styleName, "toc ") ||
				strings.HasPrefix(styleName, "TOC") {
				break loop
			}
			if _, ok := headingStyles[styleName]; ok {
				break loop
			}
		}
	}

	return paras
}
