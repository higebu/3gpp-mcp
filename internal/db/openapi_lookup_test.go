package db

import (
	"errors"
	"strings"
	"testing"
)

func seedOpenAPILookup(t *testing.T) *DB {
	t.Helper()
	d := setupTestDB(t)
	if err := d.UpsertOpenAPI("TS 29.571", "CommonData", "v1.5.0", "TS29571_CommonData.yaml",
		"openapi: 3.0.0\ncomponents:\n  schemas:\n    ProblemDetails:\n      type: object\n"); err != nil {
		t.Fatalf("seed openapi: %v", err)
	}
	return d
}

func TestGetOpenAPIResolved(t *testing.T) {
	d := seedOpenAPILookup(t)

	for name, apiName := range map[string]string{
		"exact api_name":           "CommonData",
		"case-insensitive":         "commondata",
		"filename with extension":  "TS29571_CommonData.yaml",
		"filename sans extension":  "TS29571_CommonData",
		"filename case-insensitve": "ts29571_commondata",
	} {
		t.Run(name, func(t *testing.T) {
			content, err := d.GetOpenAPIResolved(t.Context(), "TS 29.571", apiName)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(content, "ProblemDetails") {
				t.Errorf("unexpected content: %s", content)
			}
		})
	}

	t.Run("not found", func(t *testing.T) {
		_, err := d.GetOpenAPIResolved(t.Context(), "TS 29.571", "Nonexistent")
		if !errors.Is(err, ErrOpenAPINotFound) {
			t.Fatalf("expected ErrOpenAPINotFound, got %v", err)
		}
	})

	t.Run("wrong spec", func(t *testing.T) {
		_, err := d.GetOpenAPIResolved(t.Context(), "TS 29.510", "CommonData")
		if !errors.Is(err, ErrOpenAPINotFound) {
			t.Fatalf("expected ErrOpenAPINotFound, got %v", err)
		}
	})
}

func TestFindOpenAPIByAPIName(t *testing.T) {
	d := seedOpenAPILookup(t)

	for name, apiName := range map[string]string{
		"api_name":                "CommonData",
		"filename sans extension": "TS29571_CommonData",
		"filename with extension": "TS29571_CommonData.yaml",
	} {
		t.Run(name, func(t *testing.T) {
			specs, err := d.FindOpenAPIByAPIName(t.Context(), apiName)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(specs) != 1 || specs[0].SpecID != "TS 29.571" || specs[0].APIName != "CommonData" {
				t.Errorf("unexpected result: %+v", specs)
			}
		})
	}

	t.Run("no match", func(t *testing.T) {
		specs, err := d.FindOpenAPIByAPIName(t.Context(), "Nonexistent")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(specs) != 0 {
			t.Errorf("expected no results, got %+v", specs)
		}
	})
}

func TestOpenAPISchemaSources(t *testing.T) {
	d := seedOpenAPILookup(t)
	chunks := []OpenAPIChunk{
		{SpecID: "TS 29.510", APIName: "Nnrf_NFManagement", Filename: "TS29510_Nnrf_NFManagement.yaml", Kind: OpenAPIKindSchema, Name: "NFProfile", Body: "NFProfile"},
		{SpecID: "TS 29.510", APIName: "Nnrf_NFManagement", Filename: "TS29510_Nnrf_NFManagement.yaml", Kind: OpenAPIKindOperation, Name: "GET /nf-instances", Body: "list"},
		{SpecID: "TS 29.571", APIName: "CommonData", Filename: "TS29571_CommonData.yaml", Kind: OpenAPIKindSchema, Name: "ProblemDetails", Body: "ProblemDetails"},
	}
	if err := d.ReplaceOpenAPIChunks(chunks); err != nil {
		t.Fatalf("seed chunks: %v", err)
	}

	t.Run("schema found", func(t *testing.T) {
		specs, err := d.OpenAPISchemaSources(t.Context(), "nfprofile")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(specs) != 1 || specs[0].SpecID != "TS 29.510" || specs[0].APIName != "Nnrf_NFManagement" {
			t.Errorf("unexpected result: %+v", specs)
		}
	})

	t.Run("operation name is not a schema", func(t *testing.T) {
		specs, err := d.OpenAPISchemaSources(t.Context(), "GET /nf-instances")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(specs) != 0 {
			t.Errorf("expected no results, got %+v", specs)
		}
	})

	t.Run("without index", func(t *testing.T) {
		if err := d.Exec("DROP TABLE openapi_chunks_fts"); err != nil {
			t.Fatalf("drop index: %v", err)
		}
		_, err := d.OpenAPISchemaSources(t.Context(), "NFProfile")
		if !errors.Is(err, ErrNoOpenAPIIndex) {
			t.Fatalf("expected ErrNoOpenAPIIndex, got %v", err)
		}
	})
}
