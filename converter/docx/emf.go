package docx

import (
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// toPNGName replaces a filename's extension with .png.
func toPNGName(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name)) + ".png"
}

// batchItem holds per-image state for batch conversion.
type batchItem struct {
	key           string
	original      *EmbeddedImage
	convertedName string // filename written to tmpdir (may differ from original for PCZ)
	pngData       []byte
	err           error
}

// ConvertImages converts all non-LLM-readable images (EMF/WMF) to PNG using
// LibreOffice headless mode. All images are converted in a single LibreOffice
// invocation for efficiency. Images are modified in-place. Returns the number
// of successfully converted images.
func ConvertImages(ctx context.Context, images map[string]*EmbeddedImage) int {
	var items []*batchItem
	for key, img := range images {
		if img.LLMReadable {
			continue
		}
		items = append(items, &batchItem{key: key, original: img})
	}
	if len(items) == 0 {
		return 0
	}

	if err := batchConvertToPNG(ctx, items); err != nil {
		log.Printf("  batch image conversion failed: %v", err)
		return 0
	}

	converted := 0
	for _, item := range items {
		if item.err != nil {
			log.Printf("  image conversion failed for %s: %v", item.original.Name, item.err)
			continue
		}
		images[item.key] = &EmbeddedImage{
			Name:        toPNGName(item.original.Name),
			MIMEType:    "image/png",
			Data:        item.pngData,
			LLMReadable: true,
		}
		converted++
	}
	return converted
}

// ConvertResultImages converts non-LLM-readable images in a ParseResult to PNG
// and updates the image list in-place. Returns the number of converted images.
func ConvertResultImages(ctx context.Context, result *ParseResult) int {
	if len(result.Images) == 0 {
		return 0
	}
	imageMap := make(map[string]*EmbeddedImage, len(result.Images))
	for _, img := range result.Images {
		imageMap[img.Name] = img
	}
	n := ConvertImages(ctx, imageMap)
	if n > 0 {
		result.Images = result.Images[:0]
		for _, img := range imageMap {
			result.Images = append(result.Images, img)
		}
	}
	return n
}

// placeholderRE matches non-LLM-readable image placeholders in section content,
// with optional dimension suffix like ", 576x432".
var placeholderRE = regexp.MustCompile(`\[Figure:\s*(.+?)\s*\(([^,]+),\s*use get_image to retrieve(?:,\s*(\d+)x(\d+))?\)\]`)

// UpdateImagePlaceholders replaces non-LLM-readable image placeholders in
// section content with LLM-readable image links after image conversion.
func UpdateImagePlaceholders(result *ParseResult) {
	converted := make(map[string]string) // base name (without ext) → new PNG name
	for _, img := range result.Images {
		if !img.LLMReadable {
			continue
		}
		base := strings.TrimSuffix(img.Name, filepath.Ext(img.Name))
		converted[base] = img.Name
	}
	if len(converted) == 0 {
		return
	}

	for _, section := range result.Sections {
		for i, content := range section.Content {
			section.Content[i] = placeholderRE.ReplaceAllStringFunc(content, func(match string) string {
				sub := placeholderRE.FindStringSubmatch(match)
				if len(sub) < 3 {
					return match
				}
				alt, filename := sub[1], sub[2]
				base := strings.TrimSuffix(filename, filepath.Ext(filename))
				newName, ok := converted[base]
				if !ok {
					return match
				}
				if alt == filename {
					alt = "Figure"
				}
				dimSuffix := ""
				if len(sub) >= 5 && sub[3] != "" && sub[4] != "" {
					dimSuffix = fmt.Sprintf("?w=%s&h=%s", sub[3], sub[4])
				}
				return fmt.Sprintf("![%s](image://%s%s)", alt, newName, dimSuffix)
			})
		}
	}
}

const (
	emrComment       = 0x46       // EMR_COMMENT record type
	emfPlusSignature = 0x2B464D45 // "EMF+" in little-endian
	emrMinSize       = 8          // minimum EMR record size (Type + Size)
	emfHeaderMinSize = 56         // minimum EMF header size to patch fileSize and nRecords
)

