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
tools/               # MCP tool handlers (list_specs, get_toc, get_section, search, openapi, references, images)
web/                 # Web viewer UI (HTTP handlers, HTML templates, static assets)
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
make build-db RELEASE=19            # Download and import specs into DB
make import FILE=path/to/spec.docx  # Import single file
make import-dir SPECS_DIR=specs     # Import directory

# Download only (no import)
make download-specs RELEASE=19      # Download specs for a specific release
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

### MCP Tools

Nine tools are exposed via MCP:

| Tool | Description |
|------|-------------|
| `list_specs` | List available specifications |
| `get_toc` | Get table of contents for a spec |
| `get_section` | Get section content (paginated) |
| `search` | Full-text search across all specs |
| `list_openapi` | List OpenAPI definitions |
| `get_openapi` | Get OpenAPI definition (paginated) |
| `get_references` | Get cross-references for a section (incoming/outgoing) |
| `list_images` | List embedded images in a spec |
| `get_image` | Retrieve an embedded image (base64 or PNG) |

### Transport

- **stdio** (default): For Claude Code / IDE integration
- **HTTP**: With optional Bearer token auth (`THREEGPP_MCP_BEARER_TOKEN`) and optional web viewer (`--web`)

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
| `THREEGPP_MCP_BEARER_TOKEN` | Bearer token for HTTP transport auth |
| `THREEGPP_MAX_ZIP_SIZE_MB` | Max ZIP download size (default: 512 MB) |
| `THREEGPP_CACHE_TTL_HOURS` | Spec list cache TTL in hours |
| `XDG_CACHE_HOME` | Override cache directory (follows XDG Base Directory spec) |
