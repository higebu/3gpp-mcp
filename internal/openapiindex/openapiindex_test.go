package openapiindex

import (
	"strings"
	"testing"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/testutil"
)

// Two documents that reference each other the way the 5G SBI definitions do:
// the operation's request body is a $ref into this file, and one of that
// schema's properties is a $ref into the other file.
var crossFileDocs = []db.OpenAPIDoc{
	{
		SpecID: "TS 29.510", APIName: "Nnrf_NFManagement", Filename: "TS29510_Nnrf_NFManagement.yaml",
		Content: `openapi: 3.0.0
paths:
  /nf-instances/{nfInstanceID}:
    put:
      summary: Register NF Instance
      parameters:
        - name: nfInstanceID
          in: path
      requestBody:
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/NFProfile'
    get:
      summary: Read NF Instance
    options:
      summary: Discover communication options
components:
  schemas:
    NFProfile:
      type: object
      description: Information of an NF Instance
      required:
        - nfInstanceId
        - nfType
      properties:
        nfInstanceId:
          type: string
        nfType:
          $ref: 'TS29571_CommonData.yaml#/components/schemas/NFType'
        offRef:
          $ref: 'TS29999_Missing.yaml#/components/schemas/Nowhere'
        nfServices:
          type: array
          items:
            $ref: 'TS29571_CommonData.yaml#/components/schemas/NFService'
        plmnIdList:
          type: object
          additionalProperties:
            $ref: 'TS29571_CommonData.yaml#/components/schemas/PlmnId'
    SupportedFeatures:
      $ref: 'TS29571_CommonData.yaml#/components/schemas/NFType'
`,
	},
	{
		SpecID: "TS 29.571", APIName: "CommonData", Filename: "TS29571_CommonData.yaml",
		Content: `openapi: 3.0.0
components:
  schemas:
    NFType:
      type: string
      description: NF types known to the NRF
      enum:
        - NRF
        - AMF
        - SMF
    NFService:
      type: object
      properties:
        serviceInstanceId:
          type: string
    PlmnId:
      type: object
      allOf:
        - $ref: '#/components/schemas/NFType'
      properties:
        mcc:
          type: string
`,
	},
}

func chunkByName(t *testing.T, chunks []db.OpenAPIChunk, kind, name string) db.OpenAPIChunk {
	t.Helper()
	for _, c := range chunks {
		if c.Kind == kind && c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s chunk named %q in %d chunks", kind, name, len(chunks))
	return db.OpenAPIChunk{}
}

func TestChunks(t *testing.T) {
	store, parseErrs := NewStore(crossFileDocs)
	if len(parseErrs) != 0 {
		t.Fatalf("parse errors = %v, want none", parseErrs)
	}
	chunks := Chunks(store)

	var gotNames []string
	for _, c := range chunks {
		gotNames = append(gotNames, c.Kind+" "+c.Name)
	}
	want := []string{
		"schema NFProfile",
		"schema SupportedFeatures",
		"operation GET /nf-instances/{nfInstanceID}",
		"operation PUT /nf-instances/{nfInstanceID}",
		"operation OPTIONS /nf-instances/{nfInstanceID}",
		"schema NFService",
		"schema NFType",
		"schema PlmnId",
	}
	if strings.Join(gotNames, "|") != strings.Join(want, "|") {
		t.Errorf("chunks = %v, want %v", gotNames, want)
	}

	// A chunk carries the document it came from, so a hit can be handed
	// straight to get_openapi.
	nfProfile := chunkByName(t, chunks, db.OpenAPIKindSchema, "NFProfile")
	if nfProfile.SpecID != "TS 29.510" || nfProfile.APIName != "Nnrf_NFManagement" {
		t.Errorf("NFProfile came from %s %s, want TS 29.510 Nnrf_NFManagement", nfProfile.SpecID, nfProfile.APIName)
	}
}

func TestChunksExpandsOneLevel(t *testing.T) {
	store, _ := NewStore(crossFileDocs)
	chunks := Chunks(store)

	body := chunkByName(t, chunks, db.OpenAPIKindSchema, "NFProfile").Body

	// A cross-file $ref is expanded, which is what makes the referenced type
	// searchable from the schema that uses it.
	for _, want := range []string{"nfInstanceId", "nfType", "# expanded:", "NF types known to the NRF"} {
		if !strings.Contains(body, want) {
			t.Errorf("NFProfile body missing %q:\n%s", want, body)
		}
	}
	// A $ref into a document the database does not hold stays a bare ref
	// rather than failing the render.
	if !strings.Contains(body, "$ref: TS29999_Missing.yaml#/components/schemas/Nowhere") {
		t.Errorf("unresolvable ref should survive as text:\n%s", body)
	}

	// One level is the limit: PlmnId's own allOf expansion must not drag in a
	// third level.
	plmn := chunkByName(t, chunks, db.OpenAPIKindSchema, "PlmnId").Body
	if strings.Count(plmn, "# expanded:") != 1 {
		t.Errorf("PlmnId expanded more than one level:\n%s", plmn)
	}
}

// TestChunksExpandsContainerRefs covers the shape most 5G SBI relationships
// actually take: the type is behind items or additionalProperties, not behind
// the property's own $ref.
func TestChunksExpandsContainerRefs(t *testing.T) {
	store, _ := NewStore(crossFileDocs)
	body := chunkByName(t, Chunks(store), db.OpenAPIKindSchema, "NFProfile").Body

	for _, want := range []string{
		// items: the referenced type is named and its fields are expanded, so
		// NFProfile is reachable from a search for NFService.
		"items:", "NFService", "serviceInstanceId",
		// additionalProperties: the map's value type, likewise.
		"additionalProperties:", "PlmnId", "mcc",
		// The property's own scalars survive alongside the container ref.
		"type: array",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("NFProfile body missing %q:\n%s", want, body)
		}
	}
}

