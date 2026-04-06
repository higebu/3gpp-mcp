package docx

import (
	"archive/zip"
	"io"
	"path/filepath"
	"strings"
)

// mimeTypes maps file extensions to MIME types for image files.
var mimeTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".bmp":  "image/bmp",
	".tiff": "image/tiff",
	".tif":  "image/tiff",
	".emf":  "image/x-emf",
	".wmf":  "image/x-wmf",
	".pcz":  "image/x-pcz",
}

// llmReadableTypes are MIME types that multimodal LLMs can directly interpret.
var llmReadableTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// isLLMReadable returns true if the given MIME type can be interpreted by LLMs.
func isLLMReadable(mimeType string) bool {
	return llmReadableTypes[mimeType]
}

// extractImages reads all image files referenced by the relationship map from the
// DOCX zip archive. The returned map is keyed by the media filename (e.g., "image1.emf").
func extractImages(r *zip.Reader, relMap map[string]string) map[string]*EmbeddedImage {
	// Collect unique media paths from relationship map.
	mediaPaths := make(map[string]bool)
	for _, target := range relMap {
		mediaPaths[target] = true
	}

	images := make(map[string]*EmbeddedImage)
	for _, f := range r.File {
		// Strip the "word/" prefix to match relationship targets (e.g., "media/image1.emf").
		relPath := strings.TrimPrefix(f.Name, "word/")
		if !mediaPaths[relPath] {
			continue
		}

		data, err := readZipFileEntry(f)
		if err != nil {
			continue
		}

		name := filepath.Base(f.Name)
		ext := strings.ToLower(filepath.Ext(name))
		mime, ok := mimeTypes[ext]
		if !ok {
			mime = "application/octet-stream"
		}

		images[relPath] = &EmbeddedImage{
			Name:        name,
			MIMEType:    mime,
			Data:        data,
			LLMReadable: isLLMReadable(mime),
		}
	}
	return images
}

// readZipFileEntry reads the contents of a single zip file entry.
func readZipFileEntry(f *zip.File) ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}
