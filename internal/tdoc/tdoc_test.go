package tdoc

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseID(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		number  int
		wantErr bool
	}{
		{in: "R1-2509715", want: "R1-2509715", number: 2509715},
		{in: "r1-2509715", want: "R1-2509715", number: 2509715},
		{in: " R12509715 ", want: "R1-2509715", number: 2509715},
		{in: "RP-252001", want: "RP-252001", number: 252001},
		{in: "S2-2510076", want: "S2-2510076", number: 2510076},
		{in: "R1-100983", want: "R1-100983", number: 100983},
		{in: "TS 23.501", wantErr: true},
		{in: "X9-2509715", wantErr: true},
		{in: "R1-25", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range tests {
		id, err := ParseID(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseID(%q) = %v, want error", tt.in, id)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseID(%q): %v", tt.in, err)
			continue
		}
		if id.String() != tt.want || id.Number != tt.number {
			t.Errorf("ParseID(%q) = %v (%d), want %s (%d)", tt.in, id, id.Number, tt.want, tt.number)
		}
	}
	if got := (ID{}).String(); got != "" {
		t.Errorf("zero ID renders as %q, want empty", got)
	}
}

func TestGroupByCode(t *testing.T) {
	for _, in := range []string{"R1", "r1", "RAN1", "ran1"} {
		g, ok := GroupByCode(in)
		if !ok || g.Dir != "tsg_ran/WG1_RL1" {
			t.Errorf("GroupByCode(%q) = %+v, %v", in, g, ok)
		}
	}
	if _, ok := GroupByCode("Q9"); ok {
		t.Error("unknown group resolved")
	}
	gs := Groups()
	if len(gs) != len(groups) || gs[0].Code > gs[1].Code {
		t.Errorf("Groups() = %v", gs)
	}
}

