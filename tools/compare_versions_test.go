package tools

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
)

// seedOldVersion adds an older TS 23.501 to the test database, plus one extra
// section to the seeded 18.6.0, so a comparison exercises every category:
//
//	1      unchanged
//	5      content changed
//	5.1    title changed, body unchanged
//	7      renumbered to 5.1.1 (unique title on both sides)
//	6      removed
//	5.2    added in 18.6.0
func seedOldVersion(t *testing.T, d *db.DB) {
	t.Helper()
	err := d.ExecScript(`
INSERT INTO sections (spec_id, version, number, title, level, parent_number, content) VALUES
    ('TS 23.501', '18.6.0', '5.2', 'Security', 2, '5', '## 5.2 Security
Security architecture description.');

INSERT INTO specs (id, version, version_token, title, release, series) VALUES
    ('TS 23.501', '17.9.0', 'h90', 'System architecture for the 5G System (5GS)', '17', '23');

INSERT INTO sections (spec_id, version, number, title, level, parent_number, content) VALUES
    ('TS 23.501', '17.9.0', '1', 'Scope', 1, NULL, '# 1 Scope
This document defines the system architecture.'),
    ('TS 23.501', '17.9.0', '5', 'Architecture', 1, NULL, '# 5 Architecture
The EPS system architecture was defined here.
It has an extra line.'),
    ('TS 23.501', '17.9.0', '5.1', 'General aspects', 2, '5', '## 5.1 General aspects
General architecture description for 5G.'),
    ('TS 23.501', '17.9.0', '6', 'Legacy interworking', 1, NULL, '# 6 Legacy interworking
Interworking with EPS.'),
    ('TS 23.501', '17.9.0', '7', 'Overview', 1, NULL, '# 7 Overview
Overview of the architecture components.');
`)
	if err != nil {
		t.Fatalf("seed old version: %v", err)
	}
}

func TestCompareVersionsStructural(t *testing.T) {
	d := setupTestDB(t)
	seedOldVersion(t, d)
	handler := HandleCompareVersions(NewSource(d))

	// new_version omitted: the database default resolves to the newest, 18.6.0.
	result, _, err := handler(context.Background(), nil, CompareVersionsInput{
		SpecID:     "TS 23.501",
		OldVersion: "17.9.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %q", getTextContent(result))
	}
	text := getTextContent(result)

	for _, want := range []string{
		"[Compare: TS 23.501 — v17.9.0 (Rel-17) → v18.6.0 (Rel-18)]",
		"5 sections in v17.9.0 (Rel-17), 5 in v18.6.0 (Rel-18)",
		"Added: 1 | Removed: 1 | Renumbered: 1 | Title changed: 1 | Content changed: 1 | Unchanged: 1",
		"## Added in v18.6.0 (Rel-18)\n- 5.2 Security",
		"## Removed since v17.9.0 (Rel-17)\n- 6 Legacy interworking",
		"## Renumbered (same title)\n- 7 → 5.1.1  Overview",
		"## Title changed\n- 5.1: \"General aspects\" → \"General\"",
		"## Content changed\n- 5 Architecture  (3 → 2 lines)",
		"Use compare_versions with section_number",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("summary missing %q:\n%s", want, text)
		}
	}
}

