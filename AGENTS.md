# AGENTS.md

MCP server that downloads 3GPP specification documents (.docx), converts them
to Markdown, stores them in SQLite with FTS5 full-text search, and serves them
via MCP tools and an optional web viewer. Go 1.26+, pure Go SQLite
(`modernc.org/sqlite`) — **no CGO**.

README.md has the command reference, MCP tool list, environment variables,
code-fence table and deployment guide. This file only covers what an agent
cannot easily discover from the code.

## Commands

```bash
make build                # Build to bin/3gpp-mcp
go test ./...             # Run tests (-short skips tests that hit the 3GPP FTP)
gofmt -l . && go vet ./... && golangci-lint run   # Lint (CI also runs go test -race)
make build-db             # Download + import latest version of every spec (RELEASE=19 for one release, MAX_RELEASE=19 to cap)
make web                  # HTTP server with web viewer at :8080
```

## Package Map

- `cmd/3gpp-mcp/` — CLI entry point (serve, build, download, import, import-dir,
  update, plus query subcommands mirroring the MCP read tools 1:1 in `query.go`).
  New subcommands register in the `commands` slice in `main.go` — dispatch, the
  usage line and the shell completion scripts are all generated from it
- `converter/docx/` — DOCX → Markdown parser; `converter/pipeline/` — streaming download + convert worker pool
- `db/` — SQLite schema, queries, FTS5
- `versionstore/` — on-demand cache of spec versions not in the prebuilt database
- `tools/` — MCP tool handlers; `web/` — web viewer (reads through the same `tools.Source`, so it is version-aware)
- `internal/specver/` — base-36 archive token (`k20`) ↔ dotted version (`20.2.0`)
- `internal/structdiff/`, `internal/textdiff/` — compare_versions diff logic, shared with the web compare page

## Invariants & Gotchas

- **One version per spec in the main database.** `specs` is keyed by
  `(id, version)`, but `InsertSpecWithSectionsAndImages` drops any other
  version of the same spec. Search depends on this: the FTS index has no
  version column, so a second version would double every hit.
- **Archived versions never reach search.** `versionstore` fetches them into a
  separate SQLite file (`$XDG_CACHE_HOME/3gpp-mcp/versions.db`) with no FTS
  tables. Fetches are deduplicated per (spec, version) and run on a context
  detached from the caller so a client timeout does not discard the work;
  entries are evicted LRU, and a version's text and images evict as one unit.
  Images are fetched lazily on the first `get_image`/`list_images` call.
  OpenAPI YAML and cross-references are prebuilt-only.
- **OpenAPI search is a second FTS index.** `openapi_chunks` / `openapi_chunks_fts`
  (`db.OpenAPIIndexSchema`) hold one row per schema and per operation, derived
  from `openapi_specs` by `internal/openapiindex` and **rebuilt wholesale** —
  never incrementally, because most `$ref`s cross into another file, so
  importing one document changes the chunks of documents imported before it.
  Every importing CLI command ends in `rebuildOpenAPIIndex`. The tokenizer is
  plain `unicode61`, no porter: these rows are identifiers, not prose, and
  `-`/`.`/`_` split so a partial name matches. A schema chunk expands `$ref`
  exactly one level. Like the rest of the OpenAPI features it is prebuilt-only
  and absent from `versionstore`. Databases built before this existed have no
  index at all — `serve` opens read-only and cannot create it, so
  `SearchOpenAPI` returns `db.ErrNoOpenAPIIndex` and the tool points at
  `build-openapi-index`.
- **Image references are format-independent**: `image://NAME?w=&h=` in body
  text, `<img src="image://...">` in table cells. `structdiff.NormalizeImageRefs`
  keeps conversion-pair extension changes (`.emf`/`.wmf`/`.pcz`/`.png`) from
  counting as content changes in diffs.
- **Tagged code fences** (` ```asn1 `, ` ```diameter `, ` ```xml `, ` ```sip `,
  ` ```sdp `, ` ```latex `): Diameter, XML, SIP and SDP blocks carry no code
  style in the source documents and are detected by content
  (`converter/docx/xmlblock.go`, `converter/docx/sipblock.go`); paragraphs
  already fenced via style/font keep bare ` ``` ` fences and never enter
  content detection. ` ```latex ` is structural, not content-detected:
  `mathFenceBody` (`converter/docx/mathblock.go`) promotes a paragraph whose
  only content is one formula. Changing fence or notation output requires a
  database rebuild (`make build-db`) and a bump of
  `versionstore.cacheSchemaVersion`, which wipes old version caches on open.
- **Math is format-independent, like image references**: ` ```latex ` for a
  standalone equation, `$$...$$` for display math where a fence cannot go (a
  table cell, a list item), `$...$` inline. `runInfo.Text` holds *bare* LaTeX
  and `runInfo.markdownText()` is the only place the delimiters are added — so
  `mergeAdjacentRuns` must never merge a math run, or they vanish silently.
  `ommlToLaTeX` guarantees the LaTeX carries no `<`/`>` (`escapeMathAngles`),
  which is why `converter/docx/table.go` can write cell math unescaped and
  keep `&` as a matrix column separator.
- **HTTP transport runs stateless** (`StreamableHTTPOptions{Stateless: true}`)
  — required to serve MCP protocol 2026-07-28; older protocol versions get
  per-request sessions.
- `--convert-image` (EMF/WMF → PNG) requires LibreOffice (`soffice`) at runtime.

## Conventions

- Wrap errors with `fmt.Errorf("...: %w", err)`; errcheck exceptions live in `.golangci.yml`
- Use `internal/testutil.SetupTestDB(t)` for database tests and
  `internal/testutil.DownloadTestZip(t, url)` for FTP integration tests
  (auto-skipped in `-short` mode)
- CI (`.github/workflows/ci.yml`): build, vet, gofmt, golangci-lint, `go test -race`
