// Command e2eserver runs the web viewer against a seeded throwaway database
// for the Playwright end-to-end suite in e2e/. It reuses the canonical test
// seed data and adds a section exercising the browser-only features the suite
// covers: KaTeX math rendering and the image lightbox.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

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

	srv := &http.Server{Addr: addr, Handler: mux}
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
		return nil
	}
}
