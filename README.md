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

Embedding-based RAG is a common way to improve accuracy on document Q&A, and RAG systems specialized for 3GPP documents exist ([Telco-RAG](https://arxiv.org/abs/2404.15939), [TelcoAI](https://arxiv.org/abs/2601.16984)). This tool takes a simpler approach: instead of building a retrieval pipeline in front of the model, it gives the model search and navigation tools and lets it explore the specifications the way an engineer would — full-text search, then following the section hierarchy and cross-references. Since retrieval is plain FTS5 search over structured sections, there is no embedding model or vector database to run, and everything lives in a single SQLite file.

Measured on TeleQnA, this lifts accuracy on 3GPP standards questions by about 11 percentage points across three model families — see [BENCHMARK.md](BENCHMARK.md).

## Getting Started

### 1. Install

```bash
# Homebrew
brew install higebu/tap/3gpp-mcp

# ...or with Go 1.26+
go install github.com/higebu/3gpp-mcp/cmd/3gpp-mcp@latest
```

Prebuilt binaries are also available on the [releases page](https://github.com/higebu/3gpp-mcp/releases). LibreOffice is optional (needed for `.doc` to `.docx` conversion and EMF/WMF image to PNG conversion).

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

### 4. Web viewer (optional)

Browse specifications in your browser by adding `--web` to the HTTP transport:

```bash
3gpp-mcp serve --db data/3gpp.db --transport http --addr :8080 --web
# MCP endpoint: http://localhost:8080/mcp/
# Web viewer:   http://localhost:8080/
```

Features: spec list with filtering, section viewer with TOC sidebar, full-text search with pagination, past-version browsing (versions are listed per spec and downloaded on demand, like the MCP tools), version comparison (structural summary and per-section diffs), embedded images, cross-reference links, OpenAPI definitions with syntax highlighting, KaTeX rendering of the [LaTeX formulas](#formulas) the converter emits, dark mode, responsive design.

Code blocks are syntax-highlighted per notation — ASN.1, Diameter, SIP/RTSP,
SDP and XML (see [Code blocks](#code-blocks)). The color theme — Catppuccin
(default), GitHub, Monokai or Xcode/Dracula — is selectable from the settings
popover in the navbar; each has a light and a dark variant, picked by the
site's light/dark mode.

## Deployment

### Streamable HTTP

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

### Docker

The `Dockerfile` is multi-stage and builds the database for a release directly,
producing a self-contained image with the SQLite database (sections, OpenAPI
definitions, and embedded images) baked in. No pre-built database is needed in
the build context.

```bash
# Build an image with the latest version of every spec baked in (default)
docker build -t 3gpp-mcp:latest .

# ...or restrict the database to a single release
docker build --build-arg RELEASE=19 -t 3gpp-mcp:rel19 .

# ...or cap the newest release, keeping specs that have no version in it
docker build --build-arg MAX_RELEASE=19 -t 3gpp-mcp:max-rel19 .

# stdio transport (Claude Code / IDE integration)
docker run --rm -i 3gpp-mcp:latest

# HTTP transport
docker run --rm -p 8080:8080 3gpp-mcp:latest serve --db /3gpp.db --transport http --addr :8080
```

`RELEASE` defaults to `latest`, which bakes in the latest version of every spec
across all releases. Set `--build-arg RELEASE=<n>` (e.g. `19`) to restrict the
database to a single release, or `--build-arg MAX_RELEASE=<n>` to cap the newest
release without dropping specs that have no version in it. The two cannot be
combined.

### Cloud Run

To run on Cloud Run, see `cloudbuild.yaml` (build + push + deploy) and
`service.yaml` (Cloud Run service spec).

## MCP Tools

Every tool below also has a CLI twin (`list_specs` → `3gpp-mcp list-specs`, and
so on) for shell use and scripting — see the
[query commands](#query-commands) in the Command Reference.

### Browsing specifications

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `list_specs` | List available specifications (paginated) | `series` (optional): filter by series number, e.g. `"23"`; `query` (optional): spec ID prefix, e.g. `"38.21"`; `limit`, `offset` |
| `list_versions` | List the versions of a spec and where each can be read from | `spec_id` (required): e.g. `"TS 23.501"` |
| `get_toc` | Get table of contents of a spec | `spec_id` (required), `version` |
| `get_section` | Get section content (paginated) | `spec_id`, `section_number` (required), `version`, `include_subsections`, `offset`, `max_lines`, `max_chars` |
| `compare_versions` | Compare two versions of a spec: structural summary, or a section text diff | `spec_id`, `old_version` (required), `new_version`, `section_number`, `include_subsections`, `context_lines`, `offset`, `max_lines`, `max_chars` |

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
or `latest`. Release selectors and `latest` are resolved against the 3GPP
archive, so they require on-demand fetching (they do not work under
`--no-fetch`).

To see what changed between two versions, use `compare_versions`. Without
`section_number` it summarizes which sections were added, removed, renumbered,
retitled or changed; with `section_number` it returns a line-level unified diff
of that section's text:

```
compare_versions  spec_id="TS 23.501" old_version="Rel-17"
compare_versions  spec_id="TS 23.501" old_version="17.9.0" section_number="5.15.2"
```

`old_version` and `new_version` accept the same forms as `version` above;
`new_version` defaults to the version in the database. Comparing two archived
versions downloads both on first use.

A version that is not in the database is downloaded from the 3GPP archive and
converted on first use. This takes up to a few minutes for a large
specification; if it is still running when the call's budget expires, the tool
says so and the same call repeated later returns the content. Results are kept
in a size-bounded cache (see [`serve`](#serve)) that is separate from the main
database, so:

- `search` covers only the version in the database — cross-release full-text
  search is not supported
- `get_references` only has data for the version in the database, and a section
  read from an archived version says so in its header
- `get_image` and `list_images` accept a `version` too: an archived version's
  images are downloaded on their own first use (one extra archive download per
  version, with the same retry behavior), and EMF/WMF figures are converted to
  PNG when LibreOffice is installed on the server
- section numbers move between releases; check `get_toc` for the older version
  before reading a section of it

### Searching

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `search` | Full-text search across all specs | `query` (required), `spec_ids` (optional), `limit`, `offset` |

The `search` tool supports [SQLite FTS5](https://www.sqlite.org/fts5.html) query syntax:

- Phrase search: `"service based interface"`
- Boolean operators: `AMF AND UE`, `AMF OR SMF`, `NOT deprecated`
- Exclusion after a positive term: `handover -conditional`
- Prefix matching: `handov*`
- Column filter: `title:authentication`, `content:handover`
- Proximity: `NEAR(AMF UE, 5)`

Terms containing hyphens or dots (`IMS-AKA`, `38.101`) are quoted automatically,
so they need no manual escaping.

Results come as `{results, total_count, limit, offset}`; use `limit` (default
10, max 200) and `offset` to page through everything beyond the first page.
Section-title matches rank above body matches, and the snippet is taken from
whichever column matched best. The index uses porter stemming, so inflected
English forms match each other (`handover` finds `handovers`).

### Cross-references

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `get_references` | Get cross-references between specs and RFCs | `spec_id` (required), `section_number` (required for `"outgoing"`), `direction` (`"outgoing"` or `"incoming"`), `include_subsections`, `offset` |

Responses are capped at 500 references per page; the total count is reported
and `offset` pages past the cap.

### OpenAPI definitions

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `list_openapi` | List available OpenAPI definitions | `spec_id` (optional): filter by spec, e.g. `"TS 29.510"` |
| `get_openapi` | Get OpenAPI definition (paginated) | `spec_id`, `api_name` (required), `path`, `schema`, `offset`, `max_lines` |
| `search_openapi` | Full-text search across OpenAPI definitions | `query` (required), `spec_ids`, `api_name`, `kind` (`"schema"` or `"operation"`), `include_body`, `limit`, `offset` |

`search_openapi` uses its own FTS5 index, separate from the one `search` uses:
`search` covers specification clause text and never returns OpenAPI content,
`search_openapi` covers OpenAPI content only. One hit is one definition rather
than one document — a schema from `components.schemas`, or one HTTP method of
one path (named like `PUT /nf-instances/{nfInstanceID}`) — so you can find a
data type or an endpoint without knowing which API document defines it, then
read it in full with `get_openapi`. A query that is a single bare term ranks a
definition of exactly that name first, so `NFProfile` returns the `NFProfile`
schema ahead of the schemas that only reference it.

A schema's indexed text carries one level of `$ref` expansion — through `items`
and `additionalProperties` as well as directly, which is how the 5G SBI
definitions state most of their relationships — so the fields of a referenced
type are searchable from the schema that uses it; a type two hops away is not
in that text. Unlike `search`, this index applies no stemming
— identifiers are matched as written — and `-`, `.` and `_` split tokens, so
`Nnrf_NFManagement` is also found by `NFManagement` and `/nf-instances` by
`instances`. camelCase is not split.

The index is built at the end of `build` and `update`. `import` and `import-dir`
leave it alone: the YAML files ship in the archive zip, so importing a `.docx`
cannot change what there is to index. A database built before this tool existed
has no index; add it in place with
[`build-openapi-index`](#build-openapi-index).

### Embedded images

| Tool | Description | Key Parameters |
|------|-------------|----------------|
| `list_images` | List embedded images in a spec | `spec_id` (required), `version` (optional) |
| `get_image` | Get an embedded image as base64 data viewable by LLMs | `spec_id`, `name` (required): image filename, `version` (optional) |

The `build` command extracts images from DOCX files and stores them in the database. PNG/JPEG/GIF/WebP images are directly viewable by LLMs. EMF/WMF images (most 3GPP figures use this format) are stored as raw data by default; use `--convert-image` to convert them to PNG via LibreOffice at build time. For archived versions read via `version`, images are fetched into the on-demand cache the first time one is requested, and EMF/WMF figures are converted to PNG when LibreOffice is available at runtime.

```bash
# Convert EMF/WMF to PNG for LLM viewing (requires LibreOffice)
3gpp-mcp build --latest --db data/3gpp.db --convert-image
```

Figures are referenced from the section text in a single notation, whatever the
image format: `![Figure](image://NAME?w=&h=)` in body text and
`<img src="image://NAME?w=&h=" ...>` inside table cells. Pass that `NAME` to
`get_image`; both the original filename (`image3.emf`) and the converted one
(`image3.png`) resolve.

### Code blocks

Section text carries tagged code fences, so both LLMs and the web viewer can
tell the notations apart:

| Fence | Content |
|-------|---------|
| ` ```asn1 ` | ASN.1 modules between the `-- ASN1START` / `-- ASN1STOP` markers |
| ` ```diameter ` | Diameter command and grouped-AVP definitions (RFC 6733 CCF) |
| ` ```xml ` | XML schemas, XML body examples and DTDs |
| ` ```sip ` | SIP/RTSP message examples |
| ` ```sdp ` | Standalone SDP session descriptions |
| ` ```latex ` | Standalone equations converted from Word OMML |
| ` ``` ` | Anything else the source document styles as code |

Diameter, XML, SIP and SDP blocks carry no code style in the source `.docx`, so
they are recognized by content during conversion. The web viewer highlights all
of them. The `latex` fence is not content-detected: the converter emits it for
paragraphs whose only content is a formula.

### Formulas

Word formulas (OMML) are converted to LaTeX in three notations, so a formula is
readable whether it stands alone or sits in a sentence:

| Notation | Where |
|----------|-------|
| ` ```latex ` fence | A paragraph whose only content is an equation. Its equation number is kept as `\tag{7.3-1}`, which renders as a right-aligned `(7.3-1)`. |
| `$$...$$` | Display equations that cannot be a fenced block — inside a table cell or a list item. |
| `$...$` | A formula inside a sentence. |

The LaTeX is stored as-is, so MCP clients and the CLI see the same text the web
viewer renders with [KaTeX](https://katex.org/). It never contains `<` or `>`
(they become `\lt`, `\gt`, `\langle`, `\rangle`), which is what lets table cells
carry a formula unescaped and keep `&` as a matrix column separator.

Tagged fences and the unified image notation are produced when a spec is
converted, so a database built with an older version of this tool keeps its
old output until rebuilt with `3gpp-mcp build` (or `make build-db`).

## Tips

### Separate databases per release

For spot comparisons across releases, `compare_versions` and the `version` parameter need no extra setup. Building a separate database per release still pays off when you work against one release continuously: full-text `search`, `get_references` and OpenAPI definitions only cover the version baked into the database, so a release-specific database gives you all three for that release, with no on-demand downloads.

```bash
# Build databases for different releases
3gpp-mcp build --release 18 --db data/3gpp-rel18.db --convert-doc --convert-image
3gpp-mcp build --release 19 --db data/3gpp-rel19.db --convert-doc --convert-image
```

`--release` keeps only specs that have a version in that exact release, so a
spec frozen in an earlier release (TS 34.108, for example) is missing from the
database entirely. To pin a release without losing those specs, cap the
selection instead — every spec is taken at its newest version at or below the
cap:

```bash
# Everything as of Release 19: specs with no Rel-19 version fall back to their
# newest older version rather than dropping out.
3gpp-mcp build --max-release 19 --db data/3gpp-rel19.db --convert-doc --convert-image

# Keep the cap when refreshing the database later.
3gpp-mcp update --max-release 19 --db data/3gpp-rel19.db --convert-doc
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
| `--version-cache` | Path to the on-demand version cache | `$XDG_CACHE_HOME/3gpp-mcp/versions.db` (`~/.cache/3gpp-mcp/versions.db` when unset) |
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
| `--max-release` | Cap the selection at a release (e.g. `19`): take each spec at its newest version at or below it | |
| `--latest` | Select every spec at its latest version (use when no other selector is given) | `false` |
| `--spec` | Process a specific spec (e.g. `23.501`) | |
| `--series` | Filter by series, comma-separated (e.g. `23,29`) | |
| `--workers` | Number of parallel workers | NumCPU |
| `--convert-doc` | Convert `.doc` files to `.docx` using LibreOffice | `false` |
| `--convert-image` | Convert EMF/WMF images to PNG using LibreOffice | `false` |
| `--spec-list` | Read the spec list from a file instead of scraping the archive (a selector is still required) | |
| `--no-cache` | Disable the spec list cache | `false` |
| `--scrape-workers` | Concurrency for scraping spec listings (`0` = auto) | `0` |
| `--timeout` | HTTP timeout | `30s` |

One of `--release`, `--max-release`, `--latest`, `--series` or `--spec` must be
given, `--spec-list` included: the file supplies the candidate entries and the
selector filters them.

`--release` and `--max-release` differ in what happens to a spec that has no
version in the named release: `--release 19` drops it, `--max-release 19` keeps
it at its newest version below the cap. They cannot be combined.

### `download`

Download specifications without conversion.

| Flag | Description | Default |
|------|-------------|---------|
| `--release` | Download specs for a specific release | |
| `--max-release` | Cap the selection at a release (e.g. `19`): take each spec at its newest version at or below it | |
| `--latest` | Select every spec at its latest version (use when no other selector is given) | `false` |
| `--spec` | Download a specific spec (e.g. `23.501`) | |
| `--series` | Filter by series, comma-separated | |
| `--output-dir` | Output directory | `specs` |
| `--parallel` | Number of parallel downloads | NumCPU |
| `--convert-doc` | Convert `.doc` to `.docx` using LibreOffice | `false` |
| `--spec-list` | Read the spec list from a file instead of scraping the archive (a selector is still required) | |
| `--no-cache` | Disable the spec list cache | `false` |
| `--scrape-workers` | Concurrency for scraping spec listings (`0` = auto) | `0` |
| `--timeout` | HTTP timeout | `30s` |

Like `build`, this command requires one of `--release`, `--max-release`,
`--latest`, `--series` or `--spec`, even with `--spec-list`.

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
| `--max-release` | Cap updates at a release (e.g. `19`): update each spec to its newest version at or below it | |
| `--workers` | Number of parallel workers | NumCPU |
| `--convert-doc` | Convert `.doc` to `.docx` using LibreOffice | `false` |
| `--convert-image` | Convert EMF/WMF images to PNG using LibreOffice | `false` |
| `--spec-list` | Use a spec list file instead of scraping the archive | |
| `--no-cache` | Disable the spec list cache | `false` |
| `--scrape-workers` | Concurrency for scraping spec listings (`0` = auto) | `0` |
| `--timeout` | HTTP timeout | `30s` |

The cap is not stored in the database, so a database built with
`--max-release 19` needs the same flag here — otherwise the update lifts every
spec to the newest release on the archive. With a cap the update moves a spec
in either direction, so it also brings an already-built uncapped database down
to the cap; a spec whose every version is above the cap is removed, since no
version of it belongs in a capped database. A spec missing from the archive
listing is left untouched, as a failed listing looks the same as a withdrawn
spec.

### Query commands

The query commands mirror the MCP read tools 1:1, so the database can be
inspected and scripted from a shell without an MCP client:

```bash
3gpp-mcp search --db data/3gpp.db --limit 3 "AMF AND authentication" | jq '.results[].section_number'
3gpp-mcp get-section --db data/3gpp.db "TS 23.501" 5.15.2 | less
```

Conventions shared by all of them:

- Flags must come before positional arguments.
- JSON results print to stdout indented and unpaginated — pipe to `jq`,
  `head` or `less`. Warnings and progress notes go to stderr, so stdout stays
  parseable.
- Commands that accept `--version` (and `compare-versions`) take the same
  version forms as the MCP tools (`15.8.0`, `f80`, `Rel-15`, `latest`) and
  wait for an on-demand download to finish instead of asking you to retry;
  interrupt with Ctrl-C. They share `serve`'s fetch flags: `--no-fetch`,
  `--version-cache`, `--version-cache-mb`, `--fetch-budget`. Queries that name
  no version never create the version cache (`list-versions` reads an existing
  cache to report `cached` availability, but will not create one).
- Every command takes `--db` (default `3gpp.db`).

### `list-specs`

List specifications in the database as JSON.

| Flag | Description | Default |
|------|-------------|---------|
| `--series` | Filter by series number (e.g. `23`) | |
| `--query` | Filter specs whose ID starts with this text (e.g. `38.21`) | |
| `--limit` | Maximum number of results | `20` |
| `--offset` | Number of results to skip | `0` |

### `list-versions`

List the versions of a specification as JSON, newest first, with each
version's availability (`database`, `cached` or `archive`). An unreachable
archive is reported as a warning on stderr and the listing continues with the
cache and the database.

Usage: `3gpp-mcp list-versions [flags] <spec-id>`

### `get-toc`

Print a specification's table of contents as Markdown.

Usage: `3gpp-mcp get-toc [--version <v>] [fetch flags] <spec-id>`

### `get-section`

Print a section's Markdown content, prefixed with the same provenance header
as the MCP tool, without pagination.

Usage: `3gpp-mcp get-section [flags] <spec-id> <section-number>`

| Flag | Description | Default |
|------|-------------|---------|
| `--version` | Specification version to read | the database version |
| `--subsections` | Include all subsections | `false` |

### `compare-versions`

Compare two versions of a specification: a structural summary, or a unified
diff of one section with `--section`.

Usage: `3gpp-mcp compare-versions [flags] --old <version> <spec-id>`

| Flag | Description | Default |
|------|-------------|---------|
| `--old` | Older version to compare from (required) | |
| `--new` | Newer version to compare to | the database version |
| `--section` | Compare only this section's text as a unified diff | |
| `--subsections` | With `--section`: include subsections in the diff | `false` |
| `--context` | Unchanged lines shown around each change in a section diff (`0` shows none) | `3` |

### `search`

Full-text search; results print as JSON. The query supports the same FTS5
syntax as the MCP tool, and everything after the flags is joined into one
query, so multi-term queries need no quoting: `3gpp-mcp search AMF AND authentication`.

Usage: `3gpp-mcp search [flags] <query>`

| Flag | Description | Default |
|------|-------------|---------|
| `--specs` | Limit search to specific specs, comma-separated (e.g. `"TS 23.501,TS 23.502"`) | |
| `--limit` | Maximum number of results per page (max 200) | `10` |
| `--offset` | Number of results to skip | `0` |

### `list-openapi`

List OpenAPI definitions as JSON.

| Flag | Description | Default |
|------|-------------|---------|
| `--spec` | Filter by specification ID (e.g. `TS 29.510`) | |

### `get-openapi`

Print an OpenAPI definition as YAML, without pagination.

Usage: `3gpp-mcp get-openapi [flags] <spec-id> <api-name>`

| Flag | Description | Default |
|------|-------------|---------|
| `--path` | Filter by API path (e.g. `/nf-instances`) | |
| `--schema` | Filter by schema name (e.g. `NFProfile`) | |

### `search-openapi`

Full-text search across OpenAPI definitions; results print as JSON. One hit is
one schema or one operation. As with `search`, everything after the flags is
joined into one query: `3gpp-mcp search-openapi NFProfile AND heartbeat`.

Usage: `3gpp-mcp search-openapi [flags] <query>`

| Flag | Description | Default |
|------|-------------|---------|
| `--specs` | Limit search to specific specs, comma-separated (e.g. `"TS 29.510,TS 29.518"`) | |
| `--api` | Limit search to a single API document (e.g. `Nnrf_NFManagement`) | |
| `--kind` | Limit search to `schema` or `operation` | both |
| `--body` | Include the full text of each matching definition | `false` |
| `--limit` | Maximum number of results per page (max 200) | `10` |
| `--offset` | Number of results to skip | `0` |

### `build-openapi-index`

Rebuild the OpenAPI search index of an existing database. `build` and `update`
do this themselves, so this command is for adding the index to a database built
before `search_openapi` existed — the server opens the database read-only and
cannot create it on the fly.

| Flag | Description | Default |
|------|-------------|---------|
| `--db` | SQLite database path | `3gpp.db` |

### `get-references`

Print cross-references as JSON. Unlike the MCP tool, the full result is
printed with no 500-row cap — that cap protects an LLM context window, which
a shell pipeline does not have.

Usage: `3gpp-mcp get-references [flags] <spec-id> [section-number]`

| Flag | Description | Default |
|------|-------------|---------|
| `--direction` | `outgoing`: references FROM a section (section number required); `incoming`: references TO this spec | `outgoing` |
| `--subsections` | Include subsections when collecting outgoing references | `false` |

### `list-images`

List a specification's embedded images as JSON (`{images, count}`).

Usage: `3gpp-mcp list-images [--version <v>] [fetch flags] <spec-id>`

### `get-image`

Write an embedded image's raw bytes to a file or stdout. Name, MIME type and
size go to stderr. Unlike the MCP tool, an EMF/WMF image is still written —
a shell user can open it — with a conversion note on stderr.

Usage: `3gpp-mcp get-image [flags] <spec-id> <image-name>`

| Flag | Description | Default |
|------|-------------|---------|
| `--version` | Specification version to read | the database version |
| `-o` | Write the image to this file | stdout |

```bash
3gpp-mcp get-image --db data/3gpp.db -o figure1.png "TS 23.501" image1.png
```

### `completion`

Print a shell completion script.

```bash
3gpp-mcp completion bash    # or zsh, fish
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `THREEGPP_MCP_TRANSPORT` | Transport for `serve` (`stdio` or `http`); overridden by `--transport` |
| `THREEGPP_MCP_ADDR` | HTTP listen address for `serve`; overridden by `--addr` |
| `THREEGPP_MCP_BEARER_TOKEN` | Bearer token for HTTP transport auth |
| `PORT` | PaaS convention (Cloud Run / Heroku); `serve` defaults to HTTP transport on `:$PORT` |
| `THREEGPP_VERSION_CACHE_MB` | Size limit of the on-demand version cache in MB (default `1024`) |
| `THREEGPP_FETCH_BUDGET` | How long a tool call waits for an on-demand fetch (default `60s`) |
| `THREEGPP_MAX_ZIP_SIZE_MB` | Max ZIP download size (default `512`) |
| `THREEGPP_CACHE_TTL_HOURS` | Spec list cache TTL in hours (default `24`) |
| `XDG_CACHE_HOME` | Cache directory root, per the XDG Base Directory spec |
