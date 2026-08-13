package pipeline

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultMaxZipSizeMB = 512

// sofficeTimeout bounds a single LibreOffice invocation; headless conversions
// occasionally hang forever (e.g. a stuck first-run profile setup).
const sofficeTimeout = 5 * time.Minute

// maxZipSize is the upper limit for ZIP downloads. Configurable via THREEGPP_MAX_ZIP_SIZE_MB.
var maxZipSize = int64(defaultMaxZipSizeMB) << 20

func init() {
	if v := os.Getenv("THREEGPP_MAX_ZIP_SIZE_MB"); v != "" {
		// The upper bound keeps the <<20 shift from overflowing int64 into a
		// negative limit that would reject every download.
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil && mb > 0 && mb <= (1<<63-1)>>20 {
			maxZipSize = mb << 20
		}
	}
}

// DownloadResult holds the result of a download operation.
type DownloadResult struct {
	SpecID    string
	Status    string // "OK", "DOC_ONLY", "NO_DOC", "FAILED"
	DocxFiles []string
	YAMLFiles []string
	// DocFiles holds the extracted .doc file paths when Status is DOC_ONLY,
	// so callers that later run LibreOffice conversion on a shared directory
	// can tell which converted .docx (if any) belongs to this spec.
	DocFiles []string
}

// DownloadAndExtract downloads a spec zip and extracts .docx files to a temp directory.
func DownloadAndExtract(ctx context.Context, client *http.Client, spec *SpecVersion, outputDir string, timeout time.Duration) (*DownloadResult, error) {
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	result := &DownloadResult{SpecID: spec.SpecID}

	// backoff sleeps before the next retry, or reports false immediately
	// when ctx is cancelled: retrying a cancelled download cannot succeed,
	// and each worker sitting out its full backoff would stall shutdown.
	backoff := func(attempt int) bool {
		t := time.NewTimer(time.Duration(1<<uint(attempt+1)) * time.Second)
		defer t.Stop()
		select {
		case <-t.C:
			return true
		case <-ctx.Done():
			return false
		}
	}

	var lastErr error
	for attempt := range 3 {
		data, err := downloadZip(ctx, client, spec.URL)
		if err != nil {
			lastErr = err
			if attempt < 2 && backoff(attempt) {
				continue
			}
			result.Status = "FAILED"
			return result, fmt.Errorf("download failed after %d attempt(s): %w", attempt+1, lastErr)
		}

		r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			lastErr = err
			if attempt < 2 && backoff(attempt) {
				continue
			}
			result.Status = "FAILED"
			return result, fmt.Errorf("bad zip after %d attempt(s): %w", attempt+1, lastErr)
		}

		if err := os.MkdirAll(outputDir, 0o700); err != nil {
			result.Status = "FAILED"
			return result, err
		}

		var docxFiles, docFiles, yamlFiles []string
		for _, f := range r.File {
			name := filepath.Base(f.Name)
			lower := strings.ToLower(name)
			if strings.HasPrefix(name, ".") {
				continue
			}
			if strings.HasSuffix(lower, ".docx") {
				docxFiles = append(docxFiles, name)
			} else if strings.HasSuffix(lower, ".doc") {
				docFiles = append(docFiles, name)
			} else if strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") {
				yamlFiles = append(yamlFiles, name)
			}
		}

		// Extract YAML files
		if len(yamlFiles) > 0 {
			yamlDir := filepath.Join(outputDir, "_yaml")
			if err := os.MkdirAll(yamlDir, 0o700); err != nil {
				log.Printf("  %s: failed to create yaml dir: %v", spec.SpecID, err)
			} else {
				for _, f := range r.File {
					name := filepath.Base(f.Name)
					lower := strings.ToLower(name)
					if strings.HasSuffix(lower, ".yaml") || strings.HasSuffix(lower, ".yml") {
						outPath := filepath.Join(yamlDir, name)
						if err := extractFile(f, outPath); err != nil {
							log.Printf("  %s: failed to extract %s: %v", spec.SpecID, name, err)
						} else {
							result.YAMLFiles = append(result.YAMLFiles, outPath)
						}
					}
				}
			}
		}

		if len(docxFiles) > 0 {
			for _, f := range r.File {
				name := filepath.Base(f.Name)
				if strings.HasSuffix(strings.ToLower(name), ".docx") && !strings.HasPrefix(name, ".") {
					outPath := filepath.Join(outputDir, name)
					if err := extractFile(f, outPath); err != nil {
						log.Printf("  %s: failed to extract %s: %v", spec.SpecID, name, err)
					} else {
						result.DocxFiles = append(result.DocxFiles, outPath)
					}
				}
			}
			// The archive listed .docx entries but none of them could be
			// written out (disk full, permission denied, corrupt member),
			// leaving callers with nothing to parse. A partial extraction
			// still counts as OK; an empty one must not.
			if len(result.DocxFiles) == 0 {
				result.Status = "FAILED"
				return result, fmt.Errorf("extracted none of the %d .docx file(s) in the archive", len(docxFiles))
			}
			result.Status = "OK"
			return result, nil
		}

		if len(docFiles) > 0 {
			docDir := filepath.Join(outputDir, "_doc_files")
			if err := os.MkdirAll(docDir, 0o700); err != nil {
				log.Printf("  %s: failed to create doc dir: %v", spec.SpecID, err)
			}
			var extracted []string
			for _, f := range r.File {
				name := filepath.Base(f.Name)
				if strings.HasSuffix(strings.ToLower(name), ".doc") && !strings.HasPrefix(name, ".") {
					outPath := filepath.Join(docDir, name)
					if err := extractFile(f, outPath); err != nil {
						log.Printf("  %s: failed to extract %s: %v", spec.SpecID, name, err)
					} else {
						extracted = append(extracted, outPath)
					}
				}
			}
			// As with the .docx branch above, the archive listing .doc
			// entries does not mean any of them actually landed on disk.
			// Reporting DOC_ONLY here would tell the caller "install
			// LibreOffice and retry" for a spec that has nothing to convert.
			if len(extracted) == 0 {
				result.Status = "FAILED"
				return result, fmt.Errorf("extracted none of the %d .doc file(s) in the archive", len(docFiles))
			}
			result.Status = "DOC_ONLY"
			result.DocFiles = extracted
			return result, nil
		}

		result.Status = "NO_DOC"
		return result, nil
	}

	result.Status = "FAILED"
	return result, lastErr
}

