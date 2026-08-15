package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/internal/asn1index"
	"github.com/higebu/3gpp-mcp/internal/converter/pipeline"
	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/openapiindex"
	"github.com/higebu/3gpp-mcp/internal/testutil"
	"github.com/higebu/3gpp-mcp/internal/tools"
	"github.com/higebu/3gpp-mcp/internal/versionstore"
)

func TestRunListSpecs(t *testing.T) {
	d := testutil.SetupTestDB(t)
	var out bytes.Buffer
	if err := runListSpecs(t.Context(), &out, d, "", "", 0, 0); err != nil {
		t.Fatalf("runListSpecs: %v", err)
	}
	var result db.ListSpecsResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if result.TotalCount != 3 {
		t.Errorf("expected 3 specs, got %d", result.TotalCount)
	}

	out.Reset()
	if err := runListSpecs(t.Context(), &out, d, "29", "", 0, 0); err != nil {
		t.Fatalf("runListSpecs series filter: %v", err)
	}
	if !strings.Contains(out.String(), "TS 29.510") || strings.Contains(out.String(), "TS 23.501") {
		t.Errorf("series filter not applied:\n%s", out.String())
	}

	// A negative limit is reserved for internal callers and clamps to the
	// default rather than dumping the whole table.
	out.Reset()
	if err := runListSpecs(t.Context(), &out, d, "", "", -1, 0); err != nil {
		t.Fatalf("runListSpecs negative limit: %v", err)
	}
	if !strings.Contains(out.String(), `"limit": 20`) {
		t.Errorf("expected default limit for a negative input, got:\n%s", out.String())
	}
}

func TestRunSearch(t *testing.T) {
	d := testutil.SetupTestDB(t)
	var out bytes.Buffer
	if err := runSearch(t.Context(), &out, d, "architecture", "", 0, 0); err != nil {
		t.Fatalf("runSearch: %v", err)
	}
	var results db.SearchResults
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if results.TotalCount == 0 {
		t.Error("expected search hits for 'architecture'")
	}

	// The comma-separated spec filter must narrow the search.
	out.Reset()
	if err := runSearch(t.Context(), &out, d, "Scope", "TS 29.510, ", 0, 0); err != nil {
		t.Fatalf("runSearch with spec filter: %v", err)
	}
	if strings.Contains(out.String(), "TS 23.501") {
		t.Errorf("spec filter not applied:\n%s", out.String())
	}
}

func TestRunGetTOC(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	var out, errOut bytes.Buffer
	if err := runGetTOC(t.Context(), &out, &errOut, src, "TS 23.501", ""); err != nil {
		t.Fatalf("runGetTOC: %v", err)
	}
	for _, want := range []string{"Table of Contents", "5.1.1 Overview", "TS 23.501 v18.6.0"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out.String())
		}
	}

	if err := runGetTOC(t.Context(), &out, &errOut, src, "TS 99.999", ""); err == nil {
		t.Error("expected error for unknown spec")
	}
}

