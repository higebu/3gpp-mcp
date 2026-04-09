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

### 1. Install

```bash
go install github.com/higebu/3gpp-mcp/cmd/3gpp-mcp@latest
```

Requires Go 1.26+. LibreOffice is optional (needed for `.doc` to `.docx` conversion and EMF/WMF image to PNG conversion).

### 2. Build the database

Download and import specifications into the database. Temporary files are deleted after each spec is processed, minimizing disk usage.

```bash
# Download and import all Release 19 specs
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
| `get_toc` | Get table of contents of a spec | `spec_id` (required): e.g. `"TS 23.501"` |
| `get_section` | Get section content (paginated) | `spec_id`, `section_number` (required), `include_subsections`, `offset`, `max_lines`, `max_chars` |

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
3gpp-mcp build --release 19 --db data/3gpp.db --convert-image
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
| `--transport` | Transport type: `stdio` or `http` | `stdio` |
| `--addr` | HTTP listen address | `:8080` |
| `--bearer-token` | Bearer token for HTTP auth (env: `THREEGPP_MCP_BEARER_TOKEN`) | |
| `--web` | Enable web viewer alongside MCP server (HTTP transport only) | `false` |

When `--web` is enabled with HTTP transport, the MCP endpoint is served at `/mcp/` and the web viewer at `/`.

### `build`

Download and import specifications into the database (recommended for initial setup). Alias: `pipeline`.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | Output SQLite database path | `3gpp.db` |
| `--release` | Process specs for a specific release (e.g. `19`) | |
| `--latest` | Process the latest version across all releases | `false` |
| `--all` | Process all versions | `false` |
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
| `--latest` | Download the latest version across all releases | `false` |
| `--all` | Download all versions | `false` |
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

### `import-dir`

Import all `.docx` files in a directory into the database. Alias: `convert-dir`.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | Output SQLite database path | `3gpp.db` |
| `--parse-workers` | Number of parallel parse workers | NumCPU |
| `--convert-image` | Convert EMF/WMF images to PNG using LibreOffice | `false` |

Usage: `3gpp-mcp import-dir --db data/3gpp.db ./specs`

### `update`

Update specifications in the database to latest versions.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | SQLite database path | `3gpp.db` |
| `--workers` | Number of parallel workers | NumCPU |
| `--convert-doc` | Convert `.doc` to `.docx` using LibreOffice | `false` |
| `--convert-image` | Convert EMF/WMF images to PNG using LibreOffice | `false` |
| `--timeout` | HTTP timeout | `30s` |
