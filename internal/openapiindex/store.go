// Package openapiindex turns the OpenAPI YAML documents stored in
// openapi_specs into the search index behind the search_openapi tool: one row
// per schema and one per operation, rather than one per document.
//
// The unit matters. A 5G SBI document is tens of thousands of lines and names
// hundreds of data types, so a document-level hit says little more than
// list_openapi already says. A schema or an operation is the thing a question
// is actually about ("what does NFProfile carry", "what does PUT
// /nf-instances/{id} take"), and it is small enough to read.
//
// Chunks are rebuilt from the whole table at once, never per document: in the
// 5G SBI definitions most $refs point into another file, so a document's own
// chunks depend on documents that were not necessarily imported with it.
package openapiindex

import (
	"regexp"
	"sort"

	"github.com/higebu/3gpp-mcp/db"
	"gopkg.in/yaml.v3"
)

// Doc is one parsed OpenAPI document.
type Doc struct {
	SpecID   string
	APIName  string
	Filename string
	Root     map[string]any
}

// Store holds every parsed document, cross-linked by file name so a $ref can
// be followed out of the document that wrote it.
type Store struct {
	Docs   []*Doc
	byFile map[string]*Doc
}

// refRE matches the only $ref form these documents use: an optional file name
// followed by a pointer into components.schemas.
var refRE = regexp.MustCompile(`^([^#]*)#/components/schemas/([\w.-]+)$`)

// NewStore parses docs, skipping any that are not a YAML mapping. Documents
// that fail to parse are reported by count so a build can surface them without
// failing over one bad file.
func NewStore(docs []db.OpenAPIDoc) (*Store, int) {
	s := &Store{byFile: make(map[string]*Doc, len(docs))}
	skipped := 0
	for _, d := range docs {
		var root map[string]any
		if err := yaml.Unmarshal([]byte(d.Content), &root); err != nil || root == nil {
			skipped++
			continue
		}
		doc := &Doc{SpecID: d.SpecID, APIName: d.APIName, Filename: d.Filename, Root: root}
		s.Docs = append(s.Docs, doc)
		// A document with no file name cannot be the target of a cross-file
		// $ref, and indexing it under "" would make every such document
		// collide with the others.
		if d.Filename != "" {
			s.byFile[d.Filename] = doc
		}
	}
	return s, skipped
}

// Schemas returns doc's components.schemas mapping, or nil.
func (s *Store) Schemas(doc *Doc) map[string]any {
	comp, _ := doc.Root["components"].(map[string]any)
	if comp == nil {
		return nil
	}
	schemas, _ := comp["schemas"].(map[string]any)
	return schemas
}

// Resolve follows a $ref to the schema it names. It reports false when the ref
// is not a components.schemas pointer, or names a file this store does not
// hold — 3GPP documents also reference schemas from specifications outside the
// database, and those simply stay unexpanded.
func (s *Store) Resolve(doc *Doc, ref string) (*Doc, string, map[string]any, bool) {
	m := refRE.FindStringSubmatch(ref)
	if m == nil {
		return nil, "", nil, false
	}
	target := doc
	if m[1] != "" {
		target = s.byFile[m[1]]
		if target == nil {
			return nil, "", nil, false
		}
	}
	schema, _ := s.Schemas(target)[m[2]].(map[string]any)
	if schema == nil {
		return nil, "", nil, false
	}
	return target, m[2], schema, true
}

// sortedKeys returns m's keys in a fixed order. Go map iteration is random and
// yaml.Unmarshal into a map drops document order, so every rendering loop goes
// through here to keep a rebuild byte-identical to the last one.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
