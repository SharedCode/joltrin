package fs

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	log "log/slog"
	"maps"
	"os"
	"slices"
	"strconv"
	"time"

	retry "github.com/sethvargo/go-retry"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/encoding"
	"github.com/sharedcode/joltrin/internal/logsafe"
)

// StoreRepository is a filesystem-backed implementation of sop.StoreRepository.
// It manages store metadata and coordinates replication via a replicationTracker.
type StoreRepository struct {
	cache                sop.L2Cache
	manageStore          sop.ManageStore
	replicationTracker   *replicationTracker
	registryHashModValue int
}

const (
	fieldCount     = "count"
	fieldTimestamp = "timestamp"

	lockStoreListKey             = "infs_sr"
	lockStoreListDuration        = time.Duration(10 * time.Minute)
	storeListFilename            = "storelist.txt"
	StoreInfoFilename            = "storeinfo.txt"
	RegistryHashModValueFilename = "reghashmod.txt"
	// updateStoresLockDuration is the TTL for cache-based locking during updates.
	updateStoresLockDuration = time.Duration(15 * time.Minute)
)

// NewStoreRepository creates a StoreRepository that persists store info to disk.
// When replication is enabled, it validates base-folder configuration and writes
// the global registry-hash-mod value once, replicating it to the passive drive.
func NewStoreRepository(ctx context.Context, rt *replicationTracker, manageStore sop.ManageStore, cache sop.L2Cache, registryHashModVal int) (*StoreRepository, error) {
	if rt.replicate && len(rt.storesBaseFolders) != 2 {
		return nil, fmt.Errorf("'storesBaseFolders' needs to be exactly two elements if 'replicate' parameter is true")
	}
	if manageStore == nil {
		fio := NewFileIO()
		manageStore = NewManageStoreFolder(fio)
	}

	if registryHashModVal > 0 {
		sw := newFileIOWithReplication(rt, manageStore, true)
		if !sw.exists(ctx, RegistryHashModValueFilename) {
			// Write to file the global registry hash mod value.
			sw.write(ctx, RegistryHashModValueFilename, []byte(fmt.Sprintf("%d", registryHashModVal)))
			// Replicate to passive drive so it has a copy of it.
			sw.replicate(ctx)
		}
	}

	return &StoreRepository{
		cache:                cache,
		manageStore:          manageStore,
		replicationTracker:   rt,
		registryHashModValue: registryHashModVal,
	}, nil
}

// GetRegistryHashModValue returns the configured registry hash modulus value, reading from disk if needed.
// Uses the replication-aware file IO wrapper to read the value written during initialization.
func (sr *StoreRepository) GetRegistryHashModValue(ctx context.Context) (int, error) {
	if sr.registryHashModValue == 0 {
		fio := newFileIOWithReplication(sr.replicationTracker, sr.manageStore, false)
		if fio.exists(ctx, RegistryHashModValueFilename) {
			if ba, err := fio.read(ctx, RegistryHashModValueFilename); err != nil {
				return 0, fmt.Errorf("failed reading registry hash mod value from %s, details: %w", RegistryHashModValueFilename, err)
			} else {
				if i, err := strconv.Atoi(string(ba)); err != nil {
					return 0, fmt.Errorf("read invalid registry hash mod value from %s, details: %w", RegistryHashModValueFilename, err)
				} else {
					sr.registryHashModValue = i
				}
			}
		}
	}
	return sr.registryHashModValue, nil
}

