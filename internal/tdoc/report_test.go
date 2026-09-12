package tdoc

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/higebu/3gpp-mcp/internal/testutil"
)

func TestReportTitleMatches(t *testing.T) {
	ran1, _ := GroupByCode("R1")
	ran, _ := GroupByCode("RP")
	sa2, _ := GroupByCode("S2")
	for _, tt := range []struct {
		title string
		g     Group
		m     Meeting
		want  bool
	}{
		{"Report of RAN1#123 meeting", ran1, Meeting{Title: "3GPPRAN1#123"}, true},
		{"Report of RAN1#122bis meeting", ran1, Meeting{Title: "3GPPRAN1#122-bis"}, true},
		{"Report of RAN1#122 meeting", ran1, Meeting{Title: "3GPPRAN1#122-bis"}, false},
		{"Report of RAN1#122-bis meeting", ran1, Meeting{Title: "3GPPRAN1#122"}, false},
		{"Report of RAN1#1220 meeting", ran1, Meeting{Title: "3GPPRAN1#122"}, false},
		{"Report of RAN1#100-bis-e meeting", ran1, Meeting{Title: "3GPPRAN1#100-bis-e"}, true},
		{"Report of RAN1#100-bis-e meeting", ran1, Meeting{Title: "3GPPRAN1#100-e"}, false},
		{"Draft report of meeting RAN #110 held 08.12.-11.12.2025", ran, Meeting{Title: "3GPPRAN#110"}, true},
		{"Status Report RAN WG1", ran, Meeting{Title: "3GPPRAN#110"}, false},
		{"Draft Report of SA WG2 meeting #172", sa2, Meeting{Title: "3GPPSA2#172"}, true},
		{"Report of RAN2#131 meeting", ran1, Meeting{Title: "3GPPRAN1#131"}, false},
		{"Report", ran1, Meeting{Title: "no number"}, false},
	} {
		if got := ReportTitleMatches(tt.title, tt.g, tt.m); got != tt.want {
			t.Errorf("ReportTitleMatches(%q, %s, %s) = %v, want %v", tt.title, tt.g.Name, tt.m.Title, got, tt.want)
		}
	}
	if MeetingNumber(Meeting{Title: "3GPPRAN1#122-bis"}) != "122-bis" || MeetingNumber(Meeting{}) != "" {
		t.Error("MeetingNumber")
	}
}

func TestFindReportFile(t *testing.T) {
	m := Meeting{Code: "R1-123", Title: "3GPPRAN1#123", Dir: "tsg_ran/WG1_RL1/TSGR1_123"}
	serve := func(listing string) *http.Client {
		mux := http.NewServeMux()
		mux.HandleFunc("/ftp/tsg_ran/WG1_RL1/TSGR1_123/Report/{$}", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, listing)
		})
		return testutil.FakeSite(t, mux)
	}
	base := "https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/Report/"
	link := func(name string) string { return `<a href="` + base + name + `">` + name + `</a> ` }
	ctx := context.Background()

	// The final minutes, with the "#" written literally as the listing
	// does, win over the draft; History/ is skipped.
	got, err := FindReportFile(ctx, serve(link("History/")+link("PartList_3GPPRAN1%23123.xlsx")+link("Draft_Minutes_report_RAN1%23123_v020.zip")+link("Final_Minutes_report_RAN1#123_v100.zip")+link("History/draft_v1.zip")), m)
	if err != nil || got != "tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1#123_v100.zip" {
		t.Errorf("final: %q, %v", got, err)
	}
	got, err = FindReportFile(ctx, serve(link("S2-172_Draft_Report_v003.zip")+link("S2-172_Draft_Report_v010.zip")), m)
	if err != nil || got != "tsg_ran/WG1_RL1/TSGR1_123/Report/S2-172_Draft_Report_v010.zip" {
		t.Errorf("highest version: %q, %v", got, err)
	}
	got, err = FindReportFile(ctx, serve(link("R2-2506702.zip")), m)
	if err != nil || got != "tsg_ran/WG1_RL1/TSGR1_123/Report/R2-2506702.zip" {
		t.Errorf("bare tdoc: %q, %v", got, err)
	}
	for name, listing := range map[string]string{
		"only drafts in History":  link("History/") + link("History/draft.zip"),
		"only a participant list": link("PartList_x.xlsx"),
		"empty":                   "",
	} {
		if _, err := FindReportFile(ctx, serve(listing), m); !errors.Is(err, ErrNotFound) {
			t.Errorf("%s: err = %v, want ErrNotFound", name, err)
		}
	}
	if _, err := FindReportFile(ctx, nil, Meeting{Code: "R1-135"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("no folder: %v", err)
	}
	if _, err := FindReportFile(ctx, testutil.FakeSite(t, http.NotFoundHandler()), m); err == nil || errors.Is(err, ErrNotFound) {
		t.Errorf("listing failure: %v", err)
	}
}
