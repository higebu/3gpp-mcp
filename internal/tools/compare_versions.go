package tools

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/specver"
	"github.com/higebu/3gpp-mcp/internal/structdiff"
	"github.com/higebu/3gpp-mcp/internal/textdiff"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type CompareVersionsInput struct {
	SpecID             string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 23.501)"`
	OldVersion         string `json:"old_version" jsonschema:"required,Older version to compare from (e.g. 17.9.0). Also accepts an archive token (h90) or a release selector (Rel-17). Use list_versions to see what exists."`
	NewVersion         string `json:"new_version,omitempty" jsonschema:"Newer version to compare to. Defaults to the version in the database."`
	SectionNumber      string `json:"section_number,omitempty" jsonschema:"Compare only this section's text as a unified diff (e.g. 5.15.2). Omit for a structural summary of the whole specification."`
	IncludeSubsections bool   `json:"include_subsections,omitempty" jsonschema:"With section_number: include subsections in the diff (default: false)"`
	ContextLines       int    `json:"context_lines,omitempty" jsonschema:"Unchanged lines shown around each change in a section diff (default: 3)"`
	Offset             int    `json:"offset,omitempty" jsonschema:"Start line number (0-based, default: 0)"`
	MaxLines           int    `json:"max_lines,omitempty" jsonschema:"Maximum number of lines to return (default: 200)"`
	MaxChars           int    `json:"max_chars,omitempty" jsonschema:"Maximum number of characters to return (can be combined with max_lines)"`
}

var CompareVersionsTool = &mcp.Tool{
	Name:        "compare_versions",
	Description: "Compare two versions of a 3GPP specification. Without section_number, returns a structural summary: sections added, removed, renumbered, retitled, and whose content changed. With section_number, returns a line-level unified diff of that section's text. Use list_versions first to see which versions exist; a version not yet cached is downloaded and converted on first use — when the tool says a download is in progress, call it again with the same arguments.",
}

const defaultContextLines = 3

func HandleCompareVersions(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input CompareVersionsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input CompareVersionsInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}
		if input.OldVersion == "" {
			return errorResult("old_version is required"), nil, nil
		}
		if input.SectionNumber != "" {
			return compareSection(ctx, src, input), nil, nil
		}
		return compareStructure(ctx, src, input), nil, nil
	}
}

// compareStructure summarizes which sections differ between two versions.
func compareStructure(ctx context.Context, src *Source, input CompareVersionsInput) *mcp.CallToolResult {
	// Resolve both sides before reporting any in-progress fetch: when both
	// versions need one, both background fetches are running by the time the
	// caller sees the message, so a single retry can find both ready.
	oldSecs, oldRes, oldErr := src.AllSections(ctx, input.SpecID, input.OldVersion)
	newSecs, newRes, newErr := src.AllSections(ctx, input.SpecID, input.NewVersion)
	if r := twoVersionErrorResult(oldErr, newErr); r != nil {
		if hint := familyPartsHint(ctx, src, input.SpecID, oldErr, newErr); hint != nil {
			return hint
		}
		return r
	}
	if r := checkComparable(ctx, src, input.SpecID, "", oldSecs, newSecs, oldRes, newRes); r != nil {
		return r
	}

	d := structdiff.Diff(oldSecs, newSecs)
	oldLabel, newLabel := VersionLabel(oldSecs, oldRes), VersionLabel(newSecs, newRes)

	header := CompareHeader(input.SpecID, "", oldLabel, newLabel)
	return prependLine(header, paginateText(RenderStructuralSummary(d, oldLabel, newLabel), input.Offset, input.MaxLines, input.MaxChars))
}

