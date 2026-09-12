package web

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/tdoc"
	"github.com/higebu/3gpp-mcp/internal/tdocstore"
	"github.com/higebu/3gpp-mcp/internal/tools"
)

// tdocsData drives the meeting-document lookup page.
type tdocsData struct {
	Query    string
	Meeting  string
	Error    string
	Disabled bool
	Groups   []tdoc.Group
}

// tdocData drives a meeting document page. Meeting documents have no
// version, cross-references or OpenAPI definitions, so the page is a
// simpler cousin of specData.
type tdocData struct {
	Doc      *tdocstore.Document
	TOC      []db.Section
	Sections []sectionRendered
	Current  string
	Prev     *db.Section
	Next     *db.Section
	// Meeting is the meeting named in the request, carried on every link.
	Meeting string
	// Meta is what the cover sheet or LS header says; nil for other
	// documents.
	Meta *tools.TDocMetadata
	// SpecURL links a numbered section of a CR to the same clause of the
	// spec it changes, at the version the CR is written against; empty
	// when the document is not a CR.
	SpecURL func(number string) string
}

// meetingQuery renders the query string that carries a meeting name.
func meetingQuery(meeting string) string {
	if meeting == "" {
		return ""
	}
	return "?meeting=" + url.QueryEscape(meeting)
}

// handleTDocLookup shows the lookup form; with an id it redirects to the
// document so the address bar carries a shareable URL.
func (h *handler) handleTDocLookup(w http.ResponseWriter, r *http.Request) {
	data := tdocsData{
		Query:    strings.TrimSpace(r.URL.Query().Get("id")),
		Meeting:  strings.TrimSpace(r.URL.Query().Get("meeting")),
		Disabled: h.src.TDocs == nil,
		Groups:   tdoc.Groups(),
	}
	if data.Query != "" && !data.Disabled {
		id, ok := tdoc.CanonicalID(data.Query)
		if ok {
			target := "/tdocs/" + url.PathEscape(id)
			if data.Meeting != "" {
				target += "?meeting=" + url.QueryEscape(data.Meeting)
			}
			http.Redirect(w, r, target, http.StatusSeeOther)
			return
		}
		data.Error = fmt.Sprintf("%q is neither a TDoc number (e.g. R1-2509715) nor a path under https://www.3gpp.org/ftp/", data.Query)
	}
	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "tdocs", Data: data}); err != nil {
		log.Printf("template error: %v", err)
	}
}

func (h *handler) handleTDoc(w http.ResponseWriter, r *http.Request) {
	h.renderTDocPage(w, r, r.PathValue("id"), "", false)
}

func (h *handler) handleTDocSection(w http.ResponseWriter, r *http.Request) {
	h.renderTDocPage(w, r, r.PathValue("id"), r.PathValue("number"), true)
}

// renderTDocPage renders one section of a meeting document with the
// document's table of contents beside it. Without an explicit section the
// first one is shown — the preamble, whose stored number is "", when the
// document has one.
func (h *handler) renderTDocPage(w http.ResponseWriter, r *http.Request, id, number string, explicit bool) {
	meeting := r.URL.Query().Get("meeting")

	rec, toc, err := h.src.TDocTOC(r.Context(), id, meeting)
	if err != nil {
		h.renderTDocError(w, err)
		return
	}
	if !explicit {
		number = toc[0].Number
	}

	_, sections, err := h.src.TDocSections(r.Context(), rec.ID, meeting, number, false)
	if err != nil {
		h.renderTDocError(w, err)
		return
	}
	if len(sections) == 0 {
		h.renderError(w, http.StatusNotFound, fmt.Sprintf("Section %q not found in %s", number, rec.ID))
		return
	}

	// References to specifications link into the database; bare "clause
	// 4.2" references stay text, as a meeting document is not a spec.
	rendered := renderSections(sections, renderOpts{
		specID:     rec.ID,
		imageBase:  "/tdocs/" + url.PathEscape(rec.ID) + "/images/",
		imageQuery: meetingQuery(meeting),
		targetInfo: h.targetInfo(r.Context(), "", "", nil),
	})
	prev, next := adjacentSections(toc, number)

	data := tdocData{
		Doc:      rec,
		TOC:      toc,
		Sections: rendered,
		Current:  number,
		Prev:     prev,
		Next:     next,
		Meeting:  meeting,
		Meta:     h.src.TDocMeta(r.Context(), rec.ID, sections),
	}
	if data.Meta != nil && data.Meta.CR != nil && data.Meta.SpecID != "" {
		specID, version := data.Meta.SpecID, data.Meta.CR.CurrentVersion
		data.SpecURL = func(number string) string {
			if number == "" {
				return ""
			}
			u := "/specs/" + url.PathEscape(specID) + "/sections/" + url.PathEscape(number)
			if version != "" {
				u += "?version=" + url.QueryEscape(version)
			}
			return u
		}
	}
	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "tdoc", Data: data}); err != nil {
		log.Printf("template error: %v", err)
	}
}

// renderTDocError maps a document fetch error to a page: the auto-refreshing
// "preparing" page while the fetch runs, 404 when the document cannot be
// served, 500 otherwise.
func (h *handler) renderTDocError(w http.ResponseWriter, err error) {
	var inProgress *tools.FetchInProgressError
	if errors.As(err, &inProgress) {
		h.renderFetching(w, inProgress)
		return
	}
	var unavailable *tools.DocumentUnavailableError
	if errors.As(err, &unavailable) {
		h.renderError(w, http.StatusNotFound, unavailable.Error())
		return
	}
	log.Printf("tdoc error: %v", err)
	h.renderError(w, http.StatusInternalServerError, "Failed to load document")
}

func (h *handler) handleTDocImage(w http.ResponseWriter, r *http.Request) {
	img, err := h.src.TDocImage(r.Context(), r.PathValue("id"), r.URL.Query().Get("meeting"), r.PathValue("name"))
	if err != nil {
		var inProgress *tools.FetchInProgressError
		if errors.As(err, &inProgress) {
			w.Header().Set("Retry-After", "10")
			http.Error(w, "the document is being downloaded; reload shortly", http.StatusAccepted)
			return
		}
		http.NotFound(w, r)
		return
	}
	if img == nil {
		http.NotFound(w, r)
		return
	}
	if !img.LLMReadable {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, nonRenderableImageSVG)
		return
	}
	w.Header().Set("Content-Type", img.MIMEType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write(img.Data)
}
