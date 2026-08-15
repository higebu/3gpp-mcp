package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/higebu/3gpp-mcp/internal/asn1index"
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
	if _, err := asn1index.Rebuild(context.Background(), d); err != nil {
		t.Fatalf("rebuild asn1 index: %v", err)
	}
}

func TestGetASN1WithoutIndex(t *testing.T) {
	// A database from before the index existed (serve opens read-only and
	// cannot create the table): the cross-spec mode names the rebuild
	// command, the per-spec path falls back to reading the document.
	d := setupTestDB(t)
	seedASN1Spec(t, d)
	if err := d.Exec("DROP TABLE asn1_defs"); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	handler := HandleGetASN1(NewSource(d))

	result, _, err := handler(context.Background(), nil, GetASN1Input{Name: "AMF-UE-NGAP-ID"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError || !strings.Contains(getTextContent(result), "build-asn1-index") {
		t.Errorf("expected missing-index error naming the command, got: %s", getTextContent(result))
	}

	result, _, err = handler(context.Background(), nil, GetASN1Input{SpecID: "TS 38.413", Name: "AMF-UE-NGAP-ID"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text := getTextContent(result); result.IsError || !strings.Contains(text, "AMF-UE-NGAP-ID ::= INTEGER") {
		t.Errorf("per-spec fallback failed without the index, got: %s", text)
	}
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
