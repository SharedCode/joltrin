import { test, expect, Page } from '@playwright/test';
import { waitForWasmReady } from './helpers/wasm-lifecycle';

/**
 * The Private Intent and Agents pages share one sound layer (assets/site-sound.js).
 * Audio stays locked until a press, plays on verdicts and on sections rising into view,
 * and is silent once switched off.
 */
async function countOscillators(page: Page) {
  await page.addInitScript(() => {
    (window as any).__oscillators = 0;
    (window as any).__lastPress = -1e9;
    ['pointerdown', 'keydown', 'touchstart'].forEach((ev) =>
      window.addEventListener(ev, (e) => { if (e.isTrusted) (window as any).__lastPress = performance.now(); }, true));
    const Real = window.AudioContext;
    class Counting extends Real {
      unlocked = false;
      get state() { return (this.unlocked ? 'running' : 'suspended') as AudioContextState; }
      resume() {
        if (performance.now() - (window as any).__lastPress > 5000) return Promise.resolve();
        this.unlocked = true;
        this.dispatchEvent(new Event('statechange'));
        return Promise.resolve();
      }
      createOscillator() { (window as any).__oscillators += 1; return super.createOscillator(); }
    }
    (window as any).AudioContext = Counting;
  });
}
const oscillators = (page: Page) => page.evaluate(() => (window as any).__oscillators as number);

test.describe('Sound on the Private Intent page', () => {
  test.beforeEach(async ({ page }) => {
    await countOscillators(page);
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
  });

  test('stays locked until a press, and the first press does not switch it off', async ({ page }) => {
    const toggle = page.locator('.site-sound');
    await expect(toggle).toHaveText('Tap for sound');
    await page.waitForTimeout(800);
    expect(await oscillators(page)).toBe(0);
    await toggle.click();
    await expect(toggle).toHaveText('Sound on');
  });

  test('a blocked attempt then an allowed one both make sound, and off is silent', async ({ page }) => {
    const toggle = page.locator('.site-sound');
    await toggle.click();
    await expect(toggle).toHaveText('Sound on');
    const base = await oscillators(page);
    await page.locator('#b-attempt').click();
    await expect.poll(() => oscillators(page)).toBeGreaterThan(base);
    await toggle.click();
    await expect(toggle).toHaveText('Sound off');
    const muted = await oscillators(page);
    await page.locator('#b-reset').click();
    await page.locator('#b-attempt').click();
    await page.waitForTimeout(500);
    expect(await oscillators(page)).toBe(muted);
  });
});

test.describe('Sound on the Agents page', () => {
  test.beforeEach(async ({ page }) => {
    await countOscillators(page);
    await page.goto('/agents/', { waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);
  });

  test('a blocked step and a committed step both make sound', async ({ page }) => {
    const toggle = page.locator('.site-sound');
    await toggle.click();
    await expect(toggle).toHaveText('Sound on');
    const base = await oscillators(page);
    await page.getByRole('button', { name: /3\. Drop Prod DB/ }).click();
    await expect(page.locator('#exec-log')).toContainText('BLOCKED');
    const afterBlock = await oscillators(page);
    expect(afterBlock).toBeGreaterThan(base);
    await page.getByRole('button', { name: /1\. Take Backup/ }).click();
    await expect(page.locator('#exec-log')).toContainText('committed');
    await expect.poll(() => oscillators(page)).toBeGreaterThan(afterBlock);
  });
});
