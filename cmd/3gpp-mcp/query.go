package main

// The query subcommands mirror the MCP read tools 1:1 (list_specs →
// list-specs, and so on) so the corpus can be inspected and scripted from a
// shell without an MCP client. They call the same tools.Source / db.DB layer
// as the MCP handlers and the web viewer, but render for a shell: JSON
// payloads go to stdout unpaginated (pipe to jq/head/less), warnings go to
// stderr, and an on-demand version fetch is waited out instead of answered
// with "call again".

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"time"

	"github.com/higebu/3gpp-mcp/internal/asn1index"
	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/structdiff"
	"github.com/higebu/3gpp-mcp/internal/textdiff"
	"github.com/higebu/3gpp-mcp/internal/tools"
	"github.com/higebu/3gpp-mcp/internal/versionstore"
)

// queryFlags holds the flags shared by every query subcommand.
type queryFlags struct {
	db             string
	noFetch        bool
	versionCache   string
	versionCacheMB int64
	fetchBudget    time.Duration
}

// addQueryFlags registers the shared flags. withFetch adds the on-demand
// version fetch flags, mirroring serve's names and environment defaults;
// commands that only read the prebuilt database skip them.
func addQueryFlags(fs *flag.FlagSet, withFetch bool) *queryFlags {
	qf := &queryFlags{}
	fs.StringVar(&qf.db, "db", "3gpp.db", "Path to SQLite database")
	if withFetch {
		fs.BoolVar(&qf.noFetch, "no-fetch", false, "Disable on-demand fetching of spec versions that are not in the database")
		fs.StringVar(&qf.versionCache, "version-cache", "", "Path to the on-demand version cache (default: $XDG_CACHE_HOME/3gpp-mcp/versions.db)")
		fs.Int64Var(&qf.versionCacheMB, "version-cache-mb", defaultVersionCacheMB(), "Size limit of the version cache in MB, or -1 for unlimited (env: THREEGPP_VERSION_CACHE_MB)")
		fs.DurationVar(&qf.fetchBudget, "fetch-budget", defaultFetchBudget(), "How long one fetch attempt waits before the command polls again (env: THREEGPP_FETCH_BUDGET)")
	}
	return qf
}

// queryClient is the HTTP client the query commands hand to their Source for
// archive access. It is nil in production — the pipeline then falls back to
// its default client, same as serve — and is swapped in tests to keep them
// off the network, mirroring newHTTPClient.
var queryClient *http.Client

// openSource opens the database read-only and wraps it in a Source. The
// version cache — which creates its directory and a SQLite file on open — is
// only touched when needStore is set, so plain database queries stay pure
// reads. A cache that cannot open disables past-version reads with a warning,
// same as serve.
func (qf *queryFlags) openSource(needStore bool) (*tools.Source, func(), error) {
	d, err := db.Open(qf.db)
	if err != nil {
		return nil, nil, fmt.Errorf("open database: %w", err)
	}
	src := tools.NewSource(d)
	src.Budget = qf.fetchBudget
	src.Client = queryClient
	cleanup := func() { _ = d.Close() }
	if needStore && !qf.noFetch {
		store, err := versionstore.Open(versionstore.Options{
			Path:       qf.versionCache,
			LimitBytes: qf.versionCacheMB << 20,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: on-demand version fetching disabled: %v\n", err)
		} else {
			src.Store = store
			cleanup = func() {
				_ = store.Close()
				_ = d.Close()
			}
		}
	}
	return src, cleanup, nil
}

// versionCacheExists reports whether the on-demand version cache file is
// already on disk, so a command can read it without ever creating one.
func (qf *queryFlags) versionCacheExists() bool {
	path := qf.versionCache
	if path == "" {
		var err error
		if path, err = versionstore.DefaultPath(); err != nil {
			return false
		}
	}
	_, err := os.Stat(path)
	return err == nil
}

// requireArgs exits with usage unless exactly want positional arguments were
// given. flag stops parsing at the first non-flag argument, so options placed
// after a positional would be silently ignored — the reminder mirrors import's.
func requireArgs(fs *flag.FlagSet, want int, usage string) {
	if fs.NArg() != want {
		fmt.Fprintln(os.Stderr, "Usage: "+usage)
		fmt.Fprintln(os.Stderr, "Options must come before positional arguments.")
		os.Exit(1)
	}
}

// runQuery drives one query command: interruptible context, error to stderr,
// exit 1 on failure. cleanupOnErr-style resource release lives in fn itself so
// it runs before exit.
func runQuery(name string, fn func(ctx context.Context) error) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := fn(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%s failed: %v\n", name, err)
		exit(1)
	}
}

