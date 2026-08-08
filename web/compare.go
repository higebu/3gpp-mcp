package web

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/structdiff"
	"github.com/higebu/3gpp-mcp/internal/textdiff"
	"github.com/higebu/3gpp-mcp/tools"
)

// compareContextLines mirrors the compare_versions tool's default context.
const compareContextLines = 3

// diffLine is one line of a unified diff with its display class. The text is
// escaped by the template, so raw specification content cannot inject HTML.
type diffLine struct {
	Class string // "diff-add" | "diff-del" | "diff-hunk" | "diff-ctx"
	Text  string
}

type compareData struct {
	SpecID             string
	OldParam, NewParam string // request values, carried into generated links
	Section            string
	// OldSection is the section's number on the old side when it differs from
	// Section — a section that was renumbered and edited.
	OldSection         string
	OldLabel, NewLabel string
	// OldURLVersion/NewURLVersion are the versions section links must carry;
	// empty for the database version.
	OldURLVersion, NewURLVersion string
	Summary                      *structdiff.Result
	DiffLines                    []diffLine
	Identical                    bool
	Notice                       string
	Header                       specHeader
}

func (h *handler) handleCompare(w http.ResponseWriter, r *http.Request) {
	specID := r.PathValue("specID")
	q := r.URL.Query()

	data := compareData{
		SpecID:     specID,
		OldParam:   q.Get("old"),
		NewParam:   q.Get("new"),
		Section:    q.Get("section"),
		OldSection: q.Get("old_section"),
		// Carrying the old version keeps the tabs on the version being
		// compared: Document opens it, Compare keeps it selected.
		Header: specHeader{SpecID: specID, Active: "compare", Version: q.Get("old")},
	}

	// Without an old version there is nothing to compare yet; show the form.
	if data.OldParam == "" {
		h.renderCompare(w, data)
		return
	}

	if data.Section != "" {
		h.compareSectionPage(w, r, data)
		return
	}
	h.compareStructurePage(w, r, data)
}

func (h *handler) renderCompare(w http.ResponseWriter, data compareData) {
	if err := h.tmpls.ExecuteTemplate(w, "layout.html", layoutData{Page: "compare", Data: data, NavSpecID: data.SpecID}); err != nil {
		log.Printf("template error: %v", err)
	}
}

// renderCompareErrors reports version-resolution failures of a two-version
// read. Like the compare_versions tool, both sides are resolved before
// reporting, so a single retry can find both fetches done.
//
// A permanent error (anything but a fetch still in progress) is a confirmed
// result and takes priority over an in-progress fetch on the other side:
// showing it right away beats looping on the fetching page until that
// unrelated fetch completes, which can take minutes.
func (h *handler) renderCompareErrors(w http.ResponseWriter, oldErr, newErr error) bool {
	if oldErr == nil && newErr == nil {
		return false
	}
	var oldIP, newIP *tools.FetchInProgressError
	oldInProgress := errors.As(oldErr, &oldIP)
	newInProgress := errors.As(newErr, &newIP)

	if oldErr != nil && !oldInProgress {
		h.renderVersionError(w, oldErr)
		return true
	}
	if newErr != nil && !newInProgress {
		h.renderVersionError(w, newErr)
		return true
	}
	if oldInProgress {
		h.renderFetching(w, oldIP)
		return true
	}
	h.renderFetching(w, newIP)
	return true
}

func (h *handler) compareStructurePage(w http.ResponseWriter, r *http.Request, data compareData) {
	oldSecs, oldRes, oldErr := h.src.AllSections(r.Context(), data.SpecID, data.OldParam)
	newSecs, newRes, newErr := h.src.AllSections(r.Context(), data.SpecID, data.NewParam)
	if notice := h.familyPartsNotice(r.Context(), data.SpecID, oldErr, newErr); notice != "" {
		data.Notice = notice
		h.renderCompare(w, data)
		return
	}
	if h.renderCompareErrors(w, oldErr, newErr) {
		return
	}
	data.fillLabels(oldSecs, oldRes, newSecs, newRes)

	if notice := h.compareNotice(r.Context(), data.SpecID, oldSecs, newSecs, oldRes, newRes); notice != "" {
		data.Notice = notice
		h.renderCompare(w, data)
		return
	}

	d := structdiff.Diff(oldSecs, newSecs)
	data.Summary = &d
	if len(d.Added)+len(d.Removed)+len(d.Renumbered)+len(d.Retitled)+len(d.ContentChanged) == 0 {
		data.Notice = "No structural differences between the two versions."
	}
	h.renderCompare(w, data)
}

