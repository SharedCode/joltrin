import { test, expect, Page, Locator } from '@playwright/test';

// Composite CSS colors and group opacity, including the disabled-to-ready frame.
// The selected labels have solid backgrounds and no text shadows or image fills.
async function installMeasurement(page: Page) {
  await page.addInitScript(() => {
    const rgba = (value: string) => {
      const parts = value.match(/[\d.]+/g)!.map(Number);
      return [parts[0], parts[1], parts[2], parts[3] ?? 1];
    };
    const over = (a: number[], b: number[]) => {
      const alpha = a[3] + b[3] * (1 - a[3]);
      return alpha ? [...a.slice(0, 3).map((v, i) => (v * a[3] + b[i] * b[3] * (1 - a[3])) / alpha), alpha] : [0, 0, 0, 0];
    };
    const luminance = (color: number[]) => color.slice(0, 3).map(v => {
      const c = v / 255;
      return c <= .04045 ? c / 12.92 : ((c + .055) / 1.055) ** 2.4;
    }).reduce((sum, v, i) => sum + v * [.2126, .7152, .0722][i], 0);
    (window as any).contrastSnapshot = (element: Element) => {
      let foreground = rgba(getComputedStyle(element).color);
      let background = [0, 0, 0, 0];
      for (let parent: Element | null = element; parent; parent = parent.parentElement) {
        const style = getComputedStyle(parent);
        const fill = rgba(style.backgroundColor);
        foreground = over(foreground, fill);
        background = over(background, fill);
        foreground[3] *= Number(style.opacity);
        background[3] *= Number(style.opacity);
      }
      foreground = over(foreground, [255, 255, 255, 1]);
      background = over(background, [255, 255, 255, 1]);
      const f = luminance(foreground), b = luminance(background);
      return { ratio: (Math.max(f, b) + .05) / (Math.min(f, b) + .05), foreground, background, opacity: getComputedStyle(element).opacity };
    };
  });
}

async function snapshot(target: Locator) {
  return target.evaluate(element => (window as any).contrastSnapshot(element));
}

for (const width of [375, 1440]) {
  test.describe(`Dark interaction contrast at ${width}px`, () => {
    test.use({ colorScheme: 'dark', viewport: { width, height: 900 }, reducedMotion: 'no-preference' });

    test('violet callout and badge keep a contrast margin at rest, hover, and focus', async ({ page }) => {
      await installMeasurement(page);
      await page.goto('/', { waitUntil: 'load' });
      const link = page.getByRole('link', { name: 'Launch Agents Barrier Demo' });
      const badge = page.locator('#durable-memory .text-accent-violet').first();
      for (const target of [link, badge]) {
        await target.scrollIntoViewIfNeeded();
        // Wait for this element's real reveal transition to finish, without
        // disabling motion or hiding the text that the existing axe scan checks.
        await target.evaluate(async element => {
          const section = element.closest('section');
          if (section?.classList.contains('reveal')) {
            await new Promise<void>(resolve => {
              if (section.classList.contains('in') && getComputedStyle(section).opacity === '1') return resolve();
              const check = () => getComputedStyle(section).opacity === '1' ? resolve() : requestAnimationFrame(check);
              requestAnimationFrame(check);
            });
          }
        });
        expect((await snapshot(target)).ratio, JSON.stringify(await snapshot(target))).toBeGreaterThanOrEqual(5.5);
      }
      await link.scrollIntoViewIfNeeded();
      await link.hover();
      expect((await snapshot(link)).ratio, JSON.stringify(await snapshot(link))).toBeGreaterThanOrEqual(5.5);
      await link.focus();
      await expect(link).toBeFocused();
      expect((await snapshot(link)).ratio).toBeGreaterThanOrEqual(5.5);
    });

    test('private-intent controls are readable on their first enabled frame and while interacting', async ({ page }) => {
      await installMeasurement(page);
      let release: (() => void) | undefined;
      const engineGate = new Promise<void>(resolve => { release = resolve; });
      await page.route('**/intent.wasm', async route => { await engineGate; await route.continue(); });
      await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
      await page.evaluate(() => {
        const ids = ['b-attempt', 'b-backup', 'b-approve'];
        (window as any).readyContrast = [];
        const observer = new MutationObserver(() => {
          const buttons = ids.map(id => document.getElementById(id) as HTMLButtonElement);
          if (buttons.every(button => !button.disabled)) {
            (window as any).readyContrast = buttons.map(button => ({ id: button.id, ...(window as any).contrastSnapshot(button) }));
            observer.disconnect();
          }
        });
        ids.forEach(id => observer.observe(document.getElementById(id)!, { attributes: true, attributeFilter: ['disabled'] }));
      });
      release!();
      await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
      const ready = await page.evaluate(() => (window as any).readyContrast);
      expect(ready).toHaveLength(3);
      for (const value of ready) expect(value.ratio, JSON.stringify(value)).toBeGreaterThanOrEqual(4.5);
      for (const id of ['b-attempt', 'b-backup', 'b-approve']) {
        const button = page.locator(`#${id}`);
        await button.hover();
        expect((await snapshot(button)).ratio).toBeGreaterThanOrEqual(5.5);
        await button.focus();
        await expect(button).toBeFocused();
        expect((await snapshot(button)).ratio).toBeGreaterThanOrEqual(5.5);
      }
    });
  });
}
