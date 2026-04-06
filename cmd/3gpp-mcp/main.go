package main

import (
	"context"
	"crypto/subtle"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/tools"
	"github.com/higebu/3gpp-mcp/web"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version is set at build time via -ldflags "-X main.version=x.y.z".
var version = "dev"

func bearerAuthMiddleware(token string, next http.Handler) http.Handler {
	expected := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(auth, expected) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp <command> [options]")
		fmt.Fprintln(os.Stderr, "Commands: serve, build, download, import, import-dir, update")
		os.Exit(1)
	}

	command := os.Args[1]
	args := os.Args[2:]

	switch command {
	case "serve":
		cmdServe(args)
	case "download":
		cmdDownload(args)
	case "import", "convert":
		cmdConvert(args)
	case "import-dir", "convert-dir":
		cmdConvertDir(args)
	case "build", "pipeline":
		cmdPipeline(args)
	case "update":
		cmdUpdate(args)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		fmt.Fprintln(os.Stderr, "Commands: serve, build, download, import, import-dir, update")
		os.Exit(1)
	}
}

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	dbPath := fs.String("db", "3gpp.db", "Path to SQLite database")
	transport := fs.String("transport", "stdio", "Transport type: stdio or http")
	addr := fs.String("addr", ":8080", "HTTP listen address")
	bearerToken := fs.String("bearer-token", "", "Bearer token for HTTP auth (env: THREEGPP_MCP_BEARER_TOKEN)")
	enableWeb := fs.Bool("web", false, "Enable web viewer alongside MCP server (HTTP transport only)")
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

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "3gpp-mcp",
		Version: version,
	}, &mcp.ServerOptions{
		Instructions: "3GPP specification server. Use list_specs to find specifications, get_toc to browse structure, get_section to read specification document text (architecture, procedures, requirements), and search to find relevant sections. Use get_references to explore cross-references between specifications (outgoing: what a section references; incoming: what references a spec). For 5G API details (HTTP methods, request/response bodies, paths, schemas, data models) from TS 29.xxx series, use list_openapi to discover APIs and get_openapi to read their OpenAPI definitions. Always prefer get_openapi over get_section when looking up API request/response formats or data type definitions.",
	})

	mcp.AddTool(s, tools.ListSpecsTool, tools.HandleListSpecs(d))
	mcp.AddTool(s, tools.GetTOCTool, tools.HandleGetTOC(d))
	mcp.AddTool(s, tools.GetSectionTool, tools.HandleGetSection(d))
	mcp.AddTool(s, tools.SearchTool, tools.HandleSearch(d))
	mcp.AddTool(s, tools.ListOpenAPITool, tools.HandleListOpenAPI(d))
	mcp.AddTool(s, tools.GetOpenAPITool, tools.HandleGetOpenAPI(d))
	mcp.AddTool(s, tools.GetReferencesTool, tools.HandleGetReferences(d))
	mcp.AddTool(s, tools.ListImagesTool, tools.HandleListImages(d))
	mcp.AddTool(s, tools.GetImageTool, tools.HandleGetImage(d))

	switch *transport {
	case "stdio":
		log.Println("Starting 3gpp-mcp server on stdio...")
		if err := s.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	case "http":
		mcpHandler := mcp.NewStreamableHTTPHandler(
			func(r *http.Request) *mcp.Server { return s },
			nil,
		)
		var mcpH http.Handler = mcpHandler
		if *bearerToken != "" {
			mcpH = bearerAuthMiddleware(*bearerToken, mcpHandler)
		} else {
			log.Println("WARNING: HTTP transport running without authentication. Set -bearer-token or THREEGPP_MCP_BEARER_TOKEN to secure the server.")
		}

		if *enableWeb {
			mux := http.NewServeMux()
			mux.Handle("/mcp/", http.StripPrefix("/mcp", mcpH))
			mux.Handle("/", web.NewServer(d))
			log.Printf("Starting 3gpp-mcp server on %s (HTTP + Web viewer)...", *addr)
			log.Printf("  MCP endpoint: http://localhost%s/mcp/", *addr)
			log.Printf("  Web viewer:   http://localhost%s/", *addr)
			if err := http.ListenAndServe(*addr, mux); err != nil {
				log.Fatalf("Server error: %v", err)
			}
		} else {
			log.Printf("Starting 3gpp-mcp server on %s (HTTP)...", *addr)
			if err := http.ListenAndServe(*addr, mcpH); err != nil {
				log.Fatalf("Server error: %v", err)
			}
		}
	default:
		log.Fatalf("Unknown transport: %s", *transport)
	}
}

