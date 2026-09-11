package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/tdoc"
	"github.com/higebu/3gpp-mcp/internal/tdocstore"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type ListTDocsInput struct {
	Meeting    string `json:"meeting" jsonschema:"required,Meeting as listed by list_meetings: the code (R1-123, S2-172), the #-form (RAN1#123, SA2#172) or the folder path (tsg_ran/WG1_RL1/TSGR1_123)"`
	Group      string `json:"group,omitempty" jsonschema:"Group of the meeting (R1, RAN1, ...), only needed when the meeting name does not carry it, e.g. a bare folder name TSGR1_123"`
	AgendaItem string `json:"agenda_item,omitempty" jsonschema:"Agenda item to list, with its sub-items: 9.1 lists 9.1, 9.1.1, 9.1.2, ..."`
	Type       string `json:"type,omitempty" jsonschema:"Document type as the secretary classes it: CR, draftCR, pCR, discussion, LS in, LS out, agenda, report, WID new, SID new, other, ..."`
	Status     string `json:"status,omitempty" jsonschema:"Outcome: available, revised, agreed, approved, noted, endorsed, withdrawn, postponed, merged, not treated, reserved, rejected, treated, replied to, ..."`
	Source     string `json:"source,omitempty" jsonschema:"Company or group named in the source, matched as a substring (Ericsson, RAN2 for an incoming LS)"`
	Spec       string `json:"spec,omitempty" jsonschema:"Specification a CR or draft changes, e.g. 38.331"`
	Query      string `json:"query,omitempty" jsonschema:"Full-text query over title, source, abstract, agenda item description and work items; FTS5 syntax (AND, OR, NOT, quoted phrases)"`
	Details    bool   `json:"details,omitempty" jsonschema:"Also print each document's abstract, work items, clauses affected and LS addressees (default: false)"`
	Limit      int    `json:"limit,omitempty" jsonschema:"Maximum number of documents to return (default: 50)"`
	Offset     int    `json:"offset,omitempty" jsonschema:"Number of matching documents to skip, for paging (default: 0)"`
}

var ListTDocsTool = &mcp.Tool{
	Name:        "list_tdocs",
	Description: "List the documents (TDocs) of a 3GPP meeting from the meeting's official TDoc list: number, type, status, source, agenda item, title, and for a CR the spec, CR number and category, for an LS the addressees. Filter by agenda item, type, status, source, spec or a full-text query; without filters the agenda items and their document counts are listed first. The list is downloaded from the meeting's Docs folder on first use (retry if told it is in progress) and refreshed daily while the meeting is recent. Use list_meetings to find the meeting code and get_tdoc to read a listed document. search does not cover TDocs.",
}

const defaultTDocLimit = 50

func HandleListTDocs(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input ListTDocsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListTDocsInput) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(input.Meeting) == "" {
			return errorResult("meeting is required"), nil, nil
		}
		ml, err := src.TDocList(ctx, input.Meeting, input.Group)
		if err != nil {
			return tdocErrorResult(err), nil, nil
		}
		filter := tdocstore.Filter{
			AgendaItem: input.AgendaItem,
			Type:       input.Type,
			Status:     input.Status,
			Source:     input.Source,
			Spec:       input.Spec,
			Query:      input.Query,
			Limit:      input.Limit,
			Offset:     input.Offset,
		}
		if filter.Limit <= 0 {
			filter.Limit = defaultTDocLimit
		}
		res, err := src.TDocs.ListEntries(ml.Meeting.Dir, filter)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to list documents: %v", err)), nil, nil
		}
		var agenda []tdocstore.AgendaItem
		if filter.IsEmpty() && filter.Offset <= 0 {
			if agenda, err = src.TDocs.AgendaItems(ml.Meeting.Dir); err != nil {
				return errorResult(fmt.Sprintf("failed to list agenda items: %v", err)), nil, nil
			}
		}
		return textResult(FormatTDocList(ml, filter, res, agenda, input.Details)), nil, nil
	}
}

