package docx

import (
	"bytes"
	"encoding/xml"
	"fmt"
)

// headingStyles maps style names to heading levels.
var headingStyles = map[string]int{
	"ANNEX heading": 1,
	"Annex heading": 1,
	"annex heading": 1,
}

func init() {
	for i := 1; i <= 9; i++ {
		headingStyles[fmt.Sprintf("Heading %d", i)] = i
		headingStyles[fmt.Sprintf("heading %d", i)] = i
	}
}

// styleDef accumulates the parts of a <w:style> definition needed to resolve
// whether paragraphs using it should be treated as code.
type styleDef struct {
	basedOn  string
	hasFonts bool
	mono     bool
}

// parseStyles parses word/styles.xml content and returns a map from style ID
// to style name, plus the set of style IDs whose resolved run properties
// (following w:basedOn, falling back to w:docDefaults) declare a monospace
// font. 3GPP templates declare the ASN.1 code font on the style definition
// (e.g. style "PL" uses Courier New), not on each paragraph, so paragraph-level
// font detection alone never sees it.
func parseStyles(data []byte) (map[string]string, map[string]bool, error) {
	if len(data) == 0 {
		return nil, nil, nil
	}

	// Use streaming parser for robust namespace handling
	decoder := xml.NewDecoder(bytes.NewReader(data))
	m := make(map[string]string)
	defs := make(map[string]*styleDef)
	var currentStyleID string
	var inStyle, inDocDefaults bool
	var defaultMono bool

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "docDefaults":
				inDocDefaults = true
			case "style":
				inStyle = true
				currentStyleID = ""
				for _, a := range t.Attr {
					if a.Name.Local == "styleId" {
						currentStyleID = a.Value
					}
				}
				if currentStyleID != "" {
					defs[currentStyleID] = &styleDef{}
				}
			case "name":
				if inStyle && currentStyleID != "" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							m[currentStyleID] = a.Value
						}
					}
				}
			case "basedOn":
				if inStyle && currentStyleID != "" {
					defs[currentStyleID].basedOn = getAttrVal(t, "val")
				}
			case "rFonts":
				// Within w:style, w:rFonts only occurs under w:rPr, so no
				// extra rPr context tracking is needed.
				mono := false
				for _, a := range t.Attr {
					if isMonospaceFont(a.Value) {
						mono = true
						break
					}
				}
				switch {
				case inStyle && currentStyleID != "":
					defs[currentStyleID].hasFonts = true
					defs[currentStyleID].mono = mono
				case inDocDefaults:
					defaultMono = mono
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "docDefaults":
				inDocDefaults = false
			case "style":
				inStyle = false
			}
		}
	}

	codeStyles := make(map[string]bool)
	for id := range defs {
		if resolveStyleMono(id, defs, defaultMono) {
			codeStyles[id] = true
		}
	}

	return m, codeStyles, nil
}

// resolveStyleMono walks a style's w:basedOn chain (self first) and returns
// the monospace flag of the nearest style that declares fonts, falling back
// to the document default when none does.
func resolveStyleMono(id string, defs map[string]*styleDef, defaultMono bool) bool {
	visited := make(map[string]bool)
	for id != "" && !visited[id] {
		visited[id] = true
		def, ok := defs[id]
		if !ok {
			break
		}
		if def.hasFonts {
			return def.mono
		}
		id = def.basedOn
	}
	return defaultMono
}

// resolveStyleName resolves a style ID to its display name using the style map.
func resolveStyleName(styleID string, styleMap map[string]string) string {
	if name, ok := styleMap[styleID]; ok {
		return name
	}
	return styleID
}
