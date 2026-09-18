// Package infs contains SOP implementations that use Redis for caching and the filesystem for backend data storage.
package infs

import (
	"context"
	"fmt"
	log "log/slog"
	"os"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/btree"
	"github.com/sharedcode/joltrin/common"
	"github.com/sharedcode/joltrin/fs"
	"github.com/sharedcode/joltrin/internal/logsafe"
	sd "github.com/sharedcode/joltrin/streamingdata"
)

// NewBtree creates a new B-tree instance with data persisted to the backend storage upon commit.
// If the B-tree (by name) is not found, a new one is created; otherwise, the existing one is opened and parameters validated.
// If you know it exists, prefer OpenBtree for clarity.
func NewBtree[TK btree.Ordered, TV any](ctx context.Context, so sop.StoreOptions, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	if ct, ok := t.GetPhasedTransaction().(*common.Transaction); ok {
		if ct.HandleReplicationRelatedError != nil {
			return nil, fmt.Errorf("failed in NewBtree as transaction has replication enabled, use NewBtreeWithReplication instead")
		}
	}
	so.DisableRegistryStoreFormatting = true
	so.DisableBlobStoreFormatting = true
	trans, _ := t.GetPhasedTransaction().(*common.Transaction)
	if sr, ok := trans.GetStoreRepository().(*fs.StoreRepository); ok {
		so.BlobStoreBaseFolderPath = sr.GetStoresBaseFolder()
	}
	return common.NewBtree[TK, TV](ctx, so, t, comparer)
}

// OpenBtree opens an existing B-tree instance and prepares it for use in a transaction.
func OpenBtree[TK btree.Ordered, TV any](ctx context.Context, name string, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	if ct, ok := t.GetPhasedTransaction().(*common.Transaction); ok {
		if ct.HandleReplicationRelatedError != nil {
			return nil, fmt.Errorf("failed in OpenBtree as transaction has replication enabled, use OpenBtreeWithReplication instead")
		}
	}
	return common.OpenBtree[TK, TV](ctx, name, t, comparer)
}

// IsStoreExists checks if a B-tree store with the given name exists in the backend.
func IsStoreExists(ctx context.Context, t sop.Transaction, name string) (bool, error) {
	if ct, ok := t.GetPhasedTransaction().(*common.Transaction); ok {
		stores, err := ct.StoreRepository.Get(ctx, name)
		if err != nil {
			return false, err
		}
		return len(stores) > 0, nil
	}
	return false, fmt.Errorf("transaction is not a valid SOP transaction")
}

// NewBtreeWithReplication creates a B-tree that uses SOP's filesystem-based replication feature.
func NewBtreeWithReplication[TK btree.Ordered, TV any](ctx context.Context, so sop.StoreOptions, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	if ct, ok := t.GetPhasedTransaction().(*common.Transaction); ok {
		if ct.HandleReplicationRelatedError == nil {
			return nil, fmt.Errorf("failed in NewBtreeWithReplication as transaction has no replication, use NewBtree instead")
		}
	}
	so.DisableRegistryStoreFormatting = true
	so.DisableBlobStoreFormatting = true
	return common.NewBtree[TK, TV](ctx, so, t, comparer)
}

// OpenBtreeWithReplication opens a B-tree that uses SOP's filesystem-based replication feature.
func OpenBtreeWithReplication[TK btree.Ordered, TV any](ctx context.Context, name string, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	if ct, ok := t.GetPhasedTransaction().(*common.Transaction); ok {
		if ct.HandleReplicationRelatedError == nil {
			return nil, fmt.Errorf("failed in OpenBtreeWithReplication as transaction has no replication, use OpenBtree instead")
		}
	}
	return common.OpenBtree[TK, TV](ctx, name, t, comparer)
}

