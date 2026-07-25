package tools

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/specver"
	"github.com/higebu/3gpp-mcp/versionstore"
)

// Source reads specification text from the prebuilt database, falling back to
// the on-demand version cache for versions the build did not import.
type Source struct {
	DB *db.DB
	// Store is nil when on-demand fetching is unavailable, either because the
	// cache file could not be opened or because it was turned off explicitly.
	Store  *versionstore.Store
	Client *http.Client
	// Budget bounds how long a tool call waits for a fetch before telling the
	// caller to retry. Zero means the versionstore default.
	Budget time.Duration
	// UseCache controls the archive directory listing cache.
	UseCache bool
}

// NewSource returns a Source that reads only the prebuilt database.
func NewSource(d *db.DB) *Source {
	return &Source{DB: d, UseCache: true}
}

// errFetchInProgress is returned when a fetch outlives the call's budget.
type errFetchInProgress struct {
	specID  string
	version string
}

func (e *errFetchInProgress) Error() string {
	return fmt.Sprintf("%s v%s is being downloaded and converted. This takes up to a few minutes for a large specification. Call the same tool again to get the content.", e.specID, e.version)
}

// errVersionUnavailable is returned when a requested version cannot be served,
// carrying the versions that do exist so the caller can pick one.
type errVersionUnavailable struct {
	specID    string
	version   string
	reason    string
	available []string
}

func (e *errVersionUnavailable) Error() string {
	msg := fmt.Sprintf("%s v%s is not available: %s", e.specID, e.version, e.reason)
	if len(e.available) > 0 {
		msg += "\nVersions in the archive: " + strings.Join(e.available, ", ")
	}
	return msg
}

// resolution says where a requested version was found.
type resolution struct {
	// version is the canonical dotted version that was resolved.
	version string
	// archived is true when the content comes from the on-demand cache rather
	// than the prebuilt database, meaning images and cross-references for it
	// were never imported.
	archived bool
}

// resolve locates a requested version, fetching it on demand when needed. An
// empty request resolves to whatever the prebuilt database holds.
func (s *Source) resolve(ctx context.Context, specID, request string) (resolution, error) {
	if request == "" {
		return resolution{}, nil
	}

	dotted, _, err := specver.Normalize(request)
	if err != nil {
		return resolution{}, &errVersionUnavailable{specID: specID, version: request, reason: err.Error()}
	}

	// A version the build already imported is served from the prebuilt
	// database, which also has its images and cross-references.
	if dotted != "" {
		if v, err := s.DB.ResolveVersion(specID, dotted); err == nil {
			return resolution{version: v}, nil
		} else if !errors.Is(err, db.ErrNoVersion) {
			return resolution{}, err
		}
	}

	if s.Store == nil {
		return resolution{}, &errVersionUnavailable{
			specID:  specID,
			version: request,
			reason:  "on-demand fetching is disabled, and this version is not in the database",
		}
	}

	// Skip the archive listing entirely when the exact version is already
	// cached; this is the common case for a repeated call.
	if dotted != "" {
		cached, err := s.Store.Has(specID, dotted)
		if err != nil {
			return resolution{}, err
		}
		if cached {
			return resolution{version: dotted, archived: true}, nil
		}
	}

	sv, available, err := pipeline.ResolveVersion(ctx, s.Client, specID, request, s.UseCache)
	if err != nil {
		return resolution{}, &errVersionUnavailable{
			specID:    specID,
			version:   request,
			reason:    err.Error(),
			available: versionLabels(available),
		}
	}

	canonical, ok := specver.TokenToDotted(sv.Version)
	if !ok {
		canonical = sv.Version
	}

	// A release selector such as "Rel-18" may well land on the version the
	// build imported, so check the database again now that it is resolved.
	if v, err := s.DB.ResolveVersion(specID, canonical); err == nil {
		return resolution{version: v}, nil
	} else if !errors.Is(err, db.ErrNoVersion) {
		return resolution{}, err
	}

	switch err := s.Store.Ensure(ctx, specID, canonical, sv, s.Budget); {
	case err == nil:
	case errors.Is(err, versionstore.ErrInProgress):
		return resolution{}, &errFetchInProgress{specID: specID, version: canonical}
	default:
		return resolution{}, &errVersionUnavailable{
			specID:  specID,
			version: canonical,
			reason:  err.Error(),
		}
	}
	return resolution{version: canonical, archived: true}, nil
}

// GetSection returns a section from whichever source holds the version.
func (s *Source) GetSection(ctx context.Context, specID, version, number string, includeSubsections bool) ([]db.Section, resolution, error) {
	res, err := s.resolve(ctx, specID, version)
	if err != nil {
		return nil, res, err
	}
	if res.archived {
		sections, err := s.Store.GetSection(specID, res.version, number, includeSubsections)
		return sections, res, err
	}
	sections, err := s.DB.GetSection(specID, res.version, number, includeSubsections)
	return sections, res, err
}

// GetTOC returns the section structure from whichever source holds the version.
func (s *Source) GetTOC(ctx context.Context, specID, version string) ([]db.Section, resolution, error) {
	res, err := s.resolve(ctx, specID, version)
	if err != nil {
		return nil, res, err
	}
	if res.archived {
		sections, err := s.Store.GetTOC(specID, res.version)
		return sections, res, err
	}
	sections, err := s.DB.GetTOC(specID, res.version)
	return sections, res, err
}

// versionLabels renders archive entries as dotted versions for error messages.
func versionLabels(available []*pipeline.SpecVersion) []string {
	labels := make([]string, 0, len(available))
	for _, sv := range available {
		if dotted, ok := specver.TokenToDotted(sv.Version); ok {
			labels = append(labels, dotted)
			continue
		}
		labels = append(labels, sv.Version)
	}
	return labels
}
