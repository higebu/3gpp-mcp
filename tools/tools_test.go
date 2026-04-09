package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/testutil"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	return testutil.SetupTestDB(t)
}

func getTextContent(result *mcp.CallToolResult) string {
	if result == nil || len(result.Content) == 0 {
		return ""
	}
	if tc, ok := result.Content[0].(*mcp.TextContent); ok {
		return tc.Text
	}
	return ""
}

func TestHandleListSpecs(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleListSpecs(d)

	t.Run("default pagination", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListSpecsInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "TS 23.501") || !strings.Contains(text, "TS 29.510") {
			t.Errorf("expected both specs in output, got: %s", text)
		}
		if !strings.Contains(text, `"total_count"`) {
			t.Errorf("expected total_count in output, got: %s", text)
		}
	})

	t.Run("with limit", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListSpecsInput{Limit: 1})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "TS 23.501") {
			t.Errorf("expected first spec in output, got: %s", text)
		}
		if strings.Contains(text, "TS 29.510") {
			t.Errorf("expected only one spec in output, got: %s", text)
		}
	})
}

func TestHandleGetTOC(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetTOC(d)

	t.Run("valid spec", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetTOCInput{SpecID: "TS 23.501"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "Table of Contents") {
			t.Errorf("expected TOC header, got: %s", text)
		}
		if !strings.Contains(text, "5.1 General") {
			t.Errorf("expected section 5.1, got: %s", text)
		}
	})

	t.Run("empty spec_id", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetTOCInput{SpecID: ""})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for empty spec_id")
		}
	})
}

func TestHandleGetSection(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetSection(d)

	t.Run("valid section", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{
			SpecID:        "TS 23.501",
			SectionNumber: "1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		if !strings.Contains(text, "system architecture") {
			t.Errorf("expected section content, got: %s", text)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{
			SpecID:        "TS 23.501",
			SectionNumber: "5",
			MaxLines:      1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "Truncated") {
			t.Errorf("expected truncation notice, got: %s", text)
		}
	})

	t.Run("empty spec_id", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{
			SpecID:        "",
			SectionNumber: "1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for empty spec_id")
		}
	})

	t.Run("nonexistent section", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{
			SpecID:        "TS 23.501",
			SectionNumber: "99",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for nonexistent section")
		}
	})
}

func TestHandleSearch(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleSearch(d)

	t.Run("empty query", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchInput{Query: ""})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for empty query")
		}
	})

	t.Run("valid query", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchInput{Query: "architecture"})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		if !strings.Contains(text, "TS 23.501") {
			t.Errorf("expected search result for TS 23.501, got: %s", text)
		}
	})
}

func TestHandleListOpenAPI(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleListOpenAPI(d)

	result, _, err := handler(context.Background(), nil, ListOpenAPIInput{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := getTextContent(result)
	if !strings.Contains(text, "Nnrf_NFManagement") {
		t.Errorf("expected openapi entry, got: %s", text)
	}
}

func TestHandleGetOpenAPI(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetOpenAPI(d)

	t.Run("valid", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetOpenAPIInput{
			SpecID:  "TS 29.510",
			APIName: "Nnrf_NFManagement",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
	})

	t.Run("with path filter", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetOpenAPIInput{
			SpecID:  "TS 29.510",
			APIName: "Nnrf_NFManagement",
			Path:    "/nf-instances",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "/nf-instances") {
			t.Errorf("expected path in output, got: %s", text)
		}
	})

	t.Run("with schema filter", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetOpenAPIInput{
			SpecID:  "TS 29.510",
			APIName: "Nnrf_NFManagement",
			Schema:  "NFProfile",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "NFProfile") {
			t.Errorf("expected schema in output, got: %s", text)
		}
	})

	t.Run("empty spec_id", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetOpenAPIInput{
			SpecID:  "",
			APIName: "Nnrf_NFManagement",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for empty spec_id")
		}
	})
}

