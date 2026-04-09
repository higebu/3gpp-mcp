package db

import (
	"path/filepath"
	"strings"
	"testing"
)

const seedData = `
INSERT INTO specs (id, title, version, release, series) VALUES
    ('TS 23.501', 'System architecture for the 5G System (5GS)', '18.6.0', 'Rel-18', '23'),
    ('TS 29.510', 'Network Function Repository Services', '18.5.0', 'Rel-18', '29');

INSERT INTO sections (spec_id, number, title, level, parent_number, content) VALUES
    ('TS 23.501', '1', 'Scope', 1, NULL, '# 1 Scope
This document defines the system architecture.'),
    ('TS 23.501', '5', 'Architecture', 1, NULL, '# 5 Architecture
The 5G system architecture is defined here.'),
    ('TS 23.501', '5.1', 'General', 2, '5', '## 5.1 General
General architecture description for 5G.'),
    ('TS 23.501', '5.1.1', 'Overview', 3, '5.1', '### 5.1.1 Overview
Overview of the architecture components.'),
    ('TS 29.510', '1', 'Scope', 1, NULL, '# 1 Scope
This document defines the NRF services.'),
    ('TS 29.510', '6', 'API Definitions', 1, NULL, '# 6 API Definitions
API definitions for NRF.');

INSERT INTO specs (id, title, version, release, series) VALUES
    ('TS 24.229', 'IP multimedia call control protocol', '18.4.0', 'Rel-18', '24');

INSERT INTO sections (spec_id, number, title, level, parent_number, content) VALUES
    ('TS 24.229', '5', 'Procedures', 1, NULL, '# 5 Procedures
The IMS registration procedures.'),
    ('TS 24.229', '5.1', 'Registration', 2, '5', '## 5.1 Registration
The IMS registration procedures are specified in 3GPP TS 23.228 clause 5.2.1.
The security mechanisms are defined in TS 33.203. See also RFC 3261 section 10.2
for SIP registration details and IETF RFC 3327 for the Path header.
The authentication uses IMS-AKA as described in TS 33.203 subclause 6.1.');

INSERT INTO spec_references (source_spec_id, source_section, target_spec, target_section, context) VALUES
    ('TS 24.229', '5.1', 'TS 23.228', '5.2.1', '...specified in 3GPP TS 23.228 clause 5.2.1...'),
    ('TS 24.229', '5.1', 'TS 33.203', '', '...security mechanisms are defined in TS 33.203...'),
    ('TS 24.229', '5.1', 'RFC 3261', '10.2', '...RFC 3261 section 10.2 for SIP registration...'),
    ('TS 24.229', '5.1', 'RFC 3327', '', '...IETF RFC 3327 for the Path header...'),
    ('TS 24.229', '5.1', 'TS 33.203', '6.1', '...IMS-AKA as described in TS 33.203 subclause 6.1...');

INSERT INTO openapi_specs (spec_id, api_name, version, filename, content) VALUES
    ('TS 29.510', 'Nnrf_NFManagement', 'v1.3.0', 'TS29510_Nnrf_NFManagement.yaml', 'openapi: 3.0.0
info:
  title: Nnrf_NFManagement
  version: v1.3.0
paths:
  /nf-instances:
    get:
      summary: List NF Instances
  /nf-instances/{nfInstanceID}:
    put:
      summary: Register NF Instance
components:
  schemas:
    NFProfile:
      type: object
      properties:
        nfInstanceId:
          type: string');
`

