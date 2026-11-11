import { test, expect } from '@playwright/test';

// The pages that load /assets/site-motion.js. The script hides sections below
// the fold and reveals them on scroll. These tests hold the rules that make
// that safe: content on screen is never hidden, nothing stays hidden, and a
// visitor who asks for reduced motion gets none of it.
const pages = ['/agents/', '/btree/', '/about/', '/docs/'];

async function scrollThrough(page) {
  const height = await page.evaluate(() => document.documentElement.scrollHeight);
  for (let y = 0; y <= height; y += 500) {
    await page.evaluate((v) => window.scrollTo(0, v), y);
    await page.waitForTimeout(100);
  }
}

for (const path of pages) {
  test(`${path} reveals every section on scroll and hides nothing that is on screen`, async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await page.goto(path);
    await expect(page.locator('.sm-bar')).toBeAttached();
    const onScreenHidden = await page.evaluate(
      () => [...document.querySelectorAll('.sm-hide')].filter((e) => e.getBoundingClientRect().top < window.innerHeight * 0.9).length,
    );
    expect(onScreenHidden).toBe(0);
    await scrollThrough(page);
    await expect(page.locator('.sm-hide')).toHaveCount(0);
  });

  test(`${path} adds no motion for visitors who ask for reduced motion`, async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.goto(path);
    await page.waitForLoadState('load');
    await expect(page.locator('.sm-hide, .sm-bar, .sm-glow, .sm-card')).toHaveCount(0);
  });
}

test('a link to a section below the fold shows that section', async ({ page }) => {
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  await page.goto('/about/');
  await page.evaluate(() => { window.location.hash = 'gerardo-recinto'; });
  await expect(page.locator('#gerardo-recinto')).not.toHaveClass(/sm-hide/);
});
