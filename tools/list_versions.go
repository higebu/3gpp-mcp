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
	// availabilityDatabase means the version is in the prebuilt database, with
	// full-text search, images and cross-references.
	availabilityDatabase = "database"
	// availabilityCached means the version was fetched on demand earlier and is
	// served from the local cache.
	availabilityCached = "cached"
	// availabilityArchive means the version exists upstream and will be
	// downloaded and converted the first time it is read.
	availabilityArchive = "archive"
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

func HandleListVersions(src *Source) func(ctx context.Context, req *mcp.CallToolRequest, input ListVersionsInput) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, req *mcp.CallToolRequest, input ListVersionsInput) (*mcp.CallToolResult, any, error) {
		if input.SpecID == "" {
			return errorResult("spec_id is required"), nil, nil
		}

		// The strongest availability wins, so collect from weakest to strongest.
		availability := map[string]string{}
		releases := map[string]string{}
		tokens := map[string]string{}

		archived, archiveErr := pipeline.ListVersions(ctx, src.Client, input.SpecID, src.UseCache)
		for _, sv := range archived {
			dotted, ok := specver.TokenToDotted(sv.Version)
			if !ok {
				dotted = sv.Version
			}
			availability[dotted] = availabilityArchive
			tokens[dotted] = sv.Version
			releases[dotted] = fmt.Sprintf("%d", sv.Release)
		}

		if src.Store != nil {
			cached, err := src.Store.CachedVersions(input.SpecID)
			if err != nil {
				return errorResult(fmt.Sprintf("failed to list cached versions: %v", err)), nil, nil
			}
			for _, v := range cached {
				availability[v] = availabilityCached
			}
		}

		specs, err := src.DB.ListSpecVersions(input.SpecID)
		if err != nil {
			return errorResult(fmt.Sprintf("failed to list versions: %v", err)), nil, nil
		}
		for _, s := range specs {
			availability[s.Version] = availabilityDatabase
			if s.Release != "" {
				releases[s.Version] = s.Release
			}
			if s.VersionToken != "" {
				tokens[s.Version] = s.VersionToken
			}
		}

		if len(availability) == 0 {
			if archiveErr != nil {
				return errorResult(fmt.Sprintf("no versions found for %s: %v", input.SpecID, archiveErr)), nil, nil
			}
			if parts, partsErr := src.DB.FindSpecIDsByFamily(input.SpecID); partsErr == nil && len(parts) > 0 {
				return errorResult(fmt.Sprintf("%s has multiple parts: %s — specify one", input.SpecID, strings.Join(parts, ", "))), nil, nil
			}
			return errorResult(fmt.Sprintf("no versions found for %s", input.SpecID)), nil, nil
		}

		out := ListVersionsOutput{SpecID: input.SpecID}
		for version, avail := range availability {
			release := releases[version]
			if release == "" {
				release = specver.ReleaseOf(version)
			}
			out.Versions = append(out.Versions, VersionInfo{
				Version:      version,
				Release:      release,
				Token:        tokens[version],
				Availability: avail,
			})
		}
		sort.Slice(out.Versions, func(i, j int) bool {
			return specver.Compare(out.Versions[i].Version, out.Versions[j].Version) > 0
		})

		data, err := json.MarshalIndent(out, "", "  ")
		if err != nil {
			return errorResult(fmt.Sprintf("failed to marshal: %v", err)), nil, nil
		}
		return textResult(string(data)), nil, nil
	}
}
