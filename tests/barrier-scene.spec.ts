import { test, expect, Page } from '@playwright/test';

/**
 * The barrier scene in the homepage hero: small agent figures walk toward a gate, and the one marked
 * risky is held until the earlier receipts are recorded. It is decoration, so these tests check what a
 * visitor must never lose to it: no errors, no sideways scroll, no stolen clicks, no motion for people
 * who ask for less, text that stays readable, and a page that still works with scripts off.
 */

const SCENE = '.bs';

// Only these properties may animate in the scene, so it never triggers layout.
const COMPOSITOR_ONLY = new Set(['transform', 'opacity', 'offset', 'easing', 'composite', 'computedOffset']);

async function sceneAnimations(page: Page) {
  return page.evaluate((sel) => {
    const scene = document.querySelector(sel)!;
    return scene.getAnimations({ subtree: true }).map((a) => ({
      state: a.playState,
      props: Object.keys((a.effect as KeyframeEffect).getKeyframes().reduce((acc, k) => ({ ...acc, ...k }), {})),
    }));
  }, SCENE);
}

test.describe('Hero barrier scene', () => {
  test('renders with no console errors or page errors from the site', async ({ page }) => {
    const problems: string[] = [];
    page.on('pageerror', (err) => problems.push(`pageerror: ${err.message}`));
    page.on('console', (msg) => {
      // A third-party host that is unreachable in the test run logs a failed load; only the site's own files count.
      if (msg.type() === 'error' && (msg.location().url || '').includes('127.0.0.1')) problems.push(`console: ${msg.text()}`);
    });
    await page.goto('/', { waitUntil: 'load' });
    await expect(page.locator(SCENE)).toHaveCount(1);
    await page.waitForTimeout(1500);
    expect(problems).toEqual([]);
  });

  test('is hidden from assistive tech, holds nothing to click or focus, and loads nothing', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const scene = page.locator(SCENE);
    await expect(scene).toHaveAttribute('aria-hidden', 'true');
    const facts = await page.evaluate((sel) => {
      const s = document.querySelector(sel)!;
      const svgs = [...s.querySelectorAll('svg')];
      return {
        svgsHidden: svgs.length === 2 && svgs.every((e) => e.getAttribute('aria-hidden') === 'true' && e.getAttribute('focusable') === 'false'),
        pointer: getComputedStyle(s).pointerEvents,
        focusable: s.querySelectorAll('a, button, input, select, textarea, [tabindex]').length,
        external: /https?:|<image|<script|<iframe|<use|<img/i.test(s.outerHTML),
        animated: [...s.querySelectorAll('[data-px]')].length,
      };
    }, SCENE);
    expect(facts.svgsHidden).toBe(true);
    expect(facts.pointer).toBe('none');
    expect(facts.focusable).toBe(0);
    expect(facts.external, 'the scene is inline vector shapes only').toBe(false);
    expect(facts.animated).toBeGreaterThan(0);
  });

  test('a click or tap on the scene reaches the page behind it', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await page.locator(SCENE).scrollIntoViewIfNeeded();
    const hit = await page.evaluate((sel) => {
      const r = document.querySelector(sel)!.getBoundingClientRect();
      const el = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
      return { insideScene: !!el && !!el.closest(sel), tag: el?.tagName };
    }, SCENE);
    expect(hit.insideScene, `the point is over ${hit.tag}, not the scene`).toBe(false);
  });

  for (const vp of [{ width: 375, height: 812 }, { width: 1440, height: 900 }]) {
    test(`no sideways scroll and a fixed height at ${vp.width}px`, async ({ page }) => {
      await page.setViewportSize(vp);
      await page.goto('/', { waitUntil: 'load' });
      const before = await page.locator(SCENE).boundingBox();
      await page.waitForTimeout(2500);
      const after = await page.locator(SCENE).boundingBox();
      expect(after!.height, 'the scene does not change size as it plays').toBe(before!.height);
      expect(after!.x).toBeGreaterThanOrEqual(-0.5);
      expect(after!.x + after!.width).toBeLessThanOrEqual(vp.width + 0.5);
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(overflow).toBeLessThanOrEqual(0);
    });
  }

  test('the loop runs from CSS alone and animates only transform and opacity', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await page.goto('/', { waitUntil: 'load' });
    const anims = await sceneAnimations(page);
    expect(anims.length, 'figures, gate and glyphs are animated').toBeGreaterThan(20);
    expect(anims.filter((a) => a.state === 'running').length).toBeGreaterThan(20);
    const stray = anims.flatMap((a) => a.props).filter((p) => !COMPOSITOR_ONLY.has(p));
    expect(stray, 'nothing but transform and opacity animates').toEqual([]);
  });

  test('reduced motion shows one still frame and runs no animation', async ({ page }) => {
    await page.emulateMedia({ reducedMotion: 'reduce' });
    await page.goto('/', { waitUntil: 'load' });
    expect(await sceneAnimations(page)).toEqual([]);
    const still = await page.evaluate((sel) => {
      const s = document.querySelector(sel)!;
      const r = s.getBoundingClientRect();
      return {
        height: r.height,
        figures: s.querySelectorAll('.mv').length,
        gate: !!s.querySelector('.gt'),
        parallax: [...s.querySelectorAll('[data-px]')].filter((e) => (e as SVGElement).style.transform).length,
        classes: s.className,
      };
    }, SCENE);
    expect(still.height).toBeGreaterThan(100);
    expect(still.figures).toBeGreaterThanOrEqual(8);
    expect(still.gate).toBe(true);
    expect(still.parallax, 'no scroll drift either').toBe(0);
    await page.evaluate(() => window.scrollTo(0, 600));
    await page.waitForTimeout(300);
    expect(await sceneAnimations(page)).toEqual([]);
  });

  test('still renders, and still plays, with JavaScript off', async ({ browser }) => {
    const context = await browser.newContext({ javaScriptEnabled: false, reducedMotion: 'no-preference', viewport: { width: 1440, height: 900 } });
    const page = await context.newPage();
    await page.goto('/', { waitUntil: 'load' });
    await expect(page.getByRole('heading', { level: 1 })).toContainText(/delete production/i);
    const box = await page.locator(SCENE).boundingBox();
    expect(box!.height).toBeGreaterThan(100);
    expect(await sceneAnimations(page)).not.toHaveLength(0);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
    await context.close();
  });

  test('pauses while off screen and resumes when it comes back', async ({ page }) => {
    await page.setViewportSize({ width: 1440, height: 900 });
    await page.emulateMedia({ reducedMotion: 'no-preference' });
    await page.goto('/', { waitUntil: 'load' });
    await page.locator(SCENE).scrollIntoViewIfNeeded();
    await expect(page.locator(SCENE)).not.toHaveClass(/\boff\b/);
    // Scroll drift is applied while the scene is on screen.
    await page.evaluate(() => window.scrollBy(0, 120));
    await expect.poll(() => page.evaluate(() => [...document.querySelectorAll('.bs [data-px]')].some((e) => (e as SVGElement).style.transform !== ''))).toBe(true);
    await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
    await expect(page.locator(SCENE)).toHaveClass(/\boff\b/);
    expect((await sceneAnimations(page)).every((a) => a.state === 'paused')).toBe(true);
    await page.locator(SCENE).scrollIntoViewIfNeeded();
    await expect(page.locator(SCENE)).not.toHaveClass(/\boff\b/);
    expect((await sceneAnimations(page)).some((a) => a.state === 'running')).toBe(true);
  });

  test('the intro video is still a click-to-load thumbnail with no iframe', async ({ page }) => {
    await page.goto('/', { waitUntil: 'load' });
    await page.waitForTimeout(1000);
    await expect(page.locator('#demo-video iframe')).toHaveCount(0);
    await expect(page.locator('#demo-video-play')).toHaveCount(1);
  });

  // The worst case behind any hero text is the page background with a glyph stroke at its highest
  // opacity (18%) on top of it. The test measures the real background under each line, blends the accent at
  // that opacity, and checks the ratio WCAG AA asks for.
  for (const scheme of ['dark', 'light'] as const) {
    test(`hero text keeps WCAG AA contrast over the scene in ${scheme} mode`, async ({ page }) => {
      await page.setViewportSize({ width: 1440, height: 900 });
      await page.emulateMedia({ colorScheme: scheme, reducedMotion: 'reduce' });
      await page.goto('/', { waitUntil: 'load' });
      await page.addStyleTag({ content: '#start{opacity:1!important;transform:none!important} *{caret-color:transparent!important}' });

      const maxGlyph = await page.evaluate(() => Math.max(...[...document.querySelectorAll('.bs .gl')].map((g) => parseFloat((g as SVGElement).style.getPropertyValue('--o')))));
      expect(maxGlyph, 'no glyph is drawn above 18% opacity').toBeLessThanOrEqual(0.18);
      const accent = await page.evaluate(() => getComputedStyle(document.querySelector('.bs')!).getPropertyValue('--bs-acc').trim());

      const targets = await page.locator('#start h1, #start h1 span, #start .text-center > p').all();
      expect(targets.length).toBeGreaterThanOrEqual(5);
      const worst: { text: string; ratio: number; need: number }[] = [];
      // Read every line's colour and size first: a colour transition left over from hiding the text would skew a later read.
      const infos = await Promise.all(targets.map((el) => el.evaluate((n) => {
        const cs = getComputedStyle(n);
        return { color: cs.color, size: parseFloat(cs.fontSize), weight: parseInt(cs.fontWeight, 10), text: (n.textContent || '').trim().slice(0, 30) };
      })));
      for (const [i, el] of targets.entries()) {
        const info = infos[i];
        await el.evaluate((n) => n.scrollIntoView({ block: 'center' }));
        // Photograph the background only: wait until the text is really transparent, then take the shot.
        {
          const hide = await page.addStyleTag({ content: '*{color:transparent!important;-webkit-text-fill-color:transparent!important;text-decoration-color:transparent!important}' });
          await expect.poll(() => el.evaluate((n) => getComputedStyle(n).color)).toBe('rgba(0, 0, 0, 0)');
          await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
          const shot = await el.screenshot();
          await hide.evaluate((s) => s.remove());
          const lums = await page.evaluate(async ([b64, acc, color]) => {
            const lin = (v: number) => { v /= 255; return v <= 0.03928 ? v / 12.92 : Math.pow((v + 0.055) / 1.055, 2.4); };
            const lum = (c: number[]) => 0.2126 * lin(c[0]) + 0.7152 * lin(c[1]) + 0.0722 * lin(c[2]);
            const toRgb = (s: string) => { const c = document.createElement('canvas').getContext('2d')!; c.fillStyle = s; c.fillRect(0, 0, 1, 1); const d = c.getImageData(0, 0, 1, 1).data; return [d[0], d[1], d[2]]; };
            const img = new Image();
            await new Promise((ok) => { img.onload = ok; img.src = 'data:image/png;base64,' + b64; });
            const cv = document.createElement('canvas'); cv.width = img.width; cv.height = img.height;
            const cx = cv.getContext('2d')!; cx.drawImage(img, 0, 0);
            const px = cx.getImageData(0, 0, cv.width, cv.height).data;
            const a = toRgb(acc); const t = toRgb(color);
            let worst = Infinity;
            for (let i = 0; i < px.length; i += 16) {
              const bg = [px[i], px[i + 1], px[i + 2]];
              const blended = bg.map((v, k) => v * 0.82 + a[k] * 0.18);
              for (const b of [bg, blended]) {
                const l1 = lum(t); const l2 = lum(b);
                worst = Math.min(worst, (Math.max(l1, l2) + 0.05) / (Math.min(l1, l2) + 0.05));
              }
            }
            return worst;
          }, [shot.toString('base64'), accent, info.color] as const);
          const large = info.size >= 24 || (info.size >= 18.66 && info.weight >= 700);
          worst.push({ text: info.text, ratio: lums, need: large ? 3 : 4.5 });
        }
      }
      const fails = worst.filter((w) => w.ratio < w.need);
      expect(fails, JSON.stringify(worst)).toEqual([]);
    });
  }
});
