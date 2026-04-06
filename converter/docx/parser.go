package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	sectionNumberRE = regexp.MustCompile(`^([A-Z](?:\.\d+)+|\d+[A-Za-z]?(?:\.\d+)*)[\t ]+(.+)$`)
	annexRE         = regexp.MustCompile(`(?is)^Annex[\s\xa0]+([A-Z])[\s\xa0]*(?:\((?:normative|informative)\))?[\s\xa0]*[:\s\xa0]*(.*)$`)
	headingNumRE    = regexp.MustCompile(`(?i)^[Hh]eading\s+(\d+)`)
	annexSubRE      = regexp.MustCompile(`^[A-Z]\.`)
)

// bodyElement represents a top-level element in the document body.
type bodyElement struct {
	Tag       string        // "p" for paragraph, "tbl" for table, etc.
	Paragraph paragraphInfo // populated when Tag == "p"
	Table     tableInfo     // populated when Tag == "tbl"
}

// ParseDocx parses a 3GPP .docx file and returns metadata, sections, and images.
func ParseDocx(path string) (*ParseResult, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("open docx: %w", err)
	}
	defer r.Close()

	return parseFromZipReader(&r.Reader, filepath.Base(path))
}

// ParseDocxFromBytes parses a 3GPP .docx from in-memory bytes.
func ParseDocxFromBytes(data []byte, filename string) (*ParseResult, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open docx from bytes: %w", err)
	}

	return parseFromZipReader(r, filename)
}

func parseFromZipReader(r *zip.Reader, filename string) (*ParseResult, error) {
	// Read styles
	stylesData, err := readZipFile(r, "word/styles.xml")
	if err != nil {
		// styles.xml might not exist; use empty map
		stylesData = nil
	}
	styleMap, err := parseStyles(stylesData)
	if err != nil {
		log.Printf("warning: failed to parse styles.xml in %s: %v", filename, err)
	}
	if styleMap == nil {
		styleMap = make(map[string]string)
	}

	// Read core properties
	propsData, err := readZipFile(r, "docProps/core.xml")
	var props coreProperties
	if err == nil {
		props = parseCoreProperties(propsData)
	}

	// Parse relationships for image references
	relMap, err := parseRelationships(r)
	if err != nil {
		// Not fatal — just means no images will be extracted.
		relMap = nil
	}

	// Extract images from the ZIP
	var images map[string]*EmbeddedImage
	if len(relMap) > 0 {
		images = extractImages(r, relMap)
	}

	// Read document body
	docData, err := readZipFile(r, "word/document.xml")
	if err != nil {
		return nil, fmt.Errorf("read document.xml: %w", err)
	}

	// Parse body elements
	bodyElements, err := parseBody(docData)
	if err != nil {
		return nil, fmt.Errorf("parse body: %w", err)
	}

	// Extract metadata
	metadata := extractMetadata(filename, props, bodyElements, styleMap)

	// Parse sections (with image placeholder insertion)
	sections := parseSections(bodyElements, styleMap, relMap, images)

	// Collect images into a list
	var imageList []*EmbeddedImage
	for _, img := range images {
		imageList = append(imageList, img)
	}

	return &ParseResult{
		Metadata: metadata,
		Sections: sections,
		Images:   imageList,
	}, nil
}

// parseBody extracts top-level body elements (paragraphs and tables) from document.xml.
func parseBody(data []byte) ([]bodyElement, error) {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var elements []bodyElement
	inBody := false

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "body" {
				inBody = true
				continue
			}
			if !inBody {
				continue
			}
			local := t.Name.Local
			switch local {
			case "p":
				info := parseParagraphFromDecoder(decoder, t)
				elements = append(elements, bodyElement{Tag: "p", Paragraph: info})
			case "tbl":
				tbl := parseTableFromDecoder(decoder, t)
				elements = append(elements, bodyElement{Tag: "tbl", Table: tbl})
			default:
				// Skip other top-level body elements
				decoder.Skip()
			}
		case xml.EndElement:
			if t.Name.Local == "body" {
				inBody = false
			}
		}
	}

	return elements, nil
}

// imagePlaceholder returns a markdown placeholder for an image reference.
func imagePlaceholder(relMap map[string]string, images map[string]*EmbeddedImage, ref imageRef) string {
	target, ok := relMap[ref.RID]
	if !ok {
		return ""
	}
	img, ok := images[target]
	if !ok {
		return ""
	}
	dimSuffix := ""
	if ref.WidthPx > 0 && ref.HeightPx > 0 {
		dimSuffix = fmt.Sprintf("?w=%d&h=%d", ref.WidthPx, ref.HeightPx)
	}
	if img.LLMReadable {
		alt := "Figure"
		if ref.AltText != "" {
			alt = ref.AltText
		}
		return fmt.Sprintf("![%s](image://%s%s)", alt, img.Name, dimSuffix)
	}
	alt := img.Name
	if ref.AltText != "" {
		alt = ref.AltText
	}
	if dimSuffix != "" {
		return fmt.Sprintf("[Figure: %s (%s, use get_image to retrieve, %dx%d)]", alt, img.Name, ref.WidthPx, ref.HeightPx)
	}
	return fmt.Sprintf("[Figure: %s (%s, use get_image to retrieve)]", alt, img.Name)
}

