# AGENTS.md

## Project Overview

3gpp-mcp is an MCP (Model Context Protocol) server that downloads 3GPP specification documents (.docx) from the official FTP archive, converts them to Markdown, stores them in a SQLite database with full-text search (FTS5), and serves them via MCP tools.

## Tech Stack

- **Language**: Go 1.26+
- **MCP SDK**: `github.com/modelcontextprotocol/go-sdk`
- **Database**: SQLite via `modernc.org/sqlite` (pure Go, no CGO)
- **YAML**: `gopkg.in/yaml.v3`
- **Linter**: golangci-lint v2 (`.golangci.yml`)
- **CI**: GitHub Actions

## Project Structure

```
cmd/3gpp-mcp/       # CLI entry point (serve, build, download, import, import-dir, update)
converter/
  docx/              # DOCX → Markdown parser (zip/XML processing, EMF/WMF image support)
  pipeline/          # Streaming download + conversion pipeline with worker pool
db/                  # SQLite schema, queries, FTS5 full-text search
versionstore/        # On-demand cache of spec versions not in the prebuilt database
tools/               # MCP tool handlers (list_specs, list_versions, get_toc, get_section, search, openapi, references, images)
web/                 # Web viewer UI (HTTP handlers, HTML templates, static assets)
internal/specver/    # Version notation conversion (base-36 archive token <-> dotted)
internal/testutil/   # Shared test helpers (SetupTestDB, DownloadTestZip)
data/                # Database files (gitignored except .gitkeep)
examples/systemd/    # Deployment examples (service + timer)
```

## Development Commands

```bash
# Build
make build                          # Build to bin/3gpp-mcp
make install                        # Install to $GOPATH/bin

# Test
go test ./...                       # Run all tests
go test -race -coverprofile=coverage.out ./...  # With race detection + coverage

# Lint & Format
gofmt -l .                          # Check formatting
go vet ./...                        # Static analysis
golangci-lint run                   # Full lint (config: .golangci.yml)

# Build database (download + import)
make build-db                       # Download + import latest version of every spec
make build-db RELEASE=19            # ...or restrict to a single release
make import FILE=path/to/spec.docx  # Import single file
make import-dir SPECS_DIR=specs     # Import directory

# Download only (no import)
make download-specs                 # Download latest version of every spec
make download-specs RELEASE=19      # ...or restrict to a single release
make download-latest-specs          # Download latest version of each spec
make update-specs                   # Update DB to latest versions

# Web viewer
make web                            # Start HTTP server with web viewer at :8080

# Utilities
make db-info                        # Show database statistics
make clean                          # Remove bin/ and data/3gpp.db
```

## Architecture

### Streaming Pipeline

The core feature is a streaming pipeline (`converter/pipeline/`) that:

1. Scrapes the 3GPP FTP archive for spec file listings
2. Downloads ZIP archives containing .docx files
3. Parses DOCX → Markdown using pure Go XML processing
4. Inserts into SQLite with FTS5 indexing
5. Deletes temp files immediately to minimize disk usage

Uses a worker pool (`runtime.NumCPU()` workers by default) for parallel processing.

The pipeline caches the spec list to avoid repeated scraping. Cache TTL and location are controlled via environment variables.

### Versions

Specification versions are first-class in the schema: `specs` is keyed by
`(id, version)` and `sections` by `(spec_id, version, number)`. The canonical
version is the dotted form (`20.2.0`); the base-36 archive token (`k20`) is
kept alongside it in `specs.version_token` so archive files stay resolvable.
`internal/specver` converts between the two — each token digit is one base-36
digit of release, technical and editorial number.

A build imports **one version per spec**, and `InsertSpecWithSectionsAndImages`
drops any other version of the same spec to keep that invariant. Search depends
on it: the FTS index has no version column, so a second version of the same
spec would double every hit.

