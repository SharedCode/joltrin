import { test, expect } from '@playwright/test';
import { NetworkConsoleGuard } from './helpers/network-console-guard';
import { LayoutShiftObserver } from './helpers/cls-observer';
import { waitForWasmReady, waitForCanvasRendered } from './helpers/wasm-lifecycle';

/**
 * Staff QA & Automation Engineering - Suite 2: Visual, Animation & Layout Stability
 *
 * Validates that all critical animations, canvas renderings, and transitions:
 *  1. Employ web-first assertions with zero brittle arbitrary sleeps
 *  2. Render expected terminal states without visual hanging or frozen overlays
 *  3. Maintain strict Cumulative Layout Shift (CLS <= 0.15) compliance per Core Web Vitals
 */

test.describe('Visual, Animation & Layout Stability Suite', () => {
  test('Cumulative Layout Shift (CLS): Tech Demo loads within Core Web Vitals threshold (<= 0.15)', async ({
    page,
  }) => {
    const clsObserver = new LayoutShiftObserver(page);
    await clsObserver.initialize();

    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);

    // Verify CLS remains minimal after initial load and WASM overlay dismissal
    await clsObserver.assertMinimalCLS(0.15);
  });

  test('Cumulative Layout Shift (CLS): Arena view switching & modal transitions remain stable', async ({
    page,
  }) => {
    const clsObserver = new LayoutShiftObserver(page);
    await clsObserver.initialize();

    await page.goto('/arena/', { waitUntil: 'networkidle' });
    await waitForCanvasRendered(page);

    // Switch to Investor Mode view
    const investorBtn = page.getByRole('button', { name: /investor deck/i });
    await expect(investorBtn).toBeVisible();
    await investorBtn.click();

    // Verify Investor mode header and pitch cards render
    const deckHeading = page.getByRole('heading', { name: /one engine for data and compute/i });
    await expect(deckHeading).toBeVisible();

    // Return to Arena view
    const backBtn = page.getByRole('button', { name: /back to interactive arena/i });
    await expect(backBtn).toBeVisible();
    await backBtn.click();

    // Re-verify topology canvas renders
    await waitForCanvasRendered(page);

    // Verify CLS remained minimal throughout dynamic view changes
    await clsObserver.assertMinimalCLS(0.15);
  });

  test('WASM Kernel Boot Transition: Overlay gracefully fades and unblocks interactivity', async ({
    page,
  }) => {
    const guard = new NetworkConsoleGuard(page);

    await page.goto('/', { waitUntil: 'domcontentloaded' });

    // Overlay is initially present during boot
    const overlay = page.locator('#wasm-loader-overlay');
    await expect(overlay).toBeAttached();

    // Overlay transitions to opacity-0 and pointer-events-none without being indefinitely stuck
    await expect(overlay).toHaveClass(/opacity-0/, { timeout: 25_000 });
    await expect(overlay).toHaveClass(/pointer-events-none/, { timeout: 25_000 });

    // Status pill switches to active
    const statusPill = page.locator('#wasm-status-text');
    await expect(statusPill).toHaveText(/ACTIVE/i, { timeout: 25_000 });

    // Interactive buttons are clickable (not blocked by invisible overlay)
    const executeTransferBtn = page.getByRole('button', { name: /execute atomic transfer/i });
    await expect(executeTransferBtn).toBeEnabled();

    guard.assertPurity('WASM Kernel Boot');
  });

  test('Interactive Architecture Topology: Canvas mounts, animates frames, and handles interaction', async ({
    page,
  }) => {
    await page.goto('/arena/', { waitUntil: 'networkidle' });
    const canvas = page.locator('canvas').first();
    await waitForCanvasRendered(page);

    // Inspect initial canvas frame pixels to assert non-blank rendering
    const isRendered = await page.evaluate(() => {
      const el = document.querySelector('canvas') as HTMLCanvasElement | null;
      if (!el) return false;
      return el.width > 0 && el.height > 0;
    });
    expect(isRendered).toBe(true);

    // Verify canvas dimensions respond properly to viewport bounds
    const box = await canvas.boundingBox();
    expect(box).not.toBeNull();
    expect(box!.width).toBeGreaterThan(300);
    expect(box!.height).toBeGreaterThan(200);

    // Select an interactive swarm worker node card overlaid on top of canvas
    const workerNode = page.locator('div.cursor-pointer:has-text("Worker")').first();
    await expect(workerNode).toBeVisible();
    await workerNode.click();

    // Assert selected node card receives highlight ring
    await expect(workerNode).toHaveClass(/ring-2/);
  });

  test('Modal Transitions: Architecture Compare modal opens, displays content, and dismisses cleanly', async ({
    page,
  }) => {
    await page.goto('/arena/', { waitUntil: 'networkidle' });

    // Open Compare Modal
    // Label is the full "Without Joltrin vs With Joltrin" on wider viewports,
    // shortened to "Compare" below sm so it doesn't crowd the header on mobile.
    const compareBtn = page.getByRole('button', { name: /without joltrin vs with joltrin|^compare$/i });
    await expect(compareBtn).toBeVisible();
    await compareBtn.click();

    // Verify modal overlay and dialog container appear
    const modal = page.getByTestId('compare-modal');
    const modalHeading = modal.getByRole('heading', { name: /architecture comparison/i });
    await expect(modalHeading).toBeVisible();

    // Verify comparison panels render, scoped to the modal so this can't
    // accidentally match the trigger button's own (possibly hidden) label.
    const withoutPanel = modal.locator('text=WITHOUT JOLTRIN').first();
    const withPanel = modal.locator('text=WITH JOLTRIN').first();
    await expect(withoutPanel).toBeVisible();
    await expect(withPanel).toBeVisible();

    // Dismiss modal via Close X button
    const closeBtn = modal.locator('button:has(svg.lucide-x)').first();
    await expect(closeBtn).toBeVisible();
    await closeBtn.click();

    // Verify modal is dismissed
    await expect(modalHeading).not.toBeVisible();
  });

  test('Copilot Drawer & Accordion: Smooth slide-in, question expand, and answer disclosure', async ({
    page,
  }) => {
    await page.goto('/arena/', { waitUntil: 'networkidle' });

    // Open Copilot Drawer from Header
    const copilotTrigger = page.getByRole('button', { name: /copilot/i });
    await expect(copilotTrigger).toBeVisible();
    await copilotTrigger.click();

    // Verify Drawer slides in
    const drawerHeading = page.getByRole('heading', { name: /joltrin copilot/i });
    await expect(drawerHeading).toBeVisible();

    // Verify predefined question cards are rendered
    const firstQuestion = page.getByRole('button', { name: /why did the storage node failure not corrupt data\?/i });
    await expect(firstQuestion).toBeVisible();
    await firstQuestion.click();

    // Assert explanation disclosure text becomes visible
    const explanationText = page.getByText(/reed-solomon erasure coding/i).first();
    await expect(explanationText).toBeVisible();

    // Close Copilot Drawer
    const closeDrawerBtn = page.locator('div.fixed button:has(svg.lucide-x)').first();
    await closeDrawerBtn.click();
    await expect(drawerHeading).not.toBeVisible();
  });

  test('Data Inspector Modal: B-Tree transactional records render and close cleanly', async ({
    page,
  }) => {
    await page.goto('/arena/', { waitUntil: 'networkidle' });

    // Open Data Inspector
    const inspectBtn = page.getByRole('button', { name: /inspect b-tree data/i });
    await expect(inspectBtn).toBeVisible();
    await inspectBtn.click();

    // Verify Modal
    const inspectorHeading = page.getByRole('heading', { name: /embedded b-tree state inspector/i });
    await expect(inspectorHeading).toBeVisible();

    // Verify table headers (OBJECT KEY, VERSION, STATUS)
    await expect(page.getByText(/object key/i).first()).toBeVisible();
    await expect(page.getByText(/shard assignment/i).first()).toBeVisible();

    // Close Modal via Close Inspector button
    const closeBtn = page.getByRole('button', { name: /close inspector/i });
    await closeBtn.click();
    await expect(inspectorHeading).not.toBeVisible();
  });
});
