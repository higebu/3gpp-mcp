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
		wantToken   string
	}{
		{
			name:        "TS series with hyphen and letter version",
			filename:    "23501-i30.docx",
			wantSpecID:  "TS 23.501",
			wantVersion: "18.3.0",
			wantToken:   "i30",
		},
		{
			name:        "24 series with hyphen and letter version",
			filename:    "24229-h50.doc",
			wantSpecID:  "TS 24.229",
			wantVersion: "17.5.0",
			wantToken:   "h50",
		},
		{
			name:        "high series",
			filename:    "29510-f60.zip",
			wantSpecID:  "TS 29.510",
			wantVersion: "15.6.0",
			wantToken:   "f60",
		},
		{
			name:        "no extension",
			filename:    "33401-i00",
			wantSpecID:  "TS 33.401",
			wantVersion: "18.0.0",
			wantToken:   "i00",
		},
		{
			name:        "non-standard falls back to stem",
			filename:    "weirdname.docx",
			wantSpecID:  "weirdname",
			wantVersion: "",
			wantToken:   "",
		},
		{
			// The stem pattern stays permissive: a type token glued to the
			// preceding word still normalizes.
			name:        "spec token glued to a word in the stem",
			filename:    "draftTS23.501.docx",
			wantSpecID:  "TS 23.501",
			wantVersion: "",
			wantToken:   "",
		},
		{
			name:        "split multi-file spec body chunk",
			filename:    "38101-1-k00_s00-05.docx",
			wantSpecID:  "TS 38.101-1",
			wantVersion: "20.0.0",
			wantToken:   "k00",
		},
		{
			name:        "split multi-file spec annex chunk",
			filename:    "38101-1-k00_sAnnexes.docx",
			wantSpecID:  "TS 38.101-1",
			wantVersion: "20.0.0",
			wantToken:   "k00",
		},
		{
			name:        "base-36 version token with letter in second position",
			filename:    "23222-ja0.docx",
			wantSpecID:  "TS 23.222",
			wantVersion: "19.10.0",
			wantToken:   "ja0",
		},
		{
			name:        "base-36 version token, minor version b",
			filename:    "23280-ja1.docx",
			wantSpecID:  "TS 23.280",
			wantVersion: "19.10.1",
			wantToken:   "ja1",
		},
		{
			name:        "base-36 version token, different minor letter",
			filename:    "23379-jb0.docx",
			wantSpecID:  "TS 23.379",
			wantVersion: "19.11.0",
			wantToken:   "jb0",
		},
		{
			name:        "legacy digit-leading version token",
			filename:    "23060-920.docx",
			wantSpecID:  "TS 23.060",
			wantVersion: "9.2.0",
			wantToken:   "920",
		},
		{
			name:        "multi-part spec with digit-leading token",
			filename:    "38101-1-100.docx",
			wantSpecID:  "TS 38.101-1",
			wantVersion: "1.0.0",
			wantToken:   "100",
		},
		{
			name:        "digit-leading token in split body chunk",
			filename:    "36133-920_s00-11.docx",
			wantSpecID:  "TS 36.133",
			wantVersion: "9.2.0",
			wantToken:   "920",
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
			if meta.VersionToken != tt.wantToken {
				t.Errorf("VersionToken = %q, want %q", meta.VersionToken, tt.wantToken)
			}
		})
	}
}

// coverPara builds a cover-page paragraph body element for doc-type tests.
func coverPara(style, text string) bodyElement {
	return bodyElement{
		Tag: "p",
		Paragraph: paragraphInfo{
			StyleID: style,
			Text:    text,
			Runs:    []runInfo{{Text: text}},
		},
	}
}