// Add appends new stores to the repository, updating the store list, creating folders,
// writing per-store metadata, and replicating the changes when configured. A cache entry is
// written for each added store. The store list is guarded by a cache-based lock.
func (sr *StoreRepository) Add(ctx context.Context, stores ...sop.StoreInfo) error {
	if len(stores) == 0 {
		return nil
	}

	// 1. Lock Store List.
	lk := sr.cache.CreateLockKeys([]string{sr.formatCacheKey(lockStoreListKey)})
	if err := sop.Retry(ctx, func(ctx context.Context) error {
		if ok, _, err := sr.cache.DualLock(ctx, lockStoreListDuration, lk); !ok || err != nil {
			if err == nil {
				err = fmt.Errorf("lock failed, key %s already locked by another", lockStoreListKey)
			}
			return retry.RetryableError(err)
		}
		return nil
	}, nil); err != nil {
		return err
	}
	defer sr.cache.Unlock(ctx, lk)

	// 2. Get Store List & convert to a map for lookup.
	sl, err := sr.GetAll(ctx)
	if err != nil {
		return err
	}
	storesLookup := make(map[string]byte, len(sl))
	for _, s := range sl {
		storesLookup[s] = 1
	}

	// 3. Merge added items to Store List. Only allow add of store with unique name.
	for _, store := range stores {
		if _, ok := storesLookup[store.Name]; ok {
			return fmt.Errorf("can't add store %s, an existing item with such name exists", store.Name)
		}
		storesLookup[store.Name] = 1
	}

	// 4. Write Store List to tmp file.
	storeWriter := newFileIOWithReplication(sr.replicationTracker, sr.manageStore, true)
	storeList := make([]string, len(storesLookup))
	i := 0
	for k := range storesLookup {
		storeList[i] = k
		i++
	}
	ba, _ := encoding.Marshal(storeList)
	if sl == nil {
		// Ensure the stores base folder is created.
		if err := storeWriter.createStore(ctx, ""); err != nil {
			return err
		}
	}
	if err := storeWriter.write(ctx, storeListFilename, ba); err != nil {
		return err
	}

	// 5-6. Create folders and write store info to its tmp file, for each added item.
	for _, store := range stores {
		if err := storeWriter.createStore(ctx, store.Name); err != nil {
			return err
		}

		// Persist store info into a JSON text file.
		ba, err := encoding.Marshal(store)
		if err != nil {
			return err
		}

		if err := storeWriter.write(ctx, fmt.Sprintf("%c%s%c%s", os.PathSeparator, store.Name, os.PathSeparator, StoreInfoFilename), ba); err != nil {
			return err
		}
	}

	// 7. Replicate the files if configured to.
	if err := storeWriter.replicate(ctx); err != nil {
		return err
	}

	// Cache each of the stores.
	for _, store := range stores {
		if err := sr.cache.SetStruct(ctx, sr.formatCacheKey(store.Name), &store, store.CacheConfig.StoreInfoCacheDuration); err != nil {
			log.Warn(logsafe.V(fmt.Sprintf("StoreRepository Add failed (redis setstruct), details: %v", err)))
		}
	}
	return nil
	// 8. Unlock Store List. The defer statement will unlock store list.
}

// Update merges the provided deltas into store metadata. To reduce deadlock chances it
// sorts store names and locks them in order using TTL-based cache locks with retry.
// On any failure mid-flight, an undo routine best-effort restores the affected entries.
func patchJSONNumericField(data []byte, field string, value int64) ([]byte, error) {
	key := []byte(`"` + field + `"`)
	start := bytes.Index(data, key)
	if start < 0 {
		return nil, fmt.Errorf("field %q not found", field)
	}
	colon := bytes.IndexByte(data[start+len(key):], ':')
	if colon < 0 {
		return nil, fmt.Errorf("field %q missing ':' delimiter", field)
	}
	valueStart := start + len(key) + colon + 1
	for valueStart < len(data) && (data[valueStart] == ' ' || data[valueStart] == '\t' || data[valueStart] == '\n' || data[valueStart] == '\r') {
		valueStart++
	}
	valueEnd := valueStart
	for valueEnd < len(data) && data[valueEnd] != ',' && data[valueEnd] != '}' {
		valueEnd++
	}
	if valueEnd <= valueStart {
		return nil, fmt.Errorf("field %q has no numeric value", field)
	}

	patched := append([]byte(nil), data[:valueStart]...)
	patched = append(patched, strconv.AppendInt(nil, value, 10)...)
	patched = append(patched, data[valueEnd:]...)
	return patched, nil
}

