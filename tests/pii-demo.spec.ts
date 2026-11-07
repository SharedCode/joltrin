import { test, expect } from '@playwright/test';

/** The masking demo in the pricing section: the real text stays readable to screen readers, no motion on request, no sideways scroll. */

test.describe('PII masking demo', () => {
  test('the raw values fade out and the masks fade in', async ({ page }) => {
    await page.goto('/');
    await page.locator('#pii-demo').scrollIntoViewIfNeeded();
    await expect(page.locator('#pii-demo .pii-mask').first()).toHaveCSS('opacity', '1', { timeout: 6000 });
    await expect(page.locator('#pii-demo .pii-raw').first()).toHaveCSS('opacity', '0');
  });

  test('screen readers get a before and after sentence, not the animated spans', async ({ page }) => {
    await page.goto('/');
    const line = page.locator('#pii-demo .pii-line');
    await expect(line).toHaveAttribute('aria-label', /Before: .* After: /);
    await expect(line.locator('> span')).toHaveAttribute('aria-hidden', 'true');
  });

  test('reduced motion shows the raw text and no animation', async ({ browser }) => {
    const ctx = await browser.newContext({ reducedMotion: 'reduce' });
    const page = await ctx.newPage();
    await page.goto('/');
    await expect(page.locator('#pii-demo .pii-raw').first()).toHaveCSS('opacity', '1');
    await expect(page.locator('#pii-demo .pii-raw').first()).toHaveCSS('animation-name', 'none');
    await ctx.close();
  });

  test('no sideways scroll at 375px', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    await page.goto('/');
    await page.locator('#pii-demo').scrollIntoViewIfNeeded();
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
  });
});