// meetingPage mimics the DynaReport table: rows copied from the live RAN1
// and SA3 pages, plus a header row and a future meeting with no folder.
const meetingPage = `<table>
<tr><th>Meeting</th><th>Title</th><th>Town</th><th>Start</th><th>End</th><th>First &amp; Last tdoc</th><th>Register</th><th>Participants</th><th>Files</th><th>iCal</th><th>Feedback</th></tr>
<tr class="odd">    <td width="60"><a name="R1-124" target="_blank" href="https://portal.3gpp.org/Home.aspx#/meeting?MtgId=60710">R1-124</a></span></td>    <td width="100" align="left"><a name="bmR1-124--2026-02-09">3GPPRAN1#124</a></td>    <td width="100" align="left">Gothenburg</td>    <td width="80" align="center">2026&#8209;02&#8209;09</td>    <td width="80" align="center">2026&#8209;02&#8209;13</td>    <td width="160" align="center">-</td>    <td width="40" align="center">-</td>    <td width="40" align="center">-</td>    <td width="40" align="center">-</td>    <td width="80" align="center">-</td>    <td width="80" align="center">-</td>  </tr>
<tr class="even">    <td width="60"><a name="R1-123" target="_blank" href="https://portal.3gpp.org/Home.aspx#/meeting?MtgId=60607">R1-123</a></span></td>    <td width="100" align="left"><a name="bmR1-123--2025-11-17">3GPPRAN1#123</a></td>    <td width="100" align="left"><a target="_blank" href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_123\Invitation/">Dallas</a></td>    <td width="80" align="center"><a target="_blank" href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_123\Agenda/">2025&#8209;11&#8209;17</a></td>    <td width="80" align="center"><a target="_blank" href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_123\Report/">2025&#8209;11&#8209;21</a></td>    <td width="160" align="left"><a target="_blank" href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_123\\docs\">R1-2508300 - R1-2509718</a>&nbsp;&nbsp;<a target="_blank" href="https://portal.3gpp.org/ngppapp/TdocList.aspx?meetingId=60607"><br>full document list</a></td>    <td width="40" align="center">-</td>    <td width="40" align="center"><a target="_blank" href="https://webapp.etsi.org/3GPPRegistration/fViewPart.asp?mid=60607">Participants</a></td>    <td width="40" align="center"><a target="_blank" href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_123\">Files</a></td>    <td width="80" align="center">-</td>    <td width="80" align="center">-</td>  </tr>
<tr class="odd">    <td width="60"><a name="R1-122-bis" target="_blank" href="https://portal.3gpp.org/Home.aspx#/meeting?MtgId=60603">R1-122-bis</a></span></td>    <td width="100" align="left"><a name="bmR1-122-bis--2025-10-13">3GPPRAN1#122-bis</a></td>    <td width="100" align="left"><a target="_blank" href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_122b\Invitation/">Prague</a></td>    <td width="80" align="center"><a target="_blank" href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_122b\Agenda/">2025&#8209;10&#8209;13</a></td>    <td width="80" align="center"><a target="_blank" href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_122b\Report/">2025&#8209;10&#8209;17</a></td>    <td width="160" align="left"><a target="_blank" href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_122b\\docs\">R1-2506700 - R1-2508262</a>&nbsp;&nbsp;<a target="_blank" href="https://portal.3gpp.org/ngppapp/TdocList.aspx?meetingId=60603"><br>full document list</a></td>    <td width="40" align="center">-</td>    <td width="40" align="center"><a target="_blank" href="https://webapp.etsi.org/3GPPRegistration/fViewPart.asp?mid=60603">Participants</a></td>    <td width="40" align="center"><a target="_blank" href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_122b\">Files</a></td>    <td width="80" align="center">-</td>    <td width="80" align="center">-</td>  </tr>
<tr class="odd">    <td width="60"><a name="S3-104-LI" target="_blank" href="https://portal.3gpp.org/Home.aspx#/meeting?MtgId=82260">S3-104-LI</a></span></td>    <td width="100" align="left"><a name="bmS3-104-LI--2027-01-19">3GPPSA3#104-LI</a></td>    <td width="100" align="left"><a target="_blank" href="/../../../\ftp\TSG_SA\WG3_Security\TSGS3_LI\2027_104_Sophia\Invitation/">Sophia-Antipolis</a></td>    <td width="80" align="center"><a target="_blank" href="/../../../\ftp\TSG_SA\WG3_Security\TSGS3_LI\2027_104_Sophia\Agenda/">2027&#8209;01&#8209;19</a></td>    <td width="80" align="center">2027&#8209;01&#8209;22</td>    <td width="160" align="center">-</td> 	<td width="40" align="center"><a target="_blank" href="https://webapp.etsi.org/3GPPRegistration/fMain.asp?mid=82260">Register</a></td>    <td width="40" align="center">-</td>    <td width="40" align="center"><a target="_blank" href="/../../../\ftp\TSG_SA\WG3_Security\TSGS3_LI\2027_104_Sophia\">Files</a></td>    <td width="80" align="center">-</td>    <td width="80" align="center">-</td>  </tr>
<tr class="even">    <td width="60"><a name="S3-125-e" target="_blank" href="https://portal.3gpp.org/Home.aspx#/meeting?MtgId=83976">S3-125-e</a></span></td>    <td width="100" align="left"><a name="bmS3-125-e--2026-01-19">3GPPSA3#125-e</a></td>    <td width="100" align="left"><a target="_blank" href="/../../../\ftp\\tsg_sa\WG3_Security\TSGS3_125AdHoc-e\Invitation/">Online</a></td>    <td width="80" align="center"><a target="_blank" href="/../../../\ftp\\tsg_sa\WG3_Security\TSGS3_125AdHoc-e\Agenda/">2026&#8209;01&#8209;19</a></td>    <td width="80" align="center"><a target="_blank" href="/../../../\ftp\\tsg_sa\WG3_Security\TSGS3_125AdHoc-e\Report/">2026&#8209;01&#8209;23</a></td>    <td width="160" align="left"><a target="_blank" href="/../../../\ftp\\tsg_sa\WG3_Security\TSGS3_125AdHoc-e\\docs\">S3-260000 - S3-260031</a>&nbsp;&nbsp;<a target="_blank" href="https://portal.3gpp.org/ngppapp/TdocList.aspx?meetingId=83976"><br>full document list</a></td>    <td width="40" align="center">-</td>    <td width="40" align="center">-</td>    <td width="40" align="center"><a target="_blank" href="/../../../\ftp\\tsg_sa\WG3_Security\TSGS3_125AdHoc-e\">Files</a></td>    <td width="80" align="center">-</td>    <td width="80" align="center">-</td>  </tr>
<tr class="odd">    <td width="60"><a name="S3-104" target="_blank" href="https://portal.3gpp.org/Home.aspx#/meeting?MtgId=38149">S3-104</a></span></td>    <td width="100" align="left"><a name="bmS3-104--2021-08-23">3GPPSA3#104</a></td>    <td width="100" align="left"><a target="_blank" href="/../../..//Specification-Groups/">Bath</a></td>    <td width="80" align="center">2021&#8209;08&#8209;23</td>    <td width="80" align="center">2021&#8209;08&#8209;27</td>    <td width="160" align="center"> - </td>    <td width="40" align="center">-</td>    <td width="40" align="center">-</td>    <td width="40" align="center">-</td>    <td width="80" align="center">-</td>    <td width="80" align="center">-</td>  </tr>
</table>`

