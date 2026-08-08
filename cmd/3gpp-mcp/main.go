package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/specver"
	"github.com/higebu/3gpp-mcp/tools"
	"github.com/higebu/3gpp-mcp/versionstore"
	"github.com/higebu/3gpp-mcp/web"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is set at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok")
}

func bearerAuthMiddleware(token string, next http.Handler) http.Handler {
	expected := []byte(token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// RFC 7235 makes the auth-scheme case-insensitive, so split it off
		// and compare only the credentials in constant time.
		scheme, credentials, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") ||
			subtle.ConstantTimeCompare([]byte(strings.TrimSpace(credentials)), expected) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="3gpp-mcp"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// buildHTTPHandler assembles the HTTP transport's handler tree. When a bearer
// token is set it guards everything except the health probe: the web viewer
// serves the same corpus as the MCP tools, so leaving it open would make the
// token meaningless.
func buildHTTPHandler(src *tools.Source, s *mcp.Server, bearerToken string, enableWeb bool) http.Handler {
	// Stateless mode is required to serve protocol version 2026-07-28,
	// whose lifecycle has no initialize handshake or Mcp-Session-Id.
	// Older clients still work: each request runs in a temporary session.
	// This server never initiates server->client requests, so nothing is
	// lost by not keeping sessions.
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)

	appMux := http.NewServeMux()
	if enableWeb {
		appMux.Handle("/mcp/", http.StripPrefix("/mcp", mcpHandler))
		appMux.Handle("/", web.NewServer(src))
	} else {
		appMux.Handle("/", mcpHandler)
	}

	var app http.Handler = appMux
	if bearerToken != "" {
		app = bearerAuthMiddleware(bearerToken, appMux)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", healthHandler)
	mux.Handle("/", app)
	return mux
}

// command is one CLI subcommand. The dispatch in main, the usage line and
// the shell completion scripts are all generated from the commands table, so
// registering a command there is the only step needed to expose it everywhere.
type command struct {
	name    string
	aliases []string
	desc    string
	run     func(args []string)
}

// commands is ordered for the usage line and completion listings. Aliases are
// dispatchable but stay out of both. Populated in init because cmdCompletion
// renders this very table, which a composite literal would turn into an
// initialization cycle.
var commands []command

func init() {
	commands = []command{
		{name: "serve", desc: "Start the MCP server", run: cmdServe},
		{name: "build", aliases: []string{"pipeline"}, desc: "Download and import specs into database", run: cmdPipeline},
		{name: "download", desc: "Download spec files from 3GPP archive", run: cmdDownload},
		{name: "import", aliases: []string{"convert"}, desc: "Import a single DOCX file into database", run: cmdConvert},
		{name: "import-dir", aliases: []string{"convert-dir"}, desc: "Import a directory of DOCX files into database", run: cmdConvertDir},
		{name: "update", desc: "Update database to latest spec versions", run: cmdUpdate},
		{name: "list-specs", desc: "List specifications in the database", run: cmdListSpecs},
		{name: "list-versions", desc: "List versions of a specification", run: cmdListVersions},
		{name: "get-toc", desc: "Print a specification's table of contents", run: cmdGetTOC},
		{name: "get-section", desc: "Print a section's markdown content", run: cmdGetSection},
		{name: "compare-versions", desc: "Compare two versions of a specification", run: cmdCompareVersions},
		{name: "search", desc: "Full-text search across specifications", run: cmdSearch},
		{name: "list-openapi", desc: "List OpenAPI definitions", run: cmdListOpenAPI},
		{name: "get-openapi", desc: "Print an OpenAPI definition", run: cmdGetOpenAPI},
		{name: "get-references", desc: "Print cross-references as JSON", run: cmdGetReferences},
		{name: "list-images", desc: "List embedded images in a specification", run: cmdListImages},
		{name: "get-image", desc: "Write an embedded image to a file or stdout", run: cmdGetImage},
		{name: "completion", desc: "Generate shell completion scripts", run: cmdCompletion},
	}
}

// commandNames returns the primary command names in table order.
func commandNames() []string {
	names := make([]string, len(commands))
	for i, c := range commands {
		names[i] = c.name
	}
	return names
}

// lookupCommand resolves a primary name or alias to its command.
func lookupCommand(name string) *command {
	for i := range commands {
		c := &commands[i]
		if c.name == name {
			return c
		}
		for _, a := range c.aliases {
			if a == name {
				return c
			}
		}
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp <command> [options]")
		fmt.Fprintln(os.Stderr, "Commands: "+strings.Join(commandNames(), ", "))
		os.Exit(1)
	}

	c := lookupCommand(os.Args[1])
	if c == nil {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		fmt.Fprintln(os.Stderr, "Commands: "+strings.Join(commandNames(), ", "))
		os.Exit(1)
	}
	c.run(os.Args[2:])
}

// defaultVersionCacheMB reads the version cache limit from the environment,
// falling back to the versionstore default.
func defaultVersionCacheMB() int64 {
	if v := os.Getenv("THREEGPP_VERSION_CACHE_MB"); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil {
			return mb
		}
		log.Printf("WARNING: ignoring invalid THREEGPP_VERSION_CACHE_MB=%q", v)
	}
	return versionstore.DefaultLimitBytes >> 20
}

// defaultFetchBudget reads the on-demand fetch budget from the environment.
func defaultFetchBudget() time.Duration {
	if v := os.Getenv("THREEGPP_FETCH_BUDGET"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		log.Printf("WARNING: ignoring invalid THREEGPP_FETCH_BUDGET=%q", v)
	}
	return versionstore.DefaultBudget
}

// newMCPServer builds the MCP server and registers every tool. It is shared
// by cmdServe and the transport tests.
func newMCPServer(d *db.DB, src *tools.Source) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "3gpp-mcp",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "3GPP specification server. Use list_specs to find specifications, get_toc to browse structure, get_section to read specification document text (architecture, procedures, requirements), and search to find relevant sections. Use get_references to explore cross-references between specifications (outgoing: what a section references; incoming: what references a spec). For 5G API details (HTTP methods, request/response bodies, paths, schemas, data models) from TS 29.xxx series, use list_openapi to discover APIs and get_openapi to read their OpenAPI definitions. Always prefer get_openapi over get_section when looking up API request/response formats or data type definitions.\n\n" +
			"Versions: the database holds one version per specification, and every get_section, get_toc and search result names the specification and version it came from. To compare a procedure across releases, call list_versions to see which versions exist, then use compare_versions: without section_number it summarizes which sections were added, removed or changed between two versions, and with section_number it returns a line-level diff of that section's text. A version that is not in the database is downloaded and converted on first use, which takes up to a few minutes for a large specification; when that happens the tool says so and you should call it again with the same arguments. Section numbers move between releases, so check get_toc for the older version before reading a section of it. get_image and list_images also accept a version: an archived version's images are downloaded on their own first use, again taking up to a few minutes before a retry succeeds. search and get_references only have data for the version in the database.",
	})

	mcp.AddTool(s, tools.ListSpecsTool, tools.HandleListSpecs(d))
	mcp.AddTool(s, tools.ListVersionsTool, tools.HandleListVersions(src))
	mcp.AddTool(s, tools.GetTOCTool, tools.HandleGetTOC(src))
	mcp.AddTool(s, tools.GetSectionTool, tools.HandleGetSection(src))
	mcp.AddTool(s, tools.CompareVersionsTool, tools.HandleCompareVersions(src))
	mcp.AddTool(s, tools.SearchTool, tools.HandleSearch(d))
	mcp.AddTool(s, tools.ListOpenAPITool, tools.HandleListOpenAPI(d))
	mcp.AddTool(s, tools.GetOpenAPITool, tools.HandleGetOpenAPI(d))
	mcp.AddTool(s, tools.GetReferencesTool, tools.HandleGetReferences(d))
	mcp.AddTool(s, tools.ListImagesTool, tools.HandleListImages(src))
	mcp.AddTool(s, tools.GetImageTool, tools.HandleGetImage(src))

	return s
}

