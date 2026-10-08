import { test, expect, Page } from '@playwright/test';

/**
 * Smoke test for every page on the site, run against the site the deploy
 * publishes. It exists for the ways a page has looked broken while still
 * answering 200: a boot overlay that never left, a video that was never
 * published, a script that threw on load. Each test loads a page the way a
 * visitor would and checks what they would see.
 *
 * It also runs with every third-party host blocked, since one slow or blocked
 * CDN must not leave a blank page.
 */
const PAGES = ['/', '/agents/', '/arena/', '/docs/'];

// Smallest screenshot, in bytes, that counts as a page with something on it. A
// flat black or white screen compresses to a few KB.
const MIN_SCREENSHOT_BYTES = 15_000;

async function coverOnTop(page: Page): Promise<string> {
  return page.evaluate(() => {
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const top = document.elementFromPoint(vw / 2, vh / 2);
    for (let el: Element | null = top; el && el !== document.body && el !== document.documentElement; el = el.parentElement) {
      const cs = getComputedStyle(el);
      if (cs.position !== 'fixed' && cs.position !== 'absolute') continue;
      const r = el.getBoundingClientRect();
      if (r.width * r.height < 0.9 * vw * vh) continue;
      if (cs.visibility === 'hidden' || parseFloat(cs.opacity) < 0.05 || cs.pointerEvents === 'none') continue;
      const m = cs.backgroundColor.match(/rgba?\(([^)]+)\)/);
      const alpha = m ? (m[1].split(',').length > 3 ? parseFloat(m[1].split(',')[3]) : 1) : 0;
      if (alpha > 0.9) return `${el.tagName.toLowerCase()}${el.id ? '#' + el.id : ''}`;
    }
    return '';
  });
}

for (const blockThirdParty of [false, true]) {
  const mode = blockThirdParty ? 'with third-party hosts blocked' : 'as loaded normally';

  for (const path of PAGES) {
    test(`${path} shows its content ${mode}`, async ({ page, baseURL }) => {
      const origin = new URL(baseURL!).origin;
      const problems: string[] = [];

      page.on('pageerror', (err) => problems.push(`uncaught error: ${err.message}`));
      page.on('response', (res) => {
        if (res.url().startsWith(origin) && res.status() >= 400) problems.push(`HTTP ${res.status()} ${res.url()}`);
      });
      page.on('requestfailed', (req) => {
        if (req.url().startsWith(origin)) problems.push(`request failed ${req.url()}`);
      });

      if (blockThirdParty) {
        await page.route((url) => url.origin !== origin, (route) => route.abort());
      }

      const res = await page.goto(path, { waitUntil: 'load' });
      expect(res!.status(), `${path} status`).toBe(200);

      // Nothing opaque and full-screen may sit over the page once it has loaded.
      await expect
        .poll(() => coverOnTop(page), { message: `a full-screen element is covering ${path}`, timeout: 20_000 })
        .toBe('');

      // The page has to have a heading a visitor can see.
      await expect(page.locator('h1, h2').first()).toBeVisible();

      // The first screen is not blank.
      const shot = await page.screenshot();
      expect(shot.length, `${path} first screen looks blank`).toBeGreaterThan(MIN_SCREENSHOT_BYTES);

      // Same-origin images load, and no page carries a local video.
      expect(await page.locator('video').count(), `${path} has a <video> tag, videos are YouTube embeds`).toBe(0);
      const broken = await page.evaluate(() =>
        Array.from(document.images)
          .filter((i) => i.currentSrc.startsWith(location.origin) && i.complete && i.naturalWidth === 0)
          .map((i) => i.currentSrc),
      );
      expect(broken, `${path} images that did not load`).toEqual([]);

      // Every embed has a real size, so it is not a collapsed or black box.
      for (const frame of await page.locator('iframe').all()) {
        const box = await frame.boundingBox();
        expect(box, `${path} an iframe has no box`).not.toBeNull();
        expect(box!.width, `${path} an iframe has no width`).toBeGreaterThan(100);
        expect(box!.height, `${path} an iframe has no height`).toBeGreaterThan(50);
      }

      expect(problems, `${path} problems while loading`).toEqual([]);
    });
  }
}