func TestRunGetSection(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	var out, errOut bytes.Buffer
	if err := runGetSection(t.Context(), &out, &errOut, src, "TS 23.501", "5.1", "", false); err != nil {
		t.Fatalf("runGetSection: %v", err)
	}
	if !strings.Contains(out.String(), "[Source: TS 23.501 v18.6.0 (Rel-18) — Section 5.1]") {
		t.Errorf("missing provenance header:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "General architecture description") {
		t.Errorf("missing section content:\n%s", out.String())
	}
	if strings.Contains(out.String(), "Overview of the architecture") {
		t.Errorf("subsection content leaked without -subsections:\n%s", out.String())
	}

	out.Reset()
	if err := runGetSection(t.Context(), &out, &errOut, src, "TS 23.501", "5.1", "", true); err != nil {
		t.Fatalf("runGetSection subsections: %v", err)
	}
	if !strings.Contains(out.String(), "Overview of the architecture") {
		t.Errorf("expected subsection content with -subsections:\n%s", out.String())
	}

	if err := runGetSection(t.Context(), &out, &errOut, src, "TS 23.501", "9.9", "", false); err == nil {
		t.Error("expected error for missing section")
	}
}

func TestRunGetASN1(t *testing.T) {
	d := testutil.SetupTestDB(t)
	if err := d.Exec(`INSERT INTO specs (id, version, version_token, title, release, series) VALUES
		('TS 38.413', '18.6.0', 'i60', 'NG Application Protocol (NGAP)', '18', '38')`); err != nil {
		t.Fatalf("insert spec: %v", err)
	}
	if err := d.Exec(`INSERT INTO sections (spec_id, version, number, title, level, parent_number, content)
		VALUES ('TS 38.413', '18.6.0', '9.4.5', 'Information Element definitions', 2, NULL, ?)`,
		"```asn1\n-- ASN1START\nAMF-UE-NGAP-ID ::= INTEGER (0..1099511627775)\n\nCause ::= CHOICE {\n\tmisc\tCauseMisc\n}\n-- ASN1STOP\n```"); err != nil {
		t.Fatalf("insert section: %v", err)
	}
	if _, err := asn1index.Rebuild(t.Context(), d); err != nil {
		t.Fatalf("rebuild asn1 index: %v", err)
	}
	src := tools.NewSource(d)

	var out, errOut bytes.Buffer
	if err := runGetASN1(t.Context(), &out, &errOut, src, "TS 38.413", "AMF-UE-NGAP-ID", ""); err != nil {
		t.Fatalf("runGetASN1: %v", err)
	}
	if !strings.Contains(out.String(), "[Source: TS 38.413 v18.6.0 (Rel-18) — Section 9.4.5 — AMF-UE-NGAP-ID]") {
		t.Errorf("missing provenance header:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "AMF-UE-NGAP-ID ::= INTEGER (0..1099511627775)") {
		t.Errorf("missing definition:\n%s", out.String())
	}

	out.Reset()
	if err := runGetASN1(t.Context(), &out, &errOut, src, "TS 38.413", "", ""); err != nil {
		t.Fatalf("runGetASN1 listing: %v", err)
	}
	if !strings.Contains(out.String(), "2 ASN.1 assignments") || !strings.Contains(out.String(), "Cause") {
		t.Errorf("unexpected listing:\n%s", out.String())
	}

	if err := runGetASN1(t.Context(), &out, &errOut, src, "TS 38.413", "CauseMisc", ""); err == nil ||
		!strings.Contains(err.Error(), "similar names: Cause") {
		t.Errorf("expected not-found error with suggestions, got: %v", err)
	}

	// Corpus-wide lookup: name only, no spec-id.
	out.Reset()
	if err := runGetASN1(t.Context(), &out, &errOut, src, "", "AMF-UE-NGAP-ID", ""); err != nil {
		t.Fatalf("runGetASN1 corpus lookup: %v", err)
	}
	if !strings.Contains(out.String(), "AMF-UE-NGAP-ID ::= INTEGER") ||
		!strings.Contains(out.String(), "[Source: TS 38.413") {
		t.Errorf("corpus lookup output:\n%s", out.String())
	}

	if err := runGetASN1(t.Context(), &out, &errOut, src, "TS 23.501", "", ""); err == nil ||
		!strings.Contains(err.Error(), "no ASN.1 definitions") {
		t.Errorf("expected no-ASN.1 error, got: %v", err)
	}

	// Corpus miss with suggestions, and the wrong-spec hint on a per-spec
	// miss whose spec the index does not cover.
	if err := runGetASN1(t.Context(), &out, &errOut, src, "", "CauseMisc", ""); err == nil ||
		!strings.Contains(err.Error(), "similar names: Cause") {
		t.Errorf("expected corpus suggestions, got: %v", err)
	}

	// Without the index, the corpus mode names the rebuild command and the
	// per-spec path falls back to reading the document.
	if err := d.Exec("DROP TABLE asn1_defs"); err != nil {
		t.Fatalf("drop index: %v", err)
	}
	if err := runGetASN1(t.Context(), &out, &errOut, src, "", "AMF-UE-NGAP-ID", ""); err == nil ||
		!strings.Contains(err.Error(), "build-asn1-index") {
		t.Errorf("expected missing-index error, got: %v", err)
	}
	out.Reset()
	if err := runGetASN1(t.Context(), &out, &errOut, src, "TS 38.413", "AMF-UE-NGAP-ID", ""); err != nil {
		t.Fatalf("per-spec fallback without index: %v", err)
	}
	if !strings.Contains(out.String(), "AMF-UE-NGAP-ID ::= INTEGER") {
		t.Errorf("fallback output:\n%s", out.String())
	}
}

func TestRefreshASN1IndexAfterImportFailure(t *testing.T) {
	// A rebuild that fails after an import must drop the index rather than
	// leave it describing the pre-import corpus: the database then reports
	// the index as missing, which is the state serve already handles.
	d := testutil.SetupTestDB(t)
	if err := d.Exec(`INSERT INTO asn1_defs (name, key, spec_id, version, section_number, section_title, body)
		VALUES ('Stale', 'stale', 'TS 23.501', '18.6.0', '1', 't', 'Stale ::= INTEGER')`); err != nil {
		t.Fatalf("seed stale def: %v", err)
	}
	// Dropping the FTS table makes ASN1Sections — and so the rebuild — fail.
	for _, stmt := range []string{
		"DROP TRIGGER sections_ai", "DROP TRIGGER sections_ad", "DROP TRIGGER sections_au",
		"DROP TABLE sections_fts",
	} {
		if err := d.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}

	err := refreshASN1IndexAfterImport(t.Context(), d)
	if err == nil || !strings.Contains(err.Error(), "build-asn1-index") {
		t.Fatalf("expected drop-and-point error, got: %v", err)
	}
	if ok, hasErr := d.HasASN1Index(t.Context()); hasErr != nil || ok {
		t.Errorf("stale index survived a failed rebuild: ok=%v err=%v", ok, hasErr)
	}
}

func TestRunBuildASN1Index(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := d.InitSchema(); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := d.Exec(`INSERT INTO specs (id, version, version_token, title, release, series) VALUES
		('TS 38.413', '18.6.0', 'i60', 'NGAP', '18', '38')`); err != nil {
		t.Fatalf("insert spec: %v", err)
	}
	if err := d.Exec(`INSERT INTO sections (spec_id, version, number, title, level, parent_number, content)
		VALUES ('TS 38.413', '18.6.0', '9.4.5', 'IEs', 2, NULL, ?)`,
		"```asn1\n-- ASN1START\nAMF-UE-NGAP-ID ::= INTEGER (0..1099511627775)\n-- ASN1STOP\n```"); err != nil {
		t.Fatalf("insert section: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := runBuildASN1Index(t.Context(), dbPath); err != nil {
		t.Fatalf("runBuildASN1Index: %v", err)
	}

	ro, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer ro.Close()
	defs, err := ro.LookupASN1(t.Context(), "AMF-UE-NGAP-ID", "amfuengapid", "")
	if err != nil || len(defs) != 1 {
		t.Fatalf("index not built: defs=%+v err=%v", defs, err)
	}
}

func TestGetASN1SpecIDArg(t *testing.T) {
	for arg, want := range map[string]bool{
		"TS 38.331":                    true,
		"tr 21.905":                    true,
		"38.331":                       true,
		"AMF-UE-NGAP-ID":               false,
		"maxNrofCellMeas":              false,
		"KlobucharModel2Parameter-r16": false,
	} {
		if got := specIDArg.MatchString(arg); got != want {
			t.Errorf("specIDArg(%q) = %v, want %v", arg, got, want)
		}
	}
}

func TestRunCompareVersions_SameVersion(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	var out, errOut bytes.Buffer
	// -old resolves in the database and -new defaults to the database
	// version, so both sides land on 18.6.0: an informational answer.
	err := runCompareVersions(t.Context(), &out, &errOut, src, "TS 23.501", "18.6.0", "", "", false, 0)
	if err != nil {
		t.Fatalf("runCompareVersions: %v", err)
	}
	if !strings.Contains(out.String(), "nothing to compare") {
		t.Errorf("expected same-version notice, got:\n%s", out.String())
	}
}

// seedSecondVersion inserts a second version of TS 23.501 directly. The build
// pipeline keeps one version per spec, but resolve serves any version the
// specs table has, so this exercises the comparison paths without network.
func seedSecondVersion(t *testing.T, d *db.DB) {
	t.Helper()
	if err := d.ExecScript(`
INSERT INTO specs (id, version, version_token, title, release, series) VALUES
    ('TS 23.501', '18.7.0', 'i70', 'System architecture for the 5G System (5GS)', '18', '23');
INSERT INTO sections (spec_id, version, number, title, level, parent_number, content) VALUES
    ('TS 23.501', '18.7.0', '1', 'Scope', 1, NULL, '# 1 Scope
This document defines the system architecture.'),
    ('TS 23.501', '18.7.0', '5', 'Architecture', 1, NULL, '# 5 Architecture
The 5G system architecture is defined here, with amendments.'),
    ('TS 23.501', '18.7.0', '5.2', 'Additions', 2, '5', '## 5.2 Additions
A section that only the newer version has.');
`); err != nil {
		t.Fatalf("seed second version: %v", err)
	}
}

func TestRunCompareVersions_Structural(t *testing.T) {
	d := testutil.SetupTestDB(t)
	seedSecondVersion(t, d)
	src := tools.NewSource(d)
	var out, errOut bytes.Buffer
	err := runCompareVersions(t.Context(), &out, &errOut, src, "TS 23.501", "18.6.0", "18.7.0", "", false, 0)
	if err != nil {
		t.Fatalf("runCompareVersions: %v", err)
	}
	for _, want := range []string{"[Compare: TS 23.501", "Structural changes", "5.2 Additions"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out.String())
		}
	}
}

func TestRunCompareVersions_SectionDiff(t *testing.T) {
	d := testutil.SetupTestDB(t)
	seedSecondVersion(t, d)
	src := tools.NewSource(d)

	// Changed content renders as a unified diff.
	var out, errOut bytes.Buffer
	err := runCompareVersions(t.Context(), &out, &errOut, src, "TS 23.501", "18.6.0", "18.7.0", "5", false, 0)
	if err != nil {
		t.Fatalf("runCompareVersions section: %v", err)
	}
	for _, want := range []string{"Section 5", "with amendments"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("expected diff output to contain %q, got:\n%s", want, out.String())
		}
	}

	// Identical content is an informational answer.
	out.Reset()
	if err := runCompareVersions(t.Context(), &out, &errOut, src, "TS 23.501", "18.6.0", "18.7.0", "1", false, 0); err != nil {
		t.Fatalf("runCompareVersions identical section: %v", err)
	}
	if !strings.Contains(out.String(), "identical between") {
		t.Errorf("expected identical notice, got:\n%s", out.String())
	}

	// A section on one side only is an informational answer, not a failure.
	out.Reset()
	if err := runCompareVersions(t.Context(), &out, &errOut, src, "TS 23.501", "18.6.0", "18.7.0", "5.2", false, 0); err != nil {
		t.Fatalf("runCompareVersions one-sided section: %v", err)
	}
	if !strings.Contains(out.String(), "does not exist in") {
		t.Errorf("expected one-sided notice, got:\n%s", out.String())
	}

	// A section in neither version is an error.
	if err := runCompareVersions(t.Context(), &out, &errOut, src, "TS 23.501", "18.6.0", "18.7.0", "9.9", false, 0); err == nil {
		t.Error("expected error for a section absent from both versions")
	}
}

