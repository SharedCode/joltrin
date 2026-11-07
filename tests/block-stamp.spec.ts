import { test, expect } from '@playwright/test';

/** The BLOCKED stamp on the block-result example is decoration: it must not change the JSON, steal clicks, or move for people who ask for less motion. */

test.describe('Block result stamp', () => {
  test('stamp lands over the example and the JSON text is unchanged', async ({ page }) => {
    await page.goto('/');
    await page.locator('#block-result').scrollIntoViewIfNeeded();
    const stamp = page.locator('#block-result .block-stamp');
    await expect(stamp).toHaveCSS('opacity', '1', { timeout: 5000 });
    await expect(stamp).toHaveAttribute('aria-hidden', 'true');
    await expect(stamp).toHaveCSS('pointer-events', 'none');
    const json = await page.locator('#block-result pre').innerText();
    expect(json).toContain('"blocked_by": "precondition"');
    expect(json).not.toContain('BLOCKED');
  });

  test('no sideways scroll at 375px', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    await page.goto('/');
    await page.locator('#block-result').scrollIntoViewIfNeeded();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > document.documentElement.clientWidth);
    expect(overflow).toBe(false);
  });

  test('reduced motion shows the stamp with no animation', async ({ browser }) => {
    const ctx = await browser.newContext({ reducedMotion: 'reduce' });
    const page = await ctx.newPage();
    await page.goto('/');
    const stamp = page.locator('#block-result .block-stamp');
    await expect(stamp).toHaveCSS('opacity', '1');
    await expect(stamp).toHaveCSS('animation-name', 'none');
    await ctx.close();
  });
});
