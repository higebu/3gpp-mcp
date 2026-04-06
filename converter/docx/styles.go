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

// parseStyles parses word/styles.xml content and returns a map from style ID to style name.
func parseStyles(data []byte) (map[string]string, error) {
	if len(data) == 0 {
		return nil, nil
	}

	// Use streaming parser for robust namespace handling
	decoder := xml.NewDecoder(bytes.NewReader(data))
	m := make(map[string]string)
	var currentStyleID string
	var inStyle bool

	for {
		tok, err := decoder.Token()
		if err != nil {
			break
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "style":
				inStyle = true
				currentStyleID = ""
				for _, a := range t.Attr {
					if a.Name.Local == "styleId" {
						currentStyleID = a.Value
					}
				}
			case "name":
				if inStyle && currentStyleID != "" {
					for _, a := range t.Attr {
						if a.Name.Local == "val" {
							m[currentStyleID] = a.Value
						}
					}
				}
			}
		case xml.EndElement:
			if t.Name.Local == "style" {
				inStyle = false
			}
		}
	}

	return m, nil
}

// resolveStyleName resolves a style ID to its display name using the style map.
func resolveStyleName(styleID string, styleMap map[string]string) string {
	if name, ok := styleMap[styleID]; ok {
		return name
	}
	return styleID
}
