package docx

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
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
		// Update in place rather than rebuilding from the map, which would
		// randomize the order.
		for i, img := range result.Images {
			if updated, ok := imageMap[img.Name]; ok {
				result.Images[i] = updated
			}
		}
	}
	return n
}

// imageRefRE matches the filename in an image:// reference (a Markdown image
// link or an HTML <img src="image://image1.wmf?w=..&h=.."> tag emitted for
// table images), capturing only the filename portion up to any query string or
// delimiter.
var imageRefRE = regexp.MustCompile(`image://([^?"')\s]+)`)

// UpdateImagePlaceholders rewrites image:// references in section content to
// point at the converted PNG filenames after EMF/WMF conversion. Only the
// filename is replaced; alt text and any ?w=&h= suffix are kept.
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
			section.Content[i] = imageRefRE.ReplaceAllStringFunc(content, func(match string) string {
				sub := imageRefRE.FindStringSubmatch(match)
				if len(sub) < 2 {
					return match
				}
				filename := sub[1]
				base := strings.TrimSuffix(filename, filepath.Ext(filename))
				newName, ok := converted[base]
				if !ok || newName == filename {
					return match
				}
				return "image://" + newName
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
// emrRecordEnd returns the end offset of the EMR record that starts at offset
// and reports whether the record is well-formed and fits inside a buffer of n
// bytes. recSize comes straight out of the file, so the arithmetic runs in
// uint64: on a 32-bit build "offset + int(recSize)" wraps to a negative int
// for a large recSize, slips past a plain "> n" comparison and panics the
// slice expression that follows.
func emrRecordEnd(offset int, recSize uint32, n int) (int, bool) {
	if recSize < emrMinSize {
		return 0, false
	}
	end := uint64(offset) + uint64(recSize)
	if end > uint64(n) {
		return 0, false
	}
	return int(end), true
}

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
		end, ok := emrRecordEnd(offset, recSize, len(data))
		if !ok {
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
			out = append(out, data[offset:end]...)
			nRecords++
		}
		offset = end
	}

	if !hasEMFPlus {
		return data
	}
	if len(out) < emfHeaderMinSize {
		// The surviving records do not even cover the header, so the fields
		// at offsets 48/52 cannot be patched (writing into spare capacity
		// beyond len(out) would be silently dropped). Hand LibreOffice the
		// original data instead of a corrupt stream.
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
	for i, item := range items {
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

		// Prefix with the item index: soffice names every output after the
		// input's base name, so "image1.emf" and "image1.wmf" in one batch
		// would both produce "image1.png" and silently share one result.
		name = fmt.Sprintf("i%d_%s", i, name)
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
		// Trim the large white/transparent margins LibreOffice leaves around
		// EMF/WMF renders (notably equations and matrices, see issue #18).
		item.pngData = autoCropPNG(pngData)
		anySuccess = true
	}

	if !anySuccess && lastErr != nil {
		return fmt.Errorf("soffice batch conversion failed: %w", lastErr)
	}
	return nil
}

// sofficeTimeout bounds a single soffice invocation. LibreOffice occasionally
// hangs indefinitely in headless mode (e.g. a stuck first-run profile setup),
// which would otherwise block ConvertDir forever; see issue #60.
const sofficeTimeout = 5 * time.Minute

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

	ctx, cancel := context.WithTimeout(ctx, sofficeTimeout)
	defer cancel()

	args := []string{
		"--headless",
		"--norestore",
		"-env:UserInstallation=file://" + profileDir,
		"--convert-to", "png",
		"--outdir", outDir,
	}
	args = append(args, inputs...)

	cmd := exec.CommandContext(ctx, "soffice", args...)
	// The soffice wrapper forks soffice.bin; killing only the wrapper on
	// timeout leaves a child holding the output pipe, which would block
	// CombinedOutput forever. WaitDelay forcibly releases it.
	cmd.WaitDelay = 10 * time.Second
	output, err := cmd.CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("soffice timed out after %s (output: %s)", sofficeTimeout, string(output))
		}
		return fmt.Errorf("%w (output: %s)", err, string(output))
	}
	return nil
}
