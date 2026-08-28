import { defineConfig, devices } from '@playwright/test';

// E2E_COVERAGE=1 rebuilds the harness with tobari instrumentation
// (github.com/goccy/tobari, scoped Go coverage) and has it write profiles to
// TOBARI_E2E_COVERDIR on shutdown. `go tool tobari flags` builds the CLI from
// go.mod's tool directive, so it always matches the library version linked
// into the harness. modernc.org/sqlite is excluded from tobari's
// whole-program analysis: it is huge generated code and never calls back
// into this module.
const coverage = !!process.env.E2E_COVERAGE;
const coverDir = process.env.TOBARI_E2E_COVERDIR || 'coverage';
const serverCommand = 'go run ./e2eserver -addr 127.0.0.1:8877';

export default defineConfig({
  testDir: './tests',
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: process.env.CI ? [['list'], ['html', { open: 'never' }]] : 'list',
  use: {
    baseURL: 'http://127.0.0.1:8877',
    trace: 'on-first-retry',
    // Escape hatch for environments with a system-provided Chromium instead
    // of the Playwright-managed download (e.g. CHROMIUM_PATH=/usr/bin/chromium).
    ...(process.env.CHROMIUM_PATH
      ? { launchOptions: { executablePath: process.env.CHROMIUM_PATH } }
      : {}),
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  webServer: {
    // The harness seeds a throwaway SQLite database and serves the web
    // viewer with no network access; the first run also compiles it.
    // The instrumented build takes noticeably longer, hence the larger
    // startup timeout under coverage.
    command: coverage
      ? `TOBARI_E2E_COVERDIR=${coverDir} GOFLAGS="$(go tool tobari flags -exclude-analysis=modernc.org/sqlite)" ${serverCommand}`
      : serverCommand,
    url: 'http://127.0.0.1:8877/',
    reuseExistingServer: !process.env.CI,
    timeout: coverage ? 300_000 : 120_000,
    // SIGTERM (not the default SIGKILL) lets the harness remove its temp
    // database directory on the way out — and, under coverage, write the
    // profiles first, which needs the longer grace period.
    gracefulShutdown: { signal: 'SIGTERM', timeout: coverage ? 15_000 : 3000 },
  },
});
