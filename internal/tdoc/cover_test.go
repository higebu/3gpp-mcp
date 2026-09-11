package tdoc

import (
	"strings"
	"testing"
)

// crPreamble is a CR cover sheet as the converter renders it: the identity
// table, the "affects" table and the labelled fields, some rows carrying
// two label/value pairs and spacer cells.
const crPreamble = `# Preamble

**3GPP TSG RAN WG1 #123**	**R1-2509715**

<table><tbody><tr><td colspan="9"><p><em>CR-Form-v12.2</em></p></td></tr><tr><td colspan="9"><p><strong>CHANGE REQUEST</strong></p></td></tr><tr><td><p></p></td><td><p><strong>38.213</strong></p></td><td><p><strong>CR</strong></p></td><td><p><strong>0748</strong></p></td><td><p><strong>rev</strong></p></td><td><p><strong>2</strong></p></td><td><p><strong>Current version:</strong></p></td><td><p><strong>19.1.0</strong></p></td><td><p></p></td></tr></tbody></table>

<table><tbody><tr><td><p><strong><em>Proposed change affects:</em></strong></p></td><td><p>UICC apps</p></td><td><p></p></td><td><p>ME</p></td><td><p><strong>x</strong></p></td></tr></tbody></table>

<table><tbody><tr><td><p><strong><em>Title:	</em></strong></p></td><td colspan="10"><p>Introduction of PDCCH repetitions &amp; more</p></td></tr><tr><td><p><strong><em>Source to WG:</em></strong></p></td><td colspan="10"><p>Samsung</p></td></tr><tr><td><p><strong><em>Source to TSG:</em></strong></p></td><td colspan="10"><p>R1</p></td></tr><tr><td><p><strong><em>Work item code:</em></strong></p></td><td colspan="5"><p>TEI19</p></td><td><p></p></td><td colspan="3"><p><strong><em>Date:</em></strong></p></td><td><p>2025-12-02</p></td></tr><tr><td><p><strong><em>Category:</em></strong></p></td><td><p>B</p></td><td colspan="5"><p></p></td><td colspan="3"><p><strong><em>Release:</em></strong></p></td><td><p>Rel-19</p></td></tr><tr><td><p></p></td><td colspan="8"><p><em>Use one of the following categories:</em><strong><em>F</em></strong><em> (correction)</em></p></td></tr><tr><td colspan="2"><p><strong><em>Reason for change:</em></strong></p></td><td colspan="9"><p>Line one.</p><p>Line two.</p></td></tr><tr><td colspan="2"><p><strong><em>Summary of change:</em></strong></p></td><td colspan="9"><p>Remove the restriction.</p></td></tr><tr><td colspan="2"><p><strong><em>Consequences if not approved:</em></strong></p></td><td colspan="9"><p>No support.</p></td></tr><tr><td colspan="2"><p><strong><em>Clauses affected:</em></strong></p></td><td colspan="9"><p>4.2.3, 13, Annex B (new), 5.1.1a</p></td></tr><tr><td colspan="2"><p><strong><em>Other comments:</em></strong></p></td><td colspan="9"><p></p></td></tr><tr><td colspan="2"><p><strong><em>This CR's revision history:</em></strong></p></td><td colspan="9"><p>Rev 1: typo</p></td></tr></tbody></table>
`

