package tools

import (
	"context"
	"errors"
	"fmt"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/tdoc"
	"github.com/higebu/3gpp-mcp/internal/tdocstore"
)

// DocumentUnavailableError is returned when a meeting document cannot be
// served: the number resolves to no meeting, the download failed, or the
// download holds nothing convertible.
type DocumentUnavailableError struct {
	ID     string
	Reason string
}

func (e *DocumentUnavailableError) Error() string {
	return fmt.Sprintf("%s is not available: %s", e.ID, e.Reason)
}

// TDoc returns a meeting document's record, fetching the document on demand.
// request is a TDoc number or an FTP path; meeting optionally names the
// meeting when the number falls in no known TDoc range.
func (s *Source) TDoc(ctx context.Context, request, meeting string) (*tdocstore.Document, error) {
	if s.TDocs == nil {
		return nil, &DocumentUnavailableError{ID: request, Reason: "on-demand fetching of meeting documents is disabled"}
	}

	// A cached document is served without the meeting listing.
	if id, ok := tdoc.CanonicalID(request); ok {
		cached, err := s.TDocs.Has(id)
		if err != nil {
			return nil, err
		}
		if cached {
			return s.cachedTDoc(id)
		}
	}

	doc, err := tdoc.Resolve(ctx, s.Client, request, meeting, s.UseCache)
	if err != nil {
		return nil, &DocumentUnavailableError{ID: request, Reason: err.Error()}
	}
	switch err := s.TDocs.Ensure(ctx, doc, s.Budget); {
	case err == nil:
	case errors.Is(err, tdocstore.ErrInProgress):
		return nil, &FetchInProgressError{SpecID: doc.ID}
	default:
		return nil, &DocumentUnavailableError{ID: doc.ID, Reason: err.Error()}
	}
	return s.cachedTDoc(doc.ID)
}

// cachedTDoc reads a record that Has or Ensure just reported present. An
// empty read means a concurrent fetch's eviction dropped it in between; a
// retry re-fetches, so it is reported as in progress rather than missing.
func (s *Source) cachedTDoc(id string) (*tdocstore.Document, error) {
	rec, err := s.TDocs.Get(id)
	if err != nil {
		return nil, err
	}
	if rec == nil {
		return nil, &FetchInProgressError{SpecID: id}
	}
	return rec, nil
}

// TDocTOC returns a meeting document's record and section structure.
func (s *Source) TDocTOC(ctx context.Context, request, meeting string) (*tdocstore.Document, []db.Section, error) {
	rec, err := s.TDoc(ctx, request, meeting)
	if err != nil {
		return nil, nil, err
	}
	toc, err := s.TDocs.GetTOC(rec.ID)
	if err != nil {
		return nil, nil, err
	}
	if len(toc) == 0 {
		return nil, nil, &FetchInProgressError{SpecID: rec.ID}
	}
	return rec, toc, nil
}

// TDocSections returns a meeting document's text: the whole document when
// number is "*", else the named section (the preamble is ""), with its
// subsections when asked. A nil slice with a nil error means the document
// has no such section.
func (s *Source) TDocSections(ctx context.Context, request, meeting, number string, includeSubsections bool) (*tdocstore.Document, []db.Section, error) {
	rec, err := s.TDoc(ctx, request, meeting)
	if err != nil {
		return nil, nil, err
	}
	var sections []db.Section
	if number == "*" {
		sections, err = s.TDocs.AllSections(rec.ID)
	} else {
		sections, err = s.TDocs.GetSection(rec.ID, number, includeSubsections)
	}
	if err != nil {
		return nil, nil, err
	}
	if len(sections) == 0 {
		// Settle "no such section" against a whole-document read, which is
		// empty exactly when the document was evicted meanwhile.
		toc, err := s.TDocs.GetTOC(rec.ID)
		if err != nil {
			return nil, nil, err
		}
		if len(toc) == 0 {
			return nil, nil, &FetchInProgressError{SpecID: rec.ID}
		}
	}
	return rec, sections, nil
}

// TDocImage returns an image of a meeting document, or nil when it holds
// no image of that name.
func (s *Source) TDocImage(ctx context.Context, request, meeting, name string) (*db.Image, error) {
	rec, err := s.TDoc(ctx, request, meeting)
	if err != nil {
		return nil, err
	}
	return s.TDocs.GetImage(rec.ID, name)
}