// RemoveBtree removes the B-tree with the given name from backend storage.
// This is destructive: it drops registry and node-blob data and cannot be rolled back.
func RemoveBtree(ctx context.Context, name string, storesFolders []string, erasureConfig map[string]sop.ErasureCodingConfig, cacheType sop.L2CacheType, redisConfig ...*sop.RedisCacheConfig) error {
	if len(storesFolders) == 0 {
		return fmt.Errorf("needs at least a folder to delete a Btree")
	}
	var rc *sop.RedisCacheConfig
	if len(redisConfig) > 0 {
		rc = redisConfig[0]
	}
	cache := sop.GetL2Cache(sop.TransactionOptions{CacheType: cacheType, RedisConfig: rc})
	if cache == nil {
		return fmt.Errorf("unable to get L2 cache for type %v", cacheType)
	}

	log.Info(logsafe.V(fmt.Sprintf("Btree %s%c%s is about to be deleted", storesFolders[0], os.PathSeparator, name)))

	// If storesFolders is 2 or more, replicated mode is achieved, remove will delete on two store folder paths.
	replicationTracker, err := fs.NewReplicationTracker(ctx, storesFolders, len(storesFolders) >= 2, cache)
	if err != nil {
		return err
	}

	var storeRepository *fs.StoreRepository
	if len(erasureConfig) == 0 {
		storeRepository, err = fs.NewStoreRepository(ctx, replicationTracker, nil, cache, 0)
		if err != nil {
			return err
		}
		// Actually delete the Store folder recursively.
		// If in Replication structure, delete Active/Passive registry folders and the Blob Store Erasure Config folder structures.
		if err := storeRepository.Remove(ctx, name); err != nil {
			return err
		}
		return nil
	}

	// Delete the Btree with name from storeRepository drives.
	if !areFoldersInEC(name, storesFolders, erasureConfig) {
		fio := fs.NewFileIO()
		mbsf := fs.NewManageStoreFolder(fio)
		// Pass MinimumModValue, it will not be used when deleting files.
		storeRepository, err = fs.NewStoreRepository(ctx, replicationTracker, mbsf, cache, fs.MinimumModValue)
		if err != nil {
			return err
		}

		// Actually delete the Store folder recursively.
		// If in Replication structure, delete Active/Passive registry folders and the Blob Store Erasure Config folder structures.
		if err := storeRepository.Remove(ctx, name); err != nil {
			// Log the error, it does not cause corruption so, allow to proceed.
			log.Error(err.Error())
		}
	}

	// Delete the Btree with name from the Erasure Config drives.
	bswec, err := fs.NewBlobStoreWithEC(fs.DefaultToFilePath, nil, erasureConfig)
	if err != nil {
		return err
	}

	// Delete the Blob store folder from EC drives.
	bs, _ := bswec.(*fs.BlobStoreWithEC)
	if err := bs.RemoveStore(ctx, name); err != nil {
		// Log the error as warning, it does not cause corruption.
		log.Warn(err.Error())
	}
	return nil
}

func areFoldersInEC(name string, folders []string, erasureConfig map[string]sop.ErasureCodingConfig) bool {
	ec := erasureConfig[name]
	if len(ec.BaseFolderPathsAcrossDrives) == 0 {
		ec = erasureConfig[""]
	}
	if len(ec.BaseFolderPathsAcrossDrives) == 0 {
		return false
	}

	// Check if folders are in the EC (drive) folders. It is a simple check, user should specify
	// identical folder paths for this to work.
	//
	// E.g. folders = {"/Volume/disk1/sop1", "/Volume/disk1/sop2"}; EC.BaseFolderPathsAcrossDrives = {"/Volume/disk1/sop1", "/Volume/disk1/sop2"}
	// Will not match: folders = {"disk1/sop1, "disk1/sop2"}; EC.BaseFolderPathsAcrossDrives = {"/Volume/disk1/sop1", "/Volume/disk1/sop2"}
	for _, ecf := range ec.BaseFolderPathsAcrossDrives {
		for _, f := range folders {
			if ecf != f {
				return false
			}
		}
	}
	return true
}