func (sr *StoreRepository) Update(ctx context.Context, stores []sop.StoreInfo) ([]sop.StoreInfo, error) {
	if len(stores) == 0 {
		return nil, nil
	}

	// Sort the stores info so we can commit them in same sort order across transactions,
	// thus, reduced chance of deadlock.
	slices.SortFunc(stores, func(a, b sop.StoreInfo) int {
		return cmp.Compare(a.Name, b.Name)
	})

	keys := make([]string, len(stores))
	for i := range stores {
		keys[i] = sr.formatCacheKey(stores[i].Name)
	}

	// Create lock IDs that we can use to logically lock and prevent other updates.
	lockKeys := sr.cache.CreateLockKeys(keys)

	// Lock all keys.
	if err := sop.Retry(ctx, func(ctx context.Context) error {
		// 15 minutes to lock, merge/update details then unlock.
		if ok, _, err := sr.cache.DualLock(ctx, updateStoresLockDuration, lockKeys); !ok || err != nil {
			if err == nil {
				err = fmt.Errorf("lock failed, key(s) already locked by another")
			}
			log.Warn(err.Error() + ", will retry")
			return retry.RetryableError(err)
		}
		return nil
	}, func(ctx context.Context) { sr.cache.Unlock(ctx, lockKeys) }); err != nil {
		return nil, err
	}

	storeWriter := newFileIOWithReplication(sr.replicationTracker, sr.manageStore, true)

	undo := func(endIndex int, original []sop.StoreInfo) {
		// Attempt to undo changes, 'ignores error as it is a last attempt to cleanup.
		for ii := 0; ii < endIndex; ii++ {
			log.Debug(fmt.Sprintf("undo occured for store %s", stores[ii].Name))

			sis, _ := sr.GetWithTTL(ctx, stores[ii].CacheConfig.IsStoreInfoCacheTTL, stores[ii].CacheConfig.StoreInfoCacheDuration, stores[ii].Name)
			if len(sis) == 0 {
				continue
			}

			si := sis[0]
			// Reverse the count delta should restore to true count value.
			si.Count = si.Count - stores[ii].CountDelta
			si.Timestamp = original[ii].Timestamp

			storePath := fmt.Sprintf("%c%s%c%s", os.PathSeparator, stores[ii].Name, os.PathSeparator, StoreInfoFilename)
			if si.Name != "" && !stores[ii].NeedsMetaDataSave {
				// Fast path: patch only the persisted count/timestamp bytes in place.
				ba, err := storeWriter.read(ctx, storePath)
				if err == nil {
					patched, err := patchJSONNumericField(ba, fieldCount, si.Count)
					if err == nil {
						patched, err = patchJSONNumericField(patched, fieldTimestamp, si.Timestamp)
					}
					if err == nil {
						if err := storeWriter.write(ctx, storePath, patched); err == nil {
							if err := sr.cache.SetStruct(ctx, sr.formatCacheKey(si.Name), &si, si.CacheConfig.StoreInfoCacheDuration); err != nil {
								log.Warn(fmt.Sprintf("StoreRepository Update Undo (redis setstruct) store %s failed, details: %v", si.Name, err))
							}
							continue
						}
					}
				}
			}

			// Fallback path preserves the original full-JSON write behavior when patching is not safe.
			ba, err := encoding.Marshal(si)
			if err != nil {
				log.Warn(fmt.Sprintf("StoreRepository Update Undo store %s failed Marshal, details: %v", si.Name, err))
				continue
			}

			if err := storeWriter.write(ctx, storePath, ba); err != nil {
				log.Warn(fmt.Sprintf("StoreRepository Update Undo store %s failed write, details: %v", si.Name, err))
				continue
			}
			si.NeedsMetaDataSave = false
			if err := sr.cache.SetStruct(ctx, sr.formatCacheKey(si.Name), &si, si.CacheConfig.StoreInfoCacheDuration); err != nil {
				log.Warn(fmt.Sprintf("StoreRepository Update Undo (redis setstruct) store %s failed, details: %v", si.Name, err))
			}
		}
	}

	beforeUpdateStores := make([]sop.StoreInfo, 0, len(stores))
	// Unlock all keys before going out of scope.
	defer sr.cache.Unlock(ctx, lockKeys)

	for i := range stores {
		sis, err := sr.GetWithTTL(ctx, stores[i].CacheConfig.IsStoreInfoCacheTTL, stores[i].CacheConfig.StoreInfoCacheDuration, stores[i].Name)
		if len(sis) == 0 {
			undo(i, beforeUpdateStores)
			return nil, err
		}

		si := sis[0]
		// Merge or apply the "count delta".
		stores[i].Count = si.Count + stores[i].CountDelta

		storePath := fmt.Sprintf("%c%s%c%s", os.PathSeparator, stores[i].Name, os.PathSeparator, StoreInfoFilename)
		if si.Name != "" && !stores[i].NeedsMetaDataSave {
			// Fast path: patch only the persisted count/timestamp bytes in place.
			ba, err := storeWriter.read(ctx, storePath)
			if err == nil {
				patched, err := patchJSONNumericField(ba, fieldCount, stores[i].Count)
				if err == nil {
					patched, err = patchJSONNumericField(patched, fieldTimestamp, stores[i].Timestamp)
				}
				if err == nil {
					if err := storeWriter.write(ctx, storePath, patched); err == nil {
						beforeUpdateStores = append(beforeUpdateStores, sis...)
						if err := sr.cache.SetStruct(ctx, sr.formatCacheKey(stores[i].Name), &stores[i], stores[i].CacheConfig.StoreInfoCacheDuration); err != nil {
							log.Warn(fmt.Sprintf("StoreRepository Update (redis setstruct) store %s failed, details: %v", stores[i].Name, err))
						}
						continue
					}
				}
			}
		}

		// Fallback path preserves the original full-JSON update behavior when patching is not safe.
		ba, err := encoding.Marshal(stores[i])
		if err != nil {
			undo(i, beforeUpdateStores)
			return nil, err
		}

		if err := storeWriter.write(ctx, storePath, ba); err != nil {
			// Undo changes.
			undo(i, beforeUpdateStores)
			return nil, err
		}

		beforeUpdateStores = append(beforeUpdateStores, sis...)
		stores[i].NeedsMetaDataSave = false
		if err := sr.cache.SetStruct(ctx, sr.formatCacheKey(stores[i].Name), &stores[i], stores[i].CacheConfig.StoreInfoCacheDuration); err != nil {
			log.Warn(fmt.Sprintf("StoreRepository Update (redis setstruct) store %s failed, details: %v", stores[i].Name, err))
		}
	}

	return stores, nil
}

