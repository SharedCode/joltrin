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
});