func TestRunCompareVersions_UnavailableVersion(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d) // no Store: archived versions cannot be fetched
	var out, errOut bytes.Buffer
	err := runCompareVersions(t.Context(), &out, &errOut, src, "TS 23.501", "17.9.0", "", "", false, 0)
	if err == nil {
		t.Fatal("expected error for a version that cannot be fetched")
	}
	var unavailable *tools.VersionUnavailableError
	if !errors.As(err, &unavailable) {
		t.Errorf("expected VersionUnavailableError, got: %v", err)
	}
}

func TestRunGetReferences(t *testing.T) {
	d := testutil.SetupTestDB(t)
	var out bytes.Buffer
	if err := runGetReferences(t.Context(), &out, d, "TS 24.229", "5.1", db.DirectionOutgoing, false); err != nil {
		t.Fatalf("runGetReferences: %v", err)
	}
	var refs []db.Reference
	if err := json.Unmarshal(out.Bytes(), &refs); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(refs) != 5 {
		t.Errorf("expected 5 outgoing references, got %d", len(refs))
	}

	out.Reset()
	if err := runGetReferences(t.Context(), &out, d, "TS 33.203", "", db.DirectionIncoming, false); err != nil {
		t.Fatalf("runGetReferences incoming: %v", err)
	}
	refs = nil
	if err := json.Unmarshal(out.Bytes(), &refs); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(refs) != 2 {
		t.Errorf("expected 2 incoming references, got %d", len(refs))
	}

	// Outgoing without a section number cannot be answered.
	if err := runGetReferences(t.Context(), &out, d, "TS 24.229", "", db.DirectionOutgoing, false); err == nil {
		t.Error("expected error for outgoing direction without section number")
	}

	// No matches must still print valid JSON.
	out.Reset()
	if err := runGetReferences(t.Context(), &out, d, "TS 99.999", "", db.DirectionIncoming, false); err != nil {
		t.Fatalf("runGetReferences no matches: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("expected [] for no matches, got:\n%s", out.String())
	}
}

func TestRunListOpenAPI(t *testing.T) {
	d := testutil.SetupTestDB(t)
	var out bytes.Buffer
	if err := runListOpenAPI(t.Context(), &out, d, ""); err != nil {
		t.Fatalf("runListOpenAPI: %v", err)
	}
	var specs []db.OpenAPISpec
	if err := json.Unmarshal(out.Bytes(), &specs); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(specs) != 1 || specs[0].APIName != "Nnrf_NFManagement" {
		t.Errorf("unexpected listing: %+v", specs)
	}

	out.Reset()
	if err := runListOpenAPI(t.Context(), &out, d, "TS 23.501"); err != nil {
		t.Fatalf("runListOpenAPI filtered: %v", err)
	}
	if strings.TrimSpace(out.String()) != "[]" {
		t.Errorf("expected [] for a spec without OpenAPI, got:\n%s", out.String())
	}
}

func TestRunSearchOpenAPI(t *testing.T) {
	d := testutil.SetupTestDB(t)
	if _, err := openapiindex.Build(t.Context(), d); err != nil {
		t.Fatalf("build openapi index: %v", err)
	}

	var out bytes.Buffer
	if err := runSearchOpenAPI(t.Context(), &out, d, "NFProfile", "", "", "", false, 0, 0); err != nil {
		t.Fatalf("runSearchOpenAPI: %v", err)
	}
	var results db.OpenAPISearchResults
	if err := json.Unmarshal(out.Bytes(), &results); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(results.Results) == 0 || results.Results[0].Name != "NFProfile" {
		t.Fatalf("unexpected results: %+v", results.Results)
	}
	if results.Results[0].Body != "" {
		t.Errorf("body should be omitted without -body: %q", results.Results[0].Body)
	}

	out.Reset()
	if err := runSearchOpenAPI(t.Context(), &out, d, "NFProfile", "TS 23.501, TS 29.510", "", "schema", true, 0, 0); err != nil {
		t.Fatalf("runSearchOpenAPI filtered: %v", err)
	}
	if !strings.Contains(out.String(), "nfInstanceId") {
		t.Errorf("-body should return the full chunk:\n%s", out.String())
	}

	out.Reset()
	if err := runSearchOpenAPI(t.Context(), &out, d, "NFProfile", "TS 23.501", "", "", false, 0, 0); err != nil {
		t.Fatalf("runSearchOpenAPI spec filter: %v", err)
	}
	if !strings.Contains(out.String(), `"total_count": 0`) {
		t.Errorf("spec filter not applied:\n%s", out.String())
	}
}

// TestRunSearchOpenAPIWithoutIndex covers a database built before
// search_openapi existed: the CLI names the command that fixes it.
func TestRunSearchOpenAPIWithoutIndex(t *testing.T) {
	d := testutil.SetupTestDB(t)
	if err := d.Exec("DROP TABLE openapi_chunks"); err != nil {
		t.Fatalf("drop index: %v", err)
	}

	var out bytes.Buffer
	err := runSearchOpenAPI(t.Context(), &out, d, "NFProfile", "", "", "", false, 0, 0)
	if err == nil || !strings.Contains(err.Error(), "build-openapi-index") {
		t.Fatalf("err = %v, want a build-openapi-index hint", err)
	}
}

// cancelledContext is the most likely way a rebuild fails in practice: a
// Ctrl-C partway through.
func cancelledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

// TestRefreshOpenAPIIndexAfterImportDropsStaleIndex covers the guarantee update
// relies on: after openapi_specs has been rewritten, a rebuild that cannot
// finish leaves no index at all, so search_openapi reports it as missing
// instead of answering from the corpus as it was before.
func TestRefreshOpenAPIIndexAfterImportDropsStaleIndex(t *testing.T) {
	path := seedDBPath(t)
	d, err := db.OpenReadWrite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	err = refreshOpenAPIIndexAfterImport(cancelledContext(t), d)
	if err == nil {
		t.Fatal("expected the rebuild to fail")
	}
	if !strings.Contains(err.Error(), "build-openapi-index") {
		t.Errorf("error should name the recovery: %v", err)
	}
	// The check has to run on a live context: update makes the same call, and
	// reusing the cancelled one would report a failure of its own.
	if ok, checkErr := d.HasOpenAPIIndex(t.Context()); checkErr != nil || ok {
		t.Errorf("stale index survived a failed refresh: %v, %v", ok, checkErr)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// And the database is recoverable exactly the way the error says.
	out := captureStdout(t, func() {
		if err := runBuildOpenAPIIndex(t.Context(), path); err != nil {
			t.Fatalf("runBuildOpenAPIIndex: %v", err)
		}
	})
	if !strings.Contains(out, "OpenAPI index:") {
		t.Errorf("unexpected output: %s", out)
	}
}

// TestRebuildOpenAPIIndexKeepsIndexOnFailure is the other half: when nothing
// has rewritten openapi_specs, a failed rebuild changes nothing and the index
// still describes the corpus it was built from. Dropping it there would turn a
// Ctrl-C on build-openapi-index into an outage for a running serve.
func TestRebuildOpenAPIIndexKeepsIndexOnFailure(t *testing.T) {
	path := seedDBPath(t)
	d, err := db.OpenReadWrite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })

	if err := rebuildOpenAPIIndex(cancelledContext(t), d); err == nil {
		t.Fatal("expected the rebuild to fail")
	}

	if ok, checkErr := d.HasOpenAPIIndex(t.Context()); checkErr != nil || !ok {
		t.Fatalf("HasOpenAPIIndex = %v, %v; want true, nil", ok, checkErr)
	}
	got, err := d.SearchOpenAPI(t.Context(), "NFProfile", nil, "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("SearchOpenAPI: %v", err)
	}
	if len(got.Results) == 0 {
		t.Error("a failed rebuild emptied an index that was still correct")
	}
}

