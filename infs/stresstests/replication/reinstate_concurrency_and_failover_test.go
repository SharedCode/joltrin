//go:build stress
// +build stress

package replication

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

// This stress test covers:
// - multiple tables
// - concurrent writers during ReinstateFailedDrives
// - a second failover after reinstate
// - uses dedicated disks to avoid interference with other suites
func Test_Reinstate_MultiTable_Concurrency_SecondFailover(t *testing.T) {
	ctx := context.Background()

	dataPath := getDataPathReinstate()
	stores := []string{
		fmt.Sprintf("%s%cdisk8", dataPath, os.PathSeparator),
		fmt.Sprintf("%s%cdisk9", dataPath, os.PathSeparator),
	}
	ec := map[string]sop.ErasureCodingConfig{
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
		StoresFolders:        stores,
		ErasureConfig:        ec,
		CacheType:            sop.Redis,
	}

	// Do not clear Redis here; we want to run across time.

	// Tables under load.
	tables := []string{"reinstate_stress_t1", "reinstate_stress_t2", "reinstate_stress_t3"}
	for _, tb := range tables {
		// Best-effort cleanup of store repository entries to avoid flakiness across runs.
		_ = infs.RemoveBtree(ctx, tb, []string{stores[0]}, ec, sop.Redis)
		_ = infs.RemoveBtree(ctx, tb, []string{stores[1]}, ec, sop.Redis)
		for _, base := range append(stores, fmt.Sprintf("%s%cdisk10", dataPath, os.PathSeparator), fmt.Sprintf("%s%cdisk11", dataPath, os.PathSeparator), fmt.Sprintf("%s%cdisk12", dataPath, os.PathSeparator), fmt.Sprintf("%s%cdisk13", dataPath, os.PathSeparator)) {
			_ = os.RemoveAll(filepath.Join(base, tb))
		}
		// Create and pre-seed
		tr, _ := infs.NewTransactionWithReplication(ctx, to)
		_ = tr.Begin(ctx)
		b, _ := infs.NewBtreeWithReplication[int, string](ctx, sop.StoreOptions{Name: tb, SlotLength: 8, IsValueDataInNodeSegment: true}, tr, nil)
		for i := 0; i < 100; i++ {
			_, _ = b.Upsert(ctx, i, fmt.Sprintf("seed-%d", i))
		}
		_ = tr.Commit(ctx)
	}

	// Trigger failover on next registry write by making active registry path read-only.
	// Ensure GlobalReplicationDetails is up to date with Redis before we decide which folder to make read-only.
	_, _ = fs.NewReplicationTracker(ctx, stores, true, sop.GetL2Cache(to))

	makeRegistryReadOnly(t, stores, tables[0])
	tr0, _ := infs.NewTransactionWithReplication(ctx, to)
	_ = tr0.Begin(ctx)
	b0, _ := infs.OpenBtreeWithReplication[int, string](ctx, tables[0], tr0, nil)
	_, _ = b0.Upsert(ctx, 101, "after-failover-trigger")
	_ = tr0.Commit(ctx)

	if fs.GlobalReplicationDetails == nil || !fs.GlobalReplicationDetails.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate true")
	}

	// Restore permissions and start reinstate while hammering writes concurrently on all tables.
	restoreRegistryDirs(t, stores, tables)
	reinstateErr := make(chan error, 1)
	go func() { reinstateErr <- infs.ReinstateFailedDrives(ctx, stores, sop.Redis) }()

	// Writer pool
	var wg sync.WaitGroup
	workers := runtime.GOMAXPROCS(0)
	stop := make(chan struct{})
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			rnd := time.NewTicker(5 * time.Millisecond)
			defer rnd.Stop()
			for {
				select {
				case <-stop:
					return
				case <-rnd.C:
					tr, _ := infs.NewTransactionWithReplication(ctx, to)
					if tr.Begin(ctx) != nil {
						continue
					}
					tb := tables[(id+int(time.Now().UnixNano()))%len(tables)]
					b, err := infs.OpenBtreeWithReplication[int, string](ctx, tb, tr, nil)
					if err != nil {
						_ = tr.Rollback(ctx)
						continue
					}
					k := int(time.Now().UnixNano() & 0xffff)
					_, _ = b.Upsert(ctx, k, "stress")
					_ = tr.Commit(ctx)
				}
			}
		}(w)
	}

	if err := <-reinstateErr; err != nil {
		close(stop)
		wg.Wait()
		t.Fatalf("reinstate: %v", err)
	}
	close(stop)
	wg.Wait()

	// Wait briefly for the flag to clear as status writes and L2 sync can be asynchronous across goroutines.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if fs.GlobalReplicationDetails != nil && !fs.GlobalReplicationDetails.FailedToReplicate {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if fs.GlobalReplicationDetails == nil || fs.GlobalReplicationDetails.FailedToReplicate {
		t.Fatalf("expected FailedToReplicate false after reinstate")
	}

	// Second failover and a quick write (toggle active registry path again).
	makeRegistryReadOnly(t, stores, tables[1])
	tr2, _ := infs.NewTransactionWithReplication(ctx, to)
	_ = tr2.Begin(ctx)
	b2, _ := infs.OpenBtreeWithReplication[int, string](ctx, tables[1], tr2, nil)
	_, _ = b2.Upsert(ctx, 202, "second-failover")
	_ = tr2.Commit(ctx)
	restoreRegistryDirs(t, stores, tables)
}

func getDataPathReinstate() string {
	if s := os.Getenv("datapath"); s != "" {
		return s
	}
	return filepath.Join(os.TempDir(), "sop_data")
}

// helpers
func makeRegistryReadOnly(t *testing.T, stores []string, table string) {
	t.Helper()
	// Resolve current active based on toggler.
	active := stores[0]
	if fs.GlobalReplicationDetails != nil && fs.GlobalReplicationDetails.ActiveFolderToggler {
		active = stores[1]
	}
	dir := filepath.Join(active, table)
	_ = os.MkdirAll(dir, 0o755)
	_ = os.Chmod(dir, 0o555)
	seg := filepath.Join(dir, fmt.Sprintf("%s-1.reg", table))
	_ = os.Chmod(seg, 0o444)
}

func restoreRegistryDirs(t *testing.T, stores []string, tables []string) {
	t.Helper()
	for _, s := range stores {
		for _, tb := range tables {
			dir := filepath.Join(s, tb)
			_ = os.MkdirAll(dir, 0o755)
			_ = os.Chmod(dir, 0o755)
			seg := filepath.Join(dir, fmt.Sprintf("%s-1.reg", tb))
			_ = os.Chmod(seg, 0o644)
		}
	}
}
