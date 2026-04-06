package docx

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestDecompressPCZ(t *testing.T) {
	// Build a fake PCZ: 8-byte proprietary header + gzip payload.
	payload := []byte("fake EMF data for testing")
	var buf bytes.Buffer
	// Write a dummy header before the gzip stream.
	buf.Write([]byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := decompressPCZ(buf.Bytes())
	if err != nil {
		t.Fatalf("decompressPCZ failed: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("decompressPCZ = %q, want %q", got, payload)
	}
}

func TestDecompressPCZ_NoMagic(t *testing.T) {
	data := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05}
	_, err := decompressPCZ(data)
	if err == nil {
		t.Fatal("expected error for data without gzip magic")
	}
}

func TestIsPCZ(t *testing.T) {
	if !isPCZ("image/x-pcz") {
		t.Error("isPCZ should return true for image/x-pcz")
	}
	if isPCZ("image/x-emf") {
		t.Error("isPCZ should return false for image/x-emf")
	}
}
