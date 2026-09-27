// Package inredck contains SOP implementations that use Redis for caching and Cassandra for backend data storage.
package inredck

import (
	"context"

	cas "github.com/sharedcode/joltrin/adapters/cassandra"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/common"
)

// RemoveBtree removes the B-tree with the given name from backend storage.
// This is destructive and cannot be rolled back.
func RemoveBtree(ctx context.Context, name string, cacheType sop.L2CacheType) error {
	cache := sop.GetL2Cache(sop.TransactionOptions{CacheType: cacheType})
	storeRepository := cas.NewStoreRepository(nil, nil, cache)
	return storeRepository.Remove(ctx, name)
}

// OpenBtree opens an existing B-tree instance and prepares it for use in a transaction.
func OpenBtree[TK btree.Ordered, TV any](ctx context.Context, name string, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	return common.OpenBtree[TK, TV](ctx, name, t, comparer)
}

// NewBtree creates a new B-tree instance with data persisted to backend storage upon commit.
// If the B-tree (by name) is not found, a new one is created; otherwise, the existing one is opened and parameters validated.
func NewBtree[TK btree.Ordered, TV any](ctx context.Context, si sop.StoreOptions, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	return common.NewBtree[TK, TV](ctx, si, t, comparer)
}