func cmdServe(args []string) {
	defaultTransport := "stdio"
	if v := os.Getenv("THREEGPP_MCP_TRANSPORT"); v != "" {
		defaultTransport = v
	} else if os.Getenv("PORT") != "" {
		// PaaS like Cloud Run / Heroku inject PORT and expect an HTTP server.
		defaultTransport = "http"
	}

	defaultAddr := ":8080"
	if v := os.Getenv("THREEGPP_MCP_ADDR"); v != "" {
		defaultAddr = v
	} else if p := os.Getenv("PORT"); p != "" {
		defaultAddr = ":" + p
	}

	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "3gpp.db", "Path to SQLite database")
	transport := fs.String("transport", defaultTransport, "Transport type: stdio or http (env: THREEGPP_MCP_TRANSPORT, or PORT to force http)")
	addr := fs.String("addr", defaultAddr, "HTTP listen address (env: THREEGPP_MCP_ADDR, or PORT)")
	bearerToken := fs.String("bearer-token", "", "Bearer token for HTTP auth (env: THREEGPP_MCP_BEARER_TOKEN)")
	enableWeb := fs.Bool("web", false, "Enable web viewer alongside MCP server (HTTP transport only)")
	noFetch := fs.Bool("no-fetch", false, "Disable on-demand fetching of spec versions that are not in the database")
	versionCache := fs.String("version-cache", "", "Path to the on-demand version cache (default: $XDG_CACHE_HOME/3gpp-mcp/versions.db)")
	versionCacheMB := fs.Int64("version-cache-mb", defaultVersionCacheMB(), "Size limit of the version cache in MB, or -1 for unlimited (env: THREEGPP_VERSION_CACHE_MB)")
	fetchBudget := fs.Duration("fetch-budget", defaultFetchBudget(), "How long a tool call waits for an on-demand fetch before asking the caller to retry (env: THREEGPP_FETCH_BUDGET)")
	_ = fs.Parse(args)

	// Environment variable takes precedence if flag is not set
	if *bearerToken == "" {
		*bearerToken = os.Getenv("THREEGPP_MCP_BEARER_TOKEN")
	}

	d, err := db.Open(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer d.Close()

	src := tools.NewSource(d)
	src.Budget = *fetchBudget
	if !*noFetch {
		// A read-only or ephemeral filesystem — a scratch container image, for
		// instance — cannot hold the cache. That disables past-version reads but
		// leaves everything else working, so it is a warning, not a failure.
		store, err := versionstore.Open(versionstore.Options{
			Path:       *versionCache,
			LimitBytes: *versionCacheMB << 20,
		})
		if err != nil {
			log.Printf("WARNING: on-demand version fetching disabled: %v", err)
		} else {
			defer store.Close()
			src.Store = store
		}
	}

	s := newMCPServer(d, src)

	switch *transport {
	case "stdio":
		log.Println("Starting 3gpp-mcp server on stdio...")
		if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	case "http":
		if *enableWeb {
			log.Printf("Starting 3gpp-mcp server on %s (HTTP + Web viewer)...", *addr)
			log.Printf("  MCP endpoint: http://localhost%s/mcp/", *addr)
			log.Printf("  Web viewer:   http://localhost%s/", *addr)
		} else {
			log.Printf("Starting 3gpp-mcp server on %s (HTTP)...", *addr)
		}
		if *bearerToken == "" {
			log.Println("WARNING: HTTP transport running without authentication. Set -bearer-token or THREEGPP_MCP_BEARER_TOKEN to secure the server.")
		}

		server := &http.Server{
			Handler: buildHTTPHandler(src, s, *bearerToken, *enableWeb),
			// Bound header reads and idle keep-alives so stalled connections
			// (slowloris) cannot pile up. No overall write timeout: MCP
			// responses may legitimately stream for a long time.
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
		}

		ln, err := net.Listen("tcp", httpListenAddr(*addr))
		if err != nil {
			log.Printf("Server error: %v", err)
			exit(1)
			return
		}

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		if err := runHTTPServer(ctx, server, ln); err != nil {
			log.Printf("Server error: %v", err)
			exit(1)
			return
		}
	default:
		log.Fatalf("Unknown transport: %s", *transport)
	}
}