// parseSections walks the body elements and creates a section hierarchy.
func parseSections(elements []bodyElement, styleMap map[string]string, relMap map[string]string, images map[string]*EmbeddedImage) []*Section {
	var sections []*Section
	var currentSection *Section
	var sectionStack []*Section
	inAnnex := false

	// Accumulates consecutive code paragraphs (e.g. OpenAPI YAML samples)
	// so that they can be emitted as a single fenced code block instead of
	// being split across multiple Markdown paragraphs.
	var codeBuffer []string
	flushCodeBlock := func() {
		if len(codeBuffer) == 0 || currentSection == nil {
			codeBuffer = nil
			return
		}
		// Trim trailing blank lines.
		for len(codeBuffer) > 0 && strings.TrimSpace(codeBuffer[len(codeBuffer)-1]) == "" {
			codeBuffer = codeBuffer[:len(codeBuffer)-1]
		}
		if len(codeBuffer) > 0 {
			currentSection.Content = append(currentSection.Content,
				"```yaml\n"+strings.Join(codeBuffer, "\n")+"\n```")
		}
		codeBuffer = nil
	}

	for _, elem := range elements {
		switch elem.Tag {
		case "tbl":
			flushCodeBlock()
			md := rowsToMarkdown(elem.Table.Rows)
			if md != "" && currentSection != nil {
				currentSection.Content = append(currentSection.Content, md)
			}
		case "p":
			info := elem.Paragraph
			styleName := resolveStyleName(info.StyleID, styleMap)
			headingLevel := getHeadingLevel(styleName)

			// Also detect heading styles beyond level 6
			if headingLevel == 0 {
				if match := headingNumRE.FindStringSubmatch(styleName); match != nil {
					var level int
					if _, err := fmt.Sscanf(match[1], "%d", &level); err == nil {
						headingLevel = level
					}
				}
			}

			if headingLevel > 0 {
				flushCodeBlock()
				// Normalize text
				text := strings.ReplaceAll(info.Text, "\u00a0", " ")
				text = strings.ReplaceAll(text, "\n", " ")
				text = strings.TrimSpace(text)
				if text == "" {
					continue
				}

				var number, title string
				rawText := strings.ReplaceAll(info.Text, "\n", " ")
				rawText = strings.TrimSpace(rawText)

				if annexMatch := annexRE.FindStringSubmatch(rawText); annexMatch != nil {
					number = annexMatch[1]
					title = strings.ReplaceAll(annexMatch[2], "\u00a0", " ")
					title = strings.ReplaceAll(title, "\n", " ")
					title = strings.TrimSpace(title)
					if title == "" {
						title = "Annex " + number
					}
					headingLevel = 1
					inAnnex = true
				} else if match := sectionNumberRE.FindStringSubmatch(text); match != nil {
					number = match[1]
					title = strings.TrimSpace(match[2])
					// For annex subsections, derive level from depth
					if inAnnex && annexSubRE.MatchString(number) {
						headingLevel = strings.Count(number, ".") + 1
					}
				} else {
					number = text
					title = text
				}

				// Find parent
				var parentNumber string
				for len(sectionStack) > 0 && sectionStack[len(sectionStack)-1].Level >= headingLevel {
					sectionStack = sectionStack[:len(sectionStack)-1]
				}
				if len(sectionStack) > 0 {
					parentNumber = sectionStack[len(sectionStack)-1].Number
				}

				section := &Section{
					Number:       number,
					Title:        title,
					Level:        headingLevel,
					ParentNumber: parentNumber,
				}

				if len(sectionStack) > 0 {
					sectionStack[len(sectionStack)-1].Children = append(sectionStack[len(sectionStack)-1].Children, section)
				}

				sectionStack = append(sectionStack, section)
				sections = append(sections, section)
				currentSection = section
			} else {
				isCodePara := (info.IsCode || isCodeStyleName(styleName)) && len(info.Images) == 0
				switch {
				case isCodePara && currentSection != nil:
					// Append to the pending code block, preserving whitespace.
					codeBuffer = append(codeBuffer, codeLineText(info))
				case info.Text == "" && len(info.Images) == 0 && len(codeBuffer) > 0:
					// Preserve blank lines inside a pending code block.
					codeBuffer = append(codeBuffer, "")
				default:
					flushCodeBlock()
					md := paragraphToMarkdown(info, styleName)
					if md != "" && currentSection != nil {
						currentSection.Content = append(currentSection.Content, md)
					}
					// Insert image placeholders if the paragraph references images
					if currentSection != nil && len(info.Images) > 0 && relMap != nil {
						for _, ref := range info.Images {
							ph := imagePlaceholder(relMap, images, ref)
							if ph != "" {
								currentSection.Content = append(currentSection.Content, ph)
							}
						}
					}
				}
			}
		}
	}

	flushCodeBlock()

	return sections
}

// isCodeStyleName returns true for paragraph style names that indicate
// code/preformatted content in 3GPP DOCX files.
func isCodeStyleName(styleName string) bool {
	switch strings.ToLower(styleName) {
	case "macro", "code", "preformatted text", "html preformatted":
		return true
	}
	return false
}

// codeLineText returns the raw text of a paragraph without any Markdown
// formatting (no bold/italic markers), preserving leading whitespace so that
// YAML/code indentation is kept intact inside fenced code blocks.
func codeLineText(info paragraphInfo) string {
	if len(info.Runs) > 0 {
		var sb strings.Builder
		for _, r := range info.Runs {
			sb.WriteString(r.Text)
		}
		return sb.String()
	}
	return info.Text
}

// getHeadingLevel returns the heading level for a style name, or 0 if not a heading.
func getHeadingLevel(styleName string) int {
	if level, ok := headingStyles[styleName]; ok {
		return level
	}
	return 0
}

// readZipFile reads a file from within a zip archive.
func readZipFile(r *zip.Reader, name string) ([]byte, error) {
	for _, f := range r.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, fmt.Errorf("file not found in zip: %s", name)
}
