import { test, expect } from '@playwright/test';
import { createRequire } from 'node:module';
import { waitForWasmReady } from './helpers/wasm-lifecycle';

/**
 * Every page passes the automated WCAG 2 A and AA checks, in the dark and the
 * light theme. Automated checks catch roughly a third of real problems, so this
 * is a floor and not a certificate: it keeps the easy failures from coming back.
 */
const require = createRequire(import.meta.url);
const axePath: string = process.env.AXE_PATH || require.resolve('axe-core/axe.min.js');

const PAGES = ['/', '/private-intent/', '/agents/', '/btree/', '/docs/'];

for (const scheme of ['dark', 'light'] as const) {
  test.describe(`Accessibility, ${scheme} theme`, () => {
    test.use({ colorScheme: scheme });
    for (const path of PAGES) {
      test(`${path} has no WCAG A or AA violations`, async ({ page }) => {
        await page.goto(path, { waitUntil: 'load' });
        await waitForWasmReady(page);
        if (path === '/private-intent/') {
          await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
        }
        // Scan the settled text, rather than a partially transparent entrance frame.
        await page.evaluate(async () => {
          await document.fonts.ready;
          await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
          // Poll playState: WebKit can leave animation.finished unresolved for animations that have already finished.
          const settled = () => document.getAnimations()
            .filter(animation => Number.isFinite(animation.effect?.getComputedTiming().endTime))
            .every(animation => animation.playState !== 'running');
          const deadline = performance.now() + 5_000;
          while (!settled() && performance.now() < deadline) {
            await new Promise<void>(resolve => requestAnimationFrame(() => resolve()));
          }
        });
        await page.addScriptTag({ path: axePath });
        const result = await page.evaluate(async () =>
          // @ts-expect-error axe is injected above
          await axe.run(document, { runOnly: ['wcag2a', 'wcag2aa', 'wcag21aa', 'best-practice'] }),
        );
        const summary = result.violations.map((v: any) => `${v.id}: ${v.nodes.slice(0, 3).map((n: any) => n.target.join(' ')).join(' | ')}`);
        expect(summary, `${path}\n${summary.join('\n')}`).toEqual([]);
      });
    }
  });
}

// Explicit widths cover the first-run homepage rather than relying on device presets.
for (const width of [375, 1440]) {
  test.describe(`Homepage light theme at ${width}px`, () => {
    test.use({ colorScheme: 'light', viewport: { width, height: 900 }, reducedMotion: 'reduce' });
    test('accent labels and action buttons meet AA contrast', async ({ page }) => {
      await page.goto('/', { waitUntil: 'load' });
      await page.addScriptTag({ path: axePath });
      const violations = await page.evaluate(async () => {
        // @ts-expect-error axe is injected above
        const result = await axe.run(document, { runOnly: ['color-contrast'] });
        return result.violations.map((violation: { nodes: { target: string[]; failureSummary: string }[] }) =>
          violation.nodes.map(node => `${node.target.join(' ')}: ${node.failureSummary}`),
        ).flat();
      });
      expect(violations).toEqual([]);
    });
  });
}
