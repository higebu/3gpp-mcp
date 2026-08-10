package docx

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	sectionNumberRE     = regexp.MustCompile(`^([A-Z](?:\.\d+[A-Za-z]?)+|\d+[A-Za-z]?(?:\.\d+[A-Za-z]?)*)[\t ]+(.+)$`)
	annexRE             = regexp.MustCompile(`(?is)^Annex[\s\xa0]+([A-Z])[\s\xa0]*(?:\((?:normative|informative)\))?[\s\xa0]*[:\s\xa0]*(.*)$`)
	headingNumRE        = regexp.MustCompile(`(?i)^[Hh]eading\s+(\d+)`)
	annexSubRE          = regexp.MustCompile(`^[A-Z]\.`)
	unnumberedHeadingRE = regexp.MustCompile(`^\p{Pd}[\t ]+(.+)$`)

	// 3GPP ASN.1 extraction markers on their own paragraph:
	//   "-- ASN1START", "--ASN1STOP", "-- /example/ ASN1START",
	//   "-- /bad example/ ASN1STOP", optionally with trailing text.
	// Anchored at start (after NBSP normalization + TrimSpace) so prose that
	// merely mentions ASN1START (e.g. TS 38.331 A.1 explains the tagging
	// convention) never matches.
	asn1StartRE = regexp.MustCompile(`^--\s*(?:/[^/]*/\s*)?ASN1START\b`)
	asn1StopRE  = regexp.MustCompile(`^--\s*(?:/[^/]*/\s*)?ASN1STOP\b`)

	// Diameter Command Code Format (RFC 6733 clause 3.2) definition header:
	//   < Update-Location-Request> ::=	< Diameter Header: 316, REQ, PXY, 16777251 >
	//   Subscription-Data ::= <AVP header: 1400 10415>
	//   MIP6-Agent-Info ::=< AVP Header: 486 >
	// Case and spacing vary between specs. Requiring "< Diameter|AVP Header:"
	// after "::=" keeps ASN.1 value assignments and prose that merely contains
	// "::=" from matching.
	diameterDefRE = regexp.MustCompile(`(?i)::=\s*<\s*(?:diameter|avp)\s+header\s*:`)
	// One AVP reference line of a definition: an optional multiplicity
	// qualifier (RFC 6733 qual: [min] "*" [max]) followed by exactly one
	// fixed < >, mandatory { } or optional [ ] AVP reference.
	diameterAVPLineRE = regexp.MustCompile(`^[ \t]*(?:\d+\*\d*|\*\d*|\d+)?[ \t]*(?:<[^<>]+>|\{[^{}]+\}|\[[^\][]+\])[ \t]*$`)
)

// matchASN1Marker reports whether the paragraph is an ASN.1 extraction marker.
// NBSP is normalized to a space first: Go's \s does not match U+00A0, which
// 3GPP documents use liberally.
func matchASN1Marker(re *regexp.Regexp, info paragraphInfo) bool {
	text := strings.ReplaceAll(codeLineText(info), "\u00a0", " ")
	return re.MatchString(strings.TrimSpace(text))
}

// matchDiameterStart reports whether the paragraph is a Diameter command or
// grouped-AVP definition header. Diameter specs style these as plain body
// paragraphs (no code font or style), so detection is content-based like the
// ASN.1 markers. NBSP is normalized for the same reason as matchASN1Marker.
func matchDiameterStart(info paragraphInfo) bool {
	text := strings.ReplaceAll(codeLineText(info), "\u00a0", " ")
	return diameterDefRE.MatchString(text)
}

// matchDiameterLine reports whether the paragraph continues a Diameter
// definition: an AVP reference line, or another definition header (so that
// consecutive grouped-AVP definitions in one clause merge into one block).
func matchDiameterLine(info paragraphInfo) bool {
	text := strings.ReplaceAll(codeLineText(info), "\u00a0", " ")
	return diameterAVPLineRE.MatchString(text) || diameterDefRE.MatchString(text)
}

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
	styleMap, codeStyles, err := parseStyles(stylesData)
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
	sections := parseSections(bodyElements, styleMap, codeStyles, relMap, images)

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
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			// A decode error means the document is truncated or malformed.
			// Returning the partial elements as a success would let a
			// half-parsed document replace a previously complete import.
			return nil, fmt.Errorf("decode document.xml: %w", err)
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
	// Every format gets the same markdown link, EMF/WMF included, so the two
	// conversion paths (prebuilt DB and on-demand archive fetch) emit
	// byte-identical section text and compare_versions sees no notation noise.
	alt := "Figure"
	if ref.AltText != "" {
		alt = ref.AltText
	}
	return fmt.Sprintf("![%s](image://%s%s)", alt, img.Name, dimSuffix)
}

