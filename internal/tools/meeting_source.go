package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/tdoc"
	"github.com/higebu/3gpp-mcp/internal/tdocstore"
)

// Meetings returns a group's meetings, newest first, from the group's
// DynaReport page. It needs only the network, but is gated on the TDoc
// store like the other meeting features so --no-fetch turns them all off.
func (s *Source) Meetings(ctx context.Context, group string) (tdoc.Group, []tdoc.Meeting, error) {
	if s.TDocs == nil {
		return tdoc.Group{}, nil, &DocumentUnavailableError{ID: group, Reason: "on-demand fetching of meeting documents is disabled"}
	}
	g, ok := tdoc.GroupByCode(group)
	if !ok {
		return tdoc.Group{}, nil, &DocumentUnavailableError{ID: group, Reason: fmt.Sprintf("unknown group %q", group), Err: tdoc.ErrNotFound}
	}
	meetings, err := tdoc.FetchMeetings(ctx, s.Client, g, s.UseCache)
	if err != nil {
		return tdoc.Group{}, nil, &DocumentUnavailableError{ID: g.Name, Reason: err.Error(), Err: err}
	}
	return g, meetings, nil
}

// MeetingList is a meeting together with its cached TDoc list.
type MeetingList struct {
	Group   tdoc.Group
	Meeting tdoc.Meeting
	List    *tdocstore.List
}

// TDocList resolves a meeting (see tdoc.ResolveMeeting) and makes its TDoc
// list available, downloading it on first use.
func (s *Source) TDocList(ctx context.Context, meeting, group string) (*MeetingList, error) {
	if s.TDocs == nil {
		return nil, &DocumentUnavailableError{ID: meeting, Reason: "on-demand fetching of meeting documents is disabled"}
	}
	g, m, err := tdoc.ResolveMeeting(ctx, s.Client, meeting, group, s.UseCache)
	if err != nil {
		return nil, &DocumentUnavailableError{ID: meeting, Reason: err.Error(), Err: err}
	}
	switch err := s.TDocs.EnsureList(ctx, g, m, s.Budget); {
	case err == nil:
	case errors.Is(err, tdocstore.ErrInProgress):
		return nil, &FetchInProgressError{SpecID: m.Title, List: true}
	default:
		return nil, &DocumentUnavailableError{ID: m.Title, Reason: err.Error(), Err: err}
	}
	list, err := s.TDocs.GetList(m.Dir)
	if err != nil {
		return nil, err
	}
	if list == nil {
		// Evicted between EnsureList and GetList; a retry re-fetches.
		return nil, &FetchInProgressError{SpecID: m.Title, List: true}
	}
	return &MeetingList{Group: g, Meeting: m, List: list}, nil
}

// MeetingDocumentKind names a document a meeting has exactly one of.
type MeetingDocumentKind string

const (
	// KindReport is the meeting's minutes.
	KindReport MeetingDocumentKind = "report"
	// KindAgenda is the meeting's (final) agenda.
	KindAgenda MeetingDocumentKind = "agenda"
)

// MeetingDocument is the document a meeting is identified by (its report or
// its agenda), resolved to a request get_tdoc can read.
type MeetingDocument struct {
	Group   tdoc.Group
	Meeting tdoc.Meeting
	Kind    MeetingDocumentKind
	// Request is the TDoc number or FTP path to read; RequestMeeting the
	// meeting to resolve it under.
	Request        string
	RequestMeeting string
	// Title is the document's title as the TDoc list gives it; empty for a
	// file found in the Report/ folder.
	Title string
	// Note says where the document was found, for the reader.
	Note string
}

