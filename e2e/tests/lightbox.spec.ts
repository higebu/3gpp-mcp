import { test, expect } from './fixtures';

test('clicking a figure opens the lightbox, clicking again closes it', async ({ page }) => {
  await page.goto('/specs/TS%2023.501/sections/9');
  const figure = page.locator('.section-body img');

  // app.js turns figures into focusable buttons — proves the enhancement ran.
  await expect(figure).toHaveAttribute('role', 'button');
  await expect(figure).toHaveAttribute('tabindex', '0');

  await figure.click();
  const lightbox = page.locator('dialog.lightbox');
  await expect(lightbox).toBeVisible();
  await expect(lightbox.locator('img')).toHaveAttribute('src', /\/images\/e2e-fig\.png/);

  await lightbox.click();
  await expect(lightbox).toBeHidden();
});

test('keyboard opens and closes the lightbox with focus restored', async ({ page }) => {
  await page.goto('/specs/TS%2023.501/sections/9');
  const figure = page.locator('.section-body img');
  const lightbox = page.locator('dialog.lightbox');

  await figure.focus();
  await page.keyboard.press('Enter');
  await expect(lightbox).toBeVisible();

  // Escape is native <dialog> behavior; closing restores focus to the figure.
  await page.keyboard.press('Escape');
  await expect(lightbox).toBeHidden();
  await expect(figure).toBeFocused();
});
