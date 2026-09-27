//go:build integration
// +build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/adapters/redis"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/database"
)

type loadScenario struct {
	Name        string
	Concurrency int
	OpsPerWork  int
}

type loadReport struct {
	Mode          string         `json:"mode"`
	Concurrency   int            `json:"concurrency"`
	OpsPerWorker  int            `json:"ops_per_worker"`
	CompletedOps  int64          `json:"completed_ops"`
	SuccessfulTX  int64          `json:"successful_tx"`
	FailedTX      int64          `json:"failed_tx"`
	ElapsedMs     int64          `json:"elapsed_ms"`
	OpsPerSecond  float64        `json:"ops_per_second"`
	AvgTxMs       float64        `json:"avg_tx_ms"`
	P95TxMs       float64        `json:"p95_tx_ms"`
	HeapAllocMB   float64        `json:"heap_alloc_mb"`
	HeapSysMB     float64        `json:"heap_sys_mb"`
	HeapInuseMB   float64        `json:"heap_inuse_mb"`
	GCycles       uint32         `json:"gc_cycles"`
	MemorySamples []memorySample `json:"memory_samples"`
	TxLatenciesMs []float64      `json:"tx_latencies_ms"`
	ProfileDir    string         `json:"profile_dir,omitempty"`
}

const (
	loadTreeCount    = 10
	loadItemsPerTree = 400
	loadReadsPerTree = 3
)

type memorySample struct {
	ElapsedMs int64   `json:"elapsed_ms"`
	HeapAlloc float64 `json:"heap_alloc_mb"`
	HeapSys   float64 `json:"heap_sys_mb"`
	HeapInuse float64 `json:"heap_inuse_mb"`
	NumGC     uint32  `json:"num_gc"`
}

func TestCacheLoadIntegrationReport(t *testing.T) {
	ctx := context.Background()
	scenarios := []loadScenario{
		{Name: "50_concurrent", Concurrency: 50, OpsPerWork: 120},
		{Name: "100_concurrent", Concurrency: 100, OpsPerWork: 120},
		{Name: "150_concurrent", Concurrency: 150, OpsPerWork: 120},
		{Name: "200_concurrent", Concurrency: 200, OpsPerWork: 120},
	}

	modes := []struct {
		name      string
		mode      sop.DatabaseType
		cacheType sop.L2CacheType
		redisAddr string
		available bool
	}{
		{name: "standalone_inmemory", mode: sop.Standalone, cacheType: sop.InMemory},
		{name: "clustered_redis", mode: sop.Clustered, cacheType: sop.Redis, redisAddr: "127.0.0.1:6379"},
	}

	for _, mode := range modes {
		if mode.cacheType == sop.Redis {
			client := redis.NewClient(sop.TransactionOptions{CacheType: sop.Redis, RedisConfig: &sop.RedisCacheConfig{Address: mode.redisAddr}})
			if err := client.Ping(ctx); err != nil {
				t.Logf("skipping Redis-backed integration scenario %s: %v", mode.name, err)
				continue
			}
			mode.available = true
		}
		for _, scenario := range scenarios {
			t.Run(fmt.Sprintf("%s/%s", mode.name, scenario.Name), func(t *testing.T) {
				report := runScenario(t, ctx, mode.mode, mode.cacheType, mode.redisAddr, scenario)
				if report == nil {
					return
				}
				reportJSON, _ := json.MarshalIndent(report, "", "  ")
				t.Logf("LOAD_REPORT_JSON\n%s", string(reportJSON))
			})
		}
	}
}