// Get returns store info for the named stores, consulting the cache first.
func (sr *StoreRepository) Get(ctx context.Context, names ...string) ([]sop.StoreInfo, error) {
	return sr.GetWithTTL(ctx, false, 0, names...)
}

// GetAll returns the list of all store names. If the store list file is absent it returns nil
// to indicate an empty repository. The list itself is not cached by design.
func (sr *StoreRepository) GetAll(ctx context.Context) ([]string, error) {
	fio := newFileIOWithReplication(sr.replicationTracker, sr.manageStore, false)

	// Just return nil to denote no store yet on store folder.
	if !fio.exists(ctx, storeListFilename) {
		return nil, nil
	}
	ba, err := fio.read(ctx, storeListFilename)
	if err != nil {
		return nil, err
	}
	var storeList []string
	err = encoding.Unmarshal(ba, &storeList)
	// No need to cache the store list. (by intent, for now)
	return storeList, err
}

// GetWithTTL returns store info, optionally using TTL-aware cache lookups. Any misses are
// loaded from disk and then cached with the store's configured cache duration.
func (sr *StoreRepository) GetWithTTL(ctx context.Context, isCacheTTL bool, cacheDuration time.Duration, names ...string) ([]sop.StoreInfo, error) {
	stores := make([]sop.StoreInfo, 0, len(names))
	storesNotInCache := make([]string, 0)
	for i := range names {
		store := sop.StoreInfo{}
		var err error
		var found bool
		if isCacheTTL {
			found, err = sr.cache.GetStructEx(ctx, sr.formatCacheKey(names[i]), &store, cacheDuration)
		} else {
			found, err = sr.cache.GetStruct(ctx, sr.formatCacheKey(names[i]), &store)
		}
		if !found || err != nil {
			if err != nil {
				log.Warn(logsafe.V(fmt.Sprintf("StoreRepository Get (redis getstruct) failed, details: %v", err)))
			}
			storesNotInCache = append(storesNotInCache, names[i])
			continue
		}
		stores = append(stores, store)
	}
	if len(storesNotInCache) == 0 {
		return stores, nil
	}

	sio := newFileIOWithReplication(sr.replicationTracker, sr.manageStore, false)
	for _, s := range storesNotInCache {

		fn := fmt.Sprintf("%s%c%s", s, os.PathSeparator, StoreInfoFilename)
		if !sio.exists(ctx, fn) {
			continue
		}

		ba, err := sio.read(ctx, fn)
		if err != nil {
			return nil, err
		}

		var store sop.StoreInfo
		if err = encoding.Unmarshal(ba, &store); err != nil {
			return nil, err
		}

		if err := sr.cache.SetStruct(ctx, sr.formatCacheKey(store.Name), &store, store.CacheConfig.StoreInfoCacheDuration); err != nil {
			log.Warn(fmt.Sprintf("StoreRepository GetWithTTL (redis setstruct) failed, details: %v", err))
		}

		stores = append(stores, store)
	}

	return stores, nil
}