func (h *handler) compareSectionPage(w http.ResponseWriter, r *http.Request, data compareData) {
	// A renumbered section lives under a different number on the old side.
	oldSection := data.OldSection
	if oldSection == "" {
		oldSection = data.Section
	}
	oldSecs, oldRes, oldErr := h.src.GetSection(r.Context(), data.SpecID, data.OldParam, oldSection, false)
	newSecs, newRes, newErr := h.src.GetSection(r.Context(), data.SpecID, data.NewParam, data.Section, false)
	if notice := h.familyPartsNotice(r.Context(), data.SpecID, oldErr, newErr); notice != "" {
		data.Notice = notice
		h.renderCompare(w, data)
		return
	}
	if h.renderCompareErrors(w, oldErr, newErr) {
		return
	}
	data.fillLabels(oldSecs, oldRes, newSecs, newRes)

	if notice := h.compareNotice(r.Context(), data.SpecID, oldSecs, newSecs, oldRes, newRes); notice != "" {
		data.Notice = notice
		h.renderCompare(w, data)
		return
	}

	// A section present on one side only: numbers move between releases, so
	// point at the structural summary instead of failing.
	if len(oldSecs) == 0 || len(newSecs) == 0 {
		missing, present := data.OldLabel, data.NewLabel
		if len(newSecs) == 0 {
			missing, present = data.NewLabel, data.OldLabel
		}
		data.Notice = fmt.Sprintf(
			"Section %s does not exist in %s (it exists in %s). Section numbers move between releases — see the structural summary to locate it.",
			data.Section, missing, present)
		h.renderCompare(w, data)
		return
	}

	diff := textdiff.UnifiedKeyed(structdiff.SectionLines(oldSecs), structdiff.SectionLines(newSecs), compareContextLines, structdiff.NormalizeImageRefs)
	if diff == "" {
		data.Identical = true
		h.renderCompare(w, data)
		return
	}
	data.DiffLines = classifyDiff(diff)
	h.renderCompare(w, data)
}

// familyPartsNotice mirrors the compare_versions tool's family hint: when
// both sides of a comparison failed and the spec has split-file parts, the
// parts listing is the useful answer, not the resolve or fetch error — a
// family ID like "TS 38.101" never resolves to content of its own.
func (h *handler) familyPartsNotice(ctx context.Context, specID string, oldErr, newErr error) string {
	if oldErr == nil || newErr == nil {
		return ""
	}
	if parts, err := h.db.FindSpecIDsByFamily(ctx, specID); err == nil && len(parts) > 0 {
		return fmt.Sprintf("%s has multiple parts: %s — specify one.", specID, strings.Join(parts, ", "))
	}
	return ""
}

// compareNotice mirrors the compare_versions tool's guards: nothing on either
// side, or both requests landing on the same version.
func (h *handler) compareNotice(ctx context.Context, specID string, oldSecs, newSecs []db.Section, oldRes, newRes tools.Resolution) string {
	if len(oldSecs) == 0 && len(newSecs) == 0 {
		if parts, err := h.db.FindSpecIDsByFamily(ctx, specID); err == nil && len(parts) > 0 {
			return fmt.Sprintf("%s has multiple parts: %s — specify one.", specID, strings.Join(parts, ", "))
		}
		return fmt.Sprintf("No sections found for %s in either version.", specID)
	}
	oldV, newV := tools.ResolvedVersion(oldSecs, oldRes), tools.ResolvedVersion(newSecs, newRes)
	if oldV != "" && oldV == newV {
		return fmt.Sprintf("Both versions resolve to v%s; nothing to compare.", oldV)
	}
	return ""
}

// fillLabels derives the display labels and the version each side's section
// links must carry.
func (d *compareData) fillLabels(oldSecs []db.Section, oldRes tools.Resolution, newSecs []db.Section, newRes tools.Resolution) {
	d.OldLabel = tools.VersionLabel(oldSecs, oldRes)
	d.NewLabel = tools.VersionLabel(newSecs, newRes)
	if oldRes.Archived {
		d.OldURLVersion = oldRes.Version
	}
	if newRes.Archived {
		d.NewURLVersion = newRes.Version
	}
}

// classifyDiff splits unified-diff text into display lines. Every line of
// textdiff.Unified starts with ' ', '+', '-' or "@@", so the first bytes
// classify exactly.
func classifyDiff(diff string) []diffLine {
	lines := strings.Split(diff, "\n")
	out := make([]diffLine, 0, len(lines))
	for _, l := range lines {
		class := "diff-ctx"
		switch {
		case strings.HasPrefix(l, "@@"):
			class = "diff-hunk"
		case strings.HasPrefix(l, "+"):
			class = "diff-add"
		case strings.HasPrefix(l, "-"):
			class = "diff-del"
		}
		out = append(out, diffLine{Class: class, Text: l})
	}
	return out
}