// fetchPollInterval is how often a query command re-checks an on-demand fetch.
// It is a variable so tests can shorten it.
var fetchPollInterval = 5 * time.Second

// waitForFetch runs call and, unlike the MCP tools which tell the caller to
// retry, polls until an on-demand fetch finishes: a CLI user expects the
// command to come back with the content. Re-calling is cheap — the
// versionstore deduplicates the fetch within this process. Ctrl-C exits the
// process and abandons the fetch with it; the next invocation restarts it
// from scratch.
func waitForFetch(ctx context.Context, errOut io.Writer, call func() error) error {
	announced := false
	for {
		err := call()
		var inProgress *tools.FetchInProgressError
		if !errors.As(err, &inProgress) {
			return err
		}
		if !announced {
			subject := fmt.Sprintf("%s v%s", inProgress.SpecID, inProgress.Version)
			if inProgress.Images {
				subject = "images for " + subject
			}
			fmt.Fprintf(errOut, "Downloading and converting %s; this takes up to a few minutes...\n", subject)
			announced = true
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(fetchPollInterval):
		}
	}
}

// familyPartsErr reports a split-file family ID ("TS 38.101") as its parts
// listing, the same hint the MCP handlers give. Returns nil when specID is
// not a family.
func familyPartsErr(ctx context.Context, d *db.DB, specID string) error {
	if parts, err := d.FindSpecIDsByFamily(ctx, specID); err == nil && len(parts) > 0 {
		return fmt.Errorf("%s has multiple parts: %s — specify one", specID, strings.Join(parts, ", "))
	}
	return nil
}

// printJSON writes v to out as indented, jq-ready JSON.
func printJSON(out io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}

// list-specs

func cmdListSpecs(args []string) {
	fs := flag.NewFlagSet("list-specs", flag.ExitOnError)
	qf := addQueryFlags(fs, false)
	series := fs.String("series", "", "Filter by series number (e.g. 23)")
	query := fs.String("query", "", "Filter specs whose ID starts with this text (e.g. 38.21)")
	limit := fs.Int("limit", 0, "Maximum number of results (default: 20)")
	offset := fs.Int("offset", 0, "Number of results to skip")
	_ = fs.Parse(args)
	requireArgs(fs, 0, "3gpp-mcp list-specs [options]")

	runQuery("list-specs", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(false)
		if err != nil {
			return err
		}
		defer cleanup()
		return runListSpecs(ctx, os.Stdout, src.DB, *series, *query, *limit, *offset)
	})
}

func runListSpecs(ctx context.Context, out io.Writer, d *db.DB, series, query string, limit, offset int) error {
	// A negative limit means "no limit" inside db.ListSpecs, which is reserved
	// for internal callers.
	if limit < 0 {
		limit = 0
	}
	result, err := d.ListSpecs(ctx, series, query, limit, offset)
	if err != nil {
		return err
	}
	return printJSON(out, result)
}

// list-versions

func cmdListVersions(args []string) {
	fs := flag.NewFlagSet("list-versions", flag.ExitOnError)
	qf := addQueryFlags(fs, true)
	_ = fs.Parse(args)
	requireArgs(fs, 1, "3gpp-mcp list-versions [options] <spec-id>")

	runQuery("list-versions", func(ctx context.Context) error {
		// An existing cache is read so its versions are reported as cached,
		// but a listing must not create one; without the store the listing
		// still covers the database and the archive.
		src, cleanup, err := qf.openSource(qf.versionCacheExists())
		if err != nil {
			return err
		}
		defer cleanup()
		return runListVersions(ctx, os.Stdout, os.Stderr, src, fs.Arg(0))
	})
}

func runListVersions(ctx context.Context, out, errOut io.Writer, src *tools.Source, specID string) error {
	versions, archiveErr, err := src.ListVersions(ctx, specID)
	if err != nil {
		return err
	}
	if len(versions) == 0 {
		if archiveErr != nil {
			return fmt.Errorf("no versions found for %s: %w", specID, archiveErr)
		}
		if err := familyPartsErr(ctx, src.DB, specID); err != nil {
			return err
		}
		return fmt.Errorf("no versions found for %s", specID)
	}
	// The archive listing may have failed while the cache and/or database
	// still produced versions; the warning goes to stderr so stdout stays
	// parseable.
	if archiveErr != nil {
		fmt.Fprintf(errOut, "WARNING: failed to list archive versions for %s, so this list may be incomplete: %v\n", specID, archiveErr)
	}
	return printJSON(out, tools.ListVersionsOutput{SpecID: specID, Versions: versions})
}

