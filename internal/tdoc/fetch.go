package tdoc

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/higebu/3gpp-mcp/internal/converter/docx"
	"github.com/higebu/3gpp-mcp/internal/converter/pipeline"
	"github.com/higebu/3gpp-mcp/internal/db"
)

var (
	// ErrUnsupported reports that the download holds no Word document to
	// convert: a spreadsheet-only TDoc, a slide deck, a PDF. The message
	// lists the files so the caller can still fetch the original.
	ErrUnsupported = errors.New("no convertible document")
	// ErrNeedsLibreOffice reports a legacy .doc document on a server without
	// LibreOffice, which is what converts .doc to .docx.
	ErrNeedsLibreOffice = errors.New("legacy .doc document needs LibreOffice to convert")
)

// lookPath is exec.LookPath, replaced in tests to simulate a server without
// LibreOffice.
var lookPath = exec.LookPath

// Fetched is a downloaded and converted document.
type Fetched struct {
	// Title is the document's title as best it can be told: the "Title:"
	// field of a CR cover sheet or LS header when there is one.
	Title string
	// MainFile is the file that was converted, as named inside the zip
	// (a nested zip's member is "inner.zip/name.docx").
	MainFile string
	// Files lists every file the download holds, MainFile included, so a
	// reader knows about attachments that were not converted.
	Files []string
	// Sections carry the Markdown; SpecID is the document ID, Version "".
	Sections []db.Section
	Images   []db.Image
}

// member is a file inside the download, possibly inside a nested zip.
type member struct {
	name   string    // display name: "inner.zip/name.docx" for nested
	file   *zip.File // nil for a bare (non-zip) download
	nested bool
}

// Fetch downloads a document and converts its main Word file to Markdown.
// Content before the first heading is kept (docx.ParseOptions.KeepPreamble):
// that is where a change request's cover sheet and a liaison statement's
// header live. EMF/WMF figures are converted to PNG and .doc files to .docx
// when LibreOffice is installed.
func Fetch(ctx context.Context, client *http.Client, doc Document, timeout time.Duration) (*Fetched, error) {
	if client == nil {
		if timeout == 0 {
			timeout = 30 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	data, err := pipeline.DownloadZip(ctx, client, doc.URL())
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", doc.URL(), err)
	}

	tmpDir, err := os.MkdirTemp("", "3gpp-tdoc-")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	base := path.Base(doc.Path)
	var docPath string
	var fetched Fetched
	switch strings.ToLower(path.Ext(base)) {
	case ".docx", ".doc":
		// A bare document, not a zip.
		docPath = filepath.Join(tmpDir, sanitizeName(base))
		if err := os.WriteFile(docPath, data, 0o600); err != nil {
			return nil, fmt.Errorf("write %s: %w", base, err)
		}
		fetched.MainFile = base
		fetched.Files = []string{base}
	default:
		r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return nil, fmt.Errorf("%s is not a zip archive: %w", base, err)
		}
		members, err := listMembers(r)
		if err != nil {
			return nil, err
		}
		for _, m := range members {
			fetched.Files = append(fetched.Files, m.name)
		}
		main, ok := chooseMain(members, doc.ID)
		if !ok {
			return nil, fmt.Errorf("%w in %s (files: %s)", ErrUnsupported, base, strings.Join(fetched.Files, ", "))
		}
		fetched.MainFile = main.name
		docPath = filepath.Join(tmpDir, sanitizeName(path.Base(main.name)))
		if err := pipeline.ExtractFile(main.file, docPath); err != nil {
			return nil, fmt.Errorf("extract %s: %w", main.name, err)
		}
	}

	if strings.EqualFold(filepath.Ext(docPath), ".doc") {
		docPath, err = convertDoc(ctx, docPath, tmpDir)
		if err != nil {
			return nil, err
		}
	}

	parsed, err := docx.ParseDocxWithOptions(docPath, docx.ParseOptions{KeepPreamble: true})
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", fetched.MainFile, err)
	}
	if _, err := lookPath("soffice"); err == nil {
		if n := docx.ConvertResultImages(ctx, parsed); n > 0 {
			docx.UpdateImagePlaceholders(parsed)
		}
	}

	for _, s := range parsed.Sections {
		fetched.Sections = append(fetched.Sections, db.Section{
			SpecID:       doc.ID,
			Number:       s.Number,
			Title:        s.Title,
			Level:        s.Level,
			ParentNumber: s.ParentNumber,
			Content:      docx.SectionToMarkdown(s),
		})
	}
	if len(fetched.Sections) == 0 {
		return nil, fmt.Errorf("%w: %s holds no text", ErrUnsupported, fetched.MainFile)
	}
	for _, img := range parsed.Images {
		fetched.Images = append(fetched.Images, db.Image{
			SpecID:      doc.ID,
			Name:        img.Name,
			MIMEType:    img.MIMEType,
			Data:        img.Data,
			LLMReadable: img.LLMReadable,
		})
	}
	fetched.Title = documentTitle(fetched.Sections, doc.ID)
	return &fetched, nil
}

// maxNestedZips bounds how many nested archives are opened; an LS carries
// one or two attachments, not dozens.
const maxNestedZips = 8

