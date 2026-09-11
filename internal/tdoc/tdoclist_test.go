package tdoc

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/higebu/3gpp-mcp/internal/testutil"
)

// makeXlsx builds a workbook with one sheet per name, cells written as
// shared strings — the way Excel writes text — except values starting with
// "=" which are written inline and "#"-prefixed ones which are written as
// bare numbers, so every cell encoding the reader handles is exercised.
func makeXlsx(t *testing.T, sheets map[string][][]string, order ...string) []byte {
	t.Helper()
	var shared []string
	index := map[string]int{}
	sharedIdx := func(s string) int {
		if i, ok := index[s]; ok {
			return i
		}
		index[s] = len(shared)
		shared = append(shared, s)
		return len(shared) - 1
	}
	files := map[string][]byte{}
	var wb, rels strings.Builder
	wb.WriteString(`<?xml version="1.0" encoding="UTF-8"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`)
	rels.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId9" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/sharedStrings" Target="sharedStrings.xml"/>`)
	for n, name := range order {
		rows := sheets[name]
		fmt.Fprintf(&wb, `<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, name, n+1, n+1)
		fmt.Fprintf(&rels, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, n+1, n+1)
		var sheet strings.Builder
		sheet.WriteString(`<?xml version="1.0" encoding="UTF-8"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
		for r, row := range rows {
			fmt.Fprintf(&sheet, `<row r="%d">`, r+1)
			for c, v := range row {
				ref := columnName(c) + fmt.Sprint(r+1)
				switch {
				case v == "":
					// Excel omits empty cells.
				case strings.HasPrefix(v, "="):
					fmt.Fprintf(&sheet, `<c r="%s" t="inlineStr"><is><t>%s</t></is></c>`, ref, v[1:])
				case strings.HasPrefix(v, "#"):
					fmt.Fprintf(&sheet, `<c r="%s"><v>%s</v></c>`, ref, v[1:])
				default:
					fmt.Fprintf(&sheet, `<c r="%s" t="s"><v>%d</v></c>`, ref, sharedIdx(v))
				}
			}
			sheet.WriteString("</row>")
		}
		sheet.WriteString("</sheetData></worksheet>")
		files[fmt.Sprintf("xl/worksheets/sheet%d.xml", n+1)] = []byte(sheet.String())
	}
	wb.WriteString("</sheets></workbook>")
	rels.WriteString("</Relationships>")
	files["xl/workbook.xml"] = []byte(wb.String())
	files["xl/_rels/workbook.xml.rels"] = []byte(rels.String())
	var sst strings.Builder
	sst.WriteString(`<?xml version="1.0" encoding="UTF-8"?><sst xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">`)
	for _, s := range shared {
		// A rich-text string splits into runs; the reader must join them.
		if i := strings.Index(s, "|"); i > 0 {
			fmt.Fprintf(&sst, "<si><r><t>%s</t></r><r><t>%s</t></r></si>", s[:i], s[i+1:])
			continue
		}
		fmt.Fprintf(&sst, "<si><t>%s</t></si>", s)
	}
	sst.WriteString("</sst>")
	files["xl/sharedStrings.xml"] = []byte(sst.String())
	return makeZip(t, files)
}

func columnName(i int) string {
	name := ""
	for i >= 0 {
		name = string(rune('A'+i%26)) + name
		i = i/26 - 1
	}
	return name
}

var listHeader = []string{"TDoc", "Title", "Source", "Contact", "Type", "For", "Abstract", "Agenda item sort order", "Agenda item", "Agenda item description", "TDoc Status", "Uploaded", "Is revision of", "Revised to", "Release", "Spec", "Version", "Related WIs", "CR", "CR revision", "CR category", "Clauses Affected", "Reply to", "To", "Cc", "Original LS", "Reply in"}