// RenderStructuralSummary renders a structural diff between two versions as
// Markdown. It is shared with the CLI's compare-versions command.
func RenderStructuralSummary(d structdiff.Result, oldLabel, newLabel string) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Structural changes: %d sections in %s, %d in %s\n", d.OldCount, oldLabel, d.NewCount, newLabel)
	fmt.Fprintf(&sb, "Added: %d | Removed: %d | Renumbered: %d | Title changed: %d | Content changed: %d | Unchanged: %d\n",
		len(d.Added), len(d.Removed), len(d.Renumbered), len(d.Retitled), len(d.ContentChanged), d.Unchanged)

	if len(d.Added)+len(d.Removed)+len(d.Renumbered)+len(d.Retitled)+len(d.ContentChanged) == 0 {
		sb.WriteString("\nNo structural differences between the two versions.")
	}
	if len(d.Added) > 0 {
		fmt.Fprintf(&sb, "\n## Added in %s\n", newLabel)
		for _, s := range d.Added {
			fmt.Fprintf(&sb, "- %s %s\n", s.Number, s.Title)
		}
	}
	if len(d.Removed) > 0 {
		fmt.Fprintf(&sb, "\n## Removed since %s\n", oldLabel)
		for _, s := range d.Removed {
			fmt.Fprintf(&sb, "- %s %s\n", s.Number, s.Title)
		}
	}
	if len(d.Renumbered) > 0 {
		sb.WriteString("\n## Renumbered (same title)\n")
		for _, r := range d.Renumbered {
			fmt.Fprintf(&sb, "- %s → %s  %s\n", r.OldNumber, r.NewNumber, r.Title)
		}
	}
	if len(d.Retitled) > 0 {
		sb.WriteString("\n## Title changed\n")
		for _, r := range d.Retitled {
			fmt.Fprintf(&sb, "- %s: %q → %q\n", r.Number, r.OldTitle, r.NewTitle)
		}
	}
	if len(d.ContentChanged) > 0 {
		sb.WriteString("\n## Content changed\n")
		for _, c := range d.ContentChanged {
			fmt.Fprintf(&sb, "- %s %s  (%d → %d lines)\n", c.Number, c.Title, c.OldLines, c.NewLines)
		}
		sb.WriteString("\nUse compare_versions with section_number to see the text diff of a changed section.")
	}
	return sb.String()
}

// compareSection renders a unified diff of one section's text.
func compareSection(ctx context.Context, src *Source, input CompareVersionsInput) *mcp.CallToolResult {
	oldSecs, oldRes, oldErr := src.GetSection(ctx, input.SpecID, input.OldVersion, input.SectionNumber, input.IncludeSubsections)
	newSecs, newRes, newErr := src.GetSection(ctx, input.SpecID, input.NewVersion, input.SectionNumber, input.IncludeSubsections)
	if r := twoVersionErrorResult(oldErr, newErr); r != nil {
		if hint := familyPartsHint(ctx, src, input.SpecID, oldErr, newErr); hint != nil {
			return hint
		}
		return r
	}
	if r := checkComparable(ctx, src, input.SpecID, input.SectionNumber, oldSecs, newSecs, oldRes, newRes); r != nil {
		return r
	}

	oldLabel, newLabel := VersionLabel(oldSecs, oldRes), VersionLabel(newSecs, newRes)

	// A section present on one side only is an informational answer, not a
	// failure: numbers move between releases, and the structural summary or
	// the older TOC locates the new home.
	if len(oldSecs) == 0 || len(newSecs) == 0 {
		missing, present := oldLabel, newLabel
		if len(newSecs) == 0 {
			missing, present = newLabel, oldLabel
		}
		return textResult(fmt.Sprintf(
			"Section %s of %s does not exist in %s (it exists in %s). Section numbers move between releases — run compare_versions without section_number, or get_toc for %s, to locate it.",
			input.SectionNumber, input.SpecID, missing, present, missing))
	}

	ctxLines := input.ContextLines
	if ctxLines <= 0 {
		ctxLines = defaultContextLines
	}

	header := CompareHeader(input.SpecID, input.SectionNumber, oldLabel, newLabel)
	diff := textdiff.UnifiedKeyed(structdiff.SectionLines(oldSecs), structdiff.SectionLines(newSecs), ctxLines, structdiff.NormalizeImageRefs)
	if diff == "" {
		msg := fmt.Sprintf("Section %s is identical between %s and %s.", input.SectionNumber, oldLabel, newLabel)
		return prependLine(header, textResult(msg))
	}
	return prependLine(header, paginateText(diff, input.Offset, input.MaxLines, input.MaxChars))
}

