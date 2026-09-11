package tools

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/tdoc"
	"github.com/higebu/3gpp-mcp/internal/tdocstore"
)

// tdocSource returns a Source whose TDoc store fetches through fetcher and
// whose meeting listing is never consulted: the fetcher receives whatever
// Resolve produced, and tests pass documents by path, which Resolve handles
// without the network.
func tdocSource(t *testing.T, fetcher tdocstore.Fetcher) *Source {
	t.Helper()
	d := setupTestDB(t)
	store, err := tdocstore.Open(tdocstore.Options{Path: filepath.Join(t.TempDir(), "tdocs.db"), LimitBytes: -1, Fetcher: fetcher})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	src := NewSource(d)
	src.TDocs = store
	src.Budget = time.Second
	return src
}

const reportPath = "tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1#123_v100.zip"

func fakeFetch(_ context.Context, doc tdoc.Document) (*tdoc.Fetched, error) {
	return &tdoc.Fetched{
		Title:    "Final Report of RAN1#123",
		MainFile: "Final_Minutes_report_RAN1#123_v100.docx",
		Files:    []string{"Final_Minutes_report_RAN1#123_v100.docx", "TDoc_List.xlsx"},
		Sections: []db.Section{
			{SpecID: doc.ID, Number: "", Title: "Preamble", Level: 1, Content: "# Preamble\n\nTitle: Final Report of RAN1#123"},
			{SpecID: doc.ID, Number: "1", Title: "Opening of the meeting", Level: 1, Content: "# 1 Opening of the meeting\n\nThe chair opened the meeting."},
			{SpecID: doc.ID, Number: "Agreement", Title: "Agreement", Level: 1, Content: "# Agreement\n\nAgreed."},
		},
		Images: []db.Image{{SpecID: doc.ID, Name: "image1.png", MIMEType: "image/png", Data: []byte("png"), LLMReadable: true}},
	}, nil
}

func TestHandleGetTDoc(t *testing.T) {
	src := tdocSource(t, fakeFetch)
	handler := HandleGetTDoc(src)

	t.Run("whole document", func(t *testing.T) {
		result, _, err := handler(context.Background(), nil, GetTDocInput{TDocID: "/ftp/" + reportPath})
		if err != nil {
			t.Fatal(err)
		}
		text := getTextContent(result)
		if result.IsError {
			t.Fatalf("unexpected error: %s", text)
		}
		for _, want := range []string{
			"[Source: " + reportPath + " — https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1%23123_v100.zip]",
			"Title: Final Report of RAN1#123",
			"Files: Final_Minutes_report_RAN1#123_v100.docx, TDoc_List.xlsx (converted: Final_Minutes_report_RAN1#123_v100.docx; the others are attachments)",
			"Sections: preamble; 1 (Opening of the meeting); Agreement",
			"The chair opened the meeting.",
			"Agreed.",
		} {
			if !strings.Contains(text, want) {
				t.Errorf("missing %q in:\n%s", want, text)
			}
		}
	})

	t.Run("one section", func(t *testing.T) {
		result, _, _ := handler(context.Background(), nil, GetTDocInput{TDocID: reportPath, SectionNumber: "1"})
		text := getTextContent(result)
		if result.IsError || !strings.Contains(text, "Section: 1 (Opening of the meeting)") || strings.Contains(text, "Agreed.") {
			t.Errorf("got:\n%s", text)
		}
		result, _, _ = handler(context.Background(), nil, GetTDocInput{TDocID: reportPath, SectionNumber: "Preamble"})
		text = getTextContent(result)
		if result.IsError || !strings.Contains(text, "Section: preamble") || !strings.Contains(text, "Title: Final Report") {
			t.Errorf("preamble: got:\n%s", text)
		}
		result, _, _ = handler(context.Background(), nil, GetTDocInput{TDocID: reportPath, SectionNumber: "9"})
		if !result.IsError || !strings.Contains(getTextContent(result), "section 9 not found") {
			t.Errorf("missing section: got %s", getTextContent(result))
		}
	})

	t.Run("pagination", func(t *testing.T) {
		result, _, _ := handler(context.Background(), nil, GetTDocInput{TDocID: reportPath, MaxLines: 1})
		text := getTextContent(result)
		if !strings.Contains(text, "Truncated") || !strings.HasPrefix(text, "[Source: ") {
			t.Errorf("got:\n%s", text)
		}
	})

	t.Run("missing id", func(t *testing.T) {
		result, _, _ := handler(context.Background(), nil, GetTDocInput{})
		if !result.IsError {
			t.Error("expected an error")
		}
	})

	t.Run("disabled", func(t *testing.T) {
		result, _, _ := HandleGetTDoc(NewSource(setupTestDB(t)))(context.Background(), nil, GetTDocInput{TDocID: "R1-2509715"})
		if !result.IsError || !strings.Contains(getTextContent(result), "disabled") {
			t.Errorf("got %s", getTextContent(result))
		}
	})
}

