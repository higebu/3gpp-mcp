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
components:
  schemas:
    NFProfile:
      type: object
      description: Information of an NF Instance
      properties:
        nfInstanceId:
          type: string
        nfType:
          $ref: 'TS29571_CommonData.yaml#/components/schemas/NFType'
        offRef:
          $ref: 'TS29999_Missing.yaml#/components/schemas/Nowhere'
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
	store, unparsable := NewStore(crossFileDocs)
	if unparsable != 0 {
		t.Fatalf("unparsable = %d, want 0", unparsable)
	}
	chunks := Chunks(store)

	var gotNames []string
	for _, c := range chunks {
		gotNames = append(gotNames, c.Kind+" "+c.Name)
	}
	want := []string{
		"schema NFProfile",
		"operation GET /nf-instances/{nfInstanceID}",
		"operation PUT /nf-instances/{nfInstanceID}",
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

	store, unparsable := NewStore(docs)
	if unparsable != 1 {
		t.Errorf("unparsable = %d, want 1", unparsable)
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
