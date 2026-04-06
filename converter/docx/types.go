package docx

import "regexp"

// SpecMetadata holds metadata extracted from a 3GPP specification document.
type SpecMetadata struct {
	SpecID  string // e.g., "TS 23.501"
	Title   string
	Version string // e.g., "k10" or "18.6.0"
	Release string // e.g., "19"
}

// Series extracts the series number from the spec ID (e.g., "23" from "TS 23.501").
func (m *SpecMetadata) Series() string {
	match := seriesRE.FindStringSubmatch(m.SpecID)
	if match != nil {
		return match[1]
	}
	return ""
}

var seriesRE = regexp.MustCompile(`(\d+)\.\d+`)

// Section represents a parsed section from the specification document.
type Section struct {
	Number       string
	Title        string
	Level        int
	Content      []string
	Children     []*Section
	ParentNumber string
}

// imageRef holds a reference to an embedded image found in a paragraph.
type imageRef struct {
	RID      string // relationship ID (e.g., "rId9")
	AltText  string // optional alt text / title
	WidthPx  int    // display width in CSS pixels (0 = unknown)
	HeightPx int    // display height in CSS pixels (0 = unknown)
}

// EmbeddedImage holds an image extracted from a DOCX file.
type EmbeddedImage struct {
	Name        string // filename within DOCX (e.g., "image1.emf")
	MIMEType    string // e.g., "image/png", "image/x-emf"
	Data        []byte // raw image data
	LLMReadable bool   // true for PNG/JPEG/GIF/WebP
}

// ParseResult bundles all outputs from parsing a DOCX file.
type ParseResult struct {
	Metadata *SpecMetadata
	Sections []*Section
	Images   []*EmbeddedImage
}
