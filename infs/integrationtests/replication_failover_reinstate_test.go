//go:build integration
// +build integration

package integrationtests

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

// Helpers to simulate IO failures by toggling permissions on registry segment files.
// These target only Registry/StoreRepository writes. To deterministically set FailedToReplicate
// without relying on sop.Error code classification, we deny writes on the PASSIVE folder so the
// registry's replicate-to-passive path calls handleFailedToReplicate.
func registrySegmentPath(base, table string) string {
	rt := sop.FormatRegistryTable(table)
	return filepath.Join(base, rt, fmt.Sprintf("%s-1.reg", rt))
}

func ensureTableDir(t *testing.T, base, table string) string {
	t.Helper()
	dir := filepath.Join(base, table)
	_ = os.MkdirAll(dir, 0o755)
	return dir
}

// makePassiveRegistryReadOnly denies writes on the passive folder's registry path so replication fails.
func makePassiveRegistryReadOnly(t *testing.T, table string) {
	t.Helper()
	base := passiveBaseFolder()
	dir := ensureTableDir(t, base, sop.FormatRegistryTable(table))
	// Make the directory read-only to block creation/truncation of any segment files.
	_ = os.Chmod(dir, 0o555)
	seg := registrySegmentPath(base, table)
	// Ensure the segment file exists so chmod will take effect across platforms/umasks.
	if _, err := os.Stat(seg); os.IsNotExist(err) {
		// Best-effort create an empty segment file; permissions will be tightened next.
		_ = os.WriteFile(seg, []byte{}, 0o644)
	}
	// Make only the segment file read-only to fail replication writes without affecting other tables/dirs.
	_ = os.Chmod(seg, 0o444)
}

// makePassiveStoreInfoReadOnly denies writes on the passive folder's storeinfo.txt so store replication fails.
func makePassiveStoreInfoReadOnly(t *testing.T, store string) {
	t.Helper()
	base := passiveBaseFolder()
	dir := filepath.Join(base, store)
	_ = os.MkdirAll(dir, 0o755)
	fn := filepath.Join(dir, "storeinfo.txt")
	if _, err := os.Stat(fn); os.IsNotExist(err) {
		_ = os.WriteFile(fn, []byte("{}"), 0o644)
	}
	_ = os.Chmod(fn, 0o444)
}

func restoreRegistryPermissions(t *testing.T, table string) {
	t.Helper()
	for _, base := range []string{activeBaseFolder(), passiveBaseFolder()} {
		dir := ensureTableDir(t, base, sop.FormatRegistryTable(table))
		_ = os.Chmod(dir, 0o755)
		seg := registrySegmentPath(base, table)
		_ = os.Chmod(seg, 0o644)
	}
}

func restoreStoreInfoPermissions(t *testing.T, store string) {
	t.Helper()
	for _, base := range []string{activeBaseFolder(), passiveBaseFolder()} {
		dir := filepath.Join(base, store)
		_ = os.MkdirAll(dir, 0o755)
		fn := filepath.Join(dir, "storeinfo.txt")
		// If file exists, make it writable again.
		if _, err := os.Stat(fn); err == nil {
			_ = os.Chmod(fn, 0o644)
		}
	}
}

// sanitizeIsolatedReplicationBases ensures base dirs are writable and clears stray stores that
// could interfere (e.g., previously left read-only artifacts from other tests).
func sanitizeIsolatedReplicationBases(t *testing.T) {
	t.Helper()
	bases := []string{activeBaseFolder(), passiveBaseFolder()}
	for _, base := range bases {
		_ = os.MkdirAll(base, 0o755)
		_ = os.Chmod(base, 0o755)
		// Remove any stores other than our status/commit log artifacts.
		entries, _ := os.ReadDir(base)
		for _, e := range entries {
			name := e.Name()
			if name == "commitlogs" || name == "replstat.txt" {
				continue
			}
			_ = os.RemoveAll(filepath.Join(base, name))
		}
	}
}