// twoVersionErrorResult reports version-resolution errors for a two-version
// read, folding a double in-progress fetch into one retry hint.
func twoVersionErrorResult(oldErr, newErr error) *mcp.CallToolResult {
	var oldIP, newIP *FetchInProgressError
	if errors.As(oldErr, &oldIP) && errors.As(newErr, &newIP) {
		return textResult(fmt.Sprintf(
			"%s v%s and v%s are being downloaded and converted. This takes up to a few minutes for a large specification. Call the same tool again to get the comparison.",
			oldIP.SpecID, oldIP.Version, newIP.Version))
	}
	if oldErr != nil {
		return versionErrorResult(oldErr, "failed to read old version")
	}
	if newErr != nil {
		return versionErrorResult(newErr, "failed to read new version")
	}
	return nil
}

// checkComparable rejects a comparison with nothing on either side or with
// both requests landing on the same version.
// familyPartsHint lists the split-file parts of specID when both sides of a
// comparison failed: a family ID like "TS 38.101" never resolves to content
// of its own (each part is its own spec), so the parts listing is the useful
// answer, not the resolve or fetch error.
func familyPartsHint(ctx context.Context, src *Source, specID string, oldErr, newErr error) *mcp.CallToolResult {
	if oldErr == nil || newErr == nil {
		return nil
	}
	parts, err := src.DB.FindSpecIDsByFamily(ctx, specID)
	if err != nil {
		return errorResult(fmt.Sprintf("failed to check %s for parts: %v", specID, err))
	}
	if len(parts) > 0 {
		return errorResult(fmt.Sprintf("%s has multiple parts: %s — specify one", specID, strings.Join(parts, ", ")))
	}
	return nil
}

func checkComparable(ctx context.Context, src *Source, specID, sectionNumber string, oldSecs, newSecs []db.Section, oldRes, newRes Resolution) *mcp.CallToolResult {
	if len(oldSecs) == 0 && len(newSecs) == 0 {
		parts, err := src.DB.FindSpecIDsByFamily(ctx, specID)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to check %s for parts: %v", specID, err))
		}
		if len(parts) > 0 {
			return errorResult(fmt.Sprintf("%s has multiple parts: %s — specify one", specID, strings.Join(parts, ", ")))
		}
		if sectionNumber != "" {
			// Both sides came back empty because the requested section does
			// not exist in either version, not because the spec itself
			// failed to resolve — say so, rather than implying the whole
			// specification is empty.
			return errorResult(fmt.Sprintf(
				"Section %s does not exist in %s in either version. Section numbers move between releases — run compare_versions without section_number, or get_toc, to locate it.",
				sectionNumber, specID))
		}
		return errorResult(fmt.Sprintf("no sections found for %s in either version", specID))
	}
	oldV, newV := ResolvedVersion(oldSecs, oldRes), ResolvedVersion(newSecs, newRes)
	if oldV != "" && oldV == newV {
		return textResult(fmt.Sprintf("%s: old_version and new_version both resolve to v%s; nothing to compare.", specID, oldV))
	}
	return nil
}

// ResolvedVersion names the version a request landed on. The Resolution knows
// it except on the database default path, where the rows do.
func ResolvedVersion(secs []db.Section, res Resolution) string {
	if res.Version != "" {
		return res.Version
	}
	if len(secs) > 0 {
		return secs[0].Version
	}
	return ""
}

// VersionLabel renders one side of a comparison, e.g. "v17.9.0 (Rel-17, archived)".
func VersionLabel(secs []db.Section, res Resolution) string {
	name := ResolvedVersion(secs, res)
	if name == "" {
		return "the database version"
	}
	label := "v" + name
	var notes []string
	if len(secs) > 0 {
		if rel := specver.ReleaseLabel(secs[0].Release); rel != "" {
			notes = append(notes, rel)
		}
	}
	if res.Archived {
		notes = append(notes, "archived")
	}
	if len(notes) > 0 {
		label += " (" + strings.Join(notes, ", ") + ")"
	}
	return label
}

// CompareHeader builds the provenance line prepended to every page of a
// comparison. It is shared with the CLI's compare-versions command.
func CompareHeader(specID, sectionNumber, oldLabel, newLabel string) string {
	h := "[Compare: " + specID
	if sectionNumber != "" {
		h += " — Section " + sectionNumber
	}
	return h + fmt.Sprintf(" — %s → %s]", oldLabel, newLabel)
}
