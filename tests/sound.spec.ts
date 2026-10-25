import { test, expect } from '@playwright/test';

/**
 * The homepage sound layer is on by default and can be turned off. It plays
 * when the scroll story changes the verdict and stays silent once switched off.
 */
test.describe('Homepage sound toggle', () => {
  test.beforeEach(async ({ page }) => {
    await page.addInitScript(() => {
      (window as any).__oscillators = 0;
      const Real = window.AudioContext;
      class Counting extends Real {
        // CI machines have no audio device, so a real context can stay suspended. Report it running so the sound path is what is tested.
        get state() {
          return 'running' as AudioContextState;
        }
        createOscillator() {
          (window as any).__oscillators += 1;
          return super.createOscillator();
        }
      }
      (window as any).AudioContext = Counting;
    });
    await page.goto('/', { waitUntil: 'domcontentloaded' });
  });

  test('starts on and switches off and on', async ({ page }) => {
    const toggle = page.locator('.sound-toggle');
    await expect(toggle).toHaveText('Sound on');
    await expect(toggle).toHaveAttribute('aria-pressed', 'true');
    await toggle.click();
    await expect(toggle).toHaveText('Sound off');
    await expect(toggle).toHaveAttribute('aria-pressed', 'false');
    await toggle.click();
    await expect(toggle).toHaveText('Sound on');
  });

  test('plays after the first gesture, and is silent once switched off', async ({ page }) => {
    const toggle = page.locator('.sound-toggle');
    // The first click is the gesture that lets the browser start audio, and it turns sound off.
    await toggle.click();
    await page.evaluate(() => { (window as any).__oscillators = 0; });
    await page.evaluate(() => window.scrollBy(0, 4000));
    await page.waitForTimeout(600);
    expect(await page.evaluate(() => (window as any).__oscillators)).toBe(0);

    await toggle.click();
    await page.evaluate(() => { (window as any).__oscillators = 0; });
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.waitForTimeout(200);
    await page.evaluate(() => window.scrollBy(0, 4000));
    await page.waitForTimeout(800);
    expect(await page.evaluate(() => (window as any).__oscillators)).toBeGreaterThan(0);
  });
});