// GetStoreFileStat returns the FileInfo of the store's metadata file.
func (sr *StoreRepository) GetStoreFileStat(ctx context.Context, storeName string) (os.FileInfo, error) {
	fio := newFileIOWithReplication(sr.replicationTracker, sr.manageStore, false)
	fn := fmt.Sprintf("%s%c%s", storeName, os.PathSeparator, StoreInfoFilename)
	return fio.stat(ctx, fn)
}

func (sr *StoreRepository) getFromCache(ctx context.Context, names ...string) ([]sop.StoreInfo, error) {
	stores := make([]sop.StoreInfo, 0, len(names))
	for i := range names {
		store := sop.StoreInfo{}
		var err error
		var found bool
		found, err = sr.cache.GetStruct(ctx, sr.formatCacheKey(names[i]), &store)
		if !found || err != nil {
			continue
		}
		stores = append(stores, store)
	}
	return stores, nil
}

// Remove deletes the specified stores and their metadata, updates the store list, and replicates
// the removal when configured. Missing stores are tolerated with a warning.
func (sr *StoreRepository) Remove(ctx context.Context, storeNames ...string) error {
	lk := sr.cache.CreateLockKeys([]string{sr.formatCacheKey(lockStoreListKey)})
	if err := sop.Retry(ctx, func(ctx context.Context) error {
		if ok, _, err := sr.cache.DualLock(ctx, lockStoreListDuration, lk); !ok || err != nil {
			if err == nil {
				err = fmt.Errorf("lock failed, key %s already locked by another", lockStoreListKey)
			}
			return retry.RetryableError(err)
		}
		return nil
	}, nil); err != nil {
		return err
	}
	defer sr.cache.Unlock(ctx, lk)

	// Get Store List & convert to a map for lookup.
	sl, err := sr.GetAll(ctx)
	if err != nil {
		return err
	}
	storesLookup := make(map[string]byte, len(sl))
	for _, s := range sl {
		storesLookup[s] = 1
	}

	// Remove store(s) that exists. errs accumulates every store's removal failure (via
	// errors.Join) instead of only the last one, so a caller removing several stores can see
	// all of them instead of just whichever failed last.
	var errs error
	storeWriter := newFileIOWithReplication(sr.replicationTracker, sr.manageStore, true)
	for _, storeName := range storeNames {
		if _, ok := storesLookup[storeName]; !ok {
			// If store not found in list, it might be because the list is stale or the store was manually deleted.
			// We should still attempt to remove the folder to ensure cleanup.
			log.Info(logsafe.V(fmt.Sprintf("Store %s not found in store list, proceeding with folder removal attempt.", storeName)))
		}

		// Tolerate Redis cache failure.
		if _, err := sr.cache.Delete(ctx, []string{sr.formatCacheKey(storeName)}); err != nil {
			log.Warn(logsafe.V(fmt.Sprintf("StoreRepository Remove (redis Delete) failed, details: %v", err)))
		}
		// Delete store folder (contains blobs, store config & registry data files).
		if err := storeWriter.removeStore(ctx, storeName); err != nil {
			errs = errors.Join(errs, fmt.Errorf("StoreRepository Remove (fs Delete) of store %s failed: %w", storeName, err))
		}
		delete(storesLookup, storeName)

		log.Debug(logsafe.V(fmt.Sprintf("removed store %s", storeName)))
	}

	// Update Store list file of removed entries.
	storeList := slices.Sorted(maps.Keys(storesLookup))
	ba, _ := encoding.Marshal(storeList)

	if err := storeWriter.write(ctx, storeListFilename, ba); err != nil {
		return fmt.Errorf("StoreRepository Remove (write store list) failed, details: %w", err)
	}

	// Replicate the files if configured to.
	if err := storeWriter.replicate(ctx); err != nil {
		errs = errors.Join(errs, err)
	}

	return errs
}

