import { test, expect } from '@playwright/test';

/**
 * The Private Intent demo runs the real intent package in WebAssembly.
 * This walks the 60-second story: blocked, backup validated, approval
 * requested, allowed, replay refused, approval revoked, blocked again.
 */
test.describe('Private Intent demo', () => {
  test('blocks, asks for approval, allows, then blocks again after revoke', async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.getByRole('heading', { level: 1 })).toContainText('AI agents that respect your rules before they act');
    await expect(page.getByText('SIMULATED. No real system is touched.')).toBeVisible();

    const attempt = page.locator('#b-attempt');
    await expect(attempt).toBeEnabled({ timeout: 30_000 });
    const banner = page.locator('#banner');

    await attempt.click();
    await expect(banner).toContainText('Blocked: verified backup and owner approval required.');
    await expect(page.locator('#banner-title')).toHaveText('BLOCK');

    await page.locator('#b-backup').click();
    await attempt.click();
    await expect(page.locator('#banner-title')).toHaveText('REQUIRE APPROVAL');
    await expect(banner).toContainText('owner approval');

    await page.locator('#b-approve').click();
    await attempt.click();
    await expect(page.locator('#banner-title')).toHaveText('ALLOW');
    await expect(page.locator('#log')).toContainText('Receipt verified');

    // Approval is single use: the same request is refused for approval again.
    await attempt.click();
    await expect(page.locator('#banner-title')).toHaveText('REQUIRE APPROVAL');

    // Approve again, revoke before use, and the action stays blocked.
    await page.locator('#b-approve').click();
    await page.locator('#b-revoke').click();
    await attempt.click();
    await expect(page.locator('#banner-title')).toHaveText('REQUIRE APPROVAL');
    await expect(page.locator('#log')).toContainText('Revoked approval');
  });

  test('the verifier view never contains the private wording', async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
    await page.locator('#b-backup').click();
    await page.locator('#b-approve').click();
    await page.locator('#b-attempt').click();
    await expect(page.locator('#banner-title')).toHaveText('ALLOW');
    await expect(page.locator('#owner-json')).toContainText('Never delete production data');
    await page.locator('#dev summary').click();
    for (const id of ['#verifier-json', '#decision-json', '#receipt-json']) {
      await expect(page.locator(id)).not.toContainText('Never delete production data');
    }
    await expect(page.locator('#receipt-json')).toContainText('does not prove the action ran');
  });

  test('an edited or replayed receipt is refused by the executor', async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
    await page.locator('#dev summary').click();
    await page.locator('#b-backup').click();
    await page.locator('#b-approve').click();
    await page.locator('#b-attempt').click();
    await page.locator('#b-tamper').click();
    await expect(page.locator('#log')).toContainText('Edited receipt');
    await expect(page.locator('#log')).toContainText('not valid');
    await page.locator('#b-replay').click();
    await expect(page.locator('#log')).toContainText('already used');
  });

  test('fits a phone screen without sideways scrolling', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test('points at the next step and follows the story', async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
    await expect(page.locator('#b-attempt')).toHaveClass(/is-next/);
    await page.locator('#b-attempt').click();
    await expect(page.locator('#b-backup')).toHaveClass(/is-next/);
    await expect(page.locator('#b-attempt')).not.toHaveClass(/is-next/);
    await page.locator('#b-backup').click();
    await expect(page.locator('#b-attempt')).toHaveClass(/is-next/);
    await page.locator('#b-attempt').click();
    await expect(page.locator('#b-approve')).toHaveClass(/is-next/);
  });

  test('keeps the raw views behind a click until asked for', async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#owner-json')).toBeHidden();
    await expect(page.locator('#receipt-json')).toBeHidden();
    await page.getByText('What you can see, and what the verifier can see').click();
    await expect(page.locator('#owner-json')).toBeVisible();
  });
});

