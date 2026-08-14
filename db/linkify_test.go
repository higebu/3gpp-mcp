package db

import (
	"testing"
)

func urlFor(spec, section string) string {
	if len(spec) > 4 && spec[:4] == "RFC " {
		u := "https://www.rfc-editor.org/rfc/rfc" + spec[4:]
		if section != "" {
			u += "#section-" + section
		}
		return u
	}
	u := "/specs/" + spec
	if section != "" {
		u += "/sections/" + section
	}
	return u
}

func TestLinkifyRefs_TS(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple TS ref",
			input: "See TS 23.501 for details.",
			want:  "See [TS 23.501](/specs/TS 23.501) for details.",
		},
		{
			name:  "TS ref with clause",
			input: "See TS 23.501 clause 5.1 for details.",
			want:  "See [TS 23.501 clause 5.1](/specs/TS 23.501/sections/5.1) for details.",
		},
		{
			name:  "3GPP TS ref",
			input: "Defined in 3GPP TS 38.300.",
			want:  "Defined in [3GPP TS 38.300](/specs/TS 38.300).",
		},
		{
			name:  "TR ref",
			input: "See TR 21.905 for vocabulary.",
			want:  "See [TR 21.905](/specs/TR 21.905) for vocabulary.",
		},
		{
			name:  "clause before spec (tsPrefixRefRE)",
			input: "As defined in clause 5.1 of TS 23.501.",
			want:  "As defined in [clause 5.1 of TS 23.501](/specs/TS 23.501/sections/5.1).",
		},
		{
			name:  "annex ref",
			input: "See TS 33.203 Annex H for security.",
			want:  "See [TS 33.203 Annex H](/specs/TS 33.203/sections/H) for security.",
		},
		{
			name:  "section number with mid letter (TS 24.502)",
			input: "See clause 5.3A.2 of TS 24.502 for details.",
			want:  "See [clause 5.3A.2 of TS 24.502](/specs/TS 24.502/sections/5.3A.2) for details.",
		},
		{
			name:  "subclause with mid letter spec first",
			input: "See TS 24.502 subclause 5.3A.2 for details.",
			want:  "See [TS 24.502 subclause 5.3A.2](/specs/TS 24.502/sections/5.3A.2) for details.",
		},
		{
			name:  "deeply nested mid letter",
			input: "See clause 4.2.2.2A.1 of TS 24.502.",
			want:  "See [clause 4.2.2.2A.1 of TS 24.502](/specs/TS 24.502/sections/4.2.2.2A.1).",
		},
		{
			name:  "multi-part spec with clause",
			input: "See TS 38.101-1 clause 5.2 for details.",
			want:  "See [TS 38.101-1 clause 5.2](/specs/TS 38.101-1/sections/5.2) for details.",
		},
		{
			name:  "multi-part spec sentence-final",
			input: "as specified in TS 36.521-1.",
			want:  "as specified in [TS 36.521-1](/specs/TS 36.521-1).",
		},
		{
			name:  "clause before multi-part spec",
			input: "Same as subclause 5.5.4.2 of TS 36.521-1.",
			want:  "Same as [subclause 5.5.4.2 of TS 36.521-1](/specs/TS 36.521-1/sections/5.5.4.2).",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{URLFor: urlFor})
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLinkifyRefs_RFC(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "simple RFC ref",
			input: "See RFC 3748 for details.",
			want:  "See [RFC 3748](https://www.rfc-editor.org/rfc/rfc3748) for details.",
		},
		{
			name:  "RFC with section",
			input: "See RFC 3748 section 3.1.",
			want:  "See [RFC 3748 section 3.1](https://www.rfc-editor.org/rfc/rfc3748#section-3.1).",
		},
		{
			name:  "IETF RFC ref",
			input: "See IETF RFC 4868.",
			want:  "See [IETF RFC 4868](https://www.rfc-editor.org/rfc/rfc4868).",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{URLFor: urlFor})
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLinkifyRefs_Bracket(t *testing.T) {
	bracketMap := map[string]string{
		"19":  "TS 33.203",
		"2":   "TS 23.228",
		"13D": "TS 29.214",
		"45":  "TS 37.579-1",
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "bracket ref with clause",
			input: "See [19] clause 6 for details.",
			want:  "See [[19] clause 6](/specs/TS 33.203/sections/6) for details.",
		},
		{
			name:  "bracket ref with annex",
			input: "See [19] Annex H.",
			want:  "See [[19] Annex H](/specs/TS 33.203/sections/H).",
		},
		{
			name:  "bracket ref with letter suffix",
			input: "See [13D] subclause 5.2.",
			want:  "See [[13D] subclause 5.2](/specs/TS 29.214/sections/5.2).",
		},
		{
			name:  "unknown bracket ref ignored",
			input: "See [99] clause 3.",
			want:  "See [99] clause 3.",
		},
		{
			name:  "bracket ref to multi-part spec",
			input: "See [45] clause 5.2.",
			want:  "See [[45] clause 5.2](/specs/TS 37.579-1/sections/5.2).",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{BracketMap: bracketMap, URLFor: urlFor})
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLinkifyRefs_MultiSection(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "two clauses with and (prefix)",
			input: "See clauses 8.2 and 16.11 of TS 23.402 for details.",
			want:  "See clauses [8.2](/specs/TS 23.402/sections/8.2) and [16.11](/specs/TS 23.402/sections/16.11) of [TS 23.402](/specs/TS 23.402) for details.",
		},
		{
			name:  "three clauses with commas and and",
			input: "See clauses 8.2, 16.11 and 5.1 of TS 23.501.",
			want:  "See clauses [8.2](/specs/TS 23.501/sections/8.2), [16.11](/specs/TS 23.501/sections/16.11) and [5.1](/specs/TS 23.501/sections/5.1) of [TS 23.501](/specs/TS 23.501).",
		},
		{
			name:  "subclauses",
			input: "subclauses 5.1 and 5.2 of TS 23.501",
			want:  "subclauses [5.1](/specs/TS 23.501/sections/5.1) and [5.2](/specs/TS 23.501/sections/5.2) of [TS 23.501](/specs/TS 23.501)",
		},
		{
			name:  "Annexes",
			input: "Annexes A and B of TS 33.203",
			want:  "Annexes [A](/specs/TS 33.203/sections/A) and [B](/specs/TS 33.203/sections/B) of [TS 33.203](/specs/TS 33.203)",
		},
		{
			name:  "with trailing bracket ref",
			input: "clauses 8.2 and 16.11 of TS 23.402 [45]",
			want:  "clauses [8.2](/specs/TS 23.402/sections/8.2) and [16.11](/specs/TS 23.402/sections/16.11) of [TS 23.402](/specs/TS 23.402) [45]",
		},
		{
			name:  "with 3GPP prefix",
			input: "clauses 8.2 and 16.11 of 3GPP TS 23.402",
			want:  "clauses [8.2](/specs/TS 23.402/sections/8.2) and [16.11](/specs/TS 23.402/sections/16.11) of 3GPP [TS 23.402](/specs/TS 23.402)",
		},
		{
			name:  "spec-first multi-section",
			input: "TS 23.402 clauses 8.2 and 16.11",
			want:  "[TS 23.402](/specs/TS 23.402) clauses [8.2](/specs/TS 23.402/sections/8.2) and [16.11](/specs/TS 23.402/sections/16.11)",
		},
		{
			name:  "spec-first three sections",
			input: "TS 23.501 clauses 5.1, 5.2 and 5.3",
			want:  "[TS 23.501](/specs/TS 23.501) clauses [5.1](/specs/TS 23.501/sections/5.1), [5.2](/specs/TS 23.501/sections/5.2) and [5.3](/specs/TS 23.501/sections/5.3)",
		},
		{
			name:  "single clause still uses existing pattern",
			input: "clause 5.1 of TS 23.501",
			want:  "[clause 5.1 of TS 23.501](/specs/TS 23.501/sections/5.1)",
		},
		{
			name:  "spec-first multi-section with part suffix",
			input: "TS 38.101-1 clauses 8.2 and 16.11",
			want:  "[TS 38.101-1](/specs/TS 38.101-1) clauses [8.2](/specs/TS 38.101-1/sections/8.2) and [16.11](/specs/TS 38.101-1/sections/16.11)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{URLFor: urlFor})
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLinkifyRefs_NilBracketMap(t *testing.T) {
	// Bracket refs should NOT be replaced when bracketMap is nil.
	input := "See [19] clause 6 for details."
	got := LinkifyRefs(input, LinkifyRefsOpts{URLFor: urlFor})
	if got != input {
		t.Errorf("expected no change with nil bracketMap, got %q", got)
	}
}

func TestLinkifyRefs_ExistingLink(t *testing.T) {
	// References inside existing Markdown links should not be double-replaced.
	input := "See [TS 23.501 clause 5](/specs/TS%2023.501/sections/5) for details."
	got := LinkifyRefs(input, LinkifyRefsOpts{URLFor: urlFor})
	if got != input {
		t.Errorf("existing link was modified:\n got:  %q\n want: %q", got, input)
	}
}

func TestLinkifyRefs_NoRef(t *testing.T) {
	input := "This text has no spec references."
	got := LinkifyRefs(input, LinkifyRefsOpts{URLFor: urlFor})
	if got != input {
		t.Errorf("expected no change, got %q", got)
	}
}

func TestLinkifyRefs_MultipleRefs(t *testing.T) {
	input := "See TS 23.501 and RFC 3748 for details."
	got := LinkifyRefs(input, LinkifyRefsOpts{URLFor: urlFor})
	want := "See [TS 23.501](/specs/TS 23.501) and [RFC 3748](https://www.rfc-editor.org/rfc/rfc3748) for details."
	if got != want {
		t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", input, got, want)
	}
}

// References inside fenced code blocks and inline code spans must stay
// verbatim: goldmark renders code literally, so a rewritten reference would
// show up as raw link syntax.
func TestLinkifyRefs_CodeRegions(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "ref inside tagged fence untouched",
			input: "Prose about TS 29.228.\n\n```xml\n<!-- see TS 29.228 -->\n```\n\nAfter.",
			want:  "Prose about [TS 29.228](/specs/TS 29.228).\n\n```xml\n<!-- see TS 29.228 -->\n```\n\nAfter.",
		},
		{
			name:  "ref inside bare fence untouched",
			input: "```\nTS 23.501 clause 5.1\n```",
			want:  "```\nTS 23.501 clause 5.1\n```",
		},
		{
			name:  "ref inside unclosed fence untouched",
			input: "Intro TS 38.300.\n\n```asn1\n-- TS 38.331 defines RRC",
			want:  "Intro [TS 38.300](/specs/TS 38.300).\n\n```asn1\n-- TS 38.331 defines RRC",
		},
		{
			// CommonMark: the closer must be at least as long as the opener.
			name:  "four-backtick fence is not closed by three backticks",
			input: "````\n```\nTS 23.501\n````\nAfter TS 38.300.",
			want:  "````\n```\nTS 23.501\n````\nAfter [TS 38.300](/specs/TS 38.300).",
		},
		{
			// CommonMark: a closer carries no info string, so "```go" inside
			// an open fence is content.
			name:  "info-string line inside an open fence is not a closer",
			input: "```\n```go\nTS 23.501\n```\nAfter TS 38.300.",
			want:  "```\n```go\nTS 23.501\n```\nAfter [TS 38.300](/specs/TS 38.300).",
		},
		{
			name:  "closer with trailing spaces still closes",
			input: "```\nTS 23.501\n```  \nAfter TS 38.300.",
			want:  "```\nTS 23.501\n```  \nAfter [TS 38.300](/specs/TS 38.300).",
		},
		{
			name:  "ref inside inline code untouched",
			input: "Inline `see TS 29.228` too.",
			want:  "Inline `see TS 29.228` too.",
		},
		{
			name:  "ref between inline code spans is linkified",
			input: "`a` TS 23.501 `b`",
			want:  "`a` [TS 23.501](/specs/TS 23.501) `b`",
		},
		{
			name:  "unclosed backtick does not swallow the rest",
			input: "A stray ` here, but TS 23.501 still links.",
			want:  "A stray ` here, but [TS 23.501](/specs/TS 23.501) still links.",
		},
		{
			name:  "inline span does not cross a blank line",
			input: "a ` b\n\nTS 23.501 ` c",
			want:  "a ` b\n\n[TS 23.501](/specs/TS 23.501) ` c",
		},
		{
			name:  "double-backtick span",
			input: "``TS 23.501`` and TS 23.502",
			want:  "``TS 23.501`` and [TS 23.502](/specs/TS 23.502)",
		},
		{
			name:  "unclosed backtick before a fence does not swallow it",
			input: "a ` b\n```\nTS 23.501\n```\nTS 23.502 end",
			want:  "a ` b\n```\nTS 23.501\n```\n[TS 23.502](/specs/TS 23.502) end",
		},
		{
			name:  "inline span after a fence still closes",
			input: "```\nfenced\n```\n` a b ` TS 23.501",
			want:  "```\nfenced\n```\n` a b ` [TS 23.501](/specs/TS 23.501)",
		},
		{
			name:  "refs before and after a fence are linkified",
			input: "TS 23.501 first.\n```diameter\nRFC 6733 AVP\n```\nRFC 6733 last.",
			want:  "[TS 23.501](/specs/TS 23.501) first.\n```diameter\nRFC 6733 AVP\n```\n[RFC 6733](https://www.rfc-editor.org/rfc/rfc6733) last.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{URLFor: urlFor})
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

// References inside raw HTML table blocks must be rendered as HTML anchors,
// because goldmark does not process Markdown link syntax inside raw HTML.
func TestLinkifyRefs_InsideTable(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single ref in table cell becomes HTML anchor",
			input: "<table><tr><td><p>See TS 36.331 for details</p></td></tr></table>",
			want:  `<table><tr><td><p>See <a href="/specs/TS 36.331">TS 36.331</a> for details</p></td></tr></table>`,
		},
		{
			name:  "ref with clause in table cell",
			input: "<table><tr><td>TS 23.501 clause 5.1</td></tr></table>",
			want:  `<table><tr><td><a href="/specs/TS 23.501/sections/5.1">TS 23.501 clause 5.1</a></td></tr></table>`,
		},
		{
			name:  "multi-section ref in table cell",
			input: "<table><tr><td>TS 23.402 clauses 8.2 and 16.11</td></tr></table>",
			want:  `<table><tr><td><a href="/specs/TS 23.402">TS 23.402</a> clauses <a href="/specs/TS 23.402/sections/8.2">8.2</a> and <a href="/specs/TS 23.402/sections/16.11">16.11</a></td></tr></table>`,
		},
		{
			name:  "ref outside table stays Markdown, ref inside table is HTML",
			input: "See TS 38.300.\n\n<table><tr><td>See TS 36.331</td></tr></table>",
			want:  `See [TS 38.300](/specs/TS 38.300).` + "\n\n" + `<table><tr><td>See <a href="/specs/TS 36.331">TS 36.331</a></td></tr></table>`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{URLFor: urlFor})
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

// TestRegionHelpers pins the region lookup helpers, including regionEnd's
// defensive fallback for a position outside every region, which LinkifyRefs
// itself never reaches (it only calls regionEnd after inRegion says true).
func TestRegionHelpers(t *testing.T) {
	regs := []region{{start: 5, end: 10}}

	if inRegion(regs, 4) {
		t.Error("inRegion(4) = true, want false")
	}
	if !inRegion(regs, 5) {
		t.Error("inRegion(5) = false, want true")
	}
	if inRegion(regs, 10) {
		t.Error("inRegion(10) = true, want false (end is exclusive)")
	}
	if got := regionEnd(regs, 7); got != 10 {
		t.Errorf("regionEnd(7) = %d, want 10", got)
	}
	if got := regionEnd(regs, 3); got != 4 {
		t.Errorf("regionEnd(3) = %d, want 4 (pos+1 fallback)", got)
	}
}

// bareSectionSet reports the section numbers the fake current document has.
func bareSectionSet(section string) bool {
	switch section {
	case "4.2", "5.1", "5.15.2", "5.15.3", "B", "B.1":
		return true
	}
	return false
}

// bareURLFor resolves the empty-spec sentinel to the current spec, mirroring
// the web viewer's urlFor closure.
func bareURLFor(spec, section string) string {
	if spec == "" {
		spec = "TS 23.501"
	}
	return urlFor(spec, section)
}

func TestLinkifyRefs_BareRefs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "bare clause",
			input: "as described in clause 4.2 with details.",
			want:  "as described in [clause 4.2](/specs/TS 23.501/sections/4.2) with details.",
		},
		{
			name:  "sentence-initial capital",
			input: "Clause 4.2 describes this.",
			want:  "[Clause 4.2](/specs/TS 23.501/sections/4.2) describes this.",
		},
		{
			name:  "bare subclause",
			input: "See subclause 5.15.2.",
			want:  "See [subclause 5.15.2](/specs/TS 23.501/sections/5.15.2).",
		},
		{
			name:  "bare annex",
			input: "See Annex B for details.",
			want:  "See [Annex B](/specs/TS 23.501/sections/B) for details.",
		},
		{
			name:  "bare annex with subsection",
			input: "See Annex B.1 for details.",
			want:  "See [Annex B.1](/specs/TS 23.501/sections/B.1) for details.",
		},
		{
			name:  "nonexistent section gets unresolved marker",
			input: "See clause 9.9.9 for details.",
			want:  `See <span class="ref-unresolved" title="Section 9.9.9 does not exist in this document — possibly a stale or incorrect reference in the source text">clause 9.9.9</span> for details.`,
		},
		{
			name:  "keyword inside a longer word stays plain",
			input: "the intersection 4.2 metres wide",
			want:  "the intersection 4.2 metres wide",
		},
		{
			name:  "of-TS form keeps qualified link",
			input: "As defined in clause 5.1 of TS 23.402.",
			want:  "As defined in [clause 5.1 of TS 23.402](/specs/TS 23.402/sections/5.1).",
		},
		{
			name:  "spec-first form keeps qualified link",
			input: "See TS 23.402 clause 5.1 for details.",
			want:  "See [TS 23.402 clause 5.1](/specs/TS 23.402/sections/5.1) for details.",
		},
		{
			name:  "capitalized of-TS form not linked bare",
			input: "Clause 5.1 of TS 23.402 applies.",
			want:  "Clause 5.1 of [TS 23.402](/specs/TS 23.402) applies.",
		},
		{
			name:  "capitalized after spec not linked bare",
			input: "See TS 23.402 Clause 5.1.",
			want:  "See [TS 23.402](/specs/TS 23.402) Clause 5.1.",
		},
		{
			name:  "capitalized after RFC not linked bare",
			input: "See RFC 3748 Section 5.1.",
			want:  "See [RFC 3748](https://www.rfc-editor.org/rfc/rfc3748) Section 5.1.",
		},
		{
			name:  "in-TS form not linked bare",
			input: "as specified in clause 4.2 in TS 23.502.",
			want:  "as specified in clause 4.2 in [TS 23.502](/specs/TS 23.502).",
		},
		{
			name:  "of ITU-T recommendation not linked",
			input: "clause 4.2 of ITU-T Recommendation X.509 applies.",
			want:  "clause 4.2 of ITU-T Recommendation X.509 applies.",
		},
		{
			name:  "of present document links",
			input: "as specified in clause 4.2 of the present document.",
			want:  "as specified in [clause 4.2](/specs/TS 23.501/sections/4.2) of the present document.",
		},
		{
			name:  "of present specification links",
			input: "as specified in clause 4.2 of the present specification.",
			want:  "as specified in [clause 4.2](/specs/TS 23.501/sections/4.2) of the present specification.",
		},
		{
			name:  "coordinated list links every element to the named spec",
			input: "See clause 4.2 and clause 5.1 of TS 23.402.",
			want:  "See clause [4.2](/specs/TS 23.402/sections/4.2) and clause [5.1](/specs/TS 23.402/sections/5.1) of [TS 23.402](/specs/TS 23.402).",
		},
		{
			name:  "coordinated list with preposition links every element to the named spec",
			input: "described in clause 4.12.2 and in clause 4.12.2a of TS 23.502 [3], respectively.",
			want:  "described in clause [4.12.2](/specs/TS 23.502/sections/4.12.2) and in clause [4.12.2a](/specs/TS 23.502/sections/4.12.2a) of [TS 23.502](/specs/TS 23.502) [3], respectively.",
		},
		{
			name:  "coordinated bare numbers of TS keep qualified links",
			input: "See clause 4.2 and 5.1 of TS 23.402.",
			want:  "See clause [4.2](/specs/TS 23.402/sections/4.2) and [5.1](/specs/TS 23.402/sections/5.1) of [TS 23.402](/specs/TS 23.402).",
		},
		{
			name:  "trailing element after spec-first list not linked bare",
			input: "See TS 23.402 clause 4.2 and clause 5.1 for details.",
			want:  "See [TS 23.402 clause 4.2](/specs/TS 23.402/sections/4.2) and clause 5.1 for details.",
		},
		{
			name:  "parenthesized designator not linked bare",
			input: "See clause 4.2 (TS 23.402).",
			want:  "See clause 4.2 ([TS 23.402](/specs/TS 23.402)).",
		},
		{
			name:  "capitalized after multi-part spec not linked bare",
			input: "See TS 38.101-1 Clause 5.1.",
			want:  "See [TS 38.101-1](/specs/TS 38.101-1) Clause 5.1.",
		},
		{
			name:  "parenthesized multi-part designator not linked bare",
			input: "See clause 4.2 (TS 38.101-1).",
			want:  "See clause 4.2 ([TS 38.101-1](/specs/TS 38.101-1)).",
		},
		{
			name:  "colon after spec not linked bare",
			input: "See TS 23.402: Clause 5.1.",
			want:  "See [TS 23.402](/specs/TS 23.402): Clause 5.1.",
		},
		{
			name:  "mixed singular plural list links every element to the named spec",
			input: "See clause 4.2 and clauses 5.1, 5.15.2 of TS 23.402.",
			want:  "See clause [4.2](/specs/TS 23.402/sections/4.2) and clauses [5.1](/specs/TS 23.402/sections/5.1), [5.15.2](/specs/TS 23.402/sections/5.15.2) of [TS 23.402](/specs/TS 23.402).",
		},
		{
			name:  "oxford comma list of TS not linked bare",
			input: "See clause 4.2, 5.1, and 5.15.2 of TS 23.402.",
			want:  "See clause 4.2, 5.1, and 5.15.2 of [TS 23.402](/specs/TS 23.402).",
		},
		{
			name:  "coordinated list of present document links all",
			input: "See clause 4.2 and clause 5.1 of the present document.",
			want:  "See [clause 4.2](/specs/TS 23.501/sections/4.2) and [clause 5.1](/specs/TS 23.501/sections/5.1) of the present document.",
		},
		{
			name:  "coordinated list without qualifier links all",
			input: "See clause 4.2 and clause 5.1 for details.",
			want:  "See [clause 4.2](/specs/TS 23.501/sections/4.2) and [clause 5.1](/specs/TS 23.501/sections/5.1) for details.",
		},
		{
			name:  "in this specification links",
			input: "as specified in clause 4.2 in this specification.",
			want:  "as specified in [clause 4.2](/specs/TS 23.501/sections/4.2) in this specification.",
		},
		{
			name:  "this clause without number stays plain",
			input: "as described in this clause.",
			want:  "as described in this clause.",
		},
		{
			name:  "bare plural list",
			input: "See clauses 5.15.2 and 5.15.3.",
			want:  "See clauses [5.15.2](/specs/TS 23.501/sections/5.15.2) and [5.15.3](/specs/TS 23.501/sections/5.15.3).",
		},
		{
			name:  "bare plural list with comma",
			input: "See clauses 4.2, 5.15.2 and 5.15.3.",
			want:  "See clauses [4.2](/specs/TS 23.501/sections/4.2), [5.15.2](/specs/TS 23.501/sections/5.15.2) and [5.15.3](/specs/TS 23.501/sections/5.15.3).",
		},
		{
			name:  "bare plural links existing and marks missing sections",
			input: "See clauses 5.15.2 and 9.9.",
			want:  `See clauses [5.15.2](/specs/TS 23.501/sections/5.15.2) and <span class="ref-unresolved" title="Section 9.9 does not exist in this document — possibly a stale or incorrect reference in the source text">9.9</span>.`,
		},
		{
			name:  "plural of-TS form keeps qualified links",
			input: "See clauses 5.15.2 and 5.15.3 of TS 23.402.",
			want:  "See clauses [5.15.2](/specs/TS 23.402/sections/5.15.2) and [5.15.3](/specs/TS 23.402/sections/5.15.3) of [TS 23.402](/specs/TS 23.402).",
		},
		{
			name:  "bare ref in table cell becomes HTML anchor",
			input: "<table><tr><td>see clause 4.2</td></tr></table>",
			want:  `<table><tr><td>see <a href="/specs/TS 23.501/sections/4.2">clause 4.2</a></td></tr></table>`,
		},
		{
			name:  "bare ref in fenced code stays plain",
			input: "```\nclause 4.2\n```\n",
			want:  "```\nclause 4.2\n```\n",
		},
		{
			name:  "bare ref in existing link stays plain",
			input: "[clause 4.2](/specs/TS%2023.501/sections/4.2)",
			want:  "[clause 4.2](/specs/TS%2023.501/sections/4.2)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{URLFor: bareURLFor, SectionExists: bareSectionSet})
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLinkifyRefs_BareRefsWithBracketMap(t *testing.T) {
	bracketMap := map[string]string{"19": "TS 33.203"}
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "bracket-first form keeps qualified link",
			input: "See [19] clause 5.1 for details.",
			want:  "See [[19] clause 5.1](/specs/TS 33.203/sections/5.1) for details.",
		},
		{
			name:  "capitalized after bracket not linked bare",
			input: "See [19] Clause 5.1.",
			want:  "See [19] Clause 5.1.",
		},
		{
			name:  "of-bracket form not linked bare",
			input: "see clause 4.2 of [19].",
			want:  "see clause 4.2 of [19].",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{BracketMap: bracketMap, URLFor: bareURLFor, SectionExists: bareSectionSet})
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

// A nil sectionExists must disable bare linkification entirely.
func TestLinkifyRefs_BareRefsNilGate(t *testing.T) {
	input := "as described in clause 4.2 with details."
	got := LinkifyRefs(input, LinkifyRefsOpts{URLFor: bareURLFor})
	if got != input {
		t.Errorf("expected no change with nil sectionExists, got %q", got)
	}
}

// stubTargetInfo validates cross-spec references: TS 23.502 has only 4.12.2,
// TS 33.203 has only 6, TS 36.521-1 has only 5.5.4.2; other specs cannot be
// validated.
func stubTargetInfo(spec, section string) (bool, string, bool) {
	switch spec {
	case "TS 23.502":
		return section == "4.12.2", "20.2.0", true
	case "TS 33.203":
		return section == "6", "18.0.0", true
	case "TS 36.521-1":
		return section == "5.5.4.2", "18.5.0", true
	}
	return false, "", false
}

func TestLinkifyRefs_TargetValidation(t *testing.T) {
	missing502 := `Section 4.12.2a does not exist in TS 23.502 v20.2.0 — the text may reference a different version of TS 23.502; linked to the specification instead`
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "existing target keeps section link",
			input: "See TS 23.502 clause 4.12.2.",
			want:  "See [TS 23.502 clause 4.12.2](/specs/TS 23.502/sections/4.12.2).",
		},
		{
			name:  "missing target links to spec top with tooltip",
			input: "See TS 23.502 clause 4.12.2a.",
			want:  `See <a class="ref-unresolved" href="/specs/TS 23.502" title="` + missing502 + `">TS 23.502 clause 4.12.2a</a>.`,
		},
		{
			name:  "missing target in prefix form",
			input: "as described in clause 4.12.2a of TS 23.502.",
			want:  `as described in <a class="ref-unresolved" href="/specs/TS 23.502" title="` + missing502 + `">clause 4.12.2a of TS 23.502</a>.`,
		},
		{
			name:  "unvalidatable spec links as-is",
			input: "See TS 29.500 clause 5.1.",
			want:  "See [TS 29.500 clause 5.1](/specs/TS 29.500/sections/5.1).",
		},
		{
			name:  "multi list validates each element",
			input: "TS 23.502 clauses 4.12.2 and 9.9",
			want: "[TS 23.502](/specs/TS 23.502) clauses [4.12.2](/specs/TS 23.502/sections/4.12.2) and " +
				`<a class="ref-unresolved" href="/specs/TS 23.502" title="Section 9.9 does not exist in TS 23.502 v20.2.0 — the text may reference a different version of TS 23.502; linked to the specification instead">9.9</a>`,
		},
		{
			name:  "coordinated list validates each element",
			input: "in clause 4.12.2 and in clause 4.12.2a of TS 23.502.",
			want: "in clause [4.12.2](/specs/TS 23.502/sections/4.12.2) and in " +
				"clause " + `<a class="ref-unresolved" href="/specs/TS 23.502" title="` + missing502 + `">4.12.2a</a>` + " of [TS 23.502](/specs/TS 23.502).",
		},
		{
			name:  "missing target in table gets anchor title",
			input: "<table><tr><td>TS 23.502 clause 4.12.2a</td></tr></table>",
			want:  `<table><tr><td><a class="ref-unresolved" href="/specs/TS 23.502" title="` + missing502 + `">TS 23.502 clause 4.12.2a</a></td></tr></table>`,
		},
		{
			name:  "RFC references are never validated",
			input: "See RFC 3748 section 99.9.",
			want:  "See [RFC 3748 section 99.9](https://www.rfc-editor.org/rfc/rfc3748#section-99.9).",
		},
		{
			name:  "multi-part spec validates against the suffixed ID",
			input: "See TS 36.521-1 clause 5.5.4.2.",
			want:  "See [TS 36.521-1 clause 5.5.4.2](/specs/TS 36.521-1/sections/5.5.4.2).",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{URLFor: urlFor, TargetInfo: stubTargetInfo})
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLinkifyRefs_TargetValidationBracket(t *testing.T) {
	bracketMap := map[string]string{"19": "TS 33.203"}
	input := "See [19] clause 7 for details."
	want := `See <a class="ref-unresolved" href="/specs/TS 33.203" title="Section 7 does not exist in TS 33.203 v18.0.0 — the text may reference a different version of TS 33.203; linked to the specification instead">[19] clause 7</a> for details.`
	got := LinkifyRefs(input, LinkifyRefsOpts{BracketMap: bracketMap, URLFor: urlFor, TargetInfo: stubTargetInfo})
	if got != want {
		t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", input, got, want)
	}
}

// CurrentLabel names the document in bare unresolved-reference tooltips.
func TestLinkifyRefs_BareUnresolvedLabel(t *testing.T) {
	input := "See clause 9.9 for details."
	want := `See <span class="ref-unresolved" title="Section 9.9 does not exist in TS 23.501 v20.2.0 — possibly a stale or incorrect reference in the source text">clause 9.9</span> for details.`
	got := LinkifyRefs(input, LinkifyRefsOpts{
		URLFor:        bareURLFor,
		SectionExists: bareSectionSet,
		CurrentLabel:  "TS 23.501 v20.2.0",
	})
	if got != want {
		t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", input, got, want)
	}
}

// A bare reference in a table cell whose section is missing must be marked
// with the same span — raw HTML is valid in both contexts.
func TestLinkifyRefs_BareUnresolvedInTable(t *testing.T) {
	input := "<table><tr><td>see clause 9.9</td></tr></table>"
	want := `<table><tr><td>see <span class="ref-unresolved" title="Section 9.9 does not exist in this document — possibly a stale or incorrect reference in the source text">clause 9.9</span></td></tr></table>`
	got := LinkifyRefs(input, LinkifyRefsOpts{URLFor: bareURLFor, SectionExists: bareSectionSet})
	if got != want {
		t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", input, got, want)
	}
}

// Coordinated lists may use plural keywords per element; the element
// extractor must share the pattern's keyword classes.
func TestLinkifyRefs_CoordPluralKeyword(t *testing.T) {
	input := "See clause 8.2 and in clauses 8.3 and 8.4 of TS 23.402."
	want := "See clause [8.2](/specs/TS 23.402/sections/8.2) and in clauses [8.3](/specs/TS 23.402/sections/8.3) and " +
		"[8.4](/specs/TS 23.402/sections/8.4) of [TS 23.402](/specs/TS 23.402)."
	got := LinkifyRefs(input, LinkifyRefsOpts{URLFor: urlFor})
	if got != want {
		t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", input, got, want)
	}
}

// A bare number directly after a preposition is not a section reference:
// the coordinated patterns require a keyword after "of"/"in".
func TestLinkifyRefs_PrepositionNeedsKeyword(t *testing.T) {
	input := "See clause 4.1 and in 2024 of TS 23.501."
	want := "See clause 4.1 and in 2024 of [TS 23.501](/specs/TS 23.501)."
	got := LinkifyRefs(input, LinkifyRefsOpts{URLFor: urlFor})
	if got != want {
		t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", input, got, want)
	}
}

// URLFor is the one mandatory option; without it LinkifyRefs is a no-op
// instead of a panic.
// Regression test for #135: an unclosed "[" (e.g. an interval like "[0, 1)"
// or an unterminated editor's note "[FFS ...") must not have existingLinkRE
// treat everything up to a later, unrelated "](" — such as a real image
// link several paragraphs down — as one giant existing link. That used to
// swallow every reference in between, suppressing their linkification.
func TestLinkifyRefs_UnclosedBracketDoesNotSuppressLaterRefs(t *testing.T) {
	input := "The interval is [0, 1) as noted.\n\n" +
		"See TS 23.501 for architecture details.\n\n" +
		"![diagram](image://foo.png)"
	want := "The interval is [0, 1) as noted.\n\n" +
		"See [TS 23.501](/specs/TS 23.501) for architecture details.\n\n" +
		"![diagram](image://foo.png)"
	got := LinkifyRefs(input, LinkifyRefsOpts{URLFor: urlFor})
	if got != want {
		t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", input, got, want)
	}
}

func TestLinkifyRefs_NilURLFor(t *testing.T) {
	input := "See TS 23.501 clause 5.1."
	if got := LinkifyRefs(input, LinkifyRefsOpts{}); got != input {
		t.Errorf("expected no change with nil URLFor, got %q", got)
	}
}

// A reference matched across a soft line break inside a paragraph (a w:br in
// the source document) must not produce a multi-line marker: the web sanitizer
// relies on markers staying on one line.
func TestLinkifyRefs_MarkerFoldsNewline(t *testing.T) {
	input := "See clause\n9.9 for details."
	want := `See <span class="ref-unresolved" title="Section 9.9 does not exist in this document — possibly a stale or incorrect reference in the source text">clause 9.9</span> for details.`
	got := LinkifyRefs(input, LinkifyRefsOpts{URLFor: bareURLFor, SectionExists: bareSectionSet})
	if got != want {
		t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", input, got, want)
	}
}

// Regression test for #191: no reference may span a blank line. Blocks are
// joined with "\n\n", so a link or marker emitted across one glues the two
// blocks together — a heading ending in "clause" swallowed the paragraph that
// followed it, and goldmark rendered both as a single heading.
func TestLinkifyRefs_NoMatchAcrossBlankLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "heading ending in keyword and following paragraph",
			input: "# 5 Test clause\n\nUpon receipt of the REGISTRATION ACCEPT message, the UE shall:",
		},
		{
			name:  "two body paragraphs",
			input: "The procedure is defined in clause\n\nAnnex text follows here.",
		},
		{
			name:  "keyword and section number in separate paragraphs",
			input: "See clause\n\n4.2 is the relevant one.",
		},
		{
			name:  "CRLF blank line",
			input: "# 5 Test clause\r\n\r\nUpon receipt of the message.",
		},
		{
			name:  "spec designator split across a blank line",
			input: "As defined in TS\n\n23.501 the procedure applies.",
		},
		{
			name:  "RFC number split across a blank line",
			input: "See RFC\n\n6733 for details.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{URLFor: bareURLFor, SectionExists: bareSectionSet})
			if got != tt.input {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want it unchanged", tt.input, got)
			}
		})
	}
}

