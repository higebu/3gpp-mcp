package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/internal/specver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Availability values reported by list_versions.
const (
	// AvailabilityDatabase means the version is in the prebuilt database, with
	// full-text search, images and cross-references.
	AvailabilityDatabase = "database"
	// AvailabilityCached means the version was fetched on demand earlier and is
	// served from the local cache.
	AvailabilityCached = "cached"
	// AvailabilityArchive means the version exists upstream and will be
	// downloaded and converted the first time it is read.
	AvailabilityArchive = "archive"
)

type ListVersionsInput struct {
	SpecID string `json:"spec_id" jsonschema:"required,Specification ID (e.g. TS 23.501)"`
}

// VersionInfo describes one version of a specification.
type VersionInfo struct {
	Version      string `json:"version"`
	Release      string `json:"release,omitempty"`
	Token        string `json:"token,omitempty"`
	Availability string `json:"availability"`
}

// ListVersionsOutput is the JSON payload returned by list_versions.
type ListVersionsOutput struct {
	SpecID   string        `json:"spec_id"`
	Versions []VersionInfo `json:"versions"`
}

var ListVersionsTool = &mcp.Tool{
	Name: "list_versions",
	Description: `List the versions of a 3GPP specification, newest first.

Each entry reports where the version can be read from:
- database: in the prebuilt database, covered by search, images and cross-references
- cached:   fetched on demand earlier, available immediately
- archive:  exists upstream; reading it downloads and converts it first, which takes up to a few minutes for a large specification

Pass a version from this list to get_section or get_toc to read a past version.`,
}

// ListVersions merges the archive listing, the on-demand cache and the
// prebuilt database into one list of a spec's versions, newest first.
// archiveErr reports a failed archive listing; the merge still covers the
// cache and the database when it is set.
func (s *Source) ListVersions(ctx context.Context, specID string) (versions []VersionInfo, archiveErr, err error) {
	// The strongest availability wins, so collect from weakest to strongest.
	availability := map[string]string{}
	releases := map[string]string{}
	tokens := map[string]string{}

	archived, archiveErr := pipeline.ListVersions(ctx, s.Client, specID, s.UseCache)
	for _, sv := range archived {
		dotted, ok := specver.TokenToDotted(sv.Version)
		if !ok {
			dotted = sv.Version
		}
		availability[dotted] = AvailabilityArchive
		tokens[dotted] = sv.Version
		releases[dotted] = fmt.Sprintf("%d", sv.Release)
	}

	if s.Store != nil {
		cached, err := s.Store.CachedVersions(specID)
		if err != nil {
			return nil, archiveErr, fmt.Errorf("failed to list cached versions: %w", err)
		}
		for _, v := range cached {
			availability[v] = AvailabilityCached
		}
	}

	specs, err := s.DB.ListSpecVersions(ctx, specID)
	if err != nil {
		return nil, archiveErr, fmt.Errorf("failed to list versions: %w", err)
	}
	for _, spec := range specs {
		availability[spec.Version] = AvailabilityDatabase
		if spec.Release != "" {
			releases[spec.Version] = spec.Release
		}
		if spec.VersionToken != "" {
			tokens[spec.Version] = spec.VersionToken
		}
	}

	for version, avail := range availability {
		release := releases[version]
		if release == "" {
			release = specver.ReleaseOf(version)
		}
		versions = append(versions, VersionInfo{
			Version:      version,
			Release:      release,
			Token:        tokens[version],
			Availability: avail,
		})
	}
	sort.Slice(versions, func(i, j int) bool {
		return specver.Compare(versions[i].Version, versions[j].Version) > 0
	})
	return versions, archiveErr, nil
}

func HandleListVersions(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input ListVersionsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListVersionsInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}

		versions, archiveErr, err := src.ListVersions(ctx, input.SpecID)
		if err != nil {
			return errorResult(err.Error()), nil, nil
		}

		if len(versions) == 0 {
			if archiveErr != nil {
				return errorResult(fmt.Sprintf("no versions found for %s: %v", input.SpecID, archiveErr)), nil, nil
			}
			if parts, partsErr := src.DB.FindSpecIDsByFamily(ctx, input.SpecID); partsErr == nil && len(parts) > 0 {
				return errorResult(fmt.Sprintf("%s has multiple parts: %s — specify one", input.SpecID, strings.Join(parts, ", "))), nil, nil
			}
			return errorResult(fmt.Sprintf("no versions found for %s", input.SpecID)), nil, nil
		}

		out := ListVersionsOutput{SpecID: input.SpecID, Versions: versions}
		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal: %v", err)), nil, nil
		}

		// The archive listing may have failed while the cache and/or database
		// still produced versions; the list returned is then incomplete, and
		// silently returning it as if it were complete would hide that. The
		// warning travels as its own content item so the JSON payload stays
		// parseable.
		if archiveErr != nil {
			return listVersionsResult(string(data),
				fmt.Sprintf("[Warning: failed to list archive versions for %s, so this list may be incomplete: %v]", input.SpecID, archiveErr)), nil, nil
		}
		return textResult(string(data)), nil, nil
	}
}

// listVersionsResult builds a result whose first content item is the JSON
// payload; a non-empty warning is appended as a separate item so it never
// corrupts the JSON.
func listVersionsResult(payload, warning string) *mcp.CallToolResult {
	res := textResult(payload)
	if warning != "" {
		res.Content = append(res.Content, &mcp.TextContent{Text: warning})
	}
	return res
}