func TestParseMeetings(t *testing.T) {
	ms := ParseMeetings(meetingPage)
	if len(ms) != 6 {
		t.Fatalf("got %d meetings, want 6: %+v", len(ms), ms)
	}
	want := []Meeting{
		{Code: "R1-124", Title: "3GPPRAN1#124", Town: "Gothenburg", Start: "2026-02-09", End: "2026-02-13"},
		{Code: "R1-123", Title: "3GPPRAN1#123", Town: "Dallas", Start: "2025-11-17", End: "2025-11-21",
			Dir: "tsg_ran/WG1_RL1/TSGR1_123", DocsDir: "tsg_ran/WG1_RL1/TSGR1_123/docs",
			FirstTDoc: ID{"R1", 2508300, "2508300"}, LastTDoc: ID{"R1", 2509718, "2509718"}},
		{Code: "R1-122-bis", Title: "3GPPRAN1#122-bis", Town: "Prague", Start: "2025-10-13", End: "2025-10-17",
			Dir: "tsg_ran/WG1_RL1/TSGR1_122b", DocsDir: "tsg_ran/WG1_RL1/TSGR1_122b/docs",
			FirstTDoc: ID{"R1", 2506700, "2506700"}, LastTDoc: ID{"R1", 2508262, "2508262"}},
		{Code: "S3-104-LI", Title: "3GPPSA3#104-LI", Town: "Sophia-Antipolis", Start: "2027-01-19", End: "2027-01-22",
			Dir: "TSG_SA/WG3_Security/TSGS3_LI/2027_104_Sophia", DocsDir: "TSG_SA/WG3_Security/TSGS3_LI/2027_104_Sophia/Docs"},
		{Code: "S3-125-e", Title: "3GPPSA3#125-e", Town: "Online", Start: "2026-01-19", End: "2026-01-23",
			Dir: "tsg_sa/WG3_Security/TSGS3_125AdHoc-e", DocsDir: "tsg_sa/WG3_Security/TSGS3_125AdHoc-e/docs",
			FirstTDoc: ID{"S3", 260000, "260000"}, LastTDoc: ID{"S3", 260031, "260031"}},
		{Code: "S3-104", Title: "3GPPSA3#104", Town: "Bath", Start: "2021-08-23", End: "2021-08-27"},
	}
	for i := range want {
		if ms[i] != want[i] {
			t.Errorf("meeting %d:\n got %+v\nwant %+v", i, ms[i], want[i])
		}
	}
	if ms[1].Folder() != "TSGR1_123" {
		t.Errorf("Folder() = %q", ms[1].Folder())
	}

	// The disk cache round-trips every field.
	decoded, err := decodeMeetings(encodeMeetings(ms))
	if err != nil {
		t.Fatalf("decodeMeetings: %v", err)
	}
	for i := range ms {
		if decoded[i] != ms[i] {
			t.Errorf("cache round trip %d:\n got %+v\nwant %+v", i, decoded[i], ms[i])
		}
	}
	if _, err := decodeMeetings([]string{"short"}); err == nil {
		t.Error("malformed cache line accepted")
	}
}

func TestFindMeeting(t *testing.T) {
	ms := ParseMeetings(meetingPage)
	for _, req := range []string{"R1-123", "r1-123", "RAN1#123", "3GPPRAN1#123", "#123", "123", "TSGR1_123", "tsg_ran/WG1_RL1/TSGR1_123"} {
		m, ok := FindMeeting(ms, req)
		if !ok || m.Code != "R1-123" {
			t.Errorf("FindMeeting(%q) = %+v, %v", req, m, ok)
		}
	}
	if m, ok := FindMeeting(ms, "122-bis"); !ok || m.Code != "R1-122-bis" {
		t.Errorf("FindMeeting(122-bis) = %+v, %v", m, ok)
	}
	for _, req := range []string{"", "R1-999", "RAN2#123"} {
		if m, ok := FindMeeting(ms, req); ok {
			t.Errorf("FindMeeting(%q) = %+v, want miss", req, m)
		}
	}
}

func TestHolds(t *testing.T) {
	ms := ParseMeetings(meetingPage)
	r1123 := ms[1]
	for _, tt := range []struct {
		id   string
		want bool
	}{
		{"R1-2508300", true}, {"R1-2509715", true}, {"R1-2509718", true},
		{"R1-2509719", false}, {"R1-2508262", false}, {"S3-2509000", false},
	} {
		id, err := ParseID(tt.id)
		if err != nil {
			t.Fatal(err)
		}
		if got := r1123.Holds(id); got != tt.want {
			t.Errorf("Holds(%s) = %v, want %v", tt.id, got, tt.want)
		}
	}
	if ms[0].Holds(ID{"R1", 1, "1"}) {
		t.Error("a meeting without a range holds nothing")
	}
}

// redirectTransport routes every request to the test server, keeping the
// path, so production URLs can be used unchanged.
type redirectTransport struct {
	base    http.RoundTripper
	testURL string
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, err := url.Parse(rt.testURL + req.URL.EscapedPath() + "?" + req.URL.RawQuery)
	if err != nil {
		return nil, err
	}
	req.URL = target
	return rt.base.RoundTrip(req)
}

func fakeSite(t *testing.T, files map[string][]byte) *http.Client {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/dynareport", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("code") != "Meetings-R1.htm" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, meetingPage)
	})
	for p, data := range files {
		data := data
		mux.HandleFunc("/ftp/"+p, func(w http.ResponseWriter, _ *http.Request) {
			w.Write(data)
		})
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}
}

