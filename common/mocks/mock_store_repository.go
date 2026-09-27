package mocks

import (
	"context"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

// mockStoreRepository is a simple in-memory implementation of store repository to demonstrate
// or mockup the structure composition, so we can define it in preparation of v2.
type mockStoreRepository struct {
	lookup map[string]sop.StoreInfo
}

// NewMockStoreRepository manages the StoreInfo in Cassandra table.
func NewMockStoreRepository() sop.StoreRepository {
	return &mockStoreRepository{
		lookup: make(map[string]sop.StoreInfo),
	}
}

func (sr *mockStoreRepository) Add(ctx context.Context, stores ...sop.StoreInfo) error {
	for _, store := range stores {
		sr.lookup[store.Name] = store
	}
	return nil
}

func (sr *mockStoreRepository) Update(ctx context.Context, stores []sop.StoreInfo) ([]sop.StoreInfo, error) {
	for _, store := range stores {
		cs := sr.lookup[store.Name]
		// Merge or apply the "count delta".
		store.Count = cs.Count + store.CountDelta
		store.CountDelta = 0
		sr.lookup[store.Name] = store
	}
	return nil, nil
}
func (sr *mockStoreRepository) Get(ctx context.Context, names ...string) ([]sop.StoreInfo, error) {
	return sr.GetWithTTL(ctx, false, 0, names...)
}
func (sr *mockStoreRepository) GetAll(ctx context.Context) ([]string, error) {
	storeNames := make([]string, len(sr.lookup))
	var i = 0
	for k := range sr.lookup {
		storeNames[i] = k
		i++
	}
	return storeNames, nil
}

func (sr *mockStoreRepository) GetWithTTL(ctx context.Context, isCacheTTL bool, cacheDuration time.Duration, names ...string) ([]sop.StoreInfo, error) {
	stores := make([]sop.StoreInfo, len(names))
	for i, name := range names {
		v := sr.lookup[name]
		stores[i] = v
	}
	return stores, nil
}

func (sr *mockStoreRepository) Remove(ctx context.Context, names ...string) error {
	for _, name := range names {
		delete(sr.lookup, name)
	}
	return nil
}

// Mocks do not replicate.
func (sr *mockStoreRepository) Replicate(ctx context.Context, storesInfo []sop.StoreInfo) error {
	return nil
}

func (sr *mockStoreRepository) ProcessNewer(ctx context.Context, processor func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error) error {
	return nil
}
