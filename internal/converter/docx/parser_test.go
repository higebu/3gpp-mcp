package docx

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
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
	if metadata.Version != "18.2.0" {
		t.Errorf("Version = %q, want %q", metadata.Version, "18.2.0")
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
			if strings.Contains(c, "<table>") {
				hasTable = true
				break
			}
		}
		if !hasTable {
			t.Error("Section 3.2 should contain an HTML table")
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
			if strings.Contains(c, "<table>") {
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
	if metadata.Version != "19.0.0" {
		t.Errorf("Version = %q, want %q", metadata.Version, "19.0.0")
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

	// 22.839 is a study, so its cover page names it a Technical Report.
	if metadata.SpecID != "TR 22.839" {
		t.Errorf("SpecID = %q, want %q", metadata.SpecID, "TR 22.839")
	}
	if metadata.Version != "18.1.0" {
		t.Errorf("Version = %q, want %q", metadata.Version, "18.1.0")
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

// TestSectionToMarkdown_NoRealNumber covers GitHub issue #42: when a section
// has no real clause number (Number == Title, e.g. a TS 38.331 IE heading
// marked with a bare dash), the rendered heading must show the title once,
// not "Title Title".
func TestSectionToMarkdown_NoRealNumber(t *testing.T) {
	section := &Section{
		Number:  "MRB-Identity",
		Title:   "MRB-Identity",
		Level:   3,
		Content: []string{"IE definition body."},
	}

	md := SectionToMarkdown(section)
	if !strings.HasPrefix(md, "### MRB-Identity\n") {
		t.Errorf("expected heading to show the title once, got %q", md[:min(len(md), 40)])
	}
	if strings.Count(md, "MRB-Identity") != 1 {
		t.Errorf("expected title to appear exactly once, got:\n%s", md)
	}
}

func TestSectionToMarkdown_ConsecutiveTables(t *testing.T) {
	section := &Section{
		Number: "7.2.1",
		Title:  "Create Session Request",
		Level:  3,
		Content: []string{
			"<table><tbody><tr><td><p>A</p></td><td><p>B</p></td></tr></tbody></table>",
			"<table><tbody><tr><td><p>C</p></td><td><p>D</p></td></tr></tbody></table>",
		},
	}
	md := SectionToMarkdown(section)
	// Two HTML tables must be separated by a blank line so goldmark treats
	// them as separate raw HTML blocks.
	if !strings.Contains(md, "</table>\n\n<table>") {
		t.Errorf("Consecutive tables not properly separated:\n%s", md)
	}
}

func TestTableToHTML(t *testing.T) {
	tableXML := `<tbl>
		<tr><tc><p><r><t>Header1</t></r></p></tc><tc><p><r><t>Header2</t></r></p></tc></tr>
		<tr><tc><p><r><t>Cell1</t></r></p></tc><tc><p><r><t>Cell2</t></r></p></tc></tr>
	</tbl>`

	info := extractTable([]byte(tableXML))
	html := tableToHTML(info, imageContext{})
	for _, want := range []string{
		"<table>",
		"<tr><td><p>Header1</p></td><td><p>Header2</p></td></tr>",
		"<tr><td><p>Cell1</p></td><td><p>Cell2</p></td></tr>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("Expected output to contain %q, got:\n%s", want, html)
		}
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
	sections := parseSections(elements, styleMap, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	sec := sections[0]
	var found bool
	for _, c := range sec.Content {
		if strings.HasPrefix(c, "```\n") && strings.HasSuffix(c, "\n```") &&
			strings.Contains(c, "openapi: 3.0.0") &&
			strings.Contains(c, "info:") &&
			strings.Contains(c, "  version: \"1.0.0\"") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected consecutive code paragraphs to be wrapped in a single fenced code block; got content:\n%v", sec.Content)
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
	sections := parseSections(elements, styleMap, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if len(sections[0].Content) == 0 {
		t.Fatal("expected trailing code block to be flushed")
	}
	last := sections[0].Content[len(sections[0].Content)-1]
	if !strings.HasPrefix(last, "```\n") || !strings.Contains(last, "foo: bar") {
		t.Errorf("expected trailing fenced code block, got %q", last)
	}
}

// TestParseSections_LetteredSectionNumbers guards against a regression where a
// letter suffix anywhere but immediately after the first digit group (e.g.
// "5.4A.2" or "6.1.3.4a") made sectionNumberRE fail to match, causing the
// heading fallback to set both Number and Title to the full raw heading text
// and duplicate it in the rendered output.
func TestParseSections_LetteredSectionNumbers(t *testing.T) {
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "4.2.1a\tSome title",
			Runs: []runInfo{{Text: "4.2.1a\tSome title"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "5.4A.2 Channel raster for CA",
			Runs: []runInfo{{Text: "5.4A.2 Channel raster for CA"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "6.1.3.4a\tAbsolute Timing Advance Command MAC CE",
			Runs: []runInfo{{Text: "6.1.3.4a\tAbsolute Timing Advance Command MAC CE"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "Annex A: Sub clauses",
			Runs: []runInfo{{Text: "Annex A: Sub clauses"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading2", Text: "A.1a\tAnnex sub",
			Runs: []runInfo{{Text: "A.1a\tAnnex sub"}},
		}},
	}
	styleMap := map[string]string{"Heading1": "Heading 1", "Heading2": "Heading 2"}
	sections := parseSections(elements, styleMap, nil, nil, nil)

	want := map[string]string{
		"4.2.1a":   "Some title",
		"5.4A.2":   "Channel raster for CA",
		"6.1.3.4a": "Absolute Timing Advance Command MAC CE",
		"A.1a":     "Annex sub",
	}
	sectionMap := make(map[string]*Section)
	for _, s := range sections {
		sectionMap[s.Number] = s
	}
	for number, title := range want {
		s, ok := sectionMap[number]
		if !ok {
			t.Errorf("missing section %q", number)
			continue
		}
		if s.Title != title {
			t.Errorf("section %q title = %q, want %q", number, s.Title, title)
		}
		if s.Number == s.Title {
			t.Errorf("section %q: Number and Title are identical, heading was not split correctly", number)
		}
	}

	if s, ok := sectionMap["A.1a"]; ok && s.Level != 2 {
		t.Errorf("section A.1a level = %d, want 2", s.Level)
	}
}

// TestParseSections_UnnumberedDashHeadings guards against a regression
// (GitHub issue #42) where specs like TS 38.331 mark IE/message definitions
// with a bare dash instead of a decimal clause number (e.g. "-\tMRB-Identity").
// Neither sectionNumberRE nor annexRE matches a heading starting with a dash,
// so it used to fall into the final fallback that sets both Number and Title
// to the full raw heading text (including the dash and tab), duplicating the
// title in every rendered output. Number == Title is the intentional result
// here (there's no real section number); renderers must special-case that
// rather than show the same text twice.
func TestParseSections_UnnumberedDashHeadings(t *testing.T) {
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "6.3\tMessage and information element definitions",
			Runs: []runInfo{{Text: "6.3\tMessage and information element definitions"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading2", Text: "-\tMRB-Identity",
			Runs: []runInfo{{Text: "-\tMRB-Identity"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading2", Text: "–\tMeasSequence",
			Runs: []runInfo{{Text: "–\tMeasSequence"}},
		}},
	}
	styleMap := map[string]string{"Heading1": "Heading 1", "Heading2": "Heading 2"}
	sections := parseSections(elements, styleMap, nil, nil, nil)

	want := []string{"MRB-Identity", "MeasSequence"}
	sectionMap := make(map[string]*Section)
	for _, s := range sections {
		sectionMap[s.Title] = s
	}
	for _, title := range want {
		s, ok := sectionMap[title]
		if !ok {
			t.Errorf("missing section %q", title)
			continue
		}
		if s.Title != title {
			t.Errorf("section title = %q, want %q", s.Title, title)
		}
		if s.Number != s.Title {
			t.Errorf("section %q: Number = %q, want equal to Title (no real section number)", title, s.Number)
		}
		if strings.HasPrefix(s.Title, "-") || strings.HasPrefix(s.Title, "–") || strings.Contains(s.Title, "\t") {
			t.Errorf("section %q: title still contains the leading dash/tab marker", s.Title)
		}
	}
}

// TestParseSections_SectionNumberFormats pins down which heading shapes
// sectionNumberRE splits into a number and a title, and which ones keep the
// raw-text fallback. Every "numbered" case is a heading that exists in a
// published spec; the ones that used to fall through to the fallback stored
// the whole heading line (tab included) as the section number, so get_section
// could not find them by their real clause number.
func TestParseSections_SectionNumberFormats(t *testing.T) {
	tests := []struct {
		name    string
		heading string
		number  string
		title   string
	}{
		// Existing numbering must keep parsing exactly as before.
		{"plain", "5.3.2\tTitle", "5.3.2", "Title"},
		{"annex", "A.1.2\tTitle", "A.1.2", "Title"},
		{"zero suffix", "6.1.0\tGeneral", "6.1.0", "General"},
		{"top level", "4\tScope", "4", "Scope"},
		{"top level suffix", "6A\tGeneral", "6A", "General"},
		{"space separator", "5.4A.2 Channel raster for CA", "5.4A.2", "Channel raster for CA"},
		{"single letter suffix", "4.2.1a\tSome title", "4.2.1a", "Some title"},

		// Multi-character letter suffixes: 3GPP extends the suffix of the
		// preceding clause every time it inserts a new one.
		{"two letters lower", "10.5.2.21aa\tMultiRate configuration", "10.5.2.21aa", "MultiRate configuration"},
		{"mixed case suffix", "7.2.160aA\tQuota-Indicator AVP", "7.2.160aA", "Quota-Indicator AVP"},
		{"three letter suffix", "7.2.111AaD\tMonitoring-Event-Report-Data AVP", "7.2.111AaD", "Monitoring-Event-Report-Data AVP"},
		{"suffix mid number", "5.10AA.1\tDefinition and applicability", "5.10AA.1", "Definition and applicability"},

		// Trailing period after the number.
		{"trailing period", "4.5.\tThe Visitor Location Register (VLR)", "4.5", "The Visitor Location Register (VLR)"},
		{"trailing period annex", "A.1.\tGeneral", "A.1", "General"},

		// Multi-letter annex prefixes (TS 23.228 runs past Annex Z).
		{"annex AA", "AA.1\tGeneral", "AA.1", "General"},
		{"annex AA deep", "AA.2.1.2.3\tNhss_ImsUECM_Registration service operation", "AA.2.1.2.3", "Nhss_ImsUECM_Registration service operation"},

		// Letter/digit alternation inside a component (TS 24.483 management
		// object leaves).
		{"letter digit", "10.2.16F1\t/<x>/<x>/Common/OnetoOne", "10.2.16F1", "/<x>/<x>/Common/OnetoOne"},
		{"repeated alternation", "10.2.97B3B0\t/<x>/<x>/OnNetwork", "10.2.97B3B0", "/<x>/<x>/OnNetwork"},
		{"alternation then letter", "13.2.87A6A12A\tListOfLocationCriteria", "13.2.87A6A12A", "ListOfLocationCriteria"},

		// "_"-joined variant tokens (RF test specs).
		{"underscore digit", "10.1_1\tFDD MBMS performance", "10.1_1", "FDD MBMS performance"},
		{"underscore letter", "8.2.1.7_A.1\tTest purpose", "8.2.1.7_A.1", "Test purpose"},
		{"repeated underscore", "8.3.1.2.1_D_1.1\tInitial conditions", "8.3.1.2.1_D_1.1", "Initial conditions"},

		// Headings with no clause number keep the documented fallback of
		// reusing the heading text as the storage key.
		{"foreword", "Foreword", "Foreword", "Foreword"},
		{"ipr", "Intellectual Property Rights", "Intellectual Property Rights", "Intellectual Property Rights"},
		// Prose that starts like a number must not be split: a word after the
		// leading digits ("5GS", "3GPP") is not a clause suffix, and a
		// capitalised abbreviation before a dot is not an annex letter.
		{"digits then word", "5GS Bearer Contexts", "5GS Bearer Contexts", "5GS Bearer Contexts"},
		{"spec reference", "3GPP TS 23.501 overview", "3GPP TS 23.501 overview", "3GPP TS 23.501 overview"},
		{"abbreviation dot", "Fig.3 Overall architecture", "Fig.3 Overall architecture", "Fig.3 Overall architecture"},
		// A range is not a single clause number; splitting it would either
		// fabricate a clause or hide the rest of the range, so ranges stay on
		// the fallback (out of scope, see sectionNumberRE).
		{"range", "7.6.4.25-7.6.4.35\tVoid", "7.6.4.25-7.6.4.35\tVoid", "7.6.4.25-7.6.4.35\tVoid"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			elements := []bodyElement{
				{Tag: "p", Paragraph: paragraphInfo{
					StyleID: "Heading1", Text: tt.heading,
					Runs: []runInfo{{Text: tt.heading}},
				}},
			}
			styleMap := map[string]string{"Heading1": "Heading 1"}
			sections := parseSections(elements, styleMap, nil, nil, nil)
			if len(sections) != 1 {
				t.Fatalf("parsed %d sections, want 1", len(sections))
			}
			if sections[0].Number != tt.number {
				t.Errorf("Number = %q, want %q", sections[0].Number, tt.number)
			}
			if sections[0].Title != tt.title {
				t.Errorf("Title = %q, want %q", sections[0].Title, tt.title)
			}
		})
	}
}

// TestParseSections_MultiLetterAnnex covers annexes past Z: annexRE used to
// capture only the first letter, so every "Annex Ax" collapsed onto number "A"
// and overwrote the real Annex A on the (spec_id, version, number) key, while
// their subclauses ("AA.1") missed sectionNumberRE entirely and kept the raw
// heading text as their number.
func TestParseSections_MultiLetterAnnex(t *testing.T) {
	headings := []struct {
		style string
		text  string
	}{
		{"Heading1", "Annex A (informative): Change history"},
		{"Heading1", "Annex AA (informative): IMS SBA"},
		{"Heading2", "AA.1\tGeneral"},
		{"Heading3", "AA.1.1\tArchitectural Support"},
		{"Heading1", "Annex AB: Void"},
	}
	var elements []bodyElement
	for _, h := range headings {
		elements = append(elements, bodyElement{Tag: "p", Paragraph: paragraphInfo{
			StyleID: h.style, Text: h.text,
			Runs: []runInfo{{Text: h.text}},
		}})
	}
	styleMap := map[string]string{"Heading1": "Heading 1", "Heading2": "Heading 2", "Heading3": "Heading 3"}
	sections := parseSections(elements, styleMap, nil, nil, nil)

	sectionMap := make(map[string]*Section)
	for _, s := range sections {
		if _, dup := sectionMap[s.Number]; dup {
			t.Errorf("duplicate section number %q", s.Number)
		}
		sectionMap[s.Number] = s
	}

	want := []struct {
		number, title, parent string
		level                 int
	}{
		{"A", "Change history", "", 1},
		{"AA", "IMS SBA", "", 1},
		{"AA.1", "General", "AA", 2},
		{"AA.1.1", "Architectural Support", "AA.1", 3},
		{"AB", "Void", "", 1},
	}
	for _, w := range want {
		s, ok := sectionMap[w.number]
		if !ok {
			t.Errorf("missing section %q", w.number)
			continue
		}
		if s.Title != w.title {
			t.Errorf("section %q title = %q, want %q", w.number, s.Title, w.title)
		}
		if s.Level != w.level {
			t.Errorf("section %q level = %d, want %d", w.number, s.Level, w.level)
		}
		if s.ParentNumber != w.parent {
			t.Errorf("section %q parent = %q, want %q", w.number, s.ParentNumber, w.parent)
		}
	}

	// Every parent reference must resolve to a section that exists.
	for _, s := range sections {
		if s.ParentNumber == "" {
			continue
		}
		if _, ok := sectionMap[s.ParentNumber]; !ok {
			t.Errorf("section %q references missing parent %q", s.Number, s.ParentNumber)
		}
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

// TestParseBody_TruncatedXML verifies that a decode error surfaces instead of
// silently returning the elements parsed so far: a half-parsed document must
// not be imported as a complete one.
func TestParseBody_TruncatedXML(t *testing.T) {
	full := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		`<w:p><w:r><w:t>hello</w:t></w:r></w:p>` +
		`<w:p><w:r><w:t>world</w:t></w:r></w:p>` +
		`</w:body></w:document>`

	if _, err := parseBody([]byte(full)); err != nil {
		t.Fatalf("well-formed body: %v", err)
	}

	truncated := full[:len(full)/2]
	if _, err := parseBody([]byte(truncated)); err == nil {
		t.Error("expected an error for truncated document.xml")
	}

	malformed := `<w:document><w:body><w:p></w:tbl></w:body></w:document>`
	if _, err := parseBody([]byte(malformed)); err == nil {
		t.Error("expected an error for mismatched tags")
	}
}

func TestASN1MarkerRegex(t *testing.T) {
	tests := []struct {
		text  string
		start bool
		stop  bool
	}{
		{"-- ASN1START", true, false},
		{"--ASN1START", true, false},
		{"-- /example/ ASN1START", true, false},
		{"-- /bad example/ ASN1STOP", false, true},
		{"\t-- ASN1START", true, false},
		{" -- ASN1START", true, false},
		{"-- ASN1STOP", false, true},
		{"-- ASN1START -- TAG-FOO-START", true, false},
		// Prose that mentions the markers (TS 38.331 A.1) must not match.
		{"tags 'ASN1START' and 'ASN1STOP'", false, false},
		{"see ASN1START above", false, false},
		{"-- ASN1STARTED", false, false},
		{"plain text", false, false},
	}
	for _, tt := range tests {
		info := paragraphInfo{Text: tt.text, Runs: []runInfo{{Text: tt.text}}}
		if got := matchASN1Marker(asn1StartRE, info); got != tt.start {
			t.Errorf("start match for %q = %v, want %v", tt.text, got, tt.start)
		}
		if got := matchASN1Marker(asn1StopRE, info); got != tt.stop {
			t.Errorf("stop match for %q = %v, want %v", tt.text, got, tt.stop)
		}
	}
}

func TestParseSections_ASN1MarkerBlock(t *testing.T) {
	para := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: text, Runs: []runInfo{{Text: text}},
		}}
	}
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "6.2.2\tMessage definitions",
			Runs: []runInfo{{Text: "6.2.2\tMessage definitions"}},
		}},
		para("Intro prose."),
		para("-- ASN1START"),
		// Non-code-styled lines with leading tabs must be captured verbatim.
		para("RRCSetup-IEs ::= SEQUENCE {"),
		{Tag: "p", Paragraph: paragraphInfo{}}, // blank paragraph → blank line
		para("\tradioBearerConfig\tRadioBearerConfig,"),
		para("}"),
		para("-- ASN1STOP"),
		para("Trailing prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	want := "```asn1\n" +
		"-- ASN1START\n" +
		"RRCSetup-IEs ::= SEQUENCE {\n" +
		"\n" +
		"\tradioBearerConfig\tRadioBearerConfig,\n" +
		"}\n" +
		"-- ASN1STOP\n" +
		"```"
	var found bool
	for _, c := range content {
		if c == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected verbatim asn1 fence %q; got content:\n%v", want, content)
	}
	if content[len(content)-1] != "Trailing prose." {
		t.Errorf("expected trailing prose after the fence, got %q", content[len(content)-1])
	}
}

func TestParseSections_ASN1MultipleBlocksAcrossHeadings(t *testing.T) {
	heading := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading2", Text: text, Runs: []runInfo{{Text: text}},
		}}
	}
	para := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: text, Runs: []runInfo{{Text: text}},
		}}
	}
	elements := []bodyElement{
		heading("-\tRRCSetup"),
		para("-- ASN1START"),
		para("RRCSetup ::= SEQUENCE {}"),
		para("-- ASN1STOP"),
		heading("-\tRRCReject"),
		para("-- ASN1START"),
		para("RRCReject ::= SEQUENCE {}"),
		para("-- ASN1STOP"),
	}
	sections := parseSections(elements, map[string]string{"Heading2": "Heading 2"}, nil, nil, nil)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	for i, wantBody := range []string{"RRCSetup ::= SEQUENCE {}", "RRCReject ::= SEQUENCE {}"} {
		if len(sections[i].Content) != 1 {
			t.Fatalf("section %d: expected exactly 1 content entry, got %v", i, sections[i].Content)
		}
		c := sections[i].Content[0]
		if !strings.HasPrefix(c, "```asn1\n") || !strings.Contains(c, wantBody) {
			t.Errorf("section %d: expected asn1 fence containing %q, got %q", i, wantBody, c)
		}
	}
}

func TestParseSections_ASN1UnterminatedFlushedByHeading(t *testing.T) {
	para := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: text, Runs: []runInfo{{Text: text}},
		}}
	}
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "1\tFirst",
			Runs: []runInfo{{Text: "1\tFirst"}},
		}},
		para("-- ASN1START"),
		para("Unterminated ::= SEQUENCE {"),
		// Missing -- ASN1STOP: the next heading must flush the partial block.
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "2\tSecond",
			Runs: []runInfo{{Text: "2\tSecond"}},
		}},
		para("Second section prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if len(sections[0].Content) != 1 || !strings.HasPrefix(sections[0].Content[0], "```asn1\n") {
		t.Errorf("expected partial asn1 fence in first section, got %v", sections[0].Content)
	}
	if len(sections[1].Content) != 1 || sections[1].Content[0] != "Second section prose." {
		t.Errorf("expected plain prose in second section, got %v", sections[1].Content)
	}
}

func TestParseSections_ASN1UnterminatedFlushedByTable(t *testing.T) {
	para := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: text, Runs: []runInfo{{Text: text}},
		}}
	}
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "1\tFirst",
			Runs: []runInfo{{Text: "1\tFirst"}},
		}},
		para("-- ASN1START"),
		para("Unterminated ::= SEQUENCE {"),
		{Tag: "tbl", Table: tableInfo{Rows: []tableRow{{Cells: []tableCell{{Paras: []paragraphInfo{{Text: "cell", Runs: []runInfo{{Text: "cell"}}}}}}}}}},
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := strings.Join(sections[0].Content, "\n")
	if !strings.Contains(content, "```asn1\n") {
		t.Errorf("expected partial asn1 fence flushed before the table, got %v", sections[0].Content)
	}
	if !strings.Contains(content, "cell") {
		t.Errorf("expected table content preserved after the fence, got %v", sections[0].Content)
	}
	if strings.Index(content, "```asn1") > strings.Index(content, "cell") {
		t.Errorf("expected fence before table content, got %v", sections[0].Content)
	}
}

