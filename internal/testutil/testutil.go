// Package testutil provides shared test helpers for the 3gpp-mcp project.
package testutil

import (
	"io"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/higebu/3gpp-mcp/db"
)

// SeedData is the canonical seed SQL used across test packages.
const SeedData = `
INSERT INTO specs (id, title, version, release, series) VALUES
    ('TS 23.501', 'System architecture for the 5G System (5GS)', '18.6.0', 'Rel-18', '23'),
    ('TS 29.510', 'Network Function Repository Services', '18.5.0', 'Rel-18', '29');

INSERT INTO sections (spec_id, number, title, level, parent_number, content) VALUES
    ('TS 23.501', '1', 'Scope', 1, NULL, '# 1 Scope
This document defines the system architecture.'),
    ('TS 23.501', '5', 'Architecture', 1, NULL, '# 5 Architecture
The 5G system architecture is defined here.'),
    ('TS 23.501', '5.1', 'General', 2, '5', '## 5.1 General
General architecture description for 5G.'),
    ('TS 23.501', '5.1.1', 'Overview', 3, '5.1', '### 5.1.1 Overview
Overview of the architecture components.'),
    ('TS 29.510', '1', 'Scope', 1, NULL, '# 1 Scope
This document defines the NRF services.'),
    ('TS 29.510', '6', 'API Definitions', 1, NULL, '# 6 API Definitions
API definitions for NRF.');

INSERT INTO specs (id, title, version, release, series) VALUES
    ('TS 24.229', 'IP multimedia call control protocol', '18.4.0', 'Rel-18', '24');

INSERT INTO sections (spec_id, number, title, level, parent_number, content) VALUES
    ('TS 24.229', '5', 'Procedures', 1, NULL, '# 5 Procedures
The IMS registration procedures.'),
    ('TS 24.229', '5.1', 'Registration', 2, '5', '## 5.1 Registration
The IMS registration procedures are specified in 3GPP TS 23.228 clause 5.2.1.
The security mechanisms are defined in TS 33.203. See also RFC 3261 section 10.2
for SIP registration details and IETF RFC 3327 for the Path header.
The authentication uses IMS-AKA as described in TS 33.203 subclause 6.1.');

INSERT INTO spec_references (source_spec_id, source_section, target_spec, target_section, context) VALUES
    ('TS 24.229', '5.1', 'TS 23.228', '5.2.1', '...specified in 3GPP TS 23.228 clause 5.2.1...'),
    ('TS 24.229', '5.1', 'TS 33.203', '', '...security mechanisms are defined in TS 33.203...'),
    ('TS 24.229', '5.1', 'RFC 3261', '10.2', '...RFC 3261 section 10.2 for SIP registration...'),
    ('TS 24.229', '5.1', 'RFC 3327', '', '...IETF RFC 3327 for the Path header...'),
    ('TS 24.229', '5.1', 'TS 33.203', '6.1', '...IMS-AKA as described in TS 33.203 subclause 6.1...');

INSERT INTO openapi_specs (spec_id, api_name, version, filename, content) VALUES
    ('TS 29.510', 'Nnrf_NFManagement', 'v1.3.0', 'TS29510_Nnrf_NFManagement.yaml', 'openapi: 3.0.0
info:
  title: Nnrf_NFManagement
  version: v1.3.0
paths:
  /nf-instances:
    get:
      summary: List NF Instances
  /nf-instances/{nfInstanceID}:
    put:
      summary: Register NF Instance
components:
  schemas:
    NFProfile:
      type: object
      properties:
        nfInstanceId:
          type: string');
`

// SetupTestDB creates a temporary SQLite database with the standard schema and seed data.
func SetupTestDB(t testing.TB) *db.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := d.ExecScript(db.Schema); err != nil {
		t.Fatalf("failed to create schema: %v", err)
	}
	if err := d.ExecScript(SeedData); err != nil {
		t.Fatalf("failed to seed data: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
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
