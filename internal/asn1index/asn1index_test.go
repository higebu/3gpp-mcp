package asn1index

import (
	"context"
	"strings"
	"testing"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/testutil"
)

// testModule mimics how the DOCX converter renders an NGAP-style ASN.1
// clause: one ```asn1 fence holding a whole module, markers included.
const testModule = "Each IE is defined below.\n\n" +
	"```asn1\n" +
	"-- ASN1START\n" +
	"-- **************************************************************\n" +
	"NGAP-IEs {\n" +
	"itu-t (0) identified-organization (4) etsi (0) mobileDomain (0) }\n" +
	"\n" +
	"DEFINITIONS AUTOMATIC TAGS ::=\n" +
	"\n" +
	"BEGIN\n" +
	"\n" +
	"IMPORTS\n" +
	"\tCriticality\n" +
	"FROM NGAP-CommonDataTypes;\n" +
	"\n" +
	"AMF-UE-NGAP-ID ::= INTEGER (0..1099511627775)\n" +
	"\n" +
	"Cause ::= CHOICE {\n" +
	"\tradioNetwork\t\tCauseRadioNetwork,\n" +
	"\tmisc\t\t\tCauseMisc,\n" +
	"\t...\n" +
	"}\n" +
	"\n" +
	"CauseMisc ::= ENUMERATED {\n" +
	"\tcontrol-processing-overload,\n" +
	"\tunspecified,\n" +
	"\t...\n" +
	"}\n" +
	"\n" +
	"maxNrOfErrors INTEGER ::= 256\n" +
	"\n" +
	"\tid-AMF-UE-NGAP-ID\t\t\t\t\tProtocolIE-ID ::= 10\n" +
	"\n" +
	"END\n" +
	"-- ASN1STOP\n" +
	"```"

func TestExtract(t *testing.T) {
	sections := []db.Section{{
		SpecID:  "TS 38.413",
		Version: "18.6.0",
		Number:  "9.4.5",
		Title:   "Information Element definitions",
		Content: testModule,
	}}
	got := Extract(sections)

	names := make([]string, len(got))
	for i, a := range got {
		names[i] = a.Name
	}
	want := []string{"AMF-UE-NGAP-ID", "Cause", "CauseMisc", "maxNrOfErrors", "id-AMF-UE-NGAP-ID"}
	if len(names) != len(want) {
		t.Fatalf("extracted names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("extracted names = %v, want %v", names, want)
		}
	}

	for _, a := range got {
		if a.Section.Number != "9.4.5" {
			t.Errorf("%s: section = %q, want 9.4.5", a.Name, a.Section.Number)
		}
		if a.Section.Content != "" {
			t.Errorf("%s: section content not cleared", a.Name)
		}
	}

	// An ENUMERATED keeps its closing brace: the body runs to the next head,
	// not to the next column-0 line.
	if !strings.HasSuffix(got[2].Text, "}") {
		t.Errorf("CauseMisc body lost its closing brace:\n%s", got[2].Text)
	}
	// A value assignment is one line.
	if got[3].Text != "maxNrOfErrors INTEGER ::= 256" {
		t.Errorf("maxNrOfErrors body = %q", got[3].Text)
	}
	// NGAP's constant definitions are tab-indented; the indentation is kept.
	if got[4].Text != "\tid-AMF-UE-NGAP-ID\t\t\t\t\tProtocolIE-ID ::= 10" {
		t.Errorf("id-AMF-UE-NGAP-ID body = %q", got[4].Text)
	}
	// END closes the module: the last assignment must not absorb it or the
	// -- ASN1STOP marker.
	if strings.Contains(got[4].Text, "END") {
		t.Errorf("last assignment absorbed the module END:\n%s", got[4].Text)
	}
	// The module header's DEFINITIONS line is not an assignment.
	for _, a := range got {
		if a.Name == "DEFINITIONS" || a.Name == "NGAP-IEs" {
			t.Errorf("module header extracted as assignment %q", a.Name)
		}
	}
}

