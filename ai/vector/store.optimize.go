package vector

import (
	"context"
	"encoding/json"
	"fmt"
	log "log/slog"
	"math"
	"math/rand"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/common"
	"github.com/sharedcode/joltrin/v5/fs"
)

const (
	batchSize                   = 200
	optimizeLockDuration        = 1 * time.Hour
	optimizeLockRefreshInterval = 30 * time.Minute
	optimizeGracePeriod         = 1 * time.Hour
)

// Optimize reorganizes the index to improve query performance by rebuilding the vector structures.
//
// It performs two main functions:
// 1. Re-Clustering: Uses K-Means to calculate new centroids and re-distribute vectors for optimal search.
// 2. Garbage Collection: Physically removes items marked as "Soft Deleted" (Tombstones) from the Content store.
//
// This operation uses a batched approach to handle large datasets without hitting transaction limits.
// It commits the current transaction immediately to persist pending changes, then manages its own
// sequence of transactions to process the data in batches.
//
// Note: During optimization, the store enters a read-only mode. Write operations (Upsert, Delete)
// will return an error until optimization completes.
//
// Recommendation: The provided context should have a sufficient timeout or be background context,
// as optimization can take a significant amount of time depending on the dataset size.
//
// Warning: This method commits the transaction passed to Open(). The caller should not attempt
// to use the original transaction after calling Optimize().
func (di *domainIndex[T]) Optimize(ctx context.Context) error {
	log.Debug("Optimize started", "domain", di.name)

	// 1. Commit pending initial writes so Consolidate can see them
	if di.trans != nil && di.trans.HasBegun() {
		log.Debug("Committing initial transaction before optimization", "domain", di.name)
		if err := di.trans.Commit(ctx); err != nil {
			return err
		}
	}

	// 2. Perform TempVectors consolidation BEFORE locking optimization mode
	if err := di.Consolidate(ctx); err != nil {
		log.Warn("Optimize: failed to pre-consolidate TempVectors", "domain", di.name, "error", err)
		return err
	}

	// 3. Initialization (acquires optimize distributed lock)
	currentVersion, newVersion, suffix, useTempVectors, _, cleanupLock, err := di.initialize(ctx)
	if err != nil {
		return err
	}
	defer cleanupLock()

	// 2. Phase 1: Build Lookup Table
	count, err := di.phase1(ctx, currentVersion, suffix, useTempVectors)
	if err != nil {
		return err
	}

	// 3. Phase 2: Sampling & Centroids
	newCentroidsMap, err := di.phase2(ctx, currentVersion, suffix, count)
	if err != nil {
		return err
	}

	// 4. Phase 3: Migration
	if err := di.phase3(ctx, currentVersion, newVersion, suffix, useTempVectors, newCentroidsMap); err != nil {
		return err
	}

	// 5. Phase 4: Finalize
	if err := di.phase4(ctx, currentVersion, newVersion, useTempVectors); err != nil {
		return err
	}

	log.Debug("Optimize completed successfully")
	return nil
}

func (di *domainIndex[T]) openArch(ctx context.Context, tx sop.Transaction, ver int64) (*Architecture, error) {
	// Clear cache to force re-open with new tx
	di.archCache = nil
	di.trans = tx
	return di.getArchitecture(ctx, ver)
}

