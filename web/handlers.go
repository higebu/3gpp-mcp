package web

import (
	"errors"
	"fmt"
	htmlpkg "html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/specver"
	"github.com/higebu/3gpp-mcp/tools"
)

type handler struct {
	src   *tools.Source
	db    *db.DB
	tmpls *template.Template
}

// Template data types

// layoutData wraps the page-specific data with fields the shared layout
// (navbar search bar) needs regardless of which page is being rendered.
type layoutData struct {
	Page      string
	Data      any
	NavQuery  string // pre-fills the navbar search query input (only set on /search)
	NavSpecID string // pre-fills the navbar spec ID input with the current spec scope
	Refresh   int    // auto-refresh interval in seconds; 0 disables the meta tag
}

type indexData struct {
	Specs      []db.Spec
	TotalCount int
	Series     string
	Query      string
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
	Prev       *db.Section
	Next       *db.Section
	// Version is the version to carry in generated URLs. It is empty for the
	// database version so canonical URLs stay stable.
	Version  string
	Archived bool
}

// fetchingData drives the "download in progress" page shown while an archived
// version is being fetched and converted.
type fetchingData struct {
	SpecID  string
	Version string
	Images  bool
}

type sectionRendered struct {
	Number  string
	Title   string
	Level   int
	Content template.HTML
}

type searchData struct {
	Query      string
	Results    []db.SearchResult
	SpecID     string
	TotalCount int
	Page       int
	TotalPages int
	HasPrev    bool
	HasNext    bool
	Error      string
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
		// snippetHTML renders an FTS5 snippet: the surrounding text is raw
		// document content (which can itself contain HTML and angle-bracket
		// placeholders like <SUPI>), so everything is escaped and only the
		// <mark> delimiters that db.Search asked snippet() for are restored.
		"snippetHTML": func(s string) template.HTML {
			escaped := htmlpkg.EscapeString(s)
			escaped = strings.ReplaceAll(escaped, "&lt;mark&gt;", "<mark>")
			escaped = strings.ReplaceAll(escaped, "&lt;/mark&gt;", "</mark>")
			return template.HTML(escaped) //nolint:gosec
		},
		"specURL": func(specID string) string {
			return "/specs/" + url.PathEscape(specID)
		},
		"sectionURL": func(specID, number, version string) string {
			u := "/specs/" + url.PathEscape(specID) + "/sections/" + number
			if version != "" {
				u += "?version=" + url.QueryEscape(version)
			}
			return u
		},
		"refURL": refURL,
		// releaseLabel renders a bare release number as "Rel-18"; anything else
		// is shown unchanged.
		"releaseLabel": specver.ReleaseLabel,
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
		"queryEscape": url.QueryEscape,
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
	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "error", Data: data}); err != nil {
		http.Error(w, message, code)
	}
}

func (h *handler) handleIndex(w http.ResponseWriter, r *http.Request) {
	series := r.URL.Query().Get("series")
	query := r.URL.Query().Get("q")
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	// Bound the page so the offset multiplication cannot overflow into a
	// negative value; far beyond any real page count either way.
	const maxPage = 1_000_000
	if page > maxPage {
		page = maxPage
	}
	limit := 50
	offset := (page - 1) * limit

	result, err := h.db.ListSpecs(series, query, limit, offset)
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
		Query:      query,
		Page:       page,
		Limit:      limit,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
	}

	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "index", Data: data}); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (h *handler) handleSpec(w http.ResponseWriter, r *http.Request) {
	specID := r.PathValue("specID")
	h.renderSpecPage(w, r, specID, "")
}

func (h *handler) handleSection(w http.ResponseWriter, r *http.Request) {
	specID := r.PathValue("specID")
	number := r.PathValue("number")
	h.renderSpecPage(w, r, specID, number)
}

// renderVersionError renders the failure of a version-aware read: a fetch
// still running becomes an auto-refreshing "in progress" page, an unknown
// version a 404 naming the versions that do exist.
func (h *handler) renderVersionError(w http.ResponseWriter, err error) {
	var inProgress *tools.FetchInProgressError
	if errors.As(err, &inProgress) {
		h.renderFetching(w, inProgress)
		return
	}
	var unavailable *tools.VersionUnavailableError
	if errors.As(err, &unavailable) {
		h.renderError(w, http.StatusNotFound, unavailable.Error())
		return
	}
	h.renderError(w, http.StatusInternalServerError, "Failed to load version")
}