func setupTestDB(t *testing.T) *DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := d.ExecScript(Schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}
	if err := d.ExecScript(seedData); err != nil {
		t.Fatalf("failed to seed data: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestListSpecs(t *testing.T) {
	d := setupTestDB(t)

	t.Run("all", func(t *testing.T) {
		result, err := d.ListSpecs("", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Specs) != 3 {
			t.Fatalf("expected 3 specs, got %d", len(result.Specs))
		}
		if result.Specs[0].ID != "TS 23.501" {
			t.Errorf("expected first spec ID 'TS 23.501', got %q", result.Specs[0].ID)
		}
		if result.TotalCount != 3 {
			t.Errorf("expected total_count 3, got %d", result.TotalCount)
		}
	})

	t.Run("filter by series", func(t *testing.T) {
		result, err := d.ListSpecs("29", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Specs) != 1 {
			t.Fatalf("expected 1 spec, got %d", len(result.Specs))
		}
		if result.Specs[0].ID != "TS 29.510" {
			t.Errorf("expected spec ID 'TS 29.510', got %q", result.Specs[0].ID)
		}
		if result.TotalCount != 1 {
			t.Errorf("expected total_count 1, got %d", result.TotalCount)
		}
	})

	t.Run("no match", func(t *testing.T) {
		result, err := d.ListSpecs("99", 0, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Specs) != 0 {
			t.Fatalf("expected 0 specs, got %d", len(result.Specs))
		}
		if result.TotalCount != 0 {
			t.Errorf("expected total_count 0, got %d", result.TotalCount)
		}
	})

	t.Run("with limit", func(t *testing.T) {
		result, err := d.ListSpecs("", 1, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Specs) != 1 {
			t.Fatalf("expected 1 spec, got %d", len(result.Specs))
		}
		if result.TotalCount != 3 {
			t.Errorf("expected total_count 3, got %d", result.TotalCount)
		}
		if result.Specs[0].ID != "TS 23.501" {
			t.Errorf("expected first spec 'TS 23.501', got %q", result.Specs[0].ID)
		}
	})

	t.Run("with offset", func(t *testing.T) {
		result, err := d.ListSpecs("", 1, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Specs) != 1 {
			t.Fatalf("expected 1 spec, got %d", len(result.Specs))
		}
		if result.Specs[0].ID != "TS 24.229" {
			t.Errorf("expected second spec 'TS 24.229', got %q", result.Specs[0].ID)
		}
	})

	t.Run("offset beyond end", func(t *testing.T) {
		result, err := d.ListSpecs("", 10, 100)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Specs) != 0 {
			t.Fatalf("expected 0 specs, got %d", len(result.Specs))
		}
		if result.TotalCount != 3 {
			t.Errorf("expected total_count 3, got %d", result.TotalCount)
		}
	})

	t.Run("no limit", func(t *testing.T) {
		result, err := d.ListSpecs("", -1, 0)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(result.Specs) != 3 {
			t.Fatalf("expected 3 specs, got %d", len(result.Specs))
		}
	})
}

func TestGetTOC(t *testing.T) {
	d := setupTestDB(t)

	t.Run("existing spec", func(t *testing.T) {
		sections, err := d.GetTOC("TS 23.501")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 4 {
			t.Fatalf("expected 4 sections, got %d", len(sections))
		}
		if sections[0].Number != "1" || sections[0].Title != "Scope" {
			t.Errorf("unexpected first section: %+v", sections[0])
		}
	})

	t.Run("nonexistent spec", func(t *testing.T) {
		sections, err := d.GetTOC("TS 99.999")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 0 {
			t.Fatalf("expected 0 sections, got %d", len(sections))
		}
	})
}

