import { test, expect } from './fixtures';

test('series select auto-submits the filter form', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByText('TS 29.510')).toBeVisible();

  // The <select> has onchange="this.form.submit()" — no submit button needed.
  await page.locator('#series').selectOption('23');
  await expect(page).toHaveURL(/\?series=23/);
  await expect(page.getByText('TS 23.501')).toBeVisible();
  await expect(page.getByText('TS 29.510')).toHaveCount(0);
});
