import { test, expect } from '@playwright/test';

test('arrow keys navigate to next and previous chapters', async ({ page }) => {
  await page.goto('/specs/TS%2023.501/sections/1');

  await page.keyboard.press('ArrowRight');
  await expect(page).toHaveURL(/\/specs\/TS%2023\.501\/sections\/5$/);

  await page.keyboard.press('ArrowLeft');
  await expect(page).toHaveURL(/\/specs\/TS%2023\.501\/sections\/1$/);
});

test('arrow keys are ignored in inputs and with modifiers', async ({ page }) => {
  await page.goto('/specs/TS%2023.501/sections/1');

  await page.locator('.navbar-search-input').focus();
  await page.keyboard.press('ArrowRight');
  await page.locator('.navbar-search-input').blur();
  await page.keyboard.press('Shift+ArrowRight');

  // An unguarded press proves the handler is live. Had either guarded press
  // wrongly navigated (1 → 5), this one would land on 5.1 instead of 5, so
  // the final URL detects a broken guard without timing-based waits.
  await page.keyboard.press('ArrowRight');
  await expect(page).toHaveURL(/\/specs\/TS%2023\.501\/sections\/5$/);
});
