import { test as base, expect } from '@playwright/test';

// Every request a test's page makes (documents, app.js fetches, the webmcp
// bridge's /mcp/ calls) carries the test's title path, so the harness can
// scope Go coverage per scenario under `make e2e-cover`. The harness ignores
// the header unless TOBARI_E2E_COVERDIR is set, so plain `make e2e` is
// unaffected.
export const test = base.extend({
  page: async ({ page }, use, testInfo) => {
    await page.setExtraHTTPHeaders({
      'X-Tobari-Scenario': testInfo.titlePath.join(' > '),
    });
    await use(page);
  },
});

export { expect };
