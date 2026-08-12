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

// degenerateDoc exercises the shapes a hand-written 3GPP YAML can take that
// the renderer has to walk past rather than trip over.
const degenerateDoc = `openapi: 3.0.0
paths:
  # A path item that is not a mapping, and a method that is not an operation.
  /not-a-mapping: "nope"
  /partial:
    get: "also not a mapping"
    post:
      summary: Mixed scalars
      parameters:
        - "not a mapping"
        - notAName: 1
        - name: valid
      requestBody:
        $ref: '#/components/requestBodies/Shared'
    put:
      requestBody:
        content:
          text/plain: null
          application/json:
            schema:
              type: object
components:
  schemas:
    NotAMapping: "scalar schema"
    Leaves:
      type: object
      deprecated: true
      properties:
        count:
          type: integer
          minimum: 1
          multipleOf: 0.5
          deprecated: false
        notAMapping: "scalar property"
        nested:
          type: object
          properties:
            deeper:
              type: string
        offRef:
          $ref: 'not a components pointer'
        emptyList:
          enum: []
        listOfMappings:
          oneOf:
            - type: string
    Composed:
      allOf:
        - "not a mapping"
        - type: object
          required:
            - a
          properties:
            a:
              type: string
        - $ref: '#/components/schemas/NotAMapping'
`

// TestChunksHandlesDegenerateDocuments pins the renderer's behaviour on input
// it cannot make sense of: it skips what it cannot read and keeps going,
// rather than dropping the document or panicking.
func TestChunksHandlesDegenerateDocuments(t *testing.T) {
	store, parseErrs := NewStore([]db.OpenAPIDoc{
		{SpecID: "TS 29.500", APIName: "Degenerate", Filename: "degenerate.yaml", Content: degenerateDoc},
		// A document with neither components nor paths contributes nothing,
		// and a missing file name keeps it out of the cross-file lookup.
		{SpecID: "TS 29.501", APIName: "Empty", Content: "openapi: 3.0.0\n"},
	})
	if len(parseErrs) != 0 {
		t.Fatalf("parse errors = %v, want none", parseErrs)
	}
	chunks := Chunks(store)

	var got []string
	for _, c := range chunks {
		got = append(got, c.Kind+" "+c.Name)
	}
	want := []string{
		// NotAMapping is not a schema object and produces no chunk, and the
		// operations come out in httpMethods order rather than document order.
		"schema Composed",
		"schema Leaves",
		"operation PUT /partial",
		"operation POST /partial",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("chunks = %v, want %v", got, want)
	}

	leaves := chunkByName(t, chunks, db.OpenAPIKindSchema, "Leaves").Body
	for _, want := range []string{
		"deprecated: false",              // bool leaf
		"minimum: 1",                     // int leaf
		"multipleOf: 0.5",                // float leaf
		"$ref: not a components pointer", // an unrecognized ref stays as text
	} {
		if !strings.Contains(leaves, want) {
			t.Errorf("Leaves body missing %q:\n%s", want, leaves)
		}
	}
	// An empty list and a list of mappings are not leaf text.
	if strings.Contains(leaves, "enum:") || strings.Contains(leaves, "map[") {
		t.Errorf("a non-leaf list leaked into the body:\n%s", leaves)
	}

	composed := chunkByName(t, chunks, db.OpenAPIKindSchema, "Composed").Body
	for _, want := range []string{"required: a", "property: a", "$ref: #/components/schemas/NotAMapping"} {
		if !strings.Contains(composed, want) {
			t.Errorf("Composed body missing %q:\n%s", want, composed)
		}
	}

	post := chunkByName(t, chunks, db.OpenAPIKindOperation, "POST /partial").Body
	if !strings.Contains(post, "parameter: valid") {
		t.Errorf("POST body missing its one usable parameter:\n%s", post)
	}
	// A requestBody that is itself a $ref names a shared requestBody object,
	// not a schema, so nothing is expanded for it.
	if strings.Contains(post, "requestBody:") {
		t.Errorf("a $ref requestBody should not be expanded:\n%s", post)
	}
	// An inline schema with no $ref has nothing to resolve either.
	if put := chunkByName(t, chunks, db.OpenAPIKindOperation, "PUT /partial").Body; strings.Contains(put, "requestBody:") {
		t.Errorf("an inline request schema should not be expanded:\n%s", put)
	}
}

// TestResolveRejectsUnknownTargets covers the refs that lead out of the store.
func TestResolveRejectsUnknownTargets(t *testing.T) {
	store := mustStore(t)
	doc := store.Docs[0]

	tests := []struct {
		name string
		ref  string
	}{
		{"not a schema pointer", "#/components/parameters/Foo"},
		{"empty", ""},
		{"file not in the store", "TS29999_Missing.yaml#/components/schemas/Nowhere"},
		{"schema not in the file", "#/components/schemas/Nowhere"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, ok := store.Resolve(doc, tt.ref); ok {
				t.Errorf("Resolve(%q) resolved, want not found", tt.ref)
			}
		})
	}
}

func TestStatsString(t *testing.T) {
	got := Stats{Documents: 4, Chunks: 214, Schemas: 191, Operations: 23}.String()
	want := "214 chunks (191 schemas, 23 operations) from 4 OpenAPI documents"
	if got != want {
		t.Errorf("String() = %q, want %q", got, want)
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

// TestBuildReportsUnparsableDocuments checks that one bad document costs its
// own chunks and nothing else: the rest of the corpus is still indexed, and
// the count comes back so the caller can report it.
func TestBuildReportsUnparsableDocuments(t *testing.T) {
	d := testutil.SetupTestDB(t)
	if err := d.UpsertOpenAPI("TS 29.500", "Broken", "v1", "broken.yaml", "\topenapi: [unclosed\n"); err != nil {
		t.Fatalf("seed broken document: %v", err)
	}

	stats, err := Build(t.Context(), d)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if stats.Unparsable != 1 {
		t.Errorf("unparsable = %d, want 1", stats.Unparsable)
	}
	if stats.Documents != 1 || stats.Chunks == 0 {
		t.Errorf("stats = %+v, want the good document still indexed", stats)
	}
}

func TestBuildOnUnreachableDatabase(t *testing.T) {
	d := testutil.SetupTestDB(t)
	if err := d.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if _, err := Build(t.Context(), d); err == nil {
		t.Error("Build succeeded on a closed database")
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
