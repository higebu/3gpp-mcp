package docx

import (
	"archive/zip"
	"bytes"
	"testing"
)

// TestParseDocx_InlineImagesInterleavedWithText reproduces the issue #40
// scenario reported against TS 38.212: a paragraph whose text and inline
// images alternate in the document (e.g. "see sub-figure (a) [image] and
// sub-figure (b) [image] below") must render with each image placeholder
// next to the text describing it, not with every image bunched up after all
// of the paragraph's text.
func TestParseDocx_InlineImagesInterleavedWithText(t *testing.T) {
	const contentTypes = `<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="xml" ContentType="application/xml"/>
<Default Extension="png" ContentType="image/png"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`

	const rels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`

	const docRels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId5" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
<Relationship Id="rId6" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image2.png"/>
</Relationships>`

	img := func(rid string) string {
		return `<w:r><w:drawing><wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">` +
			`<a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData>` +
			`<pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:blipFill>` +
			`<a:blip r:embed="` + rid + `"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r>`
	}

	// One paragraph: text, image (a), text, image (b), text — the exact
	// interleaving pattern from a multi-part figure caption.
	para := `<w:p><w:r><w:t xml:space="preserve">See sub-figure (a) </w:t></w:r>` +
		img("rId5") +
		`<w:r><w:t xml:space="preserve"> and sub-figure (b) </w:t></w:r>` +
		img("rId6") +
		`<w:r><w:t xml:space="preserve"> below.</w:t></w:r></w:p>`

	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<w:body>
<w:p><w:pPr><w:pStyle w:val="Heading 1"/></w:pPr><w:r><w:t>5 Test</w:t></w:r></w:p>
` + para + `
</w:body>
</w:document>`

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string]string{
		"[Content_Types].xml":          contentTypes,
		"_rels/.rels":                  rels,
		"word/document.xml":            doc,
		"word/_rels/document.xml.rels": docRels,
		"word/media/image1.png":        "png-bytes-1",
		"word/media/image2.png":        "png-bytes-2",
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	result, err := ParseDocxFromBytes(buf.Bytes(), "38212-test.docx")
	if err != nil {
		t.Fatalf("ParseDocxFromBytes: %v", err)
	}
	if len(result.Sections) == 0 {
		t.Fatal("no sections parsed")
	}

	content := result.Sections[0].Content
	t.Logf("section content blocks: %#v", content)

	if len(content) != 5 {
		t.Fatalf("expected 5 content blocks (text, image, text, image, text), got %d: %#v", len(content), content)
	}

	wantContains := []string{"See sub-figure (a)", "image1.png", "sub-figure (b)", "image2.png", "below."}
	for i, want := range wantContains {
		if !bytes.Contains([]byte(content[i]), []byte(want)) {
			t.Errorf("block %d = %q, want it to contain %q", i, content[i], want)
		}
	}

	// The core regression check: image1 must appear before the "sub-figure
	// (b)" text, and image2 must appear after it — i.e. the images are
	// interleaved with their surrounding text, not both stacked at the end.
	imgAIdx, imgBIdx, subBIdx := -1, -1, -1
	for i, block := range content {
		switch {
		case bytes.Contains([]byte(block), []byte("image1.png")):
			imgAIdx = i
		case bytes.Contains([]byte(block), []byte("image2.png")):
			imgBIdx = i
		case bytes.Contains([]byte(block), []byte("sub-figure (b)")):
			subBIdx = i
		}
	}
	if imgAIdx == -1 || imgBIdx == -1 || subBIdx == -1 {
		t.Fatalf("expected to find both image placeholders and the sub-figure (b) text block: %#v", content)
	}
	if !(imgAIdx < subBIdx && subBIdx < imgBIdx) {
		t.Errorf("images stacked instead of interleaved: image1 at %d, sub-figure (b) text at %d, image2 at %d; want image1 < text < image2", imgAIdx, subBIdx, imgBIdx)
	}
}