func TestPaginateText(t *testing.T) {
	content := "line1\nline2\nline3\nline4\nline5"

	t.Run("no truncation", func(t *testing.T) {
		result := paginateText(content, 0, 10, 0)
		text := getTextContent(result)
		if strings.Contains(text, "Truncated") {
			t.Error("should not be truncated")
		}
		if !strings.Contains(text, "[Lines 1-5 of 5]") {
			t.Errorf("expected pagination header, got: %s", text)
		}
	})

	t.Run("with truncation", func(t *testing.T) {
		result := paginateText(content, 0, 2, 0)
		text := getTextContent(result)
		if !strings.Contains(text, "Truncated") {
			t.Error("should be truncated")
		}
		if !strings.Contains(text, "[Lines 1-2 of 5]") {
			t.Errorf("expected pagination header, got: %s", text)
		}
	})

	t.Run("with offset", func(t *testing.T) {
		result := paginateText(content, 2, 2, 0)
		text := getTextContent(result)
		if !strings.Contains(text, "[Lines 3-4 of 5]") {
			t.Errorf("expected pagination header, got: %s", text)
		}
	})

	t.Run("offset beyond end", func(t *testing.T) {
		result := paginateText(content, 100, 10, 0)
		text := getTextContent(result)
		if !strings.Contains(text, "No content at offset") {
			t.Errorf("expected 'no content' message, got: %s", text)
		}
	})

	t.Run("default max lines", func(t *testing.T) {
		result := paginateText(content, 0, 0, 0)
		text := getTextContent(result)
		// With default 200, all 5 lines should be included
		if strings.Contains(text, "Truncated") {
			t.Error("should not be truncated with default max lines")
		}
	})

	t.Run("smart cut at paragraph boundary", func(t *testing.T) {
		// 10 lines: "a","b","c","d","e","","g","h","i","j"
		// maxLines=5 → end=5, hardLimit=5*6/5=6, lines[5]="" → end extends to 6
		paraContent := "a\nb\nc\nd\ne\n\ng\nh\ni\nj"
		result := paginateText(paraContent, 0, 5, 0)
		text := getTextContent(result)
		if !strings.Contains(text, "[Lines 1-6 of 10]") {
			t.Errorf("expected smart cut to paragraph boundary, got: %s", text)
		}
	})

	t.Run("smart cut hard limit", func(t *testing.T) {
		// No empty lines → smart cut cannot extend, stays at original end
		noParaContent := "a\nb\nc\nd\ne\nf\ng\nh\ni\nj"
		result := paginateText(noParaContent, 0, 3, 0)
		text := getTextContent(result)
		if !strings.Contains(text, "[Lines 1-3 of 10]") {
			t.Errorf("expected no extension without paragraph boundary, got: %s", text)
		}
	})

	t.Run("max_chars limit", func(t *testing.T) {
		// "line1\nline2\nline3\nline4\nline5" — each "lineN" is 5 chars + newline = 6
		result := paginateText(content, 0, 10, 12)
		text := getTextContent(result)
		// 12 chars: "line1\n" (6) + "line2\n" (6) = 12 → fits 2 lines
		if !strings.Contains(text, "[Lines 1-2 of 5]") {
			t.Errorf("expected max_chars to limit to 2 lines, got: %s", text)
		}
	})

	t.Run("max_chars at least one line", func(t *testing.T) {
		result := paginateText(content, 0, 10, 1)
		text := getTextContent(result)
		// Even with maxChars=1, at least one line should be included
		if !strings.Contains(text, "[Lines 1-1 of 5]") {
			t.Errorf("expected at least one line, got: %s", text)
		}
	})
}

func TestHandleGetReferences(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetReferences(d)

	t.Run("outgoing", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{
			SpecID:        "TS 24.229",
			SectionNumber: "5.1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		if !strings.Contains(text, "TS 23.228") {
			t.Errorf("expected TS 23.228 reference, got: %s", text)
		}
		if !strings.Contains(text, "RFC 3261") {
			t.Errorf("expected RFC 3261 reference, got: %s", text)
		}
	})

	t.Run("incoming", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{
			SpecID:    "TS 33.203",
			Direction: "incoming",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "TS 24.229") {
			t.Errorf("expected TS 24.229 as source, got: %s", text)
		}
	})

	t.Run("incoming with section", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{
			SpecID:        "TS 33.203",
			SectionNumber: "6.1",
			Direction:     "incoming",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected error result: %s", getTextContent(result))
		}
		text := getTextContent(result)
		if !strings.Contains(text, "TS 24.229") {
			t.Errorf("expected TS 24.229 as source, got: %s", text)
		}
	})

	t.Run("empty spec_id", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error result for empty spec_id")
		}
	})

	t.Run("outgoing without section", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{
			SpecID:    "TS 24.229",
			Direction: "outgoing",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !result.IsError {
			t.Error("expected error for outgoing without section_number")
		}
	})

	t.Run("no results", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetReferencesInput{
			SpecID:        "TS 23.501",
			SectionNumber: "1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if text != "[]" {
			t.Errorf("expected empty array, got: %s", text)
		}
	})
}

