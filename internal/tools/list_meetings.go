package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/tdoc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListMeetingsInput struct {
	Group  string `json:"group,omitempty" jsonschema:"TSG or working group by TDoc prefix or name: RP or RAN (plenary), R1 or RAN1, R2, R3, R4, R5, R6, SP or SA, S1..S6, CP or CT, C1, C3, C4, C6, GP. Omit to list the groups"`
	Limit  int    `json:"limit,omitempty" jsonschema:"Maximum number of meetings to return, newest first (default: 20)"`
	Offset int    `json:"offset,omitempty" jsonschema:"Number of meetings to skip, for paging (default: 0)"`
}

var ListMeetingsTool = &mcp.Tool{
	Name:        "list_meetings",
	Description: "List the meetings of a 3GPP TSG or working group, newest first, from the group's meeting index: meeting code (R1-123), title (3GPPRAN1#123), town, dates, the range of TDoc numbers allocated to it and its folder on the 3GPP FTP site. Without a group it lists the groups that are supported. Use the meeting code with list_tdocs to see the documents of a meeting, and with get_tdoc to read one.",
}

const defaultMeetingLimit = 20

func HandleListMeetings(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input ListMeetingsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListMeetingsInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(input.Group) == "" {
			return textResult(FormatGroups(tdoc.Groups())), nil, nil
		}
		g, meetings, err := src.Meetings(ctx, input.Group)
		if err != nil {
			return tdocErrorResult(err), nil, nil
		}
		return textResult(FormatMeetings(g, meetings, input.Limit, input.Offset)), nil, nil
	}
}

// FormatGroups lists the supported groups, one per line.
func FormatGroups(groups []tdoc.Group) string {
	var sb strings.Builder
	sb.WriteString("Supported groups (name, TDoc prefix, FTP folder). Call list_meetings with one of them:\n")
	for _, g := range groups {
		fmt.Fprintf(&sb, "%s (%s) — https://www.3gpp.org/ftp/%s/\n", g.Name, g.Code, g.Dir)
	}
	return sb.String()
}

// FormatMeetings renders one page of a group's meetings, one per line. It
// is shared with the CLI's list-meetings command.
func FormatMeetings(g tdoc.Group, meetings []tdoc.Meeting, limit, offset int) string {
	if limit <= 0 {
		limit = defaultMeetingLimit
	}
	if offset < 0 {
		offset = 0
	}
	var sb strings.Builder
	if offset >= len(meetings) {
		fmt.Fprintf(&sb, "[%s (%s) has %d meetings; none at offset %d]\n", g.Name, g.Code, len(meetings), offset)
		return sb.String()
	}
	end := min(offset+limit, len(meetings))
	fmt.Fprintf(&sb, "[%s (%s): %d meetings, newest first; showing %d-%d. Columns: code | title | town | dates | TDoc range | FTP folder]\n", g.Name, g.Code, len(meetings), offset+1, end)
	for _, m := range meetings[offset:end] {
		sb.WriteString(formatMeeting(m))
		sb.WriteByte('\n')
	}
	if end < len(meetings) {
		fmt.Fprintf(&sb, "[%d more; call again with offset=%d]\n", len(meetings)-end, end)
	}
	return sb.String()
}

func formatMeeting(m tdoc.Meeting) string {
	dates := m.Start
	if m.End != "" && m.End != m.Start {
		dates += ".." + m.End
	}
	tdocs := "no TDocs yet"
	if m.FirstTDoc.Prefix != "" {
		tdocs = m.FirstTDoc.String() + ".." + m.LastTDoc.String()
	}
	dir := m.Dir
	if dir == "" {
		dir = "no folder yet"
	}
	town := m.Town
	if town == "" {
		town = "-"
	}
	return fmt.Sprintf("%s | %s | %s | %s | %s | %s", m.Code, m.Title, town, dates, tdocs, dir)
}
