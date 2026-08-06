package main

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/higebu/3gpp-mcp/converter/pipeline"
	"github.com/higebu/3gpp-mcp/db"
	"github.com/higebu/3gpp-mcp/internal/testutil"
	"github.com/higebu/3gpp-mcp/tools"

	_ "modernc.org/sqlite"
)

func TestHealthHandler(t *testing.T) {
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	healthHandler(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Code)
	}
	if body := w.Body.String(); body != "ok" {
		t.Errorf("expected body \"ok\", got %q", body)
	}
}

func TestBearerAuthMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := bearerAuthMiddleware("secret-token", inner)

	t.Run("valid token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", w.Code)
		}
	})

	t.Run("wrong token", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("missing header", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("wrong scheme", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Basic secret-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})

	t.Run("scheme is case-insensitive", func(t *testing.T) {
		// RFC 7235 auth-scheme tokens are case-insensitive; proxies may
		// normalize the casing.
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "bearer secret-token")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Errorf("expected 200 for lowercase scheme, got %d", w.Code)
		}
	})

	t.Run("401 carries a challenge", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if got := w.Header().Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
			t.Errorf("expected a Bearer challenge, got %q", got)
		}
	})

	t.Run("token must not match as a prefix", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer secret-token-extra")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", w.Code)
		}
	})
}

// captureStdout runs fn and returns whatever it wrote to os.Stdout.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	fn()
	w.Close()
	return <-done
}

func TestCmdCompletion(t *testing.T) {
	tests := []struct {
		shell    string
		contains []string
	}{
		{
			shell:    "bash",
			contains: []string{"_3gpp_mcp", "complete -F _3gpp_mcp", "import-dir"},
		},
		{
			shell:    "zsh",
			contains: []string{"#compdef 3gpp-mcp", "_describe", "completion"},
		},
		{
			shell:    "fish",
			contains: []string{"complete -c 3gpp-mcp", "Start the MCP server"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.shell, func(t *testing.T) {
			out := captureStdout(t, func() {
				cmdCompletion([]string{tt.shell})
			})
			if out == "" {
				t.Errorf("expected non-empty output for %s", tt.shell)
			}
			for _, want := range tt.contains {
				if !strings.Contains(out, want) {
					t.Errorf("expected output to contain %q, got:\n%s", want, out)
				}
			}
		})
	}
}

// projectRoot returns the path to the repository root by walking up from the
// current test file location.
func projectRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(filename), "..", "..")
}

// testdataDocxPath returns the path to the shared testdata .docx file.
func testdataDocxPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(projectRoot(t), "converter", "docx", "testdata", "23274-i20.docx")
}

// redirectTransport rewrites all request URLs to point at the test server,
// allowing tests to exercise code that uses the hardcoded pipeline baseURL.
type redirectTransport struct {
	base    http.RoundTripper
	testURL string
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	target, err := url.Parse(rt.testURL + req.URL.Path)
	if err != nil {
		return nil, err
	}
	req.URL = target
	return rt.base.RoundTrip(req)
}

// TestResolveSpecs_FromSpecList verifies resolveSpecs loads and parses a local
// spec list file, bypassing the 3GPP FTP entirely.
func TestResolveSpecs_FromSpecList(t *testing.T) {
	listPath := filepath.Join(t.TempDir(), "list.txt")
	content := strings.Join([]string{
		"23_series/23.501/23501-k10.zip",
		"23_series/23.501/23501-j60.zip",
		"29_series/29.510/29510-k10.zip",
		// Blank line and non-matching entry (should be ignored by ParseSpecEntry).
		"",
		"not-a-spec-entry",
	}, "\n")
	if err := os.WriteFile(listPath, []byte(content), 0o644); err != nil {
		t.Fatalf("write spec list: %v", err)
	}

	out := captureStdout(t, func() {
		specs := resolveSpecs(
			context.Background(),
			&http.Client{},
			listPath,
			"",    // specFlag
			"",    // seriesFlag
			0,     // release
			false, // useCache
			0,     // scrapeConcurrency
		)
		if len(specs) == 0 {
			t.Error("expected at least one spec, got 0")
		}
		// resolveSpecs keeps only the latest version, so expect 1 per SpecID.
		ids := map[string]bool{}
		for _, s := range specs {
			ids[s.SpecID] = true
		}
		if !ids["23.501"] || !ids["29.510"] {
			t.Errorf("missing expected spec ids: %+v", ids)
		}
	})
	if !strings.Contains(out, "Loading spec list") {
		t.Errorf("expected stdout to mention 'Loading spec list', got: %s", out)
	}
}

// TestResolveSpecs_FetchBySpecFlag exercises the FetchSpecZips branch with a
// mock 3GPP server returning a single zip listing.
func TestResolveSpecs_FetchBySpecFlag(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23501-k10.zip">23501-k10.zip</a>`+"\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := &http.Client{
		Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL},
	}

	out := captureStdout(t, func() {
		specs := resolveSpecs(
			context.Background(),
			client,
			"",       // specList
			"23.501", // specFlag
			"",       // seriesFlag
			0,        // release
			false,    // useCache
			0,        // scrapeConcurrency
		)
		if len(specs) != 1 {
			t.Fatalf("expected 1 spec, got %d", len(specs))
		}
		if specs[0].SpecID != "23.501" {
			t.Errorf("SpecID = %q, want 23.501", specs[0].SpecID)
		}
	})
	if !strings.Contains(out, "Fetching versions for 23.501") {
		t.Errorf("expected progress message, got: %s", out)
	}
}

// TestRequireSelector covers the selector guard shared by cmdDownload and
// cmdPipeline, including --series as a valid sole selector and the exit path
// taken when no selector is given.
func TestRequireSelector(t *testing.T) {
	tests := []struct {
		name     string
		release  int
		latest   bool
		spec     string
		series   string
		wantExit bool
	}{
		{"none", 0, false, "", "", true},
		{"release", 19, false, "", "", false},
		{"latest", 0, true, "", "", false},
		{"spec", 0, false, "23.501", "", false},
		{"series alone", 0, false, "", "23,29", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exitCode := -1
			orig := exit
			exit = func(code int) { exitCode = code }
			t.Cleanup(func() { exit = orig })

			stderr := captureStderr(t, func() {
				requireSelector(tt.release, tt.latest, tt.spec, tt.series)
			})

			if tt.wantExit {
				if exitCode != 1 {
					t.Errorf("exit code = %d, want 1", exitCode)
				}
				if !strings.Contains(stderr, "Specify --release, --latest, --series, or --spec") {
					t.Errorf("stderr = %q, want selector message", stderr)
				}
			} else if exitCode != -1 {
				t.Errorf("exit(%d) called, want no exit", exitCode)
			}
		})
	}
}

