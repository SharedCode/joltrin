import { test, expect } from '@playwright/test';

/**
 * The playful parts: the homepage "Be the agent" panel, the agent barrier
 * missions (judged from the real compiled barrier), and the docs path chooser.
 */
test.describe('Be the agent (homepage)', () => {
  test('a wrong order is blocked with the reason, the right order finishes', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const play = page.locator('#hero-play');
    await play.getByRole('button', { name: 'drop_prod_db' }).click();
    await expect(page.locator('#hero-play-log')).toContainText('requires "backup_validated"');
    await play.getByRole('button', { name: 'validate_backup' }).click();
    await expect(page.locator('#hero-play-log')).toContainText('requires "backup_taken"');
    await play.getByRole('button', { name: 'take_backup' }).click();
    await play.getByRole('button', { name: 'validate_backup' }).click();
    await play.getByRole('button', { name: 'drop_prod_db' }).click();
    await expect(page.locator('#hero-play-log')).toContainText('You hit 2 blocks');
    await expect(page.locator('#hero-play-score')).toContainText('Blocks: 2');
  });

  test('start over clears the run and a clean run says so', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    const play = page.locator('#hero-play');
    await play.getByRole('button', { name: 'drop_prod_db' }).click();
    await play.getByRole('button', { name: 'Start over' }).click();
    await expect(page.locator('#hero-play-log li')).toHaveCount(0);
    for (const s of ['take_backup', 'validate_backup', 'drop_prod_db']) await play.getByRole('button', { name: s }).click();
    await expect(page.locator('#hero-play-log')).toContainText('no blocks');
  });

  test('the original hero example is untouched', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#hero-example').getByText('BLOCKED')).toHaveCount(2);
  });
});

test.describe('Agent barrier missions', () => {
  test('missions are cleared by the real barrier answers', async ({ page }) => {
    await page.goto('/agents/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#wasm-status-text')).not.toContainText('Loading', { timeout: 30_000 });
    const list = page.locator('#mission-list');
    await expect(page.locator('#missions-score')).toHaveText('0 of 4 done');

    await page.getByRole('button', { name: '3. Drop Prod DB' }).click();
    await expect(page.locator('#missions-score')).toHaveText('1 of 4 done');
    await expect(list).toContainText('Done: Get blocked');

    await page.getByRole('button', { name: '2. Validate Backup' }).click();
    await expect(list).toContainText('Done: Hit the other wall');

    await page.getByRole('button', { name: '1. Take Backup' }).click();
    await page.getByRole('button', { name: '2. Validate Backup' }).click();
    await page.getByRole('button', { name: '3. Drop Prod DB' }).click();
    await expect(list).toContainText('Done: Finish the job');
    // blocks happened before the drop, so the clean-run mission is still open
    await expect(list).not.toContainText('Done: Clean run');

    await page.getByRole('button', { name: /reset trace/i }).click();
    for (const n of ['1. Take Backup', '2. Validate Backup', '3. Drop Prod DB']) await page.getByRole('button', { name: n }).click();
    await expect(list).toContainText('Done: Clean run');
    await expect(page.locator('#missions-score')).toHaveText('4 of 4 done');
    await expect(page.locator('#mission-note')).toContainText('all four');
  });

  test('progress survives a reload and nothing throws', async ({ page }) => {
    const errors: string[] = [];
    page.on('pageerror', (e) => errors.push(e.message));
    await page.goto('/agents/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#wasm-status-text')).not.toContainText('Loading', { timeout: 30_000 });
    await page.getByRole('button', { name: '3. Drop Prod DB' }).click();
    await page.reload({ waitUntil: 'domcontentloaded' });
    await expect(page.locator('#missions-score')).toHaveText('1 of 4 done');
    expect(errors).toEqual([]);
  });
});

test.describe('Docs path chooser', () => {
  test('six paths, and the in-page ones land on real sections', async ({ page }) => {
    await page.goto('/docs/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('a.path')).toHaveCount(6);
    for (const [href, id] of [['#local-demo', 'local-demo'], ['#mcp', 'mcp'], ['#private-intent', 'private-intent'], ['#kubernetes', 'kubernetes']]) {
      await expect(page.locator(`a.path[href="${href}"]`)).toBeVisible();
      await expect(page.locator(`#${id}`)).toHaveCount(1);
    }
    await expect(page.locator('a.path[href="../agents/"]')).toBeVisible();
    await page.getByRole('link', { name: /I build with Go/ }).click();
    await expect(page).toHaveURL(/#local-demo$/);
    await expect(page.locator('#demo-command')).toContainText('examples/verify_barrier@latest');
    await page.locator('a.path[href="#kubernetes"]').click();
    await expect(page).toHaveURL(/#kubernetes$/);
  });

  test('no sideways scroll on a phone', async ({ page }) => {
    await page.setViewportSize({ width: 375, height: 800 });
    await page.goto('/docs/', { waitUntil: 'domcontentloaded' });
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth);
    expect(overflow).toBeLessThanOrEqual(0);
  });
});

test.describe('Agents page Pro link', () => {
  test('the Pro link records a pro_click event and goes to pricing', async ({ page }) => {
    await page.route('**/plausible.io/**', (route) => route.abort());
    await page.addInitScript(() => {
      (window as any).__events = [];
      (window as any).plausible = (name: string) => (window as any).__events.push(name);
    });
    await page.goto('/agents/', { waitUntil: 'domcontentloaded' });
    const link = page.getByRole('link', { name: /explore pro/i });
    await link.scrollIntoViewIfNeeded();
    await expect(link).toHaveAttribute('href', /#pricing$/);
    // Stop the navigation so the event list can be read on this page.
    await link.evaluate((el) => el.addEventListener('click', (e) => e.preventDefault()));
    await link.click();
    expect(await page.evaluate(() => (window as any).__events)).toContain('pro_click');
  });
});

test.describe('Agents page inquiry form', () => {
  test('a submitted inquiry records inquiry_submit with the tier and no personal data', async ({ page }) => {
    await page.route('**/plausible.io/**', (route) => route.abort());
    await page.route('**/api/billing/enterprise-contact', (route) => route.fulfill({ status: 200, body: '{}' }));
    await page.addInitScript(() => {
      (window as any).__events = [];
      (window as any).plausible = (name: string, opts?: unknown) => (window as any).__events.push([name, opts]);
    });
    await page.goto('/agents/', { waitUntil: 'domcontentloaded' });
    await page.waitForTimeout(1500);
    await page.fill('#interest-form-agents [name="name"]', 'Test Person');
    await page.fill('#interest-form-agents [name="email"]', 'test@example.com');
    await page.fill('#interest-form-agents [name="company"]', 'Test Co');
    await page.selectOption('#inq-tier-agents', 'pro');
    await page.click('#inq-submit-btn-agents');
    await expect(page.locator('#agents-form-status-alert')).toContainText('inquiry has been received');
    const events = await page.evaluate(() => (window as any).__events);
    expect(events).toContainEqual(['inquiry_submit', { props: { tier: 'pro' } }]);
    expect(JSON.stringify(events)).not.toContain('test@example.com');
  });
});