func (di *domainIndex[T]) openSysStore(ctx context.Context, tx sop.Transaction) (btree.BtreeInterface[string, string], error) {
	sysStoreName := fmt.Sprintf("%s%s", di.name, sysConfigSuffix)
	return newBtree[string, string](ctx, di.config, sop.ConfigureStore(sysStoreName, true, btree.DefaultSlotLength, sysConfigDesc, sop.SmallData, ""), tx, func(a, b string) int {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
}

func (di *domainIndex[T]) initialize(ctx context.Context) (int64, int64, string, bool, []*sop.LockKey, func(), error) {
	// Set Distributed Lock
	if di.config.Cache == nil {
		return 0, 0, "", false, nil, nil, fmt.Errorf("cache is required for optimization locking")
	}
	lockKeyName := fmt.Sprintf("optimize_lock_%s", di.name)
	lockKeys := di.config.Cache.CreateLockKeys([]string{lockKeyName})

	if ok, err := di.config.Cache.IsLockedByOthers(ctx, []string{lockKeyName}); err != nil {
		return 0, 0, "", false, nil, nil, fmt.Errorf("failed to check optimization lock: %w", err)
	} else if ok {
		return 0, 0, "", false, nil, nil, fmt.Errorf("optimization already in progress for domain %s", di.name)
	}

	success, _, err := di.config.Cache.DualLock(ctx, optimizeLockDuration, lockKeys)
	if err != nil {
		return 0, 0, "", false, nil, nil, fmt.Errorf("failed to acquire optimization lock: %w", err)
	}
	if !success {
		return 0, 0, "", false, nil, nil, fmt.Errorf("failed to acquire optimization lock for domain %s (already locked)", di.name)
	}

	// Start a goroutine to refresh the lock TTL periodically
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(optimizeLockRefreshInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if ok, err := di.config.Cache.IsLockedTTL(ctx, optimizeLockDuration, lockKeys); err != nil {
					log.Warn("Failed to refresh optimization lock TTL", "error", err)
				} else if !ok {
					log.Warn("Optimization lock lost during refresh")
					ok, _, err := di.config.Cache.DualLock(ctx, optimizeLockDuration, lockKeys)
					if err != nil {
						log.Warn("failed to re-acquire optimization lock: %w", "error", err)
					} else if !ok {
						log.Warn("failed to re-acquire optimization lock, it got locked by another process")
					}
				}
			}
		}
	}()

	cleanupLock := func() {
		close(done)
		if err := di.config.Cache.Unlock(ctx, lockKeys); err != nil {
			log.Warn("Failed to unlock optimization lock", "error", err)
		}
	}

	// We need to know the current version to start.
	// We start a short TX just to get the version.
	log.Debug("Starting check transaction")
	tx, err := di.beginTransaction(ctx)
	if err != nil {
		cleanupLock()
		return 0, 0, "", false, nil, nil, err
	}
	di.trans = tx

	// Re-open SysStore
	sysStore, err := di.openSysStore(ctx, tx)
	if err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			cleanupLock()
			return 0, 0, "", false, nil, nil, fmt.Errorf("openSysStore failed: %w, rollback failed: %v", err, rbErr)
		}
		cleanupLock()
		return 0, 0, "", false, nil, nil, err
	}
	di.sysStore = sysStore

	currentVersion, err := di.getActiveVersion(ctx)
	if err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			cleanupLock()
			return 0, 0, "", false, nil, nil, fmt.Errorf("getActiveVersion failed: %w, rollback failed: %v", err, rbErr)
		}
		cleanupLock()
		return 0, 0, "", false, nil, nil, err
	}
	log.Debug("Current version", "version", currentVersion)

	// Prepare New Stores (Version + 1)
	newVersion := currentVersion + 1
	suffix := fmt.Sprintf("_%d", newVersion)

	// Check if previous optimization failed (stores exist)
	lookupName := di.name + lookupSuffix + suffix
	found, err := infs.IsStoreExists(ctx, tx, lookupName)
	if err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			cleanupLock()
			return 0, 0, "", false, nil, nil, fmt.Errorf("IsStoreExists failed: %w, rollback failed: %v", err, rbErr)
		}
		cleanupLock()
		return 0, 0, "", false, nil, nil, err
	}
	if found {
		// Check grace period
		if err := di.checkGracePeriod(ctx, tx, lookupName); err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				log.Warn("Rollback failed during grace period abort", "error", rbErr)
			}
			cleanupLock()
			return 0, 0, "", false, nil, nil, err
		}

		log.Debug("Found previous failed optimization, cleaning up")
		storesToRemove := []string{
			di.name + centroidsSuffix + suffix,
			di.name + vectorsSuffix + suffix,
			di.name + lookupSuffix + suffix,
		}

		pt := tx.GetPhasedTransaction()
		if ct, ok := pt.(*common.Transaction); ok {
			for _, name := range storesToRemove {
				_ = ct.StoreRepository.Remove(ctx, name)
			}
		}
		if err := tx.Commit(ctx); err != nil {
			cleanupLock()
			return 0, 0, "", false, nil, nil, err
		}
	} else {
		log.Debug("No previous failed optimization, rolling back check TX")
		if err := tx.Rollback(ctx); err != nil {
			cleanupLock()
			return 0, 0, "", false, nil, nil, fmt.Errorf("rollback failed: %w", err)
		}
	}

	useTempVectors := false // Processed by Consolidate
	return currentVersion, newVersion, suffix, useTempVectors, lockKeys, cleanupLock, nil
}

