package docx

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/higebu/3gpp-mcp/internal/testutil"
)

func testdataPath(name string) string {
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "testdata", name)
}

func downloadTestZip(t *testing.T, url string) []byte {
	t.Helper()
	return testutil.DownloadTestZip(t, url)
}

// extractDocxFromZip extracts the first .docx file from zip bytes into a temp file,
// preserving the original filename so that metadata extraction works correctly.
func extractDocxFromZip(t *testing.T, zipData []byte) string {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range r.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".docx") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open file in zip: %v", err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read from zip: %v", err)
		}
		return writeTempFile(t, filepath.Base(f.Name), data)
	}
	t.Fatal("no .docx file found in zip")
	return ""
}

// extractDocxBytesFromZip returns the raw bytes of the first .docx in a zip.
func extractDocxBytesFromZip(t *testing.T, zipData []byte) []byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	for _, f := range r.File {
		if !strings.HasSuffix(strings.ToLower(f.Name), ".docx") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open file in zip: %v", err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read from zip: %v", err)
		}
		return data
	}
	t.Fatal("no .docx file found in zip")
	return nil
}

// writeTempFile writes data to a named file in a temp directory.
func writeTempFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

// isStrictOOXML reports whether a .docx (zip) file uses Strict OOXML namespaces.
func isStrictOOXML(t *testing.T, docxData []byte) bool {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(docxData), int64(len(docxData)))
	if err != nil {
		t.Fatalf("open docx: %v", err)
	}
	for _, f := range r.File {
		if f.Name != "_rels/.rels" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open _rels/.rels: %v", err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read _rels/.rels: %v", err)
		}
		return strings.Contains(string(data), "purl.oclc.org/ooxml")
	}
	return false
}

func TestParseDocx(t *testing.T) {
	path := testdataPath("23274-i20.docx")
	result, err := ParseDocx(path)
	if err != nil {
		t.Fatalf("ParseDocx failed: %v", err)
	}
	metadata := result.Metadata
	sections := result.Sections

	// Verify metadata
	if metadata.SpecID != "TS 23.274" {
		t.Errorf("SpecID = %q, want %q", metadata.SpecID, "TS 23.274")
	}
	if metadata.Series() != "23" {
		t.Errorf("Series = %q, want %q", metadata.Series(), "23")
	}
	// Version comes from filename (i20), matching Python behavior
	if metadata.Version != "i20" {
		t.Errorf("Version = %q, want %q", metadata.Version, "i20")
	}
	if metadata.Release != "18" {
		t.Errorf("Release = %q, want %q", metadata.Release, "18")
	}
	// Note: release is extracted from body text "Release 18"
	if !strings.Contains(metadata.Title, "Multi Media Telephony") {
		t.Errorf("Title = %q, want to contain 'Multi Media Telephony'", metadata.Title)
	}

	// Verify sections
	if len(sections) == 0 {
		t.Fatal("No sections parsed")
	}

	// Check some expected sections
	sectionMap := make(map[string]*Section)
	for _, s := range sections {
		sectionMap[s.Number] = s
	}

	// Section 1: Scope
	if s, ok := sectionMap["1"]; !ok {
		t.Error("Missing section 1")
	} else {
		if s.Title != "Scope" {
			t.Errorf("Section 1 title = %q, want %q", s.Title, "Scope")
		}
		if s.Level != 1 {
			t.Errorf("Section 1 level = %d, want 1", s.Level)
		}
		if len(s.Content) == 0 {
			t.Error("Section 1 has no content")
		}
	}

	// Section 3.1: Definitions
	if s, ok := sectionMap["3.1"]; !ok {
		t.Error("Missing section 3.1")
	} else {
		if s.Title != "Definitions" {
			t.Errorf("Section 3.1 title = %q, want %q", s.Title, "Definitions")
		}
		if s.Level != 2 {
			t.Errorf("Section 3.1 level = %d, want 2", s.Level)
		}
		if s.ParentNumber != "3" {
			t.Errorf("Section 3.1 parent = %q, want %q", s.ParentNumber, "3")
		}
	}

	// Section 3.2: Abbreviations (should contain a table)
	if s, ok := sectionMap["3.2"]; !ok {
		t.Error("Missing section 3.2")
	} else {
		hasTable := false
		for _, c := range s.Content {
			if strings.Contains(c, "|") && strings.Contains(c, "---") {
				hasTable = true
				break
			}
		}
		if !hasTable {
			t.Error("Section 3.2 should contain a markdown table")
		}
	}

	// Section 4.2.1: Network elements (subsection)
	if s, ok := sectionMap["4.2.1"]; !ok {
		t.Error("Missing section 4.2.1")
	} else {
		if s.Level != 3 {
			t.Errorf("Section 4.2.1 level = %d, want 3", s.Level)
		}
		if s.ParentNumber != "4.2" {
			t.Errorf("Section 4.2.1 parent = %q, want %q", s.ParentNumber, "4.2")
		}
	}

	// Annex A
	if s, ok := sectionMap["A"]; !ok {
		t.Error("Missing Annex A")
	} else {
		if s.Level != 1 {
			t.Errorf("Annex A level = %d, want 1", s.Level)
		}
	}

	// Annex A.1 subsection
	if s, ok := sectionMap["A.1"]; !ok {
		t.Error("Missing section A.1")
	} else {
		if s.Level != 2 {
			t.Errorf("Section A.1 level = %d, want 2", s.Level)
		}
		if s.ParentNumber != "A" {
			t.Errorf("Section A.1 parent = %q, want %q", s.ParentNumber, "A")
		}
	}

	// Annex B
	if _, ok := sectionMap["B"]; !ok {
		t.Error("Missing Annex B")
	}

	// Annex C (change history with table)
	if s, ok := sectionMap["C"]; !ok {
		t.Error("Missing Annex C")
	} else {
		hasTable := false
		for _, c := range s.Content {
			if strings.Contains(c, "|") {
				hasTable = true
				break
			}
		}
		if !hasTable {
			t.Error("Annex C should contain a change history table")
		}
	}

	t.Logf("Parsed %d sections from %s", len(sections), metadata.SpecID)
}

