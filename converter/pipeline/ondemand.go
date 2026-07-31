package pipeline

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/higebu/3gpp-mcp/converter/docx"
	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/specver"
)

// LatestVersion asks ResolveVersion for the newest version of a spec.
const LatestVersion = "latest"

var (
	// ErrVersionNotFound reports that the archive directory for a spec holds no
	// file matching the requested version.
	ErrVersionNotFound = errors.New("version not found in archive")
	// ErrNoDocx reports that the version exists but ships no .docx file. Old
	// versions are commonly .doc only, which needs LibreOffice to convert.
	ErrNoDocx = errors.New("version has no .docx file")
)

// ListVersions returns every version of a spec present in the archive, newest
// first. Results come from the per-spec directory listing cache, so repeated
// calls within the cache TTL do not hit the network.
func ListVersions(ctx context.Context, client *http.Client, specID string, useCache bool) ([]*SpecVersion, error) {
	entries, err := FetchSpecZips(ctx, client, specID, useCache)
	if err != nil {
		return nil, fmt.Errorf("list archive versions for %s: %w", specID, err)
	}

	var versions []*SpecVersion
	for _, entry := range entries {
		if sv := ParseSpecEntry(entry); sv != nil {
			versions = append(versions, sv)
		}
	}
	sort.Slice(versions, func(i, j int) bool {
		return versionKey(versions[i]) > versionKey(versions[j])
	})
	return versions, nil
}

// ResolveVersion finds the archive file for a requested version of a spec.
// The request may be the dotted form ("18.6.0"), the archive token ("i60"),
// a bare release number ("18" or "Rel-18") selecting the newest version in
// that release, or "latest"/"" for the newest version overall.
//
// It returns the matching entry along with every version it saw, so callers can
// tell the user what does exist when nothing matches.
func ResolveVersion(ctx context.Context, client *http.Client, specID, version string, useCache bool) (*SpecVersion, []*SpecVersion, error) {
	available, err := ListVersions(ctx, client, specID, useCache)
	if err != nil {
		return nil, nil, err
	}
	if len(available) == 0 {
		return nil, nil, fmt.Errorf("%w: no versions listed for %s", ErrVersionNotFound, specID)
	}

	// available is sorted newest first, so the first match in each branch wins.
	if version == "" || version == LatestVersion {
		return available[0], available, nil
	}

	if release, ok := releaseRequest(version); ok {
		for _, sv := range available {
			if sv.Release == release {
				return sv, available, nil
			}
		}
		return nil, available, fmt.Errorf("%w: %s has no version in Rel-%d", ErrVersionNotFound, specID, release)
	}

	_, token, err := specver.Normalize(version)
	if err != nil {
		return nil, available, fmt.Errorf("invalid version %q: %w", version, err)
	}
	if token == "" {
		return nil, available, fmt.Errorf("%w: %s has no archive file for version %s", ErrVersionNotFound, specID, version)
	}
	for _, sv := range available {
		if sv.Version == token {
			return sv, available, nil
		}
	}
	return nil, available, fmt.Errorf("%w: %s has no version %s", ErrVersionNotFound, specID, version)
}