// captureStderr runs fn and returns whatever it wrote to os.Stderr.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	done := make(chan string)
	go func() {
		data, _ := io.ReadAll(r)
		done <- string(data)
	}()

	fn()
	w.Close()
	return <-done
}

// TestCmdConvert_HappyPath imports a single real 3GPP .docx testdata file via
// the CLI command wrapper, covering the end-to-end convert → DB path.
func TestCmdConvert_HappyPath(t *testing.T) {
	if _, err := os.Stat(testdataDocxPath(t)); err != nil {
		t.Skipf("testdata .docx not available: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "out.db")

	out := captureStdout(t, func() {
		cmdConvert([]string{"-db", dbPath, testdataDocxPath(t)})
	})
	if !strings.Contains(out, "Written to") {
		t.Errorf("expected 'Written to' in stdout, got: %s", out)
	}

	// Verify the DB actually got populated.
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	result, err := d.ListSpecs(t.Context(), "", "", -1, 0)
	if err != nil {
		t.Fatalf("list specs: %v", err)
	}
	if len(result.Specs) == 0 {
		t.Error("expected at least one spec inserted")
	}
}

// TestCmdConvertDir_HappyPath imports a directory containing the shared
// testdata .docx file, covering the directory walk and multi-file pipeline.
func TestCmdConvertDir_HappyPath(t *testing.T) {
	srcDocx := testdataDocxPath(t)
	if _, err := os.Stat(srcDocx); err != nil {
		t.Skipf("testdata .docx not available: %v", err)
	}
	docxBytes, err := os.ReadFile(srcDocx)
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "23274-i20.docx"), docxBytes, 0o644); err != nil {
		t.Fatalf("write docx: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "dir.db")

	_ = captureStdout(t, func() {
		cmdConvertDir([]string{"-db", dbPath, "-parse-workers", "1", dir})
	})

	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	result, err := d.ListSpecs(t.Context(), "", "", -1, 0)
	if err != nil {
		t.Fatalf("list specs: %v", err)
	}
	if len(result.Specs) == 0 {
		t.Error("expected at least one spec inserted")
	}
}

// TestCmdConvertDir_ConvertDocFlag verifies that import-dir accepts
// -convert-doc (#63). The directory has no .doc files, so ConvertDocFiles is
// a no-op and this does not require LibreOffice to be installed.
func TestCmdConvertDir_ConvertDocFlag(t *testing.T) {
	srcDocx := testdataDocxPath(t)
	if _, err := os.Stat(srcDocx); err != nil {
		t.Skipf("testdata .docx not available: %v", err)
	}
	docxBytes, err := os.ReadFile(srcDocx)
	if err != nil {
		t.Fatalf("read testdata: %v", err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "23274-i20.docx"), docxBytes, 0o644); err != nil {
		t.Fatalf("write docx: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "dir.db")

	_ = captureStdout(t, func() {
		cmdConvertDir([]string{"-db", dbPath, "-parse-workers", "1", "-convert-doc", dir})
	})

	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer d.Close()
	result, err := d.ListSpecs(t.Context(), "", "", -1, 0)
	if err != nil {
		t.Fatalf("list specs: %v", err)
	}
	if len(result.Specs) == 0 {
		t.Error("expected at least one spec inserted")
	}
}

// TestCmdConvert_OptionsAfterPath exercises the os.Exit(1) path taken when a
// flag is placed after <docx-file>. Go's flag package stops parsing at the
// first non-flag argument, so a trailing flag like --convert-image would
// otherwise be silently ignored instead of applied (#57).
func TestCmdConvert_OptionsAfterPath(t *testing.T) {
	if os.Getenv("CMD_CONVERT_OPTIONS_AFTER_PATH_HELPER") == "1" {
		cmdConvert([]string{"some.docx", "-convert-image"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCmdConvert_OptionsAfterPath")
	cmd.Env = append(os.Environ(), "CMD_CONVERT_OPTIONS_AFTER_PATH_HELPER=1")
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit when an option follows <docx-file>")
	}
	if !strings.Contains(string(stderr), "Options must come before <docx-file>") {
		t.Errorf("expected stderr to explain option order, got: %s", stderr)
	}
}

// TestCmdConvertDir_OptionsAfterPath is the import-dir counterpart of
// TestCmdConvert_OptionsAfterPath (#57).
func TestCmdConvertDir_OptionsAfterPath(t *testing.T) {
	if os.Getenv("CMD_CONVERT_DIR_OPTIONS_AFTER_PATH_HELPER") == "1" {
		cmdConvertDir([]string{"./specs", "-convert-image"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCmdConvertDir_OptionsAfterPath")
	cmd.Env = append(os.Environ(), "CMD_CONVERT_DIR_OPTIONS_AFTER_PATH_HELPER=1")
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit when an option follows <directory>")
	}
	if !strings.Contains(string(stderr), "Options must come before <directory>") {
		t.Errorf("expected stderr to explain option order, got: %s", stderr)
	}
}

// TestCmdDownload_NoMatch verifies cmdDownload cleanly returns when the spec
// list yields zero specs after filtering (exercises flag parsing, resolveSpecs
// file-load branch, and the early return path).
func TestCmdDownload_NoMatch(t *testing.T) {
	listPath := filepath.Join(t.TempDir(), "list.txt")
	if err := os.WriteFile(listPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write list: %v", err)
	}
	outDir := filepath.Join(t.TempDir(), "out")

	out := captureStdout(t, func() {
		cmdDownload([]string{
			"-latest",
			"-spec-list", listPath,
			"-output-dir", outDir,
			"-no-cache",
		})
	})
	if !strings.Contains(out, "No specs matched") {
		t.Errorf("expected 'No specs matched' message, got: %s", out)
	}
}

// TestCmdPipeline_NoMatch verifies cmdPipeline returns cleanly when the spec
// list yields zero specs after filtering.
func TestCmdPipeline_NoMatch(t *testing.T) {
	listPath := filepath.Join(t.TempDir(), "list.txt")
	if err := os.WriteFile(listPath, []byte(""), 0o644); err != nil {
		t.Fatalf("write list: %v", err)
	}
	dbPath := filepath.Join(t.TempDir(), "pipeline.db")

	out := captureStdout(t, func() {
		cmdPipeline([]string{
			"-latest",
			"-db", dbPath,
			"-spec-list", listPath,
			"-no-cache",
		})
	})
	if !strings.Contains(out, "No specs matched") {
		t.Errorf("expected 'No specs matched' message, got: %s", out)
	}
	// DB should still have been opened and schema initialized.
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("expected db file to exist: %v", err)
	}
}

// TestCmdUpdate_EmptyDB verifies cmdUpdate prints the "no specs in database"
// message when the database is empty (ListSpecs returns zero rows).
func TestCmdUpdate_EmptyDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "update.db")
	// Pre-create the DB with schema but no rows, to exercise the normal
	// ListSpecs path rather than a missing-table error path.
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := d.InitSchema(); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	d.Close()

	out := captureStdout(t, func() {
		cmdUpdate([]string{"-db", dbPath})
	})
	if !strings.Contains(out, "No specs in database") {
		t.Errorf("expected 'No specs in database' message, got: %s", out)
	}
}

// TestCmdCompletion_UnknownShell exercises the os.Exit(1) path of cmdCompletion
// by re-executing the test binary as a subprocess so the exit does not abort
// the parent test process.
func TestCmdCompletion_UnknownShell(t *testing.T) {
	if os.Getenv("CMD_COMPLETION_UNKNOWN_HELPER") == "1" {
		cmdCompletion([]string{"powershell"})
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCmdCompletion_UnknownShell")
	cmd.Env = append(os.Environ(), "CMD_COMPLETION_UNKNOWN_HELPER=1")
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for unknown shell")
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		if exitErr.ExitCode() != 1 {
			t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
		}
	}
	if !strings.Contains(string(stderr), "Unknown shell") {
		t.Errorf("expected stderr to mention 'Unknown shell', got: %s", stderr)
	}
}

// TestMainDispatch_UnknownCommand exercises the os.Exit(1) unknown-command
// path of main() via subprocess.
func TestMainDispatch_UnknownCommand(t *testing.T) {
	if os.Getenv("MAIN_UNKNOWN_HELPER") == "1" {
		os.Args = []string{"3gpp-mcp", "bogus-command"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainDispatch_UnknownCommand")
	cmd.Env = append(os.Environ(), "MAIN_UNKNOWN_HELPER=1")
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit for unknown command")
	}
	if !strings.Contains(string(stderr), "Unknown command") {
		t.Errorf("expected stderr to mention 'Unknown command', got: %s", stderr)
	}
}

// TestMainDispatch_NoArgs covers the "no command" path of main(), which prints
// a usage line and exits with code 1.
func TestMainDispatch_NoArgs(t *testing.T) {
	if os.Getenv("MAIN_NOARGS_HELPER") == "1" {
		os.Args = []string{"3gpp-mcp"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainDispatch_NoArgs")
	cmd.Env = append(os.Environ(), "MAIN_NOARGS_HELPER=1")
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit when no command given")
	}
	if !strings.Contains(string(stderr), "Usage:") {
		t.Errorf("expected stderr to mention 'Usage:', got: %s", stderr)
	}
}

// TestResolveSpecs_FetchAllSeries exercises the third branch of resolveSpecs
// (full FTP scrape) with a mock server simulating the 3-level directory layout.
func TestResolveSpecs_FetchAllSeries(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ftp/Specs/archive/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<a href="23_series/">23_series</a>`+"\n")
	})
	mux.HandleFunc("/ftp/Specs/archive/23_series/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23.501/">23.501</a>`+"\n")
	})
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23501-k10.zip">23501-k10.zip</a>`+"\n")
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := &http.Client{
		Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL},
	}

	_ = captureStdout(t, func() {
		specs := resolveSpecs(
			context.Background(),
			client,
			"",    // specList
			"",    // specFlag
			"23",  // seriesFlag
			0,     // release
			false, // useCache
			0,     // scrapeConcurrency
		)
		if len(specs) != 1 {
			t.Fatalf("expected 1 spec, got %d", len(specs))
		}
		if specs[0].SpecID != "23.501" {
			t.Errorf("SpecID = %q, want 23.501", specs[0].SpecID)
		}
	})
}

// TestCmdUpdate_AllUpToDate verifies cmdUpdate's happy path when the spec-list
// file only mentions specs already at or older than the DB versions, exercising
// FilterSpecs, the normalizeID mapping, and the "All specs are up to date"
// branch without triggering any downloads.
func TestCmdUpdate_AllUpToDate(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "update.db")
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := d.InitSchema(); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if err := d.UpsertSpec(db.Spec{
		ID:           "TS 23.501",
		Title:        "System architecture",
		Version:      "20.1.0",
		VersionToken: "k10",
		Release:      "20",
		Series:       "23",
	}); err != nil {
		t.Fatalf("upsert spec: %v", err)
	}
	d.Close()

	// Provide a spec-list with an OLDER version of the same spec. FilterSpecs
	// with latestOnly=true picks j60 (v19.6.0); cmdUpdate then sees it is older
	// than the stored v20.1.0 and prints "All specs are up to date.".
	listPath := filepath.Join(t.TempDir(), "list.txt")
	if err := os.WriteFile(listPath, []byte("23_series/23.501/23501-j60.zip\n"), 0o644); err != nil {
		t.Fatalf("write list: %v", err)
	}

	out := captureStdout(t, func() {
		cmdUpdate([]string{"-db", dbPath, "-spec-list", listPath})
	})
	if !strings.Contains(out, "All specs are up to date") {
		t.Errorf("expected 'All specs are up to date' message, got: %s", out)
	}
}

// partialArchive serves a mock archive where 23.501 lists one zip and 23.502
// fails, so a full scrape assembles a partial spec list.
func partialArchive(t *testing.T, healthyZip string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/ftp/Specs/archive/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ftp/Specs/archive/" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, `<a href="23_series/">23_series</a>`+"\n")
	})
	mux.HandleFunc("/ftp/Specs/archive/23_series/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a href="23.501/">23.501</a>`+"\n"+`<a href="23.502/">23.502</a>`+"\n")
	})
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.501/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `<a href="%s">%s</a>`+"\n", healthyZip, healthyZip)
	})
	mux.HandleFunc("/ftp/Specs/archive/23_series/23.502/", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// captureLog runs fn and returns whatever it wrote through the log package.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	fn()
	return buf.String()
}

