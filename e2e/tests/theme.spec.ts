import { test, expect } from './fixtures';

test('dark mode toggle persists across reload', async ({ page }) => {
  await page.goto('/');
  // Playwright emulates prefers-color-scheme: light by default.
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light');

  await page.locator('#theme-toggle').click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
  expect(await page.evaluate(() => localStorage.getItem('theme'))).toBe('dark');

  // The inline script in <head> must re-apply the stored theme before paint.
  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark');
});

test('settings popover selects code theme, persists and closes', async ({ page }) => {
  await page.goto('/');
  const toggle = page.locator('#settings-toggle');
  const popover = page.locator('#settings-popover');
  await expect(popover).toBeHidden();

  await toggle.click();
  await expect(popover).toBeVisible();
  await expect(toggle).toHaveAttribute('aria-expanded', 'true');

  await popover.locator('input[name="code-theme"][value="monokai"]').check();
  await expect(page.locator('html')).toHaveAttribute('data-code-theme', 'monokai');
  expect(await page.evaluate(() => localStorage.getItem('codeTheme'))).toBe('monokai');

  await page.keyboard.press('Escape');
  await expect(popover).toBeHidden();
  await expect(toggle).toBeFocused();

  await toggle.click();
  await expect(popover).toBeVisible();
  // Click a non-navigating element so the hidden state can only come from
  // the outside-click handler, not a fresh page load.
  await page.getByRole('heading', { name: '3GPP Specifications' }).click();
  await expect(popover).toBeHidden();

  await page.reload();
  await expect(page.locator('html')).toHaveAttribute('data-code-theme', 'monokai');
});
