package db

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// testChunks is a small stand-in for what internal/openapiindex renders, with
// enough shape to exercise ranking and every filter.
var testChunks = []OpenAPIChunk{
	{
		SpecID: "TS 29.510", APIName: "Nnrf_NFManagement", Filename: "TS29510_Nnrf_NFManagement.yaml",
		Kind: OpenAPIKindSchema, Name: "NFProfile",
		Body: "NFProfile:\n  type: object\n  properties:\n    nfInstanceId:\n      type: string\n",
	},
	{
		SpecID: "TS 29.510", APIName: "Nnrf_NFManagement", Filename: "TS29510_Nnrf_NFManagement.yaml",
		Kind: OpenAPIKindSchema, Name: "NFService",
		Body: "NFService:\n  type: object\n  description: mentions NFProfile in prose only\n",
	},
	{
		SpecID: "TS 29.510", APIName: "Nnrf_NFManagement", Filename: "TS29510_Nnrf_NFManagement.yaml",
		Kind: OpenAPIKindOperation, Name: "PUT /nf-instances/{nfInstanceID}",
		Body: "PUT /nf-instances/{nfInstanceID}\n  summary: Register NF Instance\n",
	},
	{
		SpecID: "TS 29.518", APIName: "Namf_Communication", Filename: "TS29518_Namf_Communication.yaml",
		Kind: OpenAPIKindSchema, Name: "UeContext",
		Body: "UeContext:\n  type: object\n  properties:\n    supi:\n      type: string\n",
	},
}

func setupOpenAPIIndexDB(t *testing.T) *DB {
	t.Helper()
	d := setupTestDB(t)
	if err := d.ReplaceOpenAPIChunks(testChunks); err != nil {
		t.Fatalf("ReplaceOpenAPIChunks: %v", err)
	}
	return d
}

func names(results []OpenAPISearchResult) []string {
	out := make([]string, len(results))
	for i, r := range results {
		out[i] = r.Name
	}
	return out
}

func TestSearchOpenAPI(t *testing.T) {
	d := setupOpenAPIIndexDB(t)

	tests := []struct {
		name    string
		query   string
		specIDs []string
		apiName string
		kind    string
		want    []string
	}{
		{
			// The name column outweighs body, so the schema that is named
			// NFProfile beats the one that only mentions it.
			name:  "name outranks body",
			query: "NFProfile",
			want:  []string{"NFProfile", "NFService"},
		},
		{
			// '-' and '.' split tokens rather than breaking the query.
			name:  "hyphenated term",
			query: "nf-instances",
			want:  []string{"PUT /nf-instances/{nfInstanceID}"},
		},
		{
			name:  "kind filter",
			query: "NFProfile OR nf-instances",
			kind:  OpenAPIKindOperation,
			want:  []string{"PUT /nf-instances/{nfInstanceID}"},
		},
		{
			name:    "spec filter",
			query:   "type",
			specIDs: []string{"TS 29.518"},
			want:    []string{"UeContext"},
		},
		{
			name:    "api filter",
			query:   "type",
			apiName: "Namf_Communication",
			want:    []string{"UeContext"},
		},
		{
			// An API name is copied out of prose as often as out of
			// list_openapi, so the filter ignores case.
			name:    "api filter ignores case",
			query:   "type",
			apiName: "namf_communication",
			want:    []string{"UeContext"},
		},
		{
			name:  "column filter",
			query: "name:NFProfile",
			want:  []string{"NFProfile"},
		},
		{
			// No stemming: the index stores identifiers, not English.
			name:  "no stemming",
			query: "properties",
			want:  []string{"NFProfile", "UeContext"},
		},
		{
			name:  "no match",
			query: "SmContextCreateData",
			want:  []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := d.SearchOpenAPI(t.Context(), tt.query, tt.specIDs, tt.apiName, tt.kind, false, 0, 0)
			if err != nil {
				t.Fatalf("SearchOpenAPI: %v", err)
			}
			if diff := strings.Join(names(got.Results), "|"); diff != strings.Join(tt.want, "|") {
				t.Errorf("results = %v, want %v", names(got.Results), tt.want)
			}
			if got.TotalCount != len(tt.want) {
				t.Errorf("total_count = %d, want %d", got.TotalCount, len(tt.want))
			}
		})
	}
}

