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
    // Nothing has unlocked audio yet, so the banner must not claim sound is playing.
    await expect(toggle).toHaveText('Tap for sound');
    await expect(toggle).toHaveAttribute('aria-pressed', 'false');
    await toggle.click();
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
    // The first click is the gesture that lets the browser start audio. It must not turn sound off.
    await toggle.click();
    await expect(toggle).toHaveText('Sound on');
    await toggle.click();
    await expect(toggle).toHaveText('Sound off');
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

test.describe('Homepage sound under the browser autoplay policy', () => {
  test('first click on the banner unlocks audio and plays without a toggle', async ({ page }) => {
    await page.addInitScript(() => {
      (window as any).__oscillators = 0;
      const Real = window.AudioContext;
      // Like Chrome and Safari: suspended until resume() is called from a gesture.
      (window as any).__lastPress = -1e9;
      ['pointerdown', 'keydown', 'touchstart'].forEach((ev) =>
        window.addEventListener(ev, (e) => { if (e.isTrusted) (window as any).__lastPress = performance.now(); }, true));
      class Policy extends Real {
        unlocked = false;
        get state() {
          return (this.unlocked ? 'running' : 'suspended') as AudioContextState;
        }
        resume() {
          // Browsers refuse to start audio unless the page was just given a real click, tap or key press.
          if (performance.now() - (window as any).__lastPress > 5000) return Promise.resolve();
          this.unlocked = true;
          this.dispatchEvent(new Event('statechange'));
          return Promise.resolve();
        }
        createOscillator() {
          (window as any).__oscillators += 1;
          return super.createOscillator();
        }
      }
      (window as any).AudioContext = Policy;
    });
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const toggle = page.locator('.sound-toggle');
    await expect(toggle).toHaveText('Tap for sound');
    // Scrolling is not a click, tap or key press, so the browser keeps audio locked.
    await page.evaluate(() => window.scrollBy(0, 4000));
    await page.waitForTimeout(600);
    expect(await page.evaluate(() => (window as any).__oscillators)).toBe(0);
    await toggle.click();
    await expect(toggle).toHaveText('Sound on');
    await page.evaluate(() => { (window as any).__oscillators = 0; });
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.waitForTimeout(200);
    await page.evaluate(() => window.scrollBy(0, 4000));
    await page.waitForTimeout(800);
    expect(await page.evaluate(() => (window as any).__oscillators)).toBeGreaterThan(0);
  });

  test('a click anywhere on the page unlocks audio and the banner follows', async ({ page }) => {
    await page.addInitScript(() => {
      (window as any).__lastPress = -1e9;
      ['pointerdown', 'keydown', 'touchstart'].forEach((ev) =>
        window.addEventListener(ev, (e) => { if (e.isTrusted) (window as any).__lastPress = performance.now(); }, true));
      const Real = window.AudioContext;
      class Policy extends Real {
        unlocked = false;
        get state() {
          return (this.unlocked ? 'running' : 'suspended') as AudioContextState;
        }
        resume() {
          // Browsers refuse to start audio unless the page was just given a real click, tap or key press.
          if (performance.now() - (window as any).__lastPress > 5000) return Promise.resolve();
          this.unlocked = true;
          this.dispatchEvent(new Event('statechange'));
          return Promise.resolve();
        }
      }
      (window as any).AudioContext = Policy;
    });
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const toggle = page.locator('.sound-toggle');
    await expect(toggle).toHaveText('Tap for sound');
    await page.mouse.click(300, 300);
    await expect(toggle).toHaveText('Sound on');
  });
});

test.describe('Homepage sound when the browser refuses audio', () => {
  test('stays silent and reports no error when resume() rejects', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await page.addInitScript(() => {
      (window as any).__oscillators = 0;
      const Real = window.AudioContext;
      class Refusing extends Real {
        get state() {
          return 'suspended' as AudioContextState;
        }
        resume() {
          return Promise.reject(new DOMException('Navigated away from page', 'InvalidStateError'));
        }
        createOscillator() {
          (window as any).__oscillators += 1;
          return super.createOscillator();
        }
      }
      (window as any).AudioContext = Refusing;
    });
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await page.locator('.sound-toggle').click();
    await page.locator('.sound-toggle').click();
    await page.evaluate(() => window.scrollBy(0, 4000));
    await page.waitForTimeout(600);
    expect(errors).toEqual([]);
    expect(await page.evaluate(() => (window as any).__oscillators)).toBe(0);
  });
});
