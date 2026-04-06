package pipeline

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/higebu/3gpp-mcp/converter/docx"
	"github.com/higebu/3gpp-mcp/db"
)

// docxVersionRE matches 3GPP docx filenames like "21900-j10.docx" to extract the version letter.
var docxVersionRE = regexp.MustCompile(`(?i).+-([a-z])\d+\.docx$`)

// releaseFromDocxFilename extracts the release number from a 3GPP docx filename.
// Returns 0 if the filename does not match the expected pattern.
func releaseFromDocxFilename(filename string) int {
	if m := docxVersionRE.FindStringSubmatch(filename); m != nil {
		return letterToRelease(m[1])
	}
	return 0
}

var (
	yamlFilenameRE = regexp.MustCompile(`^TS(\d{2})(\d{3})_(.+)\.ya?ml$`)
	yamlVersionRE  = regexp.MustCompile(`(?m)^\s+version:\s*['"]?([^'"\\n]+)`)
)

// Pipeline orchestrates the download-convert-store workflow.
type Pipeline struct {
	DB           *db.DB
	Client       *http.Client
	Workers      int
	ConvertDoc   bool
	ConvertImage bool
	Timeout      time.Duration
}

// Run processes a list of specs: download, convert, store in DB, and delete temp files.
func (p *Pipeline) Run(ctx context.Context, specs []*SpecVersion) error {
	if p.Workers <= 0 {
		p.Workers = runtime.NumCPU()
	}
	if p.Timeout == 0 {
		p.Timeout = 30 * time.Second
	}
	if p.Client == nil {
		p.Client = &http.Client{Timeout: p.Timeout}
	}

	sem := make(chan struct{}, p.Workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	stats := map[string]int{"OK": 0, "DOC_ONLY": 0, "NO_DOC": 0, "FAILED": 0}
	var docOnlySpecs []string
	total := len(specs)

	for i, spec := range specs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		spec := spec
		idx := i + 1
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				mu.Lock()
				stats["FAILED"]++
				mu.Unlock()
				return
			}
			defer func() { <-sem }()

			status, err := p.processOne(ctx, spec)
			if err != nil {
				log.Printf("[%d/%d] %s: FAILED (%v)", idx, total, spec.SpecID, err)
			} else if status == "DOC_ONLY" && !p.ConvertDoc {
				log.Printf("[%d/%d] %s: DOC_ONLY (use --convert-doc to index)", idx, total, spec.SpecID)
			} else if status == "DOC_ONLY" && p.ConvertDoc {
				log.Printf("[%d/%d] %s: DOC_ONLY (conversion failed)", idx, total, spec.SpecID)
			} else {
				log.Printf("[%d/%d] %s: %s", idx, total, spec.SpecID, status)
			}

			mu.Lock()
			stats[status]++
			if status == "DOC_ONLY" {
				docOnlySpecs = append(docOnlySpecs, spec.SpecID)
			}
			mu.Unlock()
		}()
	}

	wg.Wait()

	log.Println("Pipeline complete:")
	for status, count := range stats {
		if count > 0 {
			log.Printf("  %s: %d", status, count)
		}
	}

	if len(docOnlySpecs) > 0 && !p.ConvertDoc {
		sort.Strings(docOnlySpecs)
		log.Printf("WARNING: %d spec(s) were skipped because they only contain .doc files.", len(docOnlySpecs))
		log.Println("Re-run with --convert-doc (requires LibreOffice) to index these:")
		for _, id := range docOnlySpecs {
			log.Printf("  - %s", id)
		}
	}

	return nil
}