// httpListenAddr returns the TCP address the HTTP transport binds. An empty
// addr keeps http.Server.ListenAndServe's historical meaning of ":http";
// net.Listen alone would pick an ephemeral port instead.
func httpListenAddr(addr string) string {
	if addr == "" {
		return ":http"
	}
	return addr
}

// shutdownTimeout bounds the graceful drain when the HTTP server shuts down.
// It is a variable so tests can shorten it.
var shutdownTimeout = 10 * time.Second

// runHTTPServer serves on ln until serving fails or ctx is cancelled. On
// cancellation it drains in-flight requests and returns nil, so the caller's
// deferred database and version cache closes actually run. A failed drain —
// the deadline expired with requests still in flight — is returned as an
// error: requests were cut off, so the process must not exit 0.
func runHTTPServer(ctx context.Context, server *http.Server, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() { errCh <- server.Serve(ln) }()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Println("Shutting down...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
	}
	return nil
}

func cmdConvert(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	dbPath := fs.String("db", "3gpp.db", "Output SQLite database path")
	convertImage := fs.Bool("convert-image", false, "Convert EMF/WMF images to PNG using LibreOffice (requires soffice)")
	_ = fs.Parse(args)

	// flag stops parsing at the first non-flag argument, so any option placed
	// after <docx-file> is silently left in fs.Args() instead of being applied.
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp import [options] <docx-file>")
		fmt.Fprintln(os.Stderr, "Options must come before <docx-file>.")
		os.Exit(1)
	}
	docxPath := fs.Arg(0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := runConvert(ctx, *dbPath, docxPath, *convertImage); err != nil {
		log.Printf("Convert failed: %v", err)
		exit(1)
	}
}