// Replicate writes the updated per-store metadata to the passive target. Any write error disables
// the current operation, signaling the caller to handle replication failures upstream.
func (sr *StoreRepository) Replicate(ctx context.Context, stores []sop.StoreInfo) error {

	if !sr.replicationTracker.replicate || sr.replicationTracker.FailedToReplicate {
		log.Debug(fmt.Sprintf("replicate %v, FailedToReplicate %v, current target %s",
			sr.replicationTracker.replicate, sr.replicationTracker.FailedToReplicate,
			sr.replicationTracker.getActiveBaseFolder()))
		return nil
	}

	fio := NewFileIO()
	for i := range stores {
		// Persist store info into a JSON text file.
		ba, err := encoding.Marshal(stores[i])
		if err != nil {
			// Handle store marshal failures, it is not supposed to happen.
			// passive-side write failed: mark replication as failed to stop further passive writes
			sr.replicationTracker.handleFailedToReplicate(ctx)
			return fmt.Errorf("storeRepository.Replicate failed, error Marshal of store '%s', details: %w", stores[i].Name, err)
		}
		// When store is being written and it failed, we need to handle whether to turn off writing to the replication's passive destination
		// because if will break synchronization from here on out, thus, better to just log then turn off replication altogether, until cleared
		// to resume.
		filename := sr.replicationTracker.formatPassiveFolderEntity(fmt.Sprintf("%s%c%s", stores[i].Name, os.PathSeparator, StoreInfoFilename))
		if err := fio.WriteFile(ctx, filename, ba, permission); err != nil {
			// passive-side write failed: mark replication as failed to stop further passive writes
			sr.replicationTracker.handleFailedToReplicate(ctx)
			return err
		}
	}

	return nil
}

// GetStoresBaseFolder returns the currently active base folder path used for store files.
func (sr *StoreRepository) GetStoresBaseFolder() string {
	return sr.replicationTracker.getActiveBaseFolder()
}

func (sr *StoreRepository) formatCacheKey(name string) string {
	return fmt.Sprintf("%s:%s", sr.GetStoresBaseFolder(), name)
}