// TestParseDocx_26274 exercises parsing of TS 26.274, a spec with a small
// section count extracted from a large ZIP (tests the size-limit code path).
// The ZIP is downloaded from the 3GPP archive at test time.
func TestParseDocx_26274(t *testing.T) {
	zipData := downloadTestZip(t, "https://www.3gpp.org/ftp/Specs/archive/26_series/26.274/26274-j00.zip")
	path := extractDocxFromZip(t, zipData)

	result, err := ParseDocx(path)
	if err != nil {
		t.Fatalf("ParseDocx failed: %v", err)
	}
	metadata := result.Metadata
	sections := result.Sections

	if metadata.SpecID != "TS 26.274" {
		t.Errorf("SpecID = %q, want %q", metadata.SpecID, "TS 26.274")
	}
	if metadata.Series() != "26" {
		t.Errorf("Series = %q, want %q", metadata.Series(), "26")
	}
	if metadata.Version != "j00" {
		t.Errorf("Version = %q, want %q", metadata.Version, "j00")
	}
	if metadata.Release != "19" {
		t.Errorf("Release = %q, want %q", metadata.Release, "19")
	}
	if len(sections) == 0 {
		t.Fatal("No sections parsed")
	}
	t.Logf("Parsed %d sections from %s", len(sections), metadata.SpecID)
}

// TestParseDocxFromBytes covers the byte-slice entrypoint alongside ParseDocx,
// exercising the in-memory code path without touching the filesystem loader.
func TestParseDocxFromBytes(t *testing.T) {
	data, err := os.ReadFile(testdataPath("23274-i20.docx"))
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}

	t.Run("valid docx", func(t *testing.T) {
		result, err := ParseDocxFromBytes(data, "23274-i20.docx")
		if err != nil {
			t.Fatalf("ParseDocxFromBytes: %v", err)
		}
		if result.Metadata.SpecID != "TS 23.274" {
			t.Errorf("SpecID = %q, want TS 23.274", result.Metadata.SpecID)
		}
		if len(result.Sections) == 0 {
			t.Error("expected at least one parsed section")
		}
	})

	t.Run("empty bytes", func(t *testing.T) {
		_, err := ParseDocxFromBytes(nil, "empty.docx")
		if err == nil {
			t.Fatal("expected error for empty bytes")
		}
	})

	t.Run("garbage bytes", func(t *testing.T) {
		_, err := ParseDocxFromBytes([]byte("not a zip"), "bogus.docx")
		if err == nil {
			t.Fatal("expected error for non-zip payload")
		}
	})
}

