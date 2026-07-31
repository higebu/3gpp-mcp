# 3gpp-mcp

[![Go Reference](https://pkg.go.dev/badge/github.com/higebu/3gpp-mcp.svg)](https://pkg.go.dev/github.com/higebu/3gpp-mcp)
[![Go Report Card](https://goreportcard.com/badge/github.com/higebu/3gpp-mcp)](https://goreportcard.com/report/github.com/higebu/3gpp-mcp)
[![CI](https://github.com/higebu/3gpp-mcp/actions/workflows/ci.yml/badge.svg)](https://github.com/higebu/3gpp-mcp/actions/workflows/ci.yml)
[![codecov](https://codecov.io/github/higebu/3gpp-mcp/graph/badge.svg?token=cgYIUH4xwz)](https://codecov.io/github/higebu/3gpp-mcp)
![GitHub Release](https://img.shields.io/github/v/release/higebu/3gpp-mcp)

An MCP (Model Context Protocol) server that makes 3GPP specifications accessible to LLMs.

## Background

3GPP specifications are essential references for mobile and telecommunications engineering, but they are difficult for LLMs to work with effectively:

- **Too many documents** - Thousands of specifications exist across multiple series, making it hard to find the right one.
- **Individual documents are too large** - Many specs are hundreds of pages long, far exceeding typical context windows.
- **Distributed as Word files** - Specs are published in `.docx` / `.doc` format and require conversion for text processing.
- **Heavy cross-referencing** - Specs frequently reference each other; reading a single document in isolation gives an incomplete picture.
- **Information packed in tables and figures** - Complex tables and flow diagrams carry critical details. This tool converts tables to Markdown and extracts embedded images for LLM viewing.
- **Version complexity** - The same specification exists across multiple 3GPP releases, and identifying the correct version matters.

This tool addresses these challenges by parsing the `.docx` files, structuring the content by section, and storing everything in a SQLite database with full-text search (FTS5). An MCP server then exposes tools for searching, browsing by section, and following cross-references — letting an LLM navigate the specifications the way an engineer would.

### Why not RAG?

A RAG (Retrieval-Augmented Generation) approach — chunking documents, generating embeddings, and performing vector similarity search — is a common solution for document Q&A. However, 3GPP specifications are highly structured technical documents where that approach has significant drawbacks:

- **Loss of structure** - RAG splits documents into flat chunks, discarding the section hierarchy that is essential for navigating specs.
- **No reference traversal** - Vector search cannot follow cross-references between specifications.
- **Noisy retrieval** - Similarity search may return loosely related chunks instead of the exact section needed.
- **Additional cost** - Embedding generation and vector database hosting add infrastructure and API costs.

This tool takes a structure-aware approach: it preserves the document hierarchy, enables precise section-level retrieval, supports full-text search with FTS5 syntax, and extracts OpenAPI definitions separately. All data is stored in a single SQLite file with no external dependencies.

## Getting Started

### Build a self-contained Docker image

The `Dockerfile` is multi-stage and builds the database for a release directly,
producing a self-contained image with the SQLite database (sections, OpenAPI
definitions, and embedded images) baked in. No pre-built database is needed in
the build context.

```bash
# Build an image with the latest version of every spec baked in (default)
docker build -t 3gpp-mcp:latest .

# ...or restrict the database to a single release
docker build --build-arg RELEASE=19 -t 3gpp-mcp:rel19 .

# stdio transport (Claude Code / IDE integration)
docker run --rm -i 3gpp-mcp:latest

# HTTP transport
docker run --rm -p 8080:8080 3gpp-mcp:latest serve --db /3gpp.db --transport http --addr :8080
```

`RELEASE` defaults to `latest`, which bakes in the latest version of every spec
across all releases. Set `--build-arg RELEASE=<n>` (e.g. `19`) to restrict the
database to a single release.

### Deploy to Cloud Run

To run on Cloud Run, see `cloudbuild.yaml` (build + push + deploy) and
`service.yaml` (Cloud Run service spec).

---

### 1. Install

```bash
go install github.com/higebu/3gpp-mcp/cmd/3gpp-mcp@latest
```

Requires Go 1.26+. LibreOffice is optional (needed for `.doc` to `.docx` conversion and EMF/WMF image to PNG conversion).

### 2. Build the database

Download and import specifications into the database. Temporary files are deleted after each spec is processed, minimizing disk usage.

```bash
# Download and import the latest version of every spec (all releases)
3gpp-mcp build --latest --db data/3gpp.db --convert-doc --convert-image

# ...or restrict to a single release
3gpp-mcp build --release 19 --db data/3gpp.db --convert-doc --convert-image
```

This will scrape the 3GPP FTP archive, download ZIP files, extract and parse `.docx` files, and insert structured content into the SQLite database.

### 3. Register with your MCP client

#### Claude Code

```bash
claude mcp add --scope user 3gpp -- 3gpp-mcp serve --db /path/to/data/3gpp.db
```

#### VS Code / GitHub Copilot

```bash
code --add-mcp '{"name":"3gpp","command":"3gpp-mcp","args":["serve","--db","/path/to/data/3gpp.db"]}'
```

#### GitHub Copilot CLI

Add to `~/.config/github-copilot/cli-mcp.json` (create if it doesn't exist):

```json
{
  "mcpServers": {
    "3gpp": {
      "command": "3gpp-mcp",
      "args": ["serve", "--db", "/path/to/data/3gpp.db"]
    }
  }
}
```

#### Codex CLI

```bash
codex mcp add --name 3gpp --command 3gpp-mcp --args serve --db /path/to/data/3gpp.db
```

#### Claude Desktop

Add to your configuration file (`~/Library/Application Support/Claude/claude_desktop_config.json` on macOS, `%APPDATA%\Claude\claude_desktop_config.json` on Windows):

```json
{
  "mcpServers": {
    "3gpp": {
      "command": "3gpp-mcp",
      "args": ["serve", "--db", "/path/to/data/3gpp.db"]
    }
  }
}
```

#### Streamable HTTP (remote deployment)

The HTTP transport is stateless: it supports MCP protocol version 2026-07-28 (no
initialize handshake, no `Mcp-Session-Id`) while older clients (2024-11-05
through 2025-11-25) keep working through per-request sessions.

Start the server with HTTP transport:

```bash
3gpp-mcp serve --db data/3gpp.db --transport http --addr :8080
```

Optionally enable Bearer token authentication:

```bash
export THREEGPP_MCP_BEARER_TOKEN=$(openssl rand -hex 32)
3gpp-mcp serve --db data/3gpp.db --transport http --addr :8080
```

Then configure your client to connect via HTTP:

```json
{
  "mcpServers": {
    "3gpp": {
      "url": "http://your-server:8080",
      "headers": {
        "Authorization": "Bearer YOUR_SECRET_TOKEN"
      }
    }
  }
}
```

When using `--web`, the MCP endpoint moves to `/mcp/`:

```json
{
  "mcpServers": {
    "3gpp": {
      "url": "http://your-server:8080/mcp/"
    }
  }
}
```

See [`examples/systemd/`](examples/systemd/) for production deployment with systemd.

### 4. Web viewer (optional)

Browse specifications in your browser by adding `--web` to the HTTP transport:

```bash
3gpp-mcp serve --db data/3gpp.db --transport http --addr :8080 --web
# MCP endpoint: http://localhost:8080/mcp/
# Web viewer:   http://localhost:8080/
```

Features: spec list with filtering, section viewer with TOC sidebar, full-text search, embedded images, cross-reference links, OpenAPI definitions with syntax highlighting, dark mode, responsive design.

## MCP Tools

### Browsing specifications

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `list_specs` | List available specifications | `series` (optional): filter by series number, e.g. `"23"` |
| `list_versions` | List the versions of a spec and where each can be read from | `spec_id` (required): e.g. `"TS 23.501"` |
| `get_toc` | Get table of contents of a spec | `spec_id` (required), `version` |
| `get_section` | Get section content (paginated) | `spec_id`, `section_number` (required), `version`, `include_subsections`, `offset`, `max_lines`, `max_chars` |

Every `get_toc`, `get_section` and `search` result names the specification and
version it came from, on every page of a paginated response.

### Past versions

The database holds one version per specification. To read another version, pass
`version` to `get_section` or `get_toc`:

```
list_versions  spec_id="TS 24.301"
get_section    spec_id="TS 24.301" section_number="5.5.1" version="15.8.0"
```

`version` accepts the dotted form (`15.8.0`), the archive token (`f80`), a
release selector (`Rel-15` or `15`, picking the newest version in that release),
or `latest`.

A version that is not in the database is downloaded from the 3GPP archive and
converted on first use. This takes up to a few minutes for a large
specification; if it is still running when the call's budget expires, the tool
says so and the same call repeated later returns the content. Results are kept
in a size-bounded cache (see [`serve`](#serve)) that is separate from the main
database, so:

- `search` covers only the version in the database — cross-release full-text
  search is not supported
- `get_image` and `get_references` only have data for the version in the
  database, so a section read from an archived version returns text alone and
  says so in its header
- section numbers move between releases; check `get_toc` for the older version
  before reading a section of it

### Searching

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `search` | Full-text search across all specs | `query` (required), `spec_ids` (optional), `limit` |

The `search` tool supports [SQLite FTS5](https://www.sqlite.org/fts5.html) query syntax:

- Phrase search: `"service based interface"`
- Boolean operators: `AMF AND UE`, `AMF OR SMF`, `NOT deprecated`
- Prefix matching: `handov*`
- Column filter: `title:authentication`, `content:handover`
- Proximity: `NEAR(AMF UE, 5)`

### Cross-references

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `get_references` | Get cross-references between specs and RFCs | `spec_id` (required), `section_number`, `direction` (`"outgoing"` or `"incoming"`), `include_subsections` |

### OpenAPI definitions

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `list_openapi` | List available OpenAPI definitions | `spec_id` (optional): filter by spec, e.g. `"TS 29.510"` |
| `get_openapi` | Get OpenAPI definition (paginated) | `spec_id`, `api_name` (required), `path`, `schema`, `offset`, `max_lines` |

### Embedded images

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `list_images` | List embedded images in a spec | `spec_id` (required) |
| `get_image` | Get an embedded image as base64 data viewable by LLMs | `spec_id`, `name` (required): image filename |

The `build` command extracts images from DOCX files and stores them in the database. PNG/JPEG/GIF/WebP images are directly viewable by LLMs. EMF/WMF images (most 3GPP figures use this format) are stored as raw data by default; use `--convert-image` to convert them to PNG via LibreOffice at build time.

```bash
# Convert EMF/WMF to PNG for LLM viewing (requires LibreOffice)
3gpp-mcp build --latest --db data/3gpp.db --convert-image
```

## Tips

### Separate databases per release

You can create separate databases for different 3GPP releases and register them as independent MCP servers. This is useful when you need to compare behavior across releases or work on a specific release.

```bash
# Build databases for different releases
3gpp-mcp build --release 18 --db data/3gpp-rel18.db --convert-doc --convert-image
3gpp-mcp build --release 19 --db data/3gpp-rel19.db --convert-doc --convert-image
```

Register them as separate MCP servers:

```bash
claude mcp add --scope user 3gpp-rel18 -- 3gpp-mcp serve --db /path/to/data/3gpp-rel18.db
claude mcp add --scope user 3gpp-rel19 -- 3gpp-mcp serve --db /path/to/data/3gpp-rel19.db
```

Or in a JSON configuration:

```json
{
  "mcpServers": {
    "3gpp-rel18": {
      "command": "3gpp-mcp",
      "args": ["serve", "--db", "/path/to/data/3gpp-rel18.db"]
    },
    "3gpp-rel19": {
      "command": "3gpp-mcp",
      "args": ["serve", "--db", "/path/to/data/3gpp-rel19.db"]
    }
  }
}
```

### Keeping specs up to date

Use the `update` command to check for newer versions of specs already in your database:

```bash
3gpp-mcp update --db data/3gpp.db --convert-doc --convert-image
```

## Command Reference

### `serve`

Start the MCP server.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | Path to SQLite database | `3gpp.db` |
| `--transport` | Transport type: `stdio` or `http` (env: `THREEGPP_MCP_TRANSPORT`; defaults to `http` when `PORT` is set) | `stdio` |
| `--addr` | HTTP listen address (env: `THREEGPP_MCP_ADDR`, or `PORT` interpreted as `:$PORT`) | `:8080` |
| `--bearer-token` | Bearer token for HTTP auth (env: `THREEGPP_MCP_BEARER_TOKEN`) | |
| `--web` | Enable web viewer alongside MCP server (HTTP transport only) | `false` |
| `--no-fetch` | Disable on-demand fetching of spec versions that are not in the database | `false` |
| `--version-cache` | Path to the on-demand version cache | `$XDG_CACHE_HOME/3gpp-mcp/versions.db` |
| `--version-cache-mb` | Size limit of the version cache in MB. `0` keeps only the most recently fetched version, `-1` is unlimited (env: `THREEGPP_VERSION_CACHE_MB`) | `1024` |
| `--fetch-budget` | How long a tool call waits for an on-demand fetch before asking the caller to retry (env: `THREEGPP_FETCH_BUDGET`) | `60s` |

The version cache is a separate SQLite file, so the main database stays
read-only and is never polluted with extra versions. When the cache cannot be
created — a read-only or ephemeral filesystem, such as the `scratch`-based
container image — the server logs a warning and runs with on-demand fetching
disabled; everything else keeps working. Cached versions are evicted
least-recently-used once the size limit is exceeded.

When `--web` is enabled with HTTP transport, the MCP endpoint is served at `/mcp/` and the web viewer at `/`.

HTTP transport also exposes `GET /health`, which returns `200 OK` without authentication. Use this path for platform health checks (Cloud Run, Sakura AppRun, Kubernetes liveness/readiness probes, etc.).

For container platforms like Cloud Run or Heroku that inject a `PORT` environment variable, the server automatically switches to HTTP transport and binds to `:$PORT`. Explicit flags or `THREEGPP_MCP_TRANSPORT` / `THREEGPP_MCP_ADDR` always take precedence.

### `build`

Download and import specifications into the database (recommended for initial setup). Alias: `pipeline`.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | Output SQLite database path | `3gpp.db` |
| `--release` | Process specs for a specific release (e.g. `19`) | |
| `--latest` | Process the latest version across all releases (the default selection) | `false` |
| `--spec` | Process a specific spec (e.g. `23.501`) | |
| `--series` | Filter by series, comma-separated (e.g. `23,29`) | |
| `--workers` | Number of parallel workers | NumCPU |
| `--convert-doc` | Convert `.doc` files to `.docx` using LibreOffice | `false` |
| `--convert-image` | Convert EMF/WMF images to PNG using LibreOffice | `false` |
| `--timeout` | HTTP timeout | `30s` |

### `download`

Download specifications without conversion.

| Flag | Description | Default |
|------|-------------|---------|
| `--release` | Download specs for a specific release | |
| `--latest` | Download the latest version across all releases (the default selection) | `false` |
| `--spec` | Download a specific spec (e.g. `23.501`) | |
| `--series` | Filter by series, comma-separated | |
| `--output-dir` | Output directory | `specs` |
| `--parallel` | Number of parallel downloads | NumCPU |
| `--convert-doc` | Convert `.doc` to `.docx` using LibreOffice | `false` |
| `--timeout` | HTTP timeout | `30s` |

### `import`

Import a single `.docx` file into the database. Alias: `convert`.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | Output SQLite database path | `3gpp.db` |
| `--convert-image` | Convert EMF/WMF images to PNG using LibreOffice | `false` |

Usage: `3gpp-mcp import --db data/3gpp.db path/to/spec.docx`

Flags must come before the file path; anything after it is treated as a positional argument, not an option.

### `import-dir`

Import all `.docx` files in a directory into the database. Alias: `convert-dir`.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | Output SQLite database path | `3gpp.db` |
| `--parse-workers` | Number of parallel parse workers | NumCPU |
| `--convert-doc` | Convert `.doc` to `.docx` using LibreOffice | `false` |
| `--convert-image` | Convert EMF/WMF images to PNG using LibreOffice | `false` |

Usage: `3gpp-mcp import-dir --db data/3gpp.db ./specs`

Flags must come before the directory path; anything after it is treated as a positional argument, not an option.

### `update`

Update specifications in the database to latest versions.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | SQLite database path | `3gpp.db` |
| `--workers` | Number of parallel workers | NumCPU |
| `--convert-doc` | Convert `.doc` to `.docx` using LibreOffice | `false` |
| `--convert-image` | Convert EMF/WMF images to PNG using LibreOffice | `false` |
| `--timeout` | HTTP timeout | `30s` |
