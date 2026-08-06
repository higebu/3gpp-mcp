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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, nil, urlFor, nil)
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
			got := LinkifyRefs(tt.input, nil, urlFor, nil)
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, bracketMap, urlFor, nil)
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
			want:  "clauses [8.2](/specs/TS 23.402/sections/8.2) and [16.11](/specs/TS 23.402/sections/16.11) of [TS 23.402](/specs/TS 23.402)",
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, nil, urlFor, nil)
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLinkifyRefs_NilBracketMap(t *testing.T) {
	// Bracket refs should NOT be replaced when bracketMap is nil.
	input := "See [19] clause 6 for details."
	got := LinkifyRefs(input, nil, urlFor, nil)
	if got != input {
		t.Errorf("expected no change with nil bracketMap, got %q", got)
	}
}

func TestLinkifyRefs_ExistingLink(t *testing.T) {
	// References inside existing Markdown links should not be double-replaced.
	input := "See [TS 23.501 clause 5](/specs/TS%2023.501/sections/5) for details."
	got := LinkifyRefs(input, nil, urlFor, nil)
	if got != input {
		t.Errorf("existing link was modified:\n got:  %q\n want: %q", got, input)
	}
}

func TestLinkifyRefs_NoRef(t *testing.T) {
	input := "This text has no spec references."
	got := LinkifyRefs(input, nil, urlFor, nil)
	if got != input {
		t.Errorf("expected no change, got %q", got)
	}
}

func TestLinkifyRefs_MultipleRefs(t *testing.T) {
	input := "See TS 23.501 and RFC 3748 for details."
	got := LinkifyRefs(input, nil, urlFor, nil)
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
			got := LinkifyRefs(tt.input, nil, urlFor, nil)
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
			got := LinkifyRefs(tt.input, nil, urlFor, nil)
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
			name:  "nonexistent section stays plain",
			input: "See clause 9.9.9 for details.",
			want:  "See clause 9.9.9 for details.",
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
			name:  "bare plural links only existing sections",
			input: "See clauses 5.15.2 and 9.9.",
			want:  "See clauses [5.15.2](/specs/TS 23.501/sections/5.15.2) and 9.9.",
		},
		{
			name:  "bare plural with no existing section stays plain",
			input: "See clauses 9.8 and 9.9.",
			want:  "See clauses 9.8 and 9.9.",
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
			got := LinkifyRefs(tt.input, nil, bareURLFor, bareSectionSet)
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
			got := LinkifyRefs(tt.input, bracketMap, bareURLFor, bareSectionSet)
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

// A nil sectionExists must disable bare linkification entirely.
func TestLinkifyRefs_BareRefsNilGate(t *testing.T) {
	input := "as described in clause 4.2 with details."
	got := LinkifyRefs(input, nil, bareURLFor, nil)
	if got != input {
		t.Errorf("expected no change with nil sectionExists, got %q", got)
	}
}