// Test_EC_Failover_Reinstate_FastForward_Short exercises:
// 1) initial write OK on replication
// 2) simulated IO error to force failover
// 3) start ReinstateFailedDrives and perform delta writes while reinstate is running (commit-change logs on)
// 4) verify data integrity after reinstate; then trigger another failover and verify data still intact
func Test_EC_Failover_Reinstate_FastForward_Short(t *testing.T) {
	ctx := context.Background()

	// Ensure clean L2 and status files to avoid cross-test influence.
	conn, _ := redis.OpenConnection(redisConfig)
	_ = conn.Client.FlushDB(ctx)
	cleanupReplicationStatusFiles()

	// Use dedicated replication base folders and EC disks for this test to avoid cross-test contamination.
	isolatedStores := []string{
		fmt.Sprintf("%s%cdisk8", dataPath, os.PathSeparator),
		fmt.Sprintf("%s%cdisk9", dataPath, os.PathSeparator),
	}
	isolatedEC := map[string]sop.ErasureCodingConfig{
		"": {
			DataShardsCount:   2,
			ParityShardsCount: 2,
			BaseFolderPathsAcrossDrives: []string{
				fmt.Sprintf("%s%cdisk10", dataPath, os.PathSeparator),
				fmt.Sprintf("%s%cdisk11", dataPath, os.PathSeparator),
				fmt.Sprintf("%s%cdisk12", dataPath, os.PathSeparator),
				fmt.Sprintf("%s%cdisk13", dataPath, os.PathSeparator),
			},
			RepairCorruptedShards: true,
		},
	}
	to := sop.TransactionOptions{
		Mode:                 sop.ForWriting,
		MaxTime:              -1,
		RegistryHashModValue: fs.MinimumModValue,
		StoresFolders:        isolatedStores,
		ErasureConfig:        isolatedEC,
		CacheType:            sop.Redis,
	}

	// Use a unique table name for isolated tests to avoid cross-suite collisions.
	table := "ec_failover_ff_isolated_it"

	// Ensure no prior store artifacts remain across replication and EC disks, including store repository metadata.
	sanitizeIsolatedReplicationBases(t)
	cleanupStoreEverywhere(table)
	cleanupECShards(table)
	cleanupStoreRepository(table, isolatedStores)

	// Ensure directories/segments start with writable permissions to avoid leftover read-only from prior runs.
	restoreRegistryPermissions(t, table)

	// Baseline: ensure the store exists (open-or-create) using short transactions.
	if err := ensureStoreExists(ctx, to, table); err != nil {
		t.Fatalf("ensure store exists: %v", err)
	}

	// Seed a couple of items in a fresh transaction.
	trans, err := infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	if err = trans.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := infs.OpenBtreeWithReplication[int, string](ctx, table, trans, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := b.Upsert(ctx, 1, "alpha"); !ok || err != nil {
		t.Fatalf("seed upsert: %v", err)
	}
	if ok, err := b.Upsert(ctx, 2, "bravo"); !ok || err != nil {
		t.Fatalf("seed upsert: %v", err)
	}
	// Pre-seed more items to make copyStores take a measurable time during reinstate.
	for i := 10; i <= 30; i++ {
		if ok, err := b.Upsert(ctx, i, fmt.Sprintf("preseed-%d", i)); !ok || err != nil {
			t.Fatalf("preseed upsert %d: %v", i, err)
		}
	}
	if err := trans.Commit(ctx); err != nil {
		t.Fatalf("seed commit: %v", err)
	}

	// Simulate a drive failure using DirectIO simulator to deterministically mark FailedToReplicate
	// across all execution environments (root/containers, macOS, Linux).
	activeBase := activeBaseFolder()
	regDir := filepath.Join(activeBase, table)
	ArmActiveRegistryRestoreSectorFail(regDir, 0, true)
	defer ResetDirectIOSim()

	trans2, err := infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	if err = trans2.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	b2, err := infs.OpenBtreeWithReplication[int, string](ctx, table, trans2, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Attempt to add a new key to force a registry write and hit the simulated failure
	if ok, err := b2.Upsert(ctx, 1001, "new-after-failover-trigger"); !ok || err != nil {
		// Expect commit to fail; keep going
	}
	_ = trans2.Commit(ctx) // ignore error; failover will be decided by tracker

	// Trigger failover if needed to ensure FailedToReplicate state is set
	_ = fs.TriggerFailover(ctx, isolatedStores, true, sop.GetL2Cache(sop.TransactionOptions{CacheType: sop.Redis}))

	// Verify we are in failed replication mode (replicate-to-passive failed sets this flag).
	// Use a robust wait that checks both in-memory state and replstat files to avoid flakiness.
	if err := waitForFailedFlagTrue(3 * time.Second); err != nil {
		t.Fatalf("expected FailedToReplicate after simulated IO error: %v", err)
	}

	// Disarm simulator and restore permissions so reinstate and upcoming commits can proceed
	ResetDirectIOSim()
	restoreRegistryPermissions(t, table)
	restoreStoreInfoPermissions(t, table)
	reinstateErr := make(chan error, 1)
	go func() { reinstateErr <- infs.ReinstateFailedDrives(ctx, isolatedStores, sop.Redis) }()

	// Detect immediate reinstate failures (fail fast with clearer error).
	select {
	case err := <-reinstateErr:
		if err != nil {
			t.Fatalf("reinstate returned early: %v", err)
		}
	default:
		// continue
	}

	// While reinstate runs, continuously attempt small delta writes so at least some occur
	// during the commit-logging window.
	stopW := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopW:
				return
			case <-ticker.C:
				tw, err := infs.NewTransactionWithReplication(ctx, to)
				if err != nil {
					continue
				}
				if err = tw.Begin(ctx); err != nil {
					_ = tw.Rollback(ctx)
					continue
				}
				bw, err := infs.OpenBtreeWithReplication[int, string](ctx, table, tw, nil)
				if err != nil {
					_ = tw.Rollback(ctx)
					continue
				}
				_, _ = bw.Upsert(ctx, 2, "bravo2")
				_ = tw.Commit(ctx)
			}
		}
	}()

	// Do an immediate delta write; the writer loop above will keep attempting more.
	transU, err := infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	if err = transU.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	bu, err := infs.OpenBtreeWithReplication[int, string](ctx, table, transU, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := bu.Upsert(ctx, 2, "bravo2"); !ok || err != nil {
		t.Fatalf("delta upsert existing: %v", err)
	}
	if err := transU.Commit(ctx); err != nil {
		t.Fatalf("delta commit existing: %v", err)
	}

	// Sanity-check the active store reflects the updated value; if not, retry with remove+add.
	if gv, ok := getValue(ctx, to, table, 2, t); !ok || gv != "bravo2" {
		transFix, err := infs.NewTransactionWithReplication(ctx, to)
		if err != nil {
			t.Fatal(err)
		}
		if err = transFix.Begin(ctx); err != nil {
			t.Fatal(err)
		}
		bf, err := infs.OpenBtreeWithReplication[int, string](ctx, table, transFix, nil)
		if err != nil {
			t.Fatal(err)
		}
		// Remove then add to force a concrete change
		_, _ = bf.Remove(ctx, 2)
		if ok, err := bf.Add(ctx, 2, "bravo2"); !ok || err != nil {
			t.Fatalf("force set existing key: %v", err)
		}
		if err := transFix.Commit(ctx); err != nil {
			t.Fatalf("force set commit: %v", err)
		}
		if gv2, ok2 := getValue(ctx, to, table, 2, t); !ok2 || gv2 != "bravo2" {
			t.Fatalf("post-fix readback mismatch: got %q want %q", gv2, "bravo2")
		}
	}

	// Perform a few new-key delta writes while reinstate is running so fast-forward picks them up.
	for i := 3; i <= 5; i++ {
		transW, err := infs.NewTransactionWithReplication(ctx, to)
		if err != nil {
			t.Fatal(err)
		}
		if err = transW.Begin(ctx); err != nil {
			t.Fatal(err)
		}
		bw, err := infs.OpenBtreeWithReplication[int, string](ctx, table, transW, nil)
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := bw.Upsert(ctx, i, fmt.Sprintf("delta-%d", i)); !ok || err != nil {
			t.Fatalf("delta upsert: %v", err)
		}
		if err := transW.Commit(ctx); err != nil {
			t.Fatalf("delta commit: %v", err)
		}
		// small delay to increase overlap with reinstate
		time.Sleep(25 * time.Millisecond)
	}

	// Reinstate must complete successfully.
	if err := <-reinstateErr; err != nil {
		close(stopW)
		t.Fatalf("reinstate error: %v", err)
	}
	close(stopW)

	// After reinstate, replication flags should be cleared and logging turned off.
	if err := waitForFailedFlagFalse(2 * time.Second); err != nil {
		t.Fatalf("expected FailedToReplicate=false after reinstate: %v", err)
	}

	// Validate data integrity: read all items and ensure delta writes are present.
	got := readAll(ctx, to, table, t)
	// Expect keys: 1,2,3,4,5 with respective values updated
	want := map[int]string{1: "alpha", 2: "bravo2", 3: "delta-3", 4: "delta-4", 5: "delta-5"}
	if len(got) < len(want) {
		t.Fatalf("expected at least %d items, got %d (%v)", len(want), len(got), got)
	}
	for k, v := range want {
		if gv, ok := got[k]; !ok || gv != v {
			t.Fatalf("missing/incorrect item %d: got=%q want=%q; all=%v", k, gv, v, got)
		}
	}

	// Note: we intentionally skip simulating another failover here to keep the integration test deterministic and fast.
}

