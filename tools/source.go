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

// FetchInProgressError is returned when a fetch outlives the call's budget.
type FetchInProgressError struct {
	SpecID  string
	Version string
	// Images marks a fetch of a version's images rather than its text.
	Images bool
}

func (e *FetchInProgressError) Error() string {
	subject := fmt.Sprintf("%s v%s is", e.SpecID, e.Version)
	if e.Images {
		subject = fmt.Sprintf("Images for %s v%s are", e.SpecID, e.Version)
	}
	return subject + " being downloaded and converted. This takes up to a few minutes for a large specification. Call the same tool again to get the content."
}

// VersionUnavailableError is returned when a requested version cannot be served,
// carrying the versions that do exist so the caller can pick one.
type VersionUnavailableError struct {
	SpecID    string
	Version   string
	Reason    string
	Available []string
}

func (e *VersionUnavailableError) Error() string {
	msg := fmt.Sprintf("%s v%s is not available: %s", e.SpecID, e.Version, e.Reason)
	if len(e.Available) > 0 {
		msg += "\nVersions in the archive: " + strings.Join(e.Available, ", ")
	}
	return msg
}

// Resolution says where a requested version was found.
type Resolution struct {
	// Version is the canonical dotted version that was resolved. It is empty
	// when the default database version was requested.
	Version string
	// Archived is true when the content comes from the on-demand cache rather
	// than the prebuilt database. Images for such a version are fetched lazily
	// on first use; cross-references were never imported.
	Archived bool
	// sv is the archive entry the version resolved to. It is nil on the cached
	// fast path, which skips the archive listing — callers that need the entry
	// must re-resolve it themselves.
	sv *pipeline.SpecVersion
}

// resolve locates a requested version, fetching it on demand when needed. An
// empty request resolves to whatever the prebuilt database holds.
func (s *Source) resolve(ctx context.Context, specID, request string) (Resolution, error) {
	if request == "" {
		return Resolution{}, nil
	}

	dotted, _, err := specver.Normalize(request)
	if err != nil {
		return Resolution{}, &VersionUnavailableError{SpecID: specID, Version: request, Reason: err.Error()}
	}

	// A version the build already imported is served from the prebuilt
	// database, which also has its images and cross-references.
	if dotted != "" {
		if v, err := s.DB.ResolveVersion(ctx, specID, dotted); err == nil {
			return Resolution{Version: v}, nil
		} else if !errors.Is(err, db.ErrNoVersion) {
			return Resolution{}, err
		}
	}

	if s.Store == nil {
		return Resolution{}, &VersionUnavailableError{
			SpecID:  specID,
			Version: request,
			Reason:  "on-demand fetching is disabled, and this version is not in the database",
		}
	}

	// Skip the archive listing entirely when the exact version is already
	// cached; this is the common case for a repeated call.
	if dotted != "" {
		cached, err := s.Store.Has(specID, dotted)
		if err != nil {
			return Resolution{}, err
		}
		if cached {
			return Resolution{Version: dotted, Archived: true}, nil
		}
	}

	sv, available, err := pipeline.ResolveVersion(ctx, s.Client, specID, request, s.UseCache)
	if err != nil {
		return Resolution{}, &VersionUnavailableError{
			SpecID:    specID,
			Version:   request,
			Reason:    err.Error(),
			Available: versionLabels(available),
		}
	}

	canonical, ok := specver.TokenToDotted(sv.Version)
	if !ok {
		canonical = sv.Version
	}

	// A release selector such as "Rel-18" may well land on the version the
	// build imported, so check the database again now that it is resolved.
	if v, err := s.DB.ResolveVersion(ctx, specID, canonical); err == nil {
		return Resolution{Version: v}, nil
	} else if !errors.Is(err, db.ErrNoVersion) {
		return Resolution{}, err
	}

	switch err := s.Store.Ensure(ctx, specID, canonical, sv, s.Budget); {
	case err == nil:
	case errors.Is(err, versionstore.ErrInProgress):
		return Resolution{}, &FetchInProgressError{SpecID: specID, Version: canonical}
	default:
		return Resolution{}, &VersionUnavailableError{
			SpecID:  specID,
			Version: canonical,
			Reason:  err.Error(),
		}
	}
	return Resolution{Version: canonical, Archived: true, sv: sv}, nil
}

// GetSection returns a section from whichever source holds the version.
func (s *Source) GetSection(ctx context.Context, specID, version, number string, includeSubsections bool) ([]db.Section, Resolution, error) {
	res, err := s.resolve(ctx, specID, version)
	if err != nil {
		return nil, res, err
	}
	if res.Archived {
		sections, err := s.Store.GetSection(specID, res.Version, number, includeSubsections)
		return sections, res, err
	}
	sections, err := s.DB.GetSection(ctx, specID, res.Version, number, includeSubsections)
	return sections, res, err
}