// TestParseDocx_22839 exercises parsing of TS 22.839, which uses Strict OOXML
// namespaces (purl.oclc.org/ooxml). Go's xml.Decoder only uses Name.Local and
// the parser reads ZIP entries by fixed paths, so Strict OOXML files are parsed
// correctly without any namespace patching.
// The ZIP is downloaded from the 3GPP archive at test time.
func TestParseDocx_22839(t *testing.T) {
	zipData := downloadTestZip(t, "https://www.3gpp.org/ftp/Specs/archive/22_series/22.839/22839-i10.zip")

	// Verify the downloaded file actually uses Strict OOXML (test precondition).
	docxData := extractDocxBytesFromZip(t, zipData)
	if !isStrictOOXML(t, docxData) {
		t.Fatal("precondition failed: 22839-i10.docx is not Strict OOXML")
	}

	path := writeTempFile(t, "22839-i10.docx", docxData)
	result, err := ParseDocx(path)
	if err != nil {
		t.Fatalf("ParseDocx failed on Strict OOXML file: %v", err)
	}
	metadata := result.Metadata
	sections := result.Sections

	if metadata.SpecID != "TS 22.839" {
		t.Errorf("SpecID = %q, want %q", metadata.SpecID, "TS 22.839")
	}
	if metadata.Version != "i10" {
		t.Errorf("Version = %q, want %q", metadata.Version, "i10")
	}
	if metadata.Release != "18" {
		t.Errorf("Release = %q, want %q", metadata.Release, "18")
	}
	if len(sections) < 100 {
		t.Errorf("expected at least 100 sections, got %d", len(sections))
	}
	t.Logf("Parsed %d sections from %s (Strict OOXML)", len(sections), metadata.SpecID)
}

func TestSectionToMarkdown(t *testing.T) {
	section := &Section{
		Number:  "1",
		Title:   "Scope",
		Level:   1,
		Content: []string{"This is the scope."},
	}

	md := SectionToMarkdown(section)
	if !strings.HasPrefix(md, "# 1 Scope") {
		t.Errorf("Markdown should start with '# 1 Scope', got %q", md[:min(len(md), 30)])
	}
	if !strings.Contains(md, "This is the scope.") {
		t.Error("Markdown should contain the content")
	}
}

func TestSectionToMarkdown_ConsecutiveTables(t *testing.T) {
	section := &Section{
		Number: "7.2.1",
		Title:  "Create Session Request",
		Level:  3,
		Content: []string{
			"| H1 | H2 |\n| --- | --- |\n| A | B |",
			"| H3 | H4 |\n| --- | --- |\n| C | D |",
		},
	}
	md := SectionToMarkdown(section)
	// Two tables must be separated by a blank line
	if !strings.Contains(md, "| A | B |\n\n| H3 | H4 |") {
		t.Errorf("Consecutive tables not properly separated:\n%s", md)
	}
}

func TestTableToMarkdown(t *testing.T) {
	// Simple table XML
	tableXML := `<tbl>
		<tr><tc><p><r><t>Header1</t></r></p></tc><tc><p><r><t>Header2</t></r></p></tc></tr>
		<tr><tc><p><r><t>Cell1</t></r></p></tc><tc><p><r><t>Cell2</t></r></p></tc></tr>
	</tbl>`

	md := tableToMarkdown([]byte(tableXML))
	if !strings.Contains(md, "| Header1 | Header2 |") {
		t.Errorf("Expected header row, got:\n%s", md)
	}
	if !strings.Contains(md, "| --- | --- |") {
		t.Errorf("Expected separator row, got:\n%s", md)
	}
	if !strings.Contains(md, "| Cell1 | Cell2 |") {
		t.Errorf("Expected data row, got:\n%s", md)
	}
}

