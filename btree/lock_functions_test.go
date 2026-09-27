package btree

import (
	"context"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// Test GetCurrentValueNoLock returns value without calling NodeRepository.Fetched
func TestGetCurrentValueNoLock_DoesNotRegisterReadLockHint(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &trackingNR[int, string]{
		n:             map[sop.UUID]*Node[int, string]{},
		fetchedCalled: false,
	}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}

	b, err := New[int, string](store, &si, nil)
	if err != nil {
		t.Fatalf("new btree: %v", err)
	}

	// Add item
	if ok, err := b.Add(context.Background(), 10, "value10"); !ok || err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Find and position cursor
	if ok, err := b.Find(context.Background(), 10, true); !ok || err != nil {
		t.Fatalf("Find failed: %v", err)
	}

	// Reset tracking flag
	fnr.fetchedCalled = false

	// Call GetCurrentValueNoLock - should NOT call NodeRepository.Fetched
	val, err := b.GetCurrentValueNoLock(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentValueNoLock failed: %v", err)
	}
	if val != "value10" {
		t.Errorf("expected 'value10', got '%s'", val)
	}

	// Verify NodeRepository.Fetched was NOT called
	if fnr.fetchedCalled {
		t.Error("GetCurrentValueNoLock should not call NodeRepository.Fetched (no lock hint)")
	}
}

// Test GetCurrentValue DOES call NodeRepository.Fetched for lock hint
func TestGetCurrentValue_RegistersReadLockHint(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &trackingNR[int, string]{
		n:             map[sop.UUID]*Node[int, string]{},
		fetchedCalled: false,
	}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}

	b, err := New[int, string](store, &si, nil)
	if err != nil {
		t.Fatalf("new btree: %v", err)
	}

	// Add item
	if ok, err := b.Add(context.Background(), 10, "value10"); !ok || err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Find and position cursor
	if ok, err := b.Find(context.Background(), 10, true); !ok || err != nil {
		t.Fatalf("Find failed: %v", err)
	}

	// Reset tracking flag
	fnr.fetchedCalled = false

	// Call GetCurrentValue - SHOULD call NodeRepository.Fetched
	val, err := b.GetCurrentValue(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentValue failed: %v", err)
	}
	if val != "value10" {
		t.Errorf("expected 'value10', got '%s'", val)
	}

	// Verify NodeRepository.Fetched WAS called
	if !fnr.fetchedCalled {
		t.Error("GetCurrentValue should call NodeRepository.Fetched (lock hint)")
	}
}

// Test RLockCurrentItem registers read lock without returning value
func TestRLockCurrentItem_RegistersLock(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &trackingNR[int, string]{
		n:             map[sop.UUID]*Node[int, string]{},
		fetchedCalled: false,
	}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}

	b, err := New[int, string](store, &si, nil)
	if err != nil {
		t.Fatalf("new btree: %v", err)
	}

	// Add item
	if ok, err := b.Add(context.Background(), 10, "value10"); !ok || err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// Find and position cursor
	if ok, err := b.Find(context.Background(), 10, true); !ok || err != nil {
		t.Fatalf("Find failed: %v", err)
	}

	// Reset tracking flag
	fnr.fetchedCalled = false

	// Call RLockCurrentItem - should call NodeRepository.Fetched for lock
	err = b.RLockCurrentItem(context.Background())
	if err != nil {
		t.Fatalf("RLockCurrentItem failed: %v", err)
	}

	// Verify NodeRepository.Fetched WAS called
	if !fnr.fetchedCalled {
		t.Error("RLockCurrentItem should call NodeRepository.Fetched (lock hint)")
	}
}

// Test RLockCurrentItem with no current item
func TestRLockCurrentItem_NoCurrentItem(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &trackingNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}

	b, err := New[int, string](store, &si, nil)
	if err != nil {
		t.Fatalf("new btree: %v", err)
	}

	// Don't position cursor - no current item
	err = b.RLockCurrentItem(context.Background())
	if err == nil {
		t.Error("RLockCurrentItem should fail when no current item")
	}
}

// Test GetCurrentValueNoLock with no current item
func TestGetCurrentValueNoLock_NoCurrentItem(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &trackingNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}

	b, err := New[int, string](store, &si, nil)
	if err != nil {
		t.Fatalf("new btree: %v", err)
	}

	// Don't position cursor - no current item
	val, err := b.GetCurrentValueNoLock(context.Background())
	if err != nil {
		t.Errorf("GetCurrentValueNoLock should succeed with zero value when no current item, got error: %v", err)
	}
	// Should return zero value (empty string)
	if val != "" {
		t.Errorf("expected empty string, got: %s", val)
	}
}

// Test with nil storeInterface
func TestRLockCurrentItem_NilStoreInterface(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &trackingNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}

	b, err := New[int, string](store, &si, nil)
	if err != nil {
		t.Fatalf("new btree: %v", err)
	}

	// Add and find item
	b.Add(context.Background(), 10, "value10")
	b.Find(context.Background(), 10, true)

	// Manually set storeInterface to nil
	b.storeInterface = nil

	err = b.RLockCurrentItem(context.Background())
	if err == nil || err.Error() != "storeInterface is nil" {
		t.Errorf("expected 'storeInterface is nil' error, got: %v", err)
	}
}

