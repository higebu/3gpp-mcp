package tools

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/tdoc"
	"github.com/higebu/3gpp-mcp/internal/tdocstore"
	"github.com/higebu/3gpp-mcp/internal/testutil"
)

// crCover is a CR cover sheet as the converter renders it, for R1-2509715.
const crCover = `# Preamble

<table><tbody><tr><td colspan="9"><p><strong>CHANGE REQUEST</strong></p></td></tr><tr><td><p></p></td><td><p><strong>38.213</strong></p></td><td><p><strong>CR</strong></p></td><td><p><strong>0748</strong></p></td><td><p><strong>rev</strong></p></td><td><p><strong>1</strong></p></td><td><p><strong>Current version:</strong></p></td><td><p><strong>19.1.0</strong></p></td></tr></tbody></table>

<table><tbody><tr><td><p><strong><em>Title:</em></strong></p></td><td><p>PDCCH repetitions</p></td></tr><tr><td><p><strong><em>Source to WG:</em></strong></p></td><td><p>Samsung</p></td></tr><tr><td><p><strong><em>Category:</em></strong></p></td><td><p>B</p></td><td><p><strong><em>Release:</em></strong></p></td><td><p>Rel-19</p></td></tr><tr><td><p><strong><em>Reason for change:</em></strong></p></td><td><p>Because.</p></td></tr><tr><td><p><strong><em>Clauses affected:</em></strong></p></td><td><p>13, 10.1</p></td></tr></tbody></table>
`

const lsHeader = "# Preamble\n\n**Title:**\tLS on X\n\n**Source:**\tRAN WG1\n\n**To:**\tRAN WG2\n\n**CC:**\tRAN WG4\n\n**Attachments:**\tNone\n"

// meetingReportSource builds a Source over a fake RAN1 with three meetings:
// R1-124 (whose list carries the report of R1-123), R1-123 (with an agenda
// and a CR in its list) and R1-122-bis (no following list with its report;
// its Report/ folder holds the minutes). Every document fetch yields a
// small document whose preamble depends on the number.
func meetingReportSource(t *testing.T) *Source {
	t.Helper()
	d := setupTestDB(t)
	lists := map[string][]tdoc.Entry{
		"tsg_ran/WG1_RL1/TSGR1_124": {
			{TDoc: "R1-2600003", Title: "Report of RAN1#123 meeting", Type: "report", Status: "approved"},
			{TDoc: "R1-2600004", Title: "Status report RAN1", Type: "report", Status: "noted"},
		},
		"tsg_ran/WG1_RL1/TSGR1_123": {
			{TDoc: "R1-2508300", Title: "Draft Agenda of RAN1#123", Type: "agenda", Status: "revised", RevisedTo: "R1-2509000"},
			{TDoc: "R1-2508302", Title: "Meeting timelines", Type: "agenda", Status: "noted"},
			{TDoc: "R1-2509000", Title: "Draft Agenda of RAN1#123", Type: "agenda", Status: "approved", IsRevisionOf: "R1-2508300"},
			{TDoc: "R1-2509715", Title: "PDCCH repetitions", Type: "CR", Status: "agreed", Spec: "38.213", CR: "0748"},
		},
		"tsg_ran/WG1_RL1/TSGR1_122b": {
			{TDoc: "R1-2506700", Title: "Draft Agenda of RAN1#122bis", Type: "agenda", Status: "revised", RevisedTo: "R1-2506999"},
			{TDoc: "R1-2506701", Title: "Agenda of RAN1#122bis", Type: "agenda", Status: "withdrawn"},
			// Names the bis meeting, not RAN1#122, whose report it must
			// not be taken for.
			{TDoc: "R1-2506702", Title: "Report of RAN1#122-bis meeting", Type: "report", Status: "approved"},
		},
	}
	store, err := tdocstore.Open(tdocstore.Options{
		Path: filepath.Join(t.TempDir(), "tdocs.db"), LimitBytes: -1,
		ListFetcher: func(_ context.Context, m tdoc.Meeting) ([]tdoc.Entry, string, error) {
			entries, ok := lists[m.Dir]
			if !ok {
				return nil, "", fmt.Errorf("no list for %s", m.Code)
			}
			return entries, tdoc.TDocListPath(m), nil
		},
		Fetcher: func(_ context.Context, doc tdoc.Document) (*tdoc.Fetched, error) {
			preamble := "# Preamble\n\nSource:\tMCC\n\nTitle:\tSome document"
			switch {
			case doc.ID == "R1-2509715":
				preamble = crCover
			case strings.HasSuffix(doc.ID, "R1-2508303"):
				preamble = lsHeader
			}
			return &tdoc.Fetched{
				Title:    "Document " + doc.ID,
				MainFile: doc.ID + ".docx",
				Files:    []string{doc.ID + ".docx"},
				Sections: []db.Section{
					{SpecID: doc.ID, Number: "", Title: "Preamble", Level: 1, Content: preamble},
					{SpecID: doc.ID, Number: "13", Title: "Type0-PDCCH", Level: 1, Content: "# 13 Type0-PDCCH\n\nBody of " + doc.ID},
					{SpecID: doc.ID, Number: "Agreement", Title: "Agreement", Level: 1, Content: "# Agreement\n\nAgreed."},
				},
			}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })

	mux := http.NewServeMux()
	mux.HandleFunc("/dynareport", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("code") != "Meetings-R1.htm" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, testutil.DynaReportPage(
			testutil.DynaReportRow{Code: "R1-125", Title: "3GPPRAN1#125", Start: "2026-05-11", End: "2026-05-15"},
			testutil.DynaReportRow{Code: "R1-124", Title: "3GPPRAN1#124", Start: "2026-02-09", End: "2026-02-13", Dir: "tsg_ran/WG1_RL1/TSGR1_124", First: "R1-2600000", Last: "R1-2601800"},
			testutil.DynaReportRow{Code: "R1-123", Title: "3GPPRAN1#123", Town: "Dallas", Start: "2025-11-17", End: "2025-11-21", Dir: "tsg_ran/WG1_RL1/TSGR1_123", First: "R1-2508300", Last: "R1-2509718"},
			testutil.DynaReportRow{Code: "R1-122-bis", Title: "3GPPRAN1#122-bis", Start: "2025-10-13", End: "2025-10-17", Dir: "tsg_ran/WG1_RL1/TSGR1_122b", First: "R1-2506700", Last: "R1-2508262"},
			testutil.DynaReportRow{Code: "R1-122", Title: "3GPPRAN1#122", Start: "2025-08-25", End: "2025-08-29", Dir: "tsg_ran/WG1_RL1/TSGR1_122", First: "R1-2505000", Last: "R1-2506600"},
		))
	})
	mux.HandleFunc("/ftp/tsg_ran/WG1_RL1/TSGR1_122b/Report/{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_122b/Report/History/">History</a> <a href="https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_122b/Report/R1_25XXXXX_Minutes_report_RAN1%23122bis_v100.zip">minutes</a>`)
	})
	mux.HandleFunc("/ftp/tsg_ran/WG1_RL1/TSGR1_122/Report/{$}", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_122/Report/History/">History</a>`)
	})
	src := NewSource(d)
	src.TDocs = store
	src.Client = testutil.FakeSite(t, mux)
	src.UseCache = false
	src.Budget = 5 * time.Second
	return src
}