// TestResolveSpecs_PartialSpecListAborts verifies that a scrape which lost
// some directory listings aborts the command instead of quietly producing an
// incomplete database from the partial list.
func TestResolveSpecs_PartialSpecListAborts(t *testing.T) {
	ts := partialArchive(t, "23501-k10.zip")
	client := &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}

	exitCode := -1
	orig := exit
	exit = func(code int) { exitCode = code }
	t.Cleanup(func() { exit = orig })

	logged := captureLog(t, func() {
		_ = captureStdout(t, func() {
			specs := resolveSpecs(context.Background(), client, "", "", "23", 0, false, 0)
			if specs != nil {
				t.Errorf("expected nil specs after an abort, got %d", len(specs))
			}
		})
	})
	if exitCode != 1 {
		t.Errorf("exit code = %d, want 1", exitCode)
	}
	if !strings.Contains(logged, "spec list is incomplete") {
		t.Errorf("log = %q, want the incomplete-list abort message", logged)
	}
}

// TestCmdUpdate_PartialSpecListWarns verifies that update survives a partial
// spec list with a warning: a spec missing from the list is merely skipped,
// so aborting the whole run would be needlessly strict.
func TestCmdUpdate_PartialSpecListWarns(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "update.db")
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := d.InitSchema(); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	if err := d.UpsertSpec(db.Spec{
		ID:           "TS 23.501",
		Title:        "System architecture",
		Version:      "20.1.0",
		VersionToken: "k10",
		Release:      "20",
		Series:       "23",
	}); err != nil {
		t.Fatalf("upsert spec: %v", err)
	}
	d.Close()

	// The healthy listing offers an OLDER version (j60 = v19.6.0), so the run
	// finds nothing to update and finishes without downloading anything.
	ts := partialArchive(t, "23501-j60.zip")
	origClient := newHTTPClient
	newHTTPClient = func(timeout time.Duration) *http.Client {
		return &http.Client{Transport: &redirectTransport{base: http.DefaultTransport, testURL: ts.URL}}
	}
	t.Cleanup(func() { newHTTPClient = origClient })

	var out string
	logged := captureLog(t, func() {
		out = captureStdout(t, func() {
			cmdUpdate([]string{"-db", dbPath, "-no-cache"})
		})
	})
	if !strings.Contains(logged, "will not be updated this run") {
		t.Errorf("log = %q, want the partial-list warning", logged)
	}
	if !strings.Contains(out, "All specs are up to date") {
		t.Errorf("output = %q, want the run to continue to completion", out)
	}
}