// processOne downloads, converts, stores, and cleans up a single spec.
func (p *Pipeline) processOne(ctx context.Context, spec *SpecVersion) (string, error) {
	// Create temp directory for this spec
	tmpDir, err := os.MkdirTemp("", "3gpp-"+spec.SpecID+"-")
	if err != nil {
		return "FAILED", err
	}
	defer os.RemoveAll(tmpDir)

	// Download and extract
	result, err := DownloadAndExtract(ctx, p.Client, spec, tmpDir, p.Timeout)
	if err != nil {
		return "FAILED", err
	}
	if result.Status != "OK" {
		if result.Status == "DOC_ONLY" && p.ConvertDoc {
			docDir := filepath.Join(tmpDir, "_doc_files")
			n, err := ConvertDocFiles(ctx, docDir, tmpDir)
			if err != nil {
				log.Printf("  %s: doc conversion failed: %v", spec.SpecID, err)
			}
			if n > 0 {
				// Re-scan for docx files
				entries, readErr := os.ReadDir(tmpDir)
				if readErr != nil {
					return "FAILED", fmt.Errorf("read converted files: %w", readErr)
				}
				result.DocxFiles = nil
				for _, e := range entries {
					if strings.HasSuffix(strings.ToLower(e.Name()), ".docx") && !strings.HasPrefix(e.Name(), ".") {
						result.DocxFiles = append(result.DocxFiles, filepath.Join(tmpDir, e.Name()))
					}
				}
				if len(result.DocxFiles) > 0 {
					result.Status = "OK"
				}
			} else if err == nil {
				log.Printf("  %s: no .doc files were converted (is LibreOffice installed?)", spec.SpecID)
			}
		}
		if result.Status != "OK" {
			return result.Status, nil
		}
	}

	// Sort files with cover files last
	sortCoverLast(result.DocxFiles)

	// Parse and store each docx file
	var parsed int
	for _, docxPath := range result.DocxFiles {
		if ctx.Err() != nil {
			return "FAILED", ctx.Err()
		}
		parseResult, err := docx.ParseDocx(docxPath)
		if err != nil {
			log.Printf("  Parse error %s: %v", filepath.Base(docxPath), err)
			continue
		}

		// Optionally convert EMF/WMF images to PNG via LibreOffice
		if p.ConvertImage {
			if n := docx.ConvertResultImages(ctx, parseResult); n > 0 {
				log.Printf("  %s: converted %d images to PNG", spec.SpecID, n)
				docx.UpdateImagePlaceholders(parseResult)
			}
		}

		dbSpec, dbSections, dbImages := convertToDBRecords(parseResult)
		if spec.Release != 0 {
			dbSpec.Release = strconv.Itoa(spec.Release)
		}

		// Store in DB (serialized)
		if err := p.DB.InsertSpecWithSectionsAndImages(dbSpec, dbSections, dbImages); err != nil {
			return "FAILED", fmt.Errorf("db insert: %w", err)
		}
		parsed++

		// Delete the docx file immediately to save disk space
		if err := os.Remove(docxPath); err != nil {
			log.Printf("  warning: failed to remove %s: %v", filepath.Base(docxPath), err)
		}
	}

	if len(result.DocxFiles) > 0 && parsed == 0 {
		return "FAILED", fmt.Errorf("all %d docx files failed to parse", len(result.DocxFiles))
	}

	// Import YAML files if present
	yamlDir := filepath.Join(tmpDir, "_yaml")
	if entries, err := os.ReadDir(yamlDir); err == nil {
		for _, entry := range entries {
			match := yamlFilenameRE.FindStringSubmatch(entry.Name())
			if match == nil {
				continue
			}
			series, num, apiName := match[1], match[2], match[3]
			specID := fmt.Sprintf("TS %s.%s", series, num)

			content, err := os.ReadFile(filepath.Join(yamlDir, entry.Name()))
			if err != nil {
				log.Printf("  %s: failed to read YAML %s: %v", specID, entry.Name(), err)
				continue
			}

			var version string
			if verMatch := yamlVersionRE.FindSubmatch(content); verMatch != nil {
				version = strings.TrimSpace(string(verMatch[1]))
			}

			if err := p.DB.UpsertOpenAPI(specID, apiName, version, entry.Name(), string(content)); err != nil {
				log.Printf("  OpenAPI import error %s: %v", entry.Name(), err)
			}
		}
	}

	return "OK", nil
}

// convertToDBRecords converts parsed docx results into DB records.
func convertToDBRecords(result *docx.ParseResult) (db.Spec, []db.Section, []db.Image) {
	metadata := result.Metadata
	dbSpec := db.Spec{
		ID:      metadata.SpecID,
		Title:   metadata.Title,
		Version: metadata.Version,
		Release: metadata.Release,
		Series:  metadata.Series(),
	}

	var dbSections []db.Section
	for _, s := range result.Sections {
		dbSections = append(dbSections, db.Section{
			SpecID:       metadata.SpecID,
			Number:       s.Number,
			Title:        s.Title,
			Level:        s.Level,
			ParentNumber: s.ParentNumber,
			Content:      docx.SectionToMarkdown(s),
		})
	}

	var dbImages []db.Image
	for _, img := range result.Images {
		dbImages = append(dbImages, db.Image{
			SpecID:      metadata.SpecID,
			Name:        img.Name,
			MIMEType:    img.MIMEType,
			Data:        img.Data,
			LLMReadable: img.LLMReadable,
		})
	}

	return dbSpec, dbSections, dbImages
}