// TestCmdBuildOpenAPIIndex drives the command through its flag parsing, the
// way a shell invocation would.
func TestCmdBuildOpenAPIIndex(t *testing.T) {
	path := seedDBPath(t)
	out := captureStdout(t, func() { cmdBuildOpenAPIIndex([]string{"-db", path}) })
	if !strings.Contains(out, "OpenAPI index:") {
		t.Errorf("unexpected output: %s", out)
	}
}

func TestRunBuildOpenAPIIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	d, err := db.OpenReadWrite(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// A database with the spec tables but no index, as an older build left it.
	if err := d.ExecScript(db.SpecTablesSchema + db.ImagesTableSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}
	if err := d.Exec(`CREATE TABLE openapi_specs (
		id INTEGER PRIMARY KEY AUTOINCREMENT, spec_id TEXT NOT NULL, api_name TEXT NOT NULL,
		version TEXT, filename TEXT, content TEXT NOT NULL, UNIQUE(spec_id, api_name))`); err != nil {
		t.Fatalf("openapi_specs: %v", err)
	}
	if err := d.UpsertOpenAPI("TS 29.510", "Nnrf_NFManagement", "v1.3.0", "TS29510_Nnrf_NFManagement.yaml",
		"components:\n  schemas:\n    NFProfile:\n      type: object\n"); err != nil {
		t.Fatalf("seed openapi: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	out := captureStdout(t, func() {
		if err := runBuildOpenAPIIndex(t.Context(), path); err != nil {
			t.Fatalf("runBuildOpenAPIIndex: %v", err)
		}
	})
	if !strings.Contains(out, "OpenAPI index: 1 chunks") {
		t.Errorf("unexpected output: %s", out)
	}

	reopened, err := db.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()
	results, err := reopened.SearchOpenAPI(t.Context(), "NFProfile", nil, "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("SearchOpenAPI: %v", err)
	}
	if len(results.Results) != 1 {
		t.Errorf("index not usable after rebuild: %+v", results)
	}
}