func TestDiameterDefRegex(t *testing.T) {
	tests := []struct {
		text  string
		start bool
		line  bool
	}{
		// Header spelling variants observed across Diameter specs.
		{"< Update-Location-Request> ::=\t< Diameter Header: 316, REQ, PXY, 16777251 >", true, true},
		{"Subscription-Data ::= <AVP header: 1400 10415>", true, true},
		{"MIP6-Agent-Info ::=< AVP Header: 486 >", true, true},
		{"EPS-User-State ::= <AVP header:1495 10415>", true, true},
		{" Supported-Services::= <AVP header: 3143 10415>", true, true},
		// AVP reference lines.
		{"< Session-Id >", false, true},
		{"{ Origin-Host }", false, true},
		{"[ DRMP ]", false, true},
		{"*[ Supported-Features ]", false, true},
		{"1*{ Proxy-Info }", false, true},
		{"0*2[ Subscription-Data ]", false, true},
		{"\t[ Destination-Host ]", false, true},
		// ASN.1 and prose must match neither.
		{"Foo ::= SEQUENCE {", false, false},
		{"where X ::= Y denotes a production", false, false},
		{"The AVP header is described in clause 4.", false, false},
		{"plain text", false, false},
		{"", false, false},
	}
	for _, tt := range tests {
		info := paragraphInfo{Text: tt.text, Runs: []runInfo{{Text: tt.text}}}
		if got := matchDiameterStart(info); got != tt.start {
			t.Errorf("start match for %q = %v, want %v", tt.text, got, tt.start)
		}
		if got := matchDiameterLine(info); got != tt.line {
			t.Errorf("line match for %q = %v, want %v", tt.text, got, tt.line)
		}
	}
}