// runConvert opens the database, imports a single .docx and closes the
// database again — also on failure, where log.Fatalf in the caller would skip
// deferred closes and leave uncheckpointed WAL sidecars behind.
func runConvert(ctx context.Context, dbPath, docxPath string, convertImage bool) error {
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer d.Close()

	if err := d.InitSchema(); err != nil {
		return fmt.Errorf("init schema: %w", err)
	}

	fmt.Printf("Parsing %s...\n", docxPath)
	// ConvertSingleFile's errors already name the stage and the file
	// ("parse %s: ..."), and cmdConvert prefixes "Convert failed:" — wrapping
	// here again would only repeat that context.
	if err := pipeline.ConvertSingleFile(ctx, d, docxPath, convertImage); err != nil {
		return err
	}
	fmt.Printf("Written to %s\n", dbPath)
	return nil
}

func cmdConvertDir(args []string) {
	fs := flag.NewFlagSet("import-dir", flag.ExitOnError)
	dbPath := fs.String("db", "3gpp.db", "Output SQLite database path")
	workers := fs.Int("parse-workers", runtime.NumCPU(), "Number of parallel parse workers")
	convertDoc := fs.Bool("convert-doc", false, "Convert .doc to .docx using LibreOffice")
	convertImage := fs.Bool("convert-image", false, "Convert EMF/WMF images to PNG using LibreOffice (requires soffice)")
	_ = fs.Parse(args)

	// flag stops parsing at the first non-flag argument, so any option placed
	// after <directory> is silently left in fs.Args() instead of being applied.
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp import-dir [options] <directory>")
		fmt.Fprintln(os.Stderr, "Options must come before <directory>.")
		os.Exit(1)
	}
	dirPath := fs.Arg(0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := runConvertDir(ctx, *dbPath, dirPath, *workers, *convertDoc, *convertImage); err != nil {
		log.Printf("Convert dir failed: %v", err)
		exit(1)
	}
}

// runConvertDir opens the database, imports a directory of .docx files and
// closes the database again — also on failure, so no WAL sidecars are left
// behind when the caller exits via log.Fatalf.
func runConvertDir(ctx context.Context, dbPath, dirPath string, workers int, convertDoc, convertImage bool) error {
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer d.Close()

	if err := d.InitSchema(); err != nil {
		return fmt.Errorf("init schema: %w", err)
	}

	// ConvertDir's errors do not all carry the directory (e.g. "all N files
	// failed to parse"), so name it here.
	if err := pipeline.ConvertDir(ctx, d, dirPath, workers, convertDoc, convertImage); err != nil {
		return fmt.Errorf("import %s: %w", dirPath, err)
	}
	return nil
}

// exit is swapped in tests to cover fatal error paths without terminating
// the test process.
var exit = os.Exit

// newHTTPClient builds the client used for archive scraping and downloads.
// It is a variable so tests can point commands that construct their own
// client, such as update, at a mock archive server.
var newHTTPClient = func(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout}
}

// requireSelector exits unless at least one spec selector flag was provided.
func requireSelector(release int, latest bool, spec, series string) {
	if release != 0 || latest || spec != "" || series != "" {
		return
	}
	fmt.Fprintln(os.Stderr, "Specify --release, --latest, --series, or --spec")
	exit(1)
}

