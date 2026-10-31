import { test, expect } from '@playwright/test';
import { waitForWasmReady, waitForFormReady } from './helpers/wasm-lifecycle';

/**
 * Homepage positioning, pricing honesty, and metadata.
 * Covers the simplified hero, the three live experiences, the open-core
 * plans, the Pro request flow on a static host, and the domain in metadata.
 */
test.describe('Homepage', () => {
  test('hero states the product and offers one primary action', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const h1 = page.getByRole('heading', { level: 1 });
    await expect(h1).toContainText(/delete production/i);
    await expect(h1).toContainText(/who.s stopping it/i);
    await expect(page.getByRole('link', { name: /try to make an agent drop a database/i })).toHaveAttribute('href', /agents/);
    await expect(page.getByRole('link', { name: /watch the short demo/i })).toHaveAttribute('href', '#demo-video');
    // The hero text must sit inside the viewport, not be clipped by an overflowing child.
    const fits = await page.evaluate(() => {
      const r = document.querySelector('h1')!.getBoundingClientRect();
      return r.left >= 0 && r.right <= window.innerWidth;
    });
    expect(fits, 'the headline fits the viewport').toBe(true);
  });

  test('headline and the one primary action are in the first screen, and the engine is not preloaded', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const vh = page.viewportSize()!.height;
    const h1 = await page.getByRole('heading', { level: 1 }).boundingBox();
    expect(h1!.y + h1!.height, 'the headline ends inside the first screen').toBeLessThan(vh);
    const cta = await page.getByRole('link', { name: /try to make an agent drop a database/i }).boundingBox();
    expect(cta!.y + cta!.height, 'the primary action ends inside the first screen').toBeLessThan(vh);
    await expect(page.locator('link[rel="preload"][href$="sop.wasm"]')).toHaveCount(0);
  });

  test('the 6 MB engine downloads only after the visitor interacts', async ({ page }) => {
    let fetched = 0;
    await page.route('**/sop.wasm', (route) => { fetched++; route.continue(); });
    await page.goto('/', { waitUntil: 'load' });
    await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
    expect(fetched, 'no engine download before any interaction').toBe(0);
    await page.getByRole('link', { name: /try to make an agent drop a database/i }).hover();
    await expect.poll(() => fetched).toBeGreaterThan(0);
  });

  test('nothing covers the hero when the icon CDN is unreachable', async ({ page }) => {
    // The boot overlay once stayed up for good after the icon script failed to load,
    // and the homepage was a black screen that still answered 200.
    await page.route(/^https:\/\/unpkg\.com\//, (route) => route.abort());
    await page.goto('/', { waitUntil: 'load' });
    const h1 = page.getByRole('heading', { level: 1 });
    await expect(h1).toBeAttached();
    await expect(h1).toBeVisible();
    await expect
      .poll(
        () =>
          page.evaluate(() => {
            const el = document.querySelector('h1')!;
            const r = el.getBoundingClientRect();
            const top = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
            return !!top && el.contains(top);
          }),
        { message: 'the headline is the topmost element at its own position', timeout: 15_000 },
      )
      .toBe(true);
  });

  test('hero shows the blocked then allowed sequence', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const ex = page.locator('#hero-example');
    await expect(ex).toContainText('backup_validated missing');
    await expect(ex).toContainText('backup_taken missing');
    await expect(ex.getByText('BLOCKED')).toHaveCount(2);
    await expect(ex.getByText('ALLOWED')).toHaveCount(3);
  });

  test('block result section shows the payload fields and the replan reading', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const section = page.locator('#block-result');
    for (const field of ['blocked_by', 'missing_state', 'established_by_steps']) {
      await expect(section).toContainText(field);
    }
    await expect(section).toContainText(/replan/i);
  });

  test('limits section separates real, simulated, and not covered', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const section = page.locator('#limits');
    for (const h of ['Real', 'Simulated', 'Not covered']) {
      await expect(section.getByRole('heading', { name: h, exact: true })).toBeVisible();
    }
    await expect(section).toContainText(/another cloud account/i);
    await expect(section).toContainText(/not implemented/i);
    for (const row of ['Implemented and tested', 'Deployed and checked', 'Demonstrated', 'Not implemented']) {
      await expect(section).toContainText(row);
    }
    await expect(section).toContainText(/the barrier is not hosted/i);
    await expect(section).toContainText(/not that the action happened/i);
  });

  test('the page does not lead with database or infrastructure claims', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const hero = (await page.locator('#start').innerText()).toLowerCase();
    for (const phrase of ['future of databases', 'ai-native', 'autonomous agent platform']) {
      expect(hero).not.toContain(phrase);
    }
  });

  test('intro video is a click-to-play thumbnail that loads YouTube only on click', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const play = page.locator('#demo-video-play');
    await expect(page.locator('#demo-video iframe')).toHaveCount(0);
    await play.scrollIntoViewIfNeeded();
    await expect(page.locator('#demo-video iframe')).toHaveCount(0);
    const playY = (await play.boundingBox())!.y + (await page.evaluate(() => window.scrollY));
    const heroY = (await page.getByRole('heading', { level: 1 }).boundingBox())!.y + (await page.evaluate(() => window.scrollY));
    expect(playY).toBeGreaterThan(heroY);
    await page.route('**/youtube-nocookie.com/**', (route) => route.fulfill({ status: 200, contentType: 'text/html', body: '' }));
    await play.click();
    const frame = page.locator('iframe[title="Joltrin intro video"]');
    await expect(frame).toHaveAttribute('src', /youtube-nocookie\.com\/embed\/WZbz868nTi4\?.*autoplay=1/);
    expect(await frame.getAttribute('src')).not.toMatch(/loop=1/);
  });

  test('header has a Watch demo button that jumps to the video', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const btn = page.locator('#watch-demo-btn');
    await expect(btn).toBeVisible();
    await expect(btn).toHaveAttribute('href', '#demo-video');
    await expect(page.locator('#demo-video-play')).toHaveCount(1);
  });

  test('explains how to test the barrier with your own AI agent', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const section = page.locator('#test-your-agent');
    await expect(section).toContainText('No Go needed');
    await expect(section).toContainText('curl -fsSL https://raw.githubusercontent.com/SharedCode/joltrin/master/scripts/install.sh | sh');
    await expect(section).toContainText('irm https://raw.githubusercontent.com/SharedCode/joltrin/master/scripts/install.ps1 | iex');
    await expect(section).toContainText('powershell -NoProfile -ExecutionPolicy Bypass -Command "irm https://raw.githubusercontent.com/SharedCode/joltrin/master/scripts/install.ps1 | iex"');
  });

  test('shows how to drive the barrier from LangGraph and says the offline policy is not a model', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const section = page.locator('#test-your-agent');
    await expect(section).toContainText('Or drive it from LangGraph');
    await expect(section).toContainText('The policy is not a language model');
    await expect(section).toContainText("BLOCKED missing backup_validated, run first: ['validate_backup']");
    await expect(section.getByRole('link', { name: 'Source and setup' })).toHaveAttribute('href', /examples\/langgraph_agent$/);
  });

  test('four live experiences are linked right after the hero', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const strip = page.locator('#live-experiences');
    await expect(strip.locator('a')).toHaveCount(4);
    await expect(strip.locator('a[href="#under-the-hood"]')).toBeVisible();
    await expect(strip.locator('a[href="./arena/"]')).toBeVisible();
    await expect(strip.locator('a[href="./btree/"]')).toBeVisible();
    await expect(strip.locator('a[href="./agents/"]')).toBeVisible();
  });

  test('agent team demo replays blocked and allowed calls for both scenarios', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const log = page.locator('#team-log');
    await page.locator('#team-run').click();
    await expect(log).toContainText('BLOCKED grounding: claimed cpu_pct=97', { timeout: 10000 });
    await expect(log).toContainText('BLOCKED scope: aws.terminate_instances');
    await expect(log).toContainText('checkout-asg scaled 4 -> 6', { timeout: 10000 });
    await page.getByRole('tab', { name: 'PagerDuty incident' }).click();
    await expect(log).toBeEmpty();
    await page.locator('#team-run').click();
    await expect(log).toContainText('BLOCKED grounding: claimed error_rate_pct=40', { timeout: 10000 });
    await expect(log).toContainText('PD-77 resolved', { timeout: 10000 });
    await expect(log).toContainText('"missing_state":"recovery_confirmed"');
  });

  test('agent feedback callout explains the structured block and shows the replay', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const callout = page.locator('#agent-feedback');
    await expect(callout).toContainText('A block is feedback, not a dead end');
    await expect(callout).toContainText('which rule tripped');
    await expect(callout.getByRole('link', { name: /agent test results/i })).toHaveAttribute('href', /AGENT_BARRIER_TESTS\.md#does-the-agent-act-on-the-block-feedback/);
    const img = callout.locator('img');
    await img.scrollIntoViewIfNeeded();
    await expect(img).toBeVisible();
    await expect.poll(() => img.evaluate((el: HTMLImageElement) => el.naturalWidth)).toBeGreaterThan(0);
  });

  test('Docs is in the header, the mobile menu, and the footer, and points at /docs/', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    // Desktop header, mobile menu panel, and footer.
    expect(await page.locator('a[href="./docs/"]').count()).toBeGreaterThanOrEqual(3);
    expect(await page.locator('header nav a[href="./docs/"]').count()).toBeGreaterThanOrEqual(1);
    expect(await page.locator('#mobile-menu-panel a[href="./docs/"]').count()).toBe(1);
  });

  test('/docs/ is a real page on the site that lists the documentation', async ({ page, request }) => {
    const res = await request.get('/docs/');
    expect(res.status()).toBe(200);
    const html = await res.text();
    expect(html).toContain('<title>Documentation | Joltrin</title>');
    expect(html).toContain('independent of what the agent claims');
    expect(html).toContain('<link rel="canonical" href="https://joltrinhq.com/docs/">');

    await page.goto('/docs/', { waitUntil: 'domcontentloaded' });
    await expect(page.getByRole('heading', { level: 1, name: 'Documentation' })).toBeVisible();
    await expect(page.locator('nav[aria-label="Main"] [aria-current="page"]')).toHaveText('Docs');
    // Back to the other experiences from the docs page.
    await expect(page.locator('nav[aria-label="Main"] a[href="../agents/"]')).toBeVisible();

    const hrefs = await page.$$eval('main a[href]', (as) => as.map((a) => (a as HTMLAnchorElement).href));
    expect(hrefs.length).toBeGreaterThanOrEqual(15);
    for (const h of hrefs) {
      // Documentation opens on GitHub. Exceptions are the official Go installer, the live demos, and sections of this page.
      expect(h, 'documentation links go to the repository, official Go install guide, a live demo, or a section of this page').toMatch(/^(https:\/\/github\.com\/SharedCode\/joltrin[\/#]|https:\/\/go\.dev\/doc\/install$|https?:\/\/[^/]+\/agents\/$|https?:\/\/[^/]+\/private-intent\/#[a-z-]+$|https?:\/\/[^/]+\/docs\/#[a-z0-9-]+$)/);
    }
    for (const h of ['What it protects against', 'The independent verifier model', 'The blocked-result payload', 'Five-minute local demo', 'MCP integration', 'A2A integration']) {
      await expect(page.getByRole('heading', { level: 2, name: new RegExp(h, 'i') })).toBeVisible();
    }
    await expect(page.locator('main pre').first()).toContainText('missing_state');
    const unsafe = await page.$$eval('main a[target="_blank"]', (as) => as.filter((a) => {
      const rel = a.getAttribute('rel') ?? '';
      return !/noopener/.test(rel) || !/noreferrer/.test(rel);
    }).length);
    expect(unsafe, 'external links must carry rel="noopener noreferrer"').toBe(0);
  });

  test('pricing explains when to move to Pro and Enterprise', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const pricing = page.locator('#pricing');
    await expect(pricing).toContainText('Move to Pro when');
    await expect(pricing).toContainText('Move to Enterprise when');
    await expect(pricing.getByRole('link', { name: /try the barrier in your browser/i })).toBeVisible();
  });

  test('pricing says what Private Intent is today and what is only planned for teams', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const pricing = page.locator('#pricing');
    const free = pricing.locator('li', { hasText: 'your rules, signed approvals and receipts for one owner' });
    await expect(free.getByRole('link', { name: 'Private Intent' })).toHaveAttribute('href', './private-intent/');
    const team = pricing.locator('li', { hasText: 'Private Intent for teams' });
    await expect(team).toContainText('Planned');
    await expect(team.getByRole('link', { name: /private intent for teams/i })).toHaveAttribute('href', './private-intent/#pro');
  });

  test('pricing shows open source, Pro, and Enterprise contact without live-checkout claims', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const pricing = page.locator('#pricing');
    await expect(pricing).toContainText('$0');
    await expect(pricing).toContainText('$49');
    await expect(pricing.getByRole('button', { name: /talk to us/i })).toBeVisible();
    await expect(pricing.getByRole('button', { name: /request pro/i })).toBeVisible();
    const text = (await pricing.innerText()).toLowerCase();
    for (const claim of ['instant workspace', 'apple pay', 'google pay', 'automated stripe', 'most popular']) {
      expect(text, `pricing must not say "${claim}"`).not.toContain(claim);
    }
  });

  test('requesting Pro on the static host falls back to email instead of faking checkout', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);
    await page.locator('#pricing').getByRole('button', { name: /request pro/i }).click();
    const modal = page.locator('#pro-checkout-modal');
    await expect(modal).toBeVisible();
    await expect(modal).not.toContainText(/annual|\$490/i);
    await page.fill('#pro-team-name', 'example-team');
    await page.fill('#pro-admin-email', 'admin@example.test');
    await modal.getByRole('button', { name: /^request pro$/i }).click();
    await expect(page.locator('#pro-checkout-status')).toContainText(/isn't available on this site yet/i, { timeout: 10_000 });
    await expect(page.locator('#pro-checkout-status a[href^="mailto:"]')).toBeVisible();
  });

  test('canonical and social metadata point at joltrinhq.com on all three pages', async ({ request }) => {
    for (const [path, expected] of [
      ['/', 'https://joltrinhq.com/'],
      ['/agents/', 'https://joltrinhq.com/agents/'],
      ['/arena/', 'https://joltrinhq.com/arena/'],
    ]) {
      const html = await (await request.get(path)).text();
      expect(html, path).toContain(`<link rel="canonical" href="${expected}"`);
      expect(html, path).toContain(`property="og:url" content="${expected}"`);
      expect(html, path).not.toMatch(/https:\/\/joltrin\.com(?:[\/"'\s?#:]|$)/);
    }
  });

  test('share preview text matches the hero on the homepage and the agents page', async ({ request }) => {
    const home = await (await request.get('/')).text();
    expect(home).toContain('property="og:title" content="Your AI agent wants to delete production.');
    expect(home).toContain('name="twitter:title" content="Your AI agent wants to delete production.');
    expect(home).toContain('It checks the recorded trace, not the world.');
    const agents = await (await request.get('/agents/')).text();
    const og = agents.match(/property="og:title" content="([^"]+)"/)?.[1];
    const tw = agents.match(/name="twitter:title" content="([^"]+)"/)?.[1];
    expect(tw).toBe(og);
  });

  test('page does not scroll sideways', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });

  for (const outcome of ['accepted', 'server error', 'lost response'] as const) {
    test(`an inquiry is posted once and reports ${outcome} accurately`, async ({ page }) => {
      let posts = 0;
      await page.route('**/api/billing/enterprise-contact', route => {
        posts++;
        return outcome === 'lost response' ? route.abort('failed')
          : route.fulfill({ status: outcome === 'accepted' ? 200 : 503, body: '{}' });
      });
      await page.goto('/', { waitUntil: 'domcontentloaded' });
      await waitForFormReady(page, 'interest-ts');
      await page.fill('#inq-name', 'Test Person');
      await page.fill('#inq-email', 'test@example.test');
      await page.fill('#inq-company', 'Test Company');
      await page.click('#inq-submit-btn');
      const status = page.locator('#form-status-alert');
      await expect(status).toContainText(outcome === 'accepted' ? 'inquiry has been received' : 'We could not confirm this submission');
      expect(posts).toBe(1);
      await expect(page.locator('#inq-name')).toHaveValue(outcome === 'accepted' ? '' : 'Test Person');
      if (outcome !== 'accepted') {
        await expect(page.locator('#inq-submit-btn')).toContainText('Submission Unconfirmed');
        await expect(status).not.toContainText('Nothing was sent');
      }
    });
  }

  test('enterprise inquiry status is announced to screen readers', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#form-status-alert')).toHaveAttribute('role', 'status');
    await expect(page.locator('#form-status-alert')).toHaveAttribute('aria-live', 'polite');
  });

  test.describe('scroll story', () => {
    test('scrolling lights up the opening line and steps through the five decisions', async ({ page }) => {
      await page.goto('/', { waitUntil: 'domcontentloaded' });
      await expect(page.locator('html')).toHaveClass(/js-story/);

      const total = await page.locator('.story-words .w').count();
      // The story sits below the hero, so scroll relative to where it starts.
      const [storyTop, introHeight] = await page.locator('.story-intro').evaluate((el) => [
        el.getBoundingClientRect().top + window.scrollY,
        (el as HTMLElement).offsetHeight,
      ]);
      await page.evaluate((y) => window.scrollTo(0, y), storyTop + introHeight * 0.9);
      await expect.poll(() => page.locator('.story-words .w.lit').count()).toBe(total);

      const pin = page.locator('.story-pin');
      const [top, height, vh] = await pin.evaluate((el) => [
        el.getBoundingClientRect().top + window.scrollY,
        (el as HTMLElement).offsetHeight,
        window.innerHeight,
      ]);
      const track = height - vh;
      const stepAt = (n: number) => top + track * ((n + 0.5) / 5);

      await page.evaluate((y) => window.scrollTo(0, y), stepAt(0));
      await expect(page.locator('.story-row.on')).toHaveCount(1);
      await expect(page.locator('.story-verdict .v-block')).toHaveClass(/show/);

      await page.evaluate((y) => window.scrollTo(0, y), stepAt(4));
      await expect(page.locator('.story-row.on')).toHaveCount(5);
      await expect(page.locator('.story-verdict .v-allow')).toHaveClass(/show/);
      await expect(page.locator('.story-verdict .v-block')).not.toHaveClass(/show/);
    });

    test('the runbook rows play on their own once the stage is on screen', async ({ page }) => {
      await page.goto('/', { waitUntil: 'domcontentloaded' });
      const [top, vh] = await page.locator('.story-pin').evaluate((el) => [el.getBoundingClientRect().top + window.scrollY, window.innerHeight]);
      // Park just inside the stage and do not scroll again.
      await page.evaluate((y) => window.scrollTo(0, y), top - vh * 0.3);
      await expect(page.locator('.story-row.on')).toHaveCount(5, { timeout: 8_000 });
      await expect(page.locator('.story-verdict .v-allow')).toHaveClass(/show/);
    });

    test('a scroll hint shows at the top and goes away once the visitor scrolls', async ({ page }) => {
      await page.goto('/', { waitUntil: 'domcontentloaded' });
      const hint = page.locator('#story-hint');
      await expect(hint).toBeVisible();
      await expect(hint).toContainText('Scroll down to continue');
      const storyTop = await page.locator('.story-intro').evaluate((el) => el.getBoundingClientRect().top + window.scrollY);
      await page.evaluate((y) => window.scrollTo(0, y), storyTop + 200);
      await expect(hint).toHaveClass(/gone/);
      await page.evaluate((y) => window.scrollTo(0, y), storyTop);
      await expect(hint).not.toHaveClass(/gone/);
    });

    test('reduced motion renders the finished story with no scripted pinning', async ({ page }) => {
      await page.emulateMedia({ reducedMotion: 'reduce' });
      await page.goto('/', { waitUntil: 'domcontentloaded' });
      await expect(page.locator('html')).not.toHaveClass(/js-story/);
      await expect(page.locator('.story-row')).toHaveCount(5);
      for (const row of await page.locator('.story-row').all()) {
        await expect(row).toHaveCSS('opacity', '1');
      }
      const position = await page.locator('.story-stage').evaluate((el) => getComputedStyle(el).position);
      expect(position).not.toBe('sticky');
      await expect(page.locator('#story-hint')).toBeHidden();
    });
  });

  test('the first section after the scroll story is fully opaque when it comes into view', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await page.locator('main > section').first().scrollIntoViewIfNeeded();
    await page.evaluate(() => window.scrollBy(0, -100));
    await expect
      .poll(() => page.locator('main > section').first().evaluate((el) => Number(getComputedStyle(el).opacity)))
      .toBeGreaterThan(0.95);
  });
  test('quickstart Copy button records a copy_quickstart event', async ({ page }) => {
    await page.route('**/plausible.io/**', (route) => route.abort());
    await page.addInitScript(() => {
      (window as any).__copied = '';
      Object.defineProperty(navigator, 'clipboard', { value: {
        writeText: async (value: string) => { (window as any).__copied = value; },
      } });
      (window as any).__events = [];
      (window as any).plausible = (name: string) => (window as any).__events.push(name);
    });
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const btn = page.locator('#quickstart-copy');
    await btn.scrollIntoViewIfNeeded();
    await btn.click();
    await expect(btn).toHaveText('Copied');
    expect(await page.evaluate(() => (window as any).__events)).toContain('copy_quickstart');
    expect(await page.evaluate(() => (window as any).__copied)).toBe('go run github.com/sharedcode/joltrin/v5/examples/verify_barrier@latest');
  });
  test('hero names the problem and the FAQ answers how to buy without promising checkout', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await expect(page.getByRole('heading', { level: 1 })).toContainText(/delete production/i);
    await expect(page.locator('#faq details')).toHaveCount(5);
    const buy = page.locator('#faq details').last();
    await expect(buy).toContainText('no self-serve checkout yet');
    await expect(buy).toContainText('not charged');
  });
});
