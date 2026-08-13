package specver

import "testing"

func TestTokenToDotted(t *testing.T) {
	tests := []struct {
		token string
		want  string
		ok    bool
	}{
		{"k20", "20.2.0", true},
		{"fa0", "15.10.0", true},
		{"300", "3.0.0", true},
		{"i60", "18.6.0", true},
		{"j50", "19.5.0", true},
		{"a01", "10.0.1", true},
		{"zzz", "35.35.35", true},
		{"K20", "20.2.0", true},
		{"k2", "", false},
		{"k200", "", false},
		{"k-0", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := TokenToDotted(tt.token)
		if ok != tt.ok || got != tt.want {
			t.Errorf("TokenToDotted(%q) = %q, %v; want %q, %v", tt.token, got, ok, tt.want, tt.ok)
		}
	}
}

func TestDottedToToken(t *testing.T) {
	tests := []struct {
		dotted string
		want   string
		ok     bool
	}{
		{"20.2.0", "k20", true},
		{"15.10.0", "fa0", true},
		{"3.0.0", "300", true},
		{"35.35.35", "zzz", true},
		{"36.0.0", "", false},  // component above 35 has no single digit
		{"18.36.0", "", false}, // ditto for the technical component
		{"18.6", "", false},
		{"18.6.0.1", "", false},
		{"x.y.z", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := DottedToToken(tt.dotted)
		if ok != tt.ok || got != tt.want {
			t.Errorf("DottedToToken(%q) = %q, %v; want %q, %v", tt.dotted, got, ok, tt.want, tt.ok)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, token := range []string{"000", "k20", "fa0", "300", "zzz", "9z1"} {
		dotted, ok := TokenToDotted(token)
		if !ok {
			t.Fatalf("TokenToDotted(%q) failed", token)
		}
		back, ok := DottedToToken(dotted)
		if !ok || back != token {
			t.Errorf("round trip %q -> %q -> %q, ok=%v", token, dotted, back, ok)
		}
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		in      string
		dotted  string
		token   string
		wantErr bool
		desc    string
	}{
		{in: "20.2.0", dotted: "20.2.0", token: "k20", desc: "dotted input"},
		{in: "k20", dotted: "20.2.0", token: "k20", desc: "token input"},
		{in: "K20", dotted: "20.2.0", token: "k20", desc: "uppercase token"},
		{in: "v18.6.0", dotted: "18.6.0", token: "i60", desc: "v prefix"},
		{in: " 18.6.0 ", dotted: "18.6.0", token: "i60", desc: "surrounding space"},
		{in: "36.0.0", dotted: "36.0.0", token: "", desc: "no token counterpart"},
		{in: "k2", dotted: "", token: "k2", desc: "odd token kept as-is"},
		{in: "va0", dotted: "31.10.0", token: "va0", desc: "token starting with the digit v"},
		{in: "v00", dotted: "31.0.0", token: "v00", desc: "token v00 is release 31, not a prefixed 00"},
		{in: "018.6.0", dotted: "18.6.0", token: "i60", desc: "leading zeros canonicalized"},
		{in: "18.6", wantErr: true, desc: "two components"},
		{in: "+18.6.0", wantErr: true, desc: "signed component"},
		{in: "18.foo.0", wantErr: true, desc: "non-numeric component"},
		{in: "", wantErr: true, desc: "empty"},
	}
	for _, tt := range tests {
		dotted, token, err := Normalize(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("Normalize(%q) [%s]: want error, got %q/%q", tt.in, tt.desc, dotted, token)
			}
			continue
		}
		if err != nil {
			t.Errorf("Normalize(%q) [%s]: unexpected error: %v", tt.in, tt.desc, err)
			continue
		}
		if dotted != tt.dotted || token != tt.token {
			t.Errorf("Normalize(%q) [%s] = %q, %q; want %q, %q", tt.in, tt.desc, dotted, token, tt.dotted, tt.token)
		}
	}
}

func TestReleaseOf(t *testing.T) {
	tests := map[string]string{
		"20.2.0":  "20",
		"3.0.0":   "3",
		"18.6.0":  "18",
		"k20":     "",
		"":        "",
		".2.0":    "",
		"x.2.0":   "",
		"20.2.0.": "20",
	}
	for in, want := range tests {
		if got := ReleaseOf(in); got != want {
			t.Errorf("ReleaseOf(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestReleaseLabel(t *testing.T) {
	tests := map[string]string{
		"18":     "Rel-18",
		"20":     "Rel-20",
		"":       "",
		"Rel-18": "Rel-18",
	}
	for in, want := range tests {
		if got := ReleaseLabel(in); got != want {
			t.Errorf("ReleaseLabel(%q) = %q; want %q", in, got, want)
		}
	}
}

func TestCompare(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"18.6.0", "18.6.0", 0},
		{"18.6.0", "18.7.0", -1},
		{"18.7.0", "18.6.0", 1},
		{"9.0.0", "10.0.0", -1}, // numeric, not lexicographic
		{"20.2.0", "3.0.0", 1},
		{"18.6.0", "18.6", 1}, // longer beats a missing component
		{"", "18.6.0", -1},    // malformed sorts below
		{"junk", "junk", 0},
	}
	for _, tt := range tests {
		if got := Compare(tt.a, tt.b); got != tt.want {
			t.Errorf("Compare(%q, %q) = %d; want %d", tt.a, tt.b, got, tt.want)
		}
	}
}
