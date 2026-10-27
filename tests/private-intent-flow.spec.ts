import { test, expect } from '@playwright/test';

/**
 * The demo and the receipt checker are one story: after the engine allows the
 * delete, one click checks the receipt it just signed.
 */
test.describe('Check this receipt', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/private-intent/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#b-attempt')).toBeEnabled({ timeout: 30_000 });
  });

  test('is offered only after an ALLOW', async ({ page }) => {
    await expect(page.locator('#b-check-receipt')).toBeHidden();
    await page.locator('#b-attempt').click();
    await expect(page.locator('#banner-title')).toHaveText('BLOCK');
    await expect(page.locator('#b-check-receipt')).toBeHidden();
    await page.locator('#b-backup').click();
    await page.locator('#b-approve').click();
    await page.locator('#b-attempt').click();
    await expect(page.locator('#banner-title')).toHaveText('ALLOW');
    await expect(page.locator('#b-check-receipt')).toBeVisible();
    await page.locator('#b-reset').click();
    await expect(page.locator('#b-check-receipt')).toBeHidden();
  });

  test('opens the checker with the receipt loaded and shows that it is valid', async ({ page }) => {
    await page.locator('#b-backup').click();
    await page.locator('#b-approve').click();
    await page.locator('#b-attempt').click();
    await page.locator('#b-check-receipt').click();
    await expect(page.locator('#receipt-check')).toHaveAttribute('open', '');
    await expect(page.locator('#rc-input')).toHaveValue(/"policy_digest"/);
    await expect(page.locator('#rc-result')).toContainText('Valid. Signed by');
    await expect(page.locator('#rc-result')).toContainText('does not show the action ran');
    await expect(page.locator('#receipt-check summary')).toBeInViewport();
  });

  test('a changed character in that receipt is then caught', async ({ page }) => {
    await page.locator('#b-backup').click();
    await page.locator('#b-approve').click();
    await page.locator('#b-attempt').click();
    await page.locator('#b-check-receipt').click();
    await page.locator('#rc-flip').click();
    await expect(page.locator('#rc-result')).toContainText('signature does not match');
  });
});

test.describe('The agents page leads on to Private Intent', () => {
  test('has a next-step card and a footer link that go to the page', async ({ page, request }) => {
    await page.goto('/agents/', { waitUntil: 'domcontentloaded' });
    const card = page.locator('#next-private-intent');
    await expect(card.getByRole('heading', { level: 2 })).toContainText('Private Intent checks who said yes');
    await expect(card.getByRole('link', { name: 'Try Private Intent' })).toHaveAttribute('href', '../private-intent/');
    await expect(page.locator('footer a[href="../private-intent/"]')).toHaveCount(1);
    expect((await request.get('/private-intent/')).status()).toBe(200);
  });

  test('says only what is true about the page it links to', async ({ page }) => {
    await page.goto('/agents/', { waitUntil: 'domcontentloaded' });
    const text = (await page.locator('#next-private-intent').innerText()).toLowerCase();
    expect(text).toContain('nothing is sent anywhere');
    for (const claim of ['zero-knowledge', 'self-serve', 'enterprise-grade', 'guarantee']) expect(text).not.toContain(claim);
  });
});