// stripEMFPlus removes EMF+ data embedded in EMR_COMMENT records from EMF
// binary data. This forces LibreOffice to use the legacy EMR rendering path,
// avoiding crashes on corrupted EMF+ records. The returned data contains only
// legacy EMR records. If the input is not a valid EMF or contains no EMF+
// data, it is returned unchanged.
//
// Background: Some 3GPP spec documents contain EMF images with malformed EMF+
// records. For example, TS 33.501 v19 (33501-j60.docx) Figure 16.4-1
// (image57.emf) has DrawClosedCurve records with size=16 (header only) but
// count=33–40, causing LibreOffice to read out-of-bounds memory and crash
// with SIGABRT in EMFPPlusDrawPolygon → B2DPolygon::count(). Since EMF files
// are dual-format, the legacy EMR records render identically for these
// diagrams. Microsoft Word handles this gracefully by falling back to EMR.
func stripEMFPlus(data []byte) []byte {
	if len(data) < emfHeaderMinSize {
		return data
	}
	// EMF header: first record type must be 0x01 (EMR_HEADER)
	if binary.LittleEndian.Uint32(data[0:4]) != 0x01 {
		return data
	}

	out := make([]byte, 0, len(data))
	offset := 0
	nRecords := 0
	hasEMFPlus := false

	for offset+emrMinSize <= len(data) {
		recType := binary.LittleEndian.Uint32(data[offset : offset+4])
		recSize := binary.LittleEndian.Uint32(data[offset+4 : offset+8])
		if recSize < emrMinSize || offset+int(recSize) > len(data) {
			break
		}

		isEMFPlus := false
		// EMR_COMMENT with at least 4 bytes of comment data after DataSize field
		if recType == emrComment && recSize >= 16 {
			commentIdent := binary.LittleEndian.Uint32(data[offset+12 : offset+16])
			if commentIdent == emfPlusSignature {
				isEMFPlus = true
				hasEMFPlus = true
			}
		}

		if !isEMFPlus {
			out = append(out, data[offset:offset+int(recSize)]...)
			nRecords++
		}
		offset += int(recSize)
	}

	if !hasEMFPlus {
		return data
	}

	// Patch EMF header: file size (offset 48) and record count (offset 52)
	binary.LittleEndian.PutUint32(out[48:52], uint32(len(out)))
	binary.LittleEndian.PutUint32(out[52:56], uint32(nRecords))
	return out
}

// sofficeBatchLimit caps the number of files passed to a single
// `soffice --convert-to png` invocation. LibreOffice silently drops files
// past ~247 arguments in one run, so we keep below that threshold.
const sofficeBatchLimit = 200

// batchConvertToPNG converts multiple images to PNG using LibreOffice.
// Inputs are written to a shared temp directory, then passed to soffice in
// chunks of sofficeBatchLimit to avoid LibreOffice silently dropping files
// in large invocations. Each item's pngData and err fields are populated
// after conversion.
func batchConvertToPNG(ctx context.Context, items []*batchItem) error {
	tmpDir, err := os.MkdirTemp("", "3gpp-img-batch-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	var inputPaths []string
	for _, item := range items {
		name := item.original.Name
		data := item.original.Data

		// Decompress PCZ files to EMF before conversion.
		if isPCZ(item.original.MIMEType) {
			raw, err := decompressPCZ(data)
			if err != nil {
				item.err = fmt.Errorf("decompress PCZ %s: %w", name, err)
				continue
			}
			data = raw
			name = strings.TrimSuffix(name, filepath.Ext(name)) + ".emf"
		}

		// Strip EMF+ data to avoid LibreOffice crashes on corrupted EMF+ records.
		if strings.HasSuffix(strings.ToLower(name), ".emf") {
			data = stripEMFPlus(data)
		}

		inputPath := filepath.Join(tmpDir, name)
		if err := os.WriteFile(inputPath, data, 0o600); err != nil {
			item.err = fmt.Errorf("write temp file: %w", err)
			continue
		}
		item.convertedName = name
		inputPaths = append(inputPaths, inputPath)
	}

	var lastErr error
	for start := 0; start < len(inputPaths); start += sofficeBatchLimit {
		end := start + sofficeBatchLimit
		if end > len(inputPaths) {
			end = len(inputPaths)
		}
		if err := runSofficeBatch(ctx, tmpDir, inputPaths[start:end]); err != nil {
			log.Printf("  soffice batch [%d:%d] warning: %v", start, end, err)
			lastErr = err
		}
	}

	anySuccess := false
	for _, item := range items {
		if item.err != nil {
			continue
		}
		pngPath := filepath.Join(tmpDir, toPNGName(item.convertedName))
		pngData, err := os.ReadFile(pngPath)
		if err != nil {
			item.err = fmt.Errorf("read converted PNG: %w", err)
			continue
		}
		item.pngData = pngData
		anySuccess = true
	}

	if !anySuccess && lastErr != nil {
		return fmt.Errorf("soffice batch conversion failed: %w", lastErr)
	}
	return nil
}

// runSofficeBatch invokes `soffice --convert-to png` with the given inputs,
// writing PNG outputs to outDir. Each invocation uses a fresh user profile
// so that repeated calls do not contend for LibreOffice's per-profile lock.
func runSofficeBatch(ctx context.Context, outDir string, inputs []string) error {
	if len(inputs) == 0 {
		return nil
	}
	profileDir, err := os.MkdirTemp("", "lo-profile-")
	if err != nil {
		return fmt.Errorf("create profile dir: %w", err)
	}
	defer os.RemoveAll(profileDir)

	args := []string{
		"--headless",
		"--norestore",
		"-env:UserInstallation=file://" + profileDir,
		"--convert-to", "png",
		"--outdir", outDir,
	}
	args = append(args, inputs...)

	cmd := exec.CommandContext(ctx, "soffice", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w (output: %s)", err, string(output))
	}
	return nil
}