// TestChunksIndexesScalarLists covers enum and required, which are the text a
// question quotes most directly.
func TestChunksIndexesScalarLists(t *testing.T) {
	chunks := Chunks(mustStore(t))

	nfType := chunkByName(t, chunks, db.OpenAPIKindSchema, "NFType").Body
	if !strings.Contains(nfType, "enum: NRF, AMF, SMF") {
		t.Errorf("NFType body missing its enum:\n%s", nfType)
	}
	nfProfile := chunkByName(t, chunks, db.OpenAPIKindSchema, "NFProfile").Body
	if !strings.Contains(nfProfile, "required: nfInstanceId, nfType") {
		t.Errorf("NFProfile body missing its required list:\n%s", nfProfile)
	}
	// A list of mappings is structure, not text, and stays out.
	if strings.Contains(nfProfile, "allOf: map[") {
		t.Errorf("a composite list leaked into the body:\n%s", nfProfile)
	}
}

// TestChunksRendersAliasSchema covers a schema that is nothing but a $ref,
// which would otherwise index as its own name and no content at all.
func TestChunksRendersAliasSchema(t *testing.T) {
	body := chunkByName(t, Chunks(mustStore(t)), db.OpenAPIKindSchema, "SupportedFeatures").Body

	for _, want := range []string{"$ref: TS29571_CommonData.yaml", "# expanded:", "NF types known to the NRF"} {
		if !strings.Contains(body, want) {
			t.Errorf("SupportedFeatures body missing %q:\n%s", want, body)
		}
	}
}

func mustStore(t *testing.T) *Store {
	t.Helper()
	store, parseErrs := NewStore(crossFileDocs)
	if len(parseErrs) != 0 {
		t.Fatalf("parse errors = %v, want none", parseErrs)
	}
	return store
}

func TestChunksOperationBody(t *testing.T) {
	store, _ := NewStore(crossFileDocs)
	chunks := Chunks(store)

	body := chunkByName(t, chunks, db.OpenAPIKindOperation, "PUT /nf-instances/{nfInstanceID}").Body
	for _, want := range []string{
		"PUT /nf-instances/{nfInstanceID}",
		"summary: Register NF Instance",
		"parameter: nfInstanceID",
		"requestBody:",
		"nfInstanceId",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("operation body missing %q:\n%s", want, body)
		}
	}
}

func TestChunksDeterministic(t *testing.T) {
	// Go map iteration is random, so a rebuild that walked the parsed YAML
	// directly would churn the index on every run.
	store, _ := NewStore(crossFileDocs)
	first := Chunks(store)
	for i := 0; i < 5; i++ {
		store, _ := NewStore(crossFileDocs)
		got := Chunks(store)
		if len(got) != len(first) {
			t.Fatalf("chunk count changed between runs: %d vs %d", len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("chunk %d changed between runs:\n%+v\nvs\n%+v", j, got[j], first[j])
			}
		}
	}
}

func TestNewStoreSkipsUnparsable(t *testing.T) {
	docs := append([]db.OpenAPIDoc{{
		SpecID: "TS 29.500", APIName: "Broken", Filename: "broken.yaml",
		Content: "\topenapi: [unclosed\n",
	}}, crossFileDocs...)

	store, parseErrs := NewStore(docs)
	if len(parseErrs) != 1 {
		t.Fatalf("parse errors = %v, want 1", parseErrs)
	}
	// The error has to name the file, or an operator cannot act on it.
	if !strings.Contains(parseErrs[0].Error(), "broken.yaml") {
		t.Errorf("error does not name the document: %v", parseErrs[0])
	}
	if len(store.Docs) != len(crossFileDocs) {
		t.Errorf("kept %d documents, want %d", len(store.Docs), len(crossFileDocs))
	}
}

func TestBuild(t *testing.T) {
	d := testutil.SetupTestDB(t)

	stats, err := Build(t.Context(), d)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if stats.Documents != 1 {
		t.Errorf("documents = %d, want 1", stats.Documents)
	}
	if stats.Schemas != 2 || stats.Operations != 2 {
		t.Errorf("stats = %+v, want 2 schemas and 2 operations", stats)
	}
	if stats.Chunks != stats.Schemas+stats.Operations || stats.Bytes == 0 {
		t.Errorf("stats = %+v, want consistent chunk count and non-zero bytes", stats)
	}

	// The chunks are searchable straight after the build.
	got, err := d.SearchOpenAPI(t.Context(), "NFProfile", nil, "", "", false, 0, 0)
	if err != nil {
		t.Fatalf("SearchOpenAPI: %v", err)
	}
	if len(got.Results) == 0 || got.Results[0].Name != "NFProfile" {
		t.Errorf("search after build returned %+v", got.Results)
	}
}

func TestBuildIsIdempotent(t *testing.T) {
	d := testutil.SetupTestDB(t)

	first, err := Build(t.Context(), d)
	if err != nil {
		t.Fatalf("first Build: %v", err)
	}
	second, err := Build(t.Context(), d)
	if err != nil {
		t.Fatalf("second Build: %v", err)
	}
	if first != second {
		t.Errorf("rebuild changed the index: %+v vs %+v", first, second)
	}
}
