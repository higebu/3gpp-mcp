package docx

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestConvertResultImages_Real downloads TS 26.274 from the 3GPP archive and
// verifies that EMF/WMF images are converted to PNG by LibreOffice.
// Skipped in -short mode and when soffice is not installed.
func TestConvertResultImages_Real(t *testing.T) {
	if _, err := exec.LookPath("soffice"); err != nil {
		t.Skip("soffice not installed, skipping image conversion test")
	}

	// Verify soffice can actually convert images (requires libreoffice-draw).
	if !sofficeCanConvertImages(t) {
		t.Skip("soffice cannot convert images (libreoffice-draw may not be installed)")
	}

	zipData := downloadTestZip(t, "https://www.3gpp.org/ftp/Specs/archive/26_series/26.274/26274-j00.zip")
	path := extractDocxFromZip(t, zipData)

	result, err := ParseDocx(path)
	if err != nil {
		t.Fatalf("ParseDocx failed: %v", err)
	}

	// Count non-LLM-readable images (EMF/WMF) before conversion.
	var nonReadable int
	for _, img := range result.Images {
		if !img.LLMReadable {
			nonReadable++
		}
	}
	if nonReadable == 0 {
		t.Skip("no EMF/WMF images found in spec, nothing to test")
	}
	t.Logf("Found %d non-LLM-readable images (total %d)", nonReadable, len(result.Images))

	ctx := context.Background()
	n := ConvertResultImages(ctx, result)
	if n == 0 {
		t.Fatal("ConvertResultImages converted 0 images, expected at least 1")
	}
	t.Logf("Converted %d images to PNG", n)

	for _, img := range result.Images {
		if img.LLMReadable && img.MIMEType == "image/png" {
			if len(img.Data) == 0 {
				t.Errorf("image %s has no data after conversion", img.Name)
			}
		}
	}
}

func TestUpdateImagePlaceholders(t *testing.T) {
	result := &ParseResult{
		Sections: []*Section{
			{
				Number: "7.2.1",
				Title:  "Test",
				Level:  3,
				Content: []string{
					"Some text before",
					"[Figure: Network Topology (image1.emf, use get_image to retrieve)]",
					"[Figure: image2.wmf (image2.wmf, use get_image to retrieve)]",
					"[Figure: image3.emf (image3.emf, use get_image to retrieve)]",
				},
			},
		},
		Images: []*EmbeddedImage{
			{Name: "image1.png", MIMEType: "image/png", LLMReadable: true},
			{Name: "image2.png", MIMEType: "image/png", LLMReadable: true},
			// image3 not converted (still EMF)
			{Name: "image3.emf", MIMEType: "image/x-emf", LLMReadable: false},
		},
	}

	UpdateImagePlaceholders(result)

	content := result.Sections[0].Content
	if content[0] != "Some text before" {
		t.Errorf("plain text should be unchanged, got %q", content[0])
	}
	if content[1] != "![Network Topology](image://image1.png)" {
		t.Errorf("expected converted placeholder with alt text, got %q", content[1])
	}
	if content[2] != "![Figure](image://image2.png)" {
		t.Errorf("expected converted placeholder without alt text, got %q", content[2])
	}
	if !strings.Contains(content[3], "use get_image to retrieve") {
		t.Errorf("unconverted image should keep placeholder, got %q", content[3])
	}
}

func TestUpdateImagePlaceholders_WithDimensions(t *testing.T) {
	result := &ParseResult{
		Sections: []*Section{
			{
				Number: "7.2.1",
				Title:  "Test",
				Level:  3,
				Content: []string{
					"[Figure: Network (image1.emf, use get_image to retrieve, 600x400)]",
					"[Figure: image2.wmf (image2.wmf, use get_image to retrieve)]",
				},
			},
		},
		Images: []*EmbeddedImage{
			{Name: "image1.png", MIMEType: "image/png", LLMReadable: true},
			{Name: "image2.png", MIMEType: "image/png", LLMReadable: true},
		},
	}

	UpdateImagePlaceholders(result)

	content := result.Sections[0].Content
	if content[0] != "![Network](image://image1.png?w=600&h=400)" {
		t.Errorf("expected dimensions preserved, got %q", content[0])
	}
	if content[1] != "![Figure](image://image2.png)" {
		t.Errorf("expected no dimensions, got %q", content[1])
	}
}

