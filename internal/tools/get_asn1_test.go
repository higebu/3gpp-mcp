package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/higebu/3gpp-mcp/internal/db"
)

// asn1TestModule mimics how the DOCX converter renders an NGAP-style ASN.1
// clause: one ```asn1 fence holding a whole module, markers included.
const asn1TestModule = "Each IE is defined below.\n\n" +
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

func seedASN1Spec(t *testing.T, d *db.DB) {
	t.Helper()
	if err := d.Exec(`INSERT INTO specs (id, version, version_token, title, release, series) VALUES
		('TS 38.413', '18.6.0', 'i60', 'NG Application Protocol (NGAP)', '18', '38'),
		('TS 37.355', '18.2.0', 'i20', 'LTE Positioning Protocol (LPP)', '18', '37')`); err != nil {
		t.Fatalf("insert spec: %v", err)
	}
	sections := []struct{ spec, number, title, content string }{
		{"TS 38.413", "9.3.4", "PDU definitions", "```asn1\n-- ASN1START\nPDU-Container ::= SEQUENCE {\n\tid\tINTEGER\n}\n-- ASN1STOP\n```"},
		{"TS 38.413", "9.4.5", "Information Element definitions", asn1TestModule},
		{"TS 37.355", "6.5.2", "GNSS assistance data", "```asn1\n-- ASN1START\nKlobucharModel-r16 ::= SEQUENCE {\n\talpha0-r16\tINTEGER,\n\tbeta0-r16\tINTEGER\n}\n-- ASN1STOP\n```"},
		// The converter also accepts the space-less marker; the FTS seeding of
		// the corpus-wide index must find this section too.
		{"TS 37.355", "6.5.3", "Barometric assistance", "```asn1\n--ASN1START\nBaro-NoSpace-r16 ::= INTEGER (0..7)\n--ASN1STOP\n```"},
	}
	for _, s := range sections {
		if err := d.Exec(`INSERT INTO sections (spec_id, version, number, title, level, parent_number, content)
			VALUES (?, (SELECT version FROM specs WHERE id = ?), ?, ?, 2, NULL, ?)`, s.spec, s.spec, s.number, s.title, s.content); err != nil {
			t.Fatalf("insert section %s: %v", s.number, err)
		}
	}
}

