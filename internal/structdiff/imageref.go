package structdiff

import (
	"path"
	"regexp"
	"strings"
)

// The prebuilt database stores converted image references (image3.png) while
// an on-demand archive fetch keeps the original filenames (image3.emf), so
// the same section text differs only in image reference spelling. These
// regexes fold both the Markdown link form and the HTML <img> form (table
// cells) to a canonical shape for diff comparison.
var (
	mdImageRE    = regexp.MustCompile(`!\[([^\]]*)\]\(image://([^?)\s]+)(\?[^)]*)?\)`)
	htmlImgTagRE = regexp.MustCompile(`<img\s[^>]*?\bsrc="image://([^"?]+)(\?[^"]*)?"[^>]*?>`)
	htmlImgAltRE = regexp.MustCompile(`\balt="([^"]*)"`)
)

// NormalizeImageRefs returns line with every image reference folded to a
// canonical form: a conversion-pair extension (EMF/WMF/PCZ source or PNG
// target) is dropped so a converted figure and its original compare equal,
// and a redundant alt text (empty or just the filename) becomes "Figure".
// Other extensions are kept — image3.jpg vs image3.png is a real reference
// change, not conversion spelling. Dimensions are kept too: a resized figure
// is a real change. The result is a comparison key for diffing only, never
// for display or storage.
func NormalizeImageRefs(line string) string {
	if !strings.Contains(line, "image://") {
		return line
	}
	line = mdImageRE.ReplaceAllStringFunc(line, func(m string) string {
		sub := mdImageRE.FindStringSubmatch(m)
		return "![" + foldAlt(sub[1], sub[2]) + "](image://" + foldName(sub[2]) + sub[3] + ")"
	})
	line = htmlImgTagRE.ReplaceAllStringFunc(line, func(m string) string {
		sub := htmlImgTagRE.FindStringSubmatch(m)
		alt := ""
		if a := htmlImgAltRE.FindStringSubmatch(m); a != nil {
			alt = a[1]
		}
		// Width/height attributes mirror the ?w=&h= query, so the canonical
		// tag keeps only the query and the folded alt.
		return `<img src="image://` + foldName(sub[1]) + sub[2] + `" alt="` + foldAlt(alt, sub[1]) + `">`
	})
	return line
}

// foldName drops the filename extension when it belongs to the EMF/WMF/PCZ →
// PNG conversion the pipeline performs; any other extension is meaningful and
// kept. The strip repeats so a collision-disambiguated conversion name like
// "image1.wmf.png" (see docx.ConvertImages, used when image1.emf and
// image1.wmf would otherwise both convert to "image1.png") still folds down
// to "image1", matching the original "image1.wmf" reference in an archived,
// unconverted version.
func foldName(name string) string {
	for {
		ext := strings.ToLower(path.Ext(name))
		switch ext {
		case ".emf", ".wmf", ".pcz", ".png":
			name = strings.TrimSuffix(name, path.Ext(name))
		default:
			return name
		}
	}
}

// foldAlt maps an alt text that adds no information beyond the filename to
// the "Figure" default the converter uses.
func foldAlt(alt, name string) string {
	if alt == "" || alt == name ||
		strings.TrimSuffix(alt, path.Ext(alt)) == strings.TrimSuffix(name, path.Ext(name)) {
		return "Figure"
	}
	return alt
}
