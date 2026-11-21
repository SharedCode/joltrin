import { test, expect } from '@playwright/test';

async function fill(page, v: Record<string, string>) {
  for (const [id, value] of Object.entries(v)) await page.locator('#' + id).fill(value);
}

test.describe('Pilot worksheet', () => {
  test('stays empty until every number is entered, then shows the arithmetic', async ({ page }) => {
    await page.goto('/pilot/', { waitUntil: 'domcontentloaded' });
    await expect(page.locator('#result')).toBeHidden();
    await fill(page, { decisions: '200', blocked: '10', days: '15', share: '20', cost: '5000' });
    await expect(page.locator('#result')).toBeHidden();
    await fill(page, { adopt: '1000' });
    await expect(page.locator('#result')).toBeVisible();
    await expect(page.locator('#o-rate')).toHaveText('5% of 200 decisions');
    await expect(page.locator('#o-month')).toHaveText('20');
    await expect(page.locator('#o-inc')).toHaveText('4');
    await expect(page.locator('#o-usd')).toHaveText('$20,000');
    await expect(page.locator('#o-net')).toContainText('$19,000 more than it costs');
  });

  test('says when it costs more than it saves', async ({ page }) => {
    await page.goto('/pilot/', { waitUntil: 'domcontentloaded' });
    await fill(page, { decisions: '100', blocked: '1', days: '30', share: '10', cost: '100', adopt: '5000' });
    await expect(page.locator('#o-net')).toContainText('less than it costs');
  });

  test('rejects impossible input with a message and no result', async ({ page }) => {
    await page.goto('/pilot/', { waitUntil: 'domcontentloaded' });
    await fill(page, { decisions: '10', blocked: '11', days: '7', share: '50', cost: '10', adopt: '10' });
    await expect(page.locator('#error')).toContainText('cannot be more than decisions');
    await expect(page.locator('#blocked')).toHaveAttribute('aria-invalid', 'true');
    await expect(page.locator('#result')).toBeHidden();
    await fill(page, { blocked: '5', share: '120' });
    await expect(page.locator('#error')).toContainText('between 0 and 100');
    await fill(page, { share: '-1' });
    await expect(page.locator('#error')).toContainText('zero or more');
  });

  test('has no built-in numbers in the fields and is listed in the sitemap', async ({ page, request }) => {
    await page.goto('/pilot/', { waitUntil: 'domcontentloaded' });
    for (const id of ['decisions', 'blocked', 'days', 'share', 'cost', 'adopt']) {
      await expect(page.locator('#' + id)).toHaveValue('');
    }
    const res = await request.get('/sitemap.xml');
    expect(await res.text()).toContain('https://joltrinhq.com/pilot/');
  });
});
