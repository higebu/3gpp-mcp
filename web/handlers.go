package web

import (
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/higebu/3gpp-mcp/db"
)

type handler struct {
	db    *db.DB
	tmpls *template.Template
}

// Template data types

type indexData struct {
	Specs      []db.Spec
	TotalCount int
	Series     string
	Page       int
	Limit      int
	TotalPages int
	HasPrev    bool
	HasNext    bool
}

type specData struct {
	Spec       *db.Spec
	TOC        []db.Section
	Sections   []sectionRendered
	Current    string
	References []db.Reference
	OpenAPIs   []db.OpenAPISpec
}

type sectionRendered struct {
	Number  string
	Title   string
	Level   int
	Content template.HTML
}

type searchData struct {
	Query   string
	Results []db.SearchResult
	SpecID  string
}

type openAPIListData struct {
	SpecID string
	APIs   []db.OpenAPISpec
}

type openAPIData struct {
	SpecID  string
	APIName string
	Content template.HTML
}

type errorData struct {
	Code    int
	Message string
}

func (h *handler) initTemplates() {
	funcMap := template.FuncMap{
		"safeHTML": func(s string) template.HTML {
			return template.HTML(s) //nolint:gosec
		},
		"specURL": func(specID string) string {
			return "/specs/" + url.PathEscape(specID)
		},
		"sectionURL": func(specID, number string) string {
			return "/specs/" + url.PathEscape(specID) + "/sections/" + number
		},
		"refURL": refURL,
		"add": func(a, b int) int {
			return a + b
		},
		"sub": func(a, b int) int {
			return a - b
		},
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i + 1
			}
			return s
		},
		"isActive": func(current, number string) bool {
			return current == number
		},
		"indent": func(level int) int {
			if level > 1 {
				return (level - 1) * 16
			}
			return 0
		},
		"highlightYAML": func(s string) template.HTML {
			return template.HTML(highlightYAML(s)) //nolint:gosec
		},
		"isExternalRef": isExternalRef,
	}

	h.tmpls = template.Must(template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/*.html"))
}

func (h *handler) renderError(w http.ResponseWriter, code int, message string) {
	w.WriteHeader(code)
	data := errorData{Code: code, Message: message}
	if err := h.tmpls.ExecuteTemplate(w, "layout.html", struct {
		Page string
		Data errorData
	}{Page: "error", Data: data}); err != nil {
		http.Error(w, message, code)
	}
}

func (h *handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	series := r.URL.Query().Get("series")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	limit := 50
	offset := (page - 1) * limit

	result, err := h.db.ListSpecs(series, limit, offset)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Failed to load specifications")
		log.Printf("ListSpecs error: %v", err)
		return
	}

	totalPages := (result.TotalCount + limit - 1) / limit
	if totalPages < 1 {
		totalPages = 1
	}

	data := indexData{
		Specs:      result.Specs,
		TotalCount: result.TotalCount,
		Series:     series,
		Page:       page,
		Limit:      limit,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
	}

	if err := h.tmpls.ExecuteTemplate(w, "layout.html", struct {
		Page string
		Data indexData
	}{Page: "index", Data: data}); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (h *handler) handleSpec(w http.ResponseWriter, r *http.Request) {
	specID := r.PathValue("specID")
	h.renderSpecPage(w, specID, "")
}

func (h *handler) handleSection(w http.ResponseWriter, r *http.Request) {
	specID := r.PathValue("specID")
	number := r.PathValue("number")
	h.renderSpecPage(w, specID, number)
}

func (h *handler) renderSpecPage(w http.ResponseWriter, specID, number string) {
	toc, err := h.db.GetTOC(specID)
	if err != nil || len(toc) == 0 {
		h.renderError(w, http.StatusNotFound, fmt.Sprintf("Specification %q not found", specID))
		return
	}

	// Default to first section
	if number == "" {
		number = toc[0].Number
	}

	sections, err := h.db.GetSection(specID, number, false)
	if err != nil || len(sections) == 0 {
		h.renderError(w, http.StatusNotFound, fmt.Sprintf("Section %q not found in %s", number, specID))
		return
	}

	bracketMap, _ := h.db.GetBracketMap(specID)
	rendered := renderSections(sections, specID, bracketMap)
	openAPIs, _ := h.db.ListOpenAPI(specID)
	refs, _ := h.db.GetReferences(specID, number, db.DirectionOutgoing, false)

	data := specData{
		Spec:       &db.Spec{ID: specID},
		TOC:        toc,
		Sections:   rendered,
		Current:    number,
		References: refs,
		OpenAPIs:   openAPIs,
	}

	if err := h.tmpls.ExecuteTemplate(w, "layout.html", struct {
		Page string
		Data specData
	}{Page: "spec", Data: data}); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (h *handler) handleImage(w http.ResponseWriter, r *http.Request) {
	specID := r.PathValue("specID")
	name := r.PathValue("name")

	img, err := h.db.GetImage(specID, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", img.MIMEType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(img.Data)
}

func (h *handler) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query().Get("q")
	specID := r.URL.Query().Get("spec_id")

	data := searchData{
		Query:  query,
		SpecID: specID,
	}

	if query != "" {
		var specIDs []string
		if specID != "" {
			specIDs = []string{specID}
		}
		results, err := h.db.Search(query, specIDs, 50)
		if err != nil {
			log.Printf("Search error: %v", err)
		} else {
			data.Results = results
		}
	}

	if err := h.tmpls.ExecuteTemplate(w, "layout.html", struct {
		Page string
		Data searchData
	}{Page: "search", Data: data}); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (h *handler) handleOpenAPIList(w http.ResponseWriter, r *http.Request) {
	specID := r.PathValue("specID")

	apis, err := h.db.ListOpenAPI(specID)
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Failed to load OpenAPI definitions")
		return
	}

	data := openAPIListData{
		SpecID: specID,
		APIs:   apis,
	}

	if err := h.tmpls.ExecuteTemplate(w, "layout.html", struct {
		Page string
		Data openAPIListData
	}{Page: "openapi_list", Data: data}); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (h *handler) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	specID := r.PathValue("specID")
	apiName := r.PathValue("apiName")

	content, err := h.db.GetOpenAPI(specID, apiName)
	if err != nil {
		h.renderError(w, http.StatusNotFound, fmt.Sprintf("OpenAPI definition %q not found", apiName))
		return
	}

	data := openAPIData{
		SpecID:  specID,
		APIName: apiName,
		Content: template.HTML(highlightYAML(content)), //nolint:gosec
	}

	if err := h.tmpls.ExecuteTemplate(w, "layout.html", struct {
		Page string
		Data openAPIData
	}{Page: "openapi", Data: data}); err != nil {
		log.Printf("template error: %v", err)
	}
}

// Helper functions

func renderSections(sections []db.Section, specID string, bracketMap map[string]string) []sectionRendered {
	rendered := make([]sectionRendered, len(sections))
	for i, s := range sections {
		rendered[i] = sectionRendered{
			Number:  s.Number,
			Title:   s.Title,
			Level:   s.Level,
			Content: template.HTML(renderMarkdown(s.Content, specID, bracketMap)), //nolint:gosec
		}
	}
	return rendered
}

func refURL(ref db.Reference) string {
	target := ref.TargetSpec
	if strings.HasPrefix(target, "RFC ") {
		num := strings.TrimPrefix(target, "RFC ")
		u := "https://www.rfc-editor.org/rfc/rfc" + num
		if ref.TargetSection != "" {
			u += "#section-" + ref.TargetSection
		}
		return u
	}
	u := "/specs/" + url.PathEscape(target)
	if ref.TargetSection != "" {
		u += "/sections/" + ref.TargetSection
	}
	return u
}

func isExternalRef(ref db.Reference) bool {
	return strings.HasPrefix(ref.TargetSpec, "RFC ")
}
