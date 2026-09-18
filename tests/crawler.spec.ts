import { test, expect } from '@playwright/test';
import { NetworkConsoleGuard } from './helpers/network-console-guard';
import { waitForWasmReady } from './helpers/wasm-lifecycle';

/**
 * Staff QA & Automation Engineering - Suite 1: Crawler & Link Integrity
 *
 * Traverses all routes, anchor tags, and internal docs links to guarantee:
 *  1. Zero 404s, 500s, broken assets (scripts, images, WASM binaries, fonts)
 *  2. Pure browser console (zero uncaught exceptions, zero console.error calls)
 *  3. External link security attributes (rel="noopener noreferrer", valid HTTPS schemas)
 */

test.describe('Crawler & Deep Link Integrity Suite', () => {
  test('Recursive Spider: Discover and validate all internal routes and assets', async ({
    page,
    baseURL,
  }) => {
    const guard = new NetworkConsoleGuard(page);

    const visitedUrls = new Set<string>();
    const urlsToVisit: string[] = ['/', '/arena/', '/agents/'];
    const origin = new URL(baseURL || 'http://127.0.0.1:4173').origin;

    while (urlsToVisit.length > 0) {
      const currentRelativePath = urlsToVisit.shift()!;
      const targetUrl = new URL(currentRelativePath, origin).toString();

      if (visitedUrls.has(targetUrl)) {
        continue;
      }
      visitedUrls.add(targetUrl);

      // Navigate to route
      const response = await page.goto(targetUrl, { waitUntil: 'domcontentloaded' });
      expect(response, `Response for ${targetUrl} should not be null`).not.toBeNull();
      expect(
        response!.status(),
        `Expected HTTP 200 for ${targetUrl}, received ${response!.status()}`
      ).toBe(200);

      // Verify no 404 text in body
      const bodyText = await page.innerText('body');
      expect(bodyText).not.toContain('404 Not Found');

      // Wait for WASM engine if applicable on this page
      if (currentRelativePath === '/' || currentRelativePath === '/agents/') {
        await waitForWasmReady(page);
      }

      // Assert zero console errors or failed assets during page visit
      guard.assertPurity(`Crawler Node: ${currentRelativePath}`);

      // Discover internal anchor links on the page
      const hrefs = await page.$$eval('a[href]', (anchors) =>
        anchors.map((a) => a.getAttribute('href')).filter(Boolean) as string[]
      );

      for (const href of hrefs) {
        // Skip purely in-page hash fragments or javascript links (case-
        // insensitive and tolerant of leading whitespace, since browsers
        // accept both when resolving an href's scheme).
        const trimmedHref = href.trim().toLowerCase();
        if (href.startsWith('#') || trimmedHref.startsWith('javascript:')) {
          continue;
        }

        try {
          const resolved = new URL(href, targetUrl);

          // If internal link on same origin, queue for visitation
          if (resolved.origin === origin) {
            const relativePath = resolved.pathname;
            if (!visitedUrls.has(resolved.toString()) && !urlsToVisit.includes(relativePath)) {
              urlsToVisit.push(relativePath);
            }
          }
        } catch {
          // Skip invalid URLs
        }
      }

      guard.reset();
    }

    expect(visitedUrls.size).toBeGreaterThanOrEqual(3);
  });

  test('Asset Integrity: All critical static media, logos, and WASM binaries resolve HTTP 200', async ({
    request,
    baseURL,
  }) => {
    const criticalAssets = [
      '/favicon.svg',
      '/favicon.ico',
      '/sop.wasm',
      '/wasm_exec.js',
      '/arena/index.html',
      '/agents/index.html',
      '/agents/sop-agents.wasm',
      '/agents/wasm_exec.js',
      '/assets/joltrin-logo.svg',
      '/assets/joltrin-avatar.svg',
      '/assets/joltrin-demo.gif',
      '/assets/joltrin-social-preview.png',
      '/assets/joltrin-org-logo.jpg',
      '/assets/joltrin-page-photo.jpg',
    ];

    for (const assetPath of criticalAssets) {
      const resp = await request.get(assetPath);
      expect(
        resp.status(),
        `Expected HTTP 200 for asset: ${assetPath} (received ${resp.status()})`
      ).toBe(200);

      const contentType = resp.headers()['content-type'] || '';
      if (assetPath.endsWith('.wasm')) {
        expect(contentType).toContain('application/wasm');
      } else if (assetPath.endsWith('.svg')) {
        expect(contentType).toContain('image/svg+xml');
      } else if (assetPath.endsWith('.js')) {
        expect(contentType).toContain('javascript');
      }
    }
  });

  test('External Security & Spec Compliance: Outbound links have valid HTTPS schemas and safe rel attributes', async ({
    page,
  }) => {
    for (const route of ['/', '/arena/', '/agents/']) {
      await page.goto(route, { waitUntil: 'domcontentloaded' });

      const outboundLinks = await page.$$eval('a[href^="http"]', (anchors) =>
        anchors.map((a) => ({
          href: a.getAttribute('href') || '',
          target: a.getAttribute('target'),
          rel: a.getAttribute('rel') || '',
        }))
      );

      for (const link of outboundLinks) {
        // Assert HTTPS
        expect(
          link.href,
          `External link ${link.href} on route ${route} should use HTTPS`
        ).toMatch(/^https:\/\//);

        // Assert target="_blank" includes noopener or noreferrer
        if (link.target === '_blank') {
          const relValues = link.rel.split(/\s+/);
          const hasSafeRel = relValues.includes('noopener') || relValues.includes('noreferrer');
          expect(
            hasSafeRel,
            `Target _blank link ${link.href} on route ${route} must have rel="noopener noreferrer"`
          ).toBeTruthy();
        }
      }
    }
  });

  test('Console & Network Purity on Reload & Hard Refresh', async ({ page }) => {
    const guard = new NetworkConsoleGuard(page);

    // Load Arena
    await page.goto('/arena/', { waitUntil: 'networkidle' });
    guard.assertPurity('Arena First Load');

    // Reload
    await page.reload({ waitUntil: 'networkidle' });
    guard.assertPurity('Arena After Reload');

    // Load Tech Demo
    guard.reset();
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);
    guard.assertPurity('Tech Demo First Load');

    // Reload Tech Demo
    await page.reload({ waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);
    guard.assertPurity('Tech Demo After Reload');
  });
});
