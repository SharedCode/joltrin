import { test, expect } from '@playwright/test';
import { waitForWasmReady } from './helpers/wasm-lifecycle';

/**
 * Homepage positioning, pricing honesty, and metadata.
 * Covers the simplified hero, the three live experiences, the open-core
 * plans, the Pro request flow on a static host, and the domain in metadata.
 */
test.describe('Homepage', () => {
  test('hero states the product and offers one primary action', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await expect(page.getByRole('heading', { level: 1 })).toContainText(/durable memory/i);
    await expect(page.getByRole('heading', { level: 1 })).toContainText(/verification barrier/i);
    await expect(page.getByRole('link', { name: /start building/i }).first()).toBeVisible();
    await expect(page.getByRole('link', { name: /try the live barrier/i })).toHaveAttribute('href', /agents/);
  });

  test('intro video sits below the hero and autoplays muted', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const frame = page.locator('iframe[title="Joltrin intro video"]');
    await expect(frame).toHaveAttribute('src', /youtube-nocookie\.com\/embed\/F0jYkBHJluI\?.*autoplay=1.*mute=1/);
    const frameY = (await frame.boundingBox())!.y;
    const heroY = (await page.getByRole('heading', { level: 1 }).boundingBox())!.y;
    expect(frameY).toBeGreaterThan(heroY);
  });

  test('header has a Watch demo button that jumps to the video', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const btn = page.locator('#watch-demo-btn');
    await expect(btn).toBeVisible();
    await expect(btn).toHaveAttribute('href', '#demo-video');
    await expect(page.locator('#demo-video iframe')).toHaveCount(1);
  });

  test('explains how to test the barrier with your own AI agent', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const section = page.locator('#test-your-agent');
    await expect(section).toContainText('sop-mcp-server');
    // The full path works when Go's bin folder is not on PATH; a bare command fails with ENOENT.
    await expect(section).toContainText('$(go env GOPATH)/bin/sop-mcp-server" setup --apply');
  });

  test('three live experiences are linked right after the hero', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const strip = page.locator('#live-experiences');
    await expect(strip.locator('a')).toHaveCount(3);
    await expect(strip.locator('a[href="#under-the-hood"]')).toBeVisible();
    await expect(strip.locator('a[href="./arena/"]')).toBeVisible();
    await expect(strip.locator('a[href="./agents/"]')).toBeVisible();
  });

  test('agent team demo replays blocked and allowed calls for both scenarios', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const log = page.locator('#team-log');
    await page.locator('#team-run').click();
    await expect(log).toContainText('BLOCKED grounding: claimed cpu_pct=97', { timeout: 10000 });
    await expect(log).toContainText('BLOCKED scope: aws.terminate_instances');
    await expect(log).toContainText('checkout-asg scaled 4 -> 6', { timeout: 10000 });
    await page.getByRole('tab', { name: 'PagerDuty incident' }).click();
    await expect(log).toBeEmpty();
    await page.locator('#team-run').click();
    await expect(log).toContainText('BLOCKED grounding: claimed error_rate_pct=40', { timeout: 10000 });
    await expect(log).toContainText('PD-77 resolved', { timeout: 10000 });
    await expect(log).toContainText('"missing_state":"recovery_confirmed"');
  });

  test('agent feedback callout explains the structured block and shows the replay', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const callout = page.locator('#agent-feedback');
    await expect(callout).toContainText('A block is feedback, not a dead end');
    await expect(callout).toContainText('which rule tripped');
    await expect(callout.getByRole('link', { name: /agent test results/i })).toHaveAttribute('href', /AGENT_BARRIER_TESTS\.md#does-the-agent-act-on-the-block-feedback/);
    const img = callout.locator('img');
    await img.scrollIntoViewIfNeeded();
    await expect(img).toBeVisible();
    await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
  });

  test('Docs is in the header, the mobile menu, and the footer, and points at /docs/', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    // Desktop header, mobile menu panel, and footer.
    expect(await page.locator('a[href="./docs/"]').count()).toBeGreaterThanOrEqual(3);
    expect(await page.locator('header nav a[href="./docs/"]').count()).toBeGreaterThanOrEqual(1);
    expect(await page.locator('#mobile-menu-panel a[href="./docs/"]').count()).toBe(1);
  });

  test('/docs/ is a real page on the site that lists the documentation', async ({ page, request }) => {
    const res = await request.get('/docs/');
    expect(res.status()).toBe(200);
    const html = await res.text();
    expect(html).toContain('<title>Documentation | Joltrin</title>');
    expect(html).toContain('<link rel="canonical" href="https://joltrinhq.com/docs/">');

    await page.goto('/docs/', { waitUntil: 'domcontentloaded' });
    await expect(page.getByRole('heading', { level: 1, name: 'Documentation' })).toBeVisible();
    await expect(page.locator('nav[aria-label="Main"] [aria-current="page"]')).toHaveText('Docs');
    // Back to the other experiences from the docs page.
    await expect(page.locator('nav[aria-label="Main"] a[href="../agents/"]')).toBeVisible();

    const hrefs = await page.$$eval('main a[href]', (as) => as.map((a) => (a as HTMLAnchorElement).href));
    expect(hrefs.length).toBeGreaterThanOrEqual(15);
    for (const h of hrefs) {
      expect(h, 'every documentation link goes to the repository over https').toMatch(/^https:\/\/github\.com\/SharedCode\/joltrin\//);
    }
    const unsafe = await page.$$eval('main a[target="_blank"]', (as) => as.filter((a) => !/noopener/.test(a.rel) || !/noreferrer/.test(a.rel)).length);
    expect(unsafe, 'external links must carry rel="noopener noreferrer"').toBe(0);
  });

  test('pricing shows open source, Pro, and Enterprise contact without live-checkout claims', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const pricing = page.locator('#pricing');
    await expect(pricing).toContainText('$0');
    await expect(pricing).toContainText('$49');
    await expect(pricing.getByRole('button', { name: /talk to us/i })).toBeVisible();
    await expect(pricing.getByRole('button', { name: /request pro/i })).toBeVisible();
    const text = (await pricing.innerText()).toLowerCase();
    for (const claim of ['instant workspace', 'apple pay', 'google pay', 'automated stripe', 'most popular']) {
      expect(text, `pricing must not say "${claim}"`).not.toContain(claim);
    }
  });

  test('requesting Pro on the static host falls back to email instead of faking checkout', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);
    await page.locator('#pricing').getByRole('button', { name: /request pro/i }).click();
    const modal = page.locator('#pro-checkout-modal');
    await expect(modal).toBeVisible();
    await expect(modal).not.toContainText(/annual|\$490/i);
    await page.fill('#pro-team-name', 'example-team');
    await page.fill('#pro-admin-email', 'admin@example.test');
    await modal.getByRole('button', { name: /^request pro$/i }).click();
    await expect(page.locator('#pro-checkout-status')).toContainText(/isn't available on this site yet/i, { timeout: 10_000 });
    await expect(page.locator('#pro-checkout-status a[href^="mailto:"]')).toBeVisible();
  });

  test('canonical and social metadata point at joltrinhq.com on all three pages', async ({ request }) => {
    for (const [path, expected] of [
      ['/', 'https://joltrinhq.com/'],
      ['/agents/', 'https://joltrinhq.com/agents/'],
      ['/arena/', 'https://joltrinhq.com/arena/'],
    ]) {
      const html = await (await request.get(path)).text();
      expect(html, path).toContain(`<link rel="canonical" href="${expected}"`);
      expect(html, path).toContain(`property="og:url" content="${expected}"`);
      expect(html, path).not.toMatch(/https:\/\/joltrin\.com(?:[\/"'\s?#:]|$)/);
    }
  });

  test('page does not scroll sideways', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });
});