// The archive filename never distinguishes a Technical Specification from a
// Technical Report, so the document itself has to say which it is (#110).
func TestExtractMetadata_DocTypeDetection(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		props      coreProperties
		body       []bodyElement
		wantSpecID string
		wantType   string
	}{
		{
			name:       "TR marker on cover page",
			filename:   "21905-h20.docx",
			body:       []bodyElement{coverPara("ZA", "3GPP TR 21.905 V17.2.0 (2022-03)")},
			wantSpecID: "TR 21.905",
			wantType:   "TR",
		},
		{
			name:       "TS marker on cover page",
			filename:   "23501-i30.docx",
			body:       []bodyElement{coverPara("ZA", "3GPP TS 23.501 V18.3.0 (2023-09)")},
			wantSpecID: "TS 23.501",
			wantType:   "TS",
		},
		{
			name:       "multi-part TR keeps its part suffix",
			filename:   "23700-49-i00.docx",
			body:       []bodyElement{coverPara("ZA", "3GPP TR 23.700-49 V18.0.0 (2023-03)")},
			wantSpecID: "TR 23.700-49",
			wantType:   "TR",
		},
		{
			name:       "standalone Technical Report line",
			filename:   "21905-h20.docx",
			body:       []bodyElement{coverPara("ZB", "Technical Report")},
			wantSpecID: "TR 21.905",
			wantType:   "TR",
		},
		{
			name:       "standalone Technical Specification line with punctuation",
			filename:   "23501-i30.docx",
			body:       []bodyElement{coverPara("ZB", "technical specification.")},
			wantSpecID: "TS 23.501",
			wantType:   "TS",
		},
		{
			name:       "standalone type line with punctuation and casing",
			filename:   "21905-h20.docx",
			body:       []bodyElement{coverPara("ZB", "technical report.")},
			wantSpecID: "TR 21.905",
			wantType:   "TR",
		},
		{
			name:     "TSG name does not decide the type",
			filename: "21905-h20.docx",
			body: []bodyElement{
				coverPara("ZT", "Technical Specification Group Services and System Aspects;"),
				coverPara("ZB", "Technical Report"),
			},
			wantSpecID: "TR 21.905",
			wantType:   "TR",
		},
		{
			name:       "marker naming another spec is ignored",
			filename:   "23501-i30.docx",
			body:       []bodyElement{coverPara("Normal", "based on 3GPP TR 99.999")},
			wantSpecID: "TS 23.501",
			wantType:   "",
		},
		{
			name:       "type from document properties",
			filename:   "21905-h20.docx",
			props:      coreProperties{Title: "3GPP TR 21.905"},
			wantSpecID: "TR 21.905",
			wantType:   "TR",
		},
		{
			name:       "no signal defaults to TS",
			filename:   "21905-h20.docx",
			wantSpecID: "TS 21.905",
			wantType:   "",
		},
		{
			name:       "type spelled in the filename stem",
			filename:   "TR 21.905.docx",
			wantSpecID: "TR 21.905",
			wantType:   "TR",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			meta := extractMetadata(tt.filename, tt.props, tt.body, map[string]string{})
			if meta.SpecID != tt.wantSpecID {
				t.Errorf("SpecID = %q, want %q", meta.SpecID, tt.wantSpecID)
			}
			if meta.DocType != tt.wantType {
				t.Errorf("DocType = %q, want %q", meta.DocType, tt.wantType)
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

// The release marker is not always parenthesised. Cutting the title at a
// literal "(Release" left the title empty for these subjects, so the spec fell
// back to whatever its first heading happened to be.
func TestExtractMetadata_SubjectWithUnparenthesisedRelease(t *testing.T) {
	for _, tt := range []struct {
		subject   string
		wantTitle string
	}{
		{"5G System architecture; Release 18", "5G System architecture"},
		{"5G System architecture, Release 18", "5G System architecture,"},
		{"5G System architecture (Release 18)", "5G System architecture"},
	} {
		meta := extractMetadata("23501-i30.docx", coreProperties{Subject: tt.subject}, nil, nil)
		if meta.Title != tt.wantTitle {
			t.Errorf("subject %q: Title = %q, want %q", tt.subject, meta.Title, tt.wantTitle)
		}
		if meta.Release != "18" {
			t.Errorf("subject %q: Release = %q, want 18", tt.subject, meta.Release)
		}
	}
}

// A subject that is nothing but the release marker carries no title, so the
// usual fallbacks still have to run.
func TestExtractMetadata_SubjectOnlyRelease(t *testing.T) {
	meta := extractMetadata("23501-i30.docx", coreProperties{Subject: "(Release 18)"}, nil, nil)
	if meta.Title != "TS 23.501" {
		t.Errorf("Title = %q, want fallback to spec ID", meta.Title)
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

func TestExtractMetadata_SeriesFromBase36VersionFilename(t *testing.T) {
	meta := extractMetadata("23222-ja0.docx", coreProperties{}, nil, nil)
	if meta.SpecID != "TS 23.222" {
		t.Fatalf("SpecID = %q, want %q", meta.SpecID, "TS 23.222")
	}
	if got := meta.Series(); got != "23" {
		t.Errorf("Series() = %q, want %q", got, "23")
	}
}