// get-toc

func cmdGetTOC(args []string) {
	fs := flag.NewFlagSet("get-toc", flag.ExitOnError)
	qf := addQueryFlags(fs, true)
	version := fs.String("version", "", "Specification version to read (e.g. 18.6.0, i60, Rel-18; default: the database version)")
	_ = fs.Parse(args)
	requireArgs(fs, 1, "3gpp-mcp get-toc [options] <spec-id>")

	runQuery("get-toc", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(*version != "")
		if err != nil {
			return err
		}
		defer cleanup()
		return runGetTOC(ctx, os.Stdout, os.Stderr, src, fs.Arg(0), *version)
	})
}

func runGetTOC(ctx context.Context, out, errOut io.Writer, src *tools.Source, specID, version string) error {
	var sections []db.Section
	err := waitForFetch(ctx, errOut, func() error {
		var err error
		sections, _, err = src.GetTOC(ctx, specID, version)
		return err
	})
	if err != nil {
		return err
	}
	if len(sections) == 0 {
		if err := familyPartsErr(ctx, src.DB, specID); err != nil {
			return err
		}
		return fmt.Errorf("no sections found for %s", specID)
	}
	_, err = io.WriteString(out, tools.RenderTOC(sections))
	return err
}

// get-section

func cmdGetSection(args []string) {
	fs := flag.NewFlagSet("get-section", flag.ExitOnError)
	qf := addQueryFlags(fs, true)
	version := fs.String("version", "", "Specification version to read (e.g. 18.6.0, i60, Rel-18; default: the database version)")
	subsections := fs.Bool("subsections", false, "Include all subsections")
	_ = fs.Parse(args)
	requireArgs(fs, 2, "3gpp-mcp get-section [options] <spec-id> <section-number>")

	runQuery("get-section", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(*version != "")
		if err != nil {
			return err
		}
		defer cleanup()
		return runGetSection(ctx, os.Stdout, os.Stderr, src, fs.Arg(0), fs.Arg(1), *version, *subsections)
	})
}

func runGetSection(ctx context.Context, out, errOut io.Writer, src *tools.Source, specID, sectionNumber, version string, subsections bool) error {
	var sections []db.Section
	var res tools.Resolution
	err := waitForFetch(ctx, errOut, func() error {
		var err error
		sections, res, err = src.GetSection(ctx, specID, version, sectionNumber, subsections)
		return err
	})
	if err != nil {
		return err
	}
	if len(sections) == 0 {
		if err := familyPartsErr(ctx, src.DB, specID); err != nil {
			return err
		}
		return fmt.Errorf("section %s not found in %s", sectionNumber, specID)
	}
	fmt.Fprintln(out, tools.SourceHeader(sections[0], subsections && len(sections) > 1, res.Archived))
	for _, s := range sections {
		fmt.Fprintf(out, "%s\n\n", s.Content)
	}
	return nil
}

// get-asn1

// specIDArg matches a positional argument naming a specification ("TS 38.331",
// "TR 21.905", or a bare "38.331"), which is how get-asn1 tells a per-spec
// call from a corpus-wide name lookup: ASN.1 identifiers start with a letter
// and never take either shape. A bare number still fails the spec lookup —
// spec IDs carry their TS/TR prefix — but as "specification not found", not
// as a baffling name miss.
var specIDArg = regexp.MustCompile(`^((?i)t[sr] ?)?\d+\.\d+`)