// ConvertDocFiles converts .doc files to .docx using LibreOffice headless.
// Each invocation uses a unique LibreOffice user profile to allow concurrent execution.
func ConvertDocFiles(ctx context.Context, docDir, outputDir string) (int, error) {
	entries, err := os.ReadDir(docDir)
	if err != nil {
		return 0, err
	}

	converted := 0
	failed := 0
	for _, entry := range entries {
		if ctx.Err() != nil {
			return converted, ctx.Err()
		}
		lower := strings.ToLower(entry.Name())
		if !strings.HasSuffix(lower, ".doc") || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		inPath := filepath.Join(docDir, entry.Name())
		profileDir, err := os.MkdirTemp("", "lo-profile-")
		if err != nil {
			log.Printf("  failed to create temp profile dir: %v", err)
			failed++
			continue
		}
		// LibreOffice occasionally hangs indefinitely in headless mode, so
		// each invocation gets its own deadline. WaitDelay covers the case
		// where a leftover child process keeps the output pipe open after
		// the wrapper is killed, which would otherwise block CombinedOutput
		// past the context cancellation.
		convertCtx, cancel := context.WithTimeout(ctx, sofficeTimeout)
		cmd := exec.CommandContext(convertCtx, "libreoffice",
			"--headless",
			"-env:UserInstallation=file://"+profileDir,
			"--convert-to", "docx",
			"--outdir", outputDir,
			inPath,
		)
		cmd.WaitDelay = 10 * time.Second
		out, err := cmd.CombinedOutput()
		cancel()
		_ = os.RemoveAll(profileDir)
		if err != nil {
			log.Printf("  libreoffice convert %s: %v\n%s", entry.Name(), err, string(out))
			failed++
			continue
		}
		converted++
	}
	if failed > 0 {
		return converted, fmt.Errorf("%d of %d .doc files failed conversion", failed, converted+failed)
	}
	return converted, nil
}