func TestHandleGetMeetingReport(t *testing.T) {
	src := meetingReportSource(t)
	handler := HandleGetMeetingReport(src)
	ctx := context.Background()

	t.Run("report from the following meeting", func(t *testing.T) {
		result, _, _ := handler(ctx, nil, GetMeetingReportInput{Meeting: "RAN1#123"})
		text := getTextContent(result)
		if result.IsError {
			t.Fatalf("error: %s", text)
		}
		for _, want := range []string{
			"[Meeting report of 3GPPRAN1#123: R1-2600003 (Report of RAN1#123 meeting) from the TDoc list of 3GPPRAN1#124]",
			"[Source: R1-2600003 — 3GPPRAN1#124 (R1-124)",
			"Sections: preamble; 13 (Type0-PDCCH); Agreement",
			"Body of R1-2600003",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in:\n%s", want, text)
			}
		}
		if strings.Contains(text, "Liaison statement") {
			t.Errorf("a report took for an LS:\n%s", text)
		}
	})

	t.Run("agenda", func(t *testing.T) {
		result, _, _ := handler(ctx, nil, GetMeetingReportInput{Meeting: "R1-123", Kind: "Agenda", SectionNumber: "Agreement"})
		text := getTextContent(result)
		if result.IsError || !strings.Contains(text, "[Meeting agenda of 3GPPRAN1#123: R1-2509000 from the meeting's TDoc list]") || !strings.Contains(text, "Section: Agreement") || strings.Contains(text, "Body of") {
			t.Errorf("agenda:\n%s", text)
		}
	})

	t.Run("agenda revised to a number missing from the list", func(t *testing.T) {
		result, _, _ := handler(ctx, nil, GetMeetingReportInput{Meeting: "R1-122-bis", Kind: "agenda"})
		text := getTextContent(result)
		if result.IsError || !strings.Contains(text, "[Meeting agenda of 3GPPRAN1#122-bis: R1-2506700 from the meeting's TDoc list]") {
			t.Errorf("fallback agenda:\n%s", text)
		}
	})

	t.Run("report from the Report folder", func(t *testing.T) {
		result, _, _ := handler(ctx, nil, GetMeetingReportInput{Meeting: "R1-122-bis"})
		text := getTextContent(result)
		if result.IsError || !strings.Contains(text, "[Meeting report of 3GPPRAN1#122-bis: tsg_ran/WG1_RL1/TSGR1_122b/Report/R1_25XXXXX_Minutes_report_RAN1#122bis_v100.zip from the meeting's Report folder]") {
			t.Errorf("folder report:\n%s", text)
		}
	})

	t.Run("errors", func(t *testing.T) {
		for _, tt := range []struct {
			in   GetMeetingReportInput
			want string
		}{
			{GetMeetingReportInput{}, "meeting is required"},
			{GetMeetingReportInput{Meeting: "R1-123", Kind: "minutes"}, `unknown document kind "minutes"`},
			{GetMeetingReportInput{Meeting: "R1-999"}, `lists no meeting "R1-999"`},
			{GetMeetingReportInput{Meeting: "R1-122"}, "no following meeting lists a report of it"},
			{GetMeetingReportInput{Meeting: "R1-124", Kind: "agenda"}, "no document of type agenda"},
			{GetMeetingReportInput{Meeting: "R1-125"}, "no meeting folder"},
			{GetMeetingReportInput{Meeting: "R1-123", SectionNumber: "99"}, "section 99 not found"},
		} {
			result, _, _ := handler(ctx, nil, tt.in)
			if text := getTextContent(result); !result.IsError || !strings.Contains(text, tt.want) {
				t.Errorf("%+v: want %q, got:\n%s", tt.in, tt.want, text)
			}
		}
		src.TDocs = nil
		result, _, _ := handler(ctx, nil, GetMeetingReportInput{Meeting: "R1-123"})
		if text := getTextContent(result); !result.IsError || !strings.Contains(text, "disabled") {
			t.Errorf("disabled:\n%s", text)
		}
	})
}