func sampleList(t *testing.T) []byte {
	t.Helper()
	rows := [][]string{
		listHeader,
		{"R1-2508300", "Draft Agenda of RAN1#123", "RAN1 Chair", "Someone", "agenda", "Approval", "", "#2", "2", "Approval of Agenda", "revised", "#45965.3", "", "R1-2509000", "", "", "", "", "", "", "", "", "", "", "", "", ""},
		{"R1-2508303", "Reply LS on 6Rx", "RAN2, Qualcomm", "", "LS in", "Information", "", "#5", "5", "Incoming Liaison Statements", "noted", "#45965.4", "", "", "Rel-19", "", "", "NR_ENDC", "", "", "", "", "R4-2511898", "RAN4", "RAN1", "R2-2507743", ""},
		{"R1-2509526", "=CR to incorporate new agreements on ISAC CM", "Xiaomi, AT&amp;T", "", "CR", "Agreement", "Summary of|agreements", "#8", "8.8", "Maintenance on others", "agreed", "#45965.5", "R1-2509000", "", "Rel-19", "38.901", "19.1.0", "FS_Sensing_NR", "0033", "", "F", "7.9.4.2, 7.9.5.2", "", "", "", "", ""},
		{"", "a row without a number is skipped"},
	}
	return makeXlsx(t, map[string][][]string{"CR_Packs_List": {{"x"}}, "TDoc_List": rows}, "CR_Packs_List", "TDoc_List")
}