func cmdGetASN1(args []string) {
	fs := flag.NewFlagSet("get-asn1", flag.ExitOnError)
	qf := addQueryFlags(fs, true)
	version := fs.String("version", "", "Specification version to read (e.g. 18.6.0, i60, Rel-18; default: the database version; requires a spec-id)")
	_ = fs.Parse(args)
	usage := func() {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp get-asn1 [options] <spec-id> [name]")
		fmt.Fprintln(os.Stderr, "       3gpp-mcp get-asn1 [options] <name>        (looks the name up across every spec)")
		fmt.Fprintln(os.Stderr, "Options must come before positional arguments.")
		os.Exit(1)
	}
	var specID, name string
	switch {
	case fs.NArg() == 1 && specIDArg.MatchString(fs.Arg(0)):
		specID = fs.Arg(0)
	case fs.NArg() == 1:
		name = fs.Arg(0)
	case fs.NArg() == 2:
		specID, name = fs.Arg(0), fs.Arg(1)
	default:
		usage()
	}
	if specID == "" && *version != "" {
		fmt.Fprintln(os.Stderr, "-version requires a spec-id: the cross-spec lookup covers the database versions only.")
		os.Exit(1)
	}

	runQuery("get-asn1", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(*version != "")
		if err != nil {
			return err
		}
		defer cleanup()
		return runGetASN1(ctx, os.Stdout, os.Stderr, src, specID, name, *version)
	})
}

func runGetASN1(ctx context.Context, out, errOut io.Writer, src *tools.Source, specID, name, version string) error {
	if specID == "" {
		key := asn1index.Key(name)
		defs, err := src.DB.LookupASN1(ctx, name, key, "")
		if errors.Is(err, db.ErrNoASN1Index) {
			return fmt.Errorf("%w; run '3gpp-mcp build-asn1-index' to add it, or pass a spec-id", err)
		}
		if err != nil {
			return err
		}
		if len(defs) == 0 {
			msg := fmt.Sprintf("ASN.1 assignment %q not found in any specification in the database", name)
			if suggestions, serr := src.DB.ASN1NameSuggestions(ctx, key, "", 20); serr == nil && len(suggestions) > 0 {
				msg += "; similar names: " + strings.Join(suggestions, ", ")
			}
			return errors.New(msg)
		}
		_, err = io.WriteString(out, tools.RenderASN1Definitions(tools.ASN1DefAssignments(defs), false))
		return err
	}

	// The database version is served from the prebuilt index when it holds
	// the spec; everything else — an explicit version, an index-less
	// database, a spec the index has nothing for — reads the document.
	if version == "" {
		if listing, err := src.DB.ASN1SpecListing(ctx, specID); err == nil && len(listing) > 0 {
			if name == "" {
				_, err := io.WriteString(out, tools.RenderASN1Listing(tools.ASN1DefAssignments(listing)))
				return err
			}
			key := asn1index.Key(name)
			defs, err := src.DB.LookupASN1(ctx, name, key, specID)
			if err != nil {
				return err
			}
			if len(defs) == 0 {
				msg := fmt.Sprintf("ASN.1 assignment %q not found in %s", name, specID)
				if suggestions, serr := src.DB.ASN1NameSuggestions(ctx, key, specID, 20); serr == nil && len(suggestions) > 0 {
					msg += "; similar names: " + strings.Join(suggestions, ", ")
				} else if all, aerr := src.DB.LookupASN1(ctx, name, key, ""); aerr == nil && len(all) > 0 {
					msg += "; it is defined in " + strings.Join(db.ASN1DefSpecs(all), ", ")
				}
				return errors.New(msg)
			}
			_, err = io.WriteString(out, tools.RenderASN1Definitions(tools.ASN1DefAssignments(defs), false))
			return err
		}
	}

	var sections []db.Section
	var res tools.Resolution
	err := waitForFetch(ctx, errOut, func() error {
		var err error
		sections, res, err = src.AllSections(ctx, specID, version)
		return err
	})
	if err != nil {
		return err
	}
	if len(sections) == 0 {
		if err := familyPartsErr(ctx, src.DB, specID); err != nil {
			return err
		}
		return fmt.Errorf("specification %s not found", specID)
	}

	assignments := tools.ExtractASN1(sections)
	if len(assignments) == 0 {
		return fmt.Errorf("%s contains no ASN.1 definitions (no -- ASN1START blocks)", specID)
	}
	if name == "" {
		_, err := io.WriteString(out, tools.RenderASN1Listing(assignments))
		return err
	}
	matches := tools.MatchASN1(assignments, name)
	if len(matches) == 0 {
		msg := fmt.Sprintf("ASN.1 assignment %q not found in %s", name, specID)
		if suggestions := tools.ASN1Suggestions(assignments, name, 20); len(suggestions) > 0 {
			msg += "; similar names: " + strings.Join(suggestions, ", ")
		}
		return errors.New(msg)
	}
	_, err = io.WriteString(out, tools.RenderASN1Definitions(matches, res.Archived))
	return err
}