// TestSearchOpenAPIRanksNameFirst pins the case bm25 gets wrong on its own:
// the real NFProfile schema renders to tens of kilobytes once its properties
// are expanded, and length normalization pushes it below the small schemas
// that only mention it in a description.
func TestSearchOpenAPIRanksNameFirst(t *testing.T) {
	d := setupTestDB(t)

	long := "NFProfile:\n  type: object\n  properties:\n" +
		strings.Repeat("    filler:\n      description: padding to make this chunk long\n", 400)
	chunks := []OpenAPIChunk{
		{
			SpecID: "TS 29.510", APIName: "Nnrf_NFManagement", Kind: OpenAPIKindSchema,
			Name: "NotificationData", Body: "NotificationData:\n  description: carries an NFProfile\n",
		},
		{
			SpecID: "TS 29.510", APIName: "Nnrf_NFManagement", Kind: OpenAPIKindSchema,
			Name: "NFProfile", Body: long,
		},
		{
			SpecID: "TS 29.510", APIName: "Nnrf_NFManagement", Kind: OpenAPIKindSchema,
			Name: "NFProfileList", Body: "NFProfileList:\n  type: array\n  items:\n    $ref: '#/components/schemas/NFProfile'\n",
		},
	}
	if err := d.ReplaceOpenAPIChunks(chunks); err != nil {
		t.Fatalf("ReplaceOpenAPIChunks: %v", err)
	}

	got, err := d.SearchOpenAPI(t.Context(), "NFProfile", nil, "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("SearchOpenAPI: %v", err)
	}
	// Exact name first, then the name that contains it, then the rest.
	want := []string{"NFProfile", "NFProfileList", "NotificationData"}
	if strings.Join(names(got.Results), "|") != strings.Join(want, "|") {
		t.Errorf("results = %v, want %v", names(got.Results), want)
	}

	// A query carrying FTS5 syntax is not a name anybody typed, so the name
	// rank stays out of it and every hit is still returned.
	got, err = d.SearchOpenAPI(t.Context(), "NFProfile OR NotificationData", nil, "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("SearchOpenAPI operator query: %v", err)
	}
	if got.TotalCount != 3 {
		t.Errorf("total_count = %d, want 3", got.TotalCount)
	}
}

func TestSearchOpenAPIIncludeBody(t *testing.T) {
	d := setupOpenAPIIndexDB(t)

	got, err := d.SearchOpenAPI(t.Context(), "name:NFProfile", nil, "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("SearchOpenAPI: %v", err)
	}
	if len(got.Results) != 1 || got.Results[0].Body != "" {
		t.Fatalf("body should be omitted by default, got %q", got.Results[0].Body)
	}
	if !strings.Contains(got.Results[0].Snippet, "<mark>") {
		t.Errorf("snippet should mark the match, got %q", got.Results[0].Snippet)
	}

	got, err = d.SearchOpenAPI(t.Context(), "name:NFProfile", nil, "", "", true, 0, 0)
	if err != nil {
		t.Fatalf("SearchOpenAPI with body: %v", err)
	}
	if !strings.Contains(got.Results[0].Body, "nfInstanceId") {
		t.Errorf("body = %q, want the full chunk", got.Results[0].Body)
	}
}

