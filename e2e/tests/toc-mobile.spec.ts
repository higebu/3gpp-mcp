import { test, expect } from './fixtures';

test.use({ viewport: { width: 375, height: 667 } });

test('mobile TOC drawer opens, closes and follows links', async ({ page }) => {
  await page.goto('/specs/TS%2023.501/sections/5');
  const sidebar = page.locator('#toc-sidebar');

  await page.locator('#toc-toggle').click();
  await expect(sidebar).toHaveClass(/open/);

  await page.locator('#toc-close').click();
  await expect(sidebar).not.toHaveClass(/open/);

  await page.locator('#toc-toggle').click();
  await expect(sidebar).toHaveClass(/open/);
  await sidebar.getByRole('link', { name: '5.1.1 Overview' }).click();
  await expect(page).toHaveURL(/\/sections\/5\.1\.1$/);
});
