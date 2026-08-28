// Command e2eserver runs the web viewer against a seeded throwaway database
// for the Playwright end-to-end suite in e2e/. It reuses the canonical test
// seed data and adds a section exercising the browser-only features the suite
// covers: KaTeX math rendering and the image lightbox.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/goccy/tobari"

	"github.com/higebu/3gpp-mcp/internal/db"
	"github.com/higebu/3gpp-mcp/internal/testutil"
	"github.com/higebu/3gpp-mcp/internal/tools"
	"github.com/higebu/3gpp-mcp/internal/web"
)

// e2eSeed extends testutil.SeedData with content the Playwright suite needs:
// inline and display math for the KaTeX test, and an image reference (with a
// 1x1 PNG marked llm_readable so handleImage serves the real bytes, not the
// EMF placeholder) for the lightbox test. Kept separate from the shared seed
// so web_test.go's assertions on that data stay untouched.
const e2eSeed = `
INSERT INTO sections (spec_id, version, number, title, level, parent_number, content) VALUES
    ('TS 23.501', '18.6.0', '9', 'Formulas and figures', 1, NULL, '# 9 Formulas and figures
The energy is $E = mc^2$ in every frame.

$$\frac{a}{b} = c$$

![Architecture figure](image://e2e-fig.png?w=200&h=100)');

INSERT INTO images (spec_id, version, name, mime_type, data, llm_readable) VALUES
    ('TS 23.501', '18.6.0', 'e2e-fig.png', 'image/png',
     X'89504E470D0A1A0A0000000D49484452000000010000000108060000001F15C4890000000D4944415478DA63FCCFC0500F000485018084A98C210000000049454E44AE426082',
     1);
`

func main() {
	addr := flag.String("addr", "127.0.0.1:8877", "listen address")
	flag.Parse()
	if err := run(*addr); err != nil {
		log.Fatalf("e2eserver: %v", err)
	}
}

func run(addr string) error {
	// Shut down on SIGINT/SIGTERM (Playwright's webServer gracefulShutdown
	// sends SIGTERM) so the deferred temp-dir cleanup actually runs.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	dir, err := os.MkdirTemp("", "3gpp-e2e-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(dir)

	d, err := db.OpenReadWrite(filepath.Join(dir, "e2e.db"))
	if err != nil {
		return fmt.Errorf("open db: %w", err)
	}
	defer d.Close()

	for _, script := range []string{db.Schema, testutil.SeedData, e2eSeed} {
		if err := d.ExecScript(script); err != nil {
			return fmt.Errorf("seed db: %w", err)
		}
	}

	// Mirror cmdServe's production wiring: the same stateless handler
	// (tools.NewStreamableHTTPHandler) mounted under /mcp/ next to the
	// viewer, so the webmcp.js bridge is exercised against the real thing.
	src := tools.NewSource(d)
	s := tools.NewServer(d, src, "e2e")
	mux := http.NewServeMux()
	mux.Handle("/mcp/", http.StripPrefix("/mcp", tools.NewStreamableHTTPHandler(s)))
	mux.Handle("/", web.NewServer(src))

	var handler http.Handler = mux
	coverDir := os.Getenv("TOBARI_E2E_COVERDIR")
	if coverDir != "" {
		handler = coverageMiddleware(mux)
	}

	srv := &http.Server{Addr: addr, Handler: handler}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("e2eserver listening on http://%s", addr)

	select {
	case err := <-errc:
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("shutdown: %w", err)
		}
		if coverDir != "" {
			if err := writeCoverage(coverDir); err != nil {
				return fmt.Errorf("write coverage: %w", err)
			}
		}
		return nil
	}
}

// coverageMiddleware scopes tobari coverage measurement to the Playwright
// test named by the X-Tobari-Scenario request header (set by the fixtures in
// e2e/tests/fixtures.ts). Requests without the header — Playwright's
// readiness poll, favicon fetches — stay unscoped so they never attribute
// coverage to a test. Without the tobari-instrumented build (see
// playwright.config.ts) CoverWithName degrades to plain fn() execution, but
// the middleware is only installed when TOBARI_E2E_COVERDIR is set anyway.
func coverageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.Header.Get("X-Tobari-Scenario")
		if name == "" {
			next.ServeHTTP(w, r)
			return
		}
		tobari.CoverWithName(name, func() { next.ServeHTTP(w, r) })
	})
}

// writeCoverage dumps the coverage gathered over the run into dir:
// e2e.cover (everything the process executed, go tool cover compatible),
// scenarios/<test>.cover (one scoped profile per Playwright test) and
// tobari.json/tobari.toon (per-scenario report for tooling and AI analysis).
func writeCoverage(dir string) error {
	scenarioDir := filepath.Join(dir, "scenarios")
	if err := os.MkdirAll(scenarioDir, 0o755); err != nil {
		return err
	}

	var merged bytes.Buffer
	tobari.WriteAllCoverprofile(tobari.SetMode, &merged)
	if err := os.WriteFile(filepath.Join(dir, "e2e.cover"), merged.Bytes(), 0o644); err != nil {
		return err
	}

	for name := range tobari.CoverprofileMap(tobari.SetMode) {
		var buf bytes.Buffer
		tobari.WriteCoverprofileByName(name, tobari.SetMode, &buf)
		out := filepath.Join(scenarioDir, scenarioFileName(name)+".cover")
		if err := os.WriteFile(out, buf.Bytes(), 0o644); err != nil {
			return err
		}
	}

	report := tobari.CollectCoverReport()
	js, err := json.Marshal(report)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "tobari.json"), js, 0o644); err != nil {
		return err
	}
	toon, err := report.MarshalTOON()
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "tobari.toon"), toon, 0o644)
}

// scenarioFileName flattens a Playwright test title into a safe file name.
func scenarioFileName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
}
