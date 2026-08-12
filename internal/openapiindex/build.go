package openapiindex

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/higebu/3gpp-mcp/db"
)

// Stats summarizes a rebuild, for the line the CLI logs afterwards.
type Stats struct {
	Documents  int `json:"documents"`
	Unparsable int `json:"unparsable"`
	Chunks     int `json:"chunks"`
	Schemas    int `json:"schemas"`
	Operations int `json:"operations"`
	Bytes      int `json:"bytes"`
}

// String renders the stats as the one line a build prints.
func (s Stats) String() string {
	return fmt.Sprintf("%d chunks (%d schemas, %d operations) from %d OpenAPI documents",
		s.Chunks, s.Schemas, s.Operations, s.Documents)
}

// Chunks renders every schema and operation in the store, in store order.
func Chunks(store *Store) []db.OpenAPIChunk {
	var chunks []db.OpenAPIChunk
	for _, doc := range store.Docs {
		schemas := store.Schemas(doc)
		for _, name := range sortedKeys(schemas) {
			schema, ok := schemas[name].(map[string]any)
			if !ok {
				continue
			}
			chunks = append(chunks, db.OpenAPIChunk{
				SpecID:   doc.SpecID,
				APIName:  doc.APIName,
				Filename: doc.Filename,
				Kind:     db.OpenAPIKindSchema,
				Name:     name,
				Body:     store.RenderSchema(doc, name, schema, 0),
			})
		}

		for _, op := range store.Operations(doc) {
			name := strings.ToUpper(op.Method) + " " + op.Path
			body := []string{name}
			if summary, ok := op.Op["summary"].(string); ok {
				body = append(body, "  summary: "+summary)
			}
			params, _ := op.Op["parameters"].([]any)
			for _, p := range params {
				param, _ := p.(map[string]any)
				if param == nil {
					continue
				}
				if pname, ok := param["name"].(string); ok {
					body = append(body, "  parameter: "+pname)
				}
			}
			if target, tName, tSchema, ok := store.RequestSchema(doc, op.Op); ok {
				body = append(body, "  requestBody:")
				body = append(body, indent(store.RenderSchema(target, tName, tSchema, 0), "    ")...)
			}
			chunks = append(chunks, db.OpenAPIChunk{
				SpecID:   doc.SpecID,
				APIName:  doc.APIName,
				Filename: doc.Filename,
				Kind:     db.OpenAPIKindOperation,
				Name:     name,
				Body:     strings.Join(body, "\n"),
			})
		}
	}
	return chunks
}

// Build rebuilds the whole OpenAPI search index from openapi_specs. It is
// always a full rebuild: a $ref only resolves once the file it points at is in
// the database, so importing one document can change the chunks of documents
// that were imported long before it.
//
// The database must be open read-write and carry the index schema, so callers
// run InitSchema first.
func Build(ctx context.Context, d *db.DB) (Stats, error) {
	docs, err := d.AllOpenAPIDocs(ctx)
	if err != nil {
		return Stats{}, fmt.Errorf("read openapi documents: %w", err)
	}

	store, parseErrs := NewStore(docs)
	// Named one by one: a document missing from search is only actionable if
	// the operator can see which one it was and why.
	for _, err := range parseErrs {
		log.Printf("warning: openapi index: %v", err)
	}
	chunks := Chunks(store)

	if err := d.ReplaceOpenAPIChunks(chunks); err != nil {
		return Stats{}, fmt.Errorf("write openapi index: %w", err)
	}

	stats := Stats{Documents: len(store.Docs), Unparsable: len(parseErrs), Chunks: len(chunks)}
	for _, c := range chunks {
		if c.Kind == db.OpenAPIKindSchema {
			stats.Schemas++
		} else {
			stats.Operations++
		}
		stats.Bytes += len(c.Body)
	}
	return stats, nil
}