// diagramPlaceholder returns a markdown placeholder for a grouped vector
// diagram that had no embeddable raster image (see paragraphInfo.
// SkippedDiagramLabels). Unlike imagePlaceholder, get_image can't retrieve
// this figure since there is no extracted image file for it — the
// placeholder says so, and lists any text-box labels found so the
// information isn't lost entirely, in document order (not necessarily the
// diagram's visual reading order).
func diagramPlaceholder(labels []string) string {
	if len(labels) == 0 {
		return "[Figure: diagram not extracted — this converter cannot render grouped vector shapes/text boxes; see the original document for this figure]"
	}
	return fmt.Sprintf(
		"[Figure: diagram not extracted (vector shapes/arrows not rendered); extracted text labels, order not guaranteed: %s]",
		strings.Join(labels, "; "))
}

// parseSections walks the body elements and creates a section hierarchy.
func parseSections(elements []bodyElement, styleMap map[string]string, codeStyles map[string]bool, relMap map[string]string, images map[string]*EmbeddedImage) []*Section {
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
				"```\n"+strings.Join(codeBuffer, "\n")+"\n```")
		}
		codeBuffer = nil
	}

	// Accumulates paragraphs between "-- ASN1START" and "-- ASN1STOP" markers
	// (the normative 3GPP ASN.1 extraction convention) into one ```asn1 fence.
	// While capturing, paragraphs are taken verbatim regardless of style — the
	// markers are authoritative. Markers inside table cells are not detected;
	// 3GPP specs do not put ASN.1 modules in tables.
	var asn1Buffer []string
	inASN1 := false
	flushASN1 := func() {
		inASN1 = false
		if len(asn1Buffer) == 0 || currentSection == nil {
			asn1Buffer = nil
			return
		}
		for len(asn1Buffer) > 0 && strings.TrimSpace(asn1Buffer[len(asn1Buffer)-1]) == "" {
			asn1Buffer = asn1Buffer[:len(asn1Buffer)-1]
		}
		if len(asn1Buffer) > 0 {
			currentSection.Content = append(currentSection.Content,
				"```asn1\n"+strings.Join(asn1Buffer, "\n")+"\n```")
		}
		asn1Buffer = nil
	}

	// Accumulates Diameter command/grouped-AVP definitions into one
	// ```diameter fence. Their paragraphs carry no code style or font in the
	// source documents, so capture is triggered by content: a definition
	// header starts it, AVP reference lines continue it, and the first
	// paragraph that is neither ends it (see matchDiameterStart/Line).
	var diameterBuffer []string
	inDiameter := false
	flushDiameter := func() {
		inDiameter = false
		if len(diameterBuffer) == 0 || currentSection == nil {
			diameterBuffer = nil
			return
		}
		for len(diameterBuffer) > 0 && strings.TrimSpace(diameterBuffer[len(diameterBuffer)-1]) == "" {
			diameterBuffer = diameterBuffer[:len(diameterBuffer)-1]
		}
		if len(diameterBuffer) > 0 {
			currentSection.Content = append(currentSection.Content,
				"```diameter\n"+strings.Join(diameterBuffer, "\n")+"\n```")
		}
		diameterBuffer = nil
	}

	// emitParagraph renders a paragraph through the normal (non-code) path.
	// Split out so an abandoned XML-opener candidate (see below) replays
	// through exactly the same logic.
	emitParagraph := func(info paragraphInfo, styleName string) {
		flushCodeBlock()
		if currentSection != nil {
			blocks := paragraphToMarkdownBlocks(info, styleName, func(ref imageRef) string {
				if relMap == nil {
					return ""
				}
				return imagePlaceholder(relMap, images, ref)
			})
			currentSection.Content = append(currentSection.Content, blocks...)
		}
		// Surface any grouped vector diagram this converter couldn't render
		// as an image (see issue #25), instead of silently dropping it.
		if currentSection != nil && info.SkippedDiagramLabels != nil {
			currentSection.Content = append(currentSection.Content, diagramPlaceholder(info.SkippedDiagramLabels))
		}
	}

	// Accumulates XML/DTD blocks into one ```xml fence. Like the Diameter
	// definitions these paragraphs carry no code style or font, so capture is
	// content-based (see xmlblock.go): an XML declaration or DOCTYPE opens a
	// block on its own, an ordinary tag/comment line only when the next
	// paragraph also looks like XML — until then it is held in xmlPending and
	// replayed as a normal paragraph if the second line never comes.
	var xmlBuffer []string
	var xmlTracker xmlLineTracker
	var xmlPending *paragraphInfo
	var xmlPendingStyle string
	inXML := false
	// Paragraphs absorbed only because an element is still open (they carry no
	// markup of their own) are held here instead of going straight into the
	// fence: a block whose closing tag never comes must not turn the rest of
	// the clause into code (issue #136). They join the fence only once the
	// element that was open when holding started — recorded in xmlHeldDepth —
	// is closed, which is what proves they were its content; if the block ends
	// first they are replayed as the ordinary paragraphs they are.
	var xmlHeld []paragraphInfo
	var xmlHeldStyles []string
	xmlHeldDepth := 0
	holdXMLLine := func(info paragraphInfo, styleName string) {
		xmlHeld = append(xmlHeld, info)
		xmlHeldStyles = append(xmlHeldStyles, styleName)
	}
	commitXMLHeld := func() {
		for _, info := range xmlHeld {
			xmlBuffer = append(xmlBuffer, codeLineText(info))
		}
		xmlHeld, xmlHeldStyles = nil, nil
	}
	flushXML := func() {
		inXML = false
		xmlTracker = xmlLineTracker{}
		held, heldStyles := xmlHeld, xmlHeldStyles
		xmlHeld, xmlHeldStyles = nil, nil
		if len(xmlBuffer) > 0 && currentSection != nil {
			for len(xmlBuffer) > 0 && strings.TrimSpace(xmlBuffer[len(xmlBuffer)-1]) == "" {
				xmlBuffer = xmlBuffer[:len(xmlBuffer)-1]
			}
			if len(xmlBuffer) > 0 {
				currentSection.Content = append(currentSection.Content,
					"```xml\n"+strings.Join(xmlBuffer, "\n")+"\n```")
			}
		}
		xmlBuffer = nil
		// The unconfirmed lines belong after the fence, as the prose they are.
		for i, info := range held {
			emitParagraph(info, heldStyles[i])
		}
	}
	abandonXMLPending := func() {
		if xmlPending == nil {
			return
		}
		emitParagraph(*xmlPending, xmlPendingStyle)
		xmlPending = nil
		xmlTracker = xmlLineTracker{}
	}

	// Accumulates SIP message and standalone SDP examples into one tagged
	// fence — ```sip for message blocks, ```sdp for SDP-only blocks, both
	// highlighted by the web viewer's SIP lexer (web/siplexer.go). Like the
	// Diameter definitions, these carry no code style or font in several
	// specs, so capture is content-based: a SIP request/status line or an
	// SDP field-line run starts it, and the first paragraph that no longer
	// looks like part of the message ends it (see sipblock.go).
	var sipBuffer []string
	inSIP := false
	sipSDPOnly := false
	flushSIP := func() {
		tag := "sip"
		if sipSDPOnly {
			tag = "sdp"
		}
		inSIP = false
		sipSDPOnly = false
		if len(sipBuffer) == 0 || currentSection == nil {
			sipBuffer = nil
			return
		}
		// Trim trailing blank lines, including a soft line break at the end
		// of the final absorbed paragraph.
		body := strings.TrimRight(strings.Join(sipBuffer, "\n"), " \t\n")
		if body != "" {
			currentSection.Content = append(currentSection.Content,
				"```"+tag+"\n"+body+"\n```")
		}
		sipBuffer = nil
	}

	for i, elem := range elements {
		switch elem.Tag {
		case "tbl":
			// A table while capturing ASN.1 means a missing ASN1STOP; flush
			// what was collected rather than swallowing the table.
			flushASN1()
			flushDiameter()
			abandonXMLPending()
			flushXML()
			flushSIP()
			flushCodeBlock()
			html := tableToHTML(elem.Table, imageContext{relMap: relMap, images: images})
			if html != "" && currentSection != nil {
				currentSection.Content = append(currentSection.Content, html)
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
				// A heading while capturing ASN.1 means a missing ASN1STOP;
				// flush so headings are never swallowed into a code block.
				flushASN1()
				flushDiameter()
				abandonXMLPending()
				flushXML()
				flushSIP()
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
				} else if match := unnumberedHeadingRE.FindStringSubmatch(text); match != nil {
					// Some specs (e.g. TS 38.331's IE/message annex) mark
					// clauses with a bare dash instead of a decimal number.
					// There's no real section number here, so reuse the
					// title as the storage key (see Section.Number docs).
					// Known limitation: two such headings in the same spec
					// with an identical title collide on this key, and the
					// second one silently overwrites the first in the DB
					// (pre-existing risk, shared with the raw-text fallback
					// below; not expected in practice since these headings
					// name distinct IEs/messages).
					title = strings.TrimSpace(match[1])
					number = title
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
				// Computed before the XML pending/continuation checks: content
				// detection must never capture an already code-styled
				// paragraph — those keep their bare ``` fences.
				isCodePara := (info.IsCode || isCodeStyleName(styleName) || codeStyles[info.StyleID]) && len(info.Images) == 0
				if xmlPending != nil {
					// The commit test deliberately ignores element depth
					// (matchXMLLine, not matchXMLContinuation): a quoted
					// "<userid>" opener in prose must not pull the following
					// prose paragraph into a fence.
					if !isCodePara && len(info.Images) == 0 && matchXMLLine(info, &xmlTracker) {
						// Second consecutive XML-looking paragraph: commit the
						// held opener and the current line to an XML block.
						flushCodeBlock()
						inXML = true
						line := codeLineText(info)
						xmlBuffer = append(xmlBuffer, codeLineText(*xmlPending), line)
						xmlTracker.observe(line)
						xmlPending = nil
						continue
					}
					abandonXMLPending()
				}
				if inASN1 {
					// Capture verbatim (tabs and indentation kept, blank
					// paragraphs become blank lines, the STOP marker line
					// included) until the closing marker.
					asn1Buffer = append(asn1Buffer, codeLineText(info))
					if matchASN1Marker(asn1StopRE, info) {
						flushASN1()
					}
					continue
				}
				if matchASN1Marker(asn1StartRE, info) {
					flushDiameter()
					flushXML()
					flushSIP()
					flushCodeBlock()
					inASN1 = true
					asn1Buffer = append(asn1Buffer, codeLineText(info))
					continue
				}
				if inXML {
					switch {
					case strings.TrimSpace(info.Text) == "" && len(info.Images) == 0:
						// Preserve blank lines inside a pending block
						// (whitespace-only paragraphs included, so indentation
						// filler does not split the fence); trailing ones are
						// trimmed at flush. A blank line after unconfirmed
						// content is held with it, so the two keep their order.
						if len(xmlHeld) > 0 {
							holdXMLLine(info, styleName)
						} else {
							xmlBuffer = append(xmlBuffer, "")
						}
						continue
					case !isCodePara && len(info.Images) == 0 && matchXMLContinuation(info, &xmlTracker):
						line := codeLineText(info)
						// Observe on a copy first: whether this line closes an
						// element decides where it and anything held belong, and
						// that is only known after the line has been parsed.
						// minDepth, not the depth left at the end of the line,
						// is what answers it — "</a><b>" closes a and opens b.
						probe := xmlTracker
						probe.observe(line)
						isMarkup := matchXMLLine(info, &xmlTracker)
						switch {
						case len(xmlHeld) > 0:
							// Only the close of the element that was open when
							// holding started proves the held paragraphs were
							// its content. Any other line — including a tag that
							// merely opens something new — is held as well, so
							// document order survives and unrelated markup later
							// in the clause cannot fence the prose in between.
							// Nesting opened and closed while holding never
							// reaches below xmlHeldDepth, so it neither confirms
							// nor disturbs the wait; comments, CDATA and
							// self-closing tags leave the depth alone entirely.
							if probe.minDepth < xmlHeldDepth {
								commitXMLHeld()
								xmlBuffer = append(xmlBuffer, line)
							} else {
								holdXMLLine(info, styleName)
							}
						case isMarkup:
							// Markup in its own right: straight into the fence.
							xmlBuffer = append(xmlBuffer, line)
						case probe.minDepth < xmlTracker.depth:
							// Text absorbed by an open element that this very
							// paragraph closes ("some text </a>"): already
							// confirmed, so there is nothing to hold.
							xmlBuffer = append(xmlBuffer, line)
						default:
							// Absorbed only on the strength of an open element,
							// so hold it until that element closes. xmlHeldDepth
							// is written whenever holding starts from empty and
							// read only while xmlHeld is non-empty, so it can
							// never be stale.
							xmlHeldDepth = xmlTracker.depth
							holdXMLLine(info, styleName)
						}
						xmlTracker = probe
						continue
					default:
						// First non-matching (or code-styled) paragraph ends
						// the block and is handled normally below.
						flushXML()
					}
				}
				if inSIP {
					continues := sipBlockContinues(info, sipLastBufferedLine(sipBuffer))
					if sipSDPOnly {
						continues = sdpBlockContinues(info, sipLastBufferedLine(sipBuffer))
					}
					if continues {
						if strings.TrimSpace(codeLineText(info)) == "" {
							// Preserve blank lines inside a pending block;
							// trailing ones are trimmed at flush.
							sipBuffer = append(sipBuffer, "")
						} else {
							sipBuffer = append(sipBuffer, codeLineText(info))
						}
						continue
					}
					// First non-matching paragraph ends the block and is
					// handled normally below.
					flushSIP()
				}
				if inDiameter {
					switch {
					case info.Text == "" && len(info.Images) == 0:
						// Preserve blank lines inside a pending block;
						// trailing ones are trimmed at flush.
						diameterBuffer = append(diameterBuffer, "")
						continue
					case matchDiameterLine(info) && len(info.Images) == 0:
						diameterBuffer = append(diameterBuffer, codeLineText(info))
						continue
					default:
						// First non-matching paragraph ends the block and is
						// handled normally below.
						flushDiameter()
					}
				}
				if matchDiameterStart(info) && len(info.Images) == 0 && currentSection != nil {
					// Before the isCodePara path so a code-styled definition
					// starts a tagged fence instead of a bare one.
					flushCodeBlock()
					inDiameter = true
					diameterBuffer = append(diameterBuffer, codeLineText(info))
					continue
				}
				// Content-based XML detection only applies to paragraphs the
				// style/font paths would render as prose: specs whose XML is
				// already code-styled keep their existing bare ``` fences.
				if !isCodePara && len(info.Images) == 0 && currentSection != nil {
					if matchXMLStrongStart(info) {
						flushCodeBlock()
						inXML = true
						line := codeLineText(info)
						xmlBuffer = append(xmlBuffer, line)
						xmlTracker = xmlLineTracker{}
						xmlTracker.observe(line)
						continue
					}
					if matchXMLCandidateStart(info) {
						p := info
						xmlPending = &p
						xmlPendingStyle = styleName
						xmlTracker = xmlLineTracker{}
						xmlTracker.observe(codeLineText(info))
						continue
					}
				}
				// Content-based SIP/SDP example detection applies only to
				// paragraphs that no style-based path claims, so specs whose
				// examples are monospace-styled keep their existing fenced
				// output unchanged.
				if !isCodePara && currentSection != nil && len(info.Images) == 0 && info.SkippedDiagramLabels == nil {
					label, text, ok := sipExampleStart(info)
					if ok {
						inSIP = true
					} else if label, text, ok = sdpExampleStart(elements, i); ok {
						inSIP, sipSDPOnly = true, true
					}
					if ok {
						flushCodeBlock()
						if label != "" {
							currentSection.Content = append(currentSection.Content, label)
						}
						sipBuffer = append(sipBuffer, text)
						continue
					}
				}
				// A standalone equation becomes its own ```latex block. This
				// is decided last, so a formula inside an ASN.1 or XML
				// listing still belongs to that listing.
				mathBody, isMathFence := "", false
				if currentSection != nil && !isCodePara {
					mathBody, isMathFence = mathFenceBody(info, styleName)
				}
				switch {
				case isCodePara && currentSection != nil:
					// Append to the pending code block, preserving whitespace.
					codeBuffer = append(codeBuffer, codeLineText(info))
				case isMathFence:
					flushCodeBlock()
					currentSection.Content = append(currentSection.Content,
						"```latex\n"+mathBody+"\n```")
				case info.Text == "" && len(info.Images) == 0 && len(codeBuffer) > 0:
					// Preserve blank lines inside a pending code block.
					codeBuffer = append(codeBuffer, "")
				default:
					emitParagraph(info, styleName)
				}
			}
		}
	}

	flushASN1()
	flushDiameter()
	abandonXMLPending()
	flushXML()
	flushSIP()
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
			sb.WriteString(r.markdownText())
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
