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
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultMaxZipSizeMB = 512

// maxZipSize is the upper limit for ZIP downloads. Configurable via THREEGPP_MAX_ZIP_SIZE_MB.
var maxZipSize = int64(defaultMaxZipSizeMB) << 20

func init() {
	if v := os.Getenv("THREEGPP_MAX_ZIP_SIZE_MB"); v != "" {
		if mb, err := strconv.ParseInt(v, 10, 64); err == nil && mb > 0 {
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

	var lastErr error
	for attempt := range 3 {
		data, err := downloadZip(ctx, client, spec.URL)
		if err != nil {
			lastErr = err
			if attempt < 2 {
				time.Sleep(time.Duration(1<<uint(attempt+1)) * time.Second)
				continue
			}
			result.Status = "FAILED"
			return result, fmt.Errorf("download failed after 3 attempts: %w", lastErr)
		}

		r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			lastErr = err
			if attempt < 2 {
				time.Sleep(time.Duration(1<<uint(attempt+1)) * time.Second)
				continue
			}
			result.Status = "FAILED"
			return result, fmt.Errorf("bad zip after 3 attempts: %w", lastErr)
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
			result.Status = "OK"
			return result, nil
		}

		if len(docFiles) > 0 {
			docDir := filepath.Join(outputDir, "_doc_files")
			if err := os.MkdirAll(docDir, 0o700); err != nil {
				log.Printf("  %s: failed to create doc dir: %v", spec.SpecID, err)
			}
			for _, f := range r.File {
				name := filepath.Base(f.Name)
				if strings.HasSuffix(strings.ToLower(name), ".doc") && !strings.HasPrefix(name, ".") {
					if err := extractFile(f, filepath.Join(docDir, name)); err != nil {
						log.Printf("  %s: failed to extract %s: %v", spec.SpecID, name, err)
					}
				}
			}
			result.Status = "DOC_ONLY"
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
		cmd := exec.CommandContext(ctx, "libreoffice",
			"--headless",
			"-env:UserInstallation=file://"+profileDir,
			"--convert-to", "docx",
			"--outdir", outputDir,
			inPath,
		)
		out, err := cmd.CombinedOutput()
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
		specID string
		status string
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
			if r != nil {
				status = r.Status
			}
			results <- result{spec.SpecID, status}
		}()
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	i := 0
	for r := range results {
		i++
		stats[r.status]++
		log.Printf("[%d/%d] %s: %s", i, total, r.specID, r.status)
	}

	if convertDoc {
		docDir := filepath.Join(outputDir, "_doc_files")
		if entries, err := os.ReadDir(docDir); err == nil && len(entries) > 0 {
			log.Println("Converting .doc files to .docx...")
			n, err := ConvertDocFiles(ctx, docDir, outputDir)
			if err != nil {
				log.Printf("ConvertDocFiles error: %v", err)
			}
			log.Printf("Converted %d files", n)
			if n > 0 && stats["DOC_ONLY"] > 0 {
				converted := n
				if converted > stats["DOC_ONLY"] {
					converted = stats["DOC_ONLY"]
				}
				stats["DOC_ONLY"] -= converted
				stats["OK"] += converted
			}
		}
	}

	return stats
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
	// Limit extraction size to prevent decompression bombs.
	n, err := io.Copy(out, io.LimitReader(rc, maxZipSize))
	if closeErr := out.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(outPath)
		return err
	}
	if n >= maxZipSize {
		_ = os.Remove(outPath)
		return fmt.Errorf("extracted file %s exceeds maximum size of %d MB", f.Name, maxZipSize>>20)
	}
	return nil
}