func TestParseSections_DiameterCommandBlock(t *testing.T) {
	para := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: text, Runs: []runInfo{{Text: text}},
		}}
	}
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "6.1.1\tUser-Authorization-Request (UAR) Command",
			Runs: []runInfo{{Text: "6.1.1\tUser-Authorization-Request (UAR) Command"}},
		}},
		para("Message Format"),
		para("< User-Authorization-Request> ::=\t< Diameter Header: 300, REQ, PXY, 16777216 >"),
		para("< Session-Id >"),
		{Tag: "p", Paragraph: paragraphInfo{}}, // blank paragraph → blank line
		para("{ Origin-Host }"),
		// Bold runs (TS 29.229) must be captured without markdown markers.
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "*[ Supported-Features ]",
			Runs: []runInfo{{Text: "*[ Supported-Features ]", Bold: true}},
		}},
		para("*[ AVP ]"),
		para("Trailing prose."),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	want := "```diameter\n" +
		"< User-Authorization-Request> ::=\t< Diameter Header: 300, REQ, PXY, 16777216 >\n" +
		"< Session-Id >\n" +
		"\n" +
		"{ Origin-Host }\n" +
		"*[ Supported-Features ]\n" +
		"*[ AVP ]\n" +
		"```"
	var found bool
	for _, c := range content {
		if c == want {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected verbatim diameter fence %q; got content:\n%v", want, content)
	}
	if content[len(content)-1] != "Trailing prose." {
		t.Errorf("expected trailing prose after the fence, got %q", content[len(content)-1])
	}
	joined := strings.Join(content, "\n")
	if strings.Contains(joined, "**") {
		t.Errorf("bold markers leaked into output:\n%v", content)
	}
}