// sortCoverLast sorts file paths so that _cover files come last.
func sortCoverLast(files []string) {
	sort.Slice(files, func(i, j int) bool {
		iCover := strings.Contains(filepath.Base(files[i]), "_cover")
		jCover := strings.Contains(filepath.Base(files[j]), "_cover")
		if iCover != jCover {
			return !iCover
		}
		return filepath.Base(files[i]) < filepath.Base(files[j])
	})
}

// ConvertSingleFile parses a single docx file and stores it in the database.
func ConvertSingleFile(ctx context.Context, d *db.DB, docxPath string, convertImage bool) error {
	result, err := docx.ParseDocx(docxPath)
	if err != nil {
		return fmt.Errorf("parse %s: %w", docxPath, err)
	}

	if convertImage {
		if n := docx.ConvertResultImages(ctx, result); n > 0 {
			docx.UpdateImagePlaceholders(result)
		}
	}

	dbSpec, dbSections, dbImages := convertToDBRecords(result)
	if r := releaseFromDocxFilename(filepath.Base(docxPath)); r != 0 {
		dbSpec.Release = strconv.Itoa(r)
	}
	return d.InsertSpecWithSectionsAndImages(dbSpec, dbSections, dbImages)
}

// ConvertDir converts all .docx files in a directory and stores them in the database.
func ConvertDir(ctx context.Context, d *db.DB, dirPath string, workers int, convertImage bool) error {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return err
	}

	var files []string
	for _, e := range entries {
		if strings.HasSuffix(strings.ToLower(e.Name()), ".docx") && !strings.HasPrefix(e.Name(), ".") {
			files = append(files, filepath.Join(dirPath, e.Name()))
		}
	}
	if len(files) == 0 {
		return fmt.Errorf("no .docx files found in %s", dirPath)
	}

	sortCoverLast(files)
	log.Printf("Found %d .docx files", len(files))

	if workers <= 0 {
		workers = runtime.NumCPU()
	}

	type result struct {
		path     string
		spec     db.Spec
		sections []db.Section
		images   []db.Image
		err      error
	}

	results := make(chan result, len(files))
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup

	for _, f := range files {
		f := f
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results <- result{path: f, err: ctx.Err()}
				return
			}
			defer func() { <-sem }()

			parseResult, err := docx.ParseDocx(f)
			if err != nil {
				results <- result{path: f, err: err}
				return
			}

			if convertImage {
				if n := docx.ConvertResultImages(ctx, parseResult); n > 0 {
					docx.UpdateImagePlaceholders(parseResult)
				}
			}

			dbSpec, dbSections, dbImages := convertToDBRecords(parseResult)
			if r := releaseFromDocxFilename(filepath.Base(f)); r != 0 {
				dbSpec.Release = strconv.Itoa(r)
			}

			results <- result{path: f, spec: dbSpec, sections: dbSections, images: dbImages}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	parsed := make(map[string]result)
	var parseErrors int
	for r := range results {
		if r.err != nil {
			log.Printf("  ERROR: %s: %v", filepath.Base(r.path), r.err)
			parseErrors++
		} else {
			log.Printf("  %s: %s (%d sections, %d images)", filepath.Base(r.path), r.spec.ID, len(r.sections), len(r.images))
			parsed[r.path] = r
		}
	}

	if len(parsed) == 0 {
		return fmt.Errorf("all %d files failed to parse", len(files))
	}

	// Write to DB in cover-last order
	for _, f := range files {
		if r, ok := parsed[f]; ok {
			if err := d.InsertSpecWithSectionsAndImages(r.spec, r.sections, r.images); err != nil {
				log.Printf("  DB error for %s: %v", r.spec.ID, err)
			}
		}
	}

	if parseErrors > 0 {
		log.Printf("  %d of %d files failed to parse", parseErrors, len(files))
	}

	return nil
}