Reading a version the build did not import goes through `versionstore`, which
downloads and converts it via `pipeline.FetchVersion` and stores it in a
separate SQLite file (`$XDG_CACHE_HOME/3gpp-mcp/versions.db`). That file has no
FTS tables, so cached versions never reach search. Fetches are deduplicated per
`(spec, version)`, run on a context detached from the caller so a client timeout
does not discard minutes of work, and are evicted least-recently-used once the
size limit is exceeded. The section fetch skips images, OpenAPI YAML and
cross-reference extraction. Images are fetched lazily instead: the first
`get_image`/`list_images` call for an archived version downloads that version's
ZIP again via `pipeline.FetchVersionImages`, converts EMF/WMF figures to PNG
when LibreOffice (`soffice`) is available, and caches every image of the
version alongside its sections (image bytes count against the same LRU limit,
and a version's text and images are evicted as one unit). OpenAPI YAML and
cross-references remain prebuilt-only.

### MCP Tools

Ten tools are exposed via MCP:

| Tool | Description |
|------|-------------|
| `list_specs` | List available specifications |
| `list_versions` | List a spec's versions and where each can be read from |
| `get_toc` | Get table of contents for a spec (optionally a past version) |
| `get_section` | Get section content (paginated, optionally a past version) |
| `search` | Full-text search across all specs |
| `list_openapi` | List OpenAPI definitions |
| `get_openapi` | Get OpenAPI definition (paginated) |
| `get_references` | Get cross-references for a section (incoming/outgoing) |
| `list_images` | List embedded images in a spec (optionally a past version) |
| `get_image` | Retrieve an embedded image (base64 or PNG, optionally a past version) |

### Transport

- **stdio** (default): For Claude Code / IDE integration
- **HTTP**: With optional Bearer token auth (`THREEGPP_MCP_BEARER_TOKEN`) and optional web viewer (`--web`). Runs stateless (`StreamableHTTPOptions{Stateless: true}`), which is required to serve MCP protocol version 2026-07-28; older protocol versions are served through per-request sessions. The server has no server-initiated features, so statelessness loses nothing.

### Web Viewer

When running with `--transport http --web`, a browser-based UI is served alongside the MCP endpoint. It supports spec browsing, section viewing, full-text search, and OpenAPI rendering. Accessible via `make web` (default: `http://localhost:8080`).

## Coding Standards

- Follow standard Go conventions (`gofmt`, `go vet`)
- Error handling: return errors with context, use `fmt.Errorf("...: %w", err)` for wrapping
- `errcheck` exceptions are configured in `.golangci.yml` for `io.WriteString`, `fmt.Fprint*`, `defer`, and test files
- No CGO — the project uses pure Go SQLite (`modernc.org/sqlite`)
- Image conversion (`--convert-image`) exports EMF/WMF images to PNG and requires LibreOffice (`soffice`) at runtime

## Testing

- Tests are co-located with source files (`*_test.go`)
- Use `internal/testutil.SetupTestDB(t)` for database tests — creates a temp DB with schema and seed data, auto-cleaned via `t.Cleanup`
- Use `internal/testutil.DownloadTestZip(t, url)` for integration tests that fetch from 3GPP FTP — automatically skipped in `-short` mode
- HTTP mocks use `net/http/httptest`
- Always run with `-race` flag in CI

## CI Pipeline

GitHub Actions (`.github/workflows/ci.yml`) runs on push/PR to `main`:

1. `go build ./...`
2. `go vet ./...`
3. `gofmt -l .` (must produce no output)
4. `golangci-lint run`
5. `go test -race -coverprofile=coverage.out ./...`
6. Codecov upload

## Environment Variables

| Variable | Description |
|----------|-------------|
| `THREEGPP_MCP_TRANSPORT` | Transport type for `serve` (`stdio` or `http`); overridden by `--transport` |
| `THREEGPP_MCP_ADDR` | HTTP listen address for `serve` (e.g. `:8080`); overridden by `--addr` |
| `THREEGPP_MCP_BEARER_TOKEN` | Bearer token for HTTP transport auth |
| `PORT` | PaaS convention (Cloud Run / Heroku). When set, `serve` defaults to HTTP transport and binds to `:$PORT`. `THREEGPP_MCP_*` and CLI flags take precedence |
| `THREEGPP_MAX_ZIP_SIZE_MB` | Max ZIP download size (default: 512 MB) |
| `THREEGPP_CACHE_TTL_HOURS` | Spec list cache TTL in hours |
| `THREEGPP_VERSION_CACHE_MB` | Size limit of the on-demand version cache in MB (default: 1024; `0` keeps only the newest fetch, `-1` is unlimited) |
| `THREEGPP_FETCH_BUDGET` | How long a tool call waits for an on-demand fetch before asking the caller to retry (default: `60s`) |
| `XDG_CACHE_HOME` | Override cache directory (follows XDG Base Directory spec) |