// ReinstateFailedDrives asks the replication tracker to reinstate failed passive targets.
// storesFolders must contain the active and passive stores' base folder paths.
func ReinstateFailedDrives(ctx context.Context, storesFolders []string, cacheType sop.L2CacheType) error {
	if len(storesFolders) != 2 {
		return fmt.Errorf("'storeFolders' need to be array of two strings(drive/folder paths)")
	}

	cache := sop.GetL2Cache(sop.TransactionOptions{CacheType: cacheType})
	if cache == nil {
		return fmt.Errorf("unable to get L2 cache for type %v", cacheType)
	}

	rt, err := fs.NewReplicationTracker(ctx, storesFolders, true, cache)
	if err != nil {
		return fmt.Errorf("failed instantiating Replication Tracker: %v", err)
	}

	if err := rt.ReinstateFailedDrives(ctx); err != nil {
		return fmt.Errorf("failed reinstating failed drives: %w", err)
	}
	return nil
}

// Streaming Data Store related.

// NewStreamingDataStore creates a streaming data store backed by a B-tree for chunked large-object storage.
func NewStreamingDataStore[TK btree.Ordered](ctx context.Context, so sop.StoreOptions, trans sop.Transaction, comparer btree.ComparerFunc[sd.StreamingDataKey[TK]]) (*sd.StreamingDataStore[TK], error) {
	if so.SlotLength < sd.MinimumStreamingStoreSlotLength {
		return nil, fmt.Errorf("streaming data store requires minimum of %d SlotLength", sd.MinimumStreamingStoreSlotLength)
	}
	if so.IsValueDataInNodeSegment {
		return nil, fmt.Errorf("streaming data store requires value data to be set for save in separate segment(IsValueDataInNodeSegment = false)")
	}
	if !so.IsUnique {
		return nil, fmt.Errorf("streaming data store requires unique key (IsUnique = true) to be set to true")
	}
	btree, err := NewBtree[sd.StreamingDataKey[TK], []byte](ctx, so, trans, comparer)
	if err != nil {
		return nil, err
	}
	return &sd.StreamingDataStore[TK]{
		BtreeInterface: btree,
	}, nil
}

// OpenStreamingDataStore opens an existing streaming data store.
func OpenStreamingDataStore[TK btree.Ordered](ctx context.Context, name string, trans sop.Transaction, comparer btree.ComparerFunc[sd.StreamingDataKey[TK]]) (*sd.StreamingDataStore[TK], error) {
	btree, err := OpenBtree[sd.StreamingDataKey[TK], []byte](ctx, name, trans, comparer)
	if err != nil {
		return nil, err
	}
	return &sd.StreamingDataStore[TK]{
		BtreeInterface: btree,
	}, nil
}

// NewStreamingDataStoreWithReplication creates a streaming data store with filesystem replication enabled.
func NewStreamingDataStoreWithReplication[TK btree.Ordered](ctx context.Context, so sop.StoreOptions, trans sop.Transaction, comparer btree.ComparerFunc[sd.StreamingDataKey[TK]]) (*sd.StreamingDataStore[TK], error) {
	if so.SlotLength < sd.MinimumStreamingStoreSlotLength {
		return nil, fmt.Errorf("streaming data store requires minimum of %d SlotLength", sd.MinimumStreamingStoreSlotLength)
	}
	if so.IsValueDataInNodeSegment {
		return nil, fmt.Errorf("streaming data store requires value data to be set for save in separate segment(IsValueDataInNodeSegment = false)")
	}
	if !so.IsUnique {
		return nil, fmt.Errorf("streaming data store requires unique key (IsUnique = true) to be set to true")
	}
	btree, err := NewBtreeWithReplication[sd.StreamingDataKey[TK], []byte](ctx, so, trans, comparer)
	if err != nil {
		return nil, err
	}
	return &sd.StreamingDataStore[TK]{
		BtreeInterface: btree,
	}, nil
}

// OpenStreamingDataStoreWithReplication opens an existing streaming data store with filesystem replication enabled.
func OpenStreamingDataStoreWithReplication[TK btree.Ordered](ctx context.Context, name string, trans sop.Transaction, comparer btree.ComparerFunc[sd.StreamingDataKey[TK]]) (*sd.StreamingDataStore[TK], error) {
	btree, err := OpenBtreeWithReplication[sd.StreamingDataKey[TK], []byte](ctx, name, trans, comparer)
	if err != nil {
		return nil, err
	}
	return &sd.StreamingDataStore[TK]{
		BtreeInterface: btree,
	}, nil
}