func TestParseTDocList(t *testing.T) {
	entries, err := ParseTDocList(sampleList(t))
	if err != nil {
		t.Fatalf("ParseTDocList: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}
	want := []Entry{
		{TDoc: "R1-2508300", Title: "Draft Agenda of RAN1#123", Source: "RAN1 Chair", Type: "agenda", For: "Approval", AgendaItem: "2", AgendaDescription: "Approval of Agenda", Status: "revised", RevisedTo: "R1-2509000"},
		{TDoc: "R1-2508303", Title: "Reply LS on 6Rx", Source: "RAN2, Qualcomm", Type: "LS in", For: "Information", AgendaItem: "5", AgendaDescription: "Incoming Liaison Statements", Status: "noted", Release: "Rel-19", RelatedWIs: "NR_ENDC", ReplyTo: "R4-2511898", To: "RAN4", Cc: "RAN1", OriginalLS: "R2-2507743"},
		{TDoc: "R1-2509526", Title: "CR to incorporate new agreements on ISAC CM", Source: "Xiaomi, AT&T", Type: "CR", For: "Agreement", Abstract: "Summary ofagreements", AgendaItem: "8.8", AgendaDescription: "Maintenance on others", Status: "agreed", IsRevisionOf: "R1-2509000", Release: "Rel-19", Spec: "38.901", Version: "19.1.0", RelatedWIs: "FS_Sensing_NR", CR: "0033", CRCategory: "F", ClausesAffected: "7.9.4.2, 7.9.5.2"},
	}
	for i := range want {
		if entries[i] != want[i] {
			t.Errorf("entry %d:\n got %+v\nwant %+v", i, entries[i], want[i])
		}
	}
}

func TestParseTDocList_Errors(t *testing.T) {
	if _, err := ParseTDocList([]byte("not a zip")); err == nil {
		t.Error("garbage accepted")
	}
	// A workbook whose only sheet has no recognizable header.
	noHeader := makeXlsx(t, map[string][][]string{"Sheet1": {{"a", "b", "c"}, {"1", "2", "3"}}}, "Sheet1")
	if _, err := ParseTDocList(noHeader); err == nil || !strings.Contains(err.Error(), "header") {
		t.Errorf("no-header workbook: %v", err)
	}
	// A header and nothing under it.
	empty := makeXlsx(t, map[string][][]string{"TDoc_List": {listHeader}}, "TDoc_List")
	if _, err := ParseTDocList(empty); err == nil || !strings.Contains(err.Error(), "no rows") {
		t.Errorf("empty list: %v", err)
	}
	// The first sheet is read when none is named TDoc_List.
	unnamed := makeXlsx(t, map[string][][]string{"Sheet1": {listHeader, {"R1-1", "t", "s"}}}, "Sheet1")
	if es, err := ParseTDocList(unnamed); err != nil || len(es) != 1 || es[0].TDoc != "R1-1" {
		t.Errorf("unnamed sheet: %v, %+v", err, es)
	}
	// Workbook parts missing.
	for name, files := range map[string]map[string][]byte{
		"no workbook":  {"xl/styles.xml": []byte("<x/>")},
		"no rels":      {"xl/workbook.xml": []byte(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheets><sheet name="a" r:id="rId1" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"/></sheets></workbook>`)},
		"no sheets":    {"xl/workbook.xml": []byte(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheets></sheets></workbook>`)},
		"bad workbook": {"xl/workbook.xml": []byte("<not xml")},
		"no part": {
			"xl/workbook.xml":            []byte(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="a" r:id="rId1"/></sheets></workbook>`),
			"xl/_rels/workbook.xml.rels": []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId2" Target="worksheets/sheet2.xml"/></Relationships>`),
		},
		"bad sheet": {
			"xl/workbook.xml":            []byte(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="a" r:id="rId1"/></sheets></workbook>`),
			"xl/_rels/workbook.xml.rels": []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="/xl/worksheets/sheet1.xml"/></Relationships>`),
			"xl/worksheets/sheet1.xml":   []byte("<worksheet><sheetData><row"),
			"xl/sharedStrings.xml":       []byte("<sst/>"),
		},
	} {
		if _, err := ParseTDocList(makeZip(t, files)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseSheetCells(t *testing.T) {
	// Cells without a reference land after the previous one; a shared
	// string index out of range reads as empty; a reference past the last
	// possible column is dropped instead of grown into.
	sheet := `<worksheet><sheetData><row r="1"><c t="s"><v>0</v></c><c t="s"><v>7</v></c><c r="D1"><v>3</v></c><c r="ZZZZZZZZ1"><v>bomb</v></c></row><row r="2"><c r="A2" t="inlineStr"><is><t>x</t></is></c></row></sheetData></worksheet>`
	rows, err := parseSheet([]byte(sheet), []string{"first"})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"first", "", "", "3"}, {"x", "", "", ""}}
	if len(rows) != 2 || strings.Join(rows[0], ",") != strings.Join(want[0], ",") || strings.Join(rows[1], ",") != strings.Join(want[1], ",") {
		t.Errorf("rows = %q, want %q", rows, want)
	}
	if _, err := parseSharedStrings([]byte("<sst><si><t>a</t>")); err == nil {
		t.Error("truncated sharedStrings accepted")
	}
	for ref, want := range map[string]int{"A1": 0, "Z9": 25, "AA1": 26, "AJ2": 35, "ab3": 27, "12": -1, "": -1} {
		if got := columnIndex(ref); got != want {
			t.Errorf("columnIndex(%q) = %d, want %d", ref, got, want)
		}
	}
	for i, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 35: "AJ"} {
		if got := columnName(i); got != want {
			t.Errorf("columnName(%d) = %q, want %q", i, got, want)
		}
	}
	// Zip entries may carry a leading slash.
	files := map[string][]byte{
		"/xl/workbook.xml":            []byte(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="a" r:id="rId1"/></sheets></workbook>`),
		"/xl/_rels/workbook.xml.rels": []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`),
		"/xl/worksheets/sheet1.xml":   []byte(sheet),
	}
	rows, err = readSheet(makeZip(t, files), "a")
	if err != nil || len(rows) != 2 || rows[0][3] != "3" {
		t.Errorf("readSheet without sharedStrings: %v, %q", err, rows)
	}
}

func TestTDocListPath(t *testing.T) {
	m := Meeting{Title: "3GPPRAN1#122-bis", DocsDir: "tsg_ran/WG1_RL1/TSGR1_122b/docs"}
	if got, want := TDocListPath(m), "tsg_ran/WG1_RL1/TSGR1_122b/docs/TDoc_List_Meeting_RAN1#122-bis.xlsx"; got != want {
		t.Errorf("TDocListPath = %q, want %q", got, want)
	}
	if TDocListPath(Meeting{Title: "3GPPRAN1#135"}) != "" {
		t.Error("a meeting without a Docs folder has a list path")
	}
}

func TestFetchTDocList(t *testing.T) {
	list := sampleList(t)
	m := Meeting{Code: "R1-123", Title: "3GPPRAN1#123", Dir: "tsg_ran/WG1_RL1/TSGR1_123", DocsDir: "tsg_ran/WG1_RL1/TSGR1_123/docs"}

	t.Run("derived name", func(t *testing.T) {
		client := fakeSite(t, map[string][]byte{"tsg_ran/WG1_RL1/TSGR1_123/docs/TDoc_List_Meeting_RAN1#123.xlsx": list})
		entries, path, err := FetchTDocList(context.Background(), client, m)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 3 || path != "tsg_ran/WG1_RL1/TSGR1_123/docs/TDoc_List_Meeting_RAN1#123.xlsx" {
			t.Errorf("got %d entries from %q", len(entries), path)
		}
	})

	t.Run("found by listing", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/ftp/tsg_ran/WG1_RL1/TSGR1_123/docs/{$}", func(w http.ResponseWriter, r *http.Request) {
			// A literal "#" in the href, as some listings write it, must not
			// be read as a fragment.
			fmt.Fprint(w, `<a href="https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2508300.zip">R1-2508300.zip</a> <a href="https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/docs/TDoc_List_Meeting_RAN1#123-final.xlsx">TDoc_List_Meeting_RAN1#123-final.xlsx</a>`)
		})
		mux.HandleFunc("/ftp/tsg_ran/WG1_RL1/TSGR1_123/docs/TDoc_List_Meeting_RAN1#123-final.xlsx", func(w http.ResponseWriter, r *http.Request) {
			w.Write(list)
		})
		client := testutil.FakeSite(t, mux)
		entries, path, err := FetchTDocList(context.Background(), client, m)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 3 || path != "tsg_ran/WG1_RL1/TSGR1_123/docs/TDoc_List_Meeting_RAN1#123-final.xlsx" {
			t.Errorf("got %d entries from %q", len(entries), path)
		}
	})

	t.Run("no list", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/ftp/tsg_ran/WG1_RL1/TSGR1_123/docs/{$}", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `<a href="https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2508300.zip">R1-2508300.zip</a>`)
		})
		_, _, err := FetchTDocList(context.Background(), testutil.FakeSite(t, mux), m)
		if !errors.Is(err, ErrNoTDocList) {
			t.Errorf("err = %v, want ErrNoTDocList", err)
		}
		_, _, err = FetchTDocList(context.Background(), nil, Meeting{Code: "R1-135"})
		if !errors.Is(err, ErrNoTDocList) {
			t.Errorf("no Docs folder: err = %v, want ErrNoTDocList", err)
		}
	})

	t.Run("listing unavailable", func(t *testing.T) {
		_, _, err := FetchTDocList(context.Background(), fakeSite(t, nil), m)
		if err == nil || !strings.Contains(err.Error(), "download") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("listed file is not a list", func(t *testing.T) {
		client := fakeSite(t, map[string][]byte{"tsg_ran/WG1_RL1/TSGR1_123/docs/TDoc_List_Meeting_RAN1#123.xlsx": []byte("PK garbage")})
		_, _, err := FetchTDocList(context.Background(), client, m)
		if err == nil || !strings.Contains(err.Error(), "parse") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, err := FetchTDocList(ctx, fakeSite(t, nil), m)
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})
}

func TestGroupForMeeting(t *testing.T) {
	for req, want := range map[string]string{
		"R1-123":                                 "R1",
		"r1-122-bis":                             "R1",
		"RAN1#123":                               "R1",
		"3GPPSA2#172":                            "S2",
		"sa2#172":                                "S2",
		"tsg_ran/WG1_RL1/TSGR1_123":              "R1",
		"/ftp/tsg_sa/WG2_Arch/x/":                "S2",
		`tsg_ct\WG4_protocollars_ex-CN4\CT4_136`: "C4",
		"TSGR1_123":                              "",
		"123":                                    "",
		"X9-123":                                 "",
		"XX#1":                                   "",
		"other/folder":                           "",
		"":                                       "",
	} {
		g, ok := GroupForMeeting(req)
		if ok != (want != "") || g.Code != want {
			t.Errorf("GroupForMeeting(%q) = %q, %v; want %q", req, g.Code, ok, want)
		}
	}
}

func TestResolveMeeting(t *testing.T) {
	client := fakeSite(t, nil)
	ctx := context.Background()
	for _, tt := range []struct {
		request, group string
		want           string
		wantErr        bool
	}{
		{"R1-123", "", "R1-123", false},
		{"RAN1#123", "", "R1-123", false},
		{"TSGR1_123", "R1", "R1-123", false},
		{"TSGR1_123", "ran1", "R1-123", false},
		{"TSGR1_123", "", "", true},
		{"R1-123", "ZZ", "", true},
		{"R1-999", "", "", true},
		{"", "", "", true},
		{"S3-104", "", "", true}, // the fake site serves only the RAN1 page
	} {
		_, m, err := ResolveMeeting(ctx, client, tt.request, tt.group, false)
		if (err != nil) != tt.wantErr {
			t.Errorf("ResolveMeeting(%q, %q): err = %v", tt.request, tt.group, err)
			continue
		}
		if err != nil {
			if !errors.Is(err, ErrNotFound) && tt.request != "S3-104" {
				t.Errorf("ResolveMeeting(%q, %q): err = %v, want ErrNotFound", tt.request, tt.group, err)
			}
			continue
		}
		if m.Code != tt.want {
			t.Errorf("ResolveMeeting(%q, %q) = %s, want %s", tt.request, tt.group, m.Code, tt.want)
		}
	}
}

func TestReadSheetRejectsBrokenZip(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	_ = zw.Close()
	if _, err := readSheet(buf.Bytes(), "x"); err == nil {
		t.Error("empty zip accepted")
	}
}

func TestResolvePrefersListedRange(t *testing.T) {
	// R1-2506700 is in RAN1#122-bis's range; naming RAN1#123 as a hint
	// must not send the download to the wrong Docs folder. A number in no
	// range still goes where the hint says.
	client := fakeSite(t, nil)
	doc, err := Resolve(context.Background(), client, "R1-2506700", "R1-123", false)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Meeting.Code != "R1-122-bis" || doc.Path != "tsg_ran/WG1_RL1/TSGR1_122b/docs/R1-2506700.zip" {
		t.Errorf("resolved to %+v", doc)
	}
	doc, err = Resolve(context.Background(), client, "R1-2599999", "R1-123", false)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Meeting.Code != "R1-123" {
		t.Errorf("unlisted number resolved to %+v", doc)
	}
}

func TestReadSheetPartErrors(t *testing.T) {
	workbook := []byte(`<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="a" r:id="rId1"/></sheets></workbook>`)
	rels := []byte(`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`)
	sheet := []byte(`<worksheet><sheetData><row r="1"><c r="A1" t="s"><v>0</v></c></row></sheetData></worksheet>`)
	for name, files := range map[string]map[string][]byte{
		"truncated sharedStrings": {"xl/workbook.xml": workbook, "xl/_rels/workbook.xml.rels": rels, "xl/worksheets/sheet1.xml": sheet, "xl/sharedStrings.xml": []byte("<sst><si><t>a</t>")},
		"missing sheet part":      {"xl/workbook.xml": workbook, "xl/_rels/workbook.xml.rels": rels},
		"bad rels":                {"xl/workbook.xml": workbook, "xl/_rels/workbook.xml.rels": []byte("<Relationships")},
	} {
		if _, err := readSheet(makeZip(t, files), "a"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A part whose deflate stream is corrupt fails on read.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for p, data := range map[string][]byte{"xl/workbook.xml": workbook, "xl/_rels/workbook.xml.rels": rels} {
		w, _ := zw.Create(p)
		w.Write(data)
	}
	w, _ := zw.CreateHeader(&zip.FileHeader{Name: "xl/worksheets/sheet1.xml", Method: zip.Deflate})
	w.Write(sheet)
	_ = zw.Close()
	data := buf.Bytes()
	// Corrupt the deflate payload of the last entry: the local header is
	// followed by the name, then the compressed bytes.
	i := bytes.LastIndex(data, []byte("xl/worksheets/sheet1.xml")) + len("xl/worksheets/sheet1.xml")
	for j := i; j < i+8 && j < len(data); j++ {
		data[j] ^= 0xff
	}
	if _, err := readSheet(data, "a"); err == nil {
		t.Error("corrupt sheet part accepted")
	}
}