// compare-versions

func cmdCompareVersions(args []string) {
	fs := flag.NewFlagSet("compare-versions", flag.ExitOnError)
	qf := addQueryFlags(fs, true)
	oldVersion := fs.String("old", "", "Older version to compare from (e.g. 17.9.0, h90, Rel-17; required)")
	newVersion := fs.String("new", "", "Newer version to compare to (default: the database version)")
	section := fs.String("section", "", "Compare only this section's text as a unified diff (default: structural summary)")
	subsections := fs.Bool("subsections", false, "With -section: include subsections in the diff")
	contextLines := fs.Int("context", defaultCLIContextLines, "Unchanged lines shown around each change in a section diff")
	_ = fs.Parse(args)
	requireArgs(fs, 1, "3gpp-mcp compare-versions [options] -old <version> <spec-id>")
	if *oldVersion == "" {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp compare-versions [options] -old <version> <spec-id>")
		fmt.Fprintln(os.Stderr, "-old is required.")
		os.Exit(1)
	}

	runQuery("compare-versions", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(true)
		if err != nil {
			return err
		}
		defer cleanup()
		return runCompareVersions(ctx, os.Stdout, os.Stderr, src, fs.Arg(0), *oldVersion, *newVersion, *section, *subsections, *contextLines)
	})
}

const defaultCLIContextLines = 3

// compareSides reads both versions' sections in one poll round so both
// on-demand fetches run concurrently, mirroring the MCP handler.
func compareSides(ctx context.Context, errOut io.Writer, read func() (oldErr, newErr error)) error {
	var oldSeen, newSeen bool
	return waitForFetch(ctx, errOut, func() error {
		oldErr, newErr := read()
		var ip *tools.FetchInProgressError
		oldFetching, newFetching := errors.As(oldErr, &ip), errors.As(newErr, &ip)
		// A side that had its content and is fetching again lost it to the
		// other side's fetch: the cache cannot hold both versions (a version
		// cache limit of 0 keeps only the newest), so polling on would
		// alternate the two downloads forever.
		if (oldSeen && oldFetching) || (newSeen && newFetching) {
			return fmt.Errorf("the version cache cannot hold both versions at once; raise -version-cache-mb")
		}
		oldSeen = oldSeen || oldErr == nil
		newSeen = newSeen || newErr == nil
		// Report the in-progress side so waitForFetch keeps polling even when
		// the other side already failed for good; the terminal error surfaces
		// once its partner's fetch has finished.
		if oldFetching {
			return oldErr
		}
		if newFetching {
			return newErr
		}
		if oldErr != nil {
			return fmt.Errorf("read old version: %w", oldErr)
		}
		if newErr != nil {
			return fmt.Errorf("read new version: %w", newErr)
		}
		return nil
	})
}

