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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LinkifyRefs(tt.input, nil, urlFor)
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
			got := LinkifyRefs(tt.input, nil, urlFor)
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
			got := LinkifyRefs(tt.input, bracketMap, urlFor)
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
			got := LinkifyRefs(tt.input, nil, urlFor)
			if got != tt.want {
				t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLinkifyRefs_NilBracketMap(t *testing.T) {
	// Bracket refs should NOT be replaced when bracketMap is nil.
	input := "See [19] clause 6 for details."
	got := LinkifyRefs(input, nil, urlFor)
	if got != input {
		t.Errorf("expected no change with nil bracketMap, got %q", got)
	}
}

func TestLinkifyRefs_ExistingLink(t *testing.T) {
	// References inside existing Markdown links should not be double-replaced.
	input := "See [TS 23.501 clause 5](/specs/TS%2023.501/sections/5) for details."
	got := LinkifyRefs(input, nil, urlFor)
	if got != input {
		t.Errorf("existing link was modified:\n got:  %q\n want: %q", got, input)
	}
}

func TestLinkifyRefs_NoRef(t *testing.T) {
	input := "This text has no spec references."
	got := LinkifyRefs(input, nil, urlFor)
	if got != input {
		t.Errorf("expected no change, got %q", got)
	}
}

func TestLinkifyRefs_MultipleRefs(t *testing.T) {
	input := "See TS 23.501 and RFC 3748 for details."
	got := LinkifyRefs(input, nil, urlFor)
	want := "See [TS 23.501](/specs/TS 23.501) and [RFC 3748](https://www.rfc-editor.org/rfc/rfc3748) for details."
	if got != want {
		t.Errorf("LinkifyRefs(%q)\n got:  %q\n want: %q", input, got, want)
	}
}
