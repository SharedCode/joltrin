package agent

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
)

// -- Mock Store --

// MockStore implements jsondb.StoreAccessor using an in-memory slice.
// It is defined in package 'agent' to be available for internal tests.
type MockStore struct {
	Name  string
	Items []MockItem
	// Cursor state
	currentIndex int
	started      bool
}

type MockItem struct {
	Key   any
	Value any
}

// Remove unused time import if it exists
var _ = context.Background

var _ jsondb.StoreAccessor = (*MockStore)(nil)

func NewMockStore(name string, items []MockItem) *MockStore {
	return &MockStore{
		Name:         name,
		Items:        items,
		currentIndex: -1,
	}
}

func (m *MockStore) GetStoreInfo() sop.StoreInfo {
	return sop.StoreInfo{Name: m.Name, Count: int64(len(m.Items))}
}

func (m *MockStore) First(ctx context.Context) (bool, error) {
	if len(m.Items) == 0 {
		return false, nil
	}
	m.currentIndex = 0
	m.started = true
	return true, nil
}

func (m *MockStore) Last(ctx context.Context) (bool, error) {
	if len(m.Items) == 0 {
		return false, nil
	}
	m.currentIndex = len(m.Items) - 1
	m.started = true
	return true, nil
}

func (m *MockStore) Next(ctx context.Context) (bool, error) {
	if !m.started {
		return m.First(ctx)
	}
	if m.currentIndex+1 < len(m.Items) {
		m.currentIndex++
		return true, nil
	}
	m.currentIndex = len(m.Items)
	return false, nil
}

func (m *MockStore) Previous(ctx context.Context) (bool, error) {
	if !m.started {
		return m.Last(ctx)
	}
	if m.currentIndex-1 >= 0 {
		m.currentIndex--
		return true, nil
	}
	m.currentIndex = -1
	return false, nil
}

func (m *MockStore) FindOne(ctx context.Context, key any, first bool) (bool, error) {
	// Simple linear search for mock
	// Real implementation would use Comparer
	keyStr := fmt.Sprintf("%v", key)
	for i, item := range m.Items {
		if fmt.Sprintf("%v", item.Key) == keyStr {
			m.currentIndex = i
			m.started = true
			return true, nil
		}
	}
	return false, nil // Not found
}

func (m *MockStore) FindInDescendingOrder(ctx context.Context, key any) (bool, error) {
	return m.FindOne(ctx, key, true)
}

func (m *MockStore) GetCurrentKey() any {
	if !m.started || m.currentIndex < 0 || m.currentIndex >= len(m.Items) {
		return nil
	}
	return m.Items[m.currentIndex].Key
}

func (m *MockStore) GetCurrentValue(ctx context.Context) (any, error) {
	if !m.started || m.currentIndex < 0 || m.currentIndex >= len(m.Items) {
		return nil, nil
	}
	return m.Items[m.currentIndex].Value, nil
}

func (m *MockStore) GetCurrentValueNoLock(ctx context.Context) (any, error) {
	// Mock doesn't track locks, so this is same as GetCurrentValue
	return m.GetCurrentValue(ctx)
}

func (m *MockStore) RLockCurrentItem(ctx context.Context) error {
	// Mock doesn't track locks, so this is a no-op
	if !m.started || m.currentIndex < 0 || m.currentIndex >= len(m.Items) {
		return fmt.Errorf("no current item to lock")
	}
	return nil
}

func (m *MockStore) Add(ctx context.Context, key any, value any) (bool, error) {
	m.Items = append(m.Items, MockItem{Key: key, Value: value})
	return true, nil
}

func (m *MockStore) Update(ctx context.Context, key any, value any) (bool, error) {
	keyStr := fmt.Sprintf("%v", key)
	for i, item := range m.Items {
		if fmt.Sprintf("%v", item.Key) == keyStr {
			m.Items[i].Value = value
			return true, nil
		}
	}
	return false, fmt.Errorf("item not found for update")
}

func (m *MockStore) Remove(ctx context.Context, key any) (bool, error) {
	keyStr := fmt.Sprintf("%v", key)
	for i, item := range m.Items {
		if fmt.Sprintf("%v", item.Key) == keyStr {
			m.Items = append(m.Items[:i], m.Items[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

// Stubs for interface compliance
func (m *MockStore) RemoveCurrentItem(ctx context.Context) error { return nil }
func (m *MockStore) ItemExists(ctx context.Context, key any) (bool, error) {
	return m.FindOne(ctx, key, true)
}

// -- Mock Transaction --

type ReproMockTransaction struct {
	sop.Transaction
	Stores map[string]jsondb.StoreAccessor
}

func NewReproMockTransaction() *ReproMockTransaction {
	return &ReproMockTransaction{
		Stores: make(map[string]jsondb.StoreAccessor),
	}
}

// Helper to register stores for the transaction to find
func (mx *ReproMockTransaction) RegisterStore(name string, store jsondb.StoreAccessor) {
	mx.Stores[name] = store
}

// We need to support GetStores if execute_script uses it via OpenStore?
// OpenStore typically goes through Database -> Transaction.
