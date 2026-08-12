package openapiindex

import (
	"fmt"
	"strings"
)

// httpMethods are the operation keys read out of a path item, in the order
// their chunks are emitted. It is the full OpenAPI set rather than the four
// verbs the SBI APIs mostly use, so a rarer one (TS 29.510 defines an OPTIONS
// operation) still gets a chunk.
var httpMethods = []string{"get", "put", "post", "patch", "delete", "head", "options", "trace"}

// Operation is one method of one path.
type Operation struct {
	Path   string
	Method string
	Op     map[string]any
}

// Operations returns doc's operations, ordered by path then by method.
func (s *Store) Operations(doc *Doc) []Operation {
	paths, _ := doc.Root["paths"].(map[string]any)
	if paths == nil {
		return nil
	}
	var ops []Operation
	for _, p := range sortedKeys(paths) {
		item, _ := paths[p].(map[string]any)
		if item == nil {
			continue
		}
		for _, method := range httpMethods {
			if op, ok := item[method].(map[string]any); ok {
				ops = append(ops, Operation{Path: p, Method: method, Op: op})
			}
		}
	}
	return ops
}

// RequestSchema resolves the schema an operation's request body carries. A
// requestBody that is itself a $ref names a shared requestBody object rather
// than a schema, and is left alone.
func (s *Store) RequestSchema(doc *Doc, op map[string]any) (*Doc, string, map[string]any, bool) {
	body, _ := op["requestBody"].(map[string]any)
	if body == nil {
		return nil, "", nil, false
	}
	if _, isRef := body["$ref"]; isRef {
		return nil, "", nil, false
	}
	content, _ := body["content"].(map[string]any)
	for _, mediaType := range sortedKeys(content) {
		media, _ := content[mediaType].(map[string]any)
		if media == nil {
			continue
		}
		schema, _ := media["schema"].(map[string]any)
		if schema == nil {
			continue
		}
		if ref, _ := schema["$ref"].(string); ref != "" {
			return s.Resolve(doc, ref)
		}
	}
	return nil, "", nil, false
}

// RenderSchema renders a schema as YAML-ish text with one level of $ref
// expanded.
//
// One level is what makes a chunk self-contained enough to answer "what fields
// does this have" without the reader resolving the whole reference graph, and
// it is also the limit: an answer sitting two hops away is not in any single
// chunk. Expanding further would multiply the index size — these schemas
// reference each other densely — for text that is already reachable through
// get_openapi.
func (s *Store) RenderSchema(doc *Doc, name string, schema map[string]any, depth int) string {
	out := []string{name + ":"}

	// A schema that is nothing but a $ref is an alias for another type. Without
	// this the chunk would be its own name and nothing else.
	if ref, _ := schema["$ref"].(string); ref != "" {
		out = append(out, "  $ref: "+ref)
		if target, tName, tSchema, ok := s.expand(doc, ref, depth); ok {
			out = append(out, "  # expanded:")
			out = append(out, indent(s.RenderSchema(target, tName, tSchema, depth+1), "  ")...)
		}
	}

	for _, key := range []string{"type", "description"} {
		if v, ok := schema[key].(string); ok {
			out = append(out, "  "+key+": "+v)
		}
	}

	// enum and required are lists of leaves, and they are the text a question
	// often quotes — a status value like REGISTERED, or the field a request is
	// rejected for missing.
	for _, key := range []string{"enum", "required"} {
		if line, ok := scalarLine("  "+key, schema[key]); ok {
			out = append(out, line)
		}
	}

	for _, key := range []string{"allOf", "oneOf", "anyOf"} {
		members, ok := schema[key].([]any)
		if !ok {
			continue
		}
		out = append(out, "  "+key+":")
		for _, member := range members {
			m, ok := member.(map[string]any)
			if !ok {
				continue
			}
			ref, _ := m["$ref"].(string)
			if ref != "" {
				if target, tName, tSchema, ok := s.expand(doc, ref, depth); ok {
					out = append(out, "    - $ref: "+ref, "      # expanded:")
					out = append(out, indent(s.RenderSchema(target, tName, tSchema, depth+1), "      ")...)
				} else {
					out = append(out, "    - $ref: "+ref)
				}
				continue
			}
			for _, k := range sortedKeys(m) {
				if line, ok := scalarLine("    - "+k, m[k]); ok {
					out = append(out, line)
				}
			}
			if props, ok := m["properties"].(map[string]any); ok {
				for _, p := range sortedKeys(props) {
					out = append(out, "      property: "+p)
				}
			}
		}
	}

	if props, ok := schema["properties"].(map[string]any); ok {
		out = append(out, "  properties:")
		for _, p := range sortedKeys(props) {
			out = append(out, "    "+p+":")
			v, ok := props[p].(map[string]any)
			if !ok {
				continue
			}
			if ref, _ := v["$ref"].(string); ref != "" {
				if target, tName, tSchema, ok := s.expand(doc, ref, depth); ok {
					out = append(out, "      $ref: "+ref, "      # expanded:")
					out = append(out, indent(s.RenderSchema(target, tName, tSchema, depth+1), "      ")...)
					continue
				}
			}
			for _, k := range sortedKeys(v) {
				if line, ok := scalarLine("      "+k, v[k]); ok {
					out = append(out, line)
				}
			}
			out = append(out, s.renderContainerRefs(doc, v, depth)...)
		}
	}

	return strings.Join(out, "\n")
}