// listMembers lists the files of a download, descending one level into
// nested zips: a liaison statement's attachments arrive that way, and a few
// TDocs are a zip holding only another zip.
func listMembers(r *zip.Reader) ([]member, error) {
	var members []member
	nested := 0
	for _, f := range r.File {
		if skipEntry(f) {
			continue
		}
		members = append(members, member{name: f.Name, file: f})
		if !strings.EqualFold(path.Ext(f.Name), ".zip") || nested >= maxNestedZips {
			continue
		}
		nested++
		if f.UncompressedSize64 > uint64(pipeline.MaxZipSize()) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		inner, err := readAllLimited(rc, pipeline.MaxZipSize())
		_ = rc.Close()
		if err != nil {
			continue
		}
		ir, err := zip.NewReader(bytes.NewReader(inner), int64(len(inner)))
		if err != nil {
			continue
		}
		for _, g := range ir.File {
			if skipEntry(g) {
				continue
			}
			members = append(members, member{name: f.Name + "/" + g.Name, file: g, nested: true})
		}
	}
	return members, nil
}

// readAllLimited reads r up to limit bytes and fails when it holds more.
func readAllLimited(r io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("nested zip exceeds %d MB", limit>>20)
	}
	return data, nil
}

func skipEntry(f *zip.File) bool {
	if f.FileInfo().IsDir() {
		return true
	}
	base := path.Base(f.Name)
	return strings.HasPrefix(base, ".") || strings.HasPrefix(f.Name, "__MACOSX/") || strings.Contains(f.Name, "..")
}

// chooseMain picks the file to convert. A top-level Word file beats one
// inside an attachment zip, .docx beats .doc, and a file named after the
// TDoc beats the rest (a minutes TDoc ships the TDoc list spreadsheet next
// to the report).
func chooseMain(members []member, id string) (member, bool) {
	type scored struct {
		m     member
		score int
	}
	var candidates []scored
	lowerID := strings.ToLower(id)
	for _, m := range members {
		ext := strings.ToLower(path.Ext(m.name))
		if ext != ".docx" && ext != ".doc" {
			continue
		}
		score := 0
		if !m.nested {
			score += 4
		}
		if ext == ".docx" {
			score += 2
		}
		if lowerID != "" && strings.HasPrefix(strings.ToLower(path.Base(m.name)), lowerID) {
			score++
		}
		candidates = append(candidates, scored{m, score})
	}
	if len(candidates) == 0 {
		return member{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].score != candidates[j].score {
			return candidates[i].score > candidates[j].score
		}
		return candidates[i].m.name < candidates[j].m.name
	})
	return candidates[0].m, true
}

// convertDoc turns a legacy .doc into .docx with LibreOffice.
func convertDoc(ctx context.Context, docPath, tmpDir string) (string, error) {
	if _, err := lookPath("libreoffice"); err != nil {
		return "", fmt.Errorf("%w: %s", ErrNeedsLibreOffice, filepath.Base(docPath))
	}
	docDir := filepath.Join(tmpDir, "doc")
	outDir := filepath.Join(tmpDir, "docx")
	for _, d := range []string{docDir, outDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
	}
	moved := filepath.Join(docDir, filepath.Base(docPath))
	if err := os.Rename(docPath, moved); err != nil {
		return "", fmt.Errorf("stage .doc: %w", err)
	}
	if _, err := pipeline.ConvertDocFiles(ctx, docDir, outDir); err != nil {
		return "", fmt.Errorf("convert %s: %w", filepath.Base(docPath), err)
	}
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return "", err
	}
	for _, e := range entries {
		if strings.EqualFold(filepath.Ext(e.Name()), ".docx") {
			return filepath.Join(outDir, e.Name()), nil
		}
	}
	return "", fmt.Errorf("convert %s: LibreOffice produced no .docx", filepath.Base(docPath))
}

// sanitizeName keeps a zip member's base name usable as a temp file name.
func sanitizeName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|', 0:
			return '_'
		}
		return r
	}, name)
	if name == "" || name == "." || name == ".." {
		return "document"
	}
	return name
}

// titleFieldRE finds the "Title:" field of a CR cover sheet (a table row:
// `<td>Title:</td><td>…</td>`) or an LS header (a paragraph: "Title:\tLS on
// …", often bold: "**Title:**\tLS on …"). The optional group skips the cell
// boundary; emphasis markers around the label and the value are tolerated
// and trimmed off the result.
var titleFieldRE = regexp.MustCompile(`(?i)(?:^|[>\s*_])Title:[*_]*\s*(?:</[a-z]+>\s*(?:<[^>]+>\s*)*)?[*_]*\s*([^<\n\t]+)`)

// titleFromPreamble extracts the "Title:" field of a cover sheet or header.
func titleFromPreamble(content string) string {
	m := titleFieldRE.FindStringSubmatch(content)
	if m == nil {
		return ""
	}
	t := strings.Trim(strings.TrimSpace(html.UnescapeString(m[1])), "*_ ")
	if t == "-" {
		return ""
	}
	return t
}

// documentTitle picks a title: the "Title:" field in the preamble, else the
// ID. The parser's own title is not consulted: TDoc templates leave
// placeholders ("MTG_TITLE") or stale text in the document properties, and
// the first-heading fallback names a clause, not the document.
func documentTitle(sections []db.Section, id string) string {
	if len(sections) > 0 && sections[0].Number == "" {
		if t := titleFromPreamble(sections[0].Content); t != "" {
			return t
		}
	}
	return id
}