func runCompareVersions(ctx context.Context, out, errOut io.Writer, src *tools.Source, specID, oldVersion, newVersion, section string, subsections bool, contextLines int) error {
	var oldSecs, newSecs []db.Section
	var oldRes, newRes tools.Resolution
	err := compareSides(ctx, errOut, func() (error, error) {
		var oldErr, newErr error
		if section != "" {
			oldSecs, oldRes, oldErr = src.GetSection(ctx, specID, oldVersion, section, subsections)
			newSecs, newRes, newErr = src.GetSection(ctx, specID, newVersion, section, subsections)
		} else {
			oldSecs, oldRes, oldErr = src.AllSections(ctx, specID, oldVersion)
			newSecs, newRes, newErr = src.AllSections(ctx, specID, newVersion)
		}
		return oldErr, newErr
	})
	if err != nil {
		// A family ID like "TS 38.101" never resolves to content of its own;
		// the parts listing is the useful answer, not the resolve error.
		if familyErr := familyPartsErr(ctx, src.DB, specID); familyErr != nil {
			return familyErr
		}
		return err
	}

	if len(oldSecs) == 0 && len(newSecs) == 0 {
		if err := familyPartsErr(ctx, src.DB, specID); err != nil {
			return err
		}
		if section != "" {
			return fmt.Errorf("section %s does not exist in %s in either version; section numbers move between releases — compare without -section, or use get-toc, to locate it", section, specID)
		}
		return fmt.Errorf("no sections found for %s in either version", specID)
	}
	oldV, newV := tools.ResolvedVersion(oldSecs, oldRes), tools.ResolvedVersion(newSecs, newRes)
	if oldV != "" && oldV == newV {
		fmt.Fprintf(out, "%s: -old and -new both resolve to v%s; nothing to compare.\n", specID, oldV)
		return nil
	}
	oldLabel, newLabel := tools.VersionLabel(oldSecs, oldRes), tools.VersionLabel(newSecs, newRes)

	if section == "" {
		fmt.Fprintln(out, tools.CompareHeader(specID, "", oldLabel, newLabel))
		_, err := io.WriteString(out, tools.RenderStructuralSummary(structdiff.Diff(oldSecs, newSecs), oldLabel, newLabel))
		if err == nil {
			_, err = io.WriteString(out, "\n")
		}
		return err
	}

	// A section present on one side only is an informational answer, not a
	// failure: numbers move between releases.
	if len(oldSecs) == 0 || len(newSecs) == 0 {
		missing, present := oldLabel, newLabel
		if len(newSecs) == 0 {
			missing, present = newLabel, oldLabel
		}
		fmt.Fprintf(out, "Section %s of %s does not exist in %s (it exists in %s). Section numbers move between releases — compare without -section, or use get-toc for %s, to locate it.\n",
			section, specID, missing, present, missing)
		return nil
	}

	// Only a negative value falls back to the default: an explicit -context 0
	// is a deliberate request for a diff with no context lines.
	if contextLines < 0 {
		contextLines = defaultCLIContextLines
	}
	fmt.Fprintln(out, tools.CompareHeader(specID, section, oldLabel, newLabel))
	diff := textdiff.UnifiedKeyed(structdiff.SectionLines(oldSecs), structdiff.SectionLines(newSecs), contextLines, structdiff.NormalizeImageRefs)
	if diff == "" {
		fmt.Fprintf(out, "Section %s is identical between %s and %s.\n", section, oldLabel, newLabel)
		return nil
	}
	_, err = io.WriteString(out, diff)
	return err
}

// search

func cmdSearch(args []string) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	qf := addQueryFlags(fs, false)
	specs := fs.String("specs", "", "Limit search to specific specs, comma-separated (e.g. \"TS 23.501,TS 23.502\")")
	limit := fs.Int("limit", 0, "Maximum number of results per page (default: 10, max: 200)")
	offset := fs.Int("offset", 0, "Number of results to skip")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp search [options] <query>")
		fmt.Fprintln(os.Stderr, "Options must come before the query.")
		os.Exit(1)
	}

	runQuery("search", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(false)
		if err != nil {
			return err
		}
		defer cleanup()
		// Joining the remaining arguments lets multi-term FTS queries go
		// unquoted: 3gpp-mcp search AMF AND authentication.
		return runSearch(ctx, os.Stdout, src.DB, strings.Join(fs.Args(), " "), *specs, *limit, *offset)
	})
}

func runSearch(ctx context.Context, out io.Writer, d *db.DB, query, specs string, limit, offset int) error {
	var specIDs []string
	if specs != "" {
		for _, s := range strings.Split(specs, ",") {
			if s = strings.TrimSpace(s); s != "" {
				specIDs = append(specIDs, s)
			}
		}
	}
	if offset < 0 {
		offset = 0
	}
	results, err := d.Search(ctx, query, specIDs, limit, offset)
	if err != nil {
		return err
	}
	return printJSON(out, results)
}

// list-openapi

func cmdListOpenAPI(args []string) {
	fs := flag.NewFlagSet("list-openapi", flag.ExitOnError)
	qf := addQueryFlags(fs, false)
	spec := fs.String("spec", "", "Filter by specification ID (e.g. TS 29.510)")
	_ = fs.Parse(args)
	requireArgs(fs, 0, "3gpp-mcp list-openapi [options]")

	runQuery("list-openapi", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(false)
		if err != nil {
			return err
		}
		defer cleanup()
		return runListOpenAPI(ctx, os.Stdout, src.DB, *spec)
	})
}

func runListOpenAPI(ctx context.Context, out io.Writer, d *db.DB, specID string) error {
	specs, err := d.ListOpenAPI(ctx, specID)
	if err != nil {
		return err
	}
	if specs == nil {
		specs = []db.OpenAPISpec{}
	}
	return printJSON(out, specs)
}

// get-openapi

