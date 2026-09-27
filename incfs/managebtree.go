// Package incfs contains SOP implementations that uses Redis for caching, Cassandra & File System for backend data storage.
// The Objects Registry is stored in Cassandra and the B-Tree Nodes & their items' value data blobs are stored in the File System.
package incfs

import (
	"context"
	"fmt"

	cas "github.com/sharedcode/joltrin/adapters/cassandra"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/fs"
	"github.com/sharedcode/joltrin/v5/inredck"
	sd "github.com/sharedcode/joltrin/v5/streamingdata"
)

// NewBtree creates a new B-Tree instance with data persisted to backend storage upon commit.
// If B-Tree(name) is not found in the backend, a new one will be created. Otherwise, the existing one will be opened
// and the parameters checked if matching. If you know that it exists, then it is more convenient and more readable to call
// the OpenBtree function.
func NewBtree[TK btree.Ordered, TV any](ctx context.Context, si sop.StoreOptions, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	if si.BlobStoreBaseFolderPath == "" {
		return nil, fmt.Errorf("si.BlobStoreBaseFolderPath(\"\") needs to be a valid folder path")
	}
	return inredck.NewBtree[TK, TV](ctx, si, t, comparer)
}

// NewBtreeWithReplication is geared for enforcing the Blobs base folder path to generate good folder path that works with Erasure Coding I/O.
func NewBtreeWithReplication[TK btree.Ordered, TV any](ctx context.Context, si sop.StoreOptions, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	// Force blob base folder path to be the name of the store so we generate good folder path.
	si.BlobStoreBaseFolderPath = si.Name
	return inredck.NewBtree[TK, TV](ctx, si, t, comparer)
}

// OpenBtree opens an existing B-Tree instance & prepares it for use in a transaction.
func OpenBtree[TK btree.Ordered, TV any](ctx context.Context, name string, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	return inredck.OpenBtree[TK, TV](ctx, name, t, comparer)
}

// RemoveBtree removes B-Tree with a given name from the backend storage. This involves dropping tables
// (registry & node blob) that are permanent action and thus, 'can't get rolled back.
//
// Use with care and only when you are sure to delete the tables.
func RemoveBtree(ctx context.Context, name string, cacheType sop.L2CacheType) error {
	fio := fs.NewFileIO()
	mbsf := fs.NewManageStoreFolder(fio)
	cache := sop.GetL2Cache(sop.TransactionOptions{CacheType: cacheType})
	if cache == nil {
		return fmt.Errorf("unable to get L2 cache for type %v", cacheType)
	}
	sr := cas.NewStoreRepository(mbsf, nil, cache)
	return sr.Remove(ctx, name)
}

// NewStreamingDataStore is a convenience function to easily instantiate a streaming data store that stores
// blobs in File System.
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
	if so.BlobStoreBaseFolderPath == "" {
		return nil, fmt.Errorf("so.BlobStoreBaseFolderPath(\"\") needs to be a valid folder path")
	}
	btree, err := NewBtree[sd.StreamingDataKey[TK], []byte](ctx, so, trans, comparer)
	if err != nil {
		return nil, err
	}
	return &sd.StreamingDataStore[TK]{
		BtreeInterface: btree,
	}, nil
}

// OpenStreamingDataStore opens an existing data store for use in "streaming data".
func OpenStreamingDataStore[TK btree.Ordered](ctx context.Context, name string, trans sop.Transaction, comparer btree.ComparerFunc[sd.StreamingDataKey[TK]]) (*sd.StreamingDataStore[TK], error) {
	btree, err := OpenBtree[sd.StreamingDataKey[TK], []byte](ctx, name, trans, comparer)
	if err != nil {
		return nil, err
	}
	return &sd.StreamingDataStore[TK]{
		BtreeInterface: btree,
	}, nil
}