func (di *domainIndex[T]) checkGracePeriod(ctx context.Context, tx sop.Transaction, lookupName string) error {
	if ct, ok := tx.GetPhasedTransaction().(*common.Transaction); ok {
		if sr, ok := ct.StoreRepository.(*fs.StoreRepository); ok {
			if fi, err := sr.GetStoreFileStat(ctx, lookupName); err == nil {
				if time.Since(fi.ModTime()) < optimizeGracePeriod {
					log.Warn("Optimization aborted: grace period active for existing store", "store", lookupName, "modTime", fi.ModTime())
					return fmt.Errorf("optimization aborted: grace period active for existing store %s", lookupName)
				}
			}
		}
	}
	return nil
}

func (di *domainIndex[T]) phase1(ctx context.Context, currentVersion int64, suffix string, useTempVectors bool) (int, error) {
	log.Debug("Phase 1: Build Lookup Table")

	var count int
	var lastVectorKey *ai.VectorKey
	var lastTempKey string

	for {
		log.Debug("Starting batch transaction")
		tx, err := di.beginTransaction(ctx)
		if err != nil {
			return 0, err
		}

		oldArch, err := di.openArch(ctx, tx, currentVersion)
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				return 0, fmt.Errorf("openArch failed: %w, rollback failed: %v", err, rbErr)
			}
			return 0, err
		}

		newLookup, err := newBtree[int, string](ctx, di.config, sop.ConfigureStore(di.name+lookupSuffix+suffix, true, btree.DefaultSlotLength, lookupDesc, sop.SmallData, ""), tx, func(a, b int) int { return a - b })
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				return 0, fmt.Errorf("newBtree failed: %w, rollback failed: %v", err, rbErr)
			}
			return 0, err
		}

		var ok bool
		var b3 btree.BtreeInterface[ai.VectorKey, []float32]
		var b3Temp btree.BtreeInterface[string, []float32]

		if useTempVectors {
			if oldArch.TempVectors == nil {
				if err := tx.Rollback(ctx); err != nil {
					return 0, fmt.Errorf("rollback failed: %w", err)
				}
				break
			}
			b3Temp = oldArch.TempVectors
			if lastTempKey == "" {
				ok, err = b3Temp.First(ctx)
			} else {
				ok, err = b3Temp.Find(ctx, lastTempKey, false)
				if err == nil && ok {
					ok, err = b3Temp.Next(ctx)
				}
			}
		} else {
			b3 = oldArch.Vectors
			if lastVectorKey == nil {
				ok, err = b3.First(ctx)
			} else {
				ok, err = b3.Find(ctx, *lastVectorKey, false)
				if err == nil && ok {
					ok, err = b3.Next(ctx)
				}
			}
		}

		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				return 0, fmt.Errorf("iterator failed: %w, rollback failed: %v", err, rbErr)
			}
			return 0, err
		}

		processed := 0
		if ok {
			for {
				var itemID string
				var key interface{}

				if useTempVectors {
					item, err := b3Temp.GetCurrentItem(ctx)
					if err != nil {
						if rbErr := tx.Rollback(ctx); rbErr != nil {
							return 0, fmt.Errorf("GetCurrentItem failed: %w, rollback failed: %v", err, rbErr)
						}
						return 0, err
					}
					itemID = item.Key
					key = item.Key

					shouldProcess := true
					if di.deduplicationEnabled {
						if found, err := oldArch.Content.Find(ctx, ai.ContentKey{ItemID: item.Key}, false); err != nil {
							if rbErr := tx.Rollback(ctx); rbErr != nil {
								return 0, fmt.Errorf("Content.Find failed: %w, rollback failed: %v", err, rbErr)
							}
							return 0, err
						} else if found {
							currentKey := oldArch.Content.GetCurrentKey().Key
							if currentKey.Deleted {
								shouldProcess = false
							} else if currentKey.CentroidID != 0 {
								shouldProcess = false
							}
						}
					}

					if shouldProcess {
						if _, err := newLookup.Add(ctx, count, itemID); err != nil {
							if rbErr := tx.Rollback(ctx); rbErr != nil {
								return 0, fmt.Errorf("newLookup.Add failed: %w, rollback failed: %v", err, rbErr)
							}
							return 0, err
						}
						count++
					}
				} else {
					item, err := b3.GetCurrentItem(ctx)
					if err != nil {
						if rbErr := tx.Rollback(ctx); rbErr != nil {
							return 0, fmt.Errorf("GetCurrentItem failed: %w, rollback failed: %v", err, rbErr)
						}
						return 0, err
					}
					itemID = item.Key.ItemID
					key = item.Key

					shouldProcess := !di.deduplicationEnabled
					if !shouldProcess {
						if found, err := oldArch.Content.Find(ctx, ai.ContentKey{ItemID: itemID}, false); err != nil {
							if rbErr := tx.Rollback(ctx); rbErr != nil {
								return 0, fmt.Errorf("Content.Find failed: %w, rollback failed: %v", err, rbErr)
							}
							return 0, err
						} else if found {
							currentKey := oldArch.Content.GetCurrentKey().Key
							if !currentKey.Deleted {
								cID := currentKey.CentroidID
								cDist := currentKey.Distance
								if currentKey.Version != currentVersion {
									if currentKey.NextVersion == currentVersion {
										cID = currentKey.NextCentroidID
										cDist = currentKey.Distance
										if currentKey.NextDistance != 0 {
											cDist = currentKey.NextDistance
										}
									}
								}
								vecKey, ok := key.(ai.VectorKey)
								if ok && cID == vecKey.CentroidID && math.Abs(float64(cDist-vecKey.DistanceToCentroid)) < 1e-3 {
									shouldProcess = true
								}
							}
						}
					}

					if shouldProcess {
						if _, err := newLookup.Add(ctx, count, itemID); err != nil {
							if rbErr := tx.Rollback(ctx); rbErr != nil {
								return 0, fmt.Errorf("newLookup.Add failed: %w, rollback failed: %v", err, rbErr)
							}
							return 0, err
						}
						count++
					}
				}

				processed++
				if useTempVectors {
					lastTempKey = key.(string)
				} else {
					k := key.(ai.VectorKey)
					lastVectorKey = &k
				}

				if processed >= batchSize {
					break
				}

				var nextOk bool
				if useTempVectors {
					nextOk, err = b3Temp.Next(ctx)
				} else {
					nextOk, err = b3.Next(ctx)
				}

				if err != nil {
					if rbErr := tx.Rollback(ctx); rbErr != nil {
						return 0, fmt.Errorf("Next failed: %w, rollback failed: %v", err, rbErr)
					}
					return 0, err
				} else if !nextOk {
					break
				}
			}
		}

		log.Debug("Committing batch", "processed_items", processed)
		if err := tx.Commit(ctx); err != nil {
			return 0, err
		}

		if processed < batchSize {
			break
		}
	}
	return count, nil
}