func TestParseSections_DiameterGroupedAVPVariants(t *testing.T) {
	para := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: text, Runs: []runInfo{{Text: text}},
		}}
	}
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "7.3.2\tGrouped AVPs",
			Runs: []runInfo{{Text: "7.3.2\tGrouped AVPs"}},
		}},
		para("Subscription-Data ::= <AVP header: 1400 10415>"),
		para("[ Subscriber-Status ]"),
		{Tag: "p", Paragraph: paragraphInfo{}},
		// Consecutive definitions merge into a single fence.
		para("MIP6-Agent-Info ::=< AVP Header: 486 >"),
		para("[ MIP-Home-Agent-Address ]"),
		para("*[ AVP ]"),
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if len(sections[0].Content) != 1 {
		t.Fatalf("expected exactly 1 content entry, got %v", sections[0].Content)
	}
	c := sections[0].Content[0]
	if !strings.HasPrefix(c, "```diameter\n") ||
		!strings.Contains(c, "Subscription-Data ::= <AVP header: 1400 10415>") ||
		!strings.Contains(c, "MIP6-Agent-Info ::=< AVP Header: 486 >") {
		t.Errorf("expected one merged diameter fence with both definitions, got %q", c)
	}
}

func TestParseSections_DiameterFlushedByHeadingAndTable(t *testing.T) {
	para := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			Text: text, Runs: []runInfo{{Text: text}},
		}}
	}
	heading := func(text string) bodyElement {
		return bodyElement{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: text, Runs: []runInfo{{Text: text}},
		}}
	}
	elements := []bodyElement{
		heading("1\tFirst"),
		para("ULR ::= < Diameter Header: 316, REQ, PXY >"),
		para("{ Origin-Host }"),
		heading("2\tSecond"),
		para("ULA ::= < Diameter Header: 316, PXY >"),
		para("{ Result-Code }"),
		{Tag: "tbl", Table: tableInfo{Rows: []tableRow{{Cells: []tableCell{{Paras: []paragraphInfo{{Text: "cell", Runs: []runInfo{{Text: "cell"}}}}}}}}}},
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(sections))
	}
	if len(sections[0].Content) != 1 || !strings.HasPrefix(sections[0].Content[0], "```diameter\n") {
		t.Errorf("expected diameter fence flushed by heading in first section, got %v", sections[0].Content)
	}
	second := strings.Join(sections[1].Content, "\n")
	if !strings.Contains(second, "```diameter\n") || !strings.Contains(second, "cell") {
		t.Errorf("expected diameter fence flushed by table plus table content, got %v", sections[1].Content)
	}
	if strings.Index(second, "```diameter") > strings.Index(second, "cell") {
		t.Errorf("expected fence before table content, got %v", sections[1].Content)
	}
}

