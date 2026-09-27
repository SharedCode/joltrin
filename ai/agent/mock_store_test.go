package agent_test

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
)

// MockStore implements jsondb.StoreAccessor using an in-memory slice.
type MockStore struct {
	Name  string
	Items []MockItem // Keys and Values
	// Cursor state
	currentIndex int
	started      bool
}

type MockItem struct {
	Key   any
	Value any
}

func NewMockStore(name string) *MockStore {
	return &MockStore{
		Name:  name,
		Items: make([]MockItem, 0),
	}
}

// StoreAccessor Methods

func (m *MockStore) GetStoreInfo() sop.StoreInfo {
	return sop.StoreInfo{Name: m.Name}
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
	return false, nil
}

func (m *MockStore) FindOne(ctx context.Context, key any, first bool) (bool, error) {
	// Simple linear search for mock
	for i, item := range m.Items {
		// Compare keys (assuming string or simple map equality for now)
		if fmt.Sprintf("%v", item.Key) == fmt.Sprintf("%v", key) {
			m.currentIndex = i
			m.started = true
			return true, nil
		}
	}
	return false, nil
}

func (m *MockStore) FindInDescendingOrder(ctx context.Context, key any) (bool, error) {
	return m.FindOne(ctx, key, true)
}

func (m *MockStore) GetCurrentKey() any {
	if !m.started || m.currentIndex < 0 || m.currentIndex >= len(m.Items) {
		return nil // Return nil if no current item
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
	return false, fmt.Errorf("not implemented")
}

func (m *MockStore) Remove(ctx context.Context, key any) (bool, error) {
	return false, fmt.Errorf("not implemented")
}

// Interface compliance check
var _ jsondb.StoreAccessor = (*MockStore)(nil)
