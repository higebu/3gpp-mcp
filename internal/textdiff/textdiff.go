// Package textdiff computes line-based diffs for LLM-facing unified output.
package textdiff

import (
	"fmt"
	"strings"
)

// Op is the kind of a single edit.
type Op int

const (
	Equal Op = iota
	Delete
	Insert
)

// Edit is one line of an edit script.
type Edit struct {
	Op   Op
	Line string
}

// dCap bounds the edit distance the Myers search explores. Beyond roughly a
// thousand edits a unified diff is unreadable anyway, and both the search time
// and the backtracking trace grow quadratically with the distance, so larger
// inputs fall back to a full replacement.
const dCap = 1024

// Diff computes a line-level edit script between a and b. capped reports that
// the edit distance exceeded the search limit and the script is a full
// replacement rather than a minimal diff.
func Diff(a, b []string) (edits []Edit, capped bool) {
	return DiffKeyed(a, b, nil)
}

// DiffKeyed is Diff comparing lines by key(line) while reporting original
// lines, so cosmetic per-line differences (e.g. image reference notation) can
// be ignored without altering what the caller displays. A nil key compares
// lines verbatim. Equal edits carry the b-side original: when two lines match
// only by key, the newer spelling is the one worth showing.
func DiffKeyed(a, b []string, key func(string) string) (edits []Edit, capped bool) {
	// Interned lines let the Myers loop compare ints instead of strings.
	ids := make(map[string]int, len(a)+len(b))
	intern := func(lines []string) []int {
		out := make([]int, len(lines))
		for i, l := range lines {
			if key != nil {
				l = key(l)
			}
			id, ok := ids[l]
			if !ok {
				id = len(ids)
				ids[l] = id
			}
			out[i] = id
		}
		return out
	}
	ia, ib := intern(a), intern(b)

	// Specification revisions are mostly local changes, so trimming the common
	// prefix and suffix keeps the O(ND) search on the changed region only.
	prefix := 0
	for prefix < len(ia) && prefix < len(ib) && ia[prefix] == ib[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(ia)-prefix && suffix < len(ib)-prefix && ia[len(ia)-1-suffix] == ib[len(ib)-1-suffix] {
		suffix++
	}

	edits = make([]Edit, 0, len(a)+len(b))
	for i := 0; i < prefix; i++ {
		edits = append(edits, Edit{Equal, b[i]})
	}

	ma, mb := ia[prefix:len(ia)-suffix], ib[prefix:len(ib)-suffix]
	ops := myers(ma, mb)
	if ops == nil && len(ma)+len(mb) > 0 {
		capped = true
		for _, l := range a[prefix : len(a)-suffix] {
			edits = append(edits, Edit{Delete, l})
		}
		for _, l := range b[prefix : len(b)-suffix] {
			edits = append(edits, Edit{Insert, l})
		}
	} else {
		ai, bi := prefix, prefix
		for _, op := range ops {
			switch op {
			case Equal:
				edits = append(edits, Edit{Equal, b[bi]})
				ai++
				bi++
			case Delete:
				edits = append(edits, Edit{Delete, a[ai]})
				ai++
			case Insert:
				edits = append(edits, Edit{Insert, b[bi]})
				bi++
			}
		}
	}

	for i := len(b) - suffix; i < len(b); i++ {
		edits = append(edits, Edit{Equal, b[i]})
	}
	return edits, capped
}

// myers returns the shortest edit script between a and b as a forward list of
// ops, or nil when the edit distance exceeds dCap.
func myers(a, b []int) []Op {
	n, m := len(a), len(b)
	if n+m == 0 {
		return []Op{}
	}
	limit := n + m
	if limit > dCap {
		limit = dCap
	}
	offset := limit
	v := make([]int, 2*limit+1)
	// trace[d] is the frontier before round d, which backtracking replays.
	trace := make([][]int, 0, limit+1)

	found := -1
search:
	for d := 0; d <= limit; d++ {
		trace = append(trace, append([]int(nil), v...))
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[offset+k-1] < v[offset+k+1]) {
				x = v[offset+k+1]
			} else {
				x = v[offset+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[offset+k] = x
			if x >= n && y >= m {
				found = d
				break search
			}
		}
	}
	if found < 0 {
		return nil
	}

	ops := make([]Op, 0, n+m)
	x, y := n, m
	for d := found; d > 0; d-- {
		prev := trace[d]
		k := x - y
		var prevK int
		if k == -d || (k != d && prev[offset+k-1] < prev[offset+k+1]) {
			prevK = k + 1
		} else {
			prevK = k - 1
		}
		prevX := prev[offset+prevK]
		prevY := prevX - prevK
		for x > prevX && y > prevY {
			ops = append(ops, Equal)
			x--
			y--
		}
		if prevK == k+1 {
			ops = append(ops, Insert)
			y--
		} else {
			ops = append(ops, Delete)
			x--
		}
	}
	for x > 0 && y > 0 {
		ops = append(ops, Equal)
		x--
		y--
	}
	for i, j := 0, len(ops)-1; i < j; i, j = i+1, j-1 {
		ops[i], ops[j] = ops[j], ops[i]
	}
	return ops
}

// Stats counts the deleted and inserted lines of an edit script.
func Stats(edits []Edit) (del, ins int) {
	for _, e := range edits {
		switch e.Op {
		case Delete:
			del++
		case Insert:
			ins++
		}
	}
	return del, ins
}

// Unified renders the diff between a and b as unified-diff hunks with the
// given number of context lines. Identical inputs render as an empty string.
func Unified(a, b []string, context int) string {
	return UnifiedKeyed(a, b, context, nil)
}

// UnifiedKeyed is Unified comparing lines by key(line); see DiffKeyed. Inputs
// whose lines all match by key render as an empty string.
func UnifiedKeyed(a, b []string, context int, key func(string) string) string {
	if context < 0 {
		context = 0
	}
	edits, capped := DiffKeyed(a, b, key)

	// keep marks the lines that appear in some hunk: every change plus its
	// surrounding context. Adjacent hunks closer than a context apart merge by
	// construction.
	keep := make([]bool, len(edits))
	changed := false
	for i, e := range edits {
		if e.Op == Equal {
			continue
		}
		changed = true
		lo := i - context
		if lo < 0 {
			lo = 0
		}
		hi := i + context
		if hi > len(edits)-1 {
			hi = len(edits) - 1
		}
		for j := lo; j <= hi; j++ {
			keep[j] = true
		}
	}
	if !changed {
		return ""
	}

	var sb strings.Builder
	if capped {
		sb.WriteString("[diff too large; showing full replacement]\n")
	}

	aPos, bPos := 1, 1
	i := 0
	for i < len(edits) {
		if !keep[i] {
			aPos++
			bPos++
			i++
			continue
		}
		aStart, bStart := aPos, bPos
		aCount, bCount := 0, 0
		var lines []string
		for i < len(edits) && keep[i] {
			e := edits[i]
			switch e.Op {
			case Equal:
				lines = append(lines, " "+e.Line)
				aPos++
				bPos++
				aCount++
				bCount++
			case Delete:
				lines = append(lines, "-"+e.Line)
				aPos++
				aCount++
			case Insert:
				lines = append(lines, "+"+e.Line)
				bPos++
				bCount++
			}
			i++
		}
		// Unified convention: a zero-length range starts one line earlier.
		hunkAStart, hunkBStart := aStart, bStart
		if aCount == 0 {
			hunkAStart--
		}
		if bCount == 0 {
			hunkBStart--
		}
		fmt.Fprintf(&sb, "@@ -%d,%d +%d,%d @@\n", hunkAStart, aCount, hunkBStart, bCount)
		for _, l := range lines {
			sb.WriteString(l)
			sb.WriteByte('\n')
		}
	}
	return strings.TrimSuffix(sb.String(), "\n")
}