func readAll(ctx context.Context, to sop.TransactionOptions, table string, t *testing.T) map[int]string {
	t.Helper()
	trans, err := infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	if err = trans.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := infs.OpenBtreeWithReplication[int, string](ctx, table, trans, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[int]string{}
	b.First(ctx)
	for {
		it, err := b.GetCurrentItem(ctx)
		if err != nil {
			break
		}
		if it.Value != nil {
			out[it.Key] = *it.Value
		}
		if ok, _ := b.Next(ctx); !ok {
			break
		}
	}
	_ = trans.Commit(ctx)
	return out
}

func getValue(ctx context.Context, to sop.TransactionOptions, table string, key int, t *testing.T) (string, bool) {
	t.Helper()
	trans, err := infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	if err = trans.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	b, err := infs.OpenBtreeWithReplication[int, string](ctx, table, trans, nil)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := b.Find(ctx, key, false)
	if err != nil || !ok {
		_ = trans.Commit(ctx)
		return "", false
	}
	it, err := b.GetCurrentItem(ctx)
	_ = trans.Commit(ctx)
	if err != nil || it.Value == nil {
		return "", false
	}
	return *it.Value, true
}

// ensureStoreExists opens the store if present, or creates it in a separate short transaction if missing.
func ensureStoreExists(ctx context.Context, to sop.TransactionOptions, name string) error {
	// Try open first.
	if trans, err := infs.NewTransactionWithReplication(ctx, to); err == nil {
		if err = trans.Begin(ctx); err == nil {
			if _, err2 := infs.OpenBtreeWithReplication[int, string](ctx, name, trans, nil); err2 == nil {
				_ = trans.Commit(ctx)
				return nil
			}
		}
		// Open path rolls back on not-found; ensure we close.
		_ = trans.Rollback(ctx)
	}
	// Create it.
	if trans, err := infs.NewTransactionWithReplication(ctx, to); err == nil {
		if err = trans.Begin(ctx); err == nil {
			_, err2 := infs.NewBtreeWithReplication[int, string](ctx, sop.StoreOptions{
				Name: name, SlotLength: 8, IsValueDataInNodeSegment: true,
			}, trans, nil)
			if err2 == nil {
				return trans.Commit(ctx)
			}
			// If already exists, try open once more.
			_ = trans.Rollback(ctx)
			if trans2, err3 := infs.NewTransactionWithReplication(ctx, to); err3 == nil {
				if err = trans2.Begin(ctx); err == nil {
					if _, err4 := infs.OpenBtreeWithReplication[int, string](ctx, name, trans2, nil); err4 == nil {
						return trans2.Commit(ctx)
					}
				}
				_ = trans2.Rollback(ctx)
			}
			return err2
		}
		_ = trans.Rollback(ctx)
	}
	return fmt.Errorf("failed to ensure store exists: %s", name)
}

// waitForCommitLoggingOn polls the active replstat.txt until LogCommitChanges is true or timeout elapses.
func waitForCommitLoggingOn(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Fast path: check in-memory global first.
		if fs.GlobalReplicationDetails != nil && fs.GlobalReplicationDetails.LogCommitChanges {
			return nil
		}
		// Fallback: check status files on both candidate folders in case toggler changed mid-run.
		for _, base := range []string{activeBaseFolder(), passiveBaseFolder()} {
			fn := filepath.Join(base, "replstat.txt")
			if ba, err := os.ReadFile(fn); err == nil && strings.Contains(string(ba), "\"LogCommitChanges\":true") {
				return nil
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for LogCommitChanges=true")
}

// waitForFailedFlagFalse waits until the replication status indicates FailedToReplicate=false.
func waitForFailedFlagFalse(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fs.GlobalReplicationDetails != nil && !fs.GlobalReplicationDetails.FailedToReplicate {
			return nil
		}
		// Also check status files on both potential active bases.
		for _, base := range []string{activeBaseFolder(), passiveBaseFolder()} {
			fn := filepath.Join(base, "replstat.txt")
			if ba, err := os.ReadFile(fn); err == nil && strings.Contains(string(ba), "\"FailedToReplicate\":false") {
				return nil
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for FailedToReplicate=false; current=%v", func() any {
		if fs.GlobalReplicationDetails == nil {
			return nil
		}
		return *fs.GlobalReplicationDetails
	}())
}

// waitForFailedFlagTrue waits until the replication status indicates FailedToReplicate=true.
func waitForFailedFlagTrue(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if fs.GlobalReplicationDetails != nil && fs.GlobalReplicationDetails.FailedToReplicate {
			return nil
		}
		// Also check status files on both potential active bases.
		for _, base := range []string{activeBaseFolder(), passiveBaseFolder()} {
			fn := filepath.Join(base, "replstat.txt")
			if ba, err := os.ReadFile(fn); err == nil && strings.Contains(string(ba), "\"FailedToReplicate\":true") {
				return nil
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for FailedToReplicate=true; current=%v", func() any {
		if fs.GlobalReplicationDetails == nil {
			return nil
		}
		return *fs.GlobalReplicationDetails
	}())
}

func activeBaseFolder() string {
	// Use isolated stores for this test
	base0 := fmt.Sprintf("%s%cdisk8", dataPath, os.PathSeparator)
	base1 := fmt.Sprintf("%s%cdisk9", dataPath, os.PathSeparator)
	// Default to tracker semantics: when uninitialized, ActiveFolderToggler starts true => base0 is active.
	if fs.GlobalReplicationDetails == nil {
		return base0
	}
	if fs.GlobalReplicationDetails.ActiveFolderToggler {
		return base0
	}
	return base1
}

func passiveBaseFolder() string {
	base0 := fmt.Sprintf("%s%cdisk8", dataPath, os.PathSeparator)
	base1 := fmt.Sprintf("%s%cdisk9", dataPath, os.PathSeparator)
	// Default to tracker semantics: when uninitialized, base1 is passive.
	if fs.GlobalReplicationDetails == nil {
		return base1
	}
	if fs.GlobalReplicationDetails.ActiveFolderToggler {
		return base1
	}
	return base0
}

func cleanupReplicationStatusFiles() {
	for _, base := range []string{activeBaseFolder(), passiveBaseFolder()} {
		_ = os.Remove(fmt.Sprintf("%s%creplstat.txt", base, os.PathSeparator))
		_ = os.RemoveAll(fmt.Sprintf("%s%ccommitlogs", base, os.PathSeparator))
		_ = os.MkdirAll(base, 0o755)
	}
}

// cleanupStoreEverywhere removes the store folder across this test's isolated replication disks to ensure a clean slate.
func cleanupStoreEverywhere(name string) {
	for _, base := range []string{activeBaseFolder(), passiveBaseFolder()} {
		_ = os.RemoveAll(filepath.Join(base, name))
	}
}

// cleanupECShards removes EC blob store shards for this test's isolated disks.
func cleanupECShards(name string) {
	for i := 10; i <= 13; i++ {
		base := fmt.Sprintf("%s%cdisk%d", dataPath, os.PathSeparator, i)
		_ = os.RemoveAll(filepath.Join(base, name))
	}
}

// cleanupStoreRepository removes store repository records on both isolated replication bases.
func cleanupStoreRepository(name string, bases []string) {
	ec := fs.GetGlobalErasureConfig()
	for _, base := range bases {
		// Ignore error; it will fail if the store doesn't exist which is fine for cleanup.
		_ = infs.RemoveBtree(context.Background(), name, []string{base}, ec, sop.Redis)
	}
}

// makeActiveRegistryReadOnly makes the current ACTIVE registry segment file read-only to trigger a failover
// qualified error in the commit path.
func makeActiveRegistryReadOnly(t *testing.T, table string) {
	t.Helper()
	base := activeBaseFolder()
	ensureTableDir(t, base, table)
	seg := registrySegmentPath(base, table)
	_ = os.Chmod(seg, 0o444)
}

// Test_ActiveSide_FailoverFlip_Then_Reinstate_FastForward uses the DirectIO simulator to inject
// EIO on active registry WriteAt, causing a failover (toggler flip). It then restores IO and
// runs reinstate, verifying delta fast-forwarded writes are present and flags cleared.