func TestParseSections_DiameterFalsePositiveProse(t *testing.T) {
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "1\tFirst",
			Runs: []runInfo{{Text: "1\tFirst"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "where X ::= Y denotes a production",
			Runs: []runInfo{{Text: "where X ::= Y denotes a production"}},
		}},
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if len(sections[0].Content) != 1 || strings.Contains(sections[0].Content[0], "```") {
		t.Errorf("expected plain prose paragraph, got %v", sections[0].Content)
	}
}

func TestParseSections_DiameterAfterCodeStyledBlock(t *testing.T) {
	elements := []bodyElement{
		{Tag: "p", Paragraph: paragraphInfo{
			StyleID: "Heading1", Text: "1\tFirst",
			Runs: []runInfo{{Text: "1\tFirst"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "yaml: sample", Runs: []runInfo{{Text: "yaml: sample"}}, IsCode: true,
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "CCR ::= < Diameter Header: 272, REQ, PXY >",
			Runs: []runInfo{{Text: "CCR ::= < Diameter Header: 272, REQ, PXY >"}},
		}},
		{Tag: "p", Paragraph: paragraphInfo{
			Text: "{ Origin-Host }", Runs: []runInfo{{Text: "{ Origin-Host }"}},
		}},
	}
	sections := parseSections(elements, map[string]string{"Heading1": "Heading 1"}, nil, nil, nil)
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	content := sections[0].Content
	if len(content) != 2 {
		t.Fatalf("expected a plain fence then a diameter fence, got %v", content)
	}
	if !strings.HasPrefix(content[0], "```\n") || !strings.Contains(content[0], "yaml: sample") {
		t.Errorf("expected plain code fence first, got %q", content[0])
	}
	if !strings.HasPrefix(content[1], "```diameter\n") || !strings.Contains(content[1], "{ Origin-Host }") {
		t.Errorf("expected diameter fence second, got %q", content[1])
	}
}

func TestImagePlaceholder_UnifiedNotation(t *testing.T) {
	relMap := map[string]string{"rId1": "media/image1.emf"}
	images := map[string]*EmbeddedImage{
		"media/image1.emf": {Name: "image1.emf", MIMEType: "image/x-emf", LLMReadable: false},
	}
	tests := []struct {
		name string
		ref  imageRef
		want string
	}{
		{
			name: "non-readable with dimensions",
			ref:  imageRef{RID: "rId1", WidthPx: 612, HeightPx: 208},
			want: "![Figure](image://image1.emf?w=612&h=208)",
		},
		{
			name: "non-readable without dimensions",
			ref:  imageRef{RID: "rId1"},
			want: "![Figure](image://image1.emf)",
		},
		{
			name: "alt text kept",
			ref:  imageRef{RID: "rId1", AltText: "Network Topology", WidthPx: 10, HeightPx: 20},
			want: "![Network Topology](image://image1.emf?w=10&h=20)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := imagePlaceholder(relMap, images, tt.ref); got != tt.want {
				t.Errorf("imagePlaceholder() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestReadAllLimited_Cap exercises the decompression cap: reads at the limit
// succeed, reads over it fail, and the PCZ gzip path reports the failure.
// maxEntrySize is shrunk so the test does not allocate a gibibyte.
func TestReadAllLimited_Cap(t *testing.T) {
	old := maxEntrySize
	maxEntrySize = 8
	t.Cleanup(func() { maxEntrySize = old })

	data, err := readAllLimited(strings.NewReader("12345678"), "test entry")
	if err != nil || string(data) != "12345678" {
		t.Fatalf("at-limit read = %q, %v; want full data and nil error", data, err)
	}

	if _, err := readAllLimited(strings.NewReader("123456789"), "test entry"); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("over-limit read error = %v; want an 'exceeds' error", err)
	}

	// Through the PCZ path: a payload that decompresses past the cap fails.
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(make([]byte, 100)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := decompressPCZ(buf.Bytes()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("decompressPCZ over-limit error = %v; want an 'exceeds' error", err)
	}
}
