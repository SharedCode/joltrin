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
    // The clock is paused after load, so autoplay only moves when this test moves time. A clock left running would
    // keep ticking while the test waits on a slow machine, and the walkthrough would move on by itself.
    await page.clock.install({ time: 0 });
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await page.clock.pauseAt(60_000);
    await page.evaluate(() => document.getElementById('how-stage')!.scrollIntoView({ block: 'center' }));
    await expect(page.locator('#how-stage')).toHaveClass(/playing/);
    await page.mouse.move(0, 0);
    await page.clock.runFor(7100);
    await expect(page.locator('#how-stage')).toHaveAttribute('data-step', '2');
    // This test is about what Pause does. The fake clock hides the section's reveal transition from Playwright's own
    // check that an element has stopped moving, so a real pointer click can land a few pixels off. Click the element
    // itself instead. Real pointer clicks on this button are covered by the other walkthrough tests.
    await page.locator('#how-toggle').evaluate((el) => (el as HTMLElement).click());
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

  test('refuses a huge paste and unknown fields without trying to read them', async ({ page }) => {
    await allowed(page);
    await page.locator('#rc-input').fill('x'.repeat(20_000));
    await page.locator('#rc-check').click();
    await expect(page.locator('#rc-result')).toContainText('too large');
    await page.locator('#rc-load').click();
    const raw = await page.locator('#rc-input').inputValue();
    await page.locator('#rc-input').fill(raw.replace('"v": 1', '"v": 1, "extra": true'));
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

test.describe('Fingerprint lab', () => {
  async function open(page: import('@playwright/test').Page) {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#fp-words')).toBeEnabled({ timeout: 30_000 });
    await page.locator('#fingerprint-lab summary').click();
    await expect(page.locator('#fp-digest')).toHaveText(/^[0-9a-f]{8}( [0-9a-f]{8}){7}$/);
  }
  const digest = async (page: import('@playwright/test').Page) => (await page.locator('#fp-digest').innerText()).replace(/\s+/g, '');

  test('rewording the rule does not move the fingerprint', async ({ page }) => {
    await open(page);
    const before = await digest(page);
    await page.locator('#fp-words').fill('Please never touch production data without a good backup and a signature from the owner.');
    await expect(page.locator('#fp-note')).toContainText('did not move');
    expect(await digest(page)).toBe(before);
    await page.locator('#fp-words').fill('x');
    expect(await digest(page)).toBe(before);
  });

  test('changing what must be true moves it, and undoing it brings it back', async ({ page }) => {
    await open(page);
    const before = await digest(page);
    await page.locator('#fp-second').check();
    await expect(page.locator('#fp-note')).toContainText('fingerprint changed');
    const withSecond = await digest(page);
    expect(withSecond).not.toBe(before);
    await page.locator('#fp-second').uncheck();
    await expect(page.locator('#fp-note')).toContainText('Back to a fingerprint you saw earlier');
    expect(await digest(page)).toBe(before);
    await page.locator('#fp-backup').uncheck();
    expect(await digest(page)).not.toBe(before);
    expect(await digest(page)).not.toBe(withSecond);
  });

  test('the engine refuses wording over 500 characters, and the box stops typing at 500', async ({ page }) => {
    await open(page);
    const over = await page.evaluate(() => JSON.parse((window as any).joltrinIntentFingerprint('x'.repeat(501), true, true, false)));
    expect(over.error).toContain('under 500');
    expect(over.digest).toBeUndefined();
    const exactly = await page.evaluate(() => JSON.parse((window as any).joltrinIntentFingerprint('x'.repeat(500), true, true, false)));
    expect(exactly.digest).toMatch(/^[0-9a-f]{64}$/);
    await page.locator('#fp-words').fill('y'.repeat(600));
    expect(await page.locator('#fp-words').inputValue()).toHaveLength(500);
  });

  test('a rule with nothing required is refused with a reason', async ({ page }) => {
    await open(page);
    await page.locator('#fp-backup').uncheck();
    await page.locator('#fp-approval').uncheck();
    await expect(page.locator('#fp-note')).toContainText('at least one thing');
  });
});

test.describe('Deep links and the command palette', () => {
  for (const id of ['receipt-check', 'fingerprint-lab', 'dev']) {
    test(`/private-intent/#${id} opens that section`, async ({ page }) => {
      await page.goto(`/private-intent/#${id}`, { waitUntil: 'domcontentloaded' });
      await expect(page.locator(`#${id}`)).toHaveAttribute('open', '');
      await expect(page.locator(`#${id} summary`)).toBeInViewport();
    });
  }

  test('a hash change on the page opens the section too', async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await page.evaluate(() => { location.hash = '#receipt-check'; });
    await expect(page.locator('#receipt-check')).toHaveAttribute('open', '');
  });

  test('the homepage palette finds Private Intent and its tools', async ({ page, isMobile }) => {
    test.skip(isMobile, 'the palette trigger is desktop-only');
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await page.getByRole('button', { name: /open command palette/i }).click();
    await page.locator('#cmdk-input').fill('private intent');
    await expect(page.locator('#cmdk-list')).toContainText('check a signed receipt');
    await expect(page.locator('#cmdk-list')).toContainText('edit a rule');
    await page.locator('#cmdk-input').fill('receipt');
    await page.locator('#cmdk-input').press('Enter');
    await expect(page).toHaveURL(/\/private-intent\/#receipt-check$/);
    await expect(page.locator('#receipt-check')).toHaveAttribute('open', '');
  });
});

test.describe('Try to cheat it', () => {
  async function ready(page: import('@playwright/test').Page) {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
  }

  test('an agent cannot vouch for its own backup, and the delete stays blocked', async ({ page }) => {
    await ready(page);
    await page.locator('#b-cheat-claim').click();
    await expect(page.locator('#log')).toContainText('signs "backup validated" with its own key');
    await expect(page.locator('#log')).toContainText('refused');
    await expect(page.locator('#chk-backup-t')).toHaveText('(missing)');
    await page.locator('#b-attempt').click();
    await expect(page.locator('#banner-title')).toHaveText('BLOCK');
  });

  test('an agent cannot approve its own delete', async ({ page }) => {
    await ready(page);
    await page.locator('#b-backup').click();
    await page.locator('#b-cheat-approve').click();
    await expect(page.locator('#log')).toContainText('approves its own delete');
    await expect(page.locator('#log')).toContainText('refused');
    await expect(page.locator('#chk-approval-t')).toHaveText('(missing)');
    await page.locator('#b-attempt').click();
    await expect(page.locator('#banner-title')).toHaveText('REQUIRE APPROVAL');
  });

  test('an approval for one database does not cover another', async ({ page }) => {
    await ready(page);
    await page.locator('#b-cheat-other').click();
    await expect(page.locator('#log')).toContainText('Press step 3 first');
    await page.locator('#b-backup').click();
    await page.locator('#b-approve').click();
    await page.locator('#b-cheat-other').click();
    await expect(page.locator('#log')).toContainText('prod/users');
    // The approval and the backup proof both name prod/orders, so the other database gets neither.
    await expect(page.locator('#banner-title')).not.toHaveText('ALLOW');
    await expect(page.locator('#banner-title')).toHaveText('BLOCK');
    // The real delete of orders is still allowed with the approval it was given for.
    await page.locator('#b-attempt').click();
    await expect(page.locator('#banner-title')).toHaveText('ALLOW');
  });
});

test.describe('Share a run', () => {
  async function ready(page: import('@playwright/test').Page, hash = '') {
    await page.goto(`/private-intent/${hash}`, { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
  }

  test('each step goes into the address bar, and Start over clears it', async ({ page }) => {
    await ready(page);
    await expect(page.locator('#share-run')).toBeHidden();
    await page.locator('#b-attempt').click();
    await page.locator('#b-backup').click();
    await page.locator('#b-attempt').click();
    expect(await page.evaluate(() => location.hash)).toBe('#run=ABA');
    await expect(page.locator('#share-run')).toBeVisible();
    await page.locator('#b-reset').click();
    expect(await page.evaluate(() => location.hash)).toBe('');
    await expect(page.locator('#share-run')).toBeHidden();
  });

  test('opening a shared link replays the steps against a fresh engine', async ({ page }) => {
    await ready(page, '#run=BPA');
    await expect(page.locator('#replay-note')).toContainText('Replaying a shared run of 3 steps');
    await expect(page.locator('#banner-title')).toHaveText('ALLOW', { timeout: 15_000 });
    await expect(page.locator('#log')).toContainText('Receipt verified');
    await expect(page.locator('#replay-note')).toContainText('Replayed 3 steps');
    expect(await page.evaluate(() => location.hash)).toBe('#run=BPA');
  });

  test('a shared cheating run replays the refusals too', async ({ page }) => {
    await ready(page, '#run=XA');
    await expect(page.locator('#log')).toContainText('signs "backup validated" with its own key', { timeout: 15_000 });
    await expect(page.locator('#banner-title')).toHaveText('BLOCK', { timeout: 15_000 });
  });

  for (const [label, hash] of [['unknown letters', '#run=QQQ'], ['markup', '#run=A%3Cscript%3E'], ['lower case', '#run=aba'], ['too many steps', '#run=' + 'A'.repeat(41)]]) {
    test(`a link with ${label} is ignored`, async ({ page }) => {
      await ready(page, hash);
      await page.waitForTimeout(1500);
      await expect(page.locator('#replay-note')).toBeHidden();
      await expect(page.locator('#log li')).toHaveCount(0);
      await expect(page.locator('#banner-title')).toHaveText('READY');
    });
  }

  test('the copy button says what happened', async ({ page }) => {
    await ready(page);
    await page.locator('#b-attempt').click();
    await page.locator('#share-run').click();
    await expect(page.locator('#replay-note')).toContainText(/Link copied|Copy the link from the address bar/);
  });
});

test.describe('Private Intent in the docs', () => {
  test('the docs header, start-here card and section all lead to it', async ({ page, request }) => {
    await page.goto('/docs/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('header nav a[href="../private-intent/"]')).toBeVisible();
    await expect(page.locator('a.path[href="#private-intent"]')).toContainText('I want agents to follow my rules');
    const section = page.locator('#private-intent');
    await expect(section).toHaveText('9. Private Intent');
    for (const href of ['../private-intent/#try', '../private-intent/#fingerprint-lab', '../private-intent/#receipt-check']) {
      await expect(page.locator(`a[href="${href}"]`)).toHaveCount(1);
    }
    expect((await request.get('/private-intent/')).status()).toBe(200);
  });

  test('the docs say what it does not do, and do not promise the team layer', async ({ page }) => {
    await page.goto('/docs/', { waitUntil: 'domcontentloaded' });
    const text = (await page.locator('main').innerText()).toLowerCase();
    expect(text).toContain('not a zero-knowledge system');
    expect(text).toContain('not that the action ran');
    expect(text).toContain('planned and set up with design partners');
  });
});

test.describe('Touch targets', () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 375, height: 800 } });

  test('the controls a thumb has to hit are at least 44 pixels tall', async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
    for (const id of ['receipt-check', 'fingerprint-lab', 'dev']) await page.locator(`#${id} summary`).click();
    const ids = ['b-attempt', 'b-backup', 'b-approve', 'b-revoke', 'b-reset', 'b-cheat-claim', 'b-cheat-approve', 'b-cheat-other', 'how-toggle', 'rc-load', 'rc-check', 'rc-flip', 'b-tamper', 'b-replay'];
    const short: string[] = [];
    for (const id of ids) {
      const box = await page.locator(`#${id}`).boundingBox();
      if (!box || box.height < 43.5) short.push(`${id} ${Math.round(box?.height ?? 0)}px`);
    }
    for (const id of ['fp-backup', 'fp-approval', 'fp-second']) {
      const box = await page.locator(`label:has(#${id})`).boundingBox();
      if (!box || box.height < 43.5) short.push(`label of ${id} ${Math.round(box?.height ?? 0)}px`);
    }
    expect(short, short.join(', ')).toEqual([]);
  });

  test('making them bigger does not make the page scroll sideways', async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
    expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);
  });
});