func TestParseParagraph(t *testing.T) {
	// Bold text
	paraXML := `<p><pPr><pStyle val="Normal"/></pPr><r><rPr><b/></rPr><t>Bold text</t></r></p>`
	info := parseParagraph([]byte(paraXML))
	if info.StyleID != "Normal" {
		t.Errorf("StyleID = %q, want %q", info.StyleID, "Normal")
	}
	if len(info.Runs) != 1 || !info.Runs[0].Bold {
		t.Error("Expected bold run")
	}

	md := paragraphToMarkdown(info, "Normal")
	if md != "**Bold text**" {
		t.Errorf("Markdown = %q, want %q", md, "**Bold text**")
	}
}

func TestParseParagraph_CodeFont_Run(t *testing.T) {
	// Run-level <w:rFonts> with Courier New should mark the paragraph as code.
	paraXML := `<p><r><rPr><rFonts ascii="Courier New" hAnsi="Courier New"/></rPr><t>    post:</t></r></p>`
	info := parseParagraph([]byte(paraXML))
	if len(info.Runs) != 1 {
		t.Fatalf("expected 1 run, got %d", len(info.Runs))
	}
	if !info.Runs[0].IsCode {
		t.Error("expected run IsCode=true for Courier New font")
	}
	if !info.IsCode {
		t.Error("expected paragraph IsCode=true when all runs are code")
	}
}

func TestParseParagraph_CodeFont_Paragraph(t *testing.T) {
	// Paragraph-level <w:pPr><w:rPr><w:rFonts> should mark the paragraph as code
	// even when individual runs don't specify a font.
	paraXML := `<p><pPr><rPr><rFonts ascii="Courier New"/></rPr></pPr><r><t>openapi: 3.0.0</t></r></p>`
	info := parseParagraph([]byte(paraXML))
	if !info.IsCode {
		t.Error("expected paragraph IsCode=true for paragraph-level Courier New font")
	}
}

func TestParseParagraph_CodeFont_MixedFont(t *testing.T) {
	// A paragraph where only some runs use Courier New should not be treated as code.
	paraXML := `<p><r><rPr><rFonts ascii="Courier New"/></rPr><t>post</t></r><r><t> normal text</t></r></p>`
	info := parseParagraph([]byte(paraXML))
	if info.IsCode {
		t.Error("expected paragraph IsCode=false when only some runs are code")
	}
}

