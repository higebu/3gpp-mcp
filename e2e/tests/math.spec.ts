import { test, expect } from '@playwright/test';

test('KaTeX renders inline and display math', async ({ page }) => {
  await page.goto('/specs/TS%2023.501/sections/9');

  // app.js renders the raw LaTeX inside each span in place; a .katex child
  // only exists if KaTeX actually ran.
  await expect(page.locator('.math-inline .katex')).toBeVisible();
  await expect(page.locator('.math-inline .katex-html')).toBeVisible();
  await expect(page.locator('.math-display .katex-display')).toBeVisible();
});