func TestHandleSearch_SpecIDs(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleSearch(d)

	t.Run("with spec_ids", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchInput{
			Query:   "Scope",
			SpecIDs: []string{"TS 29.510"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "TS 29.510") {
			t.Errorf("expected TS 29.510, got: %s", text)
		}
		if strings.Contains(text, "TS 23.501") {
			t.Errorf("should not contain TS 23.501")
		}
	})

	t.Run("spec_id backward compat", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchInput{
			Query:  "Scope",
			SpecID: "TS 29.510",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "TS 29.510") {
			t.Errorf("expected TS 29.510, got: %s", text)
		}
	})

	t.Run("multi spec_ids", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchInput{
			Query:   "Scope",
			SpecIDs: []string{"TS 23.501", "TS 29.510"},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "TS 23.501") || !strings.Contains(text, "TS 29.510") {
			t.Errorf("expected both specs, got: %s", text)
		}
	})
}

func TestHandleGetOpenAPI_NonexistentPath(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetOpenAPI(d)

	result, _, _ := handler(context.Background(), nil, GetOpenAPIInput{
		SpecID: "TS 29.510", APIName: "Nnrf_NFManagement", Path: "/nonexistent",
	})
	text := getTextContent(result)
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' message, got: %s", text)
	}
	if !strings.Contains(text, "/nf-instances") {
		t.Errorf("expected available paths listing, got: %s", text)
	}
}

func TestHandleGetOpenAPI_NonexistentSchema(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetOpenAPI(d)

	result, _, _ := handler(context.Background(), nil, GetOpenAPIInput{
		SpecID: "TS 29.510", APIName: "Nnrf_NFManagement", Schema: "Nonexistent",
	})
	text := getTextContent(result)
	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' message, got: %s", text)
	}
	if !strings.Contains(text, "NFProfile") {
		t.Errorf("expected available schemas listing, got: %s", text)
	}
}

// TestHandleSearch_EdgeCases covers a set of degenerate query inputs that must
// produce clean tool errors rather than panics or 500-level responses.
func TestHandleSearch_EdgeCases(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleSearch(d)

	cases := []struct {
		name  string
		query string
	}{
		{"empty", ""},
		{"only punctuation", `"`},
		{"operators only", "AND OR NOT"},
		{"unterminated paren", "NEAR(term"},
		{"very long", strings.Repeat("a", 10000)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, _, err := handler(context.Background(), nil, SearchInput{Query: tc.query})
			if err != nil {
				t.Fatalf("handler returned error: %v", err)
			}
			if result == nil {
				t.Fatal("nil result")
			}
			// Either an error result or an empty set - never a panic.
		})
	}
}

// TestHandleGetSection_PaginationEdges covers paginateText boundary behaviour
// that was previously exercised only for happy paths.
func TestHandleGetSection_PaginationEdges(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetSection(d)

	t.Run("offset beyond content", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{
			SpecID: "TS 23.501", SectionNumber: "1", Offset: 10000,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "No content at offset") {
			t.Errorf("expected overflow message, got: %s", text)
		}
	})

	t.Run("max_chars limits output", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{
			SpecID: "TS 23.501", SectionNumber: "5", IncludeSubsections: true, MaxChars: 10,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if text == "" {
			t.Fatal("empty output")
		}
	})

	t.Run("negative offset clamped", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{
			SpecID: "TS 23.501", SectionNumber: "1", Offset: -5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "[Lines 1-") {
			t.Errorf("expected pagination header starting at line 1, got: %s", text)
		}
	})

	t.Run("max_lines=1 returns single line", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetSectionInput{
			SpecID: "TS 23.501", SectionNumber: "5", IncludeSubsections: true, MaxLines: 1,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "[Lines 1-") {
			t.Errorf("expected pagination header, got: %s", text)
		}
	})
}

// TestHandleListSpecs_InvalidPagination verifies that out-of-range pagination
// parameters are clamped by the handler rather than passed straight through to
// SQL OFFSET/LIMIT as negative or absurd values.
func TestHandleListSpecs_InvalidPagination(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleListSpecs(d)

	t.Run("negative offset", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListSpecsInput{Offset: -100})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "TS 23.501") {
			t.Errorf("expected specs in output, got: %s", text)
		}
	})

	t.Run("huge limit capped", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListSpecsInput{Limit: 1 << 30})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		// Should not crash; should contain total_count and the specs.
		if !strings.Contains(text, "total_count") {
			t.Errorf("expected total_count in output, got: %s", text)
		}
	})

	t.Run("offset past end returns empty list", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, ListSpecsInput{Offset: 10000})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		text := getTextContent(result)
		if !strings.Contains(text, "total_count") {
			t.Errorf("expected total_count in output, got: %s", text)
		}
	})
}
