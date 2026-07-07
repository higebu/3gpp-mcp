package docx

import (
	"bytes"
	"image"
	"image/png"
)

const (
	// cropWhiteThreshold is the minimum per-channel value (0–255) for a pixel
	// to be treated as background "white". LibreOffice-rendered equations often
	// have slightly off-white anti-aliased edges, so a small tolerance below
	// 255 avoids treating faint content as margin.
	cropWhiteThreshold = 250
	// cropAlphaThreshold is the maximum alpha (0–255) for a pixel to be treated
	// as transparent background. EMF renders frequently carry an alpha channel.
	cropAlphaThreshold = 8
	// cropPadding is the number of background pixels kept around the detected
	// content so the crop is not flush against the drawing.
	cropPadding = 4
)

// autoCropPNG removes surrounding white/transparent margins from a PNG image.
// It decodes the image, computes the bounding box of non-background pixels
// (background = near-white opaque OR nearly transparent), adds a small padding,
// and re-encodes the cropped region. If the input is not a decodable PNG, has
// no content, or would not shrink meaningfully, the original bytes are returned
// unchanged so the caller can always rely on getting a usable image back.
func autoCropPNG(data []byte) []byte {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}

	bounds := img.Bounds()
	minX, minY := bounds.Max.X, bounds.Max.Y
	maxX, maxY := bounds.Min.X, bounds.Min.Y
	found := false

	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if isBackgroundPixel(img.At(x, y)) {
				continue
			}
			found = true
			if x < minX {
				minX = x
			}
			if x > maxX {
				maxX = x
			}
			if y < minY {
				minY = y
			}
			if y > maxY {
				maxY = y
			}
		}
	}

	// Entirely background: nothing to crop.
	if !found {
		return data
	}

	// Add padding and clamp to the image bounds.
	minX -= cropPadding
	minY -= cropPadding
	maxX += cropPadding
	maxY += cropPadding
	if minX < bounds.Min.X {
		minX = bounds.Min.X
	}
	if minY < bounds.Min.Y {
		minY = bounds.Min.Y
	}
	if maxX >= bounds.Max.X {
		maxX = bounds.Max.X - 1
	}
	if maxY >= bounds.Max.Y {
		maxY = bounds.Max.Y - 1
	}

	crop := image.Rect(minX, minY, maxX+1, maxY+1)
	// Nothing gained if the crop covers the whole image.
	if crop.Eq(bounds) {
		return data
	}

	cropped := subImage(img, crop)

	var buf bytes.Buffer
	if err := png.Encode(&buf, cropped); err != nil {
		return data
	}
	return buf.Bytes()
}

// isBackgroundPixel reports whether c is margin: nearly transparent, or an
// opaque near-white pixel.
func isBackgroundPixel(c interface{ RGBA() (r, g, b, a uint32) }) bool {
	r, g, b, a := c.RGBA()
	// Convert from 16-bit (0–65535) to 8-bit (0–255).
	r8, g8, b8, a8 := r>>8, g>>8, b>>8, a>>8
	if a8 <= cropAlphaThreshold {
		return true
	}
	return r8 >= cropWhiteThreshold && g8 >= cropWhiteThreshold && b8 >= cropWhiteThreshold
}

// subImage returns the sub-region r of img. Standard library image types expose
// a SubImage method that shares pixels without copying; for any other image
// type the region is copied into a fresh NRGBA image.
func subImage(img image.Image, r image.Rectangle) image.Image {
	if sub, ok := img.(interface {
		SubImage(r image.Rectangle) image.Image
	}); ok {
		return sub.SubImage(r)
	}
	dst := image.NewNRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			dst.Set(x, y, img.At(r.Min.X+x, r.Min.Y+y))
		}
	}
	return dst
}