func TestParseSections_CodeBlockGrouping(t *testing.T) {
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "1 Test",
			Runs: []runInfo{{Text: "1"}, {Text: "Test"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "Intro text.",
			Runs: []runInfo{{Text: "Intro text."}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "openapi: 3.0.0", IsCode: true,
			Runs: []runInfo{{Text: "openapi: 3.0.0", IsCode: true}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "info:", IsCode: true,
			Runs: []runInfo{{Text: "info:", IsCode: true}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "  version: \"1.0.0\"", IsCode: true,
			Runs: []runInfo{{Text: "  version: \"1.0.0\"", IsCode: true}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "After code.",
			Runs: []runInfo{{Text: "After code."}},
		}},
	}
	styleMap := map[string]string{"Heading1": "Heading 1"}
	sections := parseSections(elements, styleMap, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	sec := sections[0]
	var found bool
	for _, c := range sec.Content {
		if strings.HasPrefix(c, "```yaml\n") && strings.HasSuffix(c, "\n```") &&
			strings.Contains(c, "openapi: 3.0.0") &&
			strings.Contains(c, "info:") &&
			strings.Contains(c, "  version: \"1.0.0\"") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected consecutive code paragraphs to be wrapped in a single fenced yaml block; got content:\n%v", sec.Content)
	}
	// Sanity: "After code." must appear as a regular paragraph after the code block.
	lastIdx := len(sec.Content) - 1
	if sec.Content[lastIdx] != "After code." {
		t.Errorf("expected last content entry to be 'After code.', got %q", sec.Content[lastIdx])
	}
}

func TestParseSections_CodeBlockAtEnd(t *testing.T) {
	// A code block that ends a section (no trailing non-code paragraph) must
	// still be flushed.
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "1 Test",
			Runs: []runInfo{{Text: "1 Test"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "foo: bar", IsCode: true,
			Runs: []runInfo{{Text: "foo: bar", IsCode: true}},
		}},
	}
	styleMap := map[string]string{"Heading1": "Heading 1"}
	sections := parseSections(elements, styleMap, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if len(sections[0].Content) == 0 {
		t.Fatal("expected trailing code block to be flushed")
	}
	last := sections[0].Content[len(sections[0].Content)-1]
	if !strings.HasPrefix(last, "```yaml\n") || !strings.Contains(last, "foo: bar") {
		t.Errorf("expected trailing fenced code block, got %q", last)
	}
}

func TestParseParagraphDrawingMLDimensions(t *testing.T) {
	// DrawingML image with extent dimensions
	paraXML := `<p><r><drawing><inline><extent cx="9525000" cy="4762500"/><graphic><graphicData><pic><blipFill><blip r:embed="rId5"/></blipFill></pic></graphicData></graphic></inline></drawing></r></p>`
	info := parseParagraph([]byte(paraXML))
	if len(info.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(info.Images))
	}
	img := info.Images[0]
	if img.RID != "rId5" {
		t.Errorf("RID = %q, want %q", img.RID, "rId5")
	}
	// 9525000 / 9525 = 1000px, 4762500 / 9525 = 500px
	if img.WidthPx != 1000 {
		t.Errorf("WidthPx = %d, want 1000", img.WidthPx)
	}
	if img.HeightPx != 500 {
		t.Errorf("HeightPx = %d, want 500", img.HeightPx)
	}
}

func TestParseParagraphVMLDimensions(t *testing.T) {
	// VML image with shape style dimensions
	paraXML := `<p><r><pict><shape style="width:453.6pt;height:340.2pt"><imagedata r:id="rId9" o:title="Network"/></shape></pict></r></p>`
	info := parseParagraph([]byte(paraXML))
	if len(info.Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(info.Images))
	}
	img := info.Images[0]
	if img.RID != "rId9" {
		t.Errorf("RID = %q, want %q", img.RID, "rId9")
	}
	if img.AltText != "Network" {
		t.Errorf("AltText = %q, want %q", img.AltText, "Network")
	}
	// 453.6pt * 96/72 = 604.8 → 605px
	if img.WidthPx != 605 {
		t.Errorf("WidthPx = %d, want 605", img.WidthPx)
	}
	// 340.2pt * 96/72 = 453.6 → 454px
	if img.HeightPx != 454 {
		t.Errorf("HeightPx = %d, want 454", img.HeightPx)
	}
}

func TestEmuToPx(t *testing.T) {
	tests := []struct {
		emu  string
		want int
	}{
		{"914400", 96}, // 1 inch
		{"9525", 1},    // 1 pixel
		{"0", 0},
		{"", 0},
		{"invalid", 0},
	}
	for _, tt := range tests {
		got := emuToPx(tt.emu)
		if got != tt.want {
			t.Errorf("emuToPx(%q) = %d, want %d", tt.emu, got, tt.want)
		}
	}
}

func TestParseCSSLength(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"453.6pt", 605},
		{"72pt", 96},
		{"1in", 96},
		{"2.54cm", 96},
		{"100px", 100},
		{"100", 100},
		{"0pt", 0},
		{"", 0},
	}
	for _, tt := range tests {
		got := parseCSSLength(tt.input)
		if got != tt.want {
			t.Errorf("parseCSSLength(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestHeadingStyles(t *testing.T) {
	for i := 1; i <= 9; i++ {
		name := "Heading " + string(rune('0'+i))
		level := getHeadingLevel(name)
		if level != i {
			t.Errorf("getHeadingLevel(%q) = %d, want %d", name, level, i)
		}
	}

	if level := getHeadingLevel("ANNEX heading"); level != 1 {
		t.Errorf("getHeadingLevel('ANNEX heading') = %d, want 1", level)
	}

	if level := getHeadingLevel("Normal"); level != 0 {
		t.Errorf("getHeadingLevel('Normal') = %d, want 0", level)
	}
}