func TestExtractASN1(t *testing.T) {
	sections := []db.Section{{
		SpecID:  "TS 38.413",
		Version: "18.6.0",
		Number:  "9.4.5",
		Title:   "Information Element definitions",
		Content: asn1TestModule,
	}}
	got := ExtractASN1(sections)

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

func TestExtractASN1TrimsTrailingComments(t *testing.T) {
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
	got := ExtractASN1(sections)
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

func TestExtractASN1SkipsExampleFences(t *testing.T) {
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
	got := ExtractASN1(sections)
	if len(got) != 1 || got[0].Name != "PLMN-IdentityInfo" || !strings.Contains(got[0].Text, "right") {
		t.Fatalf("expected only the normative definition, got %+v", got)
	}
}

func TestExtractASN1WhitespaceVariants(t *testing.T) {
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
	got := ExtractASN1(sections)
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

func TestExtractASN1SkipsOneLineModuleHeader(t *testing.T) {
	// RRC writes the module header in one line — "NR-RRC-Definitions
	// DEFINITIONS AUTOMATIC TAGS ::=" — which must not become an assignment
	// named after the module.
	sections := []db.Section{{
		SpecID: "TS 38.331",
		Number: "6.2.1",
		Content: "```asn1\n-- ASN1START\nNR-RRC-Definitions DEFINITIONS AUTOMATIC TAGS ::=\n\nBEGIN\n\n" +
			"CellIdentity ::= BIT STRING (SIZE (36))\n\nEND\n-- ASN1STOP\n```",
	}}
	got := ExtractASN1(sections)
	if len(got) != 1 || got[0].Name != "CellIdentity" {
		t.Fatalf("expected only CellIdentity, got %+v", got)
	}
}

func TestExtractASN1IgnoresOtherFences(t *testing.T) {
	sections := []db.Section{{
		SpecID:  "TS 29.060",
		Number:  "7",
		Content: "```\nFoo ::= SEQUENCE {}\n```\n\nBar ::= outside any fence",
	}}
	if got := ExtractASN1(sections); len(got) != 0 {
		t.Errorf("extracted %d assignments from non-asn1 content, want 0", len(got))
	}
}

func TestExtractASN1UnterminatedFence(t *testing.T) {
	sections := []db.Section{{
		SpecID:  "TS 38.413",
		Number:  "9",
		Content: "```asn1\n-- ASN1START\nFoo ::= INTEGER (0..15)",
	}}
	got := ExtractASN1(sections)
	if len(got) != 1 || got[0].Name != "Foo" || got[0].Text != "Foo ::= INTEGER (0..15)" {
		t.Errorf("unterminated fence: got %+v", got)
	}
}

func TestMatchASN1(t *testing.T) {
	assignments := ExtractASN1([]db.Section{{
		SpecID:  "TS 38.413",
		Number:  "9.4.5",
		Content: asn1TestModule,
	}})

	t.Run("exact", func(t *testing.T) {
		got := MatchASN1(assignments, "AMF-UE-NGAP-ID")
		if len(got) != 1 || got[0].Name != "AMF-UE-NGAP-ID" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("IE title form", func(t *testing.T) {
		got := MatchASN1(assignments, "AMF UE NGAP ID")
		if len(got) != 1 || got[0].Name != "AMF-UE-NGAP-ID" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("case-insensitive", func(t *testing.T) {
		got := MatchASN1(assignments, "causemisc")
		if len(got) != 1 || got[0].Name != "CauseMisc" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("not found", func(t *testing.T) {
		if got := MatchASN1(assignments, "NoSuchType"); len(got) != 0 {
			t.Fatalf("got %+v", got)
		}
	})
}

func TestRenderASN1ListingCountsListedNames(t *testing.T) {
	// A name defined twice in one section is listed once; the header must
	// count the listed lines, not the raw assignments.
	sections := []db.Section{{
		SpecID:  "TS 38.331",
		Number:  "6.3",
		Content: "```asn1\n-- ASN1START\nDup ::= INTEGER (0..1)\n\nDup ::= INTEGER (0..2)\n\nOther ::= INTEGER\n-- ASN1STOP\n```",
	}}
	got := RenderASN1Listing(ExtractASN1(sections))
	if !strings.HasPrefix(got, "2 ASN.1 assignments") {
		t.Errorf("header should count listed names, got:\n%s", got)
	}
	if strings.Count(got, "\nDup\n") != 1 {
		t.Errorf("duplicate name listed more than once:\n%s", got)
	}
}

func TestASN1Suggestions(t *testing.T) {
	assignments := ExtractASN1([]db.Section{{
		SpecID:  "TS 38.413",
		Number:  "9.4.5",
		Content: asn1TestModule,
	}})
	got := ASN1Suggestions(assignments, "cause", 20)
	want := []string{"Cause", "CauseMisc"}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("suggestions = %v, want %v", got, want)
	}
}

func TestHandleGetASN1(t *testing.T) {
	d := setupTestDB(t)
	seedASN1Spec(t, d)
	handler := HandleGetASN1(NewSource(d))

	t.Run("lookup", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{SpecID: "TS 38.413", Name: "AMF-UE-NGAP-ID"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "[Source: TS 38.413 v18.6.0 (Rel-18) — Section 9.4.5 — AMF-UE-NGAP-ID]") {
			t.Errorf("missing source header, got: %s", text)
		}
		if !strings.Contains(text, "AMF-UE-NGAP-ID ::= INTEGER (0..1099511627775)") {
			t.Errorf("missing definition, got: %s", text)
		}
	})

	t.Run("lookup via IE title", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{SpecID: "TS 38.413", Name: "AMF UE NGAP ID"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if text := getTextContent(result); !strings.Contains(text, "AMF-UE-NGAP-ID ::=") {
			t.Errorf("missing definition, got: %s", text)
		}
	})

	t.Run("listing", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{SpecID: "TS 38.413"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "[Source: TS 38.413 v18.6.0 (Rel-18)]") {
			t.Errorf("missing source header, got: %s", text)
		}
		if !strings.Contains(text, "6 ASN.1 assignments") {
			t.Errorf("missing count, got: %s", text)
		}
		for _, want := range []string{"Section 9.3.4 — PDU definitions:", "Section 9.4.5 — Information Element definitions:", "PDU-Container", "maxNrOfErrors"} {
			if !strings.Contains(text, want) {
				t.Errorf("listing missing %q, got: %s", want, text)
			}
		}
	})

	t.Run("not found with suggestions", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{SpecID: "TS 38.413", Name: "CauseRadio"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected error result")
		}
		text := getTextContent(result)
		if !strings.Contains(text, "not found") || !strings.Contains(text, "Similar names") {
			t.Errorf("expected suggestions, got: %s", text)
		}
	})

	t.Run("spec without ASN.1", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{SpecID: "TS 23.501"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected error result")
		}
		if text := getTextContent(result); !strings.Contains(text, "no ASN.1 definitions") {
			t.Errorf("got: %s", text)
		}
	})

	t.Run("unknown spec", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{SpecID: "TS 99.999"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected error result")
		}
	})

	t.Run("missing spec_id and name", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected error result")
		}
	})

	t.Run("cross-spec lookup without spec_id", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{Name: "KlobucharModel-r16"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if result.IsError {
			t.Fatalf("unexpected error result: %s", text)
		}
		if !strings.Contains(text, "[Source: TS 37.355 v18.2.0 (Rel-18) — Section 6.5.2 — KlobucharModel-r16]") {
			t.Errorf("missing source header, got: %s", text)
		}
		if !strings.Contains(text, "KlobucharModel-r16 ::= SEQUENCE") {
			t.Errorf("missing definition, got: %s", text)
		}
	})

	t.Run("cross-spec lookup finds space-less markers", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{Name: "Baro-NoSpace-r16"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if text := getTextContent(result); result.IsError || !strings.Contains(text, "Baro-NoSpace-r16 ::= INTEGER (0..7)") {
			t.Errorf("space-less --ASN1START section missing from corpus index, got: %s", text)
		}
	})

	t.Run("cross-spec lookup is fuzzy", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{Name: "klobuchar model r16"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if text := getTextContent(result); result.IsError || !strings.Contains(text, "KlobucharModel-r16 ::=") {
			t.Errorf("fuzzy corpus lookup failed, got: %s", text)
		}
	})

	t.Run("version without spec_id", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{Name: "KlobucharModel-r16", Version: "18.0.0"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected error result")
		}
	})

	t.Run("wrong spec gets a cross-spec hint", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{SpecID: "TS 38.413", Name: "KlobucharModel-r16"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Fatalf("expected error result")
		}
		text := getTextContent(result)
		if !strings.Contains(text, "not found in TS 38.413") || !strings.Contains(text, "defined in TS 37.355") {
			t.Errorf("expected cross-spec hint, got: %s", text)
		}
	})

	t.Run("spec without ASN.1 hints at the defining spec", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{SpecID: "TS 23.501", Name: "KlobucharModel-r16"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !result.IsError || !strings.Contains(text, "defined in TS 37.355") {
			t.Errorf("expected cross-spec hint, got: %s", text)
		}
	})

	t.Run("corpus not-found with suggestions", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetASN1Input{Name: "Klobuchar"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !result.IsError || !strings.Contains(text, "Similar names: KlobucharModel-r16") {
			t.Errorf("expected corpus suggestions, got: %s", text)
		}
	})
}
