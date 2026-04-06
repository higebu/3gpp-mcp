package web

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/higebu/3gpp-mcp/db"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// NewServer creates an HTTP handler that serves the 3GPP web viewer.
func NewServer(d *db.DB) http.Handler {
	mux := http.NewServeMux()

	h := &handler{db: d}
	h.initTemplates()

	// Static files
	staticSub, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServerFS(staticSub)))

	// Pages
	mux.HandleFunc("GET /{$}", h.handleIndex)
	mux.HandleFunc("GET /specs/{specID}", h.handleSpec)
	mux.HandleFunc("GET /specs/{specID}/sections/{number...}", h.handleSection)
	mux.HandleFunc("GET /specs/{specID}/images/{name...}", h.handleImage)
	mux.HandleFunc("GET /specs/{specID}/openapi", h.handleOpenAPIList)
	mux.HandleFunc("GET /specs/{specID}/openapi/{apiName...}", h.handleOpenAPI)
	mux.HandleFunc("GET /search", h.handleSearch)

	return mux
}
