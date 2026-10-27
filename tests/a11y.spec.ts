import { test, expect } from '@playwright/test';
import { createRequire } from 'node:module';

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
        await page.waitForTimeout(1200);
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
