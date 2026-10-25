import { test, expect } from '@playwright/test';

/**
 * The homepage must say what Joltrin does that an AI cannot copy, link to the
 * Private Intent demo, and stay inside a phone screen.
 */
test.describe('Homepage unique value section', () => {
  test('explains the signed-proof difference and links to the demo', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const section = page.locator('#only-joltrin');
    await expect(section.getByRole('heading', { level: 2 })).toContainText('cannot sign for your backup system');
    await expect(section).toContainText('Your rules stay private');
    await expect(section).toContainText('Other systems sign, not the agent');
    await expect(section).toContainText('The tool that holds the keys checks');
    await expect(section.getByRole('link', { name: /Watch an agent get blocked/ })).toHaveAttribute('href', './private-intent/');
  });

  test('fits a phone screen without sideways scrolling', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await page.locator('#only-joltrin').scrollIntoViewIfNeeded();
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });
});