func TestSearchOpenAPIPagination(t *testing.T) {
	d := setupOpenAPIIndexDB(t)

	var seen []string
	for offset := 0; offset < 4; offset++ {
		got, err := d.SearchOpenAPI(t.Context(), "type OR summary", nil, "", "", false, 1, offset)
		if err != nil {
			t.Fatalf("SearchOpenAPI offset %d: %v", offset, err)
		}
		if got.TotalCount != 4 {
			t.Fatalf("total_count = %d, want 4", got.TotalCount)
		}
		if len(got.Results) != 1 {
			t.Fatalf("offset %d returned %d results, want 1", offset, len(got.Results))
		}
		seen = append(seen, got.Results[0].Name)
	}
	// Paging must neither repeat nor drop a hit.
	unique := map[string]bool{}
	for _, n := range seen {
		if unique[n] {
			t.Fatalf("name %q returned twice across pages: %v", n, seen)
		}
		unique[n] = true
	}

	// An offset past the last hit still reports the full count.
	got, err := d.SearchOpenAPI(t.Context(), "type OR summary", nil, "", "", false, 10, 99)
	if err != nil {
		t.Fatalf("SearchOpenAPI past end: %v", err)
	}
	if got.TotalCount != 4 || len(got.Results) != 0 {
		t.Errorf("past end: got %d results, total %d; want 0 and 4", len(got.Results), got.TotalCount)
	}
}

func TestSearchOpenAPIInvalidKind(t *testing.T) {
	d := setupOpenAPIIndexDB(t)

	if _, err := d.SearchOpenAPI(t.Context(), "NFProfile", nil, "", "component", false, 0, 0); err == nil {
		t.Fatal("expected an error for an unknown kind")
	}
}

func TestSearchOpenAPIWithoutIndex(t *testing.T) {
	// A database built before search_openapi existed: the spec tables are
	// there, the index tables are not, and serve cannot create them because it
	// opens read-only.
	dbPath := filepath.Join(t.TempDir(), "old.db")
	d, err := OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	if err := d.ExecScript(SpecTablesSchema); err != nil {
		t.Fatalf("schema: %v", err)
	}

	if _, err := d.SearchOpenAPI(t.Context(), "NFProfile", nil, "", "", false, 0, 0); !errors.Is(err, ErrNoOpenAPIIndex) {
		t.Fatalf("err = %v, want ErrNoOpenAPIIndex", err)
	}
}

func TestAllOpenAPIDocs(t *testing.T) {
	d := setupTestDB(t)

	docs, err := d.AllOpenAPIDocs(t.Context())
	if err != nil {
		t.Fatalf("AllOpenAPIDocs: %v", err)
	}
	if len(docs) != 1 {
		t.Fatalf("got %d documents, want 1", len(docs))
	}
	// The builder needs the file name to resolve cross-file $refs, so it has
	// to survive the round trip along with the content.
	got := docs[0]
	if got.SpecID != "TS 29.510" || got.APIName != "Nnrf_NFManagement" {
		t.Errorf("document = %s %s, want TS 29.510 Nnrf_NFManagement", got.SpecID, got.APIName)
	}
	if got.Filename != "TS29510_Nnrf_NFManagement.yaml" {
		t.Errorf("filename = %q", got.Filename)
	}
	if !strings.Contains(got.Content, "NFProfile") {
		t.Errorf("content = %q, want the stored YAML", got.Content)
	}
}

func TestSearchOpenAPIClampsLimitAndOffset(t *testing.T) {
	d := setupOpenAPIIndexDB(t)

	tests := []struct {
		name       string
		limit      int
		offset     int
		wantLimit  int
		wantOffset int
	}{
		{"zero limit falls back to the default", 0, 0, DefaultSearchLimit, 0},
		{"negative limit falls back to the default", -5, 0, DefaultSearchLimit, 0},
		{"limit above the cap is clamped", MaxSearchLimit + 100, 0, MaxSearchLimit, 0},
		{"negative offset starts at the beginning", 0, -3, DefaultSearchLimit, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := d.SearchOpenAPI(t.Context(), "type", nil, "", "", false, tt.limit, tt.offset)
			if err != nil {
				t.Fatalf("SearchOpenAPI: %v", err)
			}
			if got.Limit != tt.wantLimit || got.Offset != tt.wantOffset {
				t.Errorf("limit, offset = %d, %d; want %d, %d", got.Limit, got.Offset, tt.wantLimit, tt.wantOffset)
			}
		})
	}
}

