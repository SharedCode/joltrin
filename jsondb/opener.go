package jsondb

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/common"
	"github.com/sharedcode/joltrin/v5/database"
)

// StoreAccessor provides a unified interface for accessing both primitive and JSON B-Trees.
type StoreAccessor interface {
	First(ctx context.Context) (bool, error)
	Last(ctx context.Context) (bool, error)
	Next(ctx context.Context) (bool, error)
	Previous(ctx context.Context) (bool, error)
	FindOne(ctx context.Context, key any, first bool) (bool, error)
	FindInDescendingOrder(ctx context.Context, key any) (bool, error)
	GetCurrentKey() any
	GetCurrentValue(ctx context.Context) (any, error)
	GetCurrentValueNoLock(ctx context.Context) (any, error)
	RLockCurrentItem(ctx context.Context) error
	Add(ctx context.Context, key any, value any) (bool, error)
	Update(ctx context.Context, key any, value any) (bool, error)
	Remove(ctx context.Context, key any) (bool, error)
	GetStoreInfo() sop.StoreInfo
}

// OpenStore opens a B-Tree store, automatically detecting if it's a primitive or JSON store.
func OpenStore(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (StoreAccessor, error) {
	var isPrimitiveKey bool

	if t2, ok := tx.GetPhasedTransaction().(*common.Transaction); ok {
		stores, err := t2.StoreRepository.Get(ctx, storeName)
		if err == nil && len(stores) > 0 {
			isPrimitiveKey = stores[0].IsPrimitiveKey
		}
	}

	if isPrimitiveKey {
		store, err := database.OpenBtreeCursor[any, any](ctx, dbOpts, storeName, tx, nil)
		if err != nil {
			return nil, err
		}
		return &primitiveStore{btree: store}, nil
	}

	store, err := OpenJsonBtreeMapKeyCursor(ctx, dbOpts, storeName, tx)
	if err != nil {
		return nil, err
	}
	return &jsonStore{btree: store}, nil
}

// CreateObjectStore creates a new B-Tree store with default generic Key and Value types.
func CreateObjectStore(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (StoreAccessor, error) {
	// For now, default to primitive string key for simplicity or auto-detect?
	// The OpenStore logic detects phase transaction stores.
	// We'll default to primitive string key for Temp Stores usage.
	// Explicitly allow duplicates for spill support
	opts := sop.StoreOptions{
		IsUnique:       false,
		IsPrimitiveKey: true,
	}
	store, err := database.NewBtree[any, any](ctx, dbOpts, storeName, tx, nil, opts)
	if err != nil {
		return nil, err
	}
	return &primitiveStore{btree: store}, nil
}

type primitiveStore struct {
	btree btree.BtreeInterface[any, any]
}

func (s *primitiveStore) First(ctx context.Context) (bool, error) { return s.btree.First(ctx) }
func (s *primitiveStore) Last(ctx context.Context) (bool, error)  { return s.btree.Last(ctx) }
func (s *primitiveStore) Next(ctx context.Context) (bool, error)  { return s.btree.Next(ctx) }
func (s *primitiveStore) Previous(ctx context.Context) (bool, error) {
	return s.btree.Previous(ctx)
}
func (s *primitiveStore) FindOne(ctx context.Context, key any, first bool) (bool, error) {
	return s.btree.Find(ctx, key, first)
}
func (s *primitiveStore) FindInDescendingOrder(ctx context.Context, key any) (bool, error) {
	return s.btree.FindInDescendingOrder(ctx, key)
}
func (s *primitiveStore) GetCurrentKey() any {
	return s.btree.GetCurrentKey().Key
}
func (s *primitiveStore) GetCurrentValue(ctx context.Context) (any, error) {
	return s.btree.GetCurrentValue(ctx)
}
func (s *primitiveStore) GetCurrentValueNoLock(ctx context.Context) (any, error) {
	return s.btree.GetCurrentValueNoLock(ctx)
}
func (s *primitiveStore) RLockCurrentItem(ctx context.Context) error {
	return s.btree.RLockCurrentItem(ctx)
}
func (s *primitiveStore) Add(ctx context.Context, key any, value any) (bool, error) {
	k, ok := key.(string)
	if !ok {
		return false, fmt.Errorf("key must be a string for primitive store")
	}
	return s.btree.Add(ctx, k, value)
}
func (s *primitiveStore) Update(ctx context.Context, key any, value any) (bool, error) {
	k, ok := key.(string)
	if !ok {
		return false, fmt.Errorf("key must be a string for primitive store")
	}
	return s.btree.Update(ctx, k, value)
}
func (s *primitiveStore) Remove(ctx context.Context, key any) (bool, error) {
	k, ok := key.(string)
	if !ok {
		return false, fmt.Errorf("key must be a string for primitive store")
	}
	return s.btree.Remove(ctx, k)
}
func (s *primitiveStore) GetStoreInfo() sop.StoreInfo {
	return s.btree.GetStoreInfo()
}

type jsonStore struct {
	btree *JsonDBMapKey
}

func (s *jsonStore) First(ctx context.Context) (bool, error) { return s.btree.First(ctx) }
func (s *jsonStore) Last(ctx context.Context) (bool, error)  { return s.btree.Last(ctx) }
func (s *jsonStore) Next(ctx context.Context) (bool, error)  { return s.btree.Next(ctx) }
func (s *jsonStore) Previous(ctx context.Context) (bool, error) {
	return s.btree.Previous(ctx)
}
func (s *jsonStore) FindOne(ctx context.Context, key any, first bool) (bool, error) {
	k, ok := key.(map[string]any)
	if !ok {
		return false, fmt.Errorf("key must be a map[string]any for json store")
	}
	return s.btree.Find(ctx, k, first)
}
func (s *jsonStore) FindInDescendingOrder(ctx context.Context, key any) (bool, error) {
	k, ok := key.(map[string]any)
	if !ok {
		return false, fmt.Errorf("key must be a map[string]any for json store")
	}
	return s.btree.FindInDescendingOrder(ctx, k)
}
func (s *jsonStore) GetCurrentKey() any {
	return s.btree.BtreeInterface.GetCurrentKey().Key
}
func (s *jsonStore) GetCurrentValue(ctx context.Context) (any, error) {
	return s.btree.BtreeInterface.GetCurrentValue(ctx)
}
func (s *jsonStore) GetCurrentValueNoLock(ctx context.Context) (any, error) {
	return s.btree.BtreeInterface.GetCurrentValueNoLock(ctx)
}
func (s *jsonStore) RLockCurrentItem(ctx context.Context) error {
	return s.btree.BtreeInterface.RLockCurrentItem(ctx)
}
func (s *jsonStore) Add(ctx context.Context, key any, value any) (bool, error) {
	k, ok := key.(map[string]any)
	if !ok {
		return false, fmt.Errorf("key must be a map[string]any for json store")
	}
	return s.btree.BtreeInterface.Add(ctx, k, value)
}
func (s *jsonStore) Update(ctx context.Context, key any, value any) (bool, error) {
	k, ok := key.(map[string]any)
	if !ok {
		return false, fmt.Errorf("key must be a map[string]any for json store")
	}
	return s.btree.BtreeInterface.Update(ctx, k, value)
}
func (s *jsonStore) Remove(ctx context.Context, key any) (bool, error) {
	k, ok := key.(map[string]any)
	if !ok {
		return false, fmt.Errorf("key must be a map[string]any for json store")
	}
	return s.btree.Remove(ctx, []map[string]any{k})
}
func (s *jsonStore) GetStoreInfo() sop.StoreInfo {
	return s.btree.GetStoreInfo()
}
