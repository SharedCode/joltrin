import { defineConfig, devices } from '@playwright/test';

/**
 * Staff QA & Automation Engineering - Playwright Configuration
 * Joltrin Web Platform & Docs Suite
 *
 * Configured for multi-engine resilience (Chromium, Firefox, WebKit, Mobile viewports),
 * automatic failure diagnostics capture (traces, videos, screenshots), and CI-tailored timeouts.
 */

const PORT = process.env.PORT ? parseInt(process.env.PORT, 10) : 4173;
const BASE_URL = process.env.BASE_URL || `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  workers: process.env.CI ? 2 : undefined,

  // Generates rich interactive HTML report with failure traces & terminal list outputs
  reporter: [
    ['html', { outputFolder: 'playwright-report', open: 'never' }],
    ['list'],
  ],

  // Comprehensive timeouts suited for CI execution and WASM initialization
  timeout: 60_000,
  expect: {
    timeout: 10_000,
  },

  use: {
    baseURL: BASE_URL,
    actionTimeout: 15_000,
    navigationTimeout: 30_000,

    // Automatic diagnostic artifact capture upon test regression
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',

    // Standardized test attributes
    testIdAttribute: 'data-testid',
  },

  // Multi-engine matrix & responsive viewport emulation
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1440, height: 900 },
      },
    },
    {
      name: 'firefox',
      use: {
        ...devices['Desktop Firefox'],
        viewport: { width: 1440, height: 900 },
      },
    },
    {
      name: 'webkit',
      use: {
        ...devices['Desktop Safari'],
        viewport: { width: 1440, height: 900 },
      },
    },
    {
      name: 'mobile-chrome',
      use: {
        ...devices['Pixel 5'],
      },
    },
    {
      name: 'mobile-safari',
      use: {
        ...devices['iPhone 12'],
      },
    },
  ],

  // Automated webServer lifecycle management: boots static server with WASM MIME type support
  webServer: {
    command: 'node scripts/serve-test-site.mjs',
    url: BASE_URL,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