// resolveSpecs fetches, parses, and filters specs based on CLI flags.
func resolveSpecs(ctx context.Context, client *http.Client, specList, specFlag, seriesFlag string, release int, useCache bool, scrapeConcurrency int) []*pipeline.SpecVersion {
	var seriesFilter []string
	if seriesFlag != "" {
		seriesFilter = strings.Split(seriesFlag, ",")
	}

	var entries []string
	var err error
	if specList != "" {
		fmt.Printf("Loading spec list from %s...\n", specList)
		entries, err = pipeline.LoadSpecList(specList)
		if err != nil {
			log.Fatalf("Failed to load spec list: %v", err)
		}
	} else if specFlag != "" {
		fmt.Printf("Fetching versions for %s...\n", specFlag)
		entries, err = pipeline.FetchSpecZips(ctx, client, specFlag, useCache)
		if err != nil {
			log.Fatalf("Failed to fetch spec versions: %v", err)
		}
	} else {
		fmt.Println("Fetching spec list from 3GPP archive...")
		entries, err = pipeline.FetchSpecList(ctx, client, seriesFilter, useCache, scrapeConcurrency)
		var partial *pipeline.PartialSpecListError
		if errors.As(err, &partial) {
			// Proceeding would silently drop every spec under the failed
			// directories from the result, so a build or download from this
			// list would be quietly incomplete.
			log.Printf("Aborting: %v; rerun to retry", partial)
			exit(1)
			return nil
		}
		if err != nil {
			log.Fatalf("Failed to fetch spec list: %v", err)
		}
	}

	var specs []*pipeline.SpecVersion
	for _, e := range entries {
		if s := pipeline.ParseSpecEntry(e); s != nil {
			specs = append(specs, s)
		}
	}
	fmt.Printf("Parsed %d spec entries\n", len(specs))

	return pipeline.FilterSpecs(specs, release, seriesFilter, specFlag, true)
}

func cmdDownload(args []string) {
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	release := fs.Int("release", 0, "Download specs for specific release (e.g., 19)")
	latest := fs.Bool("latest", false, "Select every spec at its latest version (use when no other selector is given)")
	seriesFlag := fs.String("series", "", "Filter by series, comma-separated (e.g., 23,29)")
	specFlag := fs.String("spec", "", "Download specific spec (e.g., 23.501)")
	outputDir := fs.String("output-dir", "specs", "Output directory")
	parallel := fs.Int("parallel", runtime.NumCPU(), "Number of parallel downloads")
	convertDoc := fs.Bool("convert-doc", false, "Convert .doc to .docx using LibreOffice")
	specList := fs.String("spec-list", "", "Use spec list file instead of scraping")
	noCache := fs.Bool("no-cache", false, "Disable spec list cache")
	scrapeWorkers := fs.Int("scrape-workers", 0, "Concurrency for scraping spec listings (0 = auto)")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	_ = fs.Parse(args)

	requireSelector(*release, *latest, *specFlag, *seriesFlag)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := &http.Client{Timeout: *timeout}
	filtered := resolveSpecs(ctx, client, *specList, *specFlag, *seriesFlag, *release, !*noCache, *scrapeWorkers)

	if len(filtered) == 0 {
		fmt.Println("No specs matched the filters.")
		return
	}

	fmt.Printf("Downloading %d specs to %s...\n", len(filtered), *outputDir)
	if err := os.MkdirAll(*outputDir, 0o755); err != nil {
		log.Fatalf("Failed to create output directory: %v", err)
	}

	stats := pipeline.DownloadSpecs(ctx, client, filtered, *outputDir, *parallel, *convertDoc, *timeout)

	fmt.Println("\nDownload complete:")
	for status, count := range stats {
		if count > 0 {
			fmt.Printf("  %s: %d\n", status, count)
		}
	}
}

func cmdPipeline(args []string) {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	dbPath := fs.String("db", "3gpp.db", "Output SQLite database path")
	release := fs.Int("release", 0, "Download specs for specific release (e.g., 19)")
	latest := fs.Bool("latest", false, "Select every spec at its latest version (use when no other selector is given)")
	seriesFlag := fs.String("series", "", "Filter by series, comma-separated (e.g., 23,29)")
	specFlag := fs.String("spec", "", "Download specific spec (e.g., 23.501)")
	workers := fs.Int("workers", runtime.NumCPU(), "Number of parallel workers")
	convertDoc := fs.Bool("convert-doc", false, "Convert .doc to .docx using LibreOffice")
	convertImage := fs.Bool("convert-image", false, "Convert EMF/WMF images to PNG using LibreOffice (requires soffice)")
	specList := fs.String("spec-list", "", "Use spec list file instead of scraping")
	noCache := fs.Bool("no-cache", false, "Disable spec list cache")
	scrapeWorkers := fs.Int("scrape-workers", 0, "Concurrency for scraping spec listings (0 = auto)")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	_ = fs.Parse(args)

	requireSelector(*release, *latest, *specFlag, *seriesFlag)

	// Resolve specs before opening the database: resolveSpecs exits via
	// log.Fatalf on failure, which must not strand an open WAL-mode database.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := &http.Client{Timeout: *timeout}
	filtered := resolveSpecs(ctx, client, *specList, *specFlag, *seriesFlag, *release, !*noCache, *scrapeWorkers)

	if err := runPipeline(ctx, *dbPath, client, filtered, *workers, *convertDoc, *convertImage, *timeout); err != nil {
		log.Printf("Pipeline failed: %v", err)
		exit(1)
	}
}

