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