func TestRunGetOpenAPI(t *testing.T) {
	d := testutil.SetupTestDB(t)
	var out bytes.Buffer
	if err := runGetOpenAPI(t.Context(), &out, d, "TS 29.510", "Nnrf_NFManagement", "", ""); err != nil {
		t.Fatalf("runGetOpenAPI: %v", err)
	}
	if !strings.Contains(out.String(), "openapi: 3.0.0") {
		t.Errorf("expected full YAML output, got:\n%s", out.String())
	}

	out.Reset()
	if err := runGetOpenAPI(t.Context(), &out, d, "TS 29.510", "Nnrf_NFManagement", "", "NFProfile"); err != nil {
		t.Fatalf("runGetOpenAPI schema filter: %v", err)
	}
	if !strings.Contains(out.String(), "NFProfile") || strings.Contains(out.String(), "/nf-instances") {
		t.Errorf("schema filter not applied:\n%s", out.String())
	}

	out.Reset()
	if err := runGetOpenAPI(t.Context(), &out, d, "TS 29.510", "Nnrf_NFManagement", "/nf-instances", ""); err != nil {
		t.Fatalf("runGetOpenAPI path filter: %v", err)
	}
	if !strings.Contains(out.String(), "/nf-instances") {
		t.Errorf("path filter not applied:\n%s", out.String())
	}

	if err := runGetOpenAPI(t.Context(), &out, d, "TS 29.510", "Nope_API", "", ""); err == nil {
		t.Error("expected error for unknown API name")
	}
}

// seedTestImage inserts one PNG image row for TS 23.501.
func seedTestImage(t *testing.T, d *db.DB) []byte {
	t.Helper()
	data := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}
	err := d.Exec(
		"INSERT INTO images (spec_id, version, name, mime_type, data, llm_readable) VALUES (?, ?, ?, ?, ?, 1)",
		"TS 23.501", "18.6.0", "figure1.png", "image/png", data,
	)
	if err != nil {
		t.Fatalf("seed image: %v", err)
	}
	return data
}

func TestRunListImages(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	var out, errOut bytes.Buffer

	// No images must still print valid JSON with a zero count.
	if err := runListImages(t.Context(), &out, &errOut, src, "TS 23.501", ""); err != nil {
		t.Fatalf("runListImages empty: %v", err)
	}
	var listing struct {
		Images []db.ImageInfo `json:"images"`
		Count  int            `json:"count"`
	}
	if err := json.Unmarshal(out.Bytes(), &listing); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if listing.Count != 0 || listing.Images == nil {
		t.Errorf("expected empty listing with non-null images array, got:\n%s", out.String())
	}

	seedTestImage(t, d)
	out.Reset()
	if err := runListImages(t.Context(), &out, &errOut, src, "TS 23.501", ""); err != nil {
		t.Fatalf("runListImages: %v", err)
	}
	if err := json.Unmarshal(out.Bytes(), &listing); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if listing.Count != 1 || listing.Images[0].Name != "figure1.png" {
		t.Errorf("unexpected listing:\n%s", out.String())
	}
}