func runScenario(t *testing.T, ctx context.Context, mode sop.DatabaseType, cacheType sop.L2CacheType, redisAddr string, scenario loadScenario) *loadReport {
	t.Helper()
	storeName := fmt.Sprintf("loadstore_%d", scenario.Concurrency)
	rootDir := filepath.Join(t.TempDir(), "stores")
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		t.Fatalf("mkdir stores: %v", err)
	}
	cfg := sop.DatabaseOptions{
		Type:          mode,
		CacheType:     cacheType,
		StoresFolders: []string{rootDir},
	}
	if cacheType == sop.Redis {
		cfg.RedisConfig = &sop.RedisCacheConfig{Address: redisAddr}
	}
	if _, err := database.Setup(ctx, cfg); err != nil {
		if err != nil {
			// Setup may return an already setup error for the same temp dir on reruns; ignore and continue.
			if err.Error() != "database already setup" && err.Error() != "database /" {
				t.Fatalf("database setup failed: %v", err)
			}
		}
	}

	l2Cache := getL2CacheForConfig(cfg)
	if l2Cache == nil {
		t.Fatalf("could not create L2 cache for mode %v", mode)
	}
	if err := l2Cache.Clear(ctx); err != nil {
		t.Fatalf("clear cache before scenario: %v", err)
	}

	profileDir := resolveProfileOutputDir(t, mode, scenario.Name)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("mkdir pprof dir: %v", err)
	}
	t.Logf("pprof output dir: %s", profileDir)

	seedCtx, cancelSeed := context.WithTimeout(ctx, 2*time.Minute)
	defer cancelSeed()
	if err := seedStore(seedCtx, cfg, storeName, 4000); err != nil {
		t.Fatalf("seed store: %v", err)
	}

	cpuProfilePath := filepath.Join(profileDir, "cpu.pprof")
	cpuFile, err := os.Create(cpuProfilePath)
	if err != nil {
		t.Fatalf("create cpu profile: %v", err)
	}
	if err := pprof.StartCPUProfile(cpuFile); err != nil {
		cpuFile.Close()
		t.Fatalf("start cpu profile: %v", err)
	}

	var completed atomic.Int64
	var successful atomic.Int64
	var failed atomic.Int64
	var totalLatencyMs atomic.Int64
	var memSamples []memorySample
	var memMu sync.Mutex
	var latencyMu sync.Mutex
	latencies := make([]float64, 0)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(scenario.Concurrency)

	runtime.GC()
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)
	_ = memStats.HeapAlloc

	stopSampling := make(chan struct{})
	startTime := time.Now
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stopSampling:
				return
			case <-ticker.C:
				var ms runtime.MemStats
				runtime.ReadMemStats(&ms)
				memMu.Lock()
				memSamples = append(memSamples, memorySample{
					ElapsedMs: time.Since(startTime()).Milliseconds(),
					HeapAlloc: float64(ms.HeapAlloc) / (1024 * 1024),
					HeapSys:   float64(ms.HeapSys) / (1024 * 1024),
					HeapInuse: float64(ms.HeapInuse) / (1024 * 1024),
					NumGC:     ms.NumGC,
				})
				memMu.Unlock()
			}
		}
	}()

	begin := startTime()
	for workerID := 0; workerID < scenario.Concurrency; workerID++ {
		go func(workerID int) {
			defer wg.Done()
			<-start
			workerCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			for op := 0; op < scenario.OpsPerWork; op++ {
				operationStart := time.Now()
				if err := runWorkerOperation(workerCtx, cfg, storeName, workerID, op); err != nil {
					failed.Add(1)
					continue
				}
				successful.Add(1)
				completed.Add(1)
				latencyMs := time.Since(operationStart).Milliseconds()
				totalLatencyMs.Add(latencyMs)
				latencyMu.Lock()
				latencies = append(latencies, float64(latencyMs))
				latencyMu.Unlock()
				if op%8 == 0 {
					runtime.Gosched()
				}
			}
		}(workerID)
	}
	close(start)
	wg.Wait()
	close(stopSampling)
	pprof.StopCPUProfile()
	_ = cpuFile.Close()
	elapsed := time.Since(begin)
	var finalMem runtime.MemStats
	runtime.ReadMemStats(&finalMem)
	runtime.GC()
	writePprofProfile(t, profileDir, "heap")
	writePprofProfile(t, profileDir, "allocs")
	writePprofProfile(t, profileDir, "goroutine")

	if completed.Load() == 0 {
		t.Fatalf("no completed operations")
	}

	elapsedMs := elapsed.Milliseconds()
	completedOps := completed.Load()
	report := &loadReport{
		Mode:          databaseTypeName(mode),
		Concurrency:   scenario.Concurrency,
		OpsPerWorker:  scenario.OpsPerWork,
		CompletedOps:  completedOps,
		SuccessfulTX:  successful.Load(),
		FailedTX:      failed.Load(),
		ElapsedMs:     elapsedMs,
		OpsPerSecond:  float64(completedOps) / (float64(elapsedMs) / 1000),
		AvgTxMs:       float64(totalLatencyMs.Load()) / float64(completedOps),
		P95TxMs:       estimateP95(latencies),
		HeapAllocMB:   float64(finalMem.HeapAlloc) / (1024 * 1024),
		HeapSysMB:     float64(finalMem.HeapSys) / (1024 * 1024),
		HeapInuseMB:   float64(finalMem.HeapInuse) / (1024 * 1024),
		GCycles:       finalMem.NumGC,
		MemorySamples: memSamples,
		TxLatenciesMs: latencies,
		ProfileDir:    profileDir,
	}
	return report
}

func getL2CacheForConfig(cfg sop.DatabaseOptions) sop.L2Cache {
	if cfg.CacheType == sop.Redis {
		return redis.NewClient(sop.TransactionOptions{CacheType: sop.Redis, RedisConfig: cfg.RedisConfig})
	}
	return cache.NewStandaloneL2InMemoryCache()
}

func resolveProfileOutputDir(t *testing.T, mode sop.DatabaseType, scenarioName string) string {
	t.Helper()
	if dir := os.Getenv("SOP_PPROF_DIR"); dir != "" {
		return filepath.Join(dir, fmt.Sprintf("%s_%s", databaseTypeName(mode), scenarioName))
	}
	return filepath.Join(t.TempDir(), "pprof", fmt.Sprintf("%s_%s", databaseTypeName(mode), scenarioName))
}