func (di *domainIndex[T]) phase2(ctx context.Context, currentVersion int64, suffix string, count int) (map[int][]float32, error) {
	log.Debug("Phase 2: Sampling & Centroids")
	if count > 0 {
		tx, err := di.beginTransaction(ctx)
		if err != nil {
			return nil, err
		}

		oldArch, err := di.openArch(ctx, tx, currentVersion)
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				return nil, fmt.Errorf("openArch failed: %w, rollback failed: %v", err, rbErr)
			}
			return nil, err
		}

		newLookup, err := newBtree[int, string](ctx, di.config, sop.ConfigureStore(di.name+lookupSuffix+suffix, true, btree.DefaultSlotLength, lookupDesc, sop.SmallData, ""), tx, func(a, b int) int { return a - b })
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				return nil, fmt.Errorf("newBtree failed: %w, rollback failed: %v", err, rbErr)
			}
			return nil, err
		}

		const maxSamples = 50000
		var samples []ai.Item[T]
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		sampleCount := maxSamples
		if count < maxSamples {
			sampleCount = count
		}
		indices := make(map[int]struct{})
		if count <= maxSamples {
			for i := 0; i < count; i++ {
				indices[i] = struct{}{}
			}
		} else {
			for len(indices) < sampleCount {
				idx := rng.Intn(count)
				indices[idx] = struct{}{}
			}
		}

		for idx := range indices {
			if found, err := newLookup.Find(ctx, idx, false); err != nil {
				if rbErr := tx.Rollback(ctx); rbErr != nil {
					return nil, fmt.Errorf("newLookup.Find failed: %w, rollback failed: %v", err, rbErr)
				}
				return nil, err
			} else if !found {
				continue
			}
			itemID, _ := newLookup.GetCurrentValue(ctx)

			var vec []float32
			if currentVersion == 0 && oldArch.TempVectors != nil {
				if found, err := oldArch.TempVectors.Find(ctx, itemID, false); err != nil {
					if rbErr := tx.Rollback(ctx); rbErr != nil {
						return nil, fmt.Errorf("TempVectors.Find failed: %w, rollback failed: %v", err, rbErr)
					}
					return nil, err
				} else if found {
					vec, _ = oldArch.TempVectors.GetCurrentValue(ctx)
				}
			} else {
				if found, err := oldArch.Content.Find(ctx, ai.ContentKey{ItemID: itemID}, false); err != nil {
					if rbErr := tx.Rollback(ctx); rbErr != nil {
						return nil, fmt.Errorf("Content.Find failed: %w, rollback failed: %v", err, rbErr)
					}
					return nil, err
				} else if !found {
					continue
				}
				currentKey := oldArch.Content.GetCurrentKey().Key
				cid := currentKey.CentroidID
				dist := currentKey.Distance
				if currentKey.Version != int64(currentVersion) && currentKey.NextVersion == int64(currentVersion) {
					cid = currentKey.NextCentroidID
					dist = currentKey.NextDistance
				}
				vecKey := ai.VectorKey{CentroidID: cid, DistanceToCentroid: dist, ItemID: itemID}
				if found, _ := oldArch.Vectors.Find(ctx, vecKey, false); found {
					vec, _ = oldArch.Vectors.GetCurrentValue(ctx)
				}
			}
			if len(vec) > 0 {
				samples = append(samples, ai.Item[T]{ID: itemID, Vector: vec})
			}
		}

		k := int(math.Sqrt(float64(count)))
		if k < 1 {
			k = 1
		}
		if k > 256 {
			k = 256
		}
		newCentroidsMap, err := ComputeCentroids(samples, k)
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				return nil, fmt.Errorf("ComputeCentroids failed: %w, rollback failed: %v", err, rbErr)
			}
			return nil, err
		}

		newCentroids, err := newBtree[int, ai.Centroid](ctx, di.config, sop.ConfigureStore(di.name+centroidsSuffix+suffix, true, btree.DefaultSlotLength, centroidsDesc, sop.SmallData, ""), tx, func(a, b int) int { return a - b })
		if err != nil {
			if rbErr := tx.Rollback(ctx); rbErr != nil {
				return nil, fmt.Errorf("newBtree failed: %w, rollback failed: %v", err, rbErr)
			}
			return nil, err
		}
		for id, vec := range newCentroidsMap {
			if _, err := newCentroids.Add(ctx, id, ai.Centroid{Vector: vec, VectorCount: 0}); err != nil {
				if rbErr := tx.Rollback(ctx); rbErr != nil {
					return nil, fmt.Errorf("newCentroids.Add failed: %w, rollback failed: %v", err, rbErr)
				}
				return nil, err
			}
		}

		di.updateCentroidsCache(newCentroidsMap)

		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return newCentroidsMap, nil
	} else {
		di.updateCentroidsCache(make(map[int][]float32))
		return di.centroidsCache, nil
	}
}