// buildTestEMF constructs a minimal valid EMF binary with the given records.
// Each record is a (type, data) pair. An EMR_HEADER and EMR_EOF are added
// automatically.
func buildTestEMF(records []struct {
	typ  uint32
	data []byte
}) []byte {
	// Build body records first to know total size and count.
	var body []byte
	nRecords := 2 // header + EOF
	for _, r := range records {
		recSize := uint32(8 + len(r.data))
		// Pad to 4-byte boundary
		if recSize%4 != 0 {
			recSize += 4 - recSize%4
		}
		rec := make([]byte, recSize)
		binary.LittleEndian.PutUint32(rec[0:4], r.typ)
		binary.LittleEndian.PutUint32(rec[4:8], recSize)
		copy(rec[8:], r.data)
		body = append(body, rec...)
		nRecords++
	}

	// EMR_EOF: type=0x0E, size=20
	eof := make([]byte, 20)
	binary.LittleEndian.PutUint32(eof[0:4], 0x0E)
	binary.LittleEndian.PutUint32(eof[4:8], 20)

	// EMR_HEADER: type=0x01, size=108 (standard header)
	header := make([]byte, 108)
	binary.LittleEndian.PutUint32(header[0:4], 0x01)         // type
	binary.LittleEndian.PutUint32(header[4:8], 108)          // size
	binary.LittleEndian.PutUint32(header[40:44], 0x464D4520) // signature " EMF"
	binary.LittleEndian.PutUint32(header[44:48], 0x00010000) // version
	totalSize := uint32(108 + len(body) + 20)
	binary.LittleEndian.PutUint32(header[48:52], totalSize)
	binary.LittleEndian.PutUint32(header[52:56], uint32(nRecords))

	out := make([]byte, 0, totalSize)
	out = append(out, header...)
	out = append(out, body...)
	out = append(out, eof...)
	return out
}

func TestStripEMFPlus(t *testing.T) {
	// Build an EMF+ comment record: DataSize(4) + CommentIdentifier "EMF+"(4) + dummy payload
	emfPlusPayload := make([]byte, 12)
	binary.LittleEndian.PutUint32(emfPlusPayload[0:4], 8) // DataSize
	binary.LittleEndian.PutUint32(emfPlusPayload[4:8], emfPlusSignature)

	// Build a non-EMF+ comment record
	normalComment := make([]byte, 8)
	binary.LittleEndian.PutUint32(normalComment[0:4], 4) // DataSize
	binary.LittleEndian.PutUint32(normalComment[4:8], 0x12345678)

	// Build a regular EMR record (e.g., SETBKMODE = 0x12)
	regularData := make([]byte, 4)
	binary.LittleEndian.PutUint32(regularData[0:4], 1)

	emf := buildTestEMF([]struct {
		typ  uint32
		data []byte
	}{
		{emrComment, emfPlusPayload}, // EMF+ comment — should be stripped
		{0x12, regularData},          // SETBKMODE — should be kept
		{emrComment, normalComment},  // non-EMF+ comment — should be kept
		{emrComment, emfPlusPayload}, // another EMF+ comment — should be stripped
	})

	result := stripEMFPlus(emf)

	if len(result) >= len(emf) {
		t.Fatalf("expected stripped data to be smaller: got %d, original %d", len(result), len(emf))
	}

	// Verify header was patched
	fileSize := binary.LittleEndian.Uint32(result[48:52])
	if fileSize != uint32(len(result)) {
		t.Errorf("header fileSize = %d, want %d", fileSize, len(result))
	}

	nRecords := binary.LittleEndian.Uint32(result[52:56])
	// header + SETBKMODE + normal comment + EOF = 4
	if nRecords != 4 {
		t.Errorf("header nRecords = %d, want 4", nRecords)
	}

	// Verify no EMF+ signature remains
	for i := 0; i+4 <= len(result); i++ {
		if binary.LittleEndian.Uint32(result[i:i+4]) == emfPlusSignature {
			t.Errorf("EMF+ signature found at offset %d after stripping", i)
		}
	}
}

func TestStripEMFPlus_NoEMFPlus(t *testing.T) {
	// EMF with no EMF+ data should be returned unchanged.
	normalComment := make([]byte, 8)
	binary.LittleEndian.PutUint32(normalComment[0:4], 4)
	binary.LittleEndian.PutUint32(normalComment[4:8], 0xDEADBEEF)

	emf := buildTestEMF([]struct {
		typ  uint32
		data []byte
	}{
		{emrComment, normalComment},
	})

	result := stripEMFPlus(emf)

	if len(result) != len(emf) {
		t.Errorf("expected unchanged length: got %d, want %d", len(result), len(emf))
	}
}

func TestStripEMFPlus_NotEMF(t *testing.T) {
	// Non-EMF data (e.g., PNG) should be returned unchanged.
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	result := stripEMFPlus(png)
	if len(result) != len(png) {
		t.Errorf("expected unchanged length for non-EMF data")
	}
}

func TestStripEMFPlus_TooSmall(t *testing.T) {
	data := []byte{0x01, 0x02, 0x03}
	result := stripEMFPlus(data)
	if len(result) != len(data) {
		t.Errorf("expected unchanged length for too-small data")
	}
}

// sofficeCanConvertImages checks whether LibreOffice Draw is available by
// attempting a trivial SVG-to-PNG conversion.
func sofficeCanConvertImages(t *testing.T) bool {
	t.Helper()
	tmpDir := t.TempDir()
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"><rect width="1" height="1"/></svg>`
	svgPath := filepath.Join(tmpDir, "test.svg")
	if err := os.WriteFile(svgPath, []byte(svg), 0o644); err != nil {
		return false
	}
	cmd := exec.Command("soffice", "--headless", "--norestore", "--convert-to", "png", "--outdir", tmpDir, svgPath)
	if err := cmd.Run(); err != nil {
		return false
	}
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.HasSuffix(strings.ToLower(e.Name()), ".png") {
			return true
		}
	}
	return false
}
