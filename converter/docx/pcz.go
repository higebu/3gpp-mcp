package docx

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
)

// gzipMagic is the magic number at the start of a gzip stream.
var gzipMagic = []byte{0x1f, 0x8b}

// decompressPCZ decompresses a PCZ (compressed metafile) to raw EMF/WMF data.
// PCZ files have a small proprietary header followed by a gzip-compressed
// EMF or WMF payload. This function scans for the gzip magic bytes and
// decompresses the stream from that offset.
func decompressPCZ(data []byte) ([]byte, error) {
	// Scan the first 64 bytes for the gzip magic number.
	// The header is typically 12-16 bytes, but we search a wider range to be safe.
	const maxScan = 64
	limit := min(maxScan, len(data))
	idx := bytes.Index(data[:limit], gzipMagic)
	if idx < 0 {
		return nil, fmt.Errorf("gzip magic not found in first %d bytes", limit)
	}

	r, err := gzip.NewReader(bytes.NewReader(data[idx:]))
	if err != nil {
		return nil, fmt.Errorf("gzip open: %w", err)
	}
	defer r.Close()

	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("gzip decompress: %w", err)
	}
	return raw, nil
}

// isPCZ returns true if the MIME type indicates a PCZ compressed metafile.
func isPCZ(mimeType string) bool {
	return mimeType == "image/x-pcz"
}
