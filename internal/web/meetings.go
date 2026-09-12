package web

import (
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/tdoc"
	"github.com/higebu/3gpp-mcp/internal/tdocstore"
	"github.com/higebu/3gpp-mcp/internal/tools"
)

// meetingsPerPage bounds the meeting list page; a group has a few hundred
// meetings, so most fit on the first page or two.
const meetingsPerPage = 50

// tdocsPerPage bounds the meeting page; an unfiltered list has well over a
// thousand rows.
const tdocsPerPage = 100

// meetingsData drives the group and meeting list pages.
type meetingsData struct {
	Groups   []tdoc.Group
	Disabled bool
	// Group and Meetings are set on a group's page.
	Group      *tdoc.Group
	Meetings   []tdoc.Meeting
	Total      int
	Page       int
	TotalPages int
	HasPrev    bool
	HasNext    bool
}

// meetingData drives a meeting's TDoc list page.
type meetingData struct {
	Group   tdoc.Group
	Meeting tdoc.Meeting
	List    *tdocstore.List
	Filter  tdocstore.Filter
	Entries []tdoc.Entry
	Total   int
	// Agenda, Types and Statuses populate the filter form.
	Agenda     []tdocstore.AgendaItem
	Types      []string
	Statuses   []string
	Page       int
	TotalPages int
	HasPrev    bool
	HasNext    bool
	// Query is the filter as a query string, for the paging links. It is
	// built from url.Values, so it is safe to splice into an href as is.
	Query template.URL
}

// meetingURL is the page of a meeting, by group (code or name, as a cached
// document records it) and meeting code. Empty when either is unknown.
func meetingURL(group, code string) string {
	g, ok := tdoc.GroupByCode(group)
	if !ok || code == "" {
		return ""
	}
	return "/meetings/" + url.PathEscape(strings.ToLower(g.Code)) + "/" + url.PathEscape(code)
}

// handleMeetingGroups lists the supported groups.
func (h *handler) handleMeetingGroups(w http.ResponseWriter, r *http.Request) {
	data := meetingsData{Groups: tdoc.Groups(), Disabled: h.src.TDocs == nil}
	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "meetings", Data: data}); err != nil {
		log.Printf("template error: %v", err)
	}
}

// handleMeetings lists a group's meetings, newest first.
func (h *handler) handleMeetings(w http.ResponseWriter, r *http.Request) {
	g, meetings, err := h.src.Meetings(r.Context(), r.PathValue("group"))
	if err != nil {
		h.renderTDocError(w, err)
		return
	}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	totalPages := max((len(meetings)+meetingsPerPage-1)/meetingsPerPage, 1)
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * meetingsPerPage
	end := min(start+meetingsPerPage, len(meetings))
	data := meetingsData{
		Groups:     tdoc.Groups(),
		Group:      &g,
		Meetings:   meetings[start:end],
		Total:      len(meetings),
		Page:       page,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
	}
	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "meetings", Data: data}); err != nil {
		log.Printf("template error: %v", err)
	}
}

// handleMeeting shows a meeting's TDoc list with its filters.
func (h *handler) handleMeeting(w http.ResponseWriter, r *http.Request) {
	ml, err := h.src.TDocList(r.Context(), r.PathValue("meeting"), r.PathValue("group"))
	if err != nil {
		h.renderTDocError(w, err)
		return
	}
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	// Bounded before the offset multiplication can overflow; clamped to the
	// last page once the count is known.
	const maxPage = 1_000_000
	if page > maxPage {
		page = maxPage
	}
	f := tdocstore.Filter{
		AgendaItem: strings.TrimSpace(q.Get("agenda_item")),
		Type:       strings.TrimSpace(q.Get("type")),
		Status:     strings.TrimSpace(q.Get("status")),
		Source:     strings.TrimSpace(q.Get("source")),
		Spec:       strings.TrimSpace(q.Get("spec")),
		Query:      strings.TrimSpace(q.Get("q")),
		Limit:      tdocsPerPage,
		Offset:     (page - 1) * tdocsPerPage,
	}
	res, err := h.src.TDocs.ListEntries(ml.Meeting.Dir, f)
	if err != nil {
		log.Printf("tdoc list error: %v", err)
		h.renderError(w, http.StatusInternalServerError, "Failed to load the TDoc list")
		return
	}
	totalPages := max((res.Total+tdocsPerPage-1)/tdocsPerPage, 1)
	if page > totalPages {
		page = totalPages
		f.Offset = (page - 1) * tdocsPerPage
		if res, err = h.src.TDocs.ListEntries(ml.Meeting.Dir, f); err != nil {
			log.Printf("tdoc list error: %v", err)
			h.renderError(w, http.StatusInternalServerError, "Failed to load the TDoc list")
			return
		}
	}
	agenda, err := h.src.TDocs.AgendaItems(ml.Meeting.Dir)
	if err != nil {
		log.Printf("tdoc list error: %v", err)
		h.renderError(w, http.StatusInternalServerError, "Failed to load the TDoc list")
		return
	}
	// The filter form's choices are a convenience: a failure here leaves
	// the selects empty and the list still renders.
	types, _ := h.src.TDocs.Values(ml.Meeting.Dir, "type")
	statuses, _ := h.src.TDocs.Values(ml.Meeting.Dir, "status")

	data := meetingData{
		Group:      ml.Group,
		Meeting:    ml.Meeting,
		List:       ml.List,
		Filter:     f,
		Entries:    res.Entries,
		Total:      res.Total,
		Agenda:     agenda,
		Types:      types,
		Statuses:   statuses,
		Page:       page,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
		Query:      filterQuery(f),
	}
	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "meeting", Data: data}); err != nil {
		log.Printf("template error: %v", err)
	}
}

// filterQuery renders a filter as query parameters, "&"-prefixed, for the
// paging links.
func filterQuery(f tdocstore.Filter) template.URL {
	v := url.Values{}
	for _, kv := range [][2]string{
		{"agenda_item", f.AgendaItem}, {"type", f.Type}, {"status", f.Status},
		{"source", f.Source}, {"spec", f.Spec}, {"q", f.Query},
	} {
		if kv[1] != "" {
			v.Set(kv[0], kv[1])
		}
	}
	if len(v) == 0 {
		return ""
	}
	return template.URL("&" + v.Encode()) //nolint:gosec // built from url.Values, every value encoded
}

// tdocListURL links a TDoc number listed for a meeting to its page, the
// meeting carried along so the number resolves without the meeting index.
func tdocListURL(id, meeting string) string {
	return "/tdocs/" + url.PathEscape(id) + meetingQuery(meeting)
}

// handleMeetingDocument resolves a meeting's report or agenda and redirects
// to the document's page, so the address bar carries the TDoc.
func (h *handler) handleMeetingDocument(w http.ResponseWriter, r *http.Request) {
	kind := tools.MeetingDocumentKind(r.PathValue("kind"))
	if kind != tools.KindReport && kind != tools.KindAgenda {
		http.NotFound(w, r)
		return
	}
	doc, err := h.src.ResolveMeetingDocument(r.Context(), r.PathValue("meeting"), r.PathValue("group"), kind)
	if err != nil {
		h.renderTDocError(w, err)
		return
	}
	http.Redirect(w, r, tdocListURL(doc.Request, doc.RequestMeeting), http.StatusSeeOther)
}