func writePprofProfile(t *testing.T, profileDir, name string) {
	t.Helper()
	path := filepath.Join(profileDir, name+".pprof")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s profile: %v", name, err)
	}
	defer f.Close()
	if err := pprof.Lookup(name).WriteTo(f, 0); err != nil {
		t.Fatalf("write %s profile: %v", name, err)
	}
}

func seedStore(ctx context.Context, cfg sop.DatabaseOptions, storeName string, itemCount int) error {
	config, err := database.ValidateOptions(cfg)
	if err != nil {
		return err
	}
	tx, err := database.BeginTransaction(ctx, config, sop.ForWriting)
	if err != nil {
		return err
	}
	defer tx.Close()
	for treeIndex := 0; treeIndex < loadTreeCount; treeIndex++ {
		store, err := database.NewBtree[string, string](ctx, config, loadTreeName(storeName, treeIndex), tx, compareStrings)
		if err != nil {
			return err
		}
		for i := 0; i < itemCount; i++ {
			key := fmt.Sprintf("key-%02d-%06d", treeIndex, i)
			if _, err := store.Add(ctx, key, fmt.Sprintf("value-%02d-%06d", treeIndex, i)); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func runWorkerOperation(ctx context.Context, cfg sop.DatabaseOptions, storeName string, workerID int, op int) error {
	config, err := database.ValidateOptions(cfg)
	if err != nil {
		return err
	}
	tx, err := database.BeginTransaction(ctx, config, sop.ForWriting)
	if err != nil {
		return err
	}
	defer tx.Close()

	activeTreeIndex := (workerID + op) % loadTreeCount
	activeTreeName := loadTreeName(storeName, activeTreeIndex)
	activeStore, err := database.NewBtree[string, string](ctx, config, activeTreeName, tx, compareStrings)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	activeKey := fmt.Sprintf("key-%02d-%06d", activeTreeIndex, (op+workerID)%loadItemsPerTree)
	if _, err := activeStore.Upsert(ctx, activeKey, fmt.Sprintf("value-%d-%d", workerID, op)); err != nil {
		tx.Rollback(ctx)
		return err
	}
	if _, err := activeStore.Find(ctx, activeKey, false); err != nil {
		tx.Rollback(ctx)
		return err
	}
	if _, err := activeStore.GetCurrentValue(ctx); err != nil {
		tx.Rollback(ctx)
		return err
	}

	for _, target := range loadReadTargets(storeName, workerID, op) {
		store, err := database.NewBtree[string, string](ctx, config, target.treeName, tx, compareStrings)
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		for _, key := range target.keys {
			if _, err := store.Find(ctx, key, false); err != nil {
				tx.Rollback(ctx)
				return err
			}
			if _, err := store.GetCurrentValue(ctx); err != nil {
				tx.Rollback(ctx)
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func loadTreeName(baseName string, treeIndex int) string {
	return fmt.Sprintf("%s_tree_%02d", baseName, treeIndex)
}

func loadReadTargets(baseName string, workerID int, op int) []struct {
	treeName string
	keys     []string
} {
	targets := make([]struct {
		treeName string
		keys     []string
	}, loadReadsPerTree)
	for i := 0; i < loadReadsPerTree; i++ {
		treeIndex := (workerID + op + i) % loadTreeCount
		treeName := loadTreeName(baseName, treeIndex)
		offsets := []int{(op + i) % loadItemsPerTree, (op + i + 1) % loadItemsPerTree, (op + i + 2) % loadItemsPerTree}
		keys := make([]string, len(offsets))
		for j, offset := range offsets {
			keys[j] = fmt.Sprintf("key-%02d-%06d", treeIndex, offset)
		}
		targets[i] = struct {
			treeName string
			keys     []string
		}{treeName: treeName, keys: keys}
	}
	return targets
}

func TestLoadTreeNamesAreDistinct(t *testing.T) {
	names := make([]string, 0, loadTreeCount)
	for i := 0; i < loadTreeCount; i++ {
		names = append(names, loadTreeName("loadstore", i))
	}
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			t.Fatalf("expected distinct tree name, got duplicate %q", name)
		}
		seen[name] = struct{}{}
	}
}

func databaseTypeName(mode sop.DatabaseType) string {
	switch mode {
	case sop.Standalone:
		return "standalone"
	case sop.Clustered:
		return "clustered"
	default:
		return fmt.Sprintf("database-type-%d", mode)
	}
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func estimateP95(latencies []float64) float64 {
	if len(latencies) == 0 {
		return 0
	}
	sorted := append([]float64(nil), latencies...)
	sort.Float64s(sorted)
	idx := int(float64(len(sorted)-1) * 0.95)
	if idx < 0 {
		idx = 0
	}
	return sorted[idx]
}