// DownloadSpecs downloads specs in parallel to the output directory (no conversion).
func DownloadSpecs(ctx context.Context, client *http.Client, specs []*SpecVersion, outputDir string, parallel int, convertDoc bool, timeout time.Duration) map[string]int {
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}
	if parallel <= 0 {
		parallel = defaultConcurrency()
	}

	stats := map[string]int{"OK": 0, "DOC_ONLY": 0, "NO_DOC": 0, "FAILED": 0}
	total := len(specs)

	sem := make(chan struct{}, parallel)
	type result struct {
		specID    string
		status    string
		docFiles  []string
		docxFiles []string
	}
	results := make(chan result, total)
	var wg sync.WaitGroup

	for _, spec := range specs {
		spec := spec
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			r, err := DownloadAndExtract(ctx, client, spec, outputDir, timeout)
			if err != nil {
				log.Printf("  %s: download error: %v", spec.SpecID, err)
			}
			status := "FAILED"
			var docFiles, docxFiles []string
			if r != nil {
				status = r.Status
				docFiles = r.DocFiles
				docxFiles = r.DocxFiles
			}
			results <- result{spec.SpecID, status, docFiles, docxFiles}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// docFilesBySpec remembers, per DOC_ONLY spec, which .doc files were
	// extracted for it into the shared docDir below, so a later conversion
	// pass can attribute success or failure back to the right spec.
	docFilesBySpec := make(map[string][]string)
	// directDocxBasenames is the set of .docx basenames this run extracted
	// straight into outputDir. The publish step below uses it to tell a
	// destination another spec just claimed (collision case 2) apart from one
	// left over by an earlier invocation (case 5).
	directDocxBasenames := make(map[string]bool)
	i := 0
	for r := range results {
		i++
		stats[r.status]++
		log.Printf("[%d/%d] %s: %s", i, total, r.specID, r.status)
		if r.status == "DOC_ONLY" && len(r.docFiles) > 0 {
			docFilesBySpec[r.specID] = r.docFiles
		}
		for _, p := range r.docxFiles {
			directDocxBasenames[filepath.Base(p)] = true
		}
	}

	// _doc_files (holding every DOC_ONLY spec's extracted .doc files) and
	// outputDir (holding every direct .docx and every published conversion
	// result) are both flat and shared across the whole batch, and
	// outputDir persists across separate invocations of this command too.
	// That flatness makes a basename collision possible in several distinct
	// shapes; each is called out below with how it's handled or why it
	// doesn't need to be, since review has surfaced a new one on each of
	// several passes and the reasoning needs to live somewhere durable:
	//
	//  1. direct .docx x direct .docx (two different specs' archives each
	//     already ship a .docx with the same basename): NOT specially
	//     handled. DownloadAndExtract writes straight into outputDir with a
	//     plain overwrite, same as it always has -- including on a second
	//     invocation of this command, where overwriting previously
	//     downloaded specs to refresh them is the intended behavior.
	//     Guarding this would either break that refresh or require
	//     namespacing outputDir per spec, both bigger changes than this
	//     collision-handling pass is scoped to. It also cannot occur in
	//     practice: every 3GPP archive filename is prefixed with that
	//     spec's own series+number (e.g. "23501-i30.docx"), so two
	//     *different* specs never produce the same basename.
	//  2. direct .docx x converted .doc->.docx (one spec's archive ships a
	//     .docx directly; a different DOC_ONLY spec's .doc converts to that
	//     same basename): handled by the no-clobber publish below -- a
	//     converted file is never allowed to overwrite a file another spec
	//     extracted this run. The spec that lost the race stays DOC_ONLY
	//     with a logged warning instead of destroying the other spec's real
	//     output.
	//  3. converted .doc x converted .doc, different specs (two DOC_ONLY
	//     specs' .doc files share a basename): handled below via
	//     unattributable -- both extract into the same _doc_files path, so
	//     only one spec's content survives extraction to be converted, and
	//     an existence check on the result can't tell whose it was. Neither
	//     spec is promoted.
	//  4. duplicate basenames within one spec's OWN archive (e.g. two zip
	//     entries under different subfolders that both flatten to the same
	//     name): handled by deduplicating each spec's own doc-file list
	//     before counting claimants below, so a spec doesn't collide with
	//     itself and get wrongly excluded as "shared with another spec".
	//     Only one of the duplicate contents survives extraction (same
	//     flat-directory constraint as case 3), but that's the existing
	//     "partial success still counts as OK" rule already used elsewhere
	//     in this file, not a new attribution risk: the survivor still
	//     definitely belongs to this spec, since no other spec claims it.
	//  5. this run's converted output vs. a stale file already sitting at
	//     the same outputDir path (a leftover from an earlier invocation,
	//     since outputDir is never cleared between runs): the leftover is
	//     replaced, the same refresh-on-rerun behaviour as case 1. By the
	//     naming argument in case 1 it can only be this spec's own earlier
	//     output, and refusing to publish over it would leave every repeat
	//     run reporting the spec DOC_ONLY forever (see the publish step).
	//
	// docPathClaimants maps each shared docDir path to the set of distinct
	// DOC_ONLY specs that recorded it (case 3), after deduplicating each
	// spec's own list first (case 4) so a spec's repeated claim on its own
	// path never inflates the count past 1.
	docPathClaimants := make(map[string]map[string]bool)
	for specID, docFiles := range docFilesBySpec {
		claimedByThisSpec := make(map[string]bool)
		for _, docPath := range docFiles {
			if claimedByThisSpec[docPath] {
				continue
			}
			claimedByThisSpec[docPath] = true
			if docPathClaimants[docPath] == nil {
				docPathClaimants[docPath] = make(map[string]bool)
			}
			docPathClaimants[docPath][specID] = true
		}
	}
	// unattributableBasenames is the set of converted .docx basenames whose
	// source .doc path is unattributable: not just excluded from
	// promotion, but never even published into outputDir. The publish loop
	// below can't tell which spec's content the surviving .doc physically
	// held (see case 3), so any file it converts to is an ownerless
	// .docx that no spec should be credited for -- and its content may
	// itself be a jumble of a racy write to a path two specs both
	// extracted into. Skipping the publish, not just the promotion, keeps
	// that ambiguous file out of outputDir entirely rather than leaving it
	// there unowned.
	unattributable := make(map[string]bool)
	unattributableBasenames := make(map[string]bool)
	for docPath, specIDSet := range docPathClaimants {
		if len(specIDSet) <= 1 {
			continue
		}
		unattributable[docPath] = true
		unattributableBasenames[filepath.Base(convertedDocxPath(docPath, ""))] = true
		specIDs := make([]string, 0, len(specIDSet))
		for specID := range specIDSet {
			specIDs = append(specIDs, specID)
		}
		sort.Strings(specIDs)
		log.Printf("  warning: %s share a same-named .doc file (%s); none will be promoted from it", strings.Join(specIDs, ", "), filepath.Base(docPath))
	}

	if convertDoc {
		docDir := filepath.Join(outputDir, "_doc_files")
		if entries, err := os.ReadDir(docDir); err == nil && len(entries) > 0 {
			// Convert into a fresh, run-scoped scratch directory instead of
			// outputDir directly. outputDir is the caller's persistent
			// --output-dir (never cleared between runs, see cmdDownload)
			// and can already hold a .docx at a spec's expected output path
			// before conversion even starts this time — either a stale
			// leftover from an earlier invocation, or, within this very
			// run, another spec's own .docx that its download already
			// extracted directly into outputDir (every spec's download and
			// extraction completes before this block runs). A wall-clock
			// "written since conversion started" check was tried and
			// discarded: under load, this environment's file mtimes were
			// observed landing before the in-process marker that preceded
			// the write, so mtime cannot be trusted here either. Converting
			// into an empty directory sidesteps all of that: any file
			// appearing in it is unambiguously this run's output, no
			// timing assumptions required. The results are moved into
			// outputDir afterward to keep the flat layout other commands
			// (e.g. import-dir) expect.
			convDir, mkErr := os.MkdirTemp(outputDir, ".doc-convert-*")
			if mkErr != nil {
				log.Printf("doc conversion scratch dir error: %v", mkErr)
			} else {
				log.Println("Converting .doc files to .docx...")
				n, err := ConvertDocFiles(ctx, docDir, convDir)
				if err != nil {
					log.Printf("ConvertDocFiles error: %v", err)
				}
				log.Printf("Converted %d files", n)

				// Publish every converted file into outputDir before
				// deciding any promotion, and track which basenames
				// actually made it there. Deciding OK from mere existence
				// in convDir (as an earlier version of this fix did) could
				// report a spec OK and then have the evidence vanish: if
				// publishing failed for that file, the .docx never reached
				// outputDir, yet the scratch-dir cleanup at the end of this
				// block would delete the only copy that had ever existed,
				// leaving the reported OK status orphaned from any real
				// file. Only a file that is confirmed published counts as
				// evidence a spec's conversion succeeded.
				//
				// Publishing tries os.Link (not os.Rename) first so it
				// fails closed on an existing destination instead of
				// silently overwriting it (collision case 2 in the survey
				// above): another spec's own .docx may already be sitting
				// at that path from this very run. Since convDir is a
				// subdirectory of outputDir (same filesystem), the hard
				// link always succeeds or fails atomically; there is no
				// separate existence check to race against.
				//
				// An occupied destination that this run did not itself
				// write is a stale leftover from an earlier invocation
				// (case 5), and there it is replaced rather than refused.
				// Refusing would make every repeat run of this command a
				// no-op for .doc specs: the first run leaves its converted
				// .docx in outputDir, so every later run hits EEXIST,
				// reports the spec DOC_ONLY even though its output is
				// sitting right there, and throws the fresh conversion
				// away. Such a leftover can only be this spec's own earlier
				// output -- archive filenames carry the spec's own
				// series+number, so no other spec produces the basename --
				// which makes replacing it the same refresh-on-rerun
				// behaviour direct .docx downloads already have (case 1).
				//
				// keepScratch records that convDir still holds a converted
				// file which reached neither outputDir nor a deliberate
				// skip. The cleanup below must not delete those: they are
				// the only copy in existence.
				keepScratch := false
				published := make(map[string]bool)
				convEntries, readErr := os.ReadDir(convDir)
				if readErr != nil {
					// Nothing can be published or attributed, and whatever
					// was converted is still in there unread.
					log.Printf("  read doc conversion scratch dir %s: %v", convDir, readErr)
					keepScratch = true
				}
				for _, e := range convEntries {
					if unattributableBasenames[e.Name()] {
						log.Printf("  publish converted %s: skipped, ambiguous ownership (shared .doc basename, see warning above)", e.Name())
						continue
					}
					src := filepath.Join(convDir, e.Name())
					dst := filepath.Join(outputDir, e.Name())
					err := os.Link(src, dst)
					if os.IsExist(err) {
						if directDocxBasenames[e.Name()] {
							log.Printf("  publish converted %s: skipped, another spec's own .docx already occupies that path", e.Name())
							continue
						}
						log.Printf("  publish converted %s: replacing stale output from an earlier run", e.Name())
						err = os.Rename(src, dst)
					}
					if err != nil {
						log.Printf("  publish converted %s: %v", e.Name(), err)
						keepScratch = true
						continue
					}
					published[e.Name()] = true
				}

				// A spec is promoted from DOC_ONLY to OK only when one of
				// its own .doc files was both converted and successfully
				// published into outputDir this run (partial success still
				// counts, matching the .docx branch above). The old code
				// used min(convertedFileCount, docOnlySpecCount), which
				// conflates a file count with a spec count: a spec with
				// several .doc files could absorb the whole converted-file
				// budget and drag an unrelated, fully-failed spec's status
				// up to OK along with it.
				//
				// consumed collects the source .doc files whose conversion
				// did reach outputDir, so they can be dropped from the
				// shared docDir. Nothing else ever clears that directory,
				// and it is converted wholesale on every run: leaving a
				// finished .doc behind means every later invocation feeds
				// it through LibreOffice again (serially, one process per
				// file) only to discard the result.
				var consumed []string
				for _, docFiles := range docFilesBySpec {
					promoted := false
					for _, docPath := range docFiles {
						if unattributable[docPath] {
							continue
						}
						base := filepath.Base(convertedDocxPath(docPath, ""))
						if !published[base] {
							continue
						}
						consumed = append(consumed, docPath)
						if !promoted {
							promoted = true
							stats["DOC_ONLY"]--
							stats["OK"]++
						}
					}
				}
				for _, docPath := range consumed {
					// Duplicate basenames within one spec's archive (case 4)
					// resolve to the same extracted path, so a repeat
					// removal is expected, not an error.
					if err := os.Remove(docPath); err != nil && !os.IsNotExist(err) {
						log.Printf("  remove converted source %s: %v", filepath.Base(docPath), err)
					}
				}

				if keepScratch {
					log.Printf("  doc conversion scratch dir kept at %s: it holds converted file(s) that could not be published", convDir)
				} else if err := os.RemoveAll(convDir); err != nil {
					log.Printf("  cleanup doc conversion scratch dir: %v", err)
				}
			}
		}
	}

	return stats
}

