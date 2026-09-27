package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// Ensures CopyToPassiveFolders returns an error when registry source directory cannot be read.
func Test_CopyToPassiveFolders_MissingRegistrySourceDir(t *testing.T) {
	ctx := context.Background()
	active := filepath.Join(t.TempDir(), "a")
	passive := filepath.Join(t.TempDir(), "b")
	cache := mocks.NewMockClient()

	// Isolate global to keep toggler deterministic
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	defer func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	}()

	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Seed a store so copier will attempt to copy its registry table
	si := sop.StoreInfo{Name: "s1", RegistryTable: sop.FormatRegistryTable("s1")}
	if err := sr.Add(ctx, si); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Do NOT create the registry source directory on active, so copyFilesByExtension's ReadDir fails.
	if err := sr.CopyToPassiveFolders(ctx); err == nil {
		t.Fatalf("expected error when source registry directory is missing")
	}
}

// Ensures TransactionLog.GetOne returns nil when lock isn't acquired (another process holds it).
func Test_TransactionLog_GetOne_LockMissReturnsNil(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()
	rt, _ := NewReplicationTracker(ctx, []string{filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")}, true, cache)
	tl := NewTransactionLog(cache, rt)

	// Pre-acquire the hour lock using a different LockKey (different LockID) to simulate another worker owning it.
	other := cache.CreateLockKeys([]string{"HBP"})
	ok, _, err := cache.Lock(ctx, time.Hour, other)
	if err != nil || !ok {
		t.Fatalf("failed to acquire pre-lock: %v ok=%v", err, ok)
	}
	// GetOne should return nils without error when lock isn't available.
	tid, hour, recs, err := tl.GetOne(ctx)
	if err != nil || !tid.IsNil() || hour != "" || recs != nil {
		t.Fatalf("expected nil result on lock miss; got tid=%v hour=%q recs=%v err=%v", tid, hour, recs, err)
	}
}

// Validates CopyToPassiveFolders successfully copies store list, store info, and registry segment files.
func Test_CopyToPassiveFolders_Success(t *testing.T) {
	ctx := context.Background()
	active := filepath.Join(t.TempDir(), "a")
	passive := filepath.Join(t.TempDir(), "b")
	cache := mocks.NewMockClient()

	// Isolate global toggler
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	// Initialize tracker and a store repo with a known registry table
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, MinimumModValue)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Seed one store and its registry segment file in active
	store := sop.NewStoreInfo(sop.StoreOptions{Name: "s1", SlotLength: 8})
	if err := sr.Add(ctx, *store); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Create registry segment file in active registry table folder
	regDir := filepath.Join(active, store.RegistryTable)
	os.MkdirAll(regDir, 0o755)
	segPath := filepath.Join(regDir, store.RegistryTable+"-1"+registryFileExtension)
	os.WriteFile(segPath, []byte("data"), 0o644)

	// Run copy
	if err := sr.CopyToPassiveFolders(ctx); err != nil {
		t.Fatalf("CopyToPassiveFolders: %v", err)
	}

	// Validate passive now has store list, store info, and the registry segment file
	if _, err := os.Stat(filepath.Join(passive, storeListFilename)); err != nil {
		t.Fatalf("passive missing store list: %v", err)
	}
	if _, err := os.Stat(filepath.Join(passive, store.Name, StoreInfoFilename)); err != nil {
		t.Fatalf("passive missing store info: %v", err)
	}
	if _, err := os.Stat(filepath.Join(passive, store.RegistryTable, store.RegistryTable+"-1"+registryFileExtension)); err != nil {
		t.Fatalf("passive missing registry segment: %v", err)
	}
}

// Exercises copyFile error branches and CopyToPassiveFolders across both toggler variants.
func Test_Copier_MoreCoverage(t *testing.T) {
	ctx := context.Background()
	pushPop := func() func() {
		prev := GlobalReplicationDetails
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = nil
		globalReplicationDetailsLocker.Unlock()
		return func() {
			globalReplicationDetailsLocker.Lock()
			GlobalReplicationDetails = prev
			globalReplicationDetailsLocker.Unlock()
		}
	}

	t.Run("copyFile_errors_open_and_create", func(t *testing.T) {
		// Non-existent source -> open error
		dst := filepath.Join(t.TempDir(), "out.reg")
		if err := copyFile(filepath.Join(t.TempDir(), "missing.reg"), dst); err == nil {
			t.Fatalf("expected open source error")
		}

		// Target path is a directory -> create error
		srcDir := t.TempDir()
		src := filepath.Join(srcDir, "src.reg")
		if err := os.WriteFile(src, []byte("data"), 0o644); err != nil {
			t.Fatalf("seed src: %v", err)
		}
		targetDir := t.TempDir()
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := copyFile(src, targetDir); err == nil {
			t.Fatalf("expected create target error")
		}
		// Success path
		dstOK := filepath.Join(t.TempDir(), "ok.reg")
		if err := copyFile(src, dstOK); err != nil {
			t.Fatalf("copyFile success: %v", err)
		}
		// Content copy error: use a directory as source to trigger read error on io.Copy
		srcDirAsFile := t.TempDir()
		dstErr := filepath.Join(t.TempDir(), "z.reg")
		if err := copyFile(srcDirAsFile, dstErr); err == nil {
			t.Fatalf("expected copy content error from directory source")
		}
	})

	t.Run("CopyToPassiveFolders_toggler_true_then_false_and_copy_segments", func(t *testing.T) {
		l2 := mocks.NewMockClient()
		defer pushPop()()
		active := t.TempDir()
		passive := t.TempDir()
		// tracker with replication enabled
		rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
		if err != nil {
			t.Fatalf("tracker: %v", err)
		}
		// Seed a store via repository API so GetAll returns it
		sr, err := NewStoreRepository(ctx, rt, nil, l2, MinimumModValue)
		if err != nil {
			t.Fatalf("repo: %v", err)
		}
		store := sop.StoreInfo{Name: "s1", RegistryTable: "c1_r"}
		if err := sr.Add(ctx, store); err != nil {
			t.Fatalf("seed store: %v", err)
		}
		// Create a registry segment on the original active side and a subdir to ensure skip-dir branch
		segDirActive := filepath.Join(active, store.RegistryTable)
		if err := os.MkdirAll(segDirActive, 0o755); err != nil {
			t.Fatalf("mkdir seg: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(segDirActive, "subdir"), 0o755); err != nil {
			t.Fatalf("mkdir subdir: %v", err)
		}
		seg1 := filepath.Join(segDirActive, store.RegistryTable+"-1"+registryFileExtension)
		if err := os.WriteFile(seg1, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed seg1: %v", err)
		}
		// Copy with toggler true path
		if err := sr.CopyToPassiveFolders(ctx); err != nil {
			t.Fatalf("copy true: %v", err)
		}
		// Assert passive has store list, store info, and registry segment
		if _, err := os.Stat(filepath.Join(passive, storeListFilename)); err != nil {
			t.Fatalf("missing passive store list: %v", err)
		}
		if _, err := os.Stat(filepath.Join(passive, store.Name, StoreInfoFilename)); err != nil {
			t.Fatalf("missing passive store info: %v", err)
		}
		if _, err := os.Stat(filepath.Join(passive, store.RegistryTable, store.RegistryTable+"-1"+registryFileExtension)); err != nil {
			t.Fatalf("missing passive registry seg1: %v", err)
		}

		// Now flip toggler and seed a segment on the now-original-active [1] side
		rt.ActiveFolderToggler = false
		segDirActive2 := filepath.Join(passive, store.RegistryTable)
		if err := os.MkdirAll(segDirActive2, 0o755); err != nil {
			t.Fatalf("mkdir seg2: %v", err)
		}
		seg2 := filepath.Join(segDirActive2, store.RegistryTable+"-2"+registryFileExtension)
		if err := os.WriteFile(seg2, []byte("y"), 0o644); err != nil {
			t.Fatalf("seed seg2: %v", err)
		}
		if err := sr.CopyToPassiveFolders(ctx); err != nil {
			t.Fatalf("copy false: %v", err)
		}
		// Expect the second segment to be copied back to the other side
		if _, err := os.Stat(filepath.Join(active, store.RegistryTable, store.RegistryTable+"-2"+registryFileExtension)); err != nil {
			t.Fatalf("missing active registry seg2 copy: %v", err)
		}
	})

	t.Run("CopyToPassiveFolders_Error_CopySegments_TargetIsFile", func(t *testing.T) {
		l2 := mocks.NewMockClient()
		defer pushPop()()
		active := t.TempDir()
		passive := t.TempDir()
		rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, l2)
		if err != nil {
			t.Fatalf("rt: %v", err)
		}
		sr, err := NewStoreRepository(ctx, rt, nil, l2, MinimumModValue)
		if err != nil {
			t.Fatalf("repo: %v", err)
		}
		store := sop.StoreInfo{Name: "s2", RegistryTable: "c2_r"}
		if err := sr.Add(ctx, store); err != nil {
			t.Fatalf("seed: %v", err)
		}
		// Seed a registry segment under original active (storesBaseFolders[0])
		segDir := filepath.Join(active, store.RegistryTable)
		if err := os.MkdirAll(segDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(segDir, store.RegistryTable+"-1"+registryFileExtension), []byte("z"), 0o644); err != nil {
			t.Fatalf("seed seg: %v", err)
		}
		// Make destination path a file so mkdir in copyFilesByExtension fails
		targetPath := filepath.Join(passive, store.RegistryTable)
		if err := os.WriteFile(targetPath, []byte("file"), 0o644); err != nil {
			t.Fatalf("seed target file: %v", err)
		}
		if err := sr.CopyToPassiveFolders(ctx); err == nil {
			t.Fatalf("expected error due to targetDir being a file")
		}
	})
}

// Exercises CopyToPassiveFolders early return when GetAll fails (invalid store list JSON).
func Test_CopyToPassiveFolders_Error_GetAllInvalidJSON(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()

	// Isolate global state
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	active := t.TempDir()
	passive := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Write invalid JSON into active storelist.txt so GetAll() errors
	if err := os.WriteFile(filepath.Join(active, storeListFilename), []byte("not-json"), 0o644); err != nil {
		t.Fatalf("seed bad storelist: %v", err)
	}
	if err := sr.CopyToPassiveFolders(ctx); err == nil {
		t.Fatalf("expected error due to invalid store list JSON")
	}
}

// Forces writer.write(storeList) error by making passive/storelist.txt a directory.
func Test_CopyToPassiveFolders_Error_WriteStoreList_TargetIsDir(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()

	// Isolate global state
	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	active := t.TempDir()
	passive := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Seed a store so GetAll returns a list and copier proceeds to write store list.
	if err := sr.Add(ctx, sop.StoreInfo{Name: "s1", RegistryTable: sop.FormatRegistryTable("s1")}); err != nil {
		t.Fatalf("add: %v", err)
	}
	// Create passive/storelist.txt as a directory to force write error when toggler flips
	// Remove any existing file first (it may have been replicated by Add).
	_ = os.Remove(filepath.Join(passive, storeListFilename))
	if err := os.MkdirAll(filepath.Join(passive, storeListFilename), 0o755); err != nil {
		t.Fatalf("mkdir passive storelist dir: %v", err)
	}
	if err := sr.CopyToPassiveFolders(ctx); err == nil {
		t.Fatalf("expected error due to storeList write target being a directory")
	}
}

// Forces sr.Get(ctx, name) to return error by making active/<name>/storeinfo.txt a directory.
func Test_CopyToPassiveFolders_Error_GetStoreInfo_ReadError(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()

	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	active := t.TempDir()
	passive := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	s := sop.StoreInfo{Name: "s2", RegistryTable: sop.FormatRegistryTable("s2")}
	if err := sr.Add(ctx, s); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Make active/<name>/storeinfo.txt a directory so reading it fails
	// Remove the file if it exists from Add() and then create a directory in its place.
	_ = os.Remove(filepath.Join(active, s.Name, StoreInfoFilename))
	if err := os.MkdirAll(filepath.Join(active, s.Name, StoreInfoFilename), 0o755); err != nil {
		t.Fatalf("mkdir storeinfo as dir: %v", err)
	}
	if err := sr.CopyToPassiveFolders(ctx); err == nil {
		t.Fatalf("expected error from sr.Get due to storeinfo read failure")
	}
}

// Forces writer.write(store info) error by making passive/<name>/storeinfo.txt a directory.
func Test_CopyToPassiveFolders_Error_WriteStoreInfo_TargetIsDir(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()

	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	active := t.TempDir()
	passive := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	s := sop.StoreInfo{Name: "s3", RegistryTable: sop.FormatRegistryTable("s3")}
	if err := sr.Add(ctx, s); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Ensure passive/<name> exists and create passive/<name>/storeinfo.txt as directory to force write error.
	// Remove the file if it exists from prior replication and replace with a directory.
	_ = os.MkdirAll(filepath.Join(passive, s.Name), 0o755)
	_ = os.Remove(filepath.Join(passive, s.Name, StoreInfoFilename))
	if err := os.MkdirAll(filepath.Join(passive, s.Name, StoreInfoFilename), 0o755); err != nil {
		t.Fatalf("mkdir passive storeinfo dir: %v", err)
	}
	if err := sr.CopyToPassiveFolders(ctx); err == nil {
		t.Fatalf("expected error due to storeinfo write target being a directory")
	}
}

// Forces createStore("") to fail by replacing the passive base folder with a file.
func Test_CopyToPassiveFolders_Error_CreateStore_PassiveBaseIsFile(t *testing.T) {
	ctx := context.Background()
	cache := mocks.NewMockClient()

	globalReplicationDetailsLocker.Lock()
	prev := GlobalReplicationDetails
	GlobalReplicationDetails = nil
	globalReplicationDetailsLocker.Unlock()
	t.Cleanup(func() {
		globalReplicationDetailsLocker.Lock()
		GlobalReplicationDetails = prev
		globalReplicationDetailsLocker.Unlock()
	})

	active := t.TempDir()
	passive := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{active, passive}, true, cache)
	if err != nil {
		t.Fatalf("rt: %v", err)
	}
	sr, err := NewStoreRepository(ctx, rt, nil, cache, 0)
	if err != nil {
		t.Fatalf("sr: %v", err)
	}

	// Seed store to ensure GetAll returns non-nil
	if err := sr.Add(ctx, sop.StoreInfo{Name: "s4", RegistryTable: sop.FormatRegistryTable("s4")}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// Replace the passive base folder with a file to cause MkdirAll failure inside createStore("")
	if err := os.RemoveAll(passive); err != nil {
		t.Fatalf("remove passive dir: %v", err)
	}
	if err := os.WriteFile(passive, []byte("x"), 0o644); err != nil {
		t.Fatalf("create passive file: %v", err)
	}

	if err := sr.CopyToPassiveFolders(ctx); err == nil {
		t.Fatalf("expected error due to passive base being a file")
	}
}
