// Package specver converts between the two 3GPP specification version
// notations: the dotted form used in documents ("18.6.0") and the base-36
// token used in archive filenames ("i60").
//
// An archive token is three base-36 digits holding the release, technical and
// editorial numbers respectively, so "k20" is v20.2.0 and "fa0" is v15.10.0.
// Components above 35 do not fit in a single digit and cannot be encoded.
package specver

import (
	"fmt"
	"strconv"
	"strings"
)

// tokenLength is the number of base-36 digits in an archive version token.
const tokenLength = 3

// maxComponent is the largest version component a single base-36 digit holds.
const maxComponent = 35

// digitValue returns the numeric value of a base-36 digit, or -1 if c is not one.
func digitValue(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	default:
		return -1
	}
}

// digitChar returns the lowercase base-36 digit for v, which must be 0..35.
func digitChar(v int) byte {
	if v < 10 {
		return byte('0' + v)
	}
	return byte('a' + v - 10)
}

// TokenToDotted converts an archive version token to the dotted form.
// It reports false for tokens that are not exactly three base-36 digits.
func TokenToDotted(token string) (string, bool) {
	if len(token) != tokenLength {
		return "", false
	}
	parts := make([]string, tokenLength)
	for i := range tokenLength {
		v := digitValue(token[i])
		if v < 0 {
			return "", false
		}
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, "."), true
}

// DottedToToken converts a dotted version to the archive token form.
// It reports false unless the version has three components, each 0..35.
func DottedToToken(dotted string) (string, bool) {
	parts := strings.Split(dotted, ".")
	if len(parts) != tokenLength {
		return "", false
	}
	token := make([]byte, tokenLength)
	for i, p := range parts {
		v, err := parseComponent(p)
		if err != nil || v > maxComponent {
			return "", false
		}
		token[i] = digitChar(v)
	}
	return string(token), true
}

// parseComponent parses one dotted-version component: an unsigned decimal
// number. Unlike strconv.Atoi it rejects a sign, so "+18" is not a component.
func parseComponent(p string) (int, error) {
	if p == "" {
		return 0, fmt.Errorf("empty component")
	}
	for i := 0; i < len(p); i++ {
		if p[i] < '0' || p[i] > '9' {
			return 0, fmt.Errorf("non-numeric component %q", p)
		}
	}
	v, err := strconv.Atoi(p)
	if err != nil {
		return 0, fmt.Errorf("component %q out of range", p)
	}
	return v, nil
}

// IsDotted reports whether s looks like a dotted version ("18.6.0").
func IsDotted(s string) bool {
	return strings.Contains(s, ".")
}

// Normalize accepts either notation and returns both. The dotted form comes
// back canonical ("018.6.0" is "18.6.0"). When only one of the two can be
// derived, the other is returned empty rather than guessed: unusual tokens
// (not three digits) and versions with a component above 35 have no
// counterpart, and callers display whichever form they have.
func Normalize(s string) (dotted, token string, err error) {
	s = strings.TrimSpace(s)
	// A leading v/V is decoration in the dotted notation ("v18.6.0") and in
	// front of a token ("v920" is the token 920) — but it is also the base-36
	// digit 31, so a three-character input like "va0" is itself a token
	// (31.10.0), not a prefixed "a0". Strip the prefix only when what follows
	// is a dotted version or a full-length token.
	if len(s) > 1 && (s[0] == 'v' || s[0] == 'V') {
		if rest := s[1:]; IsDotted(rest) {
			s = rest
		} else if _, ok := TokenToDotted(strings.ToLower(rest)); ok && len(s) != tokenLength {
			s = rest
		}
	}
	if s == "" {
		return "", "", fmt.Errorf("empty version")
	}

	if IsDotted(s) {
		parts := strings.Split(s, ".")
		if len(parts) != tokenLength {
			return "", "", fmt.Errorf("invalid version %q: expected three dot-separated components", s)
		}
		nums := make([]string, tokenLength)
		for i, p := range parts {
			v, err := parseComponent(p)
			if err != nil {
				return "", "", fmt.Errorf("invalid version %q: %w", s, err)
			}
			nums[i] = strconv.Itoa(v)
		}
		dotted = strings.Join(nums, ".")
		if t, ok := DottedToToken(dotted); ok {
			return dotted, t, nil
		}
		return dotted, "", nil
	}

	s = strings.ToLower(s)
	if d, ok := TokenToDotted(s); ok {
		return d, s, nil
	}
	return "", s, nil
}

// ReleaseOf returns the release number of a dotted version, e.g. "20" for
// "20.2.0". It returns an empty string when v is not a dotted version.
func ReleaseOf(v string) string {
	i := strings.Index(v, ".")
	if i <= 0 {
		return ""
	}
	if _, err := strconv.Atoi(v[:i]); err != nil {
		return ""
	}
	return v[:i]
}

// ReleaseLabel formats a release number for display, e.g. "Rel-18". A value
// that is not a plain number is returned unchanged so odd data still shows.
func ReleaseLabel(release string) string {
	if release == "" {
		return ""
	}
	if _, err := strconv.Atoi(release); err != nil {
		return release
	}
	return "Rel-" + release
}

// Compare orders two dotted versions numerically component by component.
// Non-numeric or missing components sort below numeric ones, so malformed
// values never win a "latest version" comparison.
func Compare(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	n := max(len(pa), len(pb))
	for i := range n {
		va, vb := componentAt(pa, i), componentAt(pb, i)
		if va != vb {
			if va < vb {
				return -1
			}
			return 1
		}
	}
	return 0
}

// componentAt returns the numeric value of the i-th component, or -1 when it
// is absent or not a number.
func componentAt(parts []string, i int) int {
	if i >= len(parts) {
		return -1
	}
	v, err := strconv.Atoi(parts[i])
	if err != nil {
		return -1
	}
	return v
}
