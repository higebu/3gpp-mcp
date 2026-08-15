package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrOpenAPINotFound reports that no stored OpenAPI document matches a
// (spec_id, api_name) request, even through the filename fallback of
// GetOpenAPIResolved. Callers turn it into a hint about what does exist
// rather than surfacing a bare SQL error.
var ErrOpenAPINotFound = errors.New("openapi definition not found")

// openapiNameMatch matches a requested api_name against a row: the api_name
// itself, or the stored filename with or without its .yaml extension, all
// case-insensitively — models copy file names out of prose
// ("TS29571_CommonData") as often as API names ("Nudm_UECM").
const openapiNameMatch = "(LOWER(api_name) = LOWER(?) OR LOWER(COALESCE(filename, '')) IN (LOWER(?), LOWER(? || '.yaml')))"

// GetOpenAPIResolved returns the content stored for (specID, apiName),
// resolving apiName as GetOpenAPI does not: case-insensitively, and by
// filename with or without the .yaml extension. When several rows qualify the
// exact api_name match wins. A miss is ErrOpenAPINotFound.
func (d *DB) GetOpenAPIResolved(ctx context.Context, specID, apiName string) (string, error) {
	var content string
	err := d.conn.QueryRowContext(ctx,
		"SELECT content FROM openapi_specs WHERE spec_id = ? AND "+openapiNameMatch+
			" ORDER BY CASE WHEN api_name = ? THEN 0 WHEN LOWER(api_name) = LOWER(?) THEN 1 ELSE 2 END LIMIT 1",
		specID, apiName, apiName, apiName, apiName, apiName,
	).Scan(&content)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrOpenAPINotFound
	}
	if err != nil {
		return "", fmt.Errorf("get openapi: %w", err)
	}
	return content, nil
}

// FindOpenAPIByAPIName returns every stored OpenAPI document whose api_name
// or filename matches apiName, across all specifications. It serves the
// wrong-document hint: a get_openapi call that named an api_name under the
// wrong spec_id learns which specification actually provides it.
func (d *DB) FindOpenAPIByAPIName(ctx context.Context, apiName string) ([]OpenAPISpec, error) {
	rows, err := d.conn.QueryContext(ctx,
		"SELECT spec_id, api_name FROM openapi_specs WHERE "+openapiNameMatch+" ORDER BY spec_id, api_name",
		apiName, apiName, apiName,
	)
	if err != nil {
		return nil, fmt.Errorf("find openapi by api name: %w", err)
	}
	defer rows.Close()

	var specs []OpenAPISpec
	for rows.Next() {
		var s OpenAPISpec
		if err := rows.Scan(&s.SpecID, &s.APIName); err != nil {
			return nil, fmt.Errorf("scan openapi by api name: %w", err)
		}
		specs = append(specs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find openapi by api name: iterate: %w", err)
	}
	return specs, nil
}

// OpenAPISchemaSources returns the documents whose components.schemas define
// the named schema, answered from the openapi_chunks index. A database
// without the index reports ErrNoOpenAPIIndex, which callers treat as "no
// hint" rather than as a failure.
func (d *DB) OpenAPISchemaSources(ctx context.Context, schema string) ([]OpenAPISpec, error) {
	ok, err := d.HasOpenAPIIndex(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoOpenAPIIndex
	}

	rows, err := d.conn.QueryContext(ctx,
		"SELECT DISTINCT spec_id, api_name FROM openapi_chunks WHERE kind = ? AND LOWER(name) = LOWER(?) ORDER BY spec_id, api_name",
		OpenAPIKindSchema, schema,
	)
	if err != nil {
		return nil, fmt.Errorf("find openapi schema sources: %w", err)
	}
	defer rows.Close()

	var specs []OpenAPISpec
	for rows.Next() {
		var s OpenAPISpec
		if err := rows.Scan(&s.SpecID, &s.APIName); err != nil {
			return nil, fmt.Errorf("scan openapi schema source: %w", err)
		}
		specs = append(specs, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("find openapi schema sources: iterate: %w", err)
	}
	return specs, nil
}
