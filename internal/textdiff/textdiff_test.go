package textdiff

import (
	"fmt"
	"strings"
	"testing"
)

// apply replays an edit script and returns the reconstructed sides.
func apply(edits []Edit) (a, b []string) {
	for _, e := range edits {
		switch e.Op {
		case Equal:
			a = append(a, e.Line)
			b = append(b, e.Line)
		case Delete:
			a = append(a, e.Line)
		case Insert:
			b = append(b, e.Line)
		}
	}
	return a, b
}

func checkRoundTrip(t *testing.T, a, b []string) []Edit {
	t.Helper()
	edits, _ := Diff(a, b)
	gotA, gotB := apply(edits)
	if strings.Join(gotA, "\n") != strings.Join(a, "\n") {
		t.Errorf("edit script does not reproduce a:\ngot  %q\nwant %q", gotA, a)
	}
	if strings.Join(gotB, "\n") != strings.Join(b, "\n") {
		t.Errorf("edit script does not reproduce b:\ngot  %q\nwant %q", gotB, b)
	}
	return edits
}

func TestDiffIdentical(t *testing.T) {
	a := []string{"one", "two", "three"}
	edits := checkRoundTrip(t, a, a)
	if del, ins := Stats(edits); del != 0 || ins != 0 {
		t.Errorf("Stats = (%d, %d), want (0, 0)", del, ins)
	}
	if got := Unified(a, a, 3); got != "" {
		t.Errorf("Unified of identical inputs = %q, want empty", got)
	}
}

func TestDiffEmptySides(t *testing.T) {
	lines := []string{"one", "two"}

	edits := checkRoundTrip(t, nil, lines)
	if del, ins := Stats(edits); del != 0 || ins != 2 {
		t.Errorf("empty→lines Stats = (%d, %d), want (0, 2)", del, ins)
	}

	edits = checkRoundTrip(t, lines, nil)
	if del, ins := Stats(edits); del != 2 || ins != 0 {
		t.Errorf("lines→empty Stats = (%d, %d), want (2, 0)", del, ins)
	}

	if edits, _ := Diff(nil, nil); len(edits) != 0 {
		t.Errorf("Diff(nil, nil) = %v, want empty", edits)
	}
}

func TestDiffChanges(t *testing.T) {
	cases := []struct {
		name     string
		a, b     []string
		del, ins int
	}{
		{"change at start", []string{"old", "two", "three"}, []string{"new", "two", "three"}, 1, 1},
		{"change at end", []string{"one", "two", "old"}, []string{"one", "two", "new"}, 1, 1},
		{"change in middle", []string{"one", "old", "three"}, []string{"one", "new", "three"}, 1, 1},
		{"insertion", []string{"one", "three"}, []string{"one", "two", "three"}, 0, 1},
		{"deletion", []string{"one", "two", "three"}, []string{"one", "three"}, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edits := checkRoundTrip(t, tc.a, tc.b)
			if del, ins := Stats(edits); del != tc.del || ins != tc.ins {
				t.Errorf("Stats = (%d, %d), want (%d, %d)", del, ins, tc.del, tc.ins)
			}
		})
	}
}

func TestUnifiedFormat(t *testing.T) {
	a := []string{"one", "two", "old", "four", "five"}
	b := []string{"one", "two", "new", "four", "five"}

	got := Unified(a, b, 1)
	want := "@@ -2,3 +2,3 @@\n two\n-old\n+new\n four"
	if got != want {
		t.Errorf("Unified = %q, want %q", got, want)
	}
}

func TestUnifiedZeroContext(t *testing.T) {
	a := []string{"one", "old", "three"}
	b := []string{"one", "new", "three"}

	got := Unified(a, b, 0)
	want := "@@ -2,1 +2,1 @@\n-old\n+new"
	if got != want {
		t.Errorf("Unified = %q, want %q", got, want)
	}
}

// TestUnifiedMergesNearbyHunks checks that changes closer than a context apart
// render as one hunk rather than two overlapping ones.
func TestUnifiedMergesNearbyHunks(t *testing.T) {
	a := []string{"one", "old1", "three", "old2", "five"}
	b := []string{"one", "new1", "three", "new2", "five"}

	got := Unified(a, b, 1)
	if strings.Count(got, "@@ -") != 1 {
		t.Errorf("changes 2 lines apart with context 1 should merge into one hunk:\n%s", got)
	}

	// With zero context the same changes stay separate hunks.
	got = Unified(a, b, 0)
	if strings.Count(got, "@@ -") != 2 {
		t.Errorf("changes with zero context should stay separate hunks:\n%s", got)
	}
}