func cmdGetOpenAPI(args []string) {
	fs := flag.NewFlagSet("get-openapi", flag.ExitOnError)
	qf := addQueryFlags(fs, false)
	path := fs.String("path", "", "Filter by API path (e.g. /nf-instances)")
	schema := fs.String("schema", "", "Filter by schema name (e.g. NFProfile)")
	_ = fs.Parse(args)
	requireArgs(fs, 2, "3gpp-mcp get-openapi [options] <spec-id> <api-name>")

	runQuery("get-openapi", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(false)
		if err != nil {
			return err
		}
		defer cleanup()
		return runGetOpenAPI(ctx, os.Stdout, src.DB, fs.Arg(0), fs.Arg(1), *path, *schema)
	})
}

func runGetOpenAPI(ctx context.Context, out io.Writer, d *db.DB, specID, apiName, path, schema string) error {
	content, err := d.GetOpenAPIResolved(ctx, specID, apiName)
	if errors.Is(err, db.ErrOpenAPINotFound) {
		return errors.New(tools.OpenAPINotFoundMessage(ctx, d, specID, apiName, schema))
	}
	if err != nil {
		return err
	}
	if path != "" || schema != "" {
		if content, err = tools.FilterOpenAPI(content, path, schema); err != nil {
			return err
		}
	}
	_, err = io.WriteString(out, content)
	return err
}

// search-openapi

func cmdSearchOpenAPI(args []string) {
	fs := flag.NewFlagSet("search-openapi", flag.ExitOnError)
	qf := addQueryFlags(fs, false)
	specs := fs.String("specs", "", "Limit search to specific specs, comma-separated (e.g. \"TS 29.510,TS 29.518\")")
	api := fs.String("api", "", "Limit search to a single API document (e.g. Nnrf_NFManagement)")
	kind := fs.String("kind", "", "Limit search to one kind of definition: schema or operation")
	body := fs.Bool("body", false, "Include the full text of each matching definition")
	limit := fs.Int("limit", 0, "Maximum number of results per page (default: 10, max: 200)")
	offset := fs.Int("offset", 0, "Number of results to skip")
	_ = fs.Parse(args)
	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp search-openapi [options] <query>")
		fmt.Fprintln(os.Stderr, "Options must come before the query.")
		os.Exit(1)
	}

	runQuery("search-openapi", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(false)
		if err != nil {
			return err
		}
		defer cleanup()
		// Joining the remaining arguments lets multi-term FTS queries go
		// unquoted, as in search.
		return runSearchOpenAPI(ctx, os.Stdout, src.DB, strings.Join(fs.Args(), " "), *specs, *api, *kind, *body, *limit, *offset)
	})
}

func runSearchOpenAPI(ctx context.Context, out io.Writer, d *db.DB, query, specs, api, kind string, includeBody bool, limit, offset int) error {
	var specIDs []string
	if specs != "" {
		for _, s := range strings.Split(specs, ",") {
			if s = strings.TrimSpace(s); s != "" {
				specIDs = append(specIDs, s)
			}
		}
	}
	if offset < 0 {
		offset = 0
	}
	results, err := d.SearchOpenAPI(ctx, query, specIDs, api, kind, includeBody, limit, offset)
	if err != nil {
		if errors.Is(err, db.ErrNoOpenAPIIndex) {
			return fmt.Errorf("%w; run '3gpp-mcp build-openapi-index' to add it", err)
		}
		return err
	}
	return printJSON(out, results)
}

// get-references

func cmdGetReferences(args []string) {
	fs := flag.NewFlagSet("get-references", flag.ExitOnError)
	qf := addQueryFlags(fs, false)
	direction := fs.String("direction", db.DirectionOutgoing, "outgoing: references FROM a section; incoming: references TO this spec")
	subsections := fs.Bool("subsections", false, "Include subsections when collecting outgoing references")
	_ = fs.Parse(args)
	if fs.NArg() != 1 && fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "Usage: 3gpp-mcp get-references [options] <spec-id> [section-number]")
		fmt.Fprintln(os.Stderr, "Options must come before positional arguments.")
		os.Exit(1)
	}

	runQuery("get-references", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(false)
		if err != nil {
			return err
		}
		defer cleanup()
		return runGetReferences(ctx, os.Stdout, src.DB, fs.Arg(0), fs.Arg(1), *direction, *subsections)
	})
}

