import { test, expect } from '@playwright/test';

test.describe('About the founders', () => {
  test('names both founders under one page heading', async ({ page }) => {
    await page.goto('/about/', { waitUntil: 'domcontentloaded' });
    await expect(page.getByRole('heading', { level: 1 })).toHaveText('The people behind Joltrin');
    await expect(page.getByRole('heading', { level: 2, name: 'Gerardo Recinto' })).toBeVisible();
    await expect(page.getByRole('heading', { level: 2, name: 'Gerard Louis Recinto' })).toBeVisible();
    await expect(page).toHaveTitle(/About the Founders/);
  });

  test('links to the public sources it cites and the jump links resolve', async ({ page }) => {
    await page.goto('/about/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('a[href="https://github.com/grecinto"]').first()).toBeVisible();
    for (const id of ['gerardo-recinto', 'gerard-louis-recinto']) {
      await expect(page.locator(`a[href="#${id}"]`)).toBeVisible();
      await expect(page.locator(`#${id}`)).toHaveCount(1);
    }
  });

  test('structured data lists two people and the copy has no em dashes', async ({ page }) => {
    await page.goto('/about/', { waitUntil: 'domcontentloaded' });
    const ld = JSON.parse(await page.locator('script[type="application/ld+json"]').innerText());
    expect(ld.about.map((p: { name: string }) => p.name)).toEqual(['Gerardo Recinto', 'Gerard Louis Recinto']);
    expect(await page.locator('main').innerText()).not.toContain('—');
  });

  test('does not publish a personal email address', async ({ page }) => {
    await page.goto('/about/', { waitUntil: 'domcontentloaded' });
    expect(await page.content()).not.toMatch(/@(gmail|yahoo|ucsd)\.[a-z]+/i);
  });

  test('has no horizontal scroll at 375px', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    await page.goto('/about/', { waitUntil: 'domcontentloaded' });
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  });

  test('lists what Gerard built beyond delivery', async ({ page }) => {
    await page.goto('/about/', { waitUntil: 'domcontentloaded' });
    await expect(page.getByRole('heading', { level: 3, name: 'What I built in Joltrin' })).toBeVisible();
    const text = await page.locator('main').innerText();
    for (const word of ['MCP server', 'A2A agent and bridge', 'Kubernetes admission gate']) expect(text).toContain(word);
  });
});
