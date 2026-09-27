package fs

import (
	"context"
	"errors"
	"fmt"
	log "log/slog"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs/erasure"
)

const (
	maxThreadCount = 7
)

// BlobStore has no caching built in because blobs are huge, caller code can apply caching on top of it.
// BlobStoreWithEC adds Erasure Coding (EC) for replication/tolerance across multiple drives.
type BlobStoreWithEC struct {
	fileIO                      FileIO
	toFilePath                  ToFilePathFunc
	erasure                     map[string]*erasure.Erasure
	baseFolderPathsAcrossDrives map[string][]string
	repairCorruptedShards       bool
}

// errBox is used with atomic.Pointer to capture any error from worker goroutines without locks.
type errBox struct{ err error }

// Allows app to specify a global Erasure Coding Config once and allow code to simply don't bother
// specifying it again.
var globalErasureConfig map[string]sop.ErasureCodingConfig

// Invoke SetGlobalErasureConfig to set the application global Erasure Coding Config lookup.
func SetGlobalErasureConfig(erasureConfig map[string]sop.ErasureCodingConfig) {
	globalErasureConfig = erasureConfig
}

// Returns the global Erasure Coding config.
func GetGlobalErasureConfig() map[string]sop.ErasureCodingConfig {
	return globalErasureConfig
}

// Instantiate a blob store with replication (via Erasure Coding (EC)) capabilities.
// If a per-table EC config is not supplied, the global configuration is used.
// Validates that the number of base paths equals data+parity shard count.
func NewBlobStoreWithEC(toFilePath ToFilePathFunc, fileIO FileIO, erasureConfig map[string]sop.ErasureCodingConfig) (sop.BlobStore, error) {
	if erasureConfig == nil {
		erasureConfig = globalErasureConfig
	}
	if toFilePath == nil {
		toFilePath = DefaultToFilePath
	}

	var e map[string]*erasure.Erasure
	var baseFolderPathsAcrossDrives map[string][]string
	var repairCorruptedShards bool

	if erasureConfig != nil {
		e = make(map[string]*erasure.Erasure, len(erasureConfig))
		baseFolderPathsAcrossDrives = make(map[string][]string, len(erasureConfig))
		for k, v := range erasureConfig {
			ec, err := erasure.NewErasure(v.DataShardsCount, v.ParityShardsCount)
			if err != nil {
				return nil, err
			}
			repairCorruptedShards = v.RepairCorruptedShards
			if ec.DataShardsCount+ec.ParityShardsCount != len(v.BaseFolderPathsAcrossDrives) {
				return nil, fmt.Errorf("baseFolderPaths array elements count should match the sum of dataShardsCount & parityShardsCount")
			}
			e[k] = ec
			baseFolderPathsAcrossDrives[k] = v.BaseFolderPathsAcrossDrives
		}
	}
	if fileIO == nil {
		fileIO = newFileIO(sop.FileIOError)
	}
	return &BlobStoreWithEC{
		fileIO:                      fileIO,
		toFilePath:                  toFilePath,
		erasure:                     e,
		baseFolderPathsAcrossDrives: baseFolderPathsAcrossDrives,
		repairCorruptedShards:       repairCorruptedShards,
	}, nil
}

