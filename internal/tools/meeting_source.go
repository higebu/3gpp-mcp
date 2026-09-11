package tools

import (
	"context"
	"errors"
	"fmt"

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
