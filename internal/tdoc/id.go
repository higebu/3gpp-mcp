// Package tdoc locates and fetches 3GPP meeting documents (TDocs) from the
// 3GPP FTP site.
//
// Every TSG and working group meeting publishes its contributions, change
// requests, liaison statements, agenda and minutes under
// https://www.3gpp.org/ftp/<group>/<meeting>/Docs/ as one zip per TDoc,
// named by the TDoc number (R1-2509715.zip). Unlike a specification, a TDoc
// number does not say which meeting folder holds it, so the meeting is found
// through the group's DynaReport meeting page, which lists every meeting
// with its FTP folder and TDoc number range.
package tdoc

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Group is a TSG or working group: the prefix its TDoc numbers carry, the
// folder under /ftp/ holding its meetings, and the DynaReport page listing
// them (https://www.3gpp.org/dynareport?code=Meetings-<Code>.htm).
type Group struct {
	Code string // TDoc prefix, e.g. "R1"
	Name string // e.g. "RAN1"
	Dir  string // FTP folder relative to /ftp/, e.g. "tsg_ran/WG1_RL1"
}

// groups maps TDoc prefixes to groups. Legacy groups (GERAN WGs, CN, T) are
// left out: their meeting pages exist but their documents are almost all
// .doc, and the prefixes overlap with later groups.
var groups = map[string]Group{
	"RP": {Code: "RP", Name: "RAN", Dir: "tsg_ran/TSG_RAN"},
	"R1": {Code: "R1", Name: "RAN1", Dir: "tsg_ran/WG1_RL1"},
	"R2": {Code: "R2", Name: "RAN2", Dir: "tsg_ran/WG2_RL2"},
	"R3": {Code: "R3", Name: "RAN3", Dir: "tsg_ran/WG3_Iu"},
	"R4": {Code: "R4", Name: "RAN4", Dir: "tsg_ran/WG4_Radio"},
	"R5": {Code: "R5", Name: "RAN5", Dir: "tsg_ran/WG5_Test_ex-T1"},
	"R6": {Code: "R6", Name: "RAN6", Dir: "tsg_ran/WG6_legacyRAN"},
	"SP": {Code: "SP", Name: "SA", Dir: "tsg_sa/TSG_SA"},
	"S1": {Code: "S1", Name: "SA1", Dir: "tsg_sa/WG1_Serv"},
	"S2": {Code: "S2", Name: "SA2", Dir: "tsg_sa/WG2_Arch"},
	"S3": {Code: "S3", Name: "SA3", Dir: "tsg_sa/WG3_Security"},
	"S4": {Code: "S4", Name: "SA4", Dir: "tsg_sa/WG4_CODEC"},
	"S5": {Code: "S5", Name: "SA5", Dir: "tsg_sa/WG5_TM"},
	"S6": {Code: "S6", Name: "SA6", Dir: "tsg_sa/WG6_MissionCritical"},
	"CP": {Code: "CP", Name: "CT", Dir: "tsg_ct/TSG_CT"},
	"C1": {Code: "C1", Name: "CT1", Dir: "tsg_ct/WG1_mm-cc-sm_ex-CN1"},
	"C3": {Code: "C3", Name: "CT3", Dir: "tsg_ct/WG3_interworking_ex-CN3"},
	"C4": {Code: "C4", Name: "CT4", Dir: "tsg_ct/WG4_protocollars_ex-CN4"},
	"C6": {Code: "C6", Name: "CT6", Dir: "tsg_ct/WG6_Smartcard_Ex-T3"},
	"GP": {Code: "GP", Name: "GERAN", Dir: "tsg_geran/TSG_GERAN"},
}

// Groups returns every known group, ordered by code.
func Groups() []Group {
	out := make([]Group, 0, len(groups))
	for _, g := range groups {
		out = append(out, g)
	}
	sortGroups(out)
	return out
}

func sortGroups(gs []Group) {
	for i := 1; i < len(gs); i++ {
		for j := i; j > 0 && gs[j].Code < gs[j-1].Code; j-- {
			gs[j], gs[j-1] = gs[j-1], gs[j]
		}
	}
}

// GroupByCode returns the group owning a TDoc prefix or group name
// ("R1", "RAN1", "ran1").
func GroupByCode(code string) (Group, bool) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if g, ok := groups[code]; ok {
		return g, true
	}
	for _, g := range groups {
		if g.Name == code {
			return g, true
		}
	}
	return Group{}, false
}

// ID is a parsed TDoc number.
type ID struct {
	// Prefix is the group code, e.g. "R1".
	Prefix string
	// Number is the numeric part, e.g. 2509715.
	Number int
	// Digits is the numeric part as written, so an ID round-trips through
	// String even if a group zero-pads its numbers.
	Digits string
}

// tdocRE matches a TDoc number: a two-character group code and five to
// eight digits. Old numbers have six digits ("R1-100983"), current ones
// seven ("R1-2509715", "S2-2510076").
var tdocRE = regexp.MustCompile(`^([A-Z][A-Z0-9])-?(\d{5,8})$`)

// ParseID parses a TDoc number such as "R1-2509715" (case-insensitive; the
// hyphen may be omitted). The prefix must name a known group.
func ParseID(s string) (ID, error) {
	norm := strings.ToUpper(strings.TrimSpace(s))
	m := tdocRE.FindStringSubmatch(norm)
	if m == nil {
		return ID{}, fmt.Errorf("%q is not a TDoc number (expected e.g. R1-2509715)", s)
	}
	if _, ok := groups[m[1]]; !ok {
		return ID{}, fmt.Errorf("%q: unknown group prefix %q", s, m[1])
	}
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return ID{}, fmt.Errorf("%q: %w", s, err)
	}
	return ID{Prefix: m[1], Number: n, Digits: m[2]}, nil
}

// String renders the canonical form, e.g. "R1-2509715"; the zero ID renders
// as "".
func (id ID) String() string {
	if id.Prefix == "" {
		return ""
	}
	return id.Prefix + "-" + id.Digits
}

// Group returns the group the number belongs to.
func (id ID) Group() Group {
	return groups[id.Prefix]
}
