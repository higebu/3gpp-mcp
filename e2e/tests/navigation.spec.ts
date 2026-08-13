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
  const url = page.url();

  await page.locator('.navbar-search-input').focus();
  await page.keyboard.press('ArrowRight');
  await page.waitForTimeout(300);
  expect(page.url()).toBe(url);

  await page.locator('.navbar-search-input').blur();
  await page.keyboard.press('Shift+ArrowRight');
  await page.waitForTimeout(300);
  expect(page.url()).toBe(url);
});
