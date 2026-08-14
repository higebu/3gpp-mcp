package docx

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"strings"
)

// parseRelationships parses word/_rels/document.xml.rels and returns a map
// from relationship ID (e.g., "rId9") to the target path (e.g., "media/image1.emf").
// Only image relationships are included.
func parseRelationships(r *zip.Reader) (map[string]string, error) {
	data, err := readZipFile(r, "word/_rels/document.xml.rels")
	if err != nil {
		return nil, fmt.Errorf("read document.xml.rels: %w", err)
	}

	type relationship struct {
		ID     string `xml:"Id,attr"`
		Type   string `xml:"Type,attr"`
		Target string `xml:"Target,attr"`
	}
	type relationships struct {
		XMLName xml.Name       `xml:"Relationships"`
		Rels    []relationship `xml:"Relationship"`
	}

	var rels relationships
	if err := xml.Unmarshal(data, &rels); err != nil {
		return nil, fmt.Errorf("parse document.xml.rels: %w", err)
	}

	result := make(map[string]string)
	for _, rel := range rels.Rels {
		if strings.HasSuffix(rel.Type, "/image") {
			result[rel.ID] = rel.Target
		}
	}
	return result, nil
}
