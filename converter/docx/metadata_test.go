package docx

import (
	"testing"
)

func TestParseCoreProperties(t *testing.T) {
	xml := `<?xml version="1.0" encoding="UTF-8"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"
    xmlns:dc="http://purl.org/dc/elements/1.1/">
  <dc:title>System architecture for 5G</dc:title>
  <dc:subject>5G architecture; Stage 2 (Release 18)</dc:subject>
</cp:coreProperties>`
	props := parseCoreProperties([]byte(xml))
	if props.Title != "System architecture for 5G" {
		t.Errorf("Title = %q", props.Title)
	}
	if props.Subject != "5G architecture; Stage 2 (Release 18)" {
		t.Errorf("Subject = %q", props.Subject)
	}
}

func TestParseCoreProperties_Empty(t *testing.T) {
	props := parseCoreProperties([]byte{})
	if props.Title != "" || props.Subject != "" {
		t.Errorf("expected empty props, got %+v", props)
	}
}

func TestParseCoreProperties_Malformed(t *testing.T) {
	props := parseCoreProperties([]byte("not xml"))
	if props.Title != "" || props.Subject != "" {
		t.Errorf("expected empty props, got %+v", props)
	}
}

func TestIsTemplateValue(t *testing.T) {
	cases := map[string]bool{
		"":                            true,
		"<Title of Document>":         true,
		"contains <Title placeholder": true,
		"prefix ab.cde suffix":        true,
		"System architecture":         false,
		"TS 23.501":                   false,
	}
	for input, want := range cases {
		if got := isTemplateValue(input); got != want {
			t.Errorf("isTemplateValue(%q) = %v, want %v", input, got, want)
		}
	}
}

func TestExtractMetadata_FilenameVariants(t *testing.T) {
	tests := []struct {
		name        string
		filename    string
		wantSpecID  string
		wantVersion string
	}{
		{
			name:        "TS series with hyphen and letter version",
			filename:    "23501-i30.docx",
			wantSpecID:  "TS 23.501",
			wantVersion: "i30",
		},
		{
			name:        "TR series with hyphen and letter version",
			filename:    "24229-h50.doc",
			wantSpecID:  "TS 24.229",
			wantVersion: "h50",
		},
		{
			name:        "high series",
			filename:    "29510-f60.zip",
			wantSpecID:  "TS 29.510",
			wantVersion: "f60",
		},
		{
			name:        "no extension",
			filename:    "33401-i00",
			wantSpecID:  "TS 33.401",
			wantVersion: "i00",
		},
		{
			name:        "non-standard falls back to stem",
			filename:    "weirdname.docx",
			wantSpecID:  "weirdname",
			wantVersion: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := extractMetadata(tt.filename, coreProperties{}, nil, nil)
			if meta.SpecID != tt.wantSpecID {
				t.Errorf("SpecID = %q, want %q", meta.SpecID, tt.wantSpecID)
			}
			if meta.Version != tt.wantVersion {
				t.Errorf("Version = %q, want %q", meta.Version, tt.wantVersion)
			}
		})
	}
}

func TestExtractMetadata_SubjectWithRelease(t *testing.T) {
	props := coreProperties{
		Subject: "5G System architecture (Release 18)",
	}
	meta := extractMetadata("23501-i30.docx", props, nil, nil)
	if meta.Title != "5G System architecture" {
		t.Errorf("Title = %q", meta.Title)
	}
	if meta.Release != "18" {
		t.Errorf("Release = %q, want 18", meta.Release)
	}
}

func TestExtractMetadata_TitleFromTitleProperty(t *testing.T) {
	props := coreProperties{
		Title: "Network Function Repository Services",
	}
	meta := extractMetadata("29510-i00.docx", props, nil, nil)
	if meta.Title != "Network Function Repository Services" {
		t.Errorf("Title = %q", meta.Title)
	}
}

func TestExtractMetadata_TemplatePropsIgnored(t *testing.T) {
	props := coreProperties{
		Title:   "<Title of the document>",
		Subject: "",
	}
	meta := extractMetadata("23501-i30.docx", props, nil, nil)
	if meta.Title != "TS 23.501" {
		// Should fall back to specID since both props are template/empty.
		t.Errorf("Title = %q, want fallback to spec ID", meta.Title)
	}
}

func TestExtractMetadata_VersionFromBody(t *testing.T) {
	bodyElements := []bodyElement{
		{
			Tag: "p",
			Paragraph: paragraphInfo{
				Text: "V18.6.0",
				Runs: []runInfo{{Text: "V18.6.0"}},
			},
		},
		{
			Tag: "p",
			Paragraph: paragraphInfo{
				Text: "Release 18",
				Runs: []runInfo{{Text: "Release 18"}},
			},
		},
	}
	meta := extractMetadata("weirdname.docx", coreProperties{}, bodyElements, map[string]string{})
	if meta.Version != "18.6.0" {
		t.Errorf("Version = %q, want 18.6.0", meta.Version)
	}
	if meta.Release != "18" {
		t.Errorf("Release = %q, want 18", meta.Release)
	}
}

func TestExtractMetadataFromBody_ZTStyle(t *testing.T) {
	styleMap := map[string]string{
		"ZA": "ZA",
		"ZT": "ZT",
	}
	elements := []bodyElement{
		{
			Tag: "p",
			Paragraph: paragraphInfo{
				StyleID: "ZT",
				Text:    "3rd Generation Partnership Project;",
				Runs:    []runInfo{{Text: "3rd Generation Partnership Project;"}},
			},
		},
		{
			Tag: "p",
			Paragraph: paragraphInfo{
				StyleID: "ZT",
				Text:    "Technical Specification Group Services and System Aspects;",
				Runs:    []runInfo{{Text: "Technical Specification Group Services and System Aspects;"}},
			},
		},
		{
			Tag: "p",
			Paragraph: paragraphInfo{
				StyleID: "ZT",
				Text:    "System architecture for the 5G System (5GS);",
				Runs:    []runInfo{{Text: "System architecture for the 5G System (5GS);"}},
			},
		},
		{
			Tag: "p",
			Paragraph: paragraphInfo{
				StyleID: "ZT",
				Text:    "Stage 2",
				Runs:    []runInfo{{Text: "Stage 2"}},
			},
		},
		{
			Tag: "p",
			Paragraph: paragraphInfo{
				StyleID: "ZT",
				Text:    "(Release 18)",
				Runs:    []runInfo{{Text: "(Release 18)"}},
			},
		},
	}
	title, release := extractMetadataFromBody(elements, styleMap)
	if title == "" {
		t.Error("expected non-empty title")
	}
	if release != "18" {
		t.Errorf("Release = %q, want 18", release)
	}
}

func TestIsCoverPageEnd(t *testing.T) {
	cases := map[string]bool{
		"TT":          true,
		"TOC heading": true,
		"FP":          true,
		"toc 1":       true,
		"TOC1":        true,
		"Heading 1":   true,
		"Heading 9":   true,
		"Normal":      false,
		"ZT":          false,
		"":            false,
	}
	for in, want := range cases {
		if got := isCoverPageEnd(in); got != want {
			t.Errorf("isCoverPageEnd(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestSpecMetadata_Series(t *testing.T) {
	cases := map[string]string{
		"TS 23.501":  "23",
		"TS 29.510":  "29",
		"TR 38.901":  "38",
		"weirdname":  "",
		"TS without": "",
	}
	for specID, want := range cases {
		m := &SpecMetadata{SpecID: specID}
		if got := m.Series(); got != want {
			t.Errorf("Series for %q = %q, want %q", specID, got, want)
		}
	}
}
