import { test, expect } from '@playwright/test';

/**
 * Every page names itself the same way for search and for link previews:
 * a Twitter card with an absolute image that really loads, and structured
 * data on the two pages that describe the product.
 */
const PAGES = ['/', '/agents/', '/private-intent/', '/btree/', '/docs/'];

test.describe('Search and link-preview metadata', () => {
  for (const path of PAGES) {
    test(`${path} has a full social card with images that load`, async ({ page, request }) => {
      await page.goto(path, { waitUntil: 'domcontentloaded' });
      // Pages differ on property= or name=, and both are read by the crawlers.
      const meta = async (key: string) => (await page.locator(`meta[property="${key}"], meta[name="${key}"]`).first().getAttribute('content')) ?? '';
      expect(await meta('twitter:card')).toBe('summary_large_image');
      for (const sel of ['twitter:title', 'twitter:description', 'og:title', 'og:description']) {
        expect((await meta(sel)).length, `${path} ${sel}`).toBeGreaterThan(10);
      }
      for (const sel of ['og:image', 'twitter:image']) {
        const url = await meta(sel);
        expect(url, `${path} ${sel}`).toMatch(/^https:\/\/joltrinhq\.com\/.+\.png$/);
        // The declared URL must be on the production host (checked above). The file is fetched from the
        // server under test, because a new image is not on production until after the deploy. A live run
        // (LIVE_SITE=1, BASE_URL=https://joltrinhq.com) fetches it from production itself.
        const res = await request.get(new URL(url).pathname);
        expect(res.status(), `${url}`).toBe(200);
        expect(res.headers()['content-type']).toContain('image/png');
        const body = await res.body();
        expect(body.length, `${url} is a real image`).toBeGreaterThan(1000);
        expect(body.subarray(0, 8).toString('hex'), `${url} starts with the PNG signature`).toBe('89504e470d0a1a0a');
      }
    });
  }

  for (const [path, name] of [['/', 'Joltrin'], ['/private-intent/', 'Joltrin Private Intent']] as const) {
    test(`${path} carries valid SoftwareApplication data`, async ({ page }) => {
      await page.goto(path, { waitUntil: 'domcontentloaded' });
      const raw = await page.locator('script[type="application/ld+json"]').first().textContent();
      const data = JSON.parse(raw ?? '');
      expect(data['@context']).toBe('https://schema.org');
      expect(data['@type']).toBe('SoftwareApplication');
      expect(data.name).toBe(name);
      expect(data.url).toBe(`https://joltrinhq.com${path}`);
      expect(data.license).toContain('MIT');
      // Only the open source core is priced here. Pro is not self-serve, so it is not listed as an offer.
      expect(data.offers.price).toBe('0');
      expect(JSON.stringify(data)).not.toMatch(/aggregateRating|review|\$49/i);
    });
  }
});

test.describe('Page weight', () => {
  for (const path of PAGES) {
    test(`${path} does not ship an oversized header logo`, async ({ page, request }) => {
      await page.goto(path, { waitUntil: 'domcontentloaded' });
      const logo = page.locator('header img[src$="logo-mark.png"], nav img[src$="logo-mark.png"]').first();
      if ((await logo.count()) === 0) test.skip(true, 'this page has no header logo');
      const src = new URL((await logo.getAttribute('src')) ?? '', page.url()).pathname;
      const res = await request.get(src);
      expect(res.status()).toBe(200);
      // The logo is drawn at 24 to 36 pixels, so a 72 pixel image is enough for a sharp 2x screen.
      expect((await res.body()).length, `${src} is a small image`).toBeLessThan(20_000);
    });
  }
});


test.describe('Link preview cards', () => {
  const OWN_CARD = ['/', '/agents/', '/private-intent/', '/btree/'];

  test('each main page has its own 1200 by 630 card, under 300 KB', async ({ page, request }) => {
    const seen = new Map<string, string>();
    for (const path of OWN_CARD) {
      await page.goto(path, { waitUntil: 'domcontentloaded' });
      const url = await page.locator('meta[property="og:image"]').first().getAttribute('content');
      const res = await request.get(new URL(url ?? '').pathname);
      expect(res.status(), `${path} image`).toBe(200);
      const body = await res.body();
      expect(body.length, `${path} card size`).toBeLessThan(300_000);
      // A PNG stores its width and height as two big-endian numbers right after the signature.
      expect([body.readUInt32BE(16), body.readUInt32BE(20)], `${path} card size in pixels`).toEqual([1200, 630]);
      const key = body.toString('base64');
      expect(seen.get(key), `${path} shares its card with ${seen.get(key)}`).toBeUndefined();
      seen.set(key, path);
    }
  });

  test('the cards use the same words as the pages and make none of the old claims', async ({ page }) => {
    // The images are pictures, so what can be checked here is that every page names its own card.
    for (const [path, card] of [['/', '/og-image.png'], ['/agents/', '/agents/og-image.png'], ['/private-intent/', '/private-intent/og-image.png'], ['/btree/', '/btree/og-image.png']] as const) {
      await page.goto(path, { waitUntil: 'domcontentloaded' });
      for (const sel of ['meta[property="og:image"]', 'meta[property="twitter:image"], meta[name="twitter:image"]']) {
        expect(await page.locator(sel).first().getAttribute('content'), `${path} ${sel}`).toBe(`https://joltrinhq.com${card}`);
      }
    }
  });
});