func TestRunGetImage(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	data := seedTestImage(t, d)

	var out, errOut bytes.Buffer
	if err := runGetImage(t.Context(), &out, &errOut, src, "TS 23.501", "figure1.png", "", ""); err != nil {
		t.Fatalf("runGetImage: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Errorf("stdout bytes differ from stored image")
	}
	if !strings.Contains(errOut.String(), "image/png") {
		t.Errorf("expected MIME type on stderr, got: %s", errOut.String())
	}

	outFile := filepath.Join(t.TempDir(), "img.png")
	if err := runGetImage(t.Context(), &out, &errOut, src, "TS 23.501", "figure1.png", "", outFile); err != nil {
		t.Fatalf("runGetImage -o: %v", err)
	}
	written, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !bytes.Equal(written, data) {
		t.Errorf("file bytes differ from stored image")
	}

	if err := runGetImage(t.Context(), &out, &errOut, src, "TS 23.501", "nope.png", "", ""); err == nil {
		t.Error("expected error for missing image")
	}
}

// errorRoundTripper fails every request, so the archive listing errors without
// touching the network.
type errorRoundTripper struct{}

func (errorRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("no network in tests")
}

func TestRunListVersions_ArchiveUnreachable(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	src.UseCache = false
	src.Client = &http.Client{Transport: errorRoundTripper{}}

	var out, errOut bytes.Buffer
	if err := runListVersions(t.Context(), &out, &errOut, src, "TS 23.501"); err != nil {
		t.Fatalf("runListVersions: %v", err)
	}
	var listing tools.ListVersionsOutput
	if err := json.Unmarshal(out.Bytes(), &listing); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(listing.Versions) != 1 || listing.Versions[0].Version != "18.6.0" {
		t.Errorf("expected the database version, got:\n%s", out.String())
	}
	if listing.Versions[0].Availability != tools.AvailabilityDatabase {
		t.Errorf("expected database availability, got %q", listing.Versions[0].Availability)
	}
	// The incomplete listing must be flagged on stderr, not silently returned.
	if !strings.Contains(errOut.String(), "WARNING") {
		t.Errorf("expected archive warning on stderr, got: %s", errOut.String())
	}

	// A spec with no versions anywhere reports the archive failure.
	if err := runListVersions(t.Context(), &out, &errOut, src, "TS 99.999"); err == nil {
		t.Error("expected error for a spec with no versions")
	}
}

// TestCompareSides_CacheThrash covers the eviction-livelock guard: when two
// archived versions evict each other from a cache too small to hold both,
// each poll would re-download the side the previous poll evicted, forever.
func TestCompareSides_CacheThrash(t *testing.T) {
	orig := fetchPollInterval
	fetchPollInterval = time.Millisecond
	defer func() { fetchPollInterval = orig }()

	inProgress := &tools.FetchInProgressError{SpecID: "TS 23.501", Version: "17.9.0"}
	round := 0
	var errOut bytes.Buffer
	// Round 1: old ready, new fetching. Round 2: fetching new evicted old.
	err := compareSides(t.Context(), &errOut, func() (error, error) {
		round++
		if round == 1 {
			return nil, inProgress
		}
		return inProgress, nil
	})
	if err == nil || !strings.Contains(err.Error(), "cannot hold both versions") {
		t.Errorf("expected cache-thrash error, got: %v", err)
	}

	// A side that simply finishes while the other is still fetching must not
	// trip the guard.
	round = 0
	err = compareSides(t.Context(), &errOut, func() (error, error) {
		round++
		if round < 3 {
			return nil, inProgress
		}
		return nil, nil
	})
	if err != nil {
		t.Errorf("expected success once both sides are ready, got: %v", err)
	}
}

// TestVersionCacheExists covers the guard that keeps list-versions from
// creating a cache that is not already on disk.
func TestVersionCacheExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "versions.db")
	qf := &queryFlags{versionCache: path}
	if qf.versionCacheExists() {
		t.Error("expected false for a missing cache file")
	}
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if !qf.versionCacheExists() {
		t.Error("expected true once the cache file exists")
	}
}