// TestSearchOpenAPISanitizesQuery checks that the syntax FTS5 would reject
// outright reaches it quoted instead of failing the search.
func TestSearchOpenAPISanitizesQuery(t *testing.T) {
	d := setupOpenAPIIndexDB(t)

	for _, query := range []string{
		"nf-instances",            // a bare hyphen is the column-filter operator
		"29.510",                  // a bare period is a syntax error
		`"unterminated`,           // an unterminated phrase
		"AND",                     // an operator with no operands
		"NFProfile -UeContext",    // a trailing exclusion
		"NEAR(NFProfile type, 5)", // a NEAR group
		"name:",                   // a column filter with no value
		"*",                       // a bare prefix operator
	} {
		t.Run(query, func(t *testing.T) {
			if _, err := d.SearchOpenAPI(t.Context(), query, nil, "", "", false, 0, 0); err != nil {
				t.Errorf("SearchOpenAPI(%q): %v", query, err)
			}
		})
	}
}

// TestDropOpenAPIIndex covers the recovery from a rebuild that failed partway:
// the database has to end up visibly index-less rather than quietly serving
// the previous corpus.
func TestDropOpenAPIIndex(t *testing.T) {
	d := setupOpenAPIIndexDB(t)

	if err := d.DropOpenAPIIndex(); err != nil {
		t.Fatalf("DropOpenAPIIndex: %v", err)
	}

	if ok, err := d.HasOpenAPIIndex(t.Context()); err != nil || ok {
		t.Fatalf("HasOpenAPIIndex = %v, %v; want false, nil", ok, err)
	}
	if _, err := d.SearchOpenAPI(t.Context(), "NFProfile", nil, "", "", false, 0, 0); !errors.Is(err, ErrNoOpenAPIIndex) {
		t.Fatalf("err = %v, want ErrNoOpenAPIIndex", err)
	}

	// Dropping is recoverable: the schema recreates the tables, triggers and
	// all, and the index works again.
	if err := d.ExecScript(OpenAPIIndexSchema); err != nil {
		t.Fatalf("recreate schema: %v", err)
	}
	if err := d.ReplaceOpenAPIChunks(testChunks); err != nil {
		t.Fatalf("ReplaceOpenAPIChunks: %v", err)
	}
	got, err := d.SearchOpenAPI(t.Context(), "NFProfile", nil, "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("SearchOpenAPI after rebuild: %v", err)
	}
	if len(got.Results) == 0 {
		t.Error("index did not come back after being dropped and rebuilt")
	}
}

func TestReplaceOpenAPIChunksClearsStaleRows(t *testing.T) {
	d := setupOpenAPIIndexDB(t)

	// A rebuild replaces the index wholesale; the FTS side has to follow, or
	// the old rows keep matching and searches fail on a missing backing row.
	if err := d.ReplaceOpenAPIChunks(testChunks[3:]); err != nil {
		t.Fatalf("ReplaceOpenAPIChunks: %v", err)
	}

	got, err := d.SearchOpenAPI(t.Context(), "NFProfile", nil, "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("SearchOpenAPI: %v", err)
	}
	if got.TotalCount != 0 {
		t.Errorf("stale chunks survived the rebuild: %v", names(got.Results))
	}

	got, err = d.SearchOpenAPI(t.Context(), "UeContext", nil, "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("SearchOpenAPI: %v", err)
	}
	if len(got.Results) != 1 {
		t.Errorf("kept chunk missing after rebuild: %v", names(got.Results))
	}
}
