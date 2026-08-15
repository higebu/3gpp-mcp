package asn1index

import (
	"context"

	"github.com/higebu/3gpp-mcp/internal/db"
)

// Rebuild replaces the asn1_defs index with what the sections currently
// hold, wholesale — extraction is per-spec, so an incremental refresh would
// also be sound, but a single transactional swap is simpler and the whole
// corpus extracts in seconds through the FTS seed. Returns a summary line
// for the build log.
func Rebuild(ctx context.Context, d *db.DB) (string, error) {
	sections, err := d.ASN1Sections(ctx)
	if err != nil {
		return "", err
	}
	assignments := Extract(sections)
	defs := make([]db.ASN1Def, 0, len(assignments))
	for _, a := range assignments {
		defs = append(defs, db.ASN1Def{
			Name:          a.Name,
			Key:           Key(a.Name),
			SpecID:        a.Section.SpecID,
			Version:       a.Section.Version,
			SectionNumber: a.Section.Number,
			SectionTitle:  a.Section.Title,
			Body:          a.Text,
		})
	}
	if err := d.ReplaceASN1Defs(defs); err != nil {
		return "", err
	}
	return db.ASN1IndexStats(defs), nil
}