func TestExtractTrimsTrailingComments(t *testing.T) {
	// The per-type fences of RRC and LPP carry no module END, so the last
	// assignment runs to the fence: the -- TAG-...-STOP and -- ASN1STOP
	// marker lines must not end up in its body. A comment on the tail of a
	// code line is content and stays.
	sections := []db.Section{{
		SpecID:  "TS 37.355",
		Number:  "6.5.2",
		Content: "```asn1\n-- ASN1START\nKlobucharModel-r16 ::= SEQUENCE {\n\talpha0-r16\tINTEGER\n}\n\n-- TAG-KLOBUCHARMODEL-STOP\n-- ASN1STOP\n```",
	}, {
		SpecID:  "TS 36.331",
		Number:  "6.4",
		Content: "```asn1\n-- ASN1START\nmaxCellMeas INTEGER ::= 32\t-- highest number of cells\n-- ASN1STOP\n```",
	}}
	got := Extract(sections)
	if len(got) != 2 {
		t.Fatalf("extracted %d assignments, want 2", len(got))
	}
	if strings.Contains(got[0].Text, "--") {
		t.Errorf("trailing comment lines kept:\n%s", got[0].Text)
	}
	if !strings.HasSuffix(got[0].Text, "}") {
		t.Errorf("body should end at the closing brace:\n%s", got[0].Text)
	}
	if got[1].Text != "maxCellMeas INTEGER ::= 32\t-- highest number of cells" {
		t.Errorf("tail comment of a code line lost: %q", got[1].Text)
	}
}

func TestExtractSkipsExampleFences(t *testing.T) {
	// A tagged marker ("-- /example/ ASN1START") fences a non-normative
	// guideline block; RRC's Annex A examples reuse the names of normative
	// IEs, so extracting them would put a wrong body beside the real one.
	sections := []db.Section{{
		SpecID: "TS 38.331",
		Number: "A.3",
		Content: "```asn1\n-- /example/ ASN1START\nPLMN-IdentityInfo ::= SEQUENCE {\n\twrong\tINTEGER\n}\n-- ASN1STOP\n```\n\n" +
			"```asn1\n-- /bad example/ ASN1START\nBadThing ::= INTEGER\n-- ASN1STOP\n```\n\n" +
			"```asn1\n-- ASN1START\nPLMN-IdentityInfo ::= SEQUENCE {\n\tright\tINTEGER\n}\n-- ASN1STOP\n```",
	}}
	got := Extract(sections)
	if len(got) != 1 || got[0].Name != "PLMN-IdentityInfo" || !strings.Contains(got[0].Text, "right") {
		t.Fatalf("expected only the normative definition, got %+v", got)
	}
}

func TestExtractWhitespaceVariants(t *testing.T) {
	// Fences keep the source text verbatim, and 3GPP documents indent with
	// NBSP as liberally as with tabs: an NBSP-indented head is still a head,
	// an example marker with leading whitespace or NBSP separators is still
	// an example marker, and a "::=" inside a line-tail comment does not
	// start an assignment mid-SEQUENCE.
	sections := []db.Section{{
		SpecID: "TS 38.331",
		Number: "6.3",
		Content: "```asn1\n-- ASN1START\nFirst ::= SEQUENCE {\n\tfield\tINTEGER,\t-- default ::= 32\n\tother\tINTEGER\n}\n\n\u00a0Second ::= INTEGER (0..1)\n-- ASN1STOP\n```\n\n" +
			"```asn1\n\t-- /example/ ASN1START\nWrongOne ::= INTEGER\n-- ASN1STOP\n```\n\n" +
			"```asn1\n--\u00a0/example/\u00a0ASN1START\nWrongTwo ::= INTEGER\n-- ASN1STOP\n```",
	}}
	got := Extract(sections)
	names := make([]string, len(got))
	for i, a := range got {
		names[i] = a.Name
	}
	if len(got) != 2 || names[0] != "First" || names[1] != "Second" {
		t.Fatalf("extracted names = %v, want [First Second]", names)
	}
	if !strings.Contains(got[0].Text, "-- default ::= 32") || !strings.Contains(got[0].Text, "other") {
		t.Errorf("First was split at the tail comment:\n%s", got[0].Text)
	}
}

func TestExtractSkipsOneLineModuleHeader(t *testing.T) {
	// RRC writes the module header in one line — "NR-RRC-Definitions
	// DEFINITIONS AUTOMATIC TAGS ::=" — which must not become an assignment
	// named after the module.
	sections := []db.Section{{
		SpecID: "TS 38.331",
		Number: "6.2.1",
		Content: "```asn1\n-- ASN1START\nNR-RRC-Definitions DEFINITIONS AUTOMATIC TAGS ::=\n\nBEGIN\n\n" +
			"CellIdentity ::= BIT STRING (SIZE (36))\n\nEND\n-- ASN1STOP\n```",
	}}
	got := Extract(sections)
	if len(got) != 1 || got[0].Name != "CellIdentity" {
		t.Fatalf("expected only CellIdentity, got %+v", got)
	}
}

