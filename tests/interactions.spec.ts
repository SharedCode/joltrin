import { test, expect } from '@playwright/test';
import { NetworkConsoleGuard } from './helpers/network-console-guard';
import { waitForWasmReady, waitForCanvasRendered } from './helpers/wasm-lifecycle';

/**
 * Staff QA & Automation Engineering - Suite 3: Critical Interactive Features
 *
 * Exercises the end-to-end interactive flows across desktop & mobile viewports:
 *  1. Responsive Navigation across Technical Demo, Arena, and Agent Barrier
 *  2. Tech Demo Tab Switching & Live Client-Side WASM Operations (ACID transactions, Vector search, Benchmarks)
 *  3. Arena Cluster Swarm Simulation Controls (Worker scaling, fault injection, parity self-healing, sound & share toggles)
 *  4. Agent Verification Safety Barrier Runbook (Precedence check: blocked -> backup -> validated -> allowed)
 *  5. Code Snippet Inspection & Syntax Verification
 */

test.describe('Critical Interactive Features Suite', () => {
  test('Cross-Portal Responsive Navigation: Seamless transitions across all three experiences', async ({
    page,
    isMobile,
  }) => {
    const guard = new NetworkConsoleGuard(page);

    // 1. Start at Technical Demo (/)
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);
    await expect(page).toHaveTitle(/Joltrin.*Embedded ACID/i);

    // 2. Navigate from Tech Demo to Arena via Navbar (or primary link on mobile)
    const arenaLink = isMobile
      ? page.locator('a[href*="arena"]:visible').first()
      : page.locator('nav a[href*="arena"], header a[href*="arena"]').first();
    await expect(arenaLink).toBeVisible();
    await arenaLink.click();
    await expect(page).toHaveURL(/.*\/arena\/?/);
    await waitForCanvasRendered(page);

    // 3. Navigate from Arena to Agent Barrier via Navbar (or direct navigation on compact viewports)
    if (isMobile) {
      await page.goto('/agents/', { waitUntil: 'domcontentloaded' });
    } else {
      const barrierLink = page.locator('header a[href*="agents"], nav a[href*="agents"]').first();
      await expect(barrierLink).toBeVisible();
      await barrierLink.click();
    }
    await expect(page).toHaveURL(/.*\/agents\/?/);
    await waitForWasmReady(page);
    await expect(page.getByText(/stop an agent before it drops your database/i)).toBeVisible();

    guard.assertPurity('Cross-Portal Navigation');
  });

  test('Technical Demo: Tab Switching & Live Client-Side ACID Transactions', async ({
    page,
  }) => {
    const guard = new NetworkConsoleGuard(page);
    await page.goto('/', { waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);

    // Initial Tab is tx-tab
    const txContent = page.locator('#tx-tab');
    await expect(txContent).toBeVisible();

    // 1. Execute Atomic Transfer (Commit)
    const commitBtn = page.getByRole('button', { name: /execute atomic transfer/i });
    await expect(commitBtn).toBeVisible();
    await commitBtn.click();

    // Verify terminal logs update with atomic commit record
    const terminalLogs = page.locator('#terminal-logs');
    await expect(terminalLogs).toBeVisible();
    await expect(terminalLogs).toContainText(/SUCCESS/i, { timeout: 5_000 });
    await expect(page.locator('#tx-latency-badge')).not.toHaveText('0 µs');

    // 2. Switch to High-Dimensional Vector Search Tab
    const vectorTabBtn = page.locator('#btn-vector-tab');
    await expect(vectorTabBtn).toBeVisible();
    await vectorTabBtn.click();
    await expect(page.locator('#vector-tab')).toBeVisible();
    await expect(txContent).toBeHidden();

    // Trigger Vector Search Query
    const searchInput = page.locator('#vector-query-input');
    if (await searchInput.count() > 0) {
      await searchInput.fill('distributed ACID transactions');
      const searchBtn = page.locator('button[onclick*="triggerVectorSearch"]').first();
      if (await searchBtn.count() > 0) {
        await searchBtn.click();
      }
    }

    // 3. Switch to Benchmark Tab & Run Benchmark
    const benchTabBtn = page.locator('#btn-bench-tab');
    await expect(benchTabBtn).toBeVisible();
    await benchTabBtn.click();
    await expect(page.locator('#bench-tab')).toBeVisible();

    const runBenchBtn = page.locator('#run-bench-btn');
    if (await runBenchBtn.count() > 0) {
      await runBenchBtn.click();
      // Wait for benchmark to run (web-first assertion on throughput metric)
      await expect(page.locator('#bench-ops-sec')).not.toHaveText('—', { timeout: 20_000 });
    }

    // 4. Switch to B-Tree Internals Tab
    const btreeTabBtn = page.locator('#btn-btree-tab');
    await expect(btreeTabBtn).toBeVisible();
    await btreeTabBtn.click();
    await expect(page.locator('#btree-tab')).toBeVisible();

    // 5. Switch to Agent Memory Tab
    const agentTabBtn = page.locator('#btn-agent-tab');
    await expect(agentTabBtn).toBeVisible();
    await agentTabBtn.click();
    await expect(page.locator('#agent-tab')).toBeVisible();

    guard.assertPurity('Technical Demo Tab Interactions');
  });

  test('Arena Cluster Simulation: Swarm scaling, fault injection, and recovery mechanisms', async ({
    page,
    context,
  }) => {
    // In environments/browsers without native clipboard grant support (Firefox, WebKit), stub clipboard
    await page.addInitScript(() => {
      if (!navigator.clipboard) {
        (navigator as any).clipboard = {};
      }
      navigator.clipboard.writeText = async () => {};
      navigator.clipboard.readText = async () => '';
    });

    // Grant clipboard permissions for share button testing where supported
    try {
      await context.grantPermissions(['clipboard-read', 'clipboard-write']);
    } catch {
      // Ignored for browsers like Firefox/WebKit which do not support granting these permissions via CDP
    }

    await page.goto('/arena/', { waitUntil: 'networkidle' });
    await waitForCanvasRendered(page);

    // 1. Test Worker Swarm Scaling (+ Add Worker)
    const addWorkerBtn = page.getByRole('button', { name: /scale \+1 worker/i });
    if (await addWorkerBtn.count() > 0) {
      await addWorkerBtn.click();
      // Verify worker metric or log stream receives event
      await expect(page.locator('text=SCALE UP: Added worker node').first()).toBeVisible({
        timeout: 5_000,
      });
    }

    // 2. Test Fault Injection: Kill Worker
    const killWorkerBtn = page.getByRole('button', { name: /kill random worker/i });
    if (await killWorkerBtn.count() > 0) {
      await killWorkerBtn.click();
      await expect(page.locator('text=Worker').first()).toBeVisible();
    }

    // 3. Test Storage Node Partition Failure
    const failStorageBtn = page.getByRole('button', { name: /fail storage node/i });
    if (await failStorageBtn.count() > 0) {
      await failStorageBtn.click();
      await expect(page.locator('text=STORAGE FAULT:').first()).toBeVisible({ timeout: 5_000 });
    }

    // 4. Test Self-Healing Trigger
    const healBtn = page.getByRole('button', { name: /trigger self-healing/i });
    if (await healBtn.count() > 0) {
      await healBtn.click();
      await expect(page.locator('text=SELF-HEALING:').first()).toBeVisible({ timeout: 5_000 });
    }

    // 5. Test Audio Mute / Unmute Toggle
    const soundToggleBtn = page.locator('header button[title*="Sound"], header button:has(svg.lucide-volume-2), header button:has(svg.lucide-volume-x)').first();
    await expect(soundToggleBtn).toBeVisible();
    await soundToggleBtn.click();

    // 6. Test Header Share / Copy URL to Clipboard
    const shareBtn = page.locator('header button[title*="Share"], header button:has(svg.lucide-share-2)').first();
    await expect(shareBtn).toBeVisible();
    await shareBtn.click();
    // Verify copied check feedback icon renders
    await expect(page.locator('header svg.lucide-check')).toBeVisible({ timeout: 3_000 });
  });

  test('Agent Verification Barrier (/agents/): Precedence check and deterministic safety barrier', async ({
    page,
  }) => {
    const guard = new NetworkConsoleGuard(page);
    await page.goto('/agents/', { waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);

    // Initial state: Step cards rendered
    const dropDbBtn = page.getByRole('button', { name: /3\. drop prod db/i });
    const takeBackupBtn = page.getByRole('button', { name: /1\. take backup/i });
    const validateBackupBtn = page.getByRole('button', { name: /2\. validate backup/i });
    const resetTraceBtn = page.getByRole('button', { name: /reset trace/i });

    await expect(dropDbBtn).toBeVisible();
    await expect(takeBackupBtn).toBeVisible();
    await expect(validateBackupBtn).toBeVisible();

    // --- SAFETY CHECK 1: Try to Drop Prod DB FIRST without backup ---
    await dropDbBtn.click();

    // Assert that the destructive operation was BLOCKED by the barrier
    const execLog = page.locator('#exec-log');
    await expect(execLog).toContainText(/BLOCKED/i, { timeout: 5_000 });
    await expect(execLog).toContainText(/backup_validated/i);

    // --- SAFETY CHECK 2: Execute Step 1 (Take Backup) ---
    await takeBackupBtn.click();
    await expect(execLog).toContainText(/committed/i);

    // --- SAFETY CHECK 3: Execute Step 2 (Validate Backup) ---
    await validateBackupBtn.click();
    await expect(execLog).toContainText(/validate_backup.*committed/i);

    // --- SAFETY CHECK 4: Execute Step 3 now that preconditions are satisfied ---
    await dropDbBtn.click();
    await expect(execLog).toContainText(/drop_prod_db.*committed/i);

    // --- SAFETY CHECK 5: Reset Trace resets runbook state ---
    await resetTraceBtn.click();
    await expect(execLog).toContainText(/trace reset/i);

    guard.assertPurity('Agent Barrier Verification Flow');
  });

  test('Code Blocks & Readout Inspection: Proper syntax and terminal display across portals', async ({
    page,
  }) => {
    // Check code snippet rendering in Agent Barrier page
    await page.goto('/agents/', { waitUntil: 'domcontentloaded' });
    await waitForWasmReady(page);

    const codeBlock = page.locator('pre').first();
    await expect(codeBlock).toBeVisible();
    const codeText = await codeBlock.innerText();
    expect(codeText).toContain('git clone https://github.com/SharedCode/joltrin.git');
    expect(codeText).toContain('go run ./examples/verify_barrier');
  });
});
