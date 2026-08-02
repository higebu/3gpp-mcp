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
// canonical form: the filename loses its extension (a converted PNG and its
// EMF/WMF original compare equal) and a redundant alt text (empty or just the
// filename) becomes "Figure". Dimensions are kept — a resized figure is a
// real change. The result is a comparison key for diffing only, never for
// display or storage.
func NormalizeImageRefs(line string) string {
	if !strings.Contains(line, "image://") {
		return line
	}
	line = mdImageRE.ReplaceAllStringFunc(line, func(m string) string {
		sub := mdImageRE.FindStringSubmatch(m)
		base := strings.TrimSuffix(sub[2], path.Ext(sub[2]))
		return "![" + foldAlt(sub[1], base) + "](image://" + base + sub[3] + ")"
	})
	line = htmlImgTagRE.ReplaceAllStringFunc(line, func(m string) string {
		sub := htmlImgTagRE.FindStringSubmatch(m)
		base := strings.TrimSuffix(sub[1], path.Ext(sub[1]))
		alt := ""
		if a := htmlImgAltRE.FindStringSubmatch(m); a != nil {
			alt = a[1]
		}
		// Width/height attributes mirror the ?w=&h= query, so the canonical
		// tag keeps only the query and the folded alt.
		return `<img src="image://` + base + sub[2] + `" alt="` + foldAlt(alt, base) + `">`
	})
	return line
}

// foldAlt maps an alt text that adds no information beyond the filename to
// the "Figure" default the converter uses.
func foldAlt(alt, base string) string {
	if alt == "" || strings.TrimSuffix(alt, path.Ext(alt)) == base {
		return "Figure"
	}
	return alt
}
