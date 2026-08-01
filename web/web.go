package web

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/higebu/3gpp-mcp/tools"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// NewServer creates an HTTP handler that serves the 3GPP web viewer. Reads
// that can name a version go through src so archived versions are fetched on
// demand; prebuilt-only features (search, references, OpenAPI) read src.DB
// directly.
func NewServer(src *tools.Source) http.Handler {
	mux := http.NewServeMux()

	h := &handler{src: src, db: src.DB}
	h.initTemplates()

	// Static files
	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticSub)))

	// Pages
	mux.HandleFunc("GET /{$}", h.handleIndex)
	mux.HandleFunc("GET /specs/{specID}", h.handleSpec)
	mux.HandleFunc("GET /specs/{specID}/versions", h.handleVersions)
	mux.HandleFunc("GET /specs/{specID}/sections/{number...}", h.handleSection)
	mux.HandleFunc("GET /specs/{specID}/images/{name...}", h.handleImage)
	mux.HandleFunc("GET /specs/{specID}/openapi", h.handleOpenAPIList)
	mux.HandleFunc("GET /specs/{specID}/openapi/{apiName...}", h.handleOpenAPI)
	mux.HandleFunc("GET /search", h.handleSearch)

	return mux
}
