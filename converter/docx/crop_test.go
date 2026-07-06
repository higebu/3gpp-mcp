package docx

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// encodePNG renders img to PNG bytes for use as test input.
func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode PNG: %v", err)
	}
	return buf.Bytes()
}

// decodePNG decodes PNG bytes back into an image for assertions.
func decodePNG(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode PNG: %v", err)
	}
	return img
}

func TestAutoCropPNG_WhiteMargin(t *testing.T) {
	// 100x100 white canvas with a 20x20 black square at (40,40)-(60,60).
	const size = 100
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	for y := 40; y < 60; y++ {
		for x := 40; x < 60; x++ {
			img.Set(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 255})
		}
	}

	out := autoCropPNG(encodePNG(t, img))
	cropped := decodePNG(t, out)
	b := cropped.Bounds()

	// Content is 20x20; with cropPadding=4 on each side the result is 28x28.
	wantW, wantH := 20+2*cropPadding, 20+2*cropPadding
	if b.Dx() != wantW || b.Dy() != wantH {
		t.Errorf("cropped size = %dx%d, want %dx%d", b.Dx(), b.Dy(), wantW, wantH)
	}
	if b.Dx() >= size || b.Dy() >= size {
		t.Errorf("crop did not shrink image: %dx%d", b.Dx(), b.Dy())
	}
}

func TestAutoCropPNG_TransparentMargin(t *testing.T) {
	const size = 80
	img := image.NewNRGBA(image.Rect(0, 0, size, size)) // fully transparent
	// Opaque red content at (30,30)-(50,50).
	for y := 30; y < 50; y++ {
		for x := 30; x < 50; x++ {
			img.Set(x, y, color.NRGBA{R: 200, G: 0, B: 0, A: 255})
		}
	}

	out := autoCropPNG(encodePNG(t, img))
	cropped := decodePNG(t, out)
	b := cropped.Bounds()

	wantW, wantH := 20+2*cropPadding, 20+2*cropPadding
	if b.Dx() != wantW || b.Dy() != wantH {
		t.Errorf("cropped size = %dx%d, want %dx%d", b.Dx(), b.Dy(), wantW, wantH)
	}
}

func TestAutoCropPNG_AllBackground(t *testing.T) {
	const size = 50
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, color.NRGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	in := encodePNG(t, img)
	out := autoCropPNG(in)
	if !bytes.Equal(in, out) {
		t.Error("all-white image should be returned unchanged")
	}
}

func TestAutoCropPNG_NoMargin(t *testing.T) {
	// Image that is entirely content should be returned unchanged (crop == bounds).
	const size = 30
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			img.Set(x, y, color.NRGBA{R: 0, G: 0, B: 0, A: 255})
		}
	}
	in := encodePNG(t, img)
	out := autoCropPNG(in)
	if !bytes.Equal(in, out) {
		t.Error("full-content image should be returned unchanged")
	}
}

func TestAutoCropPNG_InvalidData(t *testing.T) {
	in := []byte("not a png")
	out := autoCropPNG(in)
	if !bytes.Equal(in, out) {
		t.Error("invalid PNG data should be returned unchanged")
	}
}
