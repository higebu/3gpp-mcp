package docx

import (
	"bytes"
	"encoding/xml"
	"regexp"
	"strings"
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
	filenameRE    = regexp.MustCompile(`^(\d{2})(\d{3})-?([a-z])(\d+)`)
	specPatternRE = regexp.MustCompile(`(?i)(?:TS|TR)\s*(\d+)\.(\d+)`)
	versionRE     = regexp.MustCompile(`V(\d+\.\d+\.\d+)`)
	releaseRE     = regexp.MustCompile(`Release\s+(\d+)`)
	sectionPartRE = regexp.MustCompile(`_s[A-Z0-9]`)
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

	var specID, title, version, release string

	// Parse from filename
	if match := filenameRE.FindStringSubmatch(stem); match != nil {
		series, num, verLetter, verNum := match[1], match[2], match[3], match[4]
		specID = "TS " + series + "." + num
		version = verLetter + verNum
	} else if match := specPatternRE.FindStringSubmatch(stem); match != nil {
		specID = "TS " + match[1] + "." + match[2]
	} else {
		specID = stem
	}

	// Try to get title from document properties
	if props.Subject != "" && !isTemplateValue(props.Subject) {
		if relMatch := releaseRE.FindStringSubmatch(props.Subject); relMatch != nil {
			release = relMatch[1]
			idx := strings.Index(props.Subject, "(Release")
			if idx >= 0 {
				title = strings.TrimRight(props.Subject[:idx], "; ")
			}
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

	return &SpecMetadata{
		SpecID:  specID,
		Title:   title,
		Version: version,
		Release: release,
	}
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
