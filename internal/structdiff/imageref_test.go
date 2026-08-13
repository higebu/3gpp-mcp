package structdiff

import "testing"

func TestNormalizeImageRefs(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		same bool
	}{
		{
			name: "extension difference folds",
			a:    "![Figure](image://image3.png?w=612&h=208)",
			b:    "![Figure](image://image3.emf?w=612&h=208)",
			same: true,
		},
		{
			name: "filename alt folds to Figure",
			a:    "![image3.emf](image://image3.emf?w=612&h=208)",
			b:    "![Figure](image://image3.png?w=612&h=208)",
			same: true,
		},
		{
			name: "table cell img extension and alt fold",
			a:    `<td><img src="image://image1.emf?w=100&h=50" alt="image1.emf" width="100" height="50"></td>`,
			b:    `<td><img src="image://image1.png?w=100&h=50" alt="Figure" width="100" height="50"></td>`,
			same: true,
		},
		{
			name: "different dimensions stay different",
			a:    "![Figure](image://image3.png?w=612&h=208)",
			b:    "![Figure](image://image3.emf?w=600&h=200)",
			same: false,
		},
		{
			name: "different basenames stay different",
			a:    "![Figure](image://image3.png)",
			b:    "![Figure](image://image4.png)",
			same: false,
		},
		{
			name: "non-conversion extension change stays different",
			a:    "![Figure](image://image3.jpg)",
			b:    "![Figure](image://image3.png)",
			same: false,
		},
		{
			name: "same non-conversion extension compares equal",
			a:    "![image3.jpg](image://image3.jpg?w=10&h=20)",
			b:    "![Figure](image://image3.jpg?w=10&h=20)",
			same: true,
		},
		{
			name: "pcz source folds with png target",
			a:    "![Figure](image://image7.pcz)",
			b:    "![Figure](image://image7.png)",
			same: true,
		},
		{
			name: "real alt text change stays different",
			a:    "![Network Topology](image://image3.png)",
			b:    "![Attach Procedure](image://image3.png)",
			same: false,
		},
		{
			name: "real alt text survives extension fold",
			a:    "![Network Topology](image://image3.emf?w=10&h=20)",
			b:    "![Network Topology](image://image3.png?w=10&h=20)",
			same: true,
		},
		{
			name: "multiple refs on one line",
			a:    "see ![Figure](image://image1.emf) and ![Figure](image://image2.emf)",
			b:    "see ![Figure](image://image1.png) and ![Figure](image://image2.png)",
			same: true,
		},
		{
			name: "surrounding text still compared",
			a:    "old text ![Figure](image://image1.emf)",
			b:    "new text ![Figure](image://image1.png)",
			same: false,
		},
		{
			// docx.ConvertImages disambiguates a name collision (e.g.
			// image1.emf and image1.wmf both wanting "image1.png") by
			// keeping the original extension: image1.wmf -> image1.wmf.png.
			// The archived, unconverted version still references
			// image://image1.wmf, so these must still fold equal.
			name: "collision-disambiguated conversion name folds with its archived original",
			a:    "![Figure](image://image1.wmf)",
			b:    "![Figure](image://image1.wmf.png)",
			same: true,
		},
		{
			// A filename-shaped alt must fold the same way against the
			// disambiguated conversion name as against the original, or the
			// alt survives on one side only and the keys differ.
			name: "filename alt folds against disambiguated conversion name",
			a:    "![image1.wmf](image://image1.wmf)",
			b:    "![image1.wmf](image://image1.wmf.png)",
			same: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ka, kb := NormalizeImageRefs(tt.a), NormalizeImageRefs(tt.b)
			if (ka == kb) != tt.same {
				t.Errorf("NormalizeImageRefs equality = %v, want %v\n key a: %q\n key b: %q", ka == kb, tt.same, ka, kb)
			}
		})
	}
}

func TestNormalizeImageRefs_NoImageUnchanged(t *testing.T) {
	for _, line := range []string{
		"",
		"plain text with no images",
		"a [Figure: diagram not extracted — see the original document] placeholder",
		"markdown link [text](https://example.com)",
	} {
		if got := NormalizeImageRefs(line); got != line {
			t.Errorf("line without image refs changed: %q -> %q", line, got)
		}
	}
}