// renderFetching answers 202 with a page that reloads itself: every reload
// joins the running fetch and waits up to the fetch budget, so the page
// resolves as soon as the download finishes.
func (h *handler) renderFetching(w http.ResponseWriter, inProgress *tools.FetchInProgressError) {
	w.WriteHeader(http.StatusAccepted)
	data := fetchingData{SpecID: inProgress.SpecID, Version: inProgress.Version, Images: inProgress.Images}
	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "fetching", Data: data, Refresh: 10}); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (h *handler) renderSpecPage(w http.ResponseWriter, r *http.Request, specID, number string) {
	version := r.URL.Query().Get("version")

	toc, res, err := h.src.GetTOC(r.Context(), specID, version)
	if err != nil {
		h.renderVersionError(w, err)
		return
	}
	if len(toc) == 0 {
		h.renderError(w, http.StatusNotFound, fmt.Sprintf("Specification %q not found", specID))
		return
	}

	// Default to first section
	if number == "" {
		number = toc[0].Number
	}

	sections, _, err := h.src.GetSection(r.Context(), specID, version, number, false)
	if err != nil {
		h.renderVersionError(w, err)
		return
	}
	if len(sections) == 0 {
		h.renderError(w, http.StatusNotFound, fmt.Sprintf("Section %q not found in %s", number, specID))
		return
	}

	// The database version keeps canonical URLs; an archived version has to
	// carry itself in every generated link.
	urlVersion := ""
	if res.Archived {
		urlVersion = res.Version
	}

	// Cross-references, OpenAPI definitions and the bracketed-reference map
	// only exist for the database version. The database version's bracket map
	// would mislink an archived version — reference numbering moves between
	// versions — so archived pages linkify without one.
	var bracketMap map[string]string
	var openAPIs []db.OpenAPISpec
	var refs []db.Reference
	if !res.Archived {
		bracketMap, _ = h.db.GetBracketMap(specID, "")
		openAPIs, _ = h.db.ListOpenAPI(specID)
		refs, _ = h.db.GetReferences(specID, "", number, db.DirectionOutgoing, false)
	}
	rendered := renderSections(sections, specID, urlVersion, bracketMap)
	prev, next := adjacentSections(toc, number)

	data := specData{
		// GetTOC already joined the version and release of the spec being
		// viewed, so the header can name them without a second query.
		Spec:       &db.Spec{ID: specID, Version: toc[0].Version, Release: toc[0].Release},
		TOC:        toc,
		Sections:   rendered,
		Current:    number,
		References: refs,
		OpenAPIs:   openAPIs,
		Prev:       prev,
		Next:       next,
		Version:    urlVersion,
		Archived:   res.Archived,
	}

	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "spec", Data: data, NavSpecID: specID}); err != nil {
		log.Printf("template error: %v", err)
	}
}

// adjacentSections returns the sections immediately before and after number
// in toc's document order (see db.GetTOC), for "previous/next chapter"
// navigation. Either return value is nil when number is the first/last
// section in the TOC.
func adjacentSections(toc []db.Section, number string) (prev, next *db.Section) {
	for i := range toc {
		if toc[i].Number != number {
			continue
		}
		if i > 0 {
			prev = &toc[i-1]
		}
		if i < len(toc)-1 {
			next = &toc[i+1]
		}
		return prev, next
	}
	return nil, nil
}

func (h *handler) handleImage(w http.ResponseWriter, r *http.Request) {
	specID := r.PathValue("specID")
	name := r.PathValue("name")
	version := r.URL.Query().Get("version")

	img, _, err := h.src.GetImage(r.Context(), specID, version, name)
	if err != nil {
		// An archived version's images download on first use; tell the
		// browser to come back rather than caching a 404.
		var inProgress *tools.FetchInProgressError
		if errors.As(err, &inProgress) {
			w.Header().Set("Retry-After", "10")
			http.Error(w, "images are being downloaded; reload shortly", http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
		return
	}
	if img == nil {
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
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	// Same bound as handleIndex: keeps the offset multiplication from
	// overflowing while staying far beyond any real page count.
	const maxPage = 1_000_000
	if page > maxPage {
		page = maxPage
	}
	limit := 50
	offset := (page - 1) * limit

	data := searchData{
		Query:  query,
		SpecID: specID,
		Page:   page,
	}

	if query != "" {
		var specIDs []string
		if specID != "" {
			specIDs = []string{specID}
		}
		result, err := h.db.Search(query, specIDs, limit, offset)
		if err != nil {
			log.Printf("Search error: %v", err)
			data.Error = "Search failed. Check the query syntax and try again."
		} else {
			totalPages := (result.TotalCount + limit - 1) / limit
			if totalPages < 1 {
				totalPages = 1
			}
			data.Results = result.Results
			data.TotalCount = result.TotalCount
			data.TotalPages = totalPages
			data.HasPrev = page > 1
			data.HasNext = page < totalPages
		}
	}

	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "search", Data: data, NavQuery: query, NavSpecID: specID}); err != nil {
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

	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "openapi_list", Data: data, NavSpecID: specID}); err != nil {
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

	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "openapi", Data: data, NavSpecID: specID}); err != nil {
		log.Printf("template error: %v", err)
	}
}

// Helper functions

func renderSections(sections []db.Section, specID, version string, bracketMap map[string]string) []sectionRendered {
	rendered := make([]sectionRendered, len(sections))
	for i, s := range sections {
		rendered[i] = sectionRendered{
			Number:  s.Number,
			Title:   s.Title,
			Level:   s.Level,
			Content: template.HTML(renderMarkdown(s.Content, specID, version, bracketMap)), //nolint:gosec
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