func cmdConvert(args []string) {
	fs := flag.NewFlagSet("import", flag.ExitOnError)
	dbPath := fs.String("db", "3gpp.db", "Output SQLite database path")
	convertImage := fs.Bool("convert-image", false, "Convert EMF/WMF images to PNG using LibreOffice (requires soffice)")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp import [options] <docx-file>")
		os.Exit(1)
	}
	docxPath := fs.Arg(0)

	d, err := db.OpenReadWrite(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer d.Close()

	if err := d.InitSchema(); err != nil {
		log.Fatalf("Failed to init schema: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Printf("Parsing %s...\n", docxPath)
	if err := pipeline.ConvertSingleFile(ctx, d, docxPath, *convertImage); err != nil {
		log.Fatalf("Convert failed: %v", err)
	}
	fmt.Printf("Written to %s\n", *dbPath)
}

func cmdConvertDir(args []string) {
	fs := flag.NewFlagSet("import-dir", flag.ExitOnError)
	dbPath := fs.String("db", "3gpp.db", "Output SQLite database path")
	workers := fs.Int("parse-workers", runtime.NumCPU(), "Number of parallel parse workers")
	convertImage := fs.Bool("convert-image", false, "Convert EMF/WMF images to PNG using LibreOffice (requires soffice)")
	_ = fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp import-dir [options] <directory>")
		os.Exit(1)
	}
	dirPath := fs.Arg(0)

	d, err := db.OpenReadWrite(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer d.Close()

	if err := d.InitSchema(); err != nil {
		log.Fatalf("Failed to init schema: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := pipeline.ConvertDir(ctx, d, dirPath, *workers, *convertImage); err != nil {
		log.Fatalf("Convert dir failed: %v", err)
	}
}

// resolveSpecs fetches, parses, and filters specs based on CLI flags.
func resolveSpecs(ctx context.Context, client *http.Client, specList, specFlag, seriesFlag string, release int, allVersions, useCache bool) []*pipeline.SpecVersion {
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
		entries, err = pipeline.FetchSpecList(ctx, client, seriesFilter, useCache)
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

	return pipeline.FilterSpecs(specs, release, seriesFilter, specFlag, !allVersions)
}

func cmdDownload(args []string) {
	fs := flag.NewFlagSet("download", flag.ExitOnError)
	release := fs.Int("release", 0, "Download specs for specific release (e.g., 19)")
	latest := fs.Bool("latest", false, "Download the single latest version of each spec")
	allVersions := fs.Bool("all", false, "Download all versions")
	seriesFlag := fs.String("series", "", "Filter by series, comma-separated (e.g., 23,29)")
	specFlag := fs.String("spec", "", "Download specific spec (e.g., 23.501)")
	outputDir := fs.String("output-dir", "specs", "Output directory")
	parallel := fs.Int("parallel", runtime.NumCPU(), "Number of parallel downloads")
	convertDoc := fs.Bool("convert-doc", false, "Convert .doc to .docx using LibreOffice")
	specList := fs.String("spec-list", "", "Use spec list file instead of scraping")
	noCache := fs.Bool("no-cache", false, "Disable spec list cache")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	_ = fs.Parse(args)

	if *release == 0 && !*latest && !*allVersions && *specFlag == "" {
		fmt.Fprintln(os.Stderr, "Specify --release, --latest, --all, or --spec")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := &http.Client{Timeout: *timeout}
	filtered := resolveSpecs(ctx, client, *specList, *specFlag, *seriesFlag, *release, *allVersions, !*noCache)

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
	latest := fs.Bool("latest", false, "Download the single latest version of each spec")
	allVersions := fs.Bool("all", false, "Download all versions")
	seriesFlag := fs.String("series", "", "Filter by series, comma-separated (e.g., 23,29)")
	specFlag := fs.String("spec", "", "Download specific spec (e.g., 23.501)")
	workers := fs.Int("workers", runtime.NumCPU(), "Number of parallel workers")
	convertDoc := fs.Bool("convert-doc", false, "Convert .doc to .docx using LibreOffice")
	convertImage := fs.Bool("convert-image", false, "Convert EMF/WMF images to PNG using LibreOffice (requires soffice)")
	specList := fs.String("spec-list", "", "Use spec list file instead of scraping")
	noCache := fs.Bool("no-cache", false, "Disable spec list cache")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	_ = fs.Parse(args)

	if *release == 0 && !*latest && !*allVersions && *specFlag == "" {
		fmt.Fprintln(os.Stderr, "Specify --release, --latest, --all, or --spec")
		os.Exit(1)
	}

	d, err := db.OpenReadWrite(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer d.Close()

	if err := d.InitSchema(); err != nil {
		log.Fatalf("Failed to init schema: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := &http.Client{Timeout: *timeout}
	filtered := resolveSpecs(ctx, client, *specList, *specFlag, *seriesFlag, *release, *allVersions, !*noCache)

	if len(filtered) == 0 {
		fmt.Println("No specs matched the filters.")
		return
	}

	fmt.Printf("Processing %d specs with %d workers...\n", len(filtered), *workers)

	p := &pipeline.Pipeline{
		DB:           d,
		Client:       client,
		Workers:      *workers,
		ConvertDoc:   *convertDoc,
		ConvertImage: *convertImage,
		Timeout:      *timeout,
	}

	if err := p.Run(ctx, filtered); err != nil {
		log.Fatalf("Pipeline failed: %v", err)
	}
}

func cmdUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	dbPath := fs.String("db", "3gpp.db", "SQLite database path")
	workers := fs.Int("workers", runtime.NumCPU(), "Number of parallel workers")
	convertDoc := fs.Bool("convert-doc", false, "Convert .doc to .docx using LibreOffice")
	convertImage := fs.Bool("convert-image", false, "Convert EMF/WMF images to PNG using LibreOffice (requires soffice)")
	specList := fs.String("spec-list", "", "Use spec list file instead of scraping")
	noCache := fs.Bool("no-cache", false, "Disable spec list cache")
	timeout := fs.Duration("timeout", 30*time.Second, "HTTP timeout")
	_ = fs.Parse(args)

	d, err := db.OpenReadWrite(*dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	defer d.Close()

	// Get current specs from DB
	currentResult, err := d.ListSpecs("", -1, 0)
	if err != nil || len(currentResult.Specs) == 0 {
		fmt.Println("No specs in database. Use 'build' command first.")
		return
	}
	fmt.Printf("Found %d specs in database\n", len(currentResult.Specs))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	client := &http.Client{Timeout: *timeout}
	useCache := !*noCache

	// Fetch latest versions from FTP
	var entries []string
	if *specList != "" {
		entries, err = pipeline.LoadSpecList(*specList)
	} else {
		fmt.Println("Fetching spec list from 3GPP archive...")
		entries, err = pipeline.FetchSpecList(ctx, client, nil, useCache)
	}
	if err != nil {
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
		newVer := pipeline.SpecVersionString(sv)
		if pipeline.IsNewerVersion(newVer, oldVer) {
			fmt.Printf("  %s: %s -> %s\n", sv.SpecID, oldVer, newVer)
			updates = append(updates, sv)
		}
	}

	if len(updates) == 0 {
		fmt.Println("All specs are up to date.")
		return
	}

	fmt.Printf("\n%d specs to update\n", len(updates))

	p := &pipeline.Pipeline{
		DB:           d,
		Client:       client,
		Workers:      *workers,
		ConvertDoc:   *convertDoc,
		ConvertImage: *convertImage,
		Timeout:      *timeout,
	}

	if err := p.Run(ctx, updates); err != nil {
		log.Fatalf("Update failed: %v", err)
	}
}
