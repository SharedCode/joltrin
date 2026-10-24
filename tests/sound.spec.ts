import { test, expect } from '@playwright/test';

/**
 * The homepage sound layer is opt-in. It must stay silent until the toggle is
 * pressed, then play when the scroll story changes the verdict.
 */
test.describe('Homepage sound toggle', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => {
      (window as any).__oscillators = 0;
      const Real = window.AudioContext;
      class Counting extends Real {
        createOscillator() {
          (window as any).__oscillators += 1;
          return super.createOscillator();
        }
      }
      (window as any).AudioContext = Counting;
    });
    await page.goto('/', { waitUntil: 'domcontentloaded' });
  });

  test('starts off and switches on and off', async ({ page }) => {
    const toggle = page.locator('.sound-toggle');
    await expect(toggle).toHaveText('Sound off');
    await expect(toggle).toHaveAttribute('aria-pressed', 'false');
    await toggle.click();
    await expect(toggle).toHaveText('Sound on');
    await expect(toggle).toHaveAttribute('aria-pressed', 'true');
    await toggle.click();
    await expect(toggle).toHaveText('Sound off');
  });

  test('makes no sound while off, plays on the verdict once on', async ({ page }) => {
    const story = page.locator('#story');
    await story.scrollIntoViewIfNeeded();
    await page.evaluate(() => window.scrollBy(0, 3000));
    await page.waitForTimeout(600);
    expect(await page.evaluate(() => (window as any).__oscillators)).toBe(0);

    await page.locator('.sound-toggle').click();
    await page.evaluate(() => { (window as any).__oscillators = 0; });
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.waitForTimeout(200);
    await page.evaluate(() => window.scrollBy(0, 4000));
    await page.waitForTimeout(800);
    expect(await page.evaluate(() => (window as any).__oscillators)).toBeGreaterThan(0);
  });
});