// AllSections returns every section of a version, content included, from
// whichever source holds it.
func (s *Source) AllSections(ctx context.Context, specID, version string) ([]db.Section, Resolution, error) {
	res, err := s.resolve(ctx, specID, version)
	if err != nil {
		return nil, res, err
	}
	if res.Archived {
		sections, err := s.Store.AllSections(specID, res.Version)
		return sections, res, err
	}
	sections, err := s.DB.AllSections(ctx, specID, res.Version)
	return sections, res, err
}

// GetTOC returns the section structure from whichever source holds the version.
func (s *Source) GetTOC(ctx context.Context, specID, version string) ([]db.Section, Resolution, error) {
	res, err := s.resolve(ctx, specID, version)
	if err != nil {
		return nil, res, err
	}
	if res.Archived {
		sections, err := s.Store.GetTOC(specID, res.Version)
		return sections, res, err
	}
	sections, err := s.DB.GetTOC(ctx, specID, res.Version)
	return sections, res, err
}

// GetImage returns an image from whichever source holds the version. For an
// archived version the version's images are downloaded on first use; a nil
// image with a nil error means the version holds no image of that name.
func (s *Source) GetImage(ctx context.Context, specID, version, name string) (*db.Image, Resolution, error) {
	res, err := s.resolve(ctx, specID, version)
	if err != nil {
		return nil, res, err
	}
	if res.Archived {
		if err := s.ensureImages(ctx, specID, res); err != nil {
			return nil, res, err
		}
		img, err := s.Store.GetImage(specID, res.Version, name)
		if err == nil && img == nil {
			if err := s.imagesStillCached(specID, res); err != nil {
				return nil, res, err
			}
		}
		return img, res, err
	}
	img, err := s.DB.GetImage(ctx, specID, res.Version, name)
	return img, res, err
}

// ListImages returns image metadata from whichever source holds the version.
// For an archived version the version's images are downloaded on first use.
func (s *Source) ListImages(ctx context.Context, specID, version string) ([]db.ImageInfo, Resolution, error) {
	res, err := s.resolve(ctx, specID, version)
	if err != nil {
		return nil, res, err
	}
	if res.Archived {
		if err := s.ensureImages(ctx, specID, res); err != nil {
			return nil, res, err
		}
		infos, err := s.Store.ListImages(specID, res.Version)
		if err == nil && len(infos) == 0 {
			if err := s.imagesStillCached(specID, res); err != nil {
				return nil, res, err
			}
		}
		return infos, res, err
	}
	infos, err := s.DB.ListImages(ctx, specID, res.Version)
	return infos, res, err
}

// imagesStillCached distinguishes "the version holds no such images" from "the
// version was evicted between ensureImages and the read": a concurrent fetch's
// eviction can drop it in that window, and answering a definitive not-found
// then would hide content a retry recovers.
func (s *Source) imagesStillCached(specID string, res Resolution) error {
	fetched, err := s.Store.HasImages(specID, res.Version)
	if err != nil {
		return err
	}
	if !fetched {
		return &FetchInProgressError{SpecID: specID, Version: res.Version, Images: true}
	}
	return nil
}

// ensureImages makes an archived version's images available in the cache. The
// Resolution's archive entry is nil when the version came from the cached fast
// path, so the entry may have to be resolved again before fetching.
func (s *Source) ensureImages(ctx context.Context, specID string, res Resolution) error {
	fetched, err := s.Store.HasImages(specID, res.Version)
	if err != nil {
		return err
	}
	if fetched {
		return nil
	}

	sv := res.sv
	if sv == nil {
		resolved, available, err := pipeline.ResolveVersion(ctx, s.Client, specID, res.Version, s.UseCache)
		if err != nil {
			return &VersionUnavailableError{
				SpecID:    specID,
				Version:   res.Version,
				Reason:    err.Error(),
				Available: versionLabels(available),
			}
		}
		sv = resolved
	}

	switch err := s.Store.EnsureImages(ctx, specID, res.Version, sv, s.Budget); {
	case err == nil:
		return nil
	case errors.Is(err, versionstore.ErrInProgress):
		return &FetchInProgressError{SpecID: specID, Version: res.Version, Images: true}
	default:
		return &VersionUnavailableError{
			SpecID:  specID,
			Version: res.Version,
			Reason:  err.Error(),
		}
	}
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
