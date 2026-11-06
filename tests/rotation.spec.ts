import { test, expect } from '@playwright/test';

const pages = ['/', '/about/', '/docs/', '/demo-agents/', '/demo-btree/', '/demo-intent/'];

test.describe('Rotating a tablet or phone', () => {
  test.use({ viewport: { width: 820, height: 1180 }, hasTouch: true });

  for (const path of pages) {
    test(`${path} fills the screen after rotating, without a reload`, async ({ page }) => {
      await page.goto(path, { waitUntil: 'domcontentloaded' });
      const original = await page.locator('meta[name="viewport"]').getAttribute('content');
      await page.setViewportSize({ width: 1180, height: 820 });
      await page.evaluate(() => window.dispatchEvent(new Event('orientationchange')));
      // The tag is changed for a moment so the browser lays out again, then put back as it was.
      await expect(page.locator('meta[name="viewport"]')).toHaveAttribute('content', original!);
      const sizes = await page.evaluate(() => ({ inner: window.innerWidth, scroll: document.documentElement.scrollWidth }));
      expect(sizes.inner).toBe(1180);
      expect(sizes.scroll).toBeLessThanOrEqual(sizes.inner);
    });
  }
});
