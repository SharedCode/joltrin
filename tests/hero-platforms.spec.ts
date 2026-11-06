import { test, expect } from '@playwright/test';

const shell = 'curl -fsSL https://raw.githubusercontent.com/SharedCode/joltrin/master/scripts/install.sh | sh';
const powershell = 'irm https://raw.githubusercontent.com/SharedCode/joltrin/master/scripts/install.ps1 | iex';
const platforms = [
  { name: 'macOS', id: 'mcp-command', command: shell },
  { name: 'Linux', id: 'mcp-command', command: shell },
  { name: 'Windows PowerShell', id: 'mcp-command-powershell', command: powershell },
  { name: 'Windows Command Prompt (CMD)', id: 'mcp-command-cmd', command: `powershell -NoProfile -ExecutionPolicy Bypass -Command "${powershell}"` },
];

for (const width of [375, 1440]) {
  test.describe(`Hero terminal instructions, ${width}px`, () => {
    test.use({ viewport: { width, height: 900 }, reducedMotion: 'reduce' });
    test('each named terminal reaches the exact installer and copies it by keyboard', async ({ page }) => {
      await page.addInitScript(() => {
        Object.defineProperty(navigator, 'clipboard', { value: {
          writeText: async (value: string) => { (window as Window & { copiedInstall?: string }).copiedInstall = value; },
        } });
      });
      for (const platform of platforms) {
        await page.goto('/#start');
        const link = page.locator('#quickstart-platforms').getByRole('link', { name: platform.name, exact: true });
        await link.focus();
        await page.keyboard.press('Enter');
        await expect(page).toHaveURL(new RegExp(`/docs/#${platform.id}$`));
        await expect(page.locator(`#${platform.id}`)).toHaveText(platform.command);
        const button = page.locator(`button[data-command="${platform.id}"]`);
        await button.focus();
        await page.keyboard.press('Enter');
        expect(await page.evaluate(() => (window as Window & { copiedInstall?: string }).copiedInstall)).toBe(platform.command);
        expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
      }
    });
    test('terminal links and commands work with JavaScript disabled', async ({ browser }) => {
      const context = await browser.newContext({ javaScriptEnabled: false, viewport: { width, height: 900 } });
      const page = await context.newPage();
      for (const platform of platforms) {
        await page.goto('/#start');
        await page.locator('#quickstart-platforms').getByRole('link', { name: platform.name, exact: true }).click();
        await expect(page.locator(`#${platform.id}`)).toHaveText(platform.command);
      }
      await context.close();
    });
  });
}

test.describe('Hero install order', () => {
  test('the no-Go installer comes before the Go command and copies by keyboard', async ({ page }) => {
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', { value: {
        writeText: async (value: string) => { (window as Window & { copiedInstall?: string }).copiedInstall = value; },
      } });
    });
    await page.goto('/');
    await expect(page.locator('#install-cmd')).toHaveText(shell);
    const installTop = (await page.locator('#install-cmd').boundingBox())!.y;
    const goTop = (await page.locator('#quickstart-cmd').boundingBox())!.y;
    expect(installTop).toBeLessThan(goTop);
    await page.locator('#install-copy').focus();
    await page.keyboard.press('Enter');
    expect(await page.evaluate(() => (window as Window & { copiedInstall?: string }).copiedInstall)).toBe(shell);
  });

  test('the line under the buttons tells a first-time visitor they do not need Go', async ({ page }) => {
    await page.goto('/');
    await expect(page.getByText('no Go needed to install')).toBeVisible();
  });

  test('the first screen says what to ask the agent after installing', async ({ page }) => {
    await page.goto('/');
    const next = page.locator('#after-install');
    await expect(next).toBeVisible();
    await expect(next).toContainText('drop_prod_db');
    await expect(next).toContainText('db-maintenance');
    await expect(next.getByRole('link', { name: 'the browser demo' })).toHaveAttribute('href', './agents/');
  });
});

const firstRunControls = ['#quickstart-platforms a', '#install-copy', '#quickstart-copy', '#hero-wrap a[href="#demo-video"]', '#hero-wrap a[href="#pricing"]'];

test('first-run controls are at least 24px tall', async ({ page }) => {
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/');
  for (const selector of firstRunControls) {
    for (const el of await page.locator(selector).all()) {
      const box = (await el.boundingBox())!;
      expect(box.height, `${selector} is ${box.height}px tall`).toBeGreaterThanOrEqual(24);
    }
  }
});

test.describe('on a touch screen', () => {
  test.use({ hasTouch: true, isMobile: true, viewport: { width: 390, height: 844 } });
  test('first-run controls are at least 44px tall', async ({ page, browserName }) => {
    test.skip(browserName === 'firefox', 'Firefox does not support mobile emulation');
    await page.goto('/');
    for (const selector of firstRunControls) {
      for (const el of await page.locator(selector).all()) {
        const box = (await el.boundingBox())!;
        expect(box.height, `${selector} is ${box.height}px tall`).toBeGreaterThanOrEqual(44);
      }
    }
  });
});

test.describe('copy buttons say what happened', () => {
  test('success is announced and the label resets', async ({ page }) => {
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', { value: { writeText: async () => {} } });
    });
    await page.goto('/');
    await page.locator('#install-copy').click();
    await expect(page.locator('#copy-status')).toHaveText('Install command copied.');
    await expect(page.locator('#install-copy')).toHaveText('Copied');
    await expect(page.locator('#install-copy')).toHaveText('Copy', { timeout: 5000 });
  });

  test('without a clipboard the command is selected and the visitor is told', async ({ page }) => {
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', { value: undefined });
    });
    await page.goto('/');
    await page.locator('#install-copy').click();
    await expect(page.locator('#copy-status')).toContainText('The command is selected');
    expect(await page.evaluate(() => window.getSelection()?.toString())).toContain('install.sh | sh');
  });

  test('a denied clipboard falls back the same way', async ({ page }) => {
    await page.addInitScript(() => {
      Object.defineProperty(navigator, 'clipboard', { value: { writeText: async () => { throw new Error('denied'); } } });
    });
    await page.goto('/');
    await page.locator('#quickstart-copy').click();
    await expect(page.locator('#copy-status')).toContainText('The command is selected');
    expect(await page.evaluate(() => window.getSelection()?.toString())).toContain('go run github.com');
  });
});