// convertedDocxPath returns the .docx path ConvertDocFiles would produce in
// outputDir for a .doc file extracted at docPath: LibreOffice's --convert-to
// keeps the input's basename and swaps the extension.
func convertedDocxPath(docPath, outputDir string) string {
	base := filepath.Base(docPath)
	if ext := filepath.Ext(base); strings.EqualFold(ext, ".doc") {
		base = base[:len(base)-len(ext)]
	}
	return filepath.Join(outputDir, base+".docx")
}

func downloadZip(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	// Use a client without an overall timeout so that large ZIP files (e.g.
	// TS 26.274 ~258 MB, TS 26.258 ~170 MB) are not cut off mid-download.
	// The transport's dial/TLS timeouts still apply for connection establishment.
	transport := http.DefaultTransport
	if client != nil && client.Transport != nil {
		transport = client.Transport
	}
	noTimeoutClient := &http.Client{Transport: transport}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "3gpp-converter/1.0")

	resp, err := noTimeoutClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	// Check Content-Length header for early rejection.
	if resp.ContentLength > maxZipSize {
		return nil, fmt.Errorf("zip file too large: %d bytes (max %d MB)", resp.ContentLength, maxZipSize>>20)
	}

	// Read up to maxZipSize bytes.
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxZipSize))
	if err != nil {
		return nil, err
	}

	// Detect truncation: if we can read one more byte, the file exceeded the limit.
	var extra [1]byte
	if n, _ := resp.Body.Read(extra[:]); n > 0 {
		return nil, fmt.Errorf("zip file exceeds maximum size of %d MB", maxZipSize>>20)
	}

	return data, nil
}

func extractFile(f *zip.File, outPath string) error {
	// Defense-in-depth: reject zip entries with path traversal components.
	if strings.Contains(f.Name, "..") {
		return fmt.Errorf("suspicious zip entry name: %s", f.Name)
	}

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer out.Close() // safety net; explicit Close below handles errors
	// Limit extraction size to prevent decompression bombs. Reading one byte
	// past the limit distinguishes a file of exactly maxZipSize (valid) from
	// a truncated larger one.
	n, err := io.Copy(out, io.LimitReader(rc, maxZipSize+1))
	if closeErr := out.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(outPath)
		return err
	}
	if n > maxZipSize {
		_ = os.Remove(outPath)
		return fmt.Errorf("extracted file %s exceeds maximum size of %d MB", f.Name, maxZipSize>>20)
	}
	return nil
}