// FormatTDocList renders one page of a meeting's TDoc list. It is shared
// with the CLI's list-tdocs command.
func FormatTDocList(ml *MeetingList, f tdocstore.Filter, res *tdocstore.ListResult, agenda []tdocstore.AgendaItem, details bool) string {
	var sb strings.Builder
	m := ml.Meeting
	fmt.Fprintf(&sb, "[Meeting: %s (%s)", m.Title, m.Code)
	if m.Town != "" {
		fmt.Fprintf(&sb, ", %s", m.Town)
	}
	if m.Start != "" {
		fmt.Fprintf(&sb, ", %s", m.Start)
		if m.End != "" && m.End != m.Start {
			fmt.Fprintf(&sb, "..%s", m.End)
		}
	}
	fmt.Fprintf(&sb, " — %s — %d TDocs", ml.List.URL(), res.Count)
	if f.IsEmpty() {
		fmt.Fprintf(&sb, "; showing %s]", pageRange(res.Total, f.Offset, len(res.Entries)))
	} else {
		fmt.Fprintf(&sb, "; %d match %s; showing %s]", res.Total, f.String(), pageRange(res.Total, f.Offset, len(res.Entries)))
	}
	sb.WriteByte('\n')
	if len(agenda) > 0 {
		sb.WriteString("Agenda items (documents): ")
		for i, it := range agenda {
			if i > 0 {
				sb.WriteString("; ")
			}
			item := it.Item
			if item == "" {
				item = "(none)"
			}
			if it.Description != "" {
				fmt.Fprintf(&sb, "%s %s (%d)", item, it.Description, it.Count)
			} else {
				fmt.Fprintf(&sb, "%s (%d)", item, it.Count)
			}
		}
		sb.WriteString("\nColumns: TDoc | type | status | source | agenda item | title | CR or LS details | revision chain\n")
	}
	for _, e := range res.Entries {
		sb.WriteString(formatEntry(e, details))
		sb.WriteByte('\n')
	}
	if end := f.Offset + len(res.Entries); end < res.Total {
		fmt.Fprintf(&sb, "[%d more; call again with offset=%d]\n", res.Total-end, end)
	}
	return sb.String()
}

func pageRange(total, offset, n int) string {
	if n == 0 {
		return fmt.Sprintf("none (total %d)", total)
	}
	return fmt.Sprintf("%d-%d", offset+1, offset+n)
}

// formatEntry renders one list row: the columns a reader triages on, then
// the CR or LS specifics and the revision chain when there are any.
func formatEntry(e tdoc.Entry, details bool) string {
	cols := []string{e.TDoc, orDash(e.Type), orDash(e.Status), orDash(e.Source), orDash(e.AgendaItem), e.Title}
	var extra []string
	if e.Spec != "" {
		s := e.Spec
		if e.CR != "" {
			s += " CR " + e.CR
			if e.CRRevision != "" {
				s += "r" + e.CRRevision
			}
		}
		if e.CRCategory != "" {
			s += " cat " + e.CRCategory
		}
		if e.Version != "" {
			s += " v" + e.Version
		}
		extra = append(extra, s)
	}
	if e.Release != "" {
		extra = append(extra, e.Release)
	}
	if e.To != "" {
		extra = append(extra, "to "+e.To)
	}
	if e.ReplyTo != "" {
		extra = append(extra, "reply to "+e.ReplyTo)
	}
	if e.OriginalLS != "" && e.OriginalLS != e.ReplyTo {
		extra = append(extra, "original LS "+e.OriginalLS)
	}
	if len(extra) > 0 {
		cols = append(cols, strings.Join(extra, ", "))
	}
	var chain []string
	if e.IsRevisionOf != "" {
		chain = append(chain, "revision of "+e.IsRevisionOf)
	}
	if e.RevisedTo != "" {
		chain = append(chain, "revised to "+e.RevisedTo)
	}
	if e.ReplyIn != "" {
		chain = append(chain, "replied in "+e.ReplyIn)
	}
	if len(chain) > 0 {
		cols = append(cols, strings.Join(chain, ", "))
	}
	line := strings.Join(cols, " | ")
	if !details {
		return line
	}
	var sb strings.Builder
	sb.WriteString(line)
	for _, kv := range [][2]string{
		{"For", e.For},
		{"Abstract", e.Abstract},
		{"Work items", e.RelatedWIs},
		{"Clauses affected", e.ClausesAffected},
		{"Cc", e.Cc},
	} {
		if kv[1] != "" {
			fmt.Fprintf(&sb, "\n    %s: %s", kv[0], strings.ReplaceAll(kv[1], "\n", " "))
		}
	}
	return sb.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