// GetOne reads shards across drives, extracts per-shard metadata, and decodes via EC.
// If some shards are missing but enough remain (>= data shards), decoding still succeeds.
// Optionally repairs corrupted/bitrotted shards by recomputing and rewriting them.
func (b *BlobStoreWithEC) GetOne(ctx context.Context, blobFilePath string, blobID sop.UUID) ([]byte, error) {
	// Spin up a job processor of max thread count (threads) maximum.
	tr := sop.NewTaskRunner(ctx, maxThreadCount)

	// Get the blob table (blobFilePath) specific ec configuration.
	baseFolderPathsAcrossDrives, ec := b.getBaseFolderPathsAndErasureConfig(blobFilePath)
	if baseFolderPathsAcrossDrives == nil {
		return nil, fmt.Errorf("can't find Erasure Config setting for file %s", blobFilePath)
	}

	shards := make([][]byte, len(baseFolderPathsAcrossDrives))
	shardsWithMetadata := make([][]byte, len(baseFolderPathsAcrossDrives))
	shardsMetaData := make([][]byte, len(baseFolderPathsAcrossDrives))
	// Capture any error across shard reads without locks/channels.
	var readErrPtr atomic.Pointer[errBox]

	for i := range baseFolderPathsAcrossDrives {
		baseFolderPath := fmt.Sprintf("%s%c%s", baseFolderPathsAcrossDrives[i], os.PathSeparator, blobFilePath)
		blobKey := blobID

		shardIndex := i
		fp := b.toFilePath(baseFolderPath, blobKey)
		fn := fmt.Sprintf("%s%c%s_%d", fp, os.PathSeparator, blobKey.String(), shardIndex)

		tr.Go(func() error {
			log.Debug(fmt.Sprintf("reading from file %s", fn))

			ba, err := b.fileIO.ReadFile(ctx, fn)
			if err != nil {
				// Store any error (winner doesn't matter).
				readErrPtr.Store(&errBox{err: err})
				log.Warn(fmt.Sprintf("failed reading from file %s, error: %v", fn, err))
				log.Info("if there are enough shards to reconstruct data, 'reader' may still work")
				return nil
			}
			shardsWithMetadata[shardIndex] = ba
			shardsMetaData[shardIndex] = ba[0:erasure.MetaDataSize]
			shards[shardIndex] = ba[erasure.MetaDataSize:]
			return nil
		})
	}
	if err := tr.Wait(); err != nil {
		return nil, err
	}

	// Just return the (last) error if shards is empty.
	if isShardsEmpty(shards) {
		if eb := readErrPtr.Load(); eb != nil && eb.err != nil {
			return nil, eb.err
		}
		return nil, fmt.Errorf("failed to read shards; no data and no error captured")
	}
	dr := ec.Decode(shards, shardsMetaData)
	if dr.Error != nil {
		return nil, dr.Error
	}

	if b.repairCorruptedShards && len(dr.ReconstructedShardsIndeces) > 0 {
		// Repair corrupted or bitrot shards (a.k.a. damaged shards). Just do sequential processing as
		// damaged shards should be typically one, residing in a drive that failed.
		if encodedShards, err := ec.Encode(dr.DecodedData); err == nil {
			for _, i := range dr.ReconstructedShardsIndeces {
				baseFolderPath := fmt.Sprintf("%s%c%s", baseFolderPathsAcrossDrives[i], os.PathSeparator, blobFilePath)
				blobKey := blobID

				fp := b.toFilePath(baseFolderPath, blobKey)
				fn := fmt.Sprintf("%s%c%s_%d", fp, os.PathSeparator, blobKey.String(), i)

				log.Debug(fmt.Sprintf("repairing file %s", fn))

				md := ec.ComputeShardMetadata(len(dr.DecodedData), encodedShards, i)
				buf := make([]byte, len(md)+len(encodedShards[i]))

				// Tip: consider refactor to write metadata then write the shard data so we don't use temp variable,
				// more optimal if shard size is huge.
				copy(buf, md)
				copy(buf[len(md):], encodedShards[i])
				shardsWithMetadata[i] = buf

				err := b.fileIO.WriteFile(ctx, fn, shardsWithMetadata[i], permission)
				// Just log warning if damaged shard can't be repaired as we have reconstructed it part of return.
				if err != nil {
					log.Warn(fmt.Sprintf("error encountered repairing a damaged shard (%s)", fn))
				}
			}
		}
	}

	return dr.DecodedData, nil
}

func (b *BlobStoreWithEC) Update(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	return b.Add(ctx, storesblobs)
}

// Add splits blob content into shards, writes each shard with per-shard metadata prefix, and
// tolerates up to ParityShardsCount write failures per blob. Errors beyond tolerance trigger rollback.
func (b *BlobStoreWithEC) Add(ctx context.Context, storesblobs []sop.BlobsPayload[sop.KeyValuePair[sop.UUID, []byte]]) error {
	if len(storesblobs) == 0 {
		return nil
	}

	trBlobs := sop.NewTaskRunner(ctx, maxThreadCount)

	for i := range storesblobs {
		index := i

		trBlobs.Go(func() error {
			// Get the blob table specific erasure configuration.
			baseFolderPathsAcrossDrives, erasure := b.getBaseFolderPathsAndErasureConfig(storesblobs[index].BlobTable)
			if baseFolderPathsAcrossDrives == nil {
				err := fmt.Errorf("can't find Erasure Config setting for file %s", storesblobs[index].BlobTable)
				log.Error(err.Error())
				return err
			}
			var lastErr error

			for _, blob := range storesblobs[index].Blobs {
				ba := blob.Value
				contentsSize := len(ba)

				shards, err := erasure.Encode(ba)
				if err != nil {
					return err
				}

				// Spin up a job processor of shards count max threads.
				trShards := sop.NewTaskRunner(trBlobs.GetContext(), -1)
				ch := make(chan error, len(shards))

				for i := range shards {
					baseFolderPath := fmt.Sprintf("%s%c%s", baseFolderPathsAcrossDrives[i], os.PathSeparator, storesblobs[index].BlobTable)
					blobKey := blob.Key
					shardIndex := i

					// Task WriteFile will add or replace existing file.
					trShards.Go(func() error {
						fp := b.toFilePath(baseFolderPath, blobKey)
						if !b.fileIO.Exists(ctx, fp) {
							if err := b.fileIO.MkdirAll(ctx, fp, permission); err != nil {
								ch <- err
								return nil
							}
						}

						log.Debug(fmt.Sprintf("writing to file %s", fp))

						fn := fmt.Sprintf("%s%c%s_%d", fp, os.PathSeparator, blobKey.String(), shardIndex)

						md := erasure.ComputeShardMetadata(contentsSize, shards, shardIndex)
						buf := make([]byte, len(md)+len(shards[shardIndex]))

						// Prefix the shard w/ metadata followed by the shard.
						copy(buf, md)
						copy(buf[len(md):], shards[shardIndex])

						if err := b.fileIO.WriteFile(ctx, fn, buf, permission); err != nil {
							ch <- err
							// Return nil so we don't generate an error, NOT until we exceed write error beyond parity count.
							return nil
						}
						return nil
					})
				}
				// Keep the last error and return that, enough to rollback the transaction.
				if err := trShards.Wait(); err != nil {
					close(ch)
					lastErr = err
				} else {
					c := 0
					cont := true
					for cont {
						select {
						case err, ok := <-ch:
							if ok {
								c++
								lastErr = err
							} else {
								cont = false
							}
						default:
							cont = false
						}
					}
					close(ch)
					// Short circuit to cause transaction rollback if drive failures are untolerable.
					if c > erasure.ParityShardsCount {
						return lastErr
					} else {
						if lastErr != nil {
							log.Warn(fmt.Sprintf("error writing to a drive but EC tolerates it, details: %v", lastErr))
							lastErr = nil
						}
					}
				}
			}
			return lastErr
		})
	}

	return trBlobs.Wait()
}