func TestUnifiedPureInsertionRange(t *testing.T) {
	a := []string{"one", "two"}
	b := []string{"one", "extra", "two"}

	got := Unified(a, b, 0)
	// A zero-length source range starts one line earlier, per unified convention.
	want := "@@ -1,0 +2,1 @@\n+extra"
	if got != want {
		t.Errorf("Unified = %q, want %q", got, want)
	}
}

// TestDiffCapFallback checks that an edit distance beyond the search limit
// falls back to a full replacement instead of burning quadratic time.
func TestDiffCapFallback(t *testing.T) {
	var a, b []string
	for i := 0; i < dCap; i++ {
		a = append(a, fmt.Sprintf("a%d", i))
		b = append(b, fmt.Sprintf("b%d", i))
	}

	edits, capped := Diff(a, b)
	if !capped {
		t.Fatal("expected the cap fallback for disjoint inputs beyond dCap edits")
	}
	if del, ins := Stats(edits); del != len(a) || ins != len(b) {
		t.Errorf("fallback Stats = (%d, %d), want full replacement (%d, %d)", del, ins, len(a), len(b))
	}

	if got := Unified(a, b, 3); !strings.HasPrefix(got, "[diff too large; showing full replacement]") {
		t.Errorf("Unified should note the fallback, got %q", got[:min(len(got), 80)])
	}
}

// TestDiffLargeButLocalChange checks that big inputs with a small change stay
// on the exact path thanks to prefix/suffix trimming.
func TestDiffLargeButLocalChange(t *testing.T) {
	var a []string
	for i := 0; i < 20000; i++ {
		a = append(a, fmt.Sprintf("line %d", i))
	}
	b := append(append([]string{}, a...), "")
	copy(b, a)
	b[10000] = "changed"
	b = b[:len(a)]

	edits, capped := Diff(a, b)
	if capped {
		t.Fatal("a single changed line must not hit the cap")
	}
	if del, ins := Stats(edits); del != 1 || ins != 1 {
		t.Errorf("Stats = (%d, %d), want (1, 1)", del, ins)
	}
}

func TestDiffKeyedNilMatchesDiff(t *testing.T) {
	a := []string{"one", "two", "three"}
	b := []string{"one", "2", "three"}
	got, gotCapped := DiffKeyed(a, b, nil)
	want, wantCapped := Diff(a, b)
	if gotCapped != wantCapped || len(got) != len(want) {
		t.Fatalf("DiffKeyed(nil) = %v/%v, want %v/%v", got, gotCapped, want, wantCapped)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("edit %d: got %v, want %v", i, got[i], want[i])
		}
	}
}

func TestUnifiedKeyedFoldsKeyEqualLines(t *testing.T) {
	key := func(l string) string { return strings.TrimSuffix(l, ".emf") }
	a := []string{"text", "figure.emf", "more"}
	b := []string{"text", "figure", "more"}
	if got := UnifiedKeyed(a, b, 3, key); got != "" {
		t.Errorf("key-equal inputs should render empty, got %q", got)
	}
}

func TestUnifiedKeyedShowsNewSideContext(t *testing.T) {
	key := func(l string) string { return strings.TrimSuffix(l, ".emf") }
	a := []string{"figure.emf", "old line", "tail.emf"}
	b := []string{"figure", "new line", "tail"}
	got := UnifiedKeyed(a, b, 1, key)
	want := "@@ -1,3 +1,3 @@\n figure\n-old line\n+new line\n tail"
	if got != want {
		t.Errorf("UnifiedKeyed = %q, want %q", got, want)
	}
}

func TestDiffKeyedStats(t *testing.T) {
	key := func(l string) string { return strings.TrimSuffix(l, ".emf") }
	edits, _ := DiffKeyed([]string{"same.emf", "gone"}, []string{"same", "here"}, key)
	del, ins := Stats(edits)
	if del != 1 || ins != 1 {
		t.Errorf("Stats = %d/%d, want 1/1", del, ins)
	}
}
