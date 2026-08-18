package tools

import (
	"net/http"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer builds the MCP server and registers every tool. It is shared by
// the serve command, its transport tests and the e2e harness. version is the
// binary's build version reported in the server implementation info.
func NewServer(d *db.DB, src *Source, version string) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "3gpp-mcp",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "3GPP specification server. Use list_specs to find specifications, get_toc to browse structure, get_section to read specification document text (architecture, procedures, requirements), and search to find relevant sections. Use get_references to explore cross-references between specifications (outgoing: what a section references; incoming: what references a spec). For 5G API details (HTTP methods, request/response bodies, paths, schemas, data models) from TS 29.xxx series, use list_openapi to discover APIs and get_openapi to read their OpenAPI definitions, and search_openapi when you know the data type or endpoint but not which API document defines it. Always prefer get_openapi over get_section when looking up API request/response formats or data type definitions. For ASN.1-specified protocols (RRC TS 38.331/36.331, NGAP TS 38.413, S1AP TS 36.413, XnAP, F1AP, LPP TS 37.355, ...), use get_asn1 with a type, IE or constant name to get its definition and defining section directly — the ASN.1 clauses are far larger than one get_section page. If you do not know which specification defines the name, omit spec_id and the name is resolved across the whole database; get_asn1 with a spec_id and no name lists what that specification defines.\n\n" +
			"Notation: section text is Markdown. Figures are `![...](image://NAME)` links. Formulas are LaTeX — a standalone equation is a ```latex code block whose equation number is kept as `\\tag{7.3-1}`, and a formula inside a sentence or a table cell is delimited with `$...$` or `$$...$$`. A paragraph's leading indentation — the nesting of requirement and condition lists — is preserved as no-break spaces (U+00A0), one tab of the source document being four.\n\n" +
			"Versions: the database holds one version per specification, and every get_section, get_toc and search result names the specification and version it came from. To compare a procedure across releases, call list_versions to see which versions exist, then use compare_versions: without section_number it summarizes which sections were added, removed or changed between two versions, and with section_number it returns a line-level diff of that section's text. A version that is not in the database is downloaded and converted on first use, which takes up to a few minutes for a large specification; when that happens the tool says so and you should call it again with the same arguments. Section numbers move between releases, so check get_toc for the older version before reading a section of it. get_image and list_images also accept a version: an archived version's images are downloaded on their own first use, again taking up to a few minutes before a retry succeeds. search and get_references only have data for the version in the database.",
	})

	mcp.AddTool(s, ListSpecsTool, HandleListSpecs(d))
	mcp.AddTool(s, ListVersionsTool, HandleListVersions(src))
	mcp.AddTool(s, GetTOCTool, HandleGetTOC(src))
	mcp.AddTool(s, GetSectionTool, HandleGetSection(src))
	mcp.AddTool(s, GetASN1Tool, HandleGetASN1(src))
	mcp.AddTool(s, CompareVersionsTool, HandleCompareVersions(src))
	mcp.AddTool(s, SearchTool, HandleSearch(d))
	mcp.AddTool(s, ListOpenAPITool, HandleListOpenAPI(d))
	mcp.AddTool(s, GetOpenAPITool, HandleGetOpenAPI(d))
	mcp.AddTool(s, SearchOpenAPITool, HandleSearchOpenAPI(d))
	mcp.AddTool(s, GetReferencesTool, HandleGetReferences(d))
	mcp.AddTool(s, ListImagesTool, HandleListImages(src))
	mcp.AddTool(s, GetImageTool, HandleGetImage(src))

	return s
}

// NewStreamableHTTPHandler wraps s in the streamable HTTP transport in
// stateless mode. Stateless mode is required to serve protocol version
// 2026-07-28, whose lifecycle has no initialize handshake or Mcp-Session-Id.
// Older clients still work: each request runs in a temporary session. This
// server never initiates server->client requests, so nothing is lost by not
// keeping sessions.
//
// Shared by cmdServe's buildHTTPHandler and the e2e harness so both serve the
// exact same handler; mounting it under a prefix (e.g. /mcp/ next to the web
// viewer, via http.StripPrefix) is the caller's choice.
func NewStreamableHTTPHandler(s *mcp.Server) http.Handler {
	return mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
}