func (di *domainIndex[T]) phase3(ctx context.Context, currentVersion int64, newVersion int64, suffix string, useTempVectors bool, newCentroidsMap map[int][]float32) error {
	log.Debug("Phase 3: Migration")

	// 3a. Migrate Vectors
	if !useTempVectors {
		var lastVectorKey *ai.VectorKey
		for {
			tx, err := di.beginTransaction(ctx)
			if err != nil {
				return err
			}

			oldArch, err := di.openArch(ctx, tx, currentVersion)
			if err != nil {
				if rbErr := tx.Rollback(ctx); rbErr != nil {
					return fmt.Errorf("openArch failed: %w, rollback failed: %v", err, rbErr)
				}
				return err
			}

			newVectors, err := newBtree[ai.VectorKey, []float32](ctx, di.config, sop.ConfigureStore(di.name+vectorsSuffix+suffix, true, 10000, vectorsDesc, sop.SmallData, ""), tx, compositeKeyComparer)
			if err != nil {
				if rbErr := tx.Rollback(ctx); rbErr != nil {
					return fmt.Errorf("newBtree failed: %w, rollback failed: %v", err, rbErr)
				}
				return err
			}

			var newCentroids btree.BtreeInterface[int, ai.Centroid]
			if di.config.UsageMode == ai.DynamicWithVectorCountTracking {
				newCentroids, err = newBtree[int, ai.Centroid](ctx, di.config, sop.ConfigureStore(di.name+centroidsSuffix+suffix, true, btree.DefaultSlotLength, centroidsDesc, sop.SmallData, ""), tx, func(a, b int) int { return a - b })
				if err != nil {
					if rbErr := tx.Rollback(ctx); rbErr != nil {
						return fmt.Errorf("newBtree failed: %w, rollback failed: %v", err, rbErr)
					}
					return err
				}
			}

			b3 := oldArch.Vectors
			var ok bool
			if lastVectorKey == nil {
				ok, err = b3.First(ctx)
			} else {
				ok, err = b3.Find(ctx, *lastVectorKey, false)
				if err == nil && ok {
					ok, err = b3.Next(ctx)
				}
			}

			if err != nil {
				if rbErr := tx.Rollback(ctx); rbErr != nil {
					return fmt.Errorf("iterator failed: %w, rollback failed: %v", err, rbErr)
				}
				return err
			}

			processed := 0
			if ok {
				for {
					item, err := b3.GetCurrentItem(ctx)
					if err != nil {
						if rbErr := tx.Rollback(ctx); rbErr != nil {
							return fmt.Errorf("GetCurrentItem failed: %w, rollback failed: %v", err, rbErr)
						}
						return err
					}

					vec := *item.Value
					shouldMigrate := !di.deduplicationEnabled
					if !shouldMigrate {
						if found, _ := oldArch.Content.Find(ctx, ai.ContentKey{ItemID: item.Key.ItemID}, false); found {
							currentKey := oldArch.Content.GetCurrentKey().Key
							if !currentKey.Deleted {
								cID := currentKey.CentroidID
								cDist := currentKey.Distance
								if currentKey.Version != currentVersion {
									if currentKey.NextVersion == currentVersion {
										cID = currentKey.NextCentroidID
										cDist = currentKey.NextDistance
									}
								}
								if cID == item.Key.CentroidID && math.Abs(float64(cDist-item.Key.DistanceToCentroid)) < 1e-3 {
									shouldMigrate = true
								}
							} else {
								if _, err := oldArch.Content.RemoveCurrentItem(ctx); err != nil {
									if rbErr := tx.Rollback(ctx); rbErr != nil {
										return fmt.Errorf("RemoveCurrentItem failed: %w, rollback failed: %v", err, rbErr)
									}
									return err
								}
							}
						}
					}

					if shouldMigrate {
						cid, dist := findClosestCentroid(vec, newCentroidsMap)

						key := ai.VectorKey{CentroidID: cid, DistanceToCentroid: dist, ItemID: item.Key.ItemID}
						if _, err := newVectors.Add(ctx, key, vec); err != nil {
							if rbErr := tx.Rollback(ctx); rbErr != nil {
								return fmt.Errorf("newVectors.Add failed: %w, rollback failed: %v", err, rbErr)
							}
							return err
						}

						if newCentroids != nil {
							if found, _ := newCentroids.Find(ctx, cid, false); found {
								c, _ := newCentroids.GetCurrentValue(ctx)
								c.VectorCount++
								newCentroids.UpdateCurrentValue(ctx, c)
							}
						}

						if found, _ := oldArch.Content.Find(ctx, ai.ContentKey{ItemID: item.Key.ItemID}, false); found {
							currentKey := oldArch.Content.GetCurrentKey().Key

							// Critical Fix: If the item is currently relying on NextVersion for the active version,
							// we must promote it to the main fields to preserve it during this transition.
							// Otherwise, if we overwrite NextVersion and crash, we lose the reference to the current version.
							if currentKey.NextVersion == currentVersion {
								currentKey.Version = currentKey.NextVersion
								currentKey.CentroidID = currentKey.NextCentroidID
								currentKey.Distance = currentKey.NextDistance
							}

							currentKey.NextCentroidID = cid
							currentKey.NextDistance = dist
							currentKey.NextVersion = int64(newVersion)

							if _, err := oldArch.Content.UpdateCurrentKey(ctx, currentKey); err != nil {
								if rbErr := tx.Rollback(ctx); rbErr != nil {
									return fmt.Errorf("UpdateCurrentKey failed: %w, rollback failed: %v", err, rbErr)
								}
								return err
							}
						}
					}

					processed++
					k := item.Key
					lastVectorKey = &k

					if processed >= batchSize {
						break
					}

					if ok, err := b3.Next(ctx); err != nil {
						if rbErr := tx.Rollback(ctx); rbErr != nil {
							return fmt.Errorf("Next failed: %w, rollback failed: %v", err, rbErr)
						}
						return err
					} else if !ok {
						break
					}
				}
			}

			if err := tx.Commit(ctx); err != nil {
				return err
			}

			if processed < batchSize {
				break
			}
		}
	}

	// 3b. Migrate TempVectors
	if useTempVectors {
		var lastTempKey string
		for {
			tx, err := di.beginTransaction(ctx)
			if err != nil {
				return err
			}

			oldArch, err := di.openArch(ctx, tx, currentVersion)
			if err != nil {
				if rbErr := tx.Rollback(ctx); rbErr != nil {
					return fmt.Errorf("openArch failed: %w, rollback failed: %v", err, rbErr)
				}
				return err
			}

			if oldArch.TempVectors == nil {
				if err := tx.Rollback(ctx); err != nil {
					return fmt.Errorf("rollback failed: %w", err)
				}
				break
			}

			newVectors, err := newBtree[ai.VectorKey, []float32](ctx, di.config, sop.ConfigureStore(di.name+vectorsSuffix+suffix, true, 10000, vectorsDesc, sop.SmallData, ""), tx, compositeKeyComparer)
			if err != nil {
				if rbErr := tx.Rollback(ctx); rbErr != nil {
					return fmt.Errorf("newBtree failed: %w, rollback failed: %v", err, rbErr)
				}
				return err
			}

			var newCentroids btree.BtreeInterface[int, ai.Centroid]
			if di.config.UsageMode == ai.DynamicWithVectorCountTracking {
				newCentroids, err = newBtree[int, ai.Centroid](ctx, di.config, sop.ConfigureStore(di.name+centroidsSuffix+suffix, true, btree.DefaultSlotLength, centroidsDesc, sop.SmallData, ""), tx, func(a, b int) int { return a - b })
				if err != nil {
					if rbErr := tx.Rollback(ctx); rbErr != nil {
						return fmt.Errorf("newBtree failed: %w, rollback failed: %v", err, rbErr)
					}
					return err
				}
			}

			b3 := oldArch.TempVectors
			var ok bool
			if lastTempKey == "" {
				ok, err = b3.First(ctx)
			} else {
				ok, err = b3.Find(ctx, lastTempKey, false)
				if err == nil && ok {
					ok, err = b3.Next(ctx)
				}
			}

			if err != nil {
				if rbErr := tx.Rollback(ctx); rbErr != nil {
					return fmt.Errorf("iterator failed: %w, rollback failed: %v", err, rbErr)
				}
				return err
			}

			processed := 0
			if ok {
				for {
					item, err := b3.GetCurrentItem(ctx)
					if err != nil {
						if rbErr := tx.Rollback(ctx); rbErr != nil {
							return fmt.Errorf("GetCurrentItem failed: %w, rollback failed: %v", err, rbErr)
						}
						return err
					}

					shouldProcess := true
					if di.deduplicationEnabled {
						if found, _ := oldArch.Content.Find(ctx, ai.ContentKey{ItemID: item.Key}, false); found {
							currentKey := oldArch.Content.GetCurrentKey().Key
							if currentKey.Deleted {
								shouldProcess = false
								if _, err := oldArch.Content.RemoveCurrentItem(ctx); err != nil {
									if rbErr := tx.Rollback(ctx); rbErr != nil {
										return fmt.Errorf("RemoveCurrentItem failed: %w, rollback failed: %v", err, rbErr)
									}
									return err
								}
							} else if currentKey.CentroidID != 0 {
								shouldProcess = false
							}
						}
					}

					if shouldProcess {
						vec := *item.Value
						cid, dist := findClosestCentroid(vec, newCentroidsMap)

						key := ai.VectorKey{CentroidID: cid, DistanceToCentroid: dist, ItemID: item.Key}
						if _, err := newVectors.Add(ctx, key, vec); err != nil {
							if rbErr := tx.Rollback(ctx); rbErr != nil {
								return fmt.Errorf("newVectors.Add failed: %w, rollback failed: %v", err, rbErr)
							}
							return err
						}

						if newCentroids != nil {
							if found, _ := newCentroids.Find(ctx, cid, false); found {
								c, _ := newCentroids.GetCurrentValue(ctx)
								c.VectorCount++
								newCentroids.UpdateCurrentValue(ctx, c)
							}
						}

						if found, _ := oldArch.Content.Find(ctx, ai.ContentKey{ItemID: item.Key}, false); found {
							currentKey := oldArch.Content.GetCurrentKey().Key

							// Critical Fix: Promote NextVersion if it is the current active version.
							if currentKey.NextVersion == currentVersion {
								currentKey.Version = currentKey.NextVersion
								currentKey.CentroidID = currentKey.NextCentroidID
								currentKey.Distance = currentKey.NextDistance
							}

							currentKey.NextCentroidID = cid
							currentKey.NextDistance = dist
							currentKey.NextVersion = int64(newVersion)

							if _, err := oldArch.Content.UpdateCurrentKey(ctx, currentKey); err != nil {
								if rbErr := tx.Rollback(ctx); rbErr != nil {
									return fmt.Errorf("UpdateCurrentKey failed: %w, rollback failed: %v", err, rbErr)
								}
								return err
							}
						}
					}

					processed++
					lastTempKey = item.Key

					if processed >= batchSize {
						break
					}

					if ok, err := b3.Next(ctx); err != nil {
						if rbErr := tx.Rollback(ctx); rbErr != nil {
							return fmt.Errorf("Next failed: %w, rollback failed: %v", err, rbErr)
						}
						return err
					} else if !ok {
						break
					}
				}
			}

			if err := tx.Commit(ctx); err != nil {
				return err
			}

			if processed < batchSize {
				break
			}
		}
	}
	return nil
}

