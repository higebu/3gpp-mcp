// Package testutil provides shared test helpers for the 3gpp-mcp project.
package testutil

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/internal/db"
)

// SeedData is the canonical seed SQL used across test packages.
const SeedData = `
INSERT INTO specs (id, version, version_token, title, release, series) VALUES
    ('TS 23.501', '18.6.0', 'i60', 'System architecture for the 5G System (5GS)', '18', '23'),
    ('TS 29.510', '18.5.0', 'i50', 'Network Function Repository Services', '18', '29');

INSERT INTO sections (spec_id, version, number, title, level, parent_number, content) VALUES
    ('TS 23.501', '18.6.0', '1', 'Scope', 1, NULL, '# 1 Scope
This document defines the system architecture.'),
    ('TS 23.501', '18.6.0', '5', 'Architecture', 1, NULL, '# 5 Architecture
The 5G system architecture is defined here.'),
    ('TS 23.501', '18.6.0', '5.1', 'General', 2, '5', '## 5.1 General
General architecture description for 5G.'),
    ('TS 23.501', '18.6.0', '5.1.1', 'Overview', 3, '5.1', '### 5.1.1 Overview
Overview of the architecture components.'),
    ('TS 29.510', '18.5.0', '1', 'Scope', 1, NULL, '# 1 Scope
This document defines the NRF services.'),
    ('TS 29.510', '18.5.0', '6', 'API Definitions', 1, NULL, '# 6 API Definitions
API definitions for NRF.');

INSERT INTO specs (id, version, version_token, title, release, series) VALUES
    ('TS 24.229', '18.4.0', 'i40', 'IP multimedia call control protocol', '18', '24');

INSERT INTO sections (spec_id, version, number, title, level, parent_number, content) VALUES
    ('TS 24.229', '18.4.0', '5', 'Procedures', 1, NULL, '# 5 Procedures
The IMS registration procedures.'),
    ('TS 24.229', '18.4.0', '5.1', 'Registration', 2, '5', '## 5.1 Registration
The IMS registration procedures are specified in 3GPP TS 23.228 clause 5.2.1.
The security mechanisms are defined in TS 33.203. See also RFC 3261 section 10.2
for SIP registration details and IETF RFC 3327 for the Path header.
The authentication uses IMS-AKA as described in TS 33.203 subclause 6.1.');

INSERT INTO spec_references (source_spec_id, source_version, source_section, target_spec, target_section, context) VALUES
    ('TS 24.229', '18.4.0', '5.1', 'TS 23.228', '5.2.1', '...specified in 3GPP TS 23.228 clause 5.2.1...'),
    ('TS 24.229', '18.4.0', '5.1', 'TS 33.203', '', '...security mechanisms are defined in TS 33.203...'),
    ('TS 24.229', '18.4.0', '5.1', 'RFC 3261', '10.2', '...RFC 3261 section 10.2 for SIP registration...'),
    ('TS 24.229', '18.4.0', '5.1', 'RFC 3327', '', '...IETF RFC 3327 for the Path header...'),
    ('TS 24.229', '18.4.0', '5.1', 'TS 33.203', '6.1', '...IMS-AKA as described in TS 33.203 subclause 6.1...');

INSERT INTO openapi_specs (spec_id, api_name, version, filename, content) VALUES
    ('TS 29.510', 'Nnrf_NFManagement', 'v1.3.0', 'TS29510_Nnrf_NFManagement.yaml', 'openapi: 3.0.0
info:
  title: Nnrf_NFManagement
  version: v1.3.0
paths:
  /nf-instances:
    get:
      summary: List NF Instances
      parameters:
        - name: target-nf-type
          in: query
  /nf-instances/{nfInstanceID}:
    put:
      summary: Register NF Instance
      requestBody:
        content:
          application/json:
            schema:
              $ref: ''#/components/schemas/NFProfile''
components:
  schemas:
    NFProfile:
      type: object
      description: Information of an NF Instance registered in the NRF
      properties:
        nfInstanceId:
          type: string
        nfType:
          $ref: ''#/components/schemas/NFType''
    NFType:
      type: string
      description: NF types known to the NRF');
`

// SetupTestDB creates a temporary SQLite database with the standard schema and seed data.
func SetupTestDB(t testing.TB) *db.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	// Register before the ExecScript calls: their t.Fatalf would otherwise
	// leave the database open.
	t.Cleanup(func() { _ = d.Close() })
	if err := d.ExecScript(db.Schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}
	if err := d.ExecScript(SeedData); err != nil {
		t.Fatalf("failed to seed data: %v", err)
	}
	return d
}

// DownloadTestZip fetches a ZIP from the 3GPP archive for use in tests.
// The test is skipped when -short is set or the download fails.
func DownloadTestZip(t testing.TB, url string) []byte {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping download test in -short mode")
	}
	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Skipf("skipping: cannot download %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Skipf("skipping: HTTP %d for %s", resp.StatusCode, url)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Skipf("skipping: read body failed: %v", err)
	}
	return data
}

// redirectTransport routes every request to a test server, keeping the
// path and query, so production URLs can be used unchanged.
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

// FakeSite serves handler on a test server and returns a client that sends
// every request there, whatever host the request names. It stands in for
// www.3gpp.org in tests of the meeting-document code.
func FakeSite(t testing.TB, handler http.Handler) *http.Client {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}
}

// DynaReportRow is one meeting of a DynaReportPage.
type DynaReportRow struct {
	Code, Title, Town, Start, End string
	// Dir is the meeting folder relative to /ftp/; empty for a meeting
	// without one.
	Dir string
	// First and Last bound the TDoc range; empty for a meeting without
	// documents.
	First, Last string
}

// DynaReportPage renders a DynaReport meeting page in the markup the
// 3GPP site produces, one row per meeting.
func DynaReportPage(rows ...DynaReportRow) string {
	var sb strings.Builder
	sb.WriteString("<table>\n<tr><th>Meeting</th><th>Title</th><th>Town</th><th>Start</th><th>End</th><th>First &amp; Last tdoc</th><th>Register</th><th>Participants</th><th>Files</th></tr>\n")
	for _, r := range rows {
		link := func(sub, text string) string {
			if r.Dir == "" {
				return text
			}
			return `<a target="_blank" href="/../../../\ftp\` + strings.ReplaceAll(r.Dir, "/", `\`) + `\` + sub + `">` + text + `</a>`
		}
		fmt.Fprintf(&sb, `<tr><td><a name="%s" href="https://portal.3gpp.org/Home.aspx#/meeting?MtgId=1">%s</a></td><td><a name="bm%s">%s</a></td><td>%s</td><td>%s</td><td>%s</td>`,
			r.Code, r.Code, r.Code, r.Title, link("Invitation/", r.Town), link("Agenda/", r.Start), link("Report/", r.End))
		if r.First != "" {
			fmt.Fprintf(&sb, "<td>%s</td>", link(`\docs\`, r.First+" - "+r.Last))
		} else {
			sb.WriteString("<td>-</td>")
		}
		fmt.Fprintf(&sb, "<td>-</td><td>-</td><td>%s</td></tr>\n", link("", "Files"))
	}
	sb.WriteString("</table>")
	return sb.String()
}