func TestExtractIgnoresOtherFences(t *testing.T) {
	sections := []db.Section{{
		SpecID:  "TS 29.060",
		Number:  "7",
		Content: "```\nFoo ::= SEQUENCE {}\n```\n\nBar ::= outside any fence",
	}}
	if got := Extract(sections); len(got) != 0 {
		t.Errorf("extracted %d assignments from non-asn1 content, want 0", len(got))
	}
}

func TestExtractUnterminatedFence(t *testing.T) {
	sections := []db.Section{{
		SpecID:  "TS 38.413",
		Number:  "9",
		Content: "```asn1\n-- ASN1START\nFoo ::= INTEGER (0..15)",
	}}
	got := Extract(sections)
	if len(got) != 1 || got[0].Name != "Foo" || got[0].Text != "Foo ::= INTEGER (0..15)" {
		t.Errorf("unterminated fence: got %+v", got)
	}
}

func TestKey(t *testing.T) {
	for in, want := range map[string]string{
		"AMF-UE-NGAP-ID": "amfuengapid",
		"AMF UE NGAP ID": "amfuengapid",
		"maxNrOfErrors":  "maxnroferrors",
		"---":            "",
	} {
		if got := Key(in); got != want {
			t.Errorf("Key(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMatch(t *testing.T) {
	assignments := Extract([]db.Section{{
		SpecID:  "TS 38.413",
		Number:  "9.4.5",
		Content: testModule,
	}})

	t.Run("exact", func(t *testing.T) {
		got := Match(assignments, "AMF-UE-NGAP-ID")
		if len(got) != 1 || got[0].Name != "AMF-UE-NGAP-ID" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("IE title form", func(t *testing.T) {
		got := Match(assignments, "AMF UE NGAP ID")
		if len(got) != 1 || got[0].Name != "AMF-UE-NGAP-ID" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("case-insensitive", func(t *testing.T) {
		got := Match(assignments, "causemisc")
		if len(got) != 1 || got[0].Name != "CauseMisc" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("not found", func(t *testing.T) {
		if got := Match(assignments, "NoSuchType"); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestSuggestions(t *testing.T) {
	assignments := Extract([]db.Section{{
		SpecID:  "TS 38.413",
		Number:  "9.4.5",
		Content: testModule,
	}})
	got := Suggestions(assignments, "cause", 20)
	want := []string{"Cause", "CauseMisc"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("suggestions = %v, want %v", got, want)
	}
	if got := Suggestions(assignments, "---", 20); got != nil {
		t.Errorf("empty key should suggest nothing, got %v", got)
	}
	if got := Suggestions(assignments, "cause", 1); len(got) != 1 {
		t.Errorf("cap not applied, got %v", got)
	}
}

func TestRebuild(t *testing.T) {
	d := testutil.SetupTestDB(t)
	if err := d.Exec(`INSERT INTO specs (id, version, version_token, title, release, series) VALUES
		('TS 38.413', '18.6.0', 'i60', 'NG Application Protocol (NGAP)', '18', '38')`); err != nil {
		t.Fatalf("insert spec: %v", err)
	}
	if err := d.Exec(`INSERT INTO sections (spec_id, version, number, title, level, parent_number, content)
		VALUES ('TS 38.413', '18.6.0', '9.4.5', 'Information Element definitions', 2, NULL, ?)`, testModule); err != nil {
		t.Fatalf("insert section: %v", err)
	}

	stats, err := Rebuild(context.Background(), d)
	if err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	if !strings.Contains(stats, "5 assignments from 1 sections across 1 specs") {
		t.Errorf("stats = %q", stats)
	}

	defs, err := d.LookupASN1(context.Background(), "AMF-UE-NGAP-ID", Key("AMF-UE-NGAP-ID"), "")
	if err != nil {
		t.Fatalf("LookupASN1: %v", err)
	}
	if len(defs) != 1 || defs[0].Body != "AMF-UE-NGAP-ID ::= INTEGER (0..1099511627775)" ||
		defs[0].SectionNumber != "9.4.5" || defs[0].Release != "18" {
		t.Fatalf("unexpected defs: %+v", defs)
	}

	// Rebuild is a wholesale replace: running it again must not duplicate.
	if _, err := Rebuild(context.Background(), d); err != nil {
		t.Fatalf("second Rebuild: %v", err)
	}
	defs, err = d.LookupASN1(context.Background(), "AMF-UE-NGAP-ID", "", "")
	if err != nil || len(defs) != 1 {
		t.Fatalf("after second rebuild: defs=%+v err=%v", defs, err)
	}
}
