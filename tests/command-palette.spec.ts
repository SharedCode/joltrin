import { test, expect, type Page } from '@playwright/test';

// The sticky header is 56px tall. A section that is "in view" has its top just under it.
const sectionTop = (page: Page, id: string) =>
  page.evaluate((id) => Math.round(document.getElementById(id)!.getBoundingClientRect().top), id);

async function openSearch(page: Page, isMobile: boolean) {
  if (isMobile) {
    await page.locator('#mobile-menu-btn').tap();
    await page.locator('#mobile-menu-panel').getByRole('button', { name: /search/i }).tap();
  } else {
    await page.getByRole('button', { name: /open command palette/i }).click();
  }
  await expect(page.locator('#cmdk-overlay')).toBeVisible();
}

async function pick(page: Page, isMobile: boolean, query: string) {
  await page.locator('#cmdk-input').fill(query);
  const first = page.locator('#cmdk-list button').first();
  if (isMobile) await first.tap(); else await first.click();
}

async function expectInView(page: Page, id: string) {
  await expect.poll(() => sectionTop(page, id), { timeout: 15000 }).toBeGreaterThanOrEqual(56);
  await expect.poll(() => sectionTop(page, id), { timeout: 15000 }).toBeLessThanOrEqual(140);
  // Wait until it has stopped moving, so the page's own correction cannot run after the test scrolls away.
  let last = NaN, stable = 0;
  await expect.poll(async () => {
    const now = await sectionTop(page, id);
    stable = now === last ? stable + 1 : 0;
    last = now;
    return stable;
  }, { timeout: 15000, intervals: [400] }).toBeGreaterThanOrEqual(4);
}

test.describe('Search on the homepage', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
  });

  test('picking a section scrolls to it and closes the search', async ({ page, isMobile }) => {
    await openSearch(page, isMobile);
    await pick(page, isMobile, 'verification barrier');
    await expect(page.locator('#cmdk-overlay')).toBeHidden();
    await expectInView(page, 'verification-barrier');
    expect(new URL(page.url()).hash).toBe('#verification-barrier');
  });

  test('picking the section the URL already points at still scrolls to it', async ({ page, isMobile }) => {
    await page.goto('/#verification-barrier', { waitUntil: 'domcontentloaded' });
    await expectInView(page, 'verification-barrier');
    await page.evaluate(() => window.scrollTo(0, 0));
    await expect.poll(() => page.evaluate(() => Math.round(scrollY))).toBe(0);
    await openSearch(page, isMobile);
    await pick(page, isMobile, 'verification barrier');
    await expectInView(page, 'verification-barrier');
  });

  test('every section in the list can be reached', async ({ page, isMobile }) => {
    for (const [query, id] of [['taint barrier', 'taint-barrier'], ['durable memory', 'durable-memory'], ['under the hood', 'under-the-hood'], ['pricing', 'pricing'], ['enterprise', 'enterprise']]) {
      await page.evaluate(() => window.scrollTo(0, 0));
      await expect.poll(() => page.evaluate(() => Math.round(scrollY))).toBe(0);
      await openSearch(page, isMobile);
      await pick(page, isMobile, query);
      await expectInView(page, id);
    }
  });

  test('opening the page on a section hash does not hide its top under the header', async ({ page }) => {
    await page.goto('/#verification-barrier', { waitUntil: 'load' });
    await expectInView(page, 'verification-barrier');
  });

  test('moving over the results does not rebuild them under a finger', async ({ page }) => {
    await page.getByRole('button', { name: /open command palette/i }).or(page.locator('#mobile-menu-btn')).first().waitFor();
    await page.evaluate(() => openCommandPalette());
    const first = page.locator('#cmdk-list button').first();
    const handle = await first.elementHandle();
    await page.locator('#cmdk-list button').nth(2).dispatchEvent('mouseenter');
    expect(await handle!.evaluate((el) => el.isConnected)).toBe(true);
  });
});

test.describe('Search on the agents page', () => {
  test('picking the barrier demo scrolls to it', async ({ page, isMobile }) => {
    await page.goto('/agents/#demo-barrier', { waitUntil: 'domcontentloaded' });
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.evaluate(() => openCommandPalette());
    await page.locator('#cmdk-input').fill('agent verification');
    const first = page.locator('#cmdk-list button').first();
    if (isMobile) await first.tap(); else await first.click();
    await expectInView(page, 'demo-barrier');
  });
});
