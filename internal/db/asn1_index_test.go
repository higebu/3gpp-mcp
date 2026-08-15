package db

import (
	"context"
	"errors"
	"testing"
)

// seedASN1Index installs a small index by hand, bypassing internal/asn1index
// (which has its own tests): two specs, one name defined twice in one of
// them, and a fuzzy-key sibling.
func seedASN1Index(t *testing.T, d *DB) {
	t.Helper()
	defs := []ASN1Def{
		{Name: "AMF-UE-NGAP-ID", Key: "amfuengapid", SpecID: "TS 23.501", Version: "18.6.0", SectionNumber: "9.4.5", SectionTitle: "IE definitions", Body: "AMF-UE-NGAP-ID ::= INTEGER (0..1099511627775)"},
		{Name: "Cause", Key: "cause", SpecID: "TS 23.501", Version: "18.6.0", SectionNumber: "9.4.5", SectionTitle: "IE definitions", Body: "Cause ::= CHOICE { misc CauseMisc }"},
		{Name: "Cause", Key: "cause", SpecID: "TS 23.501", Version: "18.6.0", SectionNumber: "9.4.6", SectionTitle: "Common definitions", Body: "Cause ::= INTEGER (0..7)"},
		{Name: "CAUSE-Ext", Key: "causeext", SpecID: "TS 29.510", Version: "18.5.0", SectionNumber: "6.2", SectionTitle: "Extensions", Body: "CAUSE-Ext ::= INTEGER"},
	}
	if err := d.ReplaceASN1Defs(defs); err != nil {
		t.Fatalf("ReplaceASN1Defs: %v", err)
	}
}

func TestASN1IndexLookup(t *testing.T) {
	d := setupTestDB(t)
	seedASN1Index(t, d)
	ctx := context.Background()

	if ok, err := d.HasASN1Index(ctx); err != nil || !ok {
		t.Fatalf("HasASN1Index = %v, %v", ok, err)
	}

	t.Run("exact returns every definition with the release joined", func(t *testing.T) {
		defs, err := d.LookupASN1(ctx, "Cause", "cause", "")
		if err != nil {
			t.Fatalf("LookupASN1: %v", err)
		}
		if len(defs) != 2 || defs[0].SectionNumber != "9.4.5" || defs[1].SectionNumber != "9.4.6" {
			t.Fatalf("defs = %+v", defs)
		}
		if defs[0].Release != "18" {
			t.Errorf("release not joined: %+v", defs[0])
		}
	})

	t.Run("fuzzy resolves by key", func(t *testing.T) {
		defs, err := d.LookupASN1(ctx, "AMF UE NGAP ID", "amfuengapid", "")
		if err != nil || len(defs) != 1 || defs[0].Name != "AMF-UE-NGAP-ID" {
			t.Fatalf("defs = %+v, err = %v", defs, err)
		}
	})

	t.Run("fuzzy pins to the first name on a key collision", func(t *testing.T) {
		extra := []ASN1Def{
			{Name: "Foo-Bar", Key: "foobar", SpecID: "TS 23.501", Version: "18.6.0", SectionNumber: "1", SectionTitle: "t", Body: "Foo-Bar ::= INTEGER"},
			{Name: "FooBAR", Key: "foobar", SpecID: "TS 29.510", Version: "18.5.0", SectionNumber: "2", SectionTitle: "t", Body: "FooBAR ::= INTEGER"},
		}
		if err := d.ReplaceASN1Defs(extra); err != nil {
			t.Fatalf("ReplaceASN1Defs: %v", err)
		}
		defs, err := d.LookupASN1(ctx, "foo bar", "foobar", "")
		if err != nil || len(defs) != 1 || defs[0].Name != "Foo-Bar" {
			t.Fatalf("defs = %+v, err = %v", defs, err)
		}
		seedASN1Index(t, d) // restore for the remaining subtests
	})

	t.Run("spec filter", func(t *testing.T) {
		defs, err := d.LookupASN1(ctx, "Cause", "cause", "TS 29.510")
		if err != nil || len(defs) != 0 {
			t.Fatalf("defs = %+v, err = %v", defs, err)
		}
		defs, err = d.LookupASN1(ctx, "CAUSE-Ext", "causeext", "TS 29.510")
		if err != nil || len(defs) != 1 {
			t.Fatalf("defs = %+v, err = %v", defs, err)
		}
	})

	t.Run("empty key never falls back", func(t *testing.T) {
		defs, err := d.LookupASN1(ctx, "!!!", "", "")
		if err != nil || defs != nil {
			t.Fatalf("defs = %+v, err = %v", defs, err)
		}
	})
}

