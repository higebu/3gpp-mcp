import { test, expect } from '@playwright/test';

const ALL_TOOLS = [
  'list_specs', 'list_versions', 'get_toc', 'get_section', 'get_asn1',
  'compare_versions', 'search', 'list_openapi', 'get_openapi',
  'search_openapi', 'get_references', 'list_images', 'get_image',
];

// Installs a stub document.modelContext before any page script runs, so
// webmcp.js registers its tools against it. Registrations are kept on
// window for the assertions.
const installStub = (page: import('@playwright/test').Page) =>
  page.addInitScript(() => {
    (window as any).__registered = [];
    (document as any).modelContext = {
      registerTool(tool: any) {
        (window as any).__registered.push(tool);
      },
    };
  });

test('registers every MCP tool with schema and read-only annotations', async ({ page }) => {
  await installStub(page);
  await page.goto('/');

  // Registration happens after an async tools/list round-trip. Wait for at
  // least the expected count so an extra tool fails the readable toEqual
  // assertion below instead of an opaque timeout here.
  await page.waitForFunction(
    (n) => (window as any).__registered.length >= n,
    ALL_TOOLS.length,
  );

  const tools = await page.evaluate(() =>
    (window as any).__registered.map((t: any) => ({
      name: t.name,
      description: t.description,
      hasSchema: !!t.inputSchema && typeof t.inputSchema === 'object',
      readOnlyHint: t.annotations?.readOnlyHint,
      untrustedContentHint: t.annotations?.untrustedContentHint,
      hasExecute: typeof t.execute === 'function',
    })),
  );

  expect(tools.map((t: any) => t.name).sort()).toEqual([...ALL_TOOLS].sort());
  for (const tool of tools) {
    expect(tool.description, tool.name).toBeTruthy();
    expect(tool.hasSchema, tool.name).toBe(true);
    expect(tool.readOnlyHint, tool.name).toBe(true);
    expect(tool.untrustedContentHint, tool.name).toBe(true);
    expect(tool.hasExecute, tool.name).toBe(true);
  }
});

test('execute calls through the real /mcp/ endpoint', async ({ page }) => {
  await installStub(page);
  await page.goto('/');
  await page.waitForFunction(
    (n) => (window as any).__registered.length >= n,
    ALL_TOOLS.length,
  );

  const result = await page.evaluate(() => {
    const tool = (window as any).__registered.find((t: any) => t.name === 'list_specs');
    return tool.execute({});
  });

  expect(result.isError).toBeFalsy();
  expect(Array.isArray(result.content)).toBe(true);
  const text = result.content.map((c: any) => c.text ?? '').join('\n');
  // Seeded by the harness (internal/testutil.SeedData).
  expect(text).toContain('TS 23.501');
});

test('page loads cleanly when the browser has no modelContext', async ({ page }) => {
  const errors: string[] = [];
  page.on('console', (msg) => {
    if (msg.type() === 'error') {
      errors.push(msg.text());
    }
  });
  page.on('pageerror', (err) => errors.push(String(err)));

  await page.goto('/');
  await expect(page.getByText('TS 23.501')).toBeVisible();
  expect(errors).toEqual([]);
});