func runGetReferences(ctx context.Context, out io.Writer, d *db.DB, specID, sectionNumber, direction string, subsections bool) error {
	if direction == db.DirectionOutgoing && sectionNumber == "" {
		return fmt.Errorf("a section number is required for outgoing direction")
	}
	// Unlike the MCP tool, the full result is printed: the 500-row cap there
	// protects an LLM context window, which a shell pipeline does not have.
	refs, err := d.GetReferences(ctx, specID, "", sectionNumber, direction, subsections)
	if err != nil {
		return err
	}
	if refs == nil {
		refs = []db.Reference{}
	}
	return printJSON(out, refs)
}

// list-images

func cmdListImages(args []string) {
	fs := flag.NewFlagSet("list-images", flag.ExitOnError)
	qf := addQueryFlags(fs, true)
	version := fs.String("version", "", "Specification version to read (e.g. 18.6.0, i60, Rel-18; default: the database version)")
	_ = fs.Parse(args)
	requireArgs(fs, 1, "3gpp-mcp list-images [options] <spec-id>")

	runQuery("list-images", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(*version != "")
		if err != nil {
			return err
		}
		defer cleanup()
		return runListImages(ctx, os.Stdout, os.Stderr, src, fs.Arg(0), *version)
	})
}

func runListImages(ctx context.Context, out, errOut io.Writer, src *tools.Source, specID, version string) error {
	var images []db.ImageInfo
	var res tools.Resolution
	err := waitForFetch(ctx, errOut, func() error {
		var err error
		images, res, err = src.ListImages(ctx, specID, version)
		return err
	})
	if err != nil {
		return err
	}
	if len(images) == 0 && !res.Archived {
		if err := familyPartsErr(ctx, src.DB, specID); err != nil {
			return err
		}
	}
	if images == nil {
		images = []db.ImageInfo{}
	}
	return printJSON(out, struct {
		Images []db.ImageInfo `json:"images"`
		Count  int            `json:"count"`
	}{Images: images, Count: len(images)})
}

// get-image

func cmdGetImage(args []string) {
	fs := flag.NewFlagSet("get-image", flag.ExitOnError)
	qf := addQueryFlags(fs, true)
	version := fs.String("version", "", "Specification version to read (e.g. 18.6.0, i60, Rel-18; default: the database version)")
	output := fs.String("o", "", "Write the image to this file (default: stdout)")
	_ = fs.Parse(args)
	requireArgs(fs, 2, "3gpp-mcp get-image [options] <spec-id> <image-name>")

	runQuery("get-image", func(ctx context.Context) error {
		src, cleanup, err := qf.openSource(*version != "")
		if err != nil {
			return err
		}
		defer cleanup()
		return runGetImage(ctx, os.Stdout, os.Stderr, src, fs.Arg(0), fs.Arg(1), *version, *output)
	})
}

func runGetImage(ctx context.Context, out, errOut io.Writer, src *tools.Source, specID, name, version, output string) error {
	var img *db.Image
	var res tools.Resolution
	err := waitForFetch(ctx, errOut, func() error {
		var err error
		// A missing image comes back as a nil image, not an error, so an error
		// here is a real failure (bad version, failed download, database
		// trouble) and must not be labeled "not found".
		img, res, err = src.GetImage(ctx, specID, version, name)
		return err
	})
	if err != nil {
		return err
	}
	if img == nil {
		return fmt.Errorf("image %q not found in %s", name, specID)
	}
	// Unlike the MCP tool, an EMF/WMF image is still written out — a shell
	// user can open it — with a note about the conversion option. An archived
	// version's images are converted at fetch time, not at build time, so
	// rebuilding the database would not help there.
	if !img.LLMReadable {
		hint := "rebuild with --convert-image to store PNG instead."
		if res.Archived {
			hint = "converting it needs LibreOffice (soffice), which was missing or failed when this version's images were fetched."
		}
		fmt.Fprintf(errOut, "NOTE: %s is in %s format; %s\n", img.Name, img.MIMEType, hint)
	}
	fmt.Fprintf(errOut, "%s (%s, %d bytes)\n", img.Name, img.MIMEType, len(img.Data))
	if output != "" {
		if err := os.WriteFile(output, img.Data, 0o644); err != nil {
			return fmt.Errorf("write image: %w", err)
		}
		return nil
	}
	if _, err := out.Write(img.Data); err != nil {
		return fmt.Errorf("write image: %w", err)
	}
	return nil
}