func (di *domainIndex[T]) phase4(ctx context.Context, currentVersion int64, newVersion int64, useTempVectors bool) error {
	log.Debug("Phase 4: Finalize")
	tx, err := di.beginTransaction(ctx)
	if err != nil {
		return err
	}
	di.trans = tx

	sysStore, err := di.openSysStore(ctx, tx)
	if err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			return fmt.Errorf("openSysStore failed: %w, rollback failed: %v", err, rbErr)
		}
		return err
	}
	di.sysStore = sysStore

	var meta Metadata = di.config.Metadata
	if found, _ := sysStore.Find(ctx, di.name, false); found {
		strVal, _ := sysStore.GetCurrentValue(ctx)
		json.Unmarshal([]byte(strVal), &meta)
		meta.ActiveVersion = newVersion
		bytes, _ := json.Marshal(meta)
		sysStore.UpdateCurrentValue(ctx, string(bytes))
	} else {
		meta.ActiveVersion = newVersion
		bytes, _ := json.Marshal(meta)
		sysStore.Add(ctx, di.name, string(bytes))
	}

	tx.OnCommit(func(ctx context.Context) error {
		pt := tx.GetPhasedTransaction()
		ct, ok := pt.(*common.Transaction)
		if !ok {
			return nil
		}

		storeName := fmt.Sprintf("%s%s", di.name, tempVectorsSuffix)
		_ = ct.StoreRepository.Remove(ctx, storeName)

		suffix := ""
		if currentVersion > 0 {
			suffix = fmt.Sprintf("_%d", currentVersion)
		}
		storesToRemove := []string{
			fmt.Sprintf("%s%s%s", di.name, centroidsSuffix, suffix),
			fmt.Sprintf("%s%s%s", di.name, vectorsSuffix, suffix),
			fmt.Sprintf("%s%s%s", di.name, lookupSuffix, suffix),
		}
		for _, name := range storesToRemove {
			if err := ct.StoreRepository.Remove(ctx, name); err != nil {
				log.Warn("Failed to remove store", "name", name, "error", err)
			}
		}
		return nil
	})

	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return nil
}
