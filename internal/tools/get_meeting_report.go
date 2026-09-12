package tools

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetMeetingReportInput struct {
	Meeting       string `json:"meeting" jsonschema:"required,Meeting as listed by list_meetings: the code (R1-123, S2-172), the #-form (RAN1#123) or the folder path"`
	Group         string `json:"group,omitempty" jsonschema:"Group of the meeting (R1, RAN1, ...), only needed when the meeting name does not carry it"`
	Kind          string `json:"kind,omitempty" jsonschema:"report (the minutes, default) or agenda (the final agenda)"`
	SectionNumber string `json:"section_number,omitempty" jsonschema:"Section to read, exactly as listed in the header's Sections line (e.g. 7.1, Agreement (2)); default: the whole document"`
	Offset        int    `json:"offset,omitempty" jsonschema:"Start line number (0-based, default: 0)"`
	MaxLines      int    `json:"max_lines,omitempty" jsonschema:"Maximum number of lines to return (default: 200)"`
	MaxChars      int    `json:"max_chars,omitempty" jsonschema:"Maximum number of characters to return (can be combined with max_lines)"`
}

var GetMeetingReportTool = &mcp.Tool{
	Name:        "get_meeting_report",
	Description: "Read a 3GPP meeting's report (minutes: the agreements, conclusions and the outcome of every document) or its agenda as Markdown, named by the meeting alone. The report is located for you: it is the TDoc of type report that the following meeting approved, or failing that the minutes file in the meeting's Report folder. The output is a get_tdoc page: a header naming the document found, its sections, then the text, paginated (default 200 lines); use offset and max_lines to read on, or section_number to read one agenda item's section. The document is downloaded and converted on first use; if told the fetch is in progress, call again.",
}

func HandleGetMeetingReport(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input GetMeetingReportInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetMeetingReportInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(input.Meeting) == "" {
			return errorResult("meeting is required"), nil, nil
		}
		kind := MeetingDocumentKind(strings.ToLower(strings.TrimSpace(input.Kind)))
		if kind == "" {
			kind = KindReport
		}
		doc, err := src.ResolveMeetingDocument(ctx, input.Meeting, input.Group, kind)
		if err != nil {
			return tdocErrorResult(err), nil, nil
		}
		return renderTDoc(ctx, src, doc.Request, doc.RequestMeeting, "[Meeting "+doc.Note+"]", tdocPage{
			Section: input.SectionNumber, Offset: input.Offset, MaxLines: input.MaxLines, MaxChars: input.MaxChars,
		}), nil, nil
	}
}
