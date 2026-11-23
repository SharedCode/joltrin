import { test, expect, Page } from '@playwright/test';

/** The "Try to steal the key" demo. It is an in-browser simulation of the Go taint package, with fake secrets only. */

async function open(page: Page) {
  await page.goto('/');
  const section = page.locator('#taint-barrier');
  await section.scrollIntoViewIfNeeded();
  await expect(section).toHaveAttribute('data-ready', '1');
  return section;
}

async function verdict(page: Page) {
  return JSON.parse((await page.locator('#tb-result').innerText()).trim());
}

test.describe('Taint Barrier demo', () => {
  test('plain and encoded variants are blocked and the reason names the encoding', async ({ page }) => {
    await open(page);
    const cases: Array<[string, string]> = [
      ['plain', 'plain'],
      ['base64', 'base64'],
      ['hex', 'hex'],
      ['url', 'url'],
      ['reversed', 'reversed'],
      ['chunks', 'plain+chunk'],
      ['spaced', 'plain'],
    ];
    for (const [button, variant] of cases) {
      await page.locator(`[data-tb="${button}"]`).click();
      await expect(page.locator('#tb-badge')).toHaveText('Blocked');
      const v = await verdict(page);
      expect(v.allowed, button).toBe(false);
      expect(v.reason, button).toBe('label_not_allowed_for_sink');
      expect(v.findings[0].value_id, button).toBe('demo_secret');
      expect(v.findings[0].variant, button).toBe(variant);
      expect(v.sink).toBe('host:collect.attacker.example');
    }
  });

  test('the call shown for the base64 button really carries an encoded key, not the plain one', async ({ page }) => {
    await open(page);
    const secret = (await page.locator('#tb-secret').innerText()).trim();
    expect(secret).toMatch(/^demo-[0-9a-f]{32}$/);
    await page.locator('[data-tb="base64"]').click();
    const call = await page.locator('#tb-call').innerText();
    expect(call).not.toContain(secret);
    expect(call).toContain(Buffer.from(secret).toString('base64'));
  });

  test('allow-listing the host lets the same call pass, and unchecking blocks it again', async ({ page }) => {
    await open(page);
    await page.locator('[data-tb="base64"]').click();
    await expect(page.locator('#tb-badge')).toHaveText('Blocked');
    await page.locator('#tb-allow').check();
    await expect(page.locator('#tb-badge')).toHaveText('Allowed');
    const v = await verdict(page);
    expect(v.allowed).toBe(true);
    expect(v.reason).toBe('sink_allowed');
    expect(await page.locator('#tb-policy').innerText()).toContain('collect.attacker.example');
    await page.locator('#tb-allow').uncheck();
    await expect(page.locator('#tb-badge')).toHaveText('Blocked');
    expect(await page.locator('#tb-policy').innerText()).not.toContain('collect.attacker.example');
  });

  test('the decoy credential is blocked and alerts, even when the host is allowed', async ({ page }) => {
    await open(page);
    const decoy = (await page.locator('#tb-decoy').innerText()).trim();
    expect(decoy).toMatch(/^AKIA[A-Z2-7]{16}$/);
    await page.locator('#tb-allow').check();
    await page.locator('[data-tb="decoy"]').click();
    await expect(page.locator('#tb-badge')).toHaveText('Blocked');
    const v = await verdict(page);
    expect(v.outcome).toBe('honeytoken');
    expect(v.reason).toBe('honeytoken_tripped');
    await expect(page.locator('#tb-alert')).toBeVisible();
    await expect(page.locator('#tb-alert')).toContainText('honeytoken_tripped');
  });

  test('the first attempt is already shown as blocked before any click', async ({ page }) => {
    await open(page);
    await expect(page.locator('#tb-badge')).toHaveText('Blocked');
    expect((await verdict(page)).reason).toBe('label_not_allowed_for_sink');
  });

  test('nothing is sent anywhere when the buttons are used', async ({ page }) => {
    await open(page);
    const secret = (await page.locator('#tb-secret').innerText()).trim();
    const seen: string[] = [];
    page.on('request', r => seen.push(r.url() + ' ' + (r.postData() ?? '')));
    for (const b of ['plain', 'base64', 'hex', 'url', 'chunks', 'decoy']) await page.locator(`[data-tb="${b}"]`).click();
    await page.locator('#tb-allow').check();
    expect(seen.filter(s => s.includes(secret) || s.includes('attacker.example'))).toEqual([]);
  });

  test('no console errors or page errors while using the demo', async ({ page }) => {
    const errors: string[] = [];
    page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
    page.on('pageerror', e => errors.push(String(e)));
    await open(page);
    for (const b of ['plain', 'base64', 'hex', 'url', 'reversed', 'chunks', 'spaced', 'decoy']) await page.locator(`[data-tb="${b}"]`).click();
    await page.locator('#tb-allow').check();
    await page.locator('[data-tb="plain"]').click();
    expect(errors.filter(e => !/favicon|youtube|googleapis|gstatic|ERR_/i.test(e))).toEqual([]);
  });

  for (const width of [375, 1440]) {
    test(`no sideways scroll at ${width}px, before and after use`, async ({ page }) => {
      await page.setViewportSize({ width, height: 900 });
      await open(page);
      const overflow = () => page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
      expect(await overflow()).toBeLessThanOrEqual(0);
      await page.locator('[data-tb="base64"]').click();
      await page.locator('#tb-allow').check();
      expect(await overflow()).toBeLessThanOrEqual(0);
      const box = await page.locator('#taint-barrier').boundingBox();
      expect(box!.width).toBeLessThanOrEqual(width);
    });
  }

  test('reduced motion shows the content with no animation', async ({ browser }) => {
    const ctx = await browser.newContext({ reducedMotion: 'reduce' });
    const page = await ctx.newPage();
    const section = await open(page);
    await expect(section).toHaveCSS('opacity', '1');
    await expect(page.locator('#tb-badge')).toHaveText('Blocked');
    await page.locator('[data-tb="hex"]').click();
    await expect(page.locator('#tb-badge')).toHaveText('Blocked');
    await expect(page.locator('#tb-out')).not.toHaveClass(/tb-flash/);
    await expect(page.locator('#tb-badge')).toHaveCSS('animation-name', 'none');
    await ctx.close();
  });

  test('motion is on for visitors who allow it, and the block stamp plays', async ({ page }) => {
    await open(page);
    await page.locator('[data-tb="hex"]').click();
    await expect(page.locator('#tb-out')).toHaveClass(/tb-flash/);
  });

  test('the section states that it is a simulation and links the Go package and the limits', async ({ page }) => {
    const section = await open(page);
    await expect(section).toContainText('This page is a simulation');
    await expect(section).toContainText('Every secret here is fake');
    await expect(section.getByRole('link', { name: 'the taint package' })).toHaveAttribute('href', /SharedCode\/joltrin\/tree\/master\/taint$/);
    await expect(section.getByRole('link', { name: 'the controls matrix' })).toHaveAttribute('href', /SECURITY_CONTROLS\.md#taint-barrier$/);
    await expect(section).toContainText('not a sandbox');
  });
});