func TestParseCRCover(t *testing.T) {
	c := ParseCRCover(crPreamble)
	if c == nil {
		t.Fatal("cover sheet not recognized")
	}
	want := CRCover{
		Spec: "38.213", CR: "0748", Revision: "2", CurrentVersion: "19.1.0",
		Title: "Introduction of PDCCH repetitions & more", SourceWG: "Samsung", SourceTSG: "R1",
		WorkItem: "TEI19", Date: "2025-12-02", Category: "B", Release: "Rel-19",
		Reason: "Line one.\nLine two.", Summary: "Remove the restriction.", Consequences: "No support.",
		Clauses: "4.2.3, 13, Annex B (new), 5.1.1a", History: "Rev 1: typo",
	}
	if *c != want {
		t.Errorf("cover:\n got %+v\nwant %+v", *c, want)
	}
	if got := strings.Join(c.ClauseList(), ","); got != "4.2.3,13,5.1.1a" {
		t.Errorf("ClauseList = %s", got)
	}
	if c.SpecID() != "38.213" {
		t.Errorf("SpecID = %s", c.SpecID())
	}
	if ParseCRCover("# Preamble\n\nNot a CR.\n\n<table><tbody><tr><td><p>Title:</p></td><td><p>x</p></td></tr></tbody></table>") != nil {
		t.Error("a table with a Title row but no CR identity was taken for a cover sheet")
	}
	if ParseCRCover("") != nil {
		t.Error("empty preamble parsed as a cover sheet")
	}
	// An empty value cell followed by another label in the same row
	// leaves the field empty instead of taking the label.
	c = ParseCRCover(`<table><tbody><tr><td><p>CHANGE REQUEST</p></td></tr><tr><td><p></p></td><td><p>38.213</p></td><td><p>CR</p></td><td><p>0001</p></td></tr><tr><td><p>Work item code:</p></td><td><p></p></td><td><p></p></td><td><p>Date:</p></td><td><p>2025-12-02</p></td></tr><tr><td><p>Category:</p></td><td><p></p></td><td><p>Release:</p></td><td><p>Rel-19</p></td></tr></tbody></table>`)
	if c == nil || c.WorkItem != "" || c.Date != "2025-12-02" || c.Category != "" || c.Release != "Rel-19" {
		t.Errorf("empty fields before a label: %+v", c)
	}
	// The identity row alone, an old form with "Source:" instead of
	// "Source to WG:" and an empty revision cell.
	old := `<table><tbody><tr><td><p>CHANGE REQUEST</p></td></tr><tr><td><p></p></td><td><p>36.211</p></td><td><p>CR</p></td><td><p>0196</p></td><td><p>rev</p></td><td><p></p></td><td><p>Current version:</p></td><td><p>12.4.0</p></td></tr><tr><td><p>Source:</p></td><td><p>Ericsson</p></td></tr></tbody></table>`
	c = ParseCRCover(old)
	if c == nil || c.Spec != "36.211" || c.CR != "0196" || c.Revision != "" || c.CurrentVersion != "12.4.0" || c.SourceWG != "Ericsson" {
		t.Errorf("old form: %+v", c)
	}
}

const lsPreamble = `# Preamble

**3GPP TSG RAN WG#123		R1-2508303**

**Title:	Reply LS on Release Independence of 6Rx**

**Response to:**	**R2-2506735 / R4-2511898**

**Release:**	**Rel-19**

**Work Item:	NR_ENDC_RF_Ph4-Core**

Source:	RAN2

To:	RAN4

Cc:	RAN1

**Contact Person:**

Name:	Masato Kitazoe

E-mail:	mkitazoe@example.com

**Send any reply LS to:**		3GPP Liaisons Coordinator

Attachments:		None

1.	Overall Description

RAN2 would like to thank RAN4. Note: this line is body text.
`

func TestParseLSHeader(t *testing.T) {
	h := ParseLSHeader(lsPreamble)
	if h == nil {
		t.Fatal("LS header not recognized")
	}
	want := LSHeader{
		Title: "Reply LS on Release Independence of 6Rx", ResponseTo: "R2-2506735 / R4-2511898", Release: "Rel-19",
		WorkItem: "NR_ENDC_RF_Ph4-Core", Source: "RAN2", To: "RAN4", Cc: "RAN1",
		Contact: "Masato Kitazoe, mkitazoe@example.com", Attachments: "None",
	}
	if *h != want {
		t.Errorf("header:\n got %+v\nwant %+v", *h, want)
	}
	if ParseLSHeader(crPreamble) != nil {
		t.Error("a CR cover sheet parsed as an LS header")
	}
	if ParseLSHeader("# Preamble\n\nTitle: Something\n\nA plain document.") != nil {
		t.Error("a document with only a title parsed as an LS header")
	}
	if ParseLSHeader("# Preamble\n\nSource: \tMCC Support\n\nTitle:\tFinal Report of 3GPP TSG RAN WG1 #123 v1.0.0\n\nDocument for:\tApproval\n") != nil {
		t.Error("a meeting report parsed as an LS header")
	}
	// "Work Items:", "CC:" and a contact block without a blank line before
	// the next label.
	h = ParseLSHeader("**Title:**\tLS on X\n\n**Work Items:**\tA, B\n\n**Source:**\tRAN WG1\n\n**To:**\tRAN WG2\n\n**CC:**\tRAN WG4\n\n**Contact Person:**\n\nName:\tA B\nE-mail:\ta@b\n\n**Attachments:**\t2\n")
	if h == nil || h.WorkItem != "A, B" || h.Cc != "RAN WG4" || h.Contact != "A B, a@b" || h.Attachments != "2" {
		t.Errorf("variants: %+v", h)
	}
}
