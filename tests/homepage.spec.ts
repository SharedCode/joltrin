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

  test('explains how to test the barrier with your own AI agent', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const section = page.locator('#test-your-agent');
    await expect(section).toContainText('sop-mcp-server');
    await expect(section).toContainText('claude mcp add');
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
