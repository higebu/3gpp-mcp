package tools

import (
	"context"
	"strings"
	"testing"
)

// TestHandleGetOpenAPI_FilenameFallback verifies that an api_name given in
// file-name form — the "TS29510_Nnrf_NFManagement" the models copy out of
// prose, with or without .yaml — still resolves to the stored document.
func TestHandleGetOpenAPI_FilenameFallback(t *testing.T) {
	d := setupTestDB(t)
	handler := HandleGetOpenAPI(d)

	for _, apiName := range []string{"TS29510_Nnrf_NFManagement", "TS29510_Nnrf_NFManagement.yaml", "nnrf_nfmanagement"} {
		t.Run(apiName, func(t *testing.T) {
			result, _, err := handler(context.Background(), nil, GetOpenAPIInput{
				SpecID: "TS 29.510", APIName: apiName,
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if result.IsError {
				t.Fatalf("unexpected error result: %s", getTextContent(result))
			}
			if !strings.Contains(getTextContent(result), "openapi: 3.0.0") {
				t.Errorf("expected YAML content, got: %s", getTextContent(result))
			}
		})
	}
}

// TestHandleGetOpenAPI_NotFoundHints verifies that a (spec_id, api_name) miss
// answers with what to do next rather than a raw SQL error: the api_name list
// of the spec, the specification that does provide the requested api_name,
// and — when a schema was named — the document that defines it.
func TestHandleGetOpenAPI_NotFoundHints(t *testing.T) {
	d := setupOpenAPISearchDB(t)
	handler := HandleGetOpenAPI(d)

	t.Run("wrong spec for api_name", func(t *testing.T) {
		result, _, _ := handler(context.Background(), nil, GetOpenAPIInput{
			SpecID: "TS 23.501", APIName: "Nnrf_NFManagement",
		})
		if !result.IsError {
			t.Fatal("expected error result")
		}
		text := getTextContent(result)
		if strings.Contains(text, "no rows in result set") {
			t.Errorf("raw SQL error leaked: %s", text)
		}
		if !strings.Contains(text, "not found in TS 23.501") {
			t.Errorf("missing not-found statement: %s", text)
		}
		if !strings.Contains(text, "provided by TS 29.510 / Nnrf_NFManagement") {
			t.Errorf("missing wrong-document hint: %s", text)
		}
		if !strings.Contains(text, "list_openapi") {
			t.Errorf("missing list_openapi pointer for a spec with no definitions: %s", text)
		}
	})

	t.Run("unknown api_name lists what the spec has", func(t *testing.T) {
		result, _, _ := handler(context.Background(), nil, GetOpenAPIInput{
			SpecID: "TS 29.510", APIName: "Nope_API",
		})
		if !result.IsError {
			t.Fatal("expected error result")
		}
		text := getTextContent(result)
		if !strings.Contains(text, "Available api_name values in TS 29.510: Nnrf_NFManagement") {
			t.Errorf("missing available api_name listing: %s", text)
		}
	})

	t.Run("schema hint answers from the index", func(t *testing.T) {
		result, _, _ := handler(context.Background(), nil, GetOpenAPIInput{
			SpecID: "TS 23.501", APIName: "Namf_Wrong", Schema: "NFProfile",
		})
		if !result.IsError {
			t.Fatal("expected error result")
		}
		text := getTextContent(result)
		if !strings.Contains(text, `Schema "NFProfile" is defined in TS 29.510 / Nnrf_NFManagement`) {
			t.Errorf("missing schema hint: %s", text)
		}
		if !strings.Contains(text, "search_openapi") {
			t.Errorf("missing search_openapi pointer: %s", text)
		}
	})

	t.Run("schema hint omitted without index", func(t *testing.T) {
		bare := setupTestDB(t)
		if err := bare.Exec("DROP TABLE openapi_chunks_fts"); err != nil {
			t.Fatalf("drop index: %v", err)
		}
		result, _, _ := HandleGetOpenAPI(bare)(context.Background(), nil, GetOpenAPIInput{
			SpecID: "TS 23.501", APIName: "Namf_Wrong", Schema: "NFProfile",
		})
		if !result.IsError {
			t.Fatal("expected error result")
		}
		text := getTextContent(result)
		if strings.Contains(text, "is defined in") {
			t.Errorf("schema hint should be omitted without the index: %s", text)
		}
		if !strings.Contains(text, "not found in TS 23.501") {
			t.Errorf("not-found statement should survive a missing index: %s", text)
		}
	})
}
