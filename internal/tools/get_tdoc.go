package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/tdocstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type GetTDocInput struct {
	TDocID        string `json:"tdoc_id" jsonschema:"required,TDoc number (e.g. R1-2509715, S2-2510076, RP-253000), or the path of a zip/.docx under https://www.3gpp.org/ftp/ for a document that has no TDoc number, such as a meeting report (tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1#123_v100.zip)"`
	Meeting       string `json:"meeting,omitempty" jsonschema:"Meeting the TDoc belongs to, only needed when the number is not found by itself: R1-123, RAN1#123, the folder name TSGR1_123, or the folder path"`
	SectionNumber string `json:"section_number,omitempty" jsonschema:"Section to read: the number exactly as listed in the header's Sections line (the part before the parenthesised title, e.g. 1, 2.3, Agreement (2)); the cover sheet / header section is named preamble. Default: the whole document"`
	Offset        int    `json:"offset,omitempty" jsonschema:"Start line number (0-based, default: 0)"`
	MaxLines      int    `json:"max_lines,omitempty" jsonschema:"Maximum number of lines to return (default: 200)"`
	MaxChars      int    `json:"max_chars,omitempty" jsonschema:"Maximum number of characters to return (can be combined with max_lines)"`
}

var GetTDocTool = &mcp.Tool{
	Name:        "get_tdoc",
	Description: "Read a 3GPP meeting document (TDoc) as Markdown: a contribution, change request (CR), liaison statement (LS), agenda or meeting report from the 3GPP FTP site, named by TDoc number (R1-2509715) or FTP path. The document is downloaded and converted on first use, which takes seconds to a minute; if the tool says the fetch is still in progress, call it again with the same arguments. The output starts with a header naming the meeting, the source URL, the files in the download and the document's sections, followed by the text. A CR's cover sheet and an LS's header are in the section named preamble. Long documents are paginated (default 200 lines): use offset and max_lines to read on, or section_number to read one section. search does not cover TDocs.",
}

// preambleName is how a caller asks for the preamble section, whose stored
// number is "" — an empty section_number means the whole document.
const preambleName = "preamble"

func HandleGetTDoc(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input GetTDocInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input GetTDocInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(input.TDocID) == "" {
			return errorResult("tdoc_id is required"), nil, nil
		}
		return renderTDoc(ctx, src, input.TDocID, input.Meeting, "", tdocPage{
			Section: input.SectionNumber, Offset: input.Offset, MaxLines: input.MaxLines, MaxChars: input.MaxChars,
		}), nil, nil
	}
}

// tdocPage is the part of a document a tool call asks for.
type tdocPage struct {
	Section  string
	Offset   int
	MaxLines int
	MaxChars int
}

// renderTDoc reads a meeting document and renders one page of it with the
// provenance header, the metadata pulled out of its cover sheet or LS
// header, and the section list. lead, when set, is a line placed above the
// header naming what the document is (the meeting report it was resolved
// as, for instance).
func renderTDoc(ctx context.Context, src *Source, request, meeting, lead string, page tdocPage) *mcp.CallToolResult {
	number := "*"
	wholeDocument := true
	if n := strings.TrimSpace(page.Section); n != "" {
		wholeDocument = false
		number = n
		if strings.EqualFold(n, preambleName) {
			number = ""
		}
	}

	rec, sections, err := src.TDocSections(ctx, request, meeting, number, false)
	if err != nil {
		return tdocErrorResult(err)
	}
	if len(sections) == 0 {
		return errorResult(fmt.Sprintf("section %s not found in %s; call get_tdoc without section_number to see its sections", page.Section, rec.ID))
	}

	var full strings.Builder
	for _, s := range sections {
		full.WriteString(s.Content)
		full.WriteString("\n\n")
	}
	result := paginateText(full.String(), page.Offset, page.MaxLines, page.MaxChars)

	header := TDocHeader(rec)
	if lead != "" {
		header = lead + "\n" + header
	}
	if meta := src.TDocMeta(ctx, rec.ID, sections).Text(); meta != "" {
		header += "\n" + meta
	}
	if wholeDocument {
		header += "\n" + tdocSectionList(sections)
	} else {
		header += fmt.Sprintf("\nSection: %s", sectionLabel(sections[0]))
	}
	return prependLine(header, result)
}

// TDocMeta reads the cover sheet or LS header of a cached document. The
// preamble is taken from sections when it is among them and read from the
// cache otherwise, so a single-section read still shows what the document
// is. Nil for a document that is neither a CR nor an LS. It is shared with
// the CLI's get-tdoc command and the web viewer.
func (s *Source) TDocMeta(ctx context.Context, id string, sections []db.Section) *TDocMetadata {
	preamble := ""
	for _, sec := range sections {
		if sec.Number == "" {
			preamble = sec.Content
			break
		}
	}
	if preamble == "" {
		secs, err := s.TDocs.GetSection(id, "", false)
		if err != nil || len(secs) == 0 {
			return nil
		}
		preamble = secs[0].Content
	}
	return ParseTDocMetadata(ctx, s.DB, preamble)
}

// tdocErrorResult renders an error from a meeting-document fetch. A fetch
// still running is not a failure, so it is reported as ordinary text.
func tdocErrorResult(err error) *mcp.CallToolResult {
	var inProgress *FetchInProgressError
	if errors.As(err, &inProgress) {
		return textResult(inProgress.Error())
	}
	var unavailable *DocumentUnavailableError
	if errors.As(err, &unavailable) {
		return errorResult(unavailable.Error())
	}
	return errorResult(fmt.Sprintf("failed to get document: %v", err))
}

// TDocHeader builds the provenance block prepended to a get_tdoc page: the
// document, its meeting, where it was downloaded from, and the files the
// download holds (attachments are not converted, so a reader must know
// they exist). It is shared with the CLI's get-tdoc command.
func TDocHeader(rec *tdocstore.Document) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "[Source: %s", rec.ID)
	if rec.MeetingTitle != "" {
		fmt.Fprintf(&sb, " — %s", rec.MeetingTitle)
		if rec.MeetingCode != "" {
			fmt.Fprintf(&sb, " (%s)", rec.MeetingCode)
		}
	}
	fmt.Fprintf(&sb, " — %s]", rec.URL())
	if rec.Title != "" && rec.Title != rec.ID {
		fmt.Fprintf(&sb, "\nTitle: %s", rec.Title)
	}
	if len(rec.Files) > 0 {
		fmt.Fprintf(&sb, "\nFiles: %s", strings.Join(rec.Files, ", "))
		if len(rec.Files) > 1 {
			fmt.Fprintf(&sb, " (converted: %s; the others are attachments)", rec.MainFile)
		}
	}
	return sb.String()
}

// tdocSectionList lists a document's sections on one line, so a reader of
// a paginated whole-document response knows what section_number to ask for.
func tdocSectionList(sections []db.Section) string {
	labels := make([]string, 0, len(sections))
	for _, s := range sections {
		labels = append(labels, sectionLabel(s))
	}
	return "Sections: " + strings.Join(labels, "; ")
}

// sectionLabel names a section by the exact section_number that reads it,
// with the title in parentheses when the number does not already carry it:
// "1 (Opening of the meeting)", "Agreement", "Agreement (2)", "preamble".
func sectionLabel(s db.Section) string {
	switch {
	case s.Number == "":
		return preambleName
	case s.Number == s.Title, strings.HasPrefix(s.Number, s.Title+" ("):
		return s.Number
	default:
		return s.Number + " (" + s.Title + ")"
	}
}
