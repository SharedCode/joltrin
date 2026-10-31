import { Page, expect } from '@playwright/test';

/**
 * Web-First WASM Lifecycle & Rendering Synchronization
 *
 * Replaces brittle page.waitForTimeout sleeps with deterministic DOM state
 * and WebAssembly execution boundary listeners.
 */
export async function waitForWasmReady(page: Page): Promise<void> {
  // The homepage downloads its engine on the first interaction. Tests that wait for it start it here.
  await page.evaluate(() => (window as any).startWasmEngine?.());

  // Wait for the loader overlay to hide (opacity-0 class added upon WASM instantiation)
  const overlay = page.locator('#wasm-loader-overlay');
  if (await overlay.count() > 0) {
    await expect(overlay).toHaveClass(/opacity-0/, { timeout: 25_000 });
  }

  // Wait for status text to confirm active kernel status
  const statusText = page.locator('#wasm-status-text');
  if (await statusText.count() > 0) {
    await expect(statusText).toHaveText(/ACTIVE/i, {
      timeout: 25_000,
    });
  }

  // Ensure OPFS status badge has resolved away from initial "checking..." state
  const opfsBadge = page.locator('#opfs-status-text');
  if (await opfsBadge.count() > 0) {
    await expect(opfsBadge).not.toHaveText('checking...', { timeout: 15_000 });
  }
}

/**
 * Ensures Canvas animation loop has initialized and rendered frames
 */
export async function waitForCanvasRendered(page: Page, canvasSelector = 'canvas'): Promise<void> {
  const canvas = page.locator(canvasSelector).first();
  await expect(canvas).toBeVisible({ timeout: 15_000 });

  // Verify canvas dimensions are valid (> 0)
  const box = await canvas.boundingBox();
  expect(box).not.toBeNull();
  expect(box!.width).toBeGreaterThan(100);
  expect(box!.height).toBeGreaterThan(100);

  // Wait for at least 2 animation frames to confirm continuous render loop
  await page.evaluate(() => {
    return new Promise<void>((resolve) => {
      requestAnimationFrame(() => {
        requestAnimationFrame(() => {
          resolve();
        });
      });
    });
  });
}

/**
 * Lets the page run a few animation frames, so scroll handlers and timers that react to the
 * last action have run. Use it before asserting that something did NOT happen.
 */
export async function nextFrames(page: Page, frames = 5): Promise<void> {
  await page.evaluate(
    (n) =>
      new Promise<void>((resolve) => {
        const step = (left: number) => (left <= 0 ? resolve() : requestAnimationFrame(() => step(left - 1)));
        step(n);
      }),
    frames,
  );
}

/** Waits until the page's form-velocity guard (render timestamp older than 1.2 s) lets a submit through. */
export async function waitForFormReady(page: Page, tsId: string): Promise<void> {
  await page.waitForFunction(
    (id) => {
      const el = document.getElementById(id) as HTMLInputElement | null;
      return !!el && el.value !== '' && Date.now() - Number(el.value) > 1300;
    },
    tsId,
  );
}