test.describe('Private Intent in the top navigation', () => {
  for (const [name, path, link] of [
    ['homepage', '/', 'a[href="./private-intent/"]'],
    ['agents page', '/agents/', 'a[href="../private-intent/"]'],
  ] as const) {
    test(`the ${name} header links to Private Intent on a desktop screen`, async ({ page }) => {
      await page.setViewportSize({ width: 1280, height: 800 });
      await page.goto(path, { waitUntil: 'domcontentloaded' });
      const nav = page.locator(`header nav ${link}`).first();
      await expect(nav).toBeVisible();
      await expect(nav).toContainText('Private Intent');
    });
  }

  for (const width of [1024, 1280, 1440]) {
    test(`the homepage header does not scroll sideways at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.goto('/', { waitUntil: 'domcontentloaded' });
      const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
      expect(overflow).toBeLessThanOrEqual(0);
    });
  }

  test('below the desktop width the menu still offers Private Intent', async ({ page }) => {
    await page.setViewportSize({ width: 1024, height: 800 });
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await page.locator('#mobile-menu-btn').click();
    await expect(page.locator('#mobile-menu-panel a[href="./private-intent/"]')).toBeVisible();
  });

  test('the More menu holds the section links and closes when one is chosen', async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 800 });
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await page.locator('#nav-more summary').click();
    await expect(page.locator('#nav-more a[href="./btree/"]')).toBeVisible();
    await page.locator('#nav-more a[href="#under-the-hood"]').click();
    await expect(page.locator('#nav-more')).not.toHaveAttribute('open', '');
  });
});

test.describe('Private Intent walkthrough', () => {
  test('the steps can be driven by click and by arrow keys', async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    const stage = page.locator('#how-stage');
    await expect(stage).toHaveAttribute('data-step', '1');
    await page.locator('#how-t2').click();
    await expect(stage).toHaveAttribute('data-step', '2');
    await expect(page.locator('#how-t2')).toHaveAttribute('aria-selected', 'true');
    await expect(page.locator('#how-h3')).toHaveText('Other systems prove it');
    await page.locator('#how-t2').focus();
    await page.keyboard.press('ArrowRight');
    await expect(stage).toHaveAttribute('data-step', '3');
    await expect(page.locator('#how-h3')).toHaveText('The executor checks');
    await page.keyboard.press('ArrowRight');
    await expect(stage).toHaveAttribute('data-step', '1');
  });

  test('it plays itself when in view, and can be paused', async ({ page }) => {
    await page.clock.install();
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await page.evaluate(() => document.getElementById('how-stage')!.scrollIntoView({ block: 'center' }));
    await expect(page.locator('#how-stage')).toHaveClass(/playing/);
    await page.mouse.move(0, 0);
    await page.clock.runFor(7100);
    await expect(page.locator('#how-stage')).toHaveAttribute('data-step', '2');
    await page.locator('#how-toggle').click();
    await expect(page.locator('#how-toggle')).toHaveText('Play');
    await page.clock.runFor(15000);
    await expect(page.locator('#how-stage')).toHaveAttribute('data-step', '2');
  });

  test('offers the team step only after an ALLOW, and hides it again on reset', async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
    await expect(page.locator('#team-cta')).not.toHaveClass(/show/);
    await page.locator('#b-backup').click();
    await page.locator('#b-approve').click();
    await page.locator('#b-attempt').click();
    await expect(page.locator('#banner-title')).toHaveText('ALLOW');
    await expect(page.locator('#team-cta')).toHaveClass(/show/);
    await expect(page.locator('#team-cta a')).toHaveAttribute('href', '#pro');
    await page.locator('#b-reset').click();
    await expect(page.locator('#team-cta')).not.toHaveClass(/show/);
  });
});

test.describe('Finding Private Intent from the homepage', () => {
  test('a banner above the header links straight to it', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const strip = page.locator('#intent-strip');
    await expect(strip).toBeVisible();
    await expect(strip).toHaveAttribute('href', './private-intent/');
    await expect(strip).toContainText('Private Intent');
  });
});

test.describe('Receipt checker', () => {
  async function allowed(page: import('@playwright/test').Page) {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
    await page.locator('#b-backup').click();
    await page.locator('#b-approve').click();
    await page.locator('#b-attempt').click();
    await expect(page.locator('#banner-title')).toHaveText('ALLOW');
    await page.locator('#receipt-check summary').click();
  }

  test('a real receipt checks out, and one changed character does not', async ({ page }) => {
    await allowed(page);
    await expect(page.locator('#rc-key')).toHaveValue(/^[0-9a-f]{64}$/);
    await page.locator('#rc-load').click();
    await expect(page.locator('#rc-input')).toHaveValue(/"policy_digest"/);
    await page.locator('#rc-check').click();
    await expect(page.locator('#rc-result')).toContainText('Valid. Signed by');
    await expect(page.locator('#rc-result')).toContainText('does not show the action ran');
    await page.locator('#rc-flip').click();
    await expect(page.locator('#rc-result')).toContainText('signature does not match');
  });

  test('the wrong key, a bad key and garbage are all explained', async ({ page }) => {
    await allowed(page);
    await page.locator('#rc-load').click();
    await page.locator('#rc-key').fill('00'.repeat(32));
    await page.locator('#rc-check').click();
    await expect(page.locator('#rc-result')).toContainText('signature does not match');
    await page.locator('#rc-key').fill('not a key');
    await page.locator('#rc-check').click();
    await expect(page.locator('#rc-result')).toContainText('64 hex characters');
    await page.locator('#rc-input').fill('hello');
    await page.locator('#rc-check').click();
    await expect(page.locator('#rc-result')).toContainText('not a receipt');
  });

  test('says so when there is no receipt yet', async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
    await page.locator('#receipt-check summary').click();
    await page.locator('#rc-load').click();
    await expect(page.locator('#rc-result')).toContainText('Get an ALLOW');
  });

  test('a receipt from before a reset no longer checks out', async ({ page }) => {
    await allowed(page);
    await page.locator('#rc-load').click();
    await page.locator('#b-reset').click();
    await expect(page.locator('#rc-key')).toHaveValue(/^[0-9a-f]{64}$/);
    await page.locator('#rc-check').click();
    await expect(page.locator('#rc-result')).toContainText('signature does not match');
  });
});