// containerKeys are the wrappers a property puts between itself and the type it
// actually holds.
var containerKeys = []string{"items", "additionalProperties"}

// renderContainerRefs renders the $ref a property holds through a container.
// The 5G SBI definitions state most of their relationships this way — a list is
// `items: {$ref: NFService}` and a map is `additionalProperties: {$ref:
// AmfInfo}` — so reading only a property's direct $ref drops the majority of
// them, and with them every occurrence of the referenced type's name.
func (s *Store) renderContainerRefs(doc *Doc, prop map[string]any, depth int) []string {
	var out []string
	for _, key := range containerKeys {
		inner, _ := prop[key].(map[string]any)
		if inner == nil {
			continue
		}
		ref, _ := inner["$ref"].(string)
		if ref == "" {
			continue
		}
		out = append(out, "      "+key+":", "        $ref: "+ref)
		if target, tName, tSchema, ok := s.expand(doc, ref, depth); ok {
			out = append(out, "        # expanded:")
			out = append(out, indent(s.RenderSchema(target, tName, tSchema, depth+1), "        ")...)
		}
	}
	return out
}

// expand resolves ref only at the top level of a chunk, which is what keeps
// the expansion one level deep.
func (s *Store) expand(doc *Doc, ref string, depth int) (*Doc, string, map[string]any, bool) {
	if depth != 0 {
		return nil, "", nil, false
	}
	return s.Resolve(doc, ref)
}

// scalar formats a leaf value for the rendered text, reporting false for
// anything composite: a nested mapping or list belongs to a deeper level than
// this rendering goes.
func scalar(v any) (string, bool) {
	switch t := v.(type) {
	case string:
		return t, true
	case bool, int, int64, uint64, float64:
		return fmt.Sprintf("%v", t), true
	default:
		return "", false
	}
}

// scalarLine renders "key: value" for a leaf, or for a list of leaves as a
// comma-separated run. key carries its own indentation.
func scalarLine(key string, v any) (string, bool) {
	if s, ok := scalar(v); ok {
		return key + ": " + s, true
	}
	items, ok := v.([]any)
	if !ok || len(items) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(items))
	for _, item := range items {
		s, ok := scalar(item)
		if !ok {
			// A list of mappings is structure, not text; it belongs to a
			// deeper level than this rendering goes.
			return "", false
		}
		parts = append(parts, s)
	}
	return key + ": " + strings.Join(parts, ", "), true
}

// indent prefixes every line of text.
func indent(text, prefix string) []string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lines[i] = prefix + line
	}
	return lines
}