func TestCompareVersionsStructuralPagination(t *testing.T) {
	d := setupTestDB(t)
	seedOldVersion(t, d)
	handler := HandleCompareVersions(NewSource(d))

	result, _, err := handler(context.Background(), nil, CompareVersionsInput{
		SpecID:     "TS 23.501",
		OldVersion: "17.9.0",
		NewVersion: "18.6.0",
		Offset:     2,
		MaxLines:   1,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := getTextContent(result)
	if !strings.HasPrefix(text, "[Compare: TS 23.501 — v17.9.0 (Rel-17) → v18.6.0 (Rel-18)]") {
		t.Errorf("a later page lost the compare header: %q", text)
	}
	if !strings.Contains(text, "[Lines 3-") {
		t.Errorf("pagination window missing: %q", text)
	}
}

func TestCompareVersionsSectionDiff(t *testing.T) {
	d := setupTestDB(t)
	seedOldVersion(t, d)
	handler := HandleCompareVersions(NewSource(d))

	result, _, err := handler(context.Background(), nil, CompareVersionsInput{
		SpecID:        "TS 23.501",
		OldVersion:    "17.9.0",
		NewVersion:    "18.6.0",
		SectionNumber: "5",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %q", getTextContent(result))
	}
	text := getTextContent(result)

	for _, want := range []string{
		"[Compare: TS 23.501 — Section 5 — v17.9.0 (Rel-17) → v18.6.0 (Rel-18)]",
		"@@ ",
		"-The EPS system architecture was defined here.",
		"-It has an extra line.",
		"+The 5G system architecture is defined here.",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("diff missing %q:\n%s", want, text)
		}
	}
}

func TestCompareVersionsIdenticalSection(t *testing.T) {
	d := setupTestDB(t)
	seedOldVersion(t, d)
	handler := HandleCompareVersions(NewSource(d))

	result, _, err := handler(context.Background(), nil, CompareVersionsInput{
		SpecID:        "TS 23.501",
		OldVersion:    "17.9.0",
		NewVersion:    "18.6.0",
		SectionNumber: "1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %q", getTextContent(result))
	}
	if text := getTextContent(result); !strings.Contains(text, "Section 1 is identical between v17.9.0 (Rel-17) and v18.6.0 (Rel-18)") {
		t.Errorf("identical section message missing: %q", text)
	}
}

// TestCompareVersionsSectionMissingOnOneSide checks that a section absent from
// one version is an informational answer with guidance, not a tool error.
func TestCompareVersionsSectionMissingOnOneSide(t *testing.T) {
	d := setupTestDB(t)
	seedOldVersion(t, d)
	handler := HandleCompareVersions(NewSource(d))

	result, _, err := handler(context.Background(), nil, CompareVersionsInput{
		SpecID:        "TS 23.501",
		OldVersion:    "17.9.0",
		NewVersion:    "18.6.0",
		SectionNumber: "5.2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("a section missing on one side should not be a tool error")
	}
	text := getTextContent(result)
	for _, want := range []string{
		"Section 5.2 of TS 23.501 does not exist in v17.9.0",
		"it exists in v18.6.0 (Rel-18)",
		"Section numbers move between releases",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message missing %q: %q", want, text)
		}
	}
}

func TestCompareVersionsSameVersion(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleCompareVersions(NewSource(d))

	result, _, err := handler(context.Background(), nil, CompareVersionsInput{
		SpecID:     "TS 23.501",
		OldVersion: "18.6.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatal("comparing a version with itself should not be a tool error")
	}
	if text := getTextContent(result); !strings.Contains(text, "both resolve to v18.6.0; nothing to compare") {
		t.Errorf("same-version message missing: %q", text)
	}
}

func TestCompareVersionsRequiresArguments(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleCompareVersions(NewSource(d))

	for name, input := range map[string]CompareVersionsInput{
		"missing spec_id":     {OldVersion: "17.9.0"},
		"missing old_version": {SpecID: "TS 23.501"},
	} {
		result, _, err := handler(context.Background(), nil, input)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if !result.IsError {
			t.Errorf("%s: expected an error result", name)
		}
	}
}

// TestCompareVersionsBothArchived checks the on-demand path: both sides come
// from the version cache, fetched within one call.
func TestCompareVersionsBothArchived(t *testing.T) {
	d := setupTestDB(t)
	src := sourceWithStore(t, d, cannedFetcher)
	handler := HandleCompareVersions(src)

	result, _, err := handler(context.Background(), nil, CompareVersionsInput{
		SpecID:     "TS 23.501",
		OldVersion: "19.5.0",
		NewVersion: "20.2.0",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %q", getTextContent(result))
	}
	text := getTextContent(result)
	if !strings.Contains(text, "v19.5.0 (Rel-19, archived) → v20.2.0 (Rel-19, archived)") {
		t.Errorf("header should mark both sides archived: %q", text)
	}
	// The canned fetcher returns the same content for every version.
	if !strings.Contains(text, "No structural differences") {
		t.Errorf("identical archived versions should report no differences: %q", text)
	}
}

// TestCompareVersionsBothFetchesInProgress checks that when both versions are
// still downloading, the caller gets one combined retry hint — and that both
// fetches were started, so a single retry succeeds.
func TestCompareVersionsBothFetchesInProgress(t *testing.T) {
	d := setupTestDB(t)
	release := make(chan struct{})
	src := sourceWithStore(t, d, func(ctx context.Context, sv *pipeline.SpecVersion) (db.Spec, []db.Section, error) {
		<-release
		return cannedFetcher(ctx, sv)
	})
	src.Budget = 20 * time.Millisecond
	handler := HandleCompareVersions(src)

	input := CompareVersionsInput{
		SpecID:     "TS 23.501",
		OldVersion: "19.5.0",
		NewVersion: "20.2.0",
	}
	result, _, err := handler(context.Background(), nil, input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.IsError {
		t.Error("fetches that are still running should not be a tool error")
	}
	text := getTextContent(result)
	for _, want := range []string{"v19.5.0", "v20.2.0", "Call the same tool again"} {
		if !strings.Contains(text, want) {
			t.Errorf("retry hint missing %q: %q", want, text)
		}
	}

	// Both fetches were started by the first call; once they finish, the same
	// call succeeds without further downloads.
	close(release)
	src.Budget = 5 * time.Second
	result, _, err = handler(context.Background(), nil, input)
	if err != nil {
		t.Fatalf("retry: unexpected error: %v", err)
	}
	if result.IsError {
		t.Fatalf("retry: unexpected error result: %q", getTextContent(result))
	}
	if text := getTextContent(result); !strings.Contains(text, "archived") {
		t.Errorf("retry should serve both archived versions: %q", text)
	}
}

// The structural diff classification itself is tested in internal/structdiff.