// The page is ready to measure when it has loaded and its fonts are in. This replaces a fixed sleep.
async function settled(page: import('@playwright/test').Page) {
  await page.waitForLoadState('load');
  await page.evaluate(() => document.fonts.ready);
}

test.describe('Touch targets on the homepage', () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 375, height: 800 } });

  test('header controls, form fields and buttons are at least 44 pixels', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await settled(page);
    const small = await page.evaluate(() => {
      const out: string[] = [];
      const check = (e: Element, what: string) => {
        // Layout height, not the drawn box: the scroll reveal draws unrevealed sections at 98.5%.
        const el = e as HTMLElement;
        const cs = getComputedStyle(e);
        if (el.offsetWidth === 0 || el.offsetHeight === 0 || cs.visibility === 'hidden' || e.closest('[hidden], .hidden')) return;
        if (el.offsetHeight < 44) out.push(`${what} ${el.offsetWidth}x${el.offsetHeight}`);
      };
      document.querySelectorAll('header a, header button').forEach((e) => check(e, `header ${e.id || e.tagName.toLowerCase()}`));
      document.querySelectorAll('main button').forEach((e) => check(e, `button ${e.id || (e.textContent || '').trim().slice(0, 20)}`));
      document.querySelectorAll('main input:not([type=checkbox]):not([type=radio]), main select').forEach((e) => check(e, `field ${e.id}`));
      return out;
    });
    expect(small, small.join(', ')).toEqual([]);
  });

  test('the Copy button is a full size target that stays inside its code box', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await settled(page);
    const copy = page.locator('#quickstart-copy');
    await copy.scrollIntoViewIfNeeded();
    const size = await copy.evaluate((e) => [(e as HTMLElement).offsetWidth, (e as HTMLElement).offsetHeight]);
    expect(size[1], 'the button is at least 44 pixels tall').toBeGreaterThanOrEqual(44);
    const inside = await page.evaluate(() => {
      const b = document.getElementById('quickstart-copy')!;
      const c = document.getElementById('quickstart-cmd')!;
      return b.offsetTop >= 0 && b.offsetTop + b.offsetHeight <= c.offsetTop + c.offsetHeight + 1;
    });
    expect(inside, 'the button ends inside the code box').toBe(true);
  });

  test('bigger targets do not make the homepage scroll sideways', async ({ page }) => {
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await settled(page);
    expect(await page.evaluate(() => document.documentElement.scrollWidth - window.innerWidth)).toBeLessThanOrEqual(0);
  });
});

test.describe('Homepage on narrow phones', () => {
  for (const width of [320, 360, 375, 390, 414]) {
    test(`the homepage does not scroll sideways at ${width}px`, async ({ page }) => {
      await page.setViewportSize({ width, height: 800 });
      await page.goto('/', { waitUntil: 'domcontentloaded' });
      await settled(page);
      expect(await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth)).toBeLessThanOrEqual(0);
    });
  }

  test('GitHub stays reachable on a narrow phone through the menu', async ({ page }) => {
    await page.setViewportSize({ width: 360, height: 800 });
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('header a[aria-label="Joltrin on GitHub"]')).toBeHidden();
    await page.locator('#mobile-menu-btn').click();
    await expect(page.locator('#mobile-menu-panel a[href="https://github.com/SharedCode/joltrin"]')).toBeVisible();
  });
});