func TestFetchMeetings(t *testing.T) {
	client := fakeSite(t, nil)
	g, _ := GroupByCode("R1")
	ms, err := FetchMeetings(context.Background(), client, g, false)
	if err != nil {
		t.Fatalf("FetchMeetings: %v", err)
	}
	if len(ms) != 6 || ms[1].Code != "R1-123" {
		t.Errorf("got %+v", ms)
	}
	if _, err := FetchMeetings(context.Background(), client, Group{Code: "R2", Name: "RAN2"}, false); err == nil {
		t.Error("expected an error for a missing page")
	}
}

func TestResolve(t *testing.T) {
	client := fakeSite(t, nil)
	ctx := context.Background()

	t.Run("by TDoc range", func(t *testing.T) {
		doc, err := Resolve(ctx, client, "r1-2509715", "", false)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if doc.ID != "R1-2509715" || doc.Path != "tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509715.zip" || doc.Meeting.Code != "R1-123" || doc.Group.Code != "R1" {
			t.Errorf("got %+v", doc)
		}
		if doc.URL() != "https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509715.zip" {
			t.Errorf("URL() = %s", doc.URL())
		}
	})

	t.Run("older meeting", func(t *testing.T) {
		doc, err := Resolve(ctx, client, "R1-2507000", "", false)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if doc.Meeting.Code != "R1-122-bis" {
			t.Errorf("got meeting %s", doc.Meeting.Code)
		}
	})

	t.Run("out of every range", func(t *testing.T) {
		_, err := Resolve(ctx, client, "R1-2600001", "", false)
		if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "pass the meeting") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("explicit meeting", func(t *testing.T) {
		for _, m := range []string{"R1-123", "RAN1#123", "TSGR1_123"} {
			doc, err := Resolve(ctx, client, "R1-2600001", m, false)
			if err != nil {
				t.Fatalf("Resolve(meeting %q): %v", m, err)
			}
			if doc.Path != "tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2600001.zip" {
				t.Errorf("meeting %q: path %s", m, doc.Path)
			}
		}
		if _, err := Resolve(ctx, client, "R1-2600001", "R1-999", false); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown meeting: err = %v", err)
		}
	})

	t.Run("explicit folder", func(t *testing.T) {
		doc, err := Resolve(ctx, client, "R1-2600001", "https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_125/", false)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if doc.Path != "tsg_ran/WG1_RL1/TSGR1_125/Docs/R1-2600001.zip" {
			t.Errorf("path = %s", doc.Path)
		}
		doc, err = Resolve(ctx, client, "R1-2600001", "tsg_ran/WG1_RL1/TSGR1_125/Docs", false)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		if doc.Path != "tsg_ran/WG1_RL1/TSGR1_125/Docs/R1-2600001.zip" || doc.Meeting.Dir != "tsg_ran/WG1_RL1/TSGR1_125" {
			t.Errorf("got %+v", doc)
		}
	})

	t.Run("meeting without folder", func(t *testing.T) {
		_, err := Resolve(ctx, client, "R1-2600001", "R1-124", false)
		if !errors.Is(err, ErrNotFound) || !strings.Contains(err.Error(), "no document folder") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("by path", func(t *testing.T) {
		for _, req := range []string{
			"tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1#123_v100.zip",
			"/ftp/tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1#123_v100.zip",
			"https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1%23123_v100.zip",
		} {
			doc, err := Resolve(ctx, client, req, "", false)
			if err != nil {
				t.Fatalf("Resolve(%q): %v", req, err)
			}
			if doc.Path != "tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1#123_v100.zip" || doc.ID != doc.Path {
				t.Errorf("Resolve(%q) = %+v", req, doc)
			}
			if doc.URL() != "https://www.3gpp.org/ftp/tsg_ran/WG1_RL1/TSGR1_123/Report/Final_Minutes_report_RAN1%23123_v100.zip" {
				t.Errorf("URL() = %s", doc.URL())
			}
		}
	})

	t.Run("rejects foreign and traversing paths", func(t *testing.T) {
		for _, req := range []string{"https://example.com/ftp/x.zip", "//evil.example/ftp/x.zip", "../etc/passwd", "tsg_ran/../../x.zip", "TS 23.501"} {
			if doc, err := Resolve(ctx, client, req, "", false); err == nil {
				t.Errorf("Resolve(%q) = %+v, want error", req, doc)
			}
		}
	})
}

// makeZip builds a zip from name -> content.
func makeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makeDocx builds a minimal .docx whose body is the given paragraphs; a
// paragraph starting with "# " becomes a Heading 1.
func makeDocx(t *testing.T, paragraphs ...string) []byte {
	t.Helper()
	const contentTypes = `<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="xml" ContentType="application/xml"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`
	const rels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`
	var body strings.Builder
	for _, p := range paragraphs {
		if rest, ok := strings.CutPrefix(p, "# "); ok {
			fmt.Fprintf(&body, `<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>%s</w:t></w:r></w:p>`, rest)
			continue
		}
		fmt.Fprintf(&body, `<w:p><w:r><w:t xml:space="preserve">%s</w:t></w:r></w:p>`, p)
	}
	doc := `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` + body.String() + `</w:body></w:document>`
	styles := `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/></w:style>
</w:styles>`
	return makeZip(t, map[string][]byte{
		"[Content_Types].xml": []byte(contentTypes),
		"_rels/.rels":         []byte(rels),
		"word/document.xml":   []byte(doc),
		"word/styles.xml":     []byte(styles),
	})
}

func TestFetch(t *testing.T) {
	ctx := context.Background()
	ls := makeDocx(t,
		"3GPP TSG RAN WG1 #123\tR1-2509496",
		"Title:\tLS on updated Rel-19 RAN1 UE features lists",
		"Source:\tRAN WG1",
		"# 1 Overall Description",
		"RAN1 has discussed the features list.",
	)
	files := map[string][]byte{
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509496.zip": makeZip(t, map[string][]byte{
			"R1-2509496.docx": ls,
			"R1-2509494.zip":  makeZip(t, map[string][]byte{"R1-2509494 Features.docx": makeDocx(t, "attachment")}),
		}),
		// A minutes TDoc: the report plus spreadsheets, and a stray README.
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2508301.zip": makeZip(t, map[string][]byte{
			"TDoc_List.xlsx":          []byte("xlsx"),
			"R1-2508301_Minutes.docx": makeDocx(t, "# 1 Opening", "Minutes body."),
			"__MACOSX/._x":            []byte("junk"),
		}),
		// Only an attachment zip holds a document.
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509000.zip": makeZip(t, map[string][]byte{
			"inner.zip": makeZip(t, map[string][]byte{"Agenda.docx": makeDocx(t, "# 1 Agenda", "Items.")}),
		}),
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509001.zip": makeZip(t, map[string][]byte{"slides.pptx": []byte("pptx")}),
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509002.zip": makeZip(t, map[string][]byte{"R1-2509002.doc": []byte("legacy")}),
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509003.zip": []byte("not a zip"),
		"tsg_ran/WG1_RL1/TSGR1_123/Report/report.docx":  makeDocx(t, "# 1 Opening", "Report body."),
	}
	client := fakeSite(t, files)

	t.Run("LS with attachment", func(t *testing.T) {
		doc, err := Resolve(ctx, client, "R1-2509496", "", false)
		if err != nil {
			t.Fatal(err)
		}
		f, err := Fetch(ctx, client, doc)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if f.Title != "LS on updated Rel-19 RAN1 UE features lists" {
			t.Errorf("Title = %q", f.Title)
		}
		if f.MainFile != "R1-2509496.docx" {
			t.Errorf("MainFile = %q", f.MainFile)
		}
		wantFiles := []string{"R1-2509494.zip", "R1-2509494.zip/R1-2509494 Features.docx", "R1-2509496.docx"}
		if strings.Join(sorted(f.Files), "|") != strings.Join(wantFiles, "|") {
			t.Errorf("Files = %v, want %v", f.Files, wantFiles)
		}
		if len(f.Sections) != 2 {
			t.Fatalf("got %d sections: %+v", len(f.Sections), f.Sections)
		}
		pre := f.Sections[0]
		if pre.SpecID != "R1-2509496" || pre.Number != "" || !strings.Contains(pre.Content, "Source:") {
			t.Errorf("preamble = %+v", pre)
		}
		if f.Sections[1].Number != "1" || !strings.Contains(f.Sections[1].Content, "features list") {
			t.Errorf("clause 1 = %+v", f.Sections[1])
		}
	})

	t.Run("prefers the file named after the TDoc", func(t *testing.T) {
		doc, _ := Resolve(ctx, client, "R1-2508301", "", false)
		f, err := Fetch(ctx, client, doc)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if f.MainFile != "R1-2508301_Minutes.docx" || len(f.Files) != 2 {
			t.Errorf("MainFile = %q, Files = %v", f.MainFile, f.Files)
		}
		if f.Title != "R1-2508301" {
			t.Errorf("Title = %q, want the ID when the document names none", f.Title)
		}
	})

	t.Run("falls back to a nested zip", func(t *testing.T) {
		doc, _ := Resolve(ctx, client, "R1-2509000", "", false)
		f, err := Fetch(ctx, client, doc)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if f.MainFile != "inner.zip/Agenda.docx" {
			t.Errorf("MainFile = %q", f.MainFile)
		}
	})

	t.Run("unsupported format lists the files", func(t *testing.T) {
		doc, _ := Resolve(ctx, client, "R1-2509001", "", false)
		_, err := Fetch(ctx, client, doc)
		if !errors.Is(err, ErrUnsupported) || !strings.Contains(err.Error(), "slides.pptx") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("legacy doc without LibreOffice", func(t *testing.T) {
		defer func(orig func(string) (string, error)) { lookPath = orig }(lookPath)
		lookPath = func(string) (string, error) { return "", errors.New("not found") }
		doc, _ := Resolve(ctx, client, "R1-2509002", "", false)
		_, err := Fetch(ctx, client, doc)
		if !errors.Is(err, ErrNeedsLibreOffice) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("corrupt archive", func(t *testing.T) {
		doc, _ := Resolve(ctx, client, "R1-2509003", "", false)
		if _, err := Fetch(ctx, client, doc); !errors.Is(err, ErrUnsupported) {
			t.Errorf("err = %v, want ErrUnsupported", err)
		}
	})

	t.Run("bare docx by path", func(t *testing.T) {
		doc, _ := Resolve(ctx, client, "tsg_ran/WG1_RL1/TSGR1_123/Report/report.docx", "", false)
		f, err := Fetch(ctx, client, doc)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if f.MainFile != "report.docx" || len(f.Sections) != 1 || f.Sections[0].SpecID != doc.Path {
			t.Errorf("got %+v", f)
		}
	})

	t.Run("missing", func(t *testing.T) {
		doc, _ := Resolve(ctx, client, "R1-2509700", "", false)
		if _, err := Fetch(ctx, client, doc); err == nil {
			t.Error("expected an error for a 404")
		}
	})
}

func sorted(s []string) []string {
	out := append([]string(nil), s...)
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func TestDocumentTitle(t *testing.T) {
	cr := "<table><tr><td>Title:</td><td>Introduction of PDCCH repetitions &amp; more</td></tr></table>"
	if got := titleFromPreamble(cr); got != "Introduction of PDCCH repetitions & more" {
		t.Errorf("CR title = %q", got)
	}
	if got := titleFromPreamble("Title:\tLS on something\nSource: X"); got != "LS on something" {
		t.Errorf("LS title = %q", got)
	}
	if got := titleFromPreamble("**Title:**\tLS on bold\n\n**Release:** Rel-19"); got != "LS on bold" {
		t.Errorf("bold LS title = %q", got)
	}
	if got := titleFromPreamble("**Title:\tDraft Agenda**\n\n**Document for:\tDecision**"); got != "Draft Agenda" {
		t.Errorf("bold agenda title = %q", got)
	}
	if got := titleFromPreamble("Subtitle: not this\nno title here"); got != "" {
		t.Errorf("title = %q, want none", got)
	}
}

// makeDocxWithImage builds a .docx whose one heading is followed by an
// inline PNG figure.
func makeDocxWithImage(t *testing.T, png []byte) []byte {
	t.Helper()
	const contentTypes = `<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="xml" ContentType="application/xml"/>
<Default Extension="png" ContentType="image/png"/>
<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`
	const rels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
</Relationships>`
	const docRels = `<?xml version="1.0"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId5" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`
	const doc = `<?xml version="1.0"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
<w:body>
<w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>1 Figure</w:t></w:r></w:p>
<w:p><w:r><w:drawing><wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing">` +
		`<a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData>` +
		`<pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:blipFill>` +
		`<a:blip r:embed="rId5"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>
</w:body>
</w:document>`
	const styles = `<?xml version="1.0"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/></w:style>
</w:styles>`
	return makeZip(t, map[string][]byte{
		"[Content_Types].xml":          []byte(contentTypes),
		"_rels/.rels":                  []byte(rels),
		"word/document.xml":            []byte(doc),
		"word/_rels/document.xml.rels": []byte(docRels),
		"word/media/image1.png":        png,
		"word/styles.xml":              []byte(styles),
	})
}

// installFakeLibreOffice puts a `libreoffice` script first on PATH that
// "converts" any .doc by writing docx (or nothing, when docx is nil) into the
// --outdir it is given, so the .doc path is exercised without LibreOffice.
func installFakeLibreOffice(t *testing.T, docx []byte) {
	t.Helper()
	dir := t.TempDir()
	src := ""
	if docx != nil {
		src = filepath.Join(dir, "converted.docx")
		if err := os.WriteFile(src, docx, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	script := "#!/bin/sh\n" +
		"out=''; in=''\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in --outdir) out=\"$2\"; shift;; *.doc) in=\"$1\";; esac; shift; done\n" +
		"[ -n \"$FAKE_DOCX\" ] && cp \"$FAKE_DOCX\" \"$out/$(basename \"$in\" .doc).docx\"\n" +
		"exit 0\n"
	if err := os.WriteFile(filepath.Join(dir, "libreoffice"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_DOCX", src)
}

func TestFetch_MoreCases(t *testing.T) {
	ctx := context.Background()
	var manyNested = map[string][]byte{"real.docx": makeDocx(t, "# 1 Real", "body")}
	for i := range maxNestedZips + 2 {
		manyNested[fmt.Sprintf("att%02d.zip", i)] = makeZip(t, map[string][]byte{fmt.Sprintf("a%d.docx", i): makeDocx(t, "x")})
	}
	files := map[string][]byte{
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509100.zip": makeZip(t, map[string][]byte{"R1-2509100.docx": makeDocxWithImage(t, []byte("png-bytes"))}),
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509101.zip": makeZip(t, map[string][]byte{
			"R1-2509101.docx": makeDocx(t, "# 1 Main", "main"),
			"broken.zip":      []byte("this is not a zip"),
			"dir/":            nil,
			"sub/..evil.docx": makeDocx(t, "x"),
		}),
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509102.zip": makeZip(t, manyNested),
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509103.zip": makeZip(t, map[string][]byte{"R1-2509103.docx": makeDocx(t)}),
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509104.zip": makeZip(t, map[string][]byte{"R1-2509104.doc": []byte("legacy")}),
		"tsg_ran/WG1_RL1/TSGR1_123/Report/old.doc":      []byte("legacy"),
		"tsg_ran/WG1_RL1/TSGR1_123/docs/R1-2509105.zip": makeZip(t, map[string][]byte{"R1-2509105.docx": []byte("not a docx")}),
	}
	client := fakeSite(t, files)

	t.Run("images are returned", func(t *testing.T) {
		doc, _ := Resolve(ctx, client, "R1-2509100", "", false)
		f, err := Fetch(ctx, client, doc)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if len(f.Images) != 1 || f.Images[0].Name != "image1.png" || string(f.Images[0].Data) != "png-bytes" || f.Images[0].SpecID != "R1-2509100" {
			t.Errorf("Images = %+v", f.Images)
		}
	})

	t.Run("skips corrupt nested zips, directories and traversing names", func(t *testing.T) {
		doc, _ := Resolve(ctx, client, "R1-2509101", "", false)
		f, err := Fetch(ctx, client, doc)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if strings.Join(sorted(f.Files), "|") != "R1-2509101.docx|broken.zip" {
			t.Errorf("Files = %v", f.Files)
		}
	})

	t.Run("opens at most maxNestedZips attachments", func(t *testing.T) {
		doc, _ := Resolve(ctx, client, "R1-2509102", "", false)
		f, err := Fetch(ctx, client, doc)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		nested := 0
		for _, name := range f.Files {
			if strings.Contains(name, ".zip/") {
				nested++
			}
		}
		if nested != maxNestedZips || f.MainFile != "real.docx" {
			t.Errorf("nested members = %d (want %d), main = %q", nested, maxNestedZips, f.MainFile)
		}
	})

	t.Run("document without text", func(t *testing.T) {
		doc, _ := Resolve(ctx, client, "R1-2509103", "", false)
		if _, err := Fetch(ctx, client, doc); !errors.Is(err, ErrUnsupported) {
			t.Errorf("err = %v, want ErrUnsupported", err)
		}
	})

	t.Run("unparseable docx", func(t *testing.T) {
		doc, _ := Resolve(ctx, client, "R1-2509105", "", false)
		if _, err := Fetch(ctx, client, doc); err == nil || !strings.Contains(err.Error(), "parse") {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("legacy doc converted", func(t *testing.T) {
		installFakeLibreOffice(t, makeDocx(t, "# 1 Converted", "from doc"))
		doc, _ := Resolve(ctx, client, "R1-2509104", "", false)
		f, err := Fetch(ctx, client, doc)
		if err != nil {
			t.Fatalf("Fetch: %v", err)
		}
		if f.MainFile != "R1-2509104.doc" || len(f.Sections) != 1 || !strings.Contains(f.Sections[0].Content, "from doc") {
			t.Errorf("got %+v", f)
		}
		// A bare .doc named by path takes the same route.
		doc, _ = Resolve(ctx, client, "tsg_ran/WG1_RL1/TSGR1_123/Report/old.doc", "", false)
		f, err = Fetch(ctx, client, doc)
		if err != nil {
			t.Fatalf("Fetch bare .doc: %v", err)
		}
		if f.MainFile != "old.doc" || len(f.Sections) != 1 {
			t.Errorf("got %+v", f)
		}
	})

	t.Run("legacy doc conversion produced nothing", func(t *testing.T) {
		installFakeLibreOffice(t, nil)
		doc, _ := Resolve(ctx, client, "R1-2509104", "", false)
		if _, err := Fetch(ctx, client, doc); err == nil || !strings.Contains(err.Error(), "produced no .docx") {
			t.Errorf("err = %v", err)
		}
	})
}

func TestSanitizeName(t *testing.T) {
	for in, want := range map[string]string{
		"R1-2509715 CR_38213.docx": "R1-2509715 CR_38213.docx",
		`a/b\c:d*e?f"g<h>i|j.docx`: "a_b_c_d_e_f_g_h_i_j.docx",
		"":                         "document",
		".":                        "document",
		"..":                       "document",
	} {
		if got := sanitizeName(in); got != want {
			t.Errorf("sanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := titleFromPreamble("Title:\t-\n"); got != "" {
		t.Errorf("a dash title = %q, want none", got)
	}
	if got := documentTitle(nil, "R1-1"); got != "R1-1" {
		t.Errorf("documentTitle(no sections) = %q", got)
	}
}

func TestFetchMeetings_Cache(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	hits := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/dynareport", func(w http.ResponseWriter, r *http.Request) {
		hits++
		switch r.URL.Query().Get("code") {
		case "Meetings-R1.htm":
			fmt.Fprint(w, meetingPage)
		case "Meetings-R2.htm":
			fmt.Fprint(w, "<html><body>nothing here</body></html>")
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	g, _ := GroupByCode("R1")
	for i := range 2 {
		ms, err := FetchMeetings(context.Background(), client, g, true)
		if err != nil {
			t.Fatalf("FetchMeetings #%d: %v", i, err)
		}
		if len(ms) != 6 || ms[1].Code != "R1-123" || ms[1].FirstTDoc.String() != "R1-2508300" {
			t.Errorf("#%d: got %+v", i, ms[1])
		}
	}
	if hits != 1 {
		t.Errorf("page fetched %d times, want 1 (second call must hit the cache)", hits)
	}

	r2, _ := GroupByCode("R2")
	if _, err := FetchMeetings(context.Background(), client, r2, true); err == nil || !strings.Contains(err.Error(), "lists no meetings") {
		t.Errorf("empty page: err = %v", err)
	}
	if _, err := FetchMeetings(context.Background(), client, Group{Code: "S5", Name: "SA5"}, false); err == nil {
		t.Error("expected an error for a failing page")
	}
}

func TestParseMeetings_Edges(t *testing.T) {
	page := `<table>
<tr><td>too</td><td>few</td></tr>
<tr><td>has space</td><td>x</td><td>x</td><td>2025-01-01</td><td>2025-01-02</td><td>-</td></tr>
<tr><td>R1-1</td><td>3GPPRAN1#1</td><td>x</td><td>not a date</td><td>2025-01-02</td><td>-</td></tr>
<tr><td>R1-2</td><td>3GPPRAN1#2</td><td><a href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_2\..\Invitation/">x</a></td><td>2025‑01‑01</td><td>2025‑01‑02</td><td>R1-1 - S2-2</td></tr>
</table>`
	ms := ParseMeetings(page)
	if len(ms) != 1 || ms[0].Code != "R1-2" || ms[0].FirstTDoc.Prefix != "" || ms[0].Dir != "" {
		t.Errorf("got %+v", ms)
	}
	if _, err := decodeMeetings(nil); err == nil {
		t.Error("empty cache accepted")
	}
	if _, err := decodeMeetings([]string{"{not json"}); err == nil {
		t.Error("malformed cache line accepted")
	}
	if got := exampleMeeting([]Meeting{{Code: "R1-9"}}); got != "R1-123" {
		t.Errorf("exampleMeeting fallback = %q", got)
	}
}

func TestCanonicalIDAndPaths(t *testing.T) {
	for in, want := range map[string]string{
		"r1-2509715":                           "R1-2509715",
		"https://www.3gpp.org/ftp/a/b.zip":     "a/b.zip",
		"https://3gpp.org/ftp/a/b.zip":         "a/b.zip",
		"ftp/a/b.zip":                          "a/b.zip",
		`tsg_ran\WG1_RL1\x.zip`:                "tsg_ran/WG1_RL1/x.zip",
		"/a/./b.zip":                           "a/b.zip",
		"a/b%23c.zip":                          "a/b#c.zip",
		"https://www.3gpp.org/ftp/a/b%23c.zip": "a/b#c.zip",
	} {
		got, ok := CanonicalID(in)
		if !ok || got != want {
			t.Errorf("CanonicalID(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	for _, in := range []string{"TS 23.501", "https://www.3gpp.org/", "/ftp/", "//", "https://example.org/ftp/x.zip", "tsg_ran/%2e%2e/x.zip", "https://www.3gpp.org/ftp/%2E%2E/x.zip"} {
		if got, ok := CanonicalID(in); ok {
			t.Errorf("CanonicalID(%q) = %q, want rejection", in, got)
		}
	}
}

func TestResolve_Errors(t *testing.T) {
	client := fakeSite(t, nil)
	ctx := context.Background()
	if _, err := Resolve(ctx, client, "R2-2600001", "", false); err == nil {
		t.Error("expected an error when the meeting page cannot be fetched")
	}
	if _, err := Resolve(ctx, client, "R1-2600001", "../x/", false); !errors.Is(err, ErrNotFound) {
		t.Errorf("invalid folder: err = %v", err)
	}
}
