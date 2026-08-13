import { defineConfig, devices } from '@playwright/test';

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
    command: 'go run ./e2eserver -addr 127.0.0.1:8877',
    url: 'http://127.0.0.1:8877/',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
    // SIGTERM (not the default SIGKILL) lets the harness remove its temp
    // database directory on the way out.
    gracefulShutdown: { signal: 'SIGTERM', timeout: 3000 },
  },
});
