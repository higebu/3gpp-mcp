package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/tdoc"
)

// TDocMetadata is what a document's cover sheet or header says about it,
// pulled out of the preamble so a reader need not parse the form.
type TDocMetadata struct {
	CR *tdoc.CRCover
	LS *tdoc.LSHeader
	// SpecID is the database name of the spec a CR changes ("TS 38.213"),
	// resolved from the cover sheet's bare number.
	SpecID string
}

// ParseTDocMetadata reads the cover sheet or LS header out of a document's
// preamble section. The spec a CR changes is named as the database names
// it: by lookup when the database holds the spec, else by the numbering
// convention (xx.8xx and xx.9xx are reports).
func ParseTDocMetadata(ctx context.Context, d *db.DB, preamble string) *TDocMetadata {
	if preamble == "" {
		return nil
	}
	if c := tdoc.ParseCRCover(preamble); c != nil {
		return &TDocMetadata{CR: c, SpecID: SpecIDForNumber(ctx, d, c.Spec)}
	}
	if h := tdoc.ParseLSHeader(preamble); h != nil {
		return &TDocMetadata{LS: h}
	}
	return nil
}

// SpecIDForNumber turns a bare spec number ("38.213") into the database's
// spec ID ("TS 38.213").
func SpecIDForNumber(ctx context.Context, d *db.DB, number string) string {
	number = strings.TrimSpace(number)
	if number == "" {
		return ""
	}
	if d != nil {
		if res, err := d.ListSpecs(ctx, "", number, 10, 0); err == nil {
			for _, s := range res.Specs {
				if strings.HasSuffix(s.ID, " "+number) {
					return s.ID
				}
			}
		}
	}
	// TR 21.900: Technical Reports carry xx.8xx and xx.9xx numbers.
	if i := strings.Index(number, "."); i > 0 && len(number) > i+1 && (number[i+1] == '8' || number[i+1] == '9') {
		return "TR " + number
	}
	return "TS " + number
}

// Text renders the metadata as the lines that follow the get_tdoc header,
// ending with how to read the clauses a CR changes.
func (m *TDocMetadata) Text() string {
	if m == nil {
		return ""
	}
	var sb strings.Builder
	if c := m.CR; c != nil {
		fmt.Fprintf(&sb, "Change request: %s CR %s", m.SpecID, c.CR)
		if c.Revision != "" && c.Revision != "-" {
			fmt.Fprintf(&sb, " rev %s", c.Revision)
		}
		if c.Category != "" {
			fmt.Fprintf(&sb, ", category %s", c.Category)
		}
		if c.Release != "" {
			fmt.Fprintf(&sb, ", %s", c.Release)
		}
		if c.CurrentVersion != "" {
			fmt.Fprintf(&sb, ", against v%s", c.CurrentVersion)
		}
		for _, kv := range [][2]string{
			{"Source", joinNonEmpty(c.SourceWG, c.SourceTSG)},
			{"Work item", c.WorkItem},
			{"Reason for change", c.Reason},
			{"Summary of change", c.Summary},
			{"Consequences if not approved", c.Consequences},
			{"Clauses affected", c.Clauses},
		} {
			if kv[1] != "" {
				fmt.Fprintf(&sb, "\n%s: %s", kv[0], oneLine(kv[1]))
			}
		}
		if clauses := c.ClauseList(); len(clauses) > 0 && m.SpecID != "" {
			fmt.Fprintf(&sb, "\nTo see the current text of a changed clause: get_section spec_id=%q section_number=%q", m.SpecID, clauses[0])
			if c.CurrentVersion != "" {
				fmt.Fprintf(&sb, " version=%q", c.CurrentVersion)
			}
			if len(clauses) > 1 {
				fmt.Fprintf(&sb, " (also %s)", strings.Join(clauses[1:], ", "))
			}
		}
		return sb.String()
	}
	if h := m.LS; h != nil {
		sb.WriteString("Liaison statement")
		for _, kv := range [][2]string{
			{"From", h.Source},
			{"To", h.To},
			{"Cc", h.Cc},
			{"Response to", h.ResponseTo},
			{"Release", h.Release},
			{"Work item", h.WorkItem},
			{"Contact", h.Contact},
			{"Attachments", h.Attachments},
		} {
			if kv[1] != "" {
				fmt.Fprintf(&sb, "\n%s: %s", kv[0], oneLine(kv[1]))
			}
		}
		return sb.String()
	}
	return ""
}

func joinNonEmpty(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " / ")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
