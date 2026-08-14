package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/openapiindex"
	"github.com/higebu/3gpp-mcp/internal/testutil"
)

func setupOpenAPISearchDB(t *testing.T) *db.DB {
	t.Helper()
	d := testutil.SetupTestDB(t)
	if _, err := openapiindex.Build(context.Background(), d); err != nil {
		t.Fatalf("build openapi index: %v", err)
	}
	return d
}

func TestHandleSearchOpenAPI(t *testing.T) {
	d := setupOpenAPISearchDB(t)
	handler := HandleSearchOpenAPI(d)

	tests := []struct {
		name  string
		input SearchOpenAPIInput
		want  []string
		none  bool
	}{
		{
			name:  "schema by name",
			input: SearchOpenAPIInput{Query: "NFProfile"},
			want:  []string{"NFProfile", "\"kind\": \"schema\"", "TS 29.510", "Nnrf_NFManagement"},
		},
		{
			name:  "operation by path",
			input: SearchOpenAPIInput{Query: "nf-instances", Kind: db.OpenAPIKindOperation},
			want:  []string{"/nf-instances", "\"kind\": \"operation\""},
		},
		{
			name:  "spec filter excludes",
			input: SearchOpenAPIInput{Query: "NFProfile", SpecIDs: []string{"TS 23.501"}},
			none:  true,
		},
		{
			name:  "api filter",
			input: SearchOpenAPIInput{Query: "NFProfile", APIName: "Nnrf_NFManagement"},
			want:  []string{"NFProfile"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _, err := handler(context.Background(), nil, tt.input)
			if err != nil {
				t.Fatalf("handler: %v", err)
			}
			if result.IsError {
				t.Fatalf("unexpected tool error: %s", getTextContent(result))
			}
			text := getTextContent(result)

			var got db.OpenAPISearchResults
			if err := json.Unmarshal([]byte(text), &got); err != nil {
				t.Fatalf("unmarshal %q: %v", text, err)
			}
			if tt.none {
				if got.TotalCount != 0 {
					t.Errorf("total_count = %d, want 0", got.TotalCount)
				}
				return
			}
			if got.TotalCount == 0 {
				t.Fatalf("no results for %+v", tt.input)
			}
			for _, want := range tt.want {
				if !strings.Contains(text, want) {
					t.Errorf("result missing %q:\n%s", want, text)
				}
			}
		})
	}
}

func TestHandleSearchOpenAPIIncludeBody(t *testing.T) {
	d := setupOpenAPISearchDB(t)
	handler := HandleSearchOpenAPI(d)

	result, _, err := handler(context.Background(), nil, SearchOpenAPIInput{Query: "name:NFProfile"})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if strings.Contains(getTextContent(result), "\"body\"") {
		t.Error("body should be omitted unless include_body is set")
	}

	result, _, err = handler(context.Background(), nil, SearchOpenAPIInput{Query: "name:NFProfile", IncludeBody: true})
	if err != nil {
		t.Fatalf("handler with body: %v", err)
	}
	if !strings.Contains(getTextContent(result), "nfInstanceId") {
		t.Errorf("include_body should return the full chunk:\n%s", getTextContent(result))
	}
}

func TestHandleSearchOpenAPIErrors(t *testing.T) {
	d := setupOpenAPISearchDB(t)
	handler := HandleSearchOpenAPI(d)

	t.Run("empty query", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchOpenAPIInput{})
		if err != nil {
			t.Fatalf("handler: %v", err)
		}
		if !result.IsError {
			t.Error("empty query should be a tool error")
		}
	})

	t.Run("negative offset starts at the beginning", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchOpenAPIInput{Query: "NFProfile", Offset: -5})
		if err != nil {
			t.Fatalf("handler: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected tool error: %s", getTextContent(result))
		}
		var got db.OpenAPISearchResults
		if err := json.Unmarshal([]byte(getTextContent(result)), &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.Offset != 0 {
			t.Errorf("offset = %d, want 0", got.Offset)
		}
	})

	t.Run("unknown kind", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, SearchOpenAPIInput{Query: "NFProfile", Kind: "component"})
		if err != nil {
			t.Fatalf("handler: %v", err)
		}
		if !result.IsError {
			t.Error("unknown kind should be a tool error")
		}
	})
}

func TestHandleSearchOpenAPIWithoutIndex(t *testing.T) {
	// A database from before search_openapi existed answers with the rebuild
	// instructions rather than a bare SQL error.
	d := testutil.SetupTestDB(t)
	if err := d.Exec("DROP TABLE openapi_chunks_fts"); err != nil {
		t.Fatalf("drop index: %v", err)
	}

	result, _, err := HandleSearchOpenAPI(d)(context.Background(), nil, SearchOpenAPIInput{Query: "NFProfile"})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if !result.IsError || !strings.Contains(getTextContent(result), "build-openapi-index") {
		t.Errorf("want a rebuild hint, got: %s", getTextContent(result))
	}
}
