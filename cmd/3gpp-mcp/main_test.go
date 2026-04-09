package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/higebu/3gpp-mcp/db"
)

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
			false, // allVersions
			false, // useCache
		)
		if len(specs) == 0 {
			t.Error("expected at least one spec, got 0")
		}
		// latestOnly=true (because allVersions=false), so expect 1 per SpecID.
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
			false,    // allVersions
			false,    // useCache
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
	result, err := d.ListSpecs("", -1, 0)
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
	result, err := d.ListSpecs("", -1, 0)
	if err != nil {
		t.Fatalf("list specs: %v", err)
	}
	if len(result.Specs) == 0 {
		t.Error("expected at least one spec inserted")
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
			"-all",
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
			"-all",
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
			false, // allVersions (latestOnly=true)
			false, // useCache
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
		ID:      "TS 23.501",
		Title:   "System architecture",
		Version: "k10",
		Release: "Rel-20",
		Series:  "23",
	}); err != nil {
		t.Fatalf("upsert spec: %v", err)
	}
	d.Close()

	// Provide a spec-list with an OLDER version of the same spec. FilterSpecs
	// with latestOnly=true picks j60; cmdUpdate then sees j60 < k10 and prints
	// "All specs are up to date.".
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