// The optional gap between a spec designator and a trailing clause reference
// holds one optional comma, and it must not span a blank line either: written
// as two separator runs it would take a line break on each side of the absent
// comma. Each block linkifies on its own instead.
func TestLinkifyRefs_QualifiedSectionGapAcrossBlankLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "TS designator and clause in separate blocks",
			input: "See TS 23.501\n\nclause 5.1 here.",
			want:  "See [TS 23.501](/specs/TS 23.501)\n\n[clause 5.1](/specs/TS 23.501/sections/5.1) here.",
		},
		{
			name:  "RFC number and section in separate blocks",
			input: "See RFC 6733\n\nsection 5.1 here.",
			want:  "See [RFC 6733](https://www.rfc-editor.org/rfc/rfc6733)\n\n[section 5.1](/specs/TS 23.501/sections/5.1) here.",
		},
		{
			name:  "soft break in the gap still links as one reference",
			input: "See TS 23.501\nclause 5.1 here.",
			want:  "See [TS 23.501\nclause 5.1](/specs/TS 23.501/sections/5.1) here.",
		},
		{
			name:  "soft break after the comma still links as one reference",
			input: "See TS 23.501,\nclause 5.1 here.",
			want:  "See [TS 23.501,\nclause 5.1](/specs/TS 23.501/sections/5.1) here.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{URLFor: bareURLFor, SectionExists: bareSectionSet})
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

// The context gates around a bare reference must not see across a block
// boundary either: a designator or an "of TS ..." qualifier in the neighbouring
// block belongs to that block, and reading it suppresses a legitimate
// same-document link.
func TestLinkifyRefs_ContextGatesStopAtBlankLine(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "trailing qualifier in the next block",
			input: "See clause 4.2\n\nof TS 23.402 for details.",
			want:  "See [clause 4.2](/specs/TS 23.501/sections/4.2)\n\nof [TS 23.402](/specs/TS 23.402) for details.",
		},
		{
			name:  "qualifier reached through a coordinated list in the next block",
			input: "The following apply: clause 4.2,\n\nclause 4.3 of TS 23.402.",
			want: "The following apply: [clause 4.2](/specs/TS 23.501/sections/4.2),\n\n" +
				"[clause 4.3 of TS 23.402](/specs/TS 23.402/sections/4.3).",
		},
		{
			name:  "leading designator in the previous block",
			input: "See TS 23.402 clause 5.1,\n\nclause 4.2 applies.",
			want: "See [TS 23.402 clause 5.1](/specs/TS 23.402/sections/5.1),\n\n" +
				"[clause 4.2](/specs/TS 23.501/sections/4.2) applies.",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, LinkifyRefsOpts{URLFor: bareURLFor, SectionExists: bareSectionSet})
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}