// runPipeline opens the database and feeds specs through the download +
// convert pipeline, closing the database again — also on failure, so no WAL
// sidecars are left behind when the caller exits via log.Fatalf.
func runPipeline(ctx context.Context, dbPath string, client *http.Client, specs []*pipeline.SpecVersion, workers int, convertDoc, convertImage bool, timeout time.Duration) error {
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer d.Close()

	if err := d.InitSchema(); err != nil {
		return fmt.Errorf("init schema: %w", err)
	}

	if len(specs) == 0 {
		fmt.Println("No specs matched the filters.")
		return nil
	}

	fmt.Printf("Processing %d specs with %d workers...\n", len(specs), workers)

	p := &pipeline.Pipeline{
		DB:           d,
		Client:       client,
		Workers:      workers,
		ConvertDoc:   convertDoc,
		ConvertImage: convertImage,
		Timeout:      timeout,
	}

	// Run's errors are self-describing ("all N specs failed", ctx.Err()), and
	// cmdPipeline prefixes "Pipeline failed:" — wrapping here would only
	// repeat that context.
	return p.Run(ctx, specs)
}

func cmdCompletion(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp completion <bash|zsh|fish>")
		os.Exit(1)
	}
	names := strings.Join(commandNames(), " ")
	switch args[0] {
	case "bash":
		fmt.Printf(`# bash completion for 3gpp-mcp
_3gpp_mcp() {
    local commands="%s"
    local cur="${COMP_WORDS[COMP_CWORD]}"
    if [[ ${COMP_CWORD} -eq 1 ]]; then
        COMPREPLY=($(compgen -W "${commands}" -- "${cur}"))
    fi
}
complete -F _3gpp_mcp 3gpp-mcp
`, names)
	case "zsh":
		fmt.Print("#compdef 3gpp-mcp\n\n_3gpp_mcp() {\n    local -a commands\n    commands=(\n")
		for _, c := range commands {
			// A description with an apostrophe would terminate the quoted
			// string and corrupt the script.
			fmt.Printf("        '%s:%s'\n", c.name, strings.ReplaceAll(c.desc, "'", `'\''`))
		}
		fmt.Print("    )\n    _describe '3gpp-mcp command' commands\n}\n\n_3gpp_mcp\n")
	case "fish":
		fmt.Print("# fish completion for 3gpp-mcp\ncomplete -c 3gpp-mcp -f\n")
		for _, c := range commands {
			fmt.Printf("complete -c 3gpp-mcp -n \"not __fish_seen_subcommand_from %s\" -a %s -d '%s'\n", names, c.name, strings.ReplaceAll(c.desc, "'", `\'`))
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown shell: %s (supported: bash, zsh, fish)\n", args[0])
		os.Exit(1)
	}
}

// finalizeWorkingCopy merges path's write-ahead log into the main file and
// closes d, leaving a single self-contained file that can be renamed over the
// live database.
//
// The sidecar files are only removed once the merge is known to have happened.
// PRAGMA wal_checkpoint reports a blocked checkpoint in its result row rather
// than as an error, so a checkpoint can leave the whole update sitting in the
// WAL while both Exec and Close return nil; deleting the WAL at that point
// discards every change the run just made. A clean close unlinks the WAL, so a
// surviving non-empty one means the merge did not complete.
func finalizeWorkingCopy(d *db.DB, path string) error {
	checkpointErr := d.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	closeErr := d.Close()
	if checkpointErr != nil {
		return fmt.Errorf("checkpoint working copy: %w", checkpointErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close working copy: %w", closeErr)
	}
	fi, err := os.Stat(path + "-wal")
	switch {
	case err == nil:
		if fi.Size() > 0 {
			return fmt.Errorf("working copy still has a %d-byte write-ahead log after checkpoint", fi.Size())
		}
	case !errors.Is(err, os.ErrNotExist):
		// A stat failure is not proof the WAL is gone — treating it as "no
		// WAL" is exactly the unknown-merge-state this check exists to catch.
		return fmt.Errorf("stat working copy write-ahead log: %w", err)
	}
	// A sidecar that survives next to the renamed database would be picked up
	// by the next run's working copy, so refuse the rename.
	return removeStaleSidecars(path)
}

// removeStaleSidecars deletes the -wal and -shm files SQLite keeps next to the
// database at path.
//
// SQLite associates a write-ahead log with its database by file name alone, so
// a sidecar that outlives its database — left behind by a killed run, or
// orphaned when a new file is renamed over the live path — is replayed into
// whatever database next answers to that name. Deleting a database without its
// sidecars is the corruption hazard the SQLite documentation warns about.
func removeStaleSidecars(path string) error {
	var errs []error
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if err := os.Remove(sidecar); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove %s: %w", sidecar, err))
		}
	}
	return errors.Join(errs...)
}