// Test GetCurrentValueNoLock with nil ItemActionTracker.
// NoLock read should bypass ItemActionTracker.
func TestGetCurrentValueNoLock_NilItemActionTracker(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &trackingNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}

	b, err := New[int, string](store, &si, nil)
	if err != nil {
		t.Fatalf("new btree: %v", err)
	}

	// Add and find item
	b.Add(context.Background(), 10, "value10")
	b.Find(context.Background(), 10, true)

	// Manually set ItemActionTracker to nil
	b.storeInterface.ItemActionTracker = nil

	v, err := b.GetCurrentValueNoLock(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentValueNoLock should bypass ItemActionTracker, got: %v", err)
	}
	if v != "value10" {
		t.Errorf("expected 'value10', got: %v", v)
	}
}

// Test Cursor wrappers
func TestCursor_GetCurrentValueNoLock(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()
	c := NewCursor(b)

	// Add items
	c.Add(ctx, 1, "one")
	c.Add(ctx, 2, "two")

	// Find and use GetCurrentValueNoLock
	if ok, _ := c.Find(ctx, 1, true); !ok {
		t.Fatal("Find failed")
	}

	val, err := c.GetCurrentValueNoLock(ctx)
	if err != nil {
		t.Fatalf("GetCurrentValueNoLock failed: %v", err)
	}
	if val != "one" {
		t.Errorf("expected 'one', got '%s'", val)
	}
}

func TestCursor_RLockCurrentItem(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()
	c := NewCursor(b)

	// Add items
	c.Add(ctx, 1, "one")
	c.Add(ctx, 2, "two")

	// Find and use RLockCurrentItem
	if ok, _ := c.Find(ctx, 2, true); !ok {
		t.Fatal("Find failed")
	}

	err := c.RLockCurrentItem(ctx)
	if err != nil {
		t.Fatalf("RLockCurrentItem failed: %v", err)
	}
}

// Helper: NodeRepository that tracks whether Fetched was called
type trackingNR[TK Ordered, TV any] struct {
	n             map[sop.UUID]*Node[TK, TV]
	fetchedCalled bool
}

func (t *trackingNR[TK, TV]) Add(node *Node[TK, TV]) {
	t.n[node.ID] = node
}

func (t *trackingNR[TK, TV]) Update(node *Node[TK, TV]) {
	t.n[node.ID] = node
}

func (t *trackingNR[TK, TV]) Remove(id sop.UUID) {
	delete(t.n, id)
}

func (t *trackingNR[TK, TV]) Fetched(id sop.UUID) {
	t.fetchedCalled = true
}

func (t *trackingNR[TK, TV]) Get(ctx context.Context, id sop.UUID) (*Node[TK, TV], error) {
	return t.n[id], nil
}

// Test ItemActionTracker.Get error handling.
// NoLock read should bypass ItemActionTracker.Get errors.
func TestGetCurrentValueNoLock_ItemActionTrackerError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &trackingNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	tracker := &errorIAT[int, string]{}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: tracker}

	b, err := New[int, string](store, &si, nil)
	if err != nil {
		t.Fatalf("new btree: %v", err)
	}

	// Add item
	b.Add(context.Background(), 10, "value10")
	b.Find(context.Background(), 10, true)

	// GetCurrentValueNoLock should bypass ItemActionTracker.Get and succeed.
	v, err := b.GetCurrentValueNoLock(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentValueNoLock should bypass tracker.Get errors, got: %v", err)
	}
	if v != "value10" {
		t.Errorf("expected 'value10', got: %v", v)
	}
}

func TestRLockCurrentItem_ItemActionTrackerError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &trackingNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	tracker := &errorIAT[int, string]{}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: tracker}

	b, err := New[int, string](store, &si, nil)
	if err != nil {
		t.Fatalf("new btree: %v", err)
	}

	// Add item
	b.Add(context.Background(), 10, "value10")
	b.Find(context.Background(), 10, true)

	// RLockCurrentItem now calls ItemActionTracker.Get(), so tracker errors are surfaced.
	err = b.RLockCurrentItem(context.Background())
	if err == nil || err.Error() != "tracker error" {
		t.Errorf("expected 'tracker error', got: %v", err)
	}
}

// Helper: ItemActionTracker that returns errors
type errorIAT[TK Ordered, TV any] struct{}

func (errorIAT[TK, TV]) Add(ctx context.Context, item *Item[TK, TV]) error {
	return nil
}

func (errorIAT[TK, TV]) Update(ctx context.Context, item *Item[TK, TV]) error {
	return nil
}

func (errorIAT[TK, TV]) Remove(ctx context.Context, item *Item[TK, TV]) error {
	return nil
}

func (errorIAT[TK, TV]) Get(ctx context.Context, item *Item[TK, TV]) error {
	return fmt.Errorf("tracker error")
}