func TestGetSection(t *testing.T) {
	d := setupTestDB(t)

	t.Run("single section", func(t *testing.T) {
		sections, err := d.GetSection("TS 23.501", "1", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 1 {
			t.Fatalf("expected 1 section, got %d", len(sections))
		}
		if sections[0].Content == "" {
			t.Error("expected non-empty content")
		}
	})

	t.Run("with subsections", func(t *testing.T) {
		sections, err := d.GetSection("TS 23.501", "5", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 3 {
			t.Fatalf("expected 3 sections (5, 5.1, 5.1.1), got %d", len(sections))
		}
	})

	t.Run("without subsections", func(t *testing.T) {
		sections, err := d.GetSection("TS 23.501", "5", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 1 {
			t.Fatalf("expected 1 section, got %d", len(sections))
		}
	})

	t.Run("nonexistent section", func(t *testing.T) {
		sections, err := d.GetSection("TS 23.501", "99", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(sections) != 0 {
			t.Fatalf("expected 0 sections, got %d", len(sections))
		}
	})
}

func TestSanitizeFTS5Query(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bare hyphenated term", "IMS-AKA", `"IMS-AKA"`},
		{"bare hyphenated term lowercase", "sec-agree", `"sec-agree"`},
		{"multiple hyphens", "one-two-three", `"one-two-three"`},
		{"no hyphen", "AMF", "AMF"},
		{"operators preserved", "AMF AND authentication", "AMF AND authentication"},
		{"OR operator", "AMF OR SMF", "AMF OR SMF"},
		{"NOT operator", "AMF NOT SMF", "AMF NOT SMF"},
		{"quoted phrase preserved", `"service based interface"`, `"service based interface"`},
		{"prefix wildcard", "handov*", "handov*"},
		{"valid column filter", "content:handover", "content:handover"},
		{"valid column filter title", "title:authentication", "title:authentication"},
		{"column filter with hyphen value", "title:IMS-AKA", `title:"IMS-AKA"`},
		{"NEAR preserved", "NEAR(AMF UE, 5)", "NEAR(AMF UE, 5)"},
		{"leading hyphen NOT shorthand", "-excluded", "-excluded"},
		{"leading hyphen with more hyphens", "-one-two", `"-one-two"`},
		{"mixed query", `IMS-AKA AND "core network"`, `"IMS-AKA" AND "core network"`},
		{"hyphen with operator", "sec-agree OR authentication", `"sec-agree" OR authentication`},
		{"invalid column is hyphenated", "IMS-AKA", `"IMS-AKA"`},
		{"empty query", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeFTS5Query(tt.input)
			if got != tt.want {
				t.Errorf("sanitizeFTS5Query(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSearch(t *testing.T) {
	d := setupTestDB(t)

	t.Run("basic search", func(t *testing.T) {
		results, err := d.Search("architecture", nil, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) == 0 {
			t.Fatal("expected at least 1 result")
		}
	})

	t.Run("search with single spec filter", func(t *testing.T) {
		results, err := d.Search("Scope", []string{"TS 29.510"}, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 1 {
			t.Fatalf("expected 1 result, got %d", len(results))
		}
		if results[0].SpecID != "TS 29.510" {
			t.Errorf("expected spec_id 'TS 29.510', got %q", results[0].SpecID)
		}
	})

	t.Run("search with multiple spec filter", func(t *testing.T) {
		results, err := d.Search("Scope", []string{"TS 23.501", "TS 29.510"}, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 2 {
			t.Fatalf("expected 2 results, got %d", len(results))
		}
	})

	t.Run("no results", func(t *testing.T) {
		results, err := d.Search("xyznonexistent", nil, 10)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(results) != 0 {
			t.Fatalf("expected 0 results, got %d", len(results))
		}
	})

	t.Run("hyphenated term does not error", func(t *testing.T) {
		// Insert a section with a hyphenated term.
		err := d.ExecScript(`INSERT INTO specs (id, title) VALUES ('TS 33.203', 'IMS Security') ON CONFLICT DO NOTHING;
INSERT OR REPLACE INTO sections (spec_id, number, title, level, parent_number, content) VALUES
    ('TS 33.203', '1', 'Scope', 1, NULL, '# 1 Scope
This document covers IMS-AKA and sec-agree mechanisms.');`)
		if err != nil {
			t.Fatalf("failed to insert test data: %v", err)
		}
		results, err := d.Search("IMS-AKA", nil, 10)
		if err != nil {
			t.Fatalf("unexpected error for hyphenated search: %v", err)
		}
		if len(results) == 0 {
			t.Error("expected at least 1 result for IMS-AKA")
		}
	})
}

func TestListOpenAPI(t *testing.T) {
	d := setupTestDB(t)

	t.Run("all", func(t *testing.T) {
		specs, err := d.ListOpenAPI("")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(specs) != 1 {
			t.Fatalf("expected 1 openapi spec, got %d", len(specs))
		}
		if specs[0].APIName != "Nnrf_NFManagement" {
			t.Errorf("expected api_name 'Nnrf_NFManagement', got %q", specs[0].APIName)
		}
	})

	t.Run("filter by spec", func(t *testing.T) {
		specs, err := d.ListOpenAPI("TS 23.501")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(specs) != 0 {
			t.Fatalf("expected 0 openapi specs, got %d", len(specs))
		}
	})
}

func TestGetOpenAPI(t *testing.T) {
	d := setupTestDB(t)

	t.Run("existing", func(t *testing.T) {
		content, err := d.GetOpenAPI("TS 29.510", "Nnrf_NFManagement")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if content == "" {
			t.Error("expected non-empty content")
		}
	})

	t.Run("nonexistent", func(t *testing.T) {
		_, err := d.GetOpenAPI("TS 29.510", "Nonexistent")
		if err == nil {
			t.Fatal("expected error for nonexistent api")
		}
	})
}

func TestUpsertOpenAPI(t *testing.T) {
	d := setupTestDB(t)

	t.Run("insert new", func(t *testing.T) {
		err := d.UpsertOpenAPI("TS 29.512", "Npcf_SMPolicyControl", "v1.0.0", "test.yaml", "openapi: 3.0.0")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		content, err := d.GetOpenAPI("TS 29.512", "Npcf_SMPolicyControl")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if content != "openapi: 3.0.0" {
			t.Errorf("unexpected content: %s", content)
		}
	})

	t.Run("upsert existing", func(t *testing.T) {
		err := d.UpsertOpenAPI("TS 29.510", "Nnrf_NFManagement", "v2.0.0", "updated.yaml", "updated content")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		content, err := d.GetOpenAPI("TS 29.510", "Nnrf_NFManagement")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if content != "updated content" {
			t.Errorf("expected updated content, got: %s", content)
		}
	})
}

func TestExtractReferences(t *testing.T) {
	content := `## 5.1 Registration
The IMS registration procedures are specified in 3GPP TS 23.228 clause 5.2.1.
The security mechanisms are defined in TS 33.203. See also RFC 3261 section 10.2
for SIP registration details and IETF RFC 3327 for the Path header.
The authentication uses IMS-AKA as described in TS 33.203 subclause 6.1.`

	refs := ExtractReferences("TS 24.229", "5.1", content, nil)

	// Expect: TS 23.228 clause 5.2.1, TS 33.203 (no section), RFC 3261 section 10.2,
	//         RFC 3327 (no section), TS 33.203 subclause 6.1
	if len(refs) != 5 {
		t.Fatalf("expected 5 references, got %d: %+v", len(refs), refs)
	}

	// Check TS 23.228
	found := false
	for _, r := range refs {
		if r.TargetSpec == "TS 23.228" && r.TargetSection == "5.2.1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected reference to TS 23.228 clause 5.2.1")
	}

	// Check RFC 3261
	found = false
	for _, r := range refs {
		if r.TargetSpec == "RFC 3261" && r.TargetSection == "10.2" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected reference to RFC 3261 section 10.2")
	}

	// Check self-reference exclusion
	for _, r := range refs {
		if r.TargetSpec == "TS 24.229" {
			t.Errorf("should not include self-reference: %+v", r)
		}
	}

	// All refs should have context
	for _, r := range refs {
		if r.Context == "" {
			t.Errorf("expected non-empty context for %s %s", r.TargetSpec, r.TargetSection)
		}
	}
}

func TestExtractReferences_UnicodeWhitespace(t *testing.T) {
	// 3GPP DOCX files use DEGREE SIGN (U+00B0) and NO-BREAK SPACE (U+00A0) as separators.
	content := "Procedures in 3GPP\u00b0TS\u00b023.228\u00b0[7] and 3GPP\u00a0TS\u00a029.214\u00a0[13D]."

	refs := ExtractReferences("TS 24.229", "5.2.2.1", content, nil)
	if len(refs) != 2 {
		t.Fatalf("expected 2 references, got %d: %+v", len(refs), refs)
	}

	specSet := make(map[string]bool)
	for _, r := range refs {
		specSet[r.TargetSpec] = true
	}
	if !specSet["TS 23.228"] {
		t.Error("expected reference to TS 23.228 (via degree sign separator)")
	}
	if !specSet["TS 29.214"] {
		t.Error("expected reference to TS 29.214 (via NBSP separator)")
	}
}

func TestExtractReferences_Annex(t *testing.T) {
	// "TS X Annex Y" pattern (keyword after spec ID)
	content := `The security procedures are described in TS 33.203 Annex H.
See also 3GPP TS 23.228 annex A.1 for further details.`

	refs := ExtractReferences("TS 24.229", "5.2.2.1", content, nil)
	if len(refs) != 2 {
		t.Fatalf("expected 2 references, got %d: %+v", len(refs), refs)
	}

	found := false
	for _, r := range refs {
		if r.TargetSpec == "TS 33.203" && r.TargetSection == "H" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected reference to TS 33.203 Annex H")
	}

	found = false
	for _, r := range refs {
		if r.TargetSpec == "TS 23.228" && r.TargetSection == "A.1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected reference to TS 23.228 annex A.1")
	}
}

func TestExtractReferences_PrefixPattern(t *testing.T) {
	// "keyword Y of TS X" pattern (keyword before spec ID)
	content := `The mode is described in Annex H of 3GPP TS 33.203.
See subclause 11.9 of TS 29.061 and clause B.1.1 of 3GPP TS 24.186.`

	refs := ExtractReferences("TS 24.229", "5.2.2.1", content, nil)

	expect := map[string]string{
		"TS 33.203": "H",
		"TS 29.061": "11.9",
		"TS 24.186": "B.1.1",
	}
	for spec, section := range expect {
		found := false
		for _, r := range refs {
			if r.TargetSpec == spec && r.TargetSection == section {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected reference to %s section %s, got refs: %+v", spec, section, refs)
		}
	}
}

func TestGetReferences(t *testing.T) {
	d := setupTestDB(t)

	t.Run("outgoing", func(t *testing.T) {
		refs, err := d.GetReferences("TS 24.229", "5.1", "outgoing", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 5 {
			t.Fatalf("expected 5 outgoing refs, got %d", len(refs))
		}
	})

	t.Run("outgoing with subsections", func(t *testing.T) {
		refs, err := d.GetReferences("TS 24.229", "5", "outgoing", true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Section 5 has no refs, but 5.1 has 5 refs
		if len(refs) != 5 {
			t.Fatalf("expected 5 outgoing refs (from subsections), got %d", len(refs))
		}
	})

	t.Run("incoming", func(t *testing.T) {
		refs, err := d.GetReferences("TS 33.203", "", "incoming", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// TS 33.203 is referenced twice (without section and with 6.1)
		if len(refs) != 2 {
			t.Fatalf("expected 2 incoming refs, got %d", len(refs))
		}
	})

	t.Run("incoming with section", func(t *testing.T) {
		refs, err := d.GetReferences("TS 33.203", "6.1", "incoming", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Should match exact (target_section="6.1") + general spec ref (target_section="")
		if len(refs) != 2 {
			t.Fatalf("expected 2 incoming refs, got %d", len(refs))
		}
		for _, ref := range refs {
			if ref.SourceSpecID != "TS 24.229" || ref.SourceSection != "5.1" {
				t.Errorf("unexpected source: %s %s", ref.SourceSpecID, ref.SourceSection)
			}
		}
	})

	t.Run("no results", func(t *testing.T) {
		refs, err := d.GetReferences("TS 99.999", "", "incoming", false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(refs) != 0 {
			t.Fatalf("expected 0 refs, got %d", len(refs))
		}
	})

	t.Run("invalid direction", func(t *testing.T) {
		_, err := d.GetReferences("TS 24.229", "5.1", "sideways", false)
		if err == nil {
			t.Fatal("expected error for invalid direction")
		}
	})
}

func TestInsertSpecWithSections_References(t *testing.T) {
	d := setupTestDB(t)

	spec := Spec{ID: "TS 99.001", Title: "Test Spec", Series: "99"}
	sections := []Section{
		{
			SpecID:  "TS 99.001",
			Number:  "1",
			Title:   "Scope",
			Level:   1,
			Content: "# 1 Scope\nThis spec references TS 23.501 clause 5.1 and RFC 8259.",
		},
	}

	err := d.InsertSpecWithSections(spec, sections)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify references were auto-extracted
	refs, err := d.GetReferences("TS 99.001", "1", "outgoing", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(refs) != 2 {
		t.Fatalf("expected 2 auto-extracted refs, got %d: %+v", len(refs), refs)
	}

	// Check that TS 23.501 ref has the target title resolved
	for _, r := range refs {
		if r.TargetSpec == "TS 23.501" && r.TargetSection == "5.1" {
			if r.TargetTitle == "" {
				t.Error("expected target title for TS 23.501 section 5.1 (exists in DB)")
			}
		}
	}
}

func TestParseBracketedRefMap(t *testing.T) {
	content := `## 2 References

[1]	3GPP TR 21.905: "Vocabulary for 3GPP Specifications"
[2]	3GPP TS 23.228: "IP Multimedia Subsystem (IMS)"
[19]	3GPP TS 33.203: "Access security for IP-based services"
[13D]	TS 29.214: "Policy and Charging Control"`

	m := ParseBracketedRefMap(content)
	if m == nil {
		t.Fatal("expected non-nil map")
	}
	if len(m) != 4 {
		t.Fatalf("expected 4 mappings, got %d: %v", len(m), m)
	}

	expect := map[string]string{
		"1":   "TR 21.905",
		"2":   "TS 23.228",
		"19":  "TS 33.203",
		"13D": "TS 29.214",
	}
	for k, v := range expect {
		if m[k] != v {
			t.Errorf("bracket [%s]: expected %q, got %q", k, v, m[k])
		}
	}

	// Empty content returns nil.
	if ParseBracketedRefMap("") != nil {
		t.Error("expected nil for empty content")
	}

	// Content without bracket mappings returns nil.
	if ParseBracketedRefMap("No references here.") != nil {
		t.Error("expected nil for content without bracket mappings")
	}
}

func TestParseBracketedRefMap_UnicodeWhitespace(t *testing.T) {
	// NO-BREAK SPACE (U+00A0) between bracket and spec ID.
	content := "[5]\u00a03GPP\u00a0TS\u00a023.501: \"System architecture\""
	m := ParseBracketedRefMap(content)
	if m == nil {
		t.Fatal("expected non-nil map")
	}
	if m["5"] != "TS 23.501" {
		t.Errorf("expected TS 23.501, got %q", m["5"])
	}
}

func TestExtractReferences_Bracketed(t *testing.T) {
	content := `The procedures in [19] clause 6 shall apply.
See also [19] Annex H for security requirements.
Further details in [2] subclause 5.2.1.
And [99] clause 3 for unknown ref.`

	bracketMap := map[string]string{
		"19": "TS 33.203",
		"2":  "TS 23.228",
	}
	refs := ExtractReferences("TS 24.229", "5.1", content, bracketMap)

	expect := map[string]string{
		"TS 33.203": "6",
		"TS 23.228": "5.2.1",
	}
	// TS 33.203#H is also expected
	found33203H := false
	for _, r := range refs {
		if r.TargetSpec == "TS 33.203" && r.TargetSection == "H" {
			found33203H = true
		}
		if sec, ok := expect[r.TargetSpec]; ok && r.TargetSection == sec {
			delete(expect, r.TargetSpec)
		}
	}
	if len(expect) > 0 {
		t.Errorf("missing expected references: %v, got refs: %+v", expect, refs)
	}
	if !found33203H {
		t.Errorf("expected reference to TS 33.203 Annex H, got refs: %+v", refs)
	}

	// [99] should be skipped (not in bracket map).
	for _, r := range refs {
		if r.TargetSection == "3" && r.Context != "" && strings.Contains(r.Context, "[99]") {
			t.Errorf("should not resolve unknown bracket [99]: %+v", r)
		}
	}

	// All refs should have context.
	for _, r := range refs {
		if r.Context == "" {
			t.Errorf("expected non-empty context for %s %s", r.TargetSpec, r.TargetSection)
		}
	}
}

func TestExtractReferences_BracketedSelfRef(t *testing.T) {
	content := `See [1] clause 5.1 for details.`
	bracketMap := map[string]string{"1": "TS 24.229"}
	refs := ExtractReferences("TS 24.229", "3", content, bracketMap)
	if len(refs) != 0 {
		t.Errorf("expected 0 refs (self-reference excluded), got %d: %+v", len(refs), refs)
	}
}

func TestExtractReferences_BracketedDedup(t *testing.T) {
	// Same target from both explicit TS ref and bracket ref should be deduplicated.
	content := `See TS 33.203 clause 6 and [19] clause 6.`
	bracketMap := map[string]string{"19": "TS 33.203"}
	refs := ExtractReferences("TS 24.229", "5.1", content, bracketMap)

	count := 0
	for _, r := range refs {
		if r.TargetSpec == "TS 33.203" && r.TargetSection == "6" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected 1 deduplicated ref to TS 33.203#6, got %d: %+v", count, refs)
	}
}

func TestInsertSpecWithSections_BracketedRefs(t *testing.T) {
	d := setupTestDB(t)

	spec := Spec{ID: "TS 99.002", Title: "Test Bracket Spec", Series: "99"}
	sections := []Section{
		{
			SpecID:  "TS 99.002",
			Number:  "2",
			Title:   "References",
			Level:   1,
			Content: "## 2 References\n\n[1]\t3GPP TS 23.501: \"System architecture\"\n[2]\t3GPP TS 33.203: \"Access security\"",
		},
		{
			SpecID:       "TS 99.002",
			Number:       "5",
			Title:        "Procedures",
			Level:        1,
			ParentNumber: "",
			Content:      "## 5 Procedures\nThe procedures in [1] clause 5.1 shall apply.\nSee [2] Annex H for security.",
		},
	}

	err := d.InsertSpecWithSections(spec, sections)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	refs, err := d.GetReferences("TS 99.002", "5", "outgoing", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expect := map[string]string{
		"TS 23.501": "5.1",
		"TS 33.203": "H",
	}
	for _, r := range refs {
		if sec, ok := expect[r.TargetSpec]; ok && r.TargetSection == sec {
			delete(expect, r.TargetSpec)
		}
	}
	if len(expect) > 0 {
		t.Errorf("missing expected bracket references: %v, got refs: %+v", expect, refs)
	}
}

// TestOpen_ReadOnly verifies the public Open constructor returns a working
// handle that can query previously persisted data.
func TestOpen_ReadOnly(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "readonly.db")

	// Seed the database in read-write mode first.
	rw, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	if err := rw.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if err := rw.UpsertSpec(Spec{ID: "TS 23.501", Title: "Arch", Series: "23"}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}
	rw.Close()

	// Re-open through the Open entrypoint and verify queries still work.
	ro, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer ro.Close()

	result, err := ro.ListSpecs("", 0, 0)
	if err != nil {
		t.Fatalf("ListSpecs: %v", err)
	}
	if len(result.Specs) != 1 || result.Specs[0].ID != "TS 23.501" {
		t.Errorf("expected TS 23.501, got %+v", result.Specs)
	}
}

// TestInitSchema_FreshDB covers the public InitSchema entrypoint that the CLI
// commands use, separate from the ExecScript(Schema) shortcut used in tests.
func TestInitSchema_FreshDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "init.db")
	d, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("OpenReadWrite: %v", err)
	}
	defer d.Close()

	if err := d.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	// Calling InitSchema twice should be idempotent.
	if err := d.InitSchema(); err != nil {
		t.Fatalf("InitSchema (second call): %v", err)
	}

	// Verify all expected tables exist by running a probe query on each.
	for _, table := range []string{"specs", "sections", "sections_fts", "images", "openapi_specs", "spec_references"} {
		if err := d.Exec("SELECT 1 FROM " + table + " WHERE 0 = 1"); err != nil {
			t.Errorf("table %q missing or query failed: %v", table, err)
		}
	}
}

// TestExec_DirectSQL covers the exported Exec helper used for ad-hoc writes.
func TestExec_DirectSQL(t *testing.T) {
	d := setupTestDB(t)

	// Insert a new spec via Exec directly.
	if err := d.Exec(
		"INSERT INTO specs (id, title, series) VALUES (?, ?, ?)",
		"TS 99.123", "Exec-inserted", "99",
	); err != nil {
		t.Fatalf("Exec insert: %v", err)
	}

	result, err := d.ListSpecs("99", 0, 0)
	if err != nil {
		t.Fatalf("ListSpecs: %v", err)
	}
	if len(result.Specs) != 1 || result.Specs[0].ID != "TS 99.123" {
		t.Errorf("expected TS 99.123 in series 99, got %+v", result.Specs)
	}

	// An invalid statement should surface an error.
	if err := d.Exec("NOT VALID SQL"); err == nil {
		t.Error("expected error for invalid SQL")
	}
}

// TestUpsertSpec_Replaces verifies that UpsertSpec overwrites an existing row
// with the new fields (INSERT OR REPLACE behavior).
func TestUpsertSpec_Replaces(t *testing.T) {
	d := setupTestDB(t)

	// Existing spec from seed data: TS 23.501 version 18.6.0
	if err := d.UpsertSpec(Spec{
		ID: "TS 23.501", Title: "Updated", Version: "k10", Release: "Rel-20", Series: "23",
	}); err != nil {
		t.Fatalf("UpsertSpec: %v", err)
	}

	result, err := d.ListSpecs("", 0, 0)
	if err != nil {
		t.Fatalf("ListSpecs: %v", err)
	}
	var found *Spec
	for i := range result.Specs {
		if result.Specs[i].ID == "TS 23.501" {
			found = &result.Specs[i]
			break
		}
	}
	if found == nil {
		t.Fatal("TS 23.501 missing after upsert")
	}
	if found.Title != "Updated" || found.Version != "k10" || found.Release != "Rel-20" {
		t.Errorf("upsert did not overwrite fields: %+v", found)
	}
}

// TestUpsertSection_ReplacesContent verifies UpsertSection deletes and
// re-inserts a section, and that the FTS index is updated accordingly.
func TestUpsertSection_ReplacesContent(t *testing.T) {
	d := setupTestDB(t)

	// TS 23.501 section 1 seed content talks about "system architecture".
	// Replace it with completely unrelated content and confirm the FTS row
	// follows suit.
	if err := d.UpsertSection(Section{
		SpecID:  "TS 23.501",
		Number:  "1",
		Title:   "Scope",
		Level:   1,
		Content: "Completely new scope content about zucchini and tomatoes.",
	}); err != nil {
		t.Fatalf("UpsertSection: %v", err)
	}

	sections, err := d.GetSection("TS 23.501", "1", false)
	if err != nil {
		t.Fatalf("GetSection: %v", err)
	}
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if !strings.Contains(sections[0].Content, "zucchini") {
		t.Errorf("content not replaced: %q", sections[0].Content)
	}

	// FTS should pick up the new token.
	results, err := d.Search("zucchini", []string{"TS 23.501"}, 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected FTS hit for zucchini after upsert")
	}
}

// TestImageCRUD exercises the image public API end to end: UpsertImage,
// GetImage, and ListImages.
func TestImageCRUD(t *testing.T) {
	d := setupTestDB(t)

	payload := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0xde, 0xad}
	img := Image{
		SpecID:      "TS 23.501",
		Name:        "fig1.png",
		MIMEType:    "image/png",
		Data:        payload,
		LLMReadable: true,
	}
	if err := d.UpsertImage(img); err != nil {
		t.Fatalf("UpsertImage: %v", err)
	}

	// UpsertImage again should replace cleanly.
	img.MIMEType = "image/png"
	if err := d.UpsertImage(img); err != nil {
		t.Fatalf("UpsertImage (replace): %v", err)
	}

	got, err := d.GetImage("TS 23.501", "fig1.png")
	if err != nil {
		t.Fatalf("GetImage: %v", err)
	}
	if got == nil || got.Name != "fig1.png" || got.MIMEType != "image/png" {
		t.Errorf("GetImage returned %+v", got)
	}
	if !got.LLMReadable {
		t.Error("LLMReadable flag lost")
	}
	if len(got.Data) != len(payload) {
		t.Errorf("image bytes length = %d, want %d", len(got.Data), len(payload))
	}

	// Missing image.
	if _, err := d.GetImage("TS 23.501", "missing.png"); err == nil {
		t.Error("expected error for missing image")
	}

	// ListImages returns only the inserted image.
	infos, err := d.ListImages("TS 23.501")
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if len(infos) != 1 || infos[0].Name != "fig1.png" {
		t.Errorf("ListImages = %+v, want [fig1.png]", infos)
	}

	// Empty spec → empty result.
	infos, err = d.ListImages("TS 00.000")
	if err != nil {
		t.Fatalf("ListImages empty: %v", err)
	}
	if len(infos) != 0 {
		t.Errorf("expected empty list, got %+v", infos)
	}
}

// TestGetBracketMap covers the DB helper that reads the References section and
// returns a [N] -> spec map, plus its nil return for specs without one.
func TestGetBracketMap(t *testing.T) {
	d := setupTestDB(t)

	// Insert a References section into TS 23.501.
	if err := d.UpsertSection(Section{
		SpecID: "TS 23.501", Number: "2", Title: "References", Level: 1,
		Content: "## 2 References\n\n[1]\t3GPP TS 29.510: \"NRF\"\n[2]\t3GPP TR 23.700: \"Study\"",
	}); err != nil {
		t.Fatalf("UpsertSection: %v", err)
	}

	m, err := d.GetBracketMap("TS 23.501")
	if err != nil {
		t.Fatalf("GetBracketMap: %v", err)
	}
	if m["1"] != "TS 29.510" {
		t.Errorf("bracket [1] = %q, want TS 29.510", m["1"])
	}
	if m["2"] != "TR 23.700" {
		t.Errorf("bracket [2] = %q, want TR 23.700", m["2"])
	}

	// TS 29.510 has no References section in seed data → expect nil.
	m2, err := d.GetBracketMap("TS 29.510")
	if err != nil {
		t.Fatalf("GetBracketMap empty: %v", err)
	}
	if m2 != nil {
		t.Errorf("expected nil bracket map for spec without References section, got %+v", m2)
	}
}

// TestInsertSpecWithSections_MultiSectionRefs covers the
// tsMultiPrefixSpecSections and tsMultiSpecSections extractors (both 0%
// covered until now) by inserting content with the multi-section patterns and
// asserting ExtractReferences fanned out one reference per section.
func TestInsertSpecWithSections_MultiSectionRefs(t *testing.T) {
	d := setupTestDB(t)

	spec := Spec{ID: "TS 99.003", Title: "Multi Section Refs Test", Series: "99"}
	sections := []Section{
		{
			SpecID: "TS 99.003", Number: "5", Title: "Procedures", Level: 1,
			Content: "The behavior is described in clauses 8.2 and 16.11 of TS 23.402 for roaming scenarios.\n" +
				"See also TS 29.510 clauses 5.1 and 6.3 for NRF-specific behavior.",
		},
	}
	if err := d.InsertSpecWithSections(spec, sections); err != nil {
		t.Fatalf("InsertSpecWithSections: %v", err)
	}

	refs, err := d.GetReferences("TS 99.003", "5", "outgoing", false)
	if err != nil {
		t.Fatalf("GetReferences: %v", err)
	}

	// Collect (spec, section) pairs.
	got := make(map[string]bool)
	for _, r := range refs {
		got[r.TargetSpec+"#"+r.TargetSection] = true
	}

	// tsMultiPrefixRefRE should have produced both sections of TS 23.402.
	for _, want := range []string{"TS 23.402#8.2", "TS 23.402#16.11"} {
		if !got[want] {
			t.Errorf("missing multi-prefix ref %s; got: %+v", want, refs)
		}
	}
	// tsMultiRefRE should have produced both sections of TS 29.510.
	for _, want := range []string{"TS 29.510#5.1", "TS 29.510#6.3"} {
		if !got[want] {
			t.Errorf("missing multi-ref %s; got: %+v", want, refs)
		}
	}
}
