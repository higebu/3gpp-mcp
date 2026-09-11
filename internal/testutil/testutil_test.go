package testutil

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"fmt"
)

func TestFakeSite(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/a/b#c.zip", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hit "+r.URL.RawQuery)
	})
	client := FakeSite(t, mux)
	// The production host is ignored; the escaped path and the query reach
	// the handler as they are, so a "#" in a file name is not a fragment.
	resp, err := client.Get("https://www.3gpp.org/ftp/a/b%23c.zip?x=1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "hit x=1" {
		t.Errorf("got %d %q", resp.StatusCode, body)
	}
	if resp, err := client.Get("https://www.3gpp.org/other"); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Errorf("unrouted path: %v, %v", resp, err)
	}
}

func TestDynaReportPage(t *testing.T) {
	page := DynaReportPage(
		DynaReportRow{Code: "R1-123", Title: "3GPPRAN1#123", Town: "Dallas", Start: "2025-11-17", End: "2025-11-21", Dir: "tsg_ran/WG1_RL1/TSGR1_123", First: "R1-2508300", Last: "R1-2509718"},
		DynaReportRow{Code: "R1-124", Title: "3GPPRAN1#124", Start: "2026-02-09", End: "2026-02-13"},
	)
	for _, want := range []string{
		`<a name="R1-123"`,
		`href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_123\Agenda/">2025-11-17</a>`,
		`href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_123\\docs\">R1-2508300 - R1-2509718</a>`,
		`href="/../../../\ftp\tsg_ran\WG1_RL1\TSGR1_123\">Files</a>`,
		`<td>2026-02-09</td>`, // no folder: plain text, no link
		`<td>-</td><td>-</td><td>-</td><td>Files</td>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %q in:\n%s", want, page)
		}
	}
}