func TestWaitForFetch(t *testing.T) {
	orig := fetchPollInterval
	fetchPollInterval = time.Millisecond
	defer func() { fetchPollInterval = orig }()

	calls := 0
	var errOut bytes.Buffer
	err := waitForFetch(t.Context(), &errOut, func() error {
		calls++
		if calls < 3 {
			return &tools.FetchInProgressError{SpecID: "TS 23.501", Version: "17.9.0"}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("waitForFetch: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts, got %d", calls)
	}
	// The progress note is announced once, not on every poll.
	if got := strings.Count(errOut.String(), "Downloading"); got != 1 {
		t.Errorf("expected one announcement, got %d: %s", got, errOut.String())
	}

	// An image fetch names the images, not the spec text.
	errOut.Reset()
	calls = 0
	err = waitForFetch(t.Context(), &errOut, func() error {
		calls++
		if calls == 1 {
			return &tools.FetchInProgressError{SpecID: "TS 23.501", Version: "17.9.0", Images: true}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("waitForFetch images: %v", err)
	}
	if !strings.Contains(errOut.String(), "images for TS 23.501") {
		t.Errorf("expected images announcement, got: %s", errOut.String())
	}

	// A terminal error passes through unchanged.
	sentinel := errors.New("boom")
	if err := waitForFetch(t.Context(), &errOut, func() error { return sentinel }); !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}

	// Cancellation stops the polling.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err = waitForFetch(ctx, &errOut, func() error {
		return &tools.FetchInProgressError{SpecID: "TS 23.501", Version: "17.9.0"}
	})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
}

// seedDBPath writes the standard schema and seed data to a closed database
// file, so cmd-level tests can open it by path the way a user would.
func seedDBPath(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.OpenReadWrite(path)
	if err != nil {
		t.Fatalf("open seed db: %v", err)
	}
	if err := d.ExecScript(db.Schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if err := d.ExecScript(testutil.SeedData); err != nil {
		t.Fatalf("seed data: %v", err)
	}
	// A real database gets its OpenAPI index built at the end of every import,
	// so the seed does too.
	if _, err := openapiindex.Build(t.Context(), d); err != nil {
		t.Fatalf("build openapi index: %v", err)
	}
	// Same for the ASN.1 name index, seeded with one small module.
	if err := d.Exec(`INSERT INTO specs (id, version, version_token, title, release, series) VALUES
		('TS 38.413', '18.6.0', 'i60', 'NGAP', '18', '38')`); err != nil {
		t.Fatalf("insert asn1 spec: %v", err)
	}
	if err := d.Exec(`INSERT INTO sections (spec_id, version, number, title, level, parent_number, content)
		VALUES ('TS 38.413', '18.6.0', '9.4.5', 'IE definitions', 2, NULL, ?)`,
		"```asn1\n-- ASN1START\nAMF-UE-NGAP-ID ::= INTEGER (0..1099511627775)\n-- ASN1STOP\n```"); err != nil {
		t.Fatalf("insert asn1 section: %v", err)
	}
	if _, err := asn1index.Rebuild(t.Context(), d); err != nil {
		t.Fatalf("build asn1 index: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}
	return path
}

// TestCmdQueryCommands drives each query command through its full cmd layer —
// flag parsing, source setup, output — the way a shell invocation would.
func TestCmdQueryCommands(t *testing.T) {
	path := seedDBPath(t)
	// Keep any cache access inside the test's sandbox.
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	tests := []struct {
		name string
		run  func()
		want []string
	}{
		{"list-specs", func() { cmdListSpecs([]string{"-db", path, "-series", "23"}) }, []string{"TS 23.501", "total_count"}},
		{"search", func() { cmdSearch([]string{"-db", path, "-limit", "2", "architecture"}) }, []string{"results", "total_count"}},
		{"search multi-arg", func() { cmdSearch([]string{"-db", path, "architecture", "AND", "5G"}) }, []string{"total_count"}},
		{"get-toc", func() { cmdGetTOC([]string{"-db", path, "TS 23.501"}) }, []string{"Table of Contents", "5.1.1 Overview"}},
		{"get-section", func() { cmdGetSection([]string{"-db", path, "-subsections", "TS 23.501", "5.1"}) }, []string{"[Source: TS 23.501", "Overview of the architecture"}},
		{"compare-versions", func() {
			cmdCompareVersions([]string{"-db", path, "-no-fetch", "-old", "18.6.0", "TS 23.501"})
		}, []string{"nothing to compare"}},
		{"get-references", func() { cmdGetReferences([]string{"-db", path, "TS 24.229", "5.1"}) }, []string{"TS 23.228"}},
		{"list-openapi", func() { cmdListOpenAPI([]string{"-db", path}) }, []string{"Nnrf_NFManagement"}},
		{"get-openapi", func() { cmdGetOpenAPI([]string{"-db", path, "-schema", "NFProfile", "TS 29.510", "Nnrf_NFManagement"}) }, []string{"NFProfile"}},
		{"search-openapi", func() { cmdSearchOpenAPI([]string{"-db", path, "-limit", "2", "NFProfile"}) }, []string{"NFProfile", "total_count"}},
		{"search-openapi multi-arg", func() { cmdSearchOpenAPI([]string{"-db", path, "-kind", "operation", "nf-instances"}) }, []string{`"operation"`}},
		{"list-images", func() { cmdListImages([]string{"-db", path, "TS 23.501"}) }, []string{`"count"`}},
		{"get-asn1 lookup", func() { cmdGetASN1([]string{"-db", path, "TS 38.413", "AMF-UE-NGAP-ID"}) }, []string{"[Source: TS 38.413", "AMF-UE-NGAP-ID ::= INTEGER"}},
		{"get-asn1 corpus", func() { cmdGetASN1([]string{"-db", path, "AMF-UE-NGAP-ID"}) }, []string{"AMF-UE-NGAP-ID ::= INTEGER"}},
		{"get-asn1 listing", func() { cmdGetASN1([]string{"-db", path, "TS 38.413"}) }, []string{"1 ASN.1 assignments", "Section 9.4.5"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := captureStdout(t, tt.run)
			for _, want := range tt.want {
				if !strings.Contains(out, want) {
					t.Errorf("expected output to contain %q, got:\n%s", want, out)
				}
			}
		})
	}
}

// TestCmdGetImage_ToFile covers the -o path through the cmd layer.
func TestCmdGetImage_ToFile(t *testing.T) {
	path := seedDBPath(t)
	d, err := db.OpenReadWrite(path)
	if err != nil {
		t.Fatal(err)
	}
	data := []byte{0x89, 'P', 'N', 'G'}
	if err := d.Exec(
		"INSERT INTO images (spec_id, version, name, mime_type, data, llm_readable) VALUES (?, ?, ?, ?, ?, 1)",
		"TS 23.501", "18.6.0", "figure1.png", "image/png", data,
	); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}

	outFile := filepath.Join(t.TempDir(), "img.png")
	cmdGetImage([]string{"-db", path, "-o", outFile, "TS 23.501", "figure1.png"})
	written, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !bytes.Equal(written, data) {
		t.Error("file bytes differ from stored image")
	}
}

// TestCmdListVersions_Offline drives list-versions through its full cmd
// layer with the archive unreachable: the database version still lists, the
// incomplete-listing warning goes to stderr, and no version cache is created.
func TestCmdListVersions_Offline(t *testing.T) {
	path := seedDBPath(t)
	cacheRoot := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheRoot)
	orig := queryClient
	queryClient = &http.Client{Transport: errorRoundTripper{}}
	t.Cleanup(func() { queryClient = orig })

	out := captureStdout(t, func() {
		cmdListVersions([]string{"-db", path, "TS 23.501"})
	})
	if !strings.Contains(out, "18.6.0") {
		t.Errorf("expected the database version in the listing, got:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(cacheRoot, "3gpp-mcp", "versions.db")); err == nil {
		t.Error("list-versions must not create the version cache")
	}
}

// TestOpenSource_Store covers the store-open path shared by all version-aware
// commands.
func TestOpenSource_Store(t *testing.T) {
	path := seedDBPath(t)
	qf := &queryFlags{db: path, versionCache: filepath.Join(t.TempDir(), "versions.db"), versionCacheMB: 1}
	src, cleanup, err := qf.openSource(true)
	if err != nil {
		t.Fatalf("openSource: %v", err)
	}
	defer cleanup()
	if src.Store == nil {
		t.Error("expected the version store to be opened")
	}

	// -no-fetch skips the store even when a version was requested.
	qf.noFetch = true
	src, cleanup, err = qf.openSource(true)
	if err != nil {
		t.Fatalf("openSource with -no-fetch: %v", err)
	}
	defer cleanup()
	if src.Store != nil {
		t.Error("expected no store with -no-fetch")
	}
}

// TestFamilyPartsHint covers the split-file family hint shared by the query
// commands: a family ID never resolves to content of its own, so the parts
// listing is the useful answer.
func TestFamilyPartsHint(t *testing.T) {
	d := testutil.SetupTestDB(t)
	if err := d.ExecScript(`
INSERT INTO specs (id, version, version_token, title, release, series) VALUES
    ('TS 38.101-1', '18.0.0', 'i00', 'NR UE radio Part 1', '18', '38'),
    ('TS 38.101-2', '18.0.0', 'i00', 'NR UE radio Part 2', '18', '38');`); err != nil {
		t.Fatalf("seed family parts: %v", err)
	}
	src := tools.NewSource(d)
	var out, errOut bytes.Buffer

	err := runGetTOC(t.Context(), &out, &errOut, src, "TS 38.101", "")
	if err == nil || !strings.Contains(err.Error(), "multiple parts") {
		t.Errorf("expected family parts hint from get-toc, got: %v", err)
	}
	err = runGetSection(t.Context(), &out, &errOut, src, "TS 38.101", "1", "", false)
	if err == nil || !strings.Contains(err.Error(), "multiple parts") {
		t.Errorf("expected family parts hint from get-section, got: %v", err)
	}
	err = runListImages(t.Context(), &out, &errOut, src, "TS 38.101", "")
	if err == nil || !strings.Contains(err.Error(), "multiple parts") {
		t.Errorf("expected family parts hint from list-images, got: %v", err)
	}
}

// TestRunGetImage_NotConverted pins the EMF passthrough: unlike the MCP tool,
// the bytes are still written, with a conversion note on stderr.
func TestRunGetImage_NotConverted(t *testing.T) {
	d := testutil.SetupTestDB(t)
	src := tools.NewSource(d)
	data := []byte{0x01, 0x00, 0x00, 0x00}
	if err := d.Exec(
		"INSERT INTO images (spec_id, version, name, mime_type, data, llm_readable) VALUES (?, ?, ?, ?, ?, 0)",
		"TS 23.501", "18.6.0", "figure2.emf", "image/emf", data,
	); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if err := runGetImage(t.Context(), &out, &errOut, src, "TS 23.501", "figure2.emf", "", ""); err != nil {
		t.Fatalf("runGetImage: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Error("expected raw EMF bytes on stdout")
	}
	if !strings.Contains(errOut.String(), "--convert-image") {
		t.Errorf("expected conversion note on stderr, got: %s", errOut.String())
	}
}

// TestRunGetImage_ArchivedHint covers the archived branch of the EMF note: an
// image fetched on demand is converted at fetch time, so the note must point
// at LibreOffice, not at rebuilding the prebuilt database.
func TestRunGetImage_ArchivedHint(t *testing.T) {
	origPoll := fetchPollInterval
	fetchPollInterval = 10 * time.Millisecond
	defer func() { fetchPollInterval = origPoll }()

	d := testutil.SetupTestDB(t)

	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<a href="23501-j50.zip">23501-j50.zip</a>`+"\n")
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	data := []byte{0x01, 0x00, 0x00, 0x00}
	store, err := versionstore.Open(versionstore.Options{
		Path:       filepath.Join(t.TempDir(), "versions.db"),
		LimitBytes: -1,
		Fetcher: func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
			return db.Spec{Title: "System architecture", Release: "19", Series: "23"},
				[]db.Section{{Number: "5.1", Title: "General", Level: 2, Content: "Archived text."}}, nil
		},
		ImageFetcher: func(ctx context.Context, sv *pipeline.SpecVersion) ([]db.Image, error) {
			return []db.Image{{Name: "figure3.emf", MIMEType: "image/emf", Data: data, LLMReadable: false}}, nil
		},
	})
	if err != nil {
		t.Fatalf("versionstore.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	src := tools.NewSource(d)
	src.Store = store
	src.Client = &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}
	src.UseCache = false
	src.Budget = 5 * time.Second

	var out, errOut bytes.Buffer
	if err := runGetImage(t.Context(), &out, &errOut, src, "TS 23.501", "figure3.emf", "19.5.0", ""); err != nil {
		t.Fatalf("runGetImage archived: %v", err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Error("expected raw image bytes on stdout")
	}
	if !strings.Contains(errOut.String(), "LibreOffice") {
		t.Errorf("expected the fetch-time LibreOffice hint for an archived image, got: %s", errOut.String())
	}
	if strings.Contains(errOut.String(), "--convert-image") {
		t.Errorf("the rebuild hint does not apply to archived versions, got: %s", errOut.String())
	}
}

// TestCmdQuery_ExitsOnError pins the failure path: a query command against a
// missing database reports the error and exits 1.
func TestCmdQuery_ExitsOnError(t *testing.T) {
	orig := exit
	code := 0
	exit = func(c int) { code = c }
	t.Cleanup(func() { exit = orig })

	cmdListSpecs([]string{"-db", filepath.Join(t.TempDir(), "missing.db")})
	if code != 1 {
		t.Errorf("expected exit code 1, got %d", code)
	}
}

// TestCmdGetSection_ArgOrder covers the options-before-positionals guard via
// subprocess, mirroring TestCmdCompletion_UnknownShell.
func TestCmdGetSection_ArgOrder(t *testing.T) {
	if os.Getenv("CMD_GET_SECTION_ARGS_HELPER") == "1" {
		cmdGetSection([]string{"TS 23.501"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCmdGetSection_ArgOrder")
	cmd.Env = append(os.Environ(), "CMD_GET_SECTION_ARGS_HELPER=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit for missing section number")
	}
	if !strings.Contains(stderr.String(), "Options must come before positional arguments.") {
		t.Errorf("expected usage reminder on stderr, got: %s", stderr.String())
	}
}