// releaseRequest recognises a bare release selector such as "18" or "Rel-18".
// A dotted version is never a release selector, so "18.6.0" does not match.
// Without a prefix, only one or two digits count: a three-digit string such
// as "920" is a base-36 archive token (9.2.0), not a request for Rel-920.
func releaseRequest(s string) (int, bool) {
	trimmed := s
	prefixed := false
	for _, prefix := range []string{"Rel-", "rel-", "REL-", "R", "r"} {
		if len(trimmed) > len(prefix) && trimmed[:len(prefix)] == prefix {
			trimmed = trimmed[len(prefix):]
			prefixed = true
			break
		}
	}
	if !prefixed && len(trimmed) > 2 {
		return 0, false
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// FetchVersion downloads one archive entry, converts its documents to Markdown
// and returns the resulting records without writing them anywhere.
//
// Unlike the build pipeline it deliberately skips images, OpenAPI YAML and
// cross-reference extraction. Images are fetched separately and lazily by
// FetchVersionImages when a tool actually asks for one; carrying them for every
// fetched version would bloat the cache with data no tool reads.
func FetchVersion(ctx context.Context, client *http.Client, sv *SpecVersion, timeout time.Duration) (db.Spec, []db.Section, error) {
	tmpDir, err := os.MkdirTemp("", "3gpp-ondemand-"+sv.SpecID+"-")
	if err != nil {
		return db.Spec{}, nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	result, err := DownloadAndExtract(ctx, client, sv, tmpDir, timeout)
	if err != nil {
		return db.Spec{}, nil, err
	}
	switch result.Status {
	case "OK":
	case "DOC_ONLY":
		return db.Spec{}, nil, fmt.Errorf("%w: %s v%s ships only legacy .doc files, which need LibreOffice to convert; build it into the database with `3gpp-mcp build --convert-doc`",
			ErrNoDocx, sv.SpecID, displayVersion(sv))
	default:
		return db.Spec{}, nil, fmt.Errorf("%w: %s v%s (%s)", ErrNoDocx, sv.SpecID, displayVersion(sv), result.Status)
	}

	// Cover files carry the title and are processed last so their metadata wins.
	sortCoverLast(result.DocxFiles)

	var spec db.Spec
	var sections []db.Section
	// A multi-file spec repeats section numbers across its parts only when a
	// later part supersedes an earlier one, so last write wins by number.
	index := map[string]int{}

	for _, docxPath := range result.DocxFiles {
		if err := ctx.Err(); err != nil {
			return db.Spec{}, nil, err
		}
		parsed, err := docx.ParseDocx(docxPath)
		if err != nil {
			log.Printf("  on-demand parse error %s: %v", filepath.Base(docxPath), err)
			continue
		}
		fileSpec, fileSections, _ := convertToDBRecords(parsed)
		if fileSpec.Title != "" {
			spec = fileSpec
		} else if spec.ID == "" {
			spec = fileSpec
		}
		for _, s := range fileSections {
			if i, ok := index[s.Number]; ok {
				sections[i] = s
				continue
			}
			index[s.Number] = len(sections)
			sections = append(sections, s)
		}
		// Free the file as soon as it is parsed, as the build pipeline does.
		if err := os.Remove(docxPath); err != nil {
			log.Printf("  warning: failed to remove %s: %v", filepath.Base(docxPath), err)
		}
	}

	if len(sections) == 0 {
		return db.Spec{}, nil, fmt.Errorf("%w: %s v%s produced no sections", ErrNoDocx, sv.SpecID, displayVersion(sv))
	}

	if spec.ID == "" {
		// The archive path carries the bare number; the database form has a
		// document-type prefix.
		spec.ID = "TS " + sv.SpecID
	}
	if spec.Title == "" {
		spec.Title = spec.ID
	}
	applyArchiveVersion(&spec, sections, sv)
	for i := range sections {
		sections[i].SpecID = spec.ID
	}
	return spec, sections, nil
}

// FetchVersionImages downloads one archive entry and returns only its embedded
// images. It backs the lazy image path: the archive offers no partial download,
// so retrieving even a single image of an archived version costs a full ZIP
// download, and callers are expected to cache everything returned here.
//
// When LibreOffice (soffice) is available, EMF/WMF images are converted to PNG
// before they are returned, renaming them from image1.emf to image1.png; the
// caller's lookups must tolerate that rename because section Markdown cached by
// FetchVersion still names the original file. Zero images is a success, not an
// error: many specifications simply ship no figures.
func FetchVersionImages(ctx context.Context, client *http.Client, sv *SpecVersion, timeout time.Duration) ([]db.Image, error) {
	tmpDir, err := os.MkdirTemp("", "3gpp-ondemand-img-"+sv.SpecID+"-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	result, err := DownloadAndExtract(ctx, client, sv, tmpDir, timeout)
	if err != nil {
		return nil, err
	}
	switch result.Status {
	case "OK":
	case "DOC_ONLY":
		return nil, fmt.Errorf("%w: %s v%s ships only legacy .doc files, which need LibreOffice to convert; build it into the database with `3gpp-mcp build --convert-doc`",
			ErrNoDocx, sv.SpecID, displayVersion(sv))
	default:
		return nil, fmt.Errorf("%w: %s v%s (%s)", ErrNoDocx, sv.SpecID, displayVersion(sv), result.Status)
	}

	sortCoverLast(result.DocxFiles)
	_, sofficeErr := exec.LookPath("soffice")
	convertImages := sofficeErr == nil

	var images []db.Image
	// Parts of a multi-file spec each number their images from image1, so the
	// same policy as the section merge applies: last write wins by name.
	index := map[string]int{}
	parsedFiles := 0

	for _, docxPath := range result.DocxFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parsed, err := docx.ParseDocx(docxPath)
		if err != nil {
			log.Printf("  on-demand parse error %s: %v", filepath.Base(docxPath), err)
			continue
		}
		parsedFiles++
		if convertImages {
			if n := docx.ConvertResultImages(ctx, parsed); n > 0 {
				log.Printf("  %s: converted %d images to PNG", sv.SpecID, n)
			}
		}
		_, _, fileImages := convertToDBRecords(parsed)
		for _, img := range fileImages {
			if i, ok := index[img.Name]; ok {
				images[i] = img
				continue
			}
			index[img.Name] = len(images)
			images = append(images, img)
		}
		if err := os.Remove(docxPath); err != nil {
			log.Printf("  warning: failed to remove %s: %v", filepath.Base(docxPath), err)
		}
	}
	// Zero images from parsed documents means the version has no figures, but
	// zero parsed documents means the download was unusable — succeeding would
	// cache "no figures" permanently for a version that may well have them.
	if parsedFiles == 0 {
		return nil, fmt.Errorf("%w: %s v%s produced no readable documents", ErrNoDocx, sv.SpecID, displayVersion(sv))
	}
	return images, nil
}

// displayVersion formats an archive entry's version for messages, preferring
// the dotted form and falling back to the raw token.
func displayVersion(sv *SpecVersion) string {
	if dotted, ok := specver.TokenToDotted(sv.Version); ok {
		return dotted
	}
	return sv.Version
}