func TestHandleGetTDoc_FetchOutcomes(t *testing.T) {
	t.Run("in progress", func(t *testing.T) {
		release := make(chan struct{})
		src := tdocSource(t, func(ctx context.Context, doc tdoc.Document) (*tdoc.Fetched, error) {
			<-release
			return fakeFetch(ctx, doc)
		})
		src.Budget = 20 * time.Millisecond
		result, _, _ := HandleGetTDoc(src)(context.Background(), nil, GetTDocInput{TDocID: reportPath})
		text := getTextContent(result)
		if result.IsError || !strings.HasPrefix(text, reportPath+" is being downloaded and converted") {
			t.Errorf("got error=%v text=%s", result.IsError, text)
		}
		close(release)
		src.Budget = time.Second
		result, _, _ = HandleGetTDoc(src)(context.Background(), nil, GetTDocInput{TDocID: reportPath})
		if result.IsError || !strings.Contains(getTextContent(result), "Agreed.") {
			t.Errorf("after the fetch: %s", getTextContent(result))
		}
	})

	t.Run("unsupported download", func(t *testing.T) {
		src := tdocSource(t, func(context.Context, tdoc.Document) (*tdoc.Fetched, error) {
			return nil, errors.New("no convertible document in x.zip (files: slides.pptx)")
		})
		result, _, _ := HandleGetTDoc(src)(context.Background(), nil, GetTDocInput{TDocID: reportPath})
		if !result.IsError || !strings.Contains(getTextContent(result), "slides.pptx") {
			t.Errorf("got %s", getTextContent(result))
		}
	})

	t.Run("unknown number without network", func(t *testing.T) {
		src := tdocSource(t, fakeFetch)
		result, _, _ := HandleGetTDoc(src)(context.Background(), nil, GetTDocInput{TDocID: "TS 23.501"})
		if !result.IsError || !strings.Contains(getTextContent(result), "not a TDoc number") {
			t.Errorf("got %s", getTextContent(result))
		}
		// The cause survives on the error for callers that test for it.
		_, err := src.TDoc(context.Background(), "TS 23.501", "")
		var unavailable *DocumentUnavailableError
		if !errors.As(err, &unavailable) || !errors.Is(err, tdoc.ErrNotFound) {
			t.Errorf("err = %v, want DocumentUnavailableError wrapping tdoc.ErrNotFound", err)
		}
	})

	t.Run("section labels round-trip", func(t *testing.T) {
		src := tdocSource(t, fakeFetch)
		handler := HandleGetTDoc(src)
		result, _, _ := handler(context.Background(), nil, GetTDocInput{TDocID: reportPath})
		for _, label := range []string{"1", "Agreement", "preamble"} {
			r, _, _ := handler(context.Background(), nil, GetTDocInput{TDocID: reportPath, SectionNumber: label})
			if r.IsError {
				t.Errorf("section_number %q from the Sections line: %s", label, getTextContent(r))
			}
		}
		if got := sectionLabel(db.Section{Number: "Agreement (2)", Title: "Agreement"}); got != "Agreement (2)" {
			t.Errorf("de-duplicated label = %q", got)
		}
		_ = result
	})
}

func TestTDocImage(t *testing.T) {
	src := tdocSource(t, fakeFetch)
	img, err := src.TDocImage(context.Background(), reportPath, "", "image1.png")
	if err != nil || img == nil || string(img.Data) != "png" {
		t.Fatalf("TDocImage = %+v, %v", img, err)
	}
	if img, err := src.TDocImage(context.Background(), reportPath, "", "nope.png"); err != nil || img != nil {
		t.Errorf("TDocImage(missing) = %+v, %v", img, err)
	}
	rec, toc, err := src.TDocTOC(context.Background(), reportPath, "")
	if err != nil || rec == nil || len(toc) != 3 {
		t.Errorf("TDocTOC = %+v, %+v, %v", rec, toc, err)
	}
}

// TestTDoc_StoreErrors covers the database error branches of the Source
// methods: a failing store reports the failure rather than "not cached".
func TestTDoc_StoreErrors(t *testing.T) {
	src := tdocSource(t, fakeFetch)
	if _, _, err := src.TDocTOC(context.Background(), reportPath, ""); err != nil {
		t.Fatal(err)
	}
	if err := src.TDocs.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := src.TDoc(context.Background(), reportPath, ""); err == nil {
		t.Error("TDoc on a closed store must fail")
	}
	if _, _, err := src.TDocTOC(context.Background(), reportPath, ""); err == nil {
		t.Error("TDocTOC on a closed store must fail")
	}
	if _, _, err := src.TDocSections(context.Background(), reportPath, "", "*", false); err == nil {
		t.Error("TDocSections on a closed store must fail")
	}
	if _, err := src.TDocImage(context.Background(), reportPath, "", "image1.png"); err == nil {
		t.Error("TDocImage on a closed store must fail")
	}
	result, _, _ := HandleGetTDoc(src)(context.Background(), nil, GetTDocInput{TDocID: reportPath})
	if !result.IsError || !strings.Contains(getTextContent(result), "failed to get document") {
		t.Errorf("got %s", getTextContent(result))
	}
}

func TestTDocSections_Subsections(t *testing.T) {
	src := tdocSource(t, func(_ context.Context, doc tdoc.Document) (*tdoc.Fetched, error) {
		return &tdoc.Fetched{
			MainFile: "x.docx", Files: []string{"x.docx"},
			Sections: []db.Section{
				{SpecID: doc.ID, Number: "1", Title: "A", Level: 1, Content: "# 1 A"},
				{SpecID: doc.ID, Number: "1.1", Title: "B", Level: 2, ParentNumber: "1", Content: "## 1.1 B"},
			},
		}, nil
	})
	rec, sections, err := src.TDocSections(context.Background(), reportPath, "", "1", true)
	if err != nil || rec == nil || len(sections) != 2 {
		t.Errorf("got %+v, %+v, %v", rec, sections, err)
	}
	if rec.Title != reportPath {
		t.Errorf("Title = %q, want the ID when the fetch names none", rec.Title)
	}
	h := TDocHeader(rec)
	if strings.Contains(h, "Title:") || strings.Contains(h, "attachments") {
		t.Errorf("header for an untitled single-file document:\n%s", h)
	}
}