// ResolveMeetingDocument finds a meeting's report or agenda. The agenda is
// the final (unrevised) TDoc of type "agenda" in the meeting's own list. The
// report is submitted to the following meeting as a TDoc of type "report",
// so it is looked for in the lists of the meetings that came after; failing
// that, the meeting's Report/ folder is searched for the minutes file.
func (s *Source) ResolveMeetingDocument(ctx context.Context, meeting, group string, kind MeetingDocumentKind) (*MeetingDocument, error) {
	if s.TDocs == nil {
		return nil, &DocumentUnavailableError{ID: meeting, Reason: "on-demand fetching of meeting documents is disabled"}
	}
	g, m, err := tdoc.ResolveMeeting(ctx, s.Client, meeting, group, s.UseCache)
	if err != nil {
		return nil, &DocumentUnavailableError{ID: meeting, Reason: err.Error(), Err: err}
	}
	doc := &MeetingDocument{Group: g, Meeting: m, Kind: kind}
	switch kind {
	case KindAgenda:
		ml, err := s.TDocList(ctx, m.Code, g.Code)
		if err != nil {
			return nil, err
		}
		e, ok, err := finalOfType(s, ml, "agenda", func(e tdoc.Entry) bool { return strings.Contains(strings.ToLower(e.Title), "agenda") })
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &DocumentUnavailableError{ID: m.Title, Reason: "its TDoc list has no document of type agenda", Err: tdoc.ErrNotFound}
		}
		doc.Request, doc.RequestMeeting, doc.Title = e.TDoc, m.Code, e.Title
		doc.Note = fmt.Sprintf("agenda of %s: %s from the meeting's TDoc list", m.Title, e.TDoc)
		return doc, nil
	case KindReport:
	default:
		return nil, &DocumentUnavailableError{ID: meeting, Reason: fmt.Sprintf("unknown document kind %q; use report or agenda", kind)}
	}

	// The report: a TDoc of one of the following meetings.
	meetings, err := tdoc.FetchMeetings(ctx, s.Client, g, s.UseCache)
	if err != nil {
		return nil, &DocumentUnavailableError{ID: m.Title, Reason: err.Error(), Err: err}
	}
	var lastErr error
	for _, next := range followingMeetings(meetings, m, 3) {
		ml, err := s.TDocList(ctx, next.Code, g.Code)
		if err != nil {
			var inProgress *FetchInProgressError
			if errors.As(err, &inProgress) {
				return nil, err
			}
			lastErr = err
			continue
		}
		e, ok, err := finalOfType(s, ml, "report", func(e tdoc.Entry) bool { return tdoc.ReportTitleMatches(e.Title, g, m) })
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		doc.Request, doc.RequestMeeting, doc.Title = e.TDoc, next.Code, e.Title
		doc.Note = fmt.Sprintf("report of %s: %s (%s) from the TDoc list of %s", m.Title, e.TDoc, e.Title, next.Title)
		return doc, nil
	}

	// The minutes file in the meeting's own Report/ folder.
	path, err := tdoc.FindReportFile(ctx, s.Client, m)
	if err != nil {
		reason := fmt.Sprintf("no following meeting lists a report of it, and %v", err)
		if lastErr != nil {
			reason += fmt.Sprintf(" (%v)", lastErr)
		}
		return nil, &DocumentUnavailableError{ID: m.Title, Reason: reason, Err: err}
	}
	doc.Request = path
	doc.Note = fmt.Sprintf("report of %s: %s from the meeting's Report folder", m.Title, path)
	return doc, nil
}

// followingMeetings returns up to n meetings held after m, nearest first,
// that have a document folder. The index is newest first.
func followingMeetings(meetings []tdoc.Meeting, m tdoc.Meeting, n int) []tdoc.Meeting {
	var out []tdoc.Meeting
	for i := len(meetings) - 1; i >= 0; i-- {
		if meetings[i].Code == m.Code {
			for j := i - 1; j >= 0 && len(out) < n; j-- {
				if meetings[j].DocsDir != "" {
					out = append(out, meetings[j])
				}
			}
			break
		}
	}
	return out
}

// finalOfType picks, among a list's entries of a type that satisfy want,
// the one at the end of its revision chain, preferring an approved or
// agreed one. A withdrawn document is never the final one. A cache read
// failure is returned, not reported as an absent document.
func finalOfType(s *Source, ml *MeetingList, typ string, want func(tdoc.Entry) bool) (tdoc.Entry, bool, error) {
	res, err := s.TDocs.ListEntries(ml.Meeting.Dir, tdocstore.Filter{Type: typ})
	if err != nil {
		return tdoc.Entry{}, false, fmt.Errorf("read the TDoc list of %s: %w", ml.Meeting.Code, err)
	}
	candidate := func(e tdoc.Entry) bool { return want(e) && !strings.EqualFold(e.Status, "withdrawn") }
	var best tdoc.Entry
	found := false
	for _, e := range res.Entries {
		if !candidate(e) || e.RevisedTo != "" {
			continue
		}
		approved := strings.EqualFold(e.Status, "approved") || strings.EqualFold(e.Status, "agreed")
		if !found || approved {
			best, found = e, true
			if approved {
				break
			}
		}
	}
	if !found {
		// Every candidate was revised: the chain's last link may be missing
		// from the list; fall back to the last candidate in list order.
		for _, e := range res.Entries {
			if candidate(e) {
				best, found = e, true
			}
		}
	}
	return best, found, nil
}
