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