// TestCmdCompletion_NoArgs covers the os.Exit(1) no-args path via subprocess.
func TestCmdCompletion_NoArgs(t *testing.T) {
	if os.Getenv("CMD_COMPLETION_NOARGS_HELPER") == "1" {
		cmdCompletion(nil)
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestCmdCompletion_NoArgs")
	cmd.Env = append(os.Environ(), "CMD_COMPLETION_NOARGS_HELPER=1")
	stderr, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit when completion called without shell arg")
	}
	if !strings.Contains(string(stderr), "Usage:") {
		t.Errorf("expected Usage: message, got: %s", stderr)
	}
}

// TestMainDispatch_CompletionBash exercises the happy-path completion
// subcommand via main() dispatch, ensuring command routing works end-to-end.
func TestMainDispatch_CompletionBash(t *testing.T) {
	if os.Getenv("MAIN_COMPLETION_HELPER") == "1" {
		os.Args = []string{"3gpp-mcp", "completion", "bash"}
		main()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestMainDispatch_CompletionBash")
	cmd.Env = append(os.Environ(), "MAIN_COMPLETION_HELPER=1")
	stdout, err := cmd.Output()
	if err != nil {
		t.Fatalf("main dispatch failed: %v", err)
	}
	if !strings.Contains(string(stdout), "_3gpp_mcp") {
		t.Errorf("expected bash completion output, got: %s", stdout)
	}
}

// TestStreamableHTTP exercises the HTTP transport end to end: an SDK client
// speaking the latest protocol version against the stateless handler used by
// cmdServe.
func TestStreamableHTTP(t *testing.T) {
	d := testutil.SetupTestDB(t)
	s := newMCPServer(d, tools.NewSource(d))

	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL}, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()

	if got := session.InitializeResult().ProtocolVersion; got != "2026-07-28" {
		t.Errorf("negotiated protocol version = %q, want %q", got, "2026-07-28")
	}

	toolsRes, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(toolsRes.Tools) != 11 {
		t.Errorf("got %d tools, want 11", len(toolsRes.Tools))
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "list_specs",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("CallTool(list_specs): %v", err)
	}
	if res.IsError {
		t.Fatalf("list_specs returned an error result: %v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("list_specs returned no content")
	}
}

// TestStreamableHTTPNewProtocolRawPOST verifies that a bare 2026-07-28
// request — no initialize handshake, no session — is served.
func TestStreamableHTTPNewProtocolRawPOST(t *testing.T) {
	d := testutil.SetupTestDB(t)
	s := newMCPServer(d, tools.NewSource(d))

	handler := mcp.NewStreamableHTTPHandler(
		func(r *http.Request) *mcp.Server { return s },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	ts := httptest.NewServer(handler)
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{` +
		`"io.modelcontextprotocol/protocolVersion":"2026-07-28",` +
		`"io.modelcontextprotocol/clientCapabilities":{}}}}`
	req, err := http.NewRequest("POST", ts.URL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", "2026-07-28")
	// SEP-2243: 2026-07-28 requests mirror the JSON-RPC method in a header.
	req.Header.Set("Mcp-Method", "tools/list")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", resp.StatusCode, respBody)
	}
	if !strings.Contains(string(respBody), "list_specs") {
		t.Errorf("response does not contain tool list: %s", respBody)
	}
	if id := resp.Header.Get("Mcp-Session-Id"); id != "" {
		t.Errorf("stateless server issued Mcp-Session-Id %q", id)
	}
}

// TestBuildHTTPHandler_WebViewerAuth verifies that a bearer token protects the
// web viewer too, not just the MCP endpoint: the viewer serves the same corpus
// as the tools, so an unauthenticated viewer would make the token meaningless.
func TestBuildHTTPHandler_WebViewerAuth(t *testing.T) {
	d := testutil.SetupTestDB(t)
	s := newMCPServer(d, tools.NewSource(d))

	t.Run("token set", func(t *testing.T) {
		handler := buildHTTPHandler(tools.NewSource(d), s, "secret-token", true)
		ts := httptest.NewServer(handler)
		defer ts.Close()

		for _, path := range []string{"/", "/specs/TS%2023.501", "/search?q=architecture", "/mcp/"} {
			resp, err := http.Get(ts.URL + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Errorf("GET %s without token = %d, want 401", path, resp.StatusCode)
			}
		}

		// The health probe stays open for load balancers.
		resp, err := http.Get(ts.URL + "/health")
		if err != nil {
			t.Fatalf("GET /health: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET /health = %d, want 200", resp.StatusCode)
		}

		// With the token, the viewer answers.
		req, _ := http.NewRequest("GET", ts.URL+"/", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		authed, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("GET / with token: %v", err)
		}
		defer authed.Body.Close()
		if authed.StatusCode != http.StatusOK {
			t.Errorf("GET / with token = %d, want 200", authed.StatusCode)
		}
	})

	t.Run("no token keeps viewer open", func(t *testing.T) {
		handler := buildHTTPHandler(tools.NewSource(d), s, "", true)
		ts := httptest.NewServer(handler)
		defer ts.Close()

		resp, err := http.Get(ts.URL + "/")
		if err != nil {
			t.Fatalf("GET /: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET / without a configured token = %d, want 200", resp.StatusCode)
		}
	})
}

// listenLocal binds a listener on a free loopback port for runHTTPServer tests.
func listenLocal(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	return ln
}

// TestRunHTTPServer covers the serve loop extracted from cmdServe: graceful
// shutdown on context cancellation, error propagation on serve failure, and a
// non-nil error when the drain deadline expires with requests in flight — the
// process must not exit 0 after cutting requests off (#105).
func TestRunHTTPServer(t *testing.T) {
	t.Run("graceful shutdown on cancel", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		server := &http.Server{Handler: http.HandlerFunc(healthHandler)}

		done := make(chan error, 1)
		go func() { done <- runHTTPServer(ctx, server, listenLocal(t)) }()
		// Give Serve a moment to start, then cancel.
		time.Sleep(100 * time.Millisecond)
		cancel()

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("expected nil after graceful shutdown, got %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("runHTTPServer did not return after cancellation")
		}
	})

	t.Run("serve error propagates", func(t *testing.T) {
		ln := listenLocal(t)
		ln.Close()
		server := &http.Server{Handler: http.HandlerFunc(healthHandler)}
		if err := runHTTPServer(context.Background(), server, ln); err == nil {
			t.Error("expected an error for a closed listener")
		}
	})

	t.Run("failed drain returns an error", func(t *testing.T) {
		origTimeout := shutdownTimeout
		shutdownTimeout = 50 * time.Millisecond
		t.Cleanup(func() { shutdownTimeout = origTimeout })

		release := make(chan struct{})
		defer close(release)
		started := make(chan struct{})
		server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-release
		})}
		defer server.Close()

		ln := listenLocal(t)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		done := make(chan error, 1)
		go func() { done <- runHTTPServer(ctx, server, ln) }()

		// Park a request inside the handler so Shutdown cannot drain.
		go func() {
			resp, err := http.Get("http://" + ln.Addr().String())
			if err == nil {
				resp.Body.Close()
			}
		}()
		<-started
		cancel()

		select {
		case err := <-done:
			if err == nil {
				t.Error("expected an error when the drain deadline expires with a request in flight")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("runHTTPServer did not return after the drain deadline")
		}
	})
}

// seedWorkingCopy builds a WAL-mode database at path with enough rows to leave
// a substantial write-ahead log behind.
func seedWorkingCopy(t *testing.T, path string) *db.DB {
	t.Helper()
	d, err := db.OpenReadWrite(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Exec("CREATE TABLE t(a)"); err != nil {
		t.Fatal(err)
	}
	for i := range 200 {
		if err := d.Exec("INSERT INTO t VALUES (?)", i); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestFinalizeWorkingCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "3gpp.db.new")
	d := seedWorkingCopy(t, path)

	if err := finalizeWorkingCopy(d, path); err != nil {
		t.Fatalf("finalizeWorkingCopy: %v", err)
	}
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Errorf("%s still exists after finalize (stat err %v)", sidecar, err)
		}
	}

	// The rows have to survive in the main file on their own.
	conn, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 200 {
		t.Errorf("row count = %d, want 200", n)
	}
}

// A checkpoint blocked by a concurrent reader reports "busy" in its result row,
// not as an error: PRAGMA wal_checkpoint and Close both return nil while the
// whole update is still sitting in the WAL. Deleting the WAL and renaming the
// file at that point throws the run away and still reports success.
func TestFinalizeWorkingCopy_BlockedCheckpointKeepsWAL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "3gpp.db.new")
	d := seedWorkingCopy(t, path)

	reader, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	reader.SetMaxOpenConns(1)
	// An open read transaction pins the snapshot the WAL still describes.
	tx, err := reader.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var n int
	if err := tx.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}

	if err := finalizeWorkingCopy(d, path); err == nil {
		t.Fatal("expected an error when the checkpoint cannot merge the WAL")
	}
	fi, statErr := os.Stat(path + "-wal")
	if statErr != nil {
		t.Fatalf("the unmerged WAL was deleted: %v", statErr)
	}
	if fi.Size() == 0 {
		t.Error("the unmerged WAL was truncated")
	}
}

// TestFinalizeWorkingCopy_StatError checks that a stat failure on the WAL
// sidecar is reported instead of being treated as "no WAL": an unreadable
// sidecar is exactly the unknown-merge-state the check exists to catch (#105).
func TestFinalizeWorkingCopy_StatError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// A path whose parent is a regular file: os.Stat(path+"-wal") fails with
	// ENOTDIR, which is a stat error but not os.ErrNotExist.
	path := filepath.Join(blocker, "3gpp.db.new")

	d := seedWorkingCopy(t, filepath.Join(dir, "real.db"))
	err := finalizeWorkingCopy(d, path)
	if err == nil {
		t.Fatal("expected an error when the WAL sidecar cannot be stat'ed")
	}
	if !strings.Contains(err.Error(), "write-ahead log") {
		t.Errorf("error = %v, want a WAL stat failure report", err)
	}
}

// TestFinalizeWorkingCopy_RemoveError checks that a sidecar that cannot be
// removed refuses the rename instead of being silently ignored: a stale
// sidecar next to the next run's working copy would be picked up as its WAL.
func TestFinalizeWorkingCopy_RemoveError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "3gpp.db.new")
	// A non-empty directory in the sidecar's place makes os.Remove fail with
	// ENOTEMPTY, an error that is not os.ErrNotExist.
	if err := os.MkdirAll(filepath.Join(path+"-shm", "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	d := seedWorkingCopy(t, filepath.Join(dir, "real.db"))
	err := finalizeWorkingCopy(d, path)
	if err == nil {
		t.Fatal("expected an error when a sidecar cannot be removed")
	}
	if !strings.Contains(err.Error(), "remove") {
		t.Errorf("error = %v, want a remove failure report", err)
	}
}

// A run killed mid-update leaves the working copy's -wal and -shm behind, and
// SQLite binds a write-ahead log to its database by file name alone: removing
// only the database file lets the stale WAL be replayed into the copy the next
// run builds at the same path (#129).
func TestRemoveWorkingCopy_ClearsStaleSidecars(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "3gpp.db")
	newPath := live + ".new"

	src := seedWorkingCopy(t, live)
	if err := finalizeWorkingCopy(src, live); err != nil {
		t.Fatalf("finalizeWorkingCopy: %v", err)
	}

	// Debris of the killed run: a half-written copy and both sidecars.
	debris := []string{newPath, newPath + "-wal", newPath + "-shm"}
	for _, p := range debris {
		if err := os.WriteFile(p, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := removeWorkingCopy(newPath); err != nil {
		t.Fatalf("removeWorkingCopy: %v", err)
	}
	for _, p := range debris {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived cleanup (stat err %v)", p, err)
		}
	}

	// The rebuild has to land on a clean path: VACUUM INTO refuses an existing
	// target, and any sidecar left next to the result follows it into the
	// rename.
	d, err := db.OpenReadWrite(live)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.VacuumInto(newPath); err != nil {
		_ = d.Close()
		t.Fatalf("VACUUM INTO after cleanup: %v", err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []string{newPath + "-wal", newPath + "-shm"} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Errorf("%s exists next to the rebuilt copy (stat err %v)", sidecar, err)
		}
	}

	conn, err := sql.Open("sqlite", "file:"+newPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var n int
	if err := conn.QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 200 {
		t.Errorf("row count in rebuilt copy = %d, want 200", n)
	}
}

// Nothing to clean up is not a failure: the first run of 'update' finds no
// working copy at all.
func TestRemoveWorkingCopy_NoFiles(t *testing.T) {
	if err := removeWorkingCopy(filepath.Join(t.TempDir(), "3gpp.db.new")); err != nil {
		t.Errorf("removeWorkingCopy on a missing working copy = %v, want nil", err)
	}
}

// A sidecar that cannot be removed has to be reported: proceeding would run
// VACUUM INTO next to a WAL that does not belong to its output.
func TestRemoveWorkingCopy_SidecarError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "3gpp.db.new")
	// A non-empty directory in the sidecar's place makes os.Remove fail with
	// ENOTEMPTY, an error that is not os.ErrNotExist.
	if err := os.MkdirAll(filepath.Join(path+"-wal", "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := removeWorkingCopy(path)
	if err == nil {
		t.Fatal("expected an error when a stale sidecar cannot be removed")
	}
	if !strings.Contains(err.Error(), "-wal") {
		t.Errorf("error = %v, want it to name the -wal sidecar", err)
	}
}

// The rename leaves the replaced database's sidecars next to the file that
// just took its name, so they have to go with it.
func TestReplaceDatabase_ClearsPreviousSidecars(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "3gpp.db")
	newPath := live + ".new"

	if err := os.WriteFile(live, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, sidecar := range []string{live + "-wal", live + "-shm"} {
		if err := os.WriteFile(sidecar, []byte("stale"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(newPath, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := replaceDatabase(newPath, live); err != nil {
		t.Fatalf("replaceDatabase: %v", err)
	}

	got, err := os.ReadFile(live)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("live database content = %q, want the working copy's", got)
	}
	for _, p := range []string{live + "-wal", live + "-shm", newPath} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s survived the replacement (stat err %v)", p, err)
		}
	}
}

// A failed rename leaves the live database alone and is reported.
func TestReplaceDatabase_RenameError(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "3gpp.db")
	if err := os.WriteFile(live, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := replaceDatabase(live+".new", live)
	if err == nil {
		t.Fatal("expected an error when the working copy is missing")
	}
	if !strings.Contains(err.Error(), "rename working copy") {
		t.Errorf("error = %v, want a rename failure report", err)
	}
	if errors.Is(err, errSidecarsRemain) {
		t.Errorf("error = %v, want it not to claim the database was replaced", err)
	}
	got, readErr := os.ReadFile(live)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "old" {
		t.Errorf("live database content = %q, want it untouched", got)
	}
}

// A sidecar the replacement cannot delete is a corruption hazard on the live
// path, so it is surfaced instead of being swallowed by the success message.
func TestReplaceDatabase_SidecarError(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "3gpp.db")
	newPath := live + ".new"
	if err := os.WriteFile(newPath, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(live+"-shm", "child"), 0o755); err != nil {
		t.Fatal(err)
	}

	err := replaceDatabase(newPath, live)
	if err == nil {
		t.Fatal("expected an error when a sidecar of the replaced database survives")
	}
	if !strings.Contains(err.Error(), "-shm") {
		t.Errorf("error = %v, want it to name the -shm sidecar", err)
	}
	// The rename did land, so the caller must not report a failed replacement
	// or try to delete a working copy that no longer exists.
	if !errors.Is(err, errSidecarsRemain) {
		t.Errorf("error = %v, want it to report that the replacement already happened", err)
	}
	if _, statErr := os.Stat(newPath); !os.IsNotExist(statErr) {
		t.Errorf("working copy still at %s (stat err %v)", newPath, statErr)
	}
}

// TestHTTPListenAddr covers the ListenAndServe-compatible default for an
// empty listen address.
func TestHTTPListenAddr(t *testing.T) {
	if got := httpListenAddr(""); got != ":http" {
		t.Errorf("httpListenAddr(\"\") = %q, want \":http\"", got)
	}
	if got := httpListenAddr("127.0.0.1:8080"); got != "127.0.0.1:8080" {
		t.Errorf("httpListenAddr passthrough = %q, want unchanged", got)
	}
}

// TestRunHelpers_DatabaseErrors covers the open and init-schema error branches
// shared by the run helpers extracted for #105.
func TestRunHelpers_DatabaseErrors(t *testing.T) {
	ctx := context.Background()
	helpers := map[string]func(dbPath string) error{
		"convert": func(p string) error {
			return runConvert(ctx, p, "unused.docx", false)
		},
		"convert-dir": func(p string) error {
			return runConvertDir(ctx, p, t.TempDir(), 1, false, false)
		},
		"pipeline": func(p string) error {
			return runPipeline(ctx, p, nil, nil, 1, false, false, time.Second)
		},
	}
	for name, run := range helpers {
		t.Run(name, func(t *testing.T) {
			// A directory is not a valid database file, so opening fails.
			if err := run(t.TempDir()); err == nil || !strings.Contains(err.Error(), "open database") {
				t.Errorf("open error = %v, want an \"open database\" failure", err)
			}

			// A pre-existing view named "specs" makes InitSchema fail
			// (its index cannot be created on a view) after a clean open.
			p := filepath.Join(t.TempDir(), "conflict.db")
			d, err := db.OpenReadWrite(p)
			if err != nil {
				t.Fatal(err)
			}
			if err := d.Exec("CREATE VIEW specs AS SELECT 1 AS id"); err != nil {
				t.Fatal(err)
			}
			if err := d.Close(); err != nil {
				t.Fatal(err)
			}
			if err := run(p); err == nil || !strings.Contains(err.Error(), "init schema") {
				t.Errorf("schema error = %v, want an \"init schema\" failure", err)
			}
			assertNoWALSidecars(t, p)
		})
	}
}

// captureExit swaps the exit hook and returns a pointer to the recorded exit
// code, -1 until exit is called.
func captureExit(t *testing.T) *int {
	t.Helper()
	code := -1
	orig := exit
	exit = func(c int) { code = c }
	t.Cleanup(func() { exit = orig })
	return &code
}

// TestCmdConvert_FatalError covers cmdConvert's error exit: the run helper's
// error is reported and the process exits 1 (via the exit hook), after the
// helper has already closed the database.
func TestCmdConvert_FatalError(t *testing.T) {
	code := captureExit(t)
	dbPath := filepath.Join(t.TempDir(), "out.db")
	var logged string
	_ = captureStdout(t, func() {
		logged = captureLog(t, func() {
			cmdConvert([]string{"-db", dbPath, filepath.Join(t.TempDir(), "missing.docx")})
		})
	})
	if *code != 1 {
		t.Errorf("exit code = %d, want 1", *code)
	}
	if !strings.Contains(logged, "Convert failed") {
		t.Errorf("log = %q, want a 'Convert failed' report", logged)
	}
	assertNoWALSidecars(t, dbPath)
}

// TestCmdConvertDir_FatalError is the import-dir counterpart of
// TestCmdConvert_FatalError.
func TestCmdConvertDir_FatalError(t *testing.T) {
	code := captureExit(t)
	dbPath := filepath.Join(t.TempDir(), "dir.db")
	logged := captureLog(t, func() {
		// An empty directory has no .docx files, so the import fails.
		cmdConvertDir([]string{"-db", dbPath, t.TempDir()})
	})
	if *code != 1 {
		t.Errorf("exit code = %d, want 1", *code)
	}
	if !strings.Contains(logged, "Convert dir failed") {
		t.Errorf("log = %q, want a 'Convert dir failed' report", logged)
	}
	assertNoWALSidecars(t, dbPath)
}

// TestCmdPipeline_FatalError covers cmdPipeline's error exit: the spec list
// resolves fine, but the database path is a directory so runPipeline fails.
func TestCmdPipeline_FatalError(t *testing.T) {
	code := captureExit(t)
	listPath := filepath.Join(t.TempDir(), "list.txt")
	if err := os.WriteFile(listPath, []byte("23_series/23.501/23501-k10.zip\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var logged string
	_ = captureStdout(t, func() {
		logged = captureLog(t, func() {
			cmdPipeline([]string{"-latest", "-db", t.TempDir(), "-spec-list", listPath, "-no-cache"})
		})
	})
	if *code != 1 {
		t.Errorf("exit code = %d, want 1", *code)
	}
	if !strings.Contains(logged, "Pipeline failed") {
		t.Errorf("log = %q, want a 'Pipeline failed' report", logged)
	}
}

// serveTestDB creates an empty database with the schema so cmdServe's
// read-only open succeeds.
func serveTestDB(t *testing.T) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "serve.db")
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.InitSchema(); err != nil {
		t.Fatal(err)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	return dbPath
}

// TestCmdServe_ListenError covers the HTTP transport's listen failure exit:
// the port is already taken, so cmdServe reports the error and exits 1.
func TestCmdServe_ListenError(t *testing.T) {
	code := captureExit(t)
	taken := listenLocal(t)
	defer taken.Close()

	logged := captureLog(t, func() {
		cmdServe([]string{
			"-db", serveTestDB(t),
			"-transport", "http",
			"-addr", taken.Addr().String(),
			"-no-fetch",
		})
	})
	if *code != 1 {
		t.Errorf("exit code = %d, want 1", *code)
	}
	if !strings.Contains(logged, "Server error") {
		t.Errorf("log = %q, want a 'Server error' report", logged)
	}
}

// TestCmdServe_HTTPGracefulShutdown runs the full serve command over the HTTP
// transport and shuts it down with SIGTERM, the signal cmdServe registers for.
// This is the only way to cover cmdServe's serve loop in-process.
func TestCmdServe_HTTPGracefulShutdown(t *testing.T) {
	// Safety net: if the port is stolen between Close and cmdServe's bind,
	// the exit hook keeps the failure inside this test instead of killing
	// the whole test process.
	exitCh := make(chan int, 1)
	orig := exit
	exit = func(c int) {
		select {
		case exitCh <- c:
		default:
		}
	}
	t.Cleanup(func() { exit = orig })

	dbPath := serveTestDB(t)
	ln := listenLocal(t)
	addr := ln.Addr().String()
	ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		cmdServe([]string{"-db", dbPath, "-transport", "http", "-addr", addr, "-no-fetch"})
	}()

	up := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		select {
		case <-done:
			code := 0
			select {
			case code = <-exitCh:
			default:
			}
			t.Fatalf("cmdServe returned before serving (exit code %d)", code)
		default:
		}
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				up = true
			}
		}
		if up {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !up {
		t.Fatal("server did not come up")
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cmdServe did not shut down on SIGTERM")
	}
}

// TestCmdServe_FailedDrainExitsNonZero runs cmdServe end to end, parks a
// half-sent request so the drain deadline expires, and checks the failed
// shutdown is reported through the exit hook instead of exiting 0.
func TestCmdServe_FailedDrainExitsNonZero(t *testing.T) {
	origTimeout := shutdownTimeout
	shutdownTimeout = 50 * time.Millisecond
	t.Cleanup(func() { shutdownTimeout = origTimeout })

	exitCh := make(chan int, 1)
	orig := exit
	exit = func(c int) {
		select {
		case exitCh <- c:
		default:
		}
	}
	t.Cleanup(func() { exit = orig })

	dbPath := serveTestDB(t)
	ln := listenLocal(t)
	addr := ln.Addr().String()
	ln.Close()

	done := make(chan struct{})
	go func() {
		defer close(done)
		cmdServe([]string{"-db", dbPath, "-transport", "http", "-addr", addr, "-no-fetch"})
	}()

	up := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		resp, err := http.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				up = true
			}
		}
		if up {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !up {
		t.Fatal("server did not come up")
	}

	// A connection with a half-sent request counts as active, so Shutdown
	// cannot finish within the shortened drain deadline.
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("GET /health HTTP/1.1\r\nHost: x\r\n")); err != nil {
		t.Fatal(err)
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("cmdServe did not return after the failed drain")
	}
	select {
	case code := <-exitCh:
		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
	default:
		t.Error("expected the failed drain to exit non-zero")
	}
}

// assertNoWALSidecars fails the test when dbPath has -wal/-shm files left
// behind — the symptom of exiting without closing a WAL-mode database (#105).
func assertNoWALSidecars(t *testing.T, dbPath string) {
	t.Helper()
	for _, sidecar := range []string{dbPath + "-wal", dbPath + "-shm"} {
		if _, err := os.Stat(sidecar); !os.IsNotExist(err) {
			t.Errorf("%s left behind (stat err: %v)", sidecar, err)
		}
	}
}

// TestRunConvert_ErrorClosesDatabase verifies that a failed import still
// closes the database: before #105, cmdConvert exited via log.Fatalf and the
// skipped deferred Close left uncheckpointed WAL sidecars behind.
func TestRunConvert_ErrorClosesDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "out.db")
	_ = captureStdout(t, func() {
		err := runConvert(context.Background(), dbPath, filepath.Join(t.TempDir(), "missing.docx"), false)
		if err == nil {
			t.Error("expected an error for a missing .docx")
		}
	})
	assertNoWALSidecars(t, dbPath)
}

// TestRunConvertDir_ErrorClosesDatabase is the import-dir counterpart of
// TestRunConvert_ErrorClosesDatabase (#105).
func TestRunConvertDir_ErrorClosesDatabase(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "dir.db")
	// An empty directory makes ConvertDir fail with "no .docx files found".
	err := runConvertDir(context.Background(), dbPath, t.TempDir(), 1, false, false)
	if err == nil {
		t.Error("expected an error for a directory without .docx files")
	}
	assertNoWALSidecars(t, dbPath)
}

// TestRunPipeline_ErrorClosesDatabase verifies the build pipeline closes the
// database when the run fails (#105). The mock server returns a valid zip
// whose only .docx cannot be parsed, so every spec fails quickly without the
// download retry backoff.
func TestRunPipeline_ErrorClosesDatabase(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("bad.docx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("not a docx")); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(buf.Bytes())
	}))
	defer ts.Close()

	dbPath := filepath.Join(t.TempDir(), "pipeline.db")
	specs := []*pipeline.SpecVersion{
		{SpecID: "23.501", Filename: "23501-i60.zip", URL: ts.URL + "/23501-i60.zip"},
	}

	_ = captureStdout(t, func() {
		err := runPipeline(context.Background(), dbPath, ts.Client(), specs, 1, false, false, 10*time.Second)
		if err == nil {
			t.Error("expected an error when every spec failed")
		}
	})
	assertNoWALSidecars(t, dbPath)
}

// TestCmdUpdate_UnreadableDB checks that a database that cannot be read is
// reported as a failure rather than as "No specs in database": the two used to
// share one branch, so a broken database told the user to run 'build' and
// exited 0. Run as a subprocess because the failure path calls log.Fatalf.
func TestCmdUpdate_UnreadableDB(t *testing.T) {
	if os.Getenv("CMD_UPDATE_UNREADABLE_HELPER") != "" {
		cmdUpdate([]string{"-db", os.Getenv("CMD_UPDATE_UNREADABLE_HELPER")})
		return
	}
	// A database file with no schema at all: ListSpecs fails with "no such
	// table" instead of returning zero rows.
	dbPath := filepath.Join(t.TempDir(), "broken.db")
	d, err := db.OpenReadWrite(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	d.Close()

	cmd := exec.Command(os.Args[0], "-test.run=TestCmdUpdate_UnreadableDB")
	cmd.Env = append(os.Environ(), "CMD_UPDATE_UNREADABLE_HELPER="+dbPath)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected a non-zero exit for an unreadable database, got: %s", out)
	}
	if strings.Contains(string(out), "No specs in database") {
		t.Errorf("an unreadable database was reported as empty: %s", out)
	}
	if !strings.Contains(string(out), "Failed to read specs") {
		t.Errorf("expected the read failure to be reported, got: %s", out)
	}
}