// Remove deletes shard files across all configured drives. Errors are tolerated and logged when
// replication is expected to handle eventual consistency.
func (b *BlobStoreWithEC) Remove(ctx context.Context, storesBlobsIDs []sop.BlobsPayload[sop.UUID]) error {
	// Spin up a job processor of max thread count (threads) maximum.
	tr := sop.NewTaskRunner(ctx, maxThreadCount)
	// Capture any error across file removals without locks/channels.
	for _, storeBlobIDs := range storesBlobsIDs {
		// Get the blob table specific erasure configuration.
		baseFolderPathsAcrossDrives, _ := b.getBaseFolderPathsAndErasureConfig(storeBlobIDs.BlobTable)
		if baseFolderPathsAcrossDrives == nil {
			return fmt.Errorf("can't find Erasure Config setting for file %s", storeBlobIDs.BlobTable)
		}

		for _, blobID := range storeBlobIDs.Blobs {

			for i := range baseFolderPathsAcrossDrives {
				baseFolderPath := fmt.Sprintf("%s%c%s", baseFolderPathsAcrossDrives[i], os.PathSeparator, storeBlobIDs.BlobTable)
				blobKey := blobID

				fp := b.toFilePath(baseFolderPath, blobKey)
				fn := fmt.Sprintf("%s%c%s_%d", fp, os.PathSeparator, blobKey.String(), i)

				// Do nothing if file already not existent.
				if !b.fileIO.Exists(ctx, fn) {
					continue
				}

				tr.Go(func() error {
					if err := b.fileIO.Remove(ctx, fn); err != nil {
						log.Warn(fmt.Sprintf("error deleting from drive but ignoring it to tolerate, part of EC feature, details: %v", err))
					}
					return nil
				})
			}

		}
	}
	return tr.Wait()
}

// RemoveStore recursively deletes the base folder for a store and all of its contents.
func (b *BlobStoreWithEC) RemoveStore(ctx context.Context, blobStoreName string) error {
	baseFolderPathsAcrossDrives, _ := b.getBaseFolderPathsAndErasureConfig(blobStoreName)
	if len(baseFolderPathsAcrossDrives) == 0 {
		return fmt.Errorf("can't find Erasure Config setting for file %s", blobStoreName)
	}

	var errs error
	for _, folderPath := range baseFolderPathsAcrossDrives {
		// Needs to delete the store folder relative to the base folder path.
		path := filepath.Join(folderPath, blobStoreName)
		if err := b.fileIO.RemoveAll(ctx, path); err != nil {
			// Join every drive's error instead of keeping only the last one, but attempt to
			// delete from all EC drive paths provided not to leak storage.
			errs = errors.Join(errs, fmt.Errorf("drive path %s: %w", path, err))
		}
	}

	if errs == nil {
		return nil
	}
	return fmt.Errorf("unable to delete all blob store(%s) folders in EC: %w", blobStoreName, errs)
}

func isShardsEmpty(shards [][]byte) bool {
	for i := range shards {
		if shards[i] != nil {
			return false
		}
	}
	return true
}

func (b *BlobStoreWithEC) getBaseFolderPathsAndErasureConfig(blobTable string) ([]string, *erasure.Erasure) {
	// Get the blob table specific erasure configuration.
	paths := b.baseFolderPathsAcrossDrives[blobTable]
	erasure := b.erasure[blobTable]

	if paths == nil {
		paths = b.baseFolderPathsAcrossDrives[""]
		erasure = b.erasure[""]
	}
	return paths, erasure
}