func TestASN1SpecListingAndSuggestions(t *testing.T) {
	d := setupTestDB(t)
	seedASN1Index(t, d)
	ctx := context.Background()

	listing, err := d.ASN1SpecListing(ctx, "TS 23.501")
	if err != nil {
		t.Fatalf("ASN1SpecListing: %v", err)
	}
	if len(listing) != 3 || listing[0].Name != "AMF-UE-NGAP-ID" || listing[0].Release != "18" {
		t.Fatalf("listing = %+v", listing)
	}
	if listing[0].Body != "" {
		t.Errorf("listing must not carry bodies: %+v", listing[0])
	}

	// Suggestions match by containment in either direction, deduplicate, and
	// respect the spec filter and the cap.
	names, err := d.ASN1NameSuggestions(ctx, "causemisc", "", 20)
	if err != nil || len(names) != 1 || names[0] != "Cause" {
		t.Fatalf("names = %v, err = %v", names, err)
	}
	names, err = d.ASN1NameSuggestions(ctx, "cause", "", 20)
	if err != nil || len(names) != 2 {
		t.Fatalf("names = %v, err = %v", names, err)
	}
	names, err = d.ASN1NameSuggestions(ctx, "cause", "TS 29.510", 20)
	if err != nil || len(names) != 1 || names[0] != "CAUSE-Ext" {
		t.Fatalf("names = %v, err = %v", names, err)
	}
	names, err = d.ASN1NameSuggestions(ctx, "cause", "", 1)
	if err != nil || len(names) != 1 {
		t.Fatalf("cap not applied: %v, %v", names, err)
	}
	if names, err := d.ASN1NameSuggestions(ctx, "", "", 20); err != nil || names != nil {
		t.Fatalf("empty key: %v, %v", names, err)
	}
}

func TestASN1IndexMissing(t *testing.T) {
	d := setupTestDB(t)
	if err := d.Exec("DROP TABLE asn1_defs"); err != nil {
		t.Fatalf("drop: %v", err)
	}
	ctx := context.Background()

	if ok, err := d.HasASN1Index(ctx); err != nil || ok {
		t.Fatalf("HasASN1Index = %v, %v", ok, err)
	}
	if _, err := d.LookupASN1(ctx, "X", "x", ""); !errors.Is(err, ErrNoASN1Index) {
		t.Errorf("LookupASN1 err = %v, want ErrNoASN1Index", err)
	}
	if _, err := d.ASN1SpecListing(ctx, "TS 23.501"); !errors.Is(err, ErrNoASN1Index) {
		t.Errorf("ASN1SpecListing err = %v, want ErrNoASN1Index", err)
	}
	if _, err := d.ASN1NameSuggestions(ctx, "x", "", 20); !errors.Is(err, ErrNoASN1Index) {
		t.Errorf("ASN1NameSuggestions err = %v, want ErrNoASN1Index", err)
	}

	// DropASN1Index is idempotent, and InitSchema restores the table.
	if err := d.DropASN1Index(); err != nil {
		t.Fatalf("DropASN1Index: %v", err)
	}
	if err := d.InitSchema(); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if ok, err := d.HasASN1Index(ctx); err != nil || !ok {
		t.Fatalf("HasASN1Index after InitSchema = %v, %v", ok, err)
	}
}

func TestASN1DefSpecsAndStats(t *testing.T) {
	defs := []ASN1Def{
		{Name: "A", SpecID: "TS 1.1", SectionNumber: "1"},
		{Name: "A", SpecID: "TS 1.1", SectionNumber: "2"},
		{Name: "A", SpecID: "TS 2.2", SectionNumber: "1"},
	}
	specs := ASN1DefSpecs(defs)
	if len(specs) != 2 || specs[0] != "TS 1.1" || specs[1] != "TS 2.2" {
		t.Errorf("specs = %v", specs)
	}
	if got := ASN1IndexStats(defs); got != "3 assignments from 3 sections across 2 specs" {
		t.Errorf("stats = %q", got)
	}
}

func TestASN1Sections(t *testing.T) {
	d := setupTestDB(t)
	// Both marker spellings must be found through the FTS seed; a section
	// without a marker must not.
	inserts := []struct{ number, content string }{
		{"90.1", "```asn1\n-- ASN1START\nFoo ::= INTEGER\n-- ASN1STOP\n```"},
		{"90.2", "```asn1\n--ASN1START\nBar ::= INTEGER\n--ASN1STOP\n```"},
		{"90.3", "no markers here"},
	}
	for _, in := range inserts {
		if err := d.Exec(`INSERT INTO sections (spec_id, version, number, title, level, parent_number, content)
			VALUES ('TS 23.501', '18.6.0', ?, 't', 2, NULL, ?)`, in.number, in.content); err != nil {
			t.Fatalf("insert %s: %v", in.number, err)
		}
	}

	sections, err := d.ASN1Sections(context.Background())
	if err != nil {
		t.Fatalf("ASN1Sections: %v", err)
	}
	got := map[string]bool{}
	for _, s := range sections {
		got[s.Number] = true
		if s.Release == "" || s.Content == "" {
			t.Errorf("section %s missing release or content: %+v", s.Number, s)
		}
	}
	if !got["90.1"] || !got["90.2"] || got["90.3"] {
		t.Errorf("sections found = %v", got)
	}
}