// removeWorkingCopy deletes the working copy at path together with its
// sidecars. The sidecars go first, and a sidecar that cannot be removed stops
// the cleanup: an interrupted or failed removal can leave a database without
// its WAL, but never a WAL without its database. Deleting the database anyway
// would orphan the survivor onto the copy the next run writes at this path,
// which is the corruption this cleanup exists to prevent.
func removeWorkingCopy(path string) error {
	if err := removeStaleSidecars(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	return nil
}

// discardWorkingCopy drops a working copy the run is walking away from. The
// caller is already aborting, or has nothing to update, so a failure here
// cannot change what it does next — but it leaves debris that stops the next
// run in its tracks, so it is reported rather than dropped.
func discardWorkingCopy(path string) {
	if err := removeWorkingCopy(path); err != nil {
		log.Printf("warning: failed to remove working copy: %v", err)
	}
}

// errSidecarsRemain marks a replacement whose rename went through while the
// replaced database's sidecars survived: the new file is in place but is not
// safe to open until they are gone.
var errSidecarsRemain = errors.New("stale sidecars survived the replacement")

// replaceDatabase moves the finalized working copy over the live database.
//
// The sidecars of the database being replaced are named after the live path,
// so the rename leaves them next to the file that just took that name and
// SQLite would replay that old write-ahead log into it. A serve process that
// still has them open keeps reading its own unlinked inodes until it restarts.
//
// Sidecars are bound to a path rather than an inode, so no ordering of these
// two steps keeps the new file from ever sharing a directory entry with the
// old sidecars; a kill between them leaves the state this clears up. Deleting
// them before the rename only trades that for discarding a live write-ahead
// log that the run may then fail to replace, so the removal follows the
// rename and the window is two syscalls wide.
func replaceDatabase(newPath, dbPath string) error {
	if err := os.Rename(newPath, dbPath); err != nil {
		return fmt.Errorf("rename working copy: %w", err)
	}
	if err := removeStaleSidecars(dbPath); err != nil {
		return fmt.Errorf("%w and must be deleted before serving %s: %w", errSidecarsRemain, dbPath, err)
	}
	return nil
}

func cmdUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	dbPath := fs.String("db", "3gpp.db", "SQLite database path")
	workers := fs.Int("workers", runtime.NumCPU(), "Number of parallel workers")
	convertDoc := fs.Bool("convert-doc", false, "Convert .doc to .docx using LibreOffice")
	convertImage := fs.Bool("convert-image", false, "Convert EMF/WMF images to PNG using LibreOffice (requires soffice)")
	specList := fs.String("spec-list", "", "Use spec list file instead of scraping")
	noCache := fs.Bool("no-cache", false, "Disable spec list cache")
	scrapeWorkers := fs.Int("scrape-workers", 0, "Concurrency for scraping spec listings (0 = auto)")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	_ = fs.Parse(args)

	newPath := *dbPath + ".new"
	// Clear any copy left by a previous failed run. Its sidecars have to go
	// too: a -wal left by a run that was killed mid-update outlives the copy
	// itself and would be replayed into the one VACUUM INTO writes below.
	if err := removeWorkingCopy(newPath); err != nil {
		log.Fatalf("Failed to remove stale working copy: %v", err)
	}

	// Open live DB (WAL mode allows one concurrent writer alongside serve's readers)
	// to snapshot current spec versions and create a working copy via VACUUM INTO.
	src, err := db.OpenReadWrite(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	currentResult, err := src.ListSpecs(context.Background(), "", "", -1, 0)
	if err != nil {
		// An unreadable database is not an empty one; telling the user to run
		// 'build' would hide the real failure and still exit 0.
		_ = src.Close()
		log.Fatalf("Failed to read specs from %s: %v", *dbPath, err)
	}
	if len(currentResult.Specs) == 0 {
		_ = src.Close()
		fmt.Println("No specs in database. Use 'build' command first.")
		return
	}
	fmt.Printf("Found %d specs in database\n", len(currentResult.Specs))

	fmt.Println("Creating working copy of database...")
	if err := src.VacuumInto(newPath); err != nil {
		_ = src.Close()
		log.Fatalf("Failed to create working copy: %v", err)
	}
	_ = src.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := newHTTPClient(*timeout)
	useCache := !*noCache

	// Fetch latest versions from FTP
	var entries []string
	if *specList != "" {
		entries, err = pipeline.LoadSpecList(*specList)
	} else {
		fmt.Println("Fetching spec list from 3GPP archive...")
		entries, err = pipeline.FetchSpecList(ctx, client, nil, useCache, *scrapeWorkers)
	}
	// A partial list is survivable here: a spec missing from it is skipped
	// rather than deleted, so the cost is missed updates until the next run.
	var partial *pipeline.PartialSpecListError
	if errors.As(err, &partial) {
		log.Printf("warning: %v; specs under the failed directories will not be updated this run", partial)
		err = nil
	}
	if err != nil {
		discardWorkingCopy(newPath)
		log.Fatalf("Failed to fetch spec list: %v", err)
	}

	var allSpecs []*pipeline.SpecVersion
	for _, e := range entries {
		if s := pipeline.ParseSpecEntry(e); s != nil {
			allSpecs = append(allSpecs, s)
		}
	}

	latestSpecs := pipeline.FilterSpecs(allSpecs, 0, nil, "", true)

	// Find specs that need updating
	normalizeID := func(id string) string {
		return strings.TrimPrefix(strings.TrimPrefix(id, "TS "), "TR ")
	}
	dbVersions := make(map[string]string)
	for _, s := range currentResult.Specs {
		dbVersions[normalizeID(s.ID)] = s.Version
	}

	var updates []*pipeline.SpecVersion
	for _, sv := range latestSpecs {
		normID := normalizeID(sv.SpecID)
		oldVer, ok := dbVersions[normID]
		if !ok {
			continue
		}
		// Stored versions are the dotted form, so compare in that form rather
		// than on the archive token.
		newVer, ok := specver.TokenToDotted(pipeline.SpecVersionString(sv))
		if !ok {
			continue
		}
		if specver.Compare(newVer, oldVer) > 0 {
			fmt.Printf("  %s: %s -> %s\n", sv.SpecID, oldVer, newVer)
			updates = append(updates, sv)
		}
	}

	if len(updates) == 0 {
		fmt.Println("All specs are up to date.")
		discardWorkingCopy(newPath)
		return
	}

	fmt.Printf("\n%d specs to update\n", len(updates))

	d, err := db.OpenReadWrite(newPath)
	if err != nil {
		discardWorkingCopy(newPath)
		log.Fatalf("Failed to open working copy: %v", err)
	}
	// VACUUM INTO copies whatever schema the live database has, which may
	// predate the current binary.
	if err := d.InitSchema(); err != nil {
		_ = d.Close()
		discardWorkingCopy(newPath)
		log.Fatalf("Failed to initialize working copy schema: %v", err)
	}

	p := &pipeline.Pipeline{
		DB:           d,
		Client:       client,
		Workers:      *workers,
		ConvertDoc:   *convertDoc,
		ConvertImage: *convertImage,
		Timeout:      *timeout,
	}

	if err := p.Run(ctx, updates); err != nil {
		_ = d.Close()
		discardWorkingCopy(newPath)
		log.Fatalf("Update failed: %v", err)
	}

	// Checkpoint WAL into the main file so the renamed DB is self-contained.
	if err := finalizeWorkingCopy(d, newPath); err != nil {
		log.Fatalf("Failed to finalize working copy: %v\nThe updated database is left at %s; %s was not replaced.", err, newPath, *dbPath)
	}

	// Atomically replace the live database. The serve process retains its old
	// inode until restarted; ExecStartPost in the systemd unit handles that.
	if err := replaceDatabase(newPath, *dbPath); err != nil {
		// Once the rename has gone through the working copy is no longer ours
		// to delete, and the database really was replaced: the leftover
		// sidecars are the whole failure, so say so instead of claiming the
		// update did not land.
		if errors.Is(err, errSidecarsRemain) {
			log.Fatalf("Database replaced, but %v", err)
		}
		discardWorkingCopy(newPath)
		log.Fatalf("Failed to replace database: %v", err)
	}
	fmt.Println("Database updated successfully.")
}