func TestGetTDocMetadata(t *testing.T) {
	src := meetingReportSource(t)
	handler := HandleGetTDoc(src)
	ctx := context.Background()

	// The whole CR: cover sheet summarized, clause read-through named.
	result, _, _ := handler(ctx, nil, GetTDocInput{TDocID: "R1-2509715"})
	text := getTextContent(result)
	for _, want := range []string{
		"Change request: TS 38.213 CR 0748 rev 1, category B, Rel-19, against v19.1.0",
		"Source: Samsung",
		"Reason for change: Because.",
		"Clauses affected: 13, 10.1",
		`get_section spec_id="TS 38.213" section_number="13" version="19.1.0" (also 10.1)`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	// One section of it: the cover sheet is read from the cache.
	result, _, _ = handler(ctx, nil, GetTDocInput{TDocID: "R1-2509715", SectionNumber: "13"})
	text = getTextContent(result)
	if !strings.Contains(text, "Change request: TS 38.213 CR 0748") || !strings.Contains(text, "Section: 13 (Type0-PDCCH)") {
		t.Errorf("section read:\n%s", text)
	}
	// An LS.
	result, _, _ = handler(ctx, nil, GetTDocInput{TDocID: "R1-2508303"})
	text = getTextContent(result)
	if !strings.Contains(text, "Liaison statement\nFrom: RAN WG1\nTo: RAN WG2\nCc: RAN WG4\nAttachments: None") {
		t.Errorf("LS:\n%s", text)
	}
	// Neither.
	result, _, _ = handler(ctx, nil, GetTDocInput{TDocID: "R1-2600004", Meeting: "R1-124"})
	text = getTextContent(result)
	if strings.Contains(text, "Change request") || strings.Contains(text, "Liaison statement") {
		t.Errorf("plain document:\n%s", text)
	}
}

func TestSpecIDForNumber(t *testing.T) {
	d := setupTestDB(t)
	ctx := context.Background()
	for number, want := range map[string]string{
		"23.501": "TS 23.501", // in the test database
		"38.213": "TS 38.213", // not in it: by convention
		"38.901": "TR 38.901",
		"22.8xx": "TR 22.8xx",
		"":       "",
	} {
		if got := SpecIDForNumber(ctx, d, number); got != want {
			t.Errorf("SpecIDForNumber(%q) = %q, want %q", number, got, want)
		}
	}
	if got := SpecIDForNumber(ctx, nil, "38.213"); got != "TS 38.213" {
		t.Errorf("without a database: %q", got)
	}
	if (*TDocMetadata)(nil).Text() != "" || ParseTDocMetadata(ctx, d, "") != nil || ParseTDocMetadata(ctx, d, "plain") != nil {
		t.Error("empty metadata")
	}
}
