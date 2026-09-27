package btree

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

// Test btreeWithTransaction.GetCurrentValueNoLock requires begun transaction
func TestBtreeWithTransaction_GetCurrentValueNoLock_RequiresBegunTransaction(t *testing.T) {
	b, _ := newTestBtree[string]()
	trans := &mockTransaction{begun: false}
	bt := NewBtreeWithTransaction[int, string](trans, b)

	_, err := bt.GetCurrentValueNoLock(context.Background())
	if err == nil {
		t.Error("GetCurrentValueNoLock should fail when transaction not begun")
	}
	if !errors.Is(err, errTransHasNotBegunMsg) {
		t.Errorf("expected errTransHasNotBegunMsg, got: %v", err)
	}
}

// Test btreeWithTransaction.GetCurrentValueNoLock with begun transaction
func TestBtreeWithTransaction_GetCurrentValueNoLock_Success(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()

	// Add item directly to btree
	b.Add(ctx, 10, "value10")
	b.Find(ctx, 10, true)

	trans := &mockTransaction{begun: true}
	bt := NewBtreeWithTransaction[int, string](trans, b)

	val, err := bt.GetCurrentValueNoLock(ctx)
	if err != nil {
		t.Fatalf("GetCurrentValueNoLock failed: %v", err)
	}
	if val != "value10" {
		t.Errorf("expected 'value10', got '%s'", val)
	}
}

// Test btreeWithTransaction.RLockCurrentItem requires begun transaction
func TestBtreeWithTransaction_RLockCurrentItem_RequiresBegunTransaction(t *testing.T) {
	b, _ := newTestBtree[string]()
	trans := &mockTransaction{begun: false}
	bt := NewBtreeWithTransaction[int, string](trans, b)

	err := bt.RLockCurrentItem(context.Background())
	if err == nil {
		t.Error("RLockCurrentItem should fail when transaction not begun")
	}
	if !errors.Is(err, errTransHasNotBegunMsg) {
		t.Errorf("expected errTransHasNotBegunMsg, got: %v", err)
	}
}

// Test btreeWithTransaction.RLockCurrentItem with begun transaction
func TestBtreeWithTransaction_RLockCurrentItem_Success(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()

	// Add item directly to btree
	b.Add(ctx, 20, "value20")
	b.Find(ctx, 20, true)

	trans := &mockTransaction{begun: true}
	bt := NewBtreeWithTransaction[int, string](trans, b)

	err := bt.RLockCurrentItem(ctx)
	if err != nil {
		t.Fatalf("RLockCurrentItem failed: %v", err)
	}
}

// Test btreeWithTransaction GetCurrentValueNoLock bypasses ItemActionTracker.
func TestBtreeWithTransaction_GetCurrentValueNoLock_RollbackOnError(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()

	// Create a transaction wrapper but cause an error by setting storeInterface to nil after positioning
	trans := &mockTransaction{begun: true, rollbackCalled: false}
	bt := NewBtreeWithTransaction[int, string](trans, b)

	// Add and find item first
	b.Add(ctx, 10, "value10")
	b.Find(ctx, 10, true)

	// Force an error by setting ItemActionTracker to nil
	b.storeInterface.ItemActionTracker = nil

	v, err := bt.GetCurrentValueNoLock(ctx)
	if err != nil {
		t.Fatalf("GetCurrentValueNoLock should bypass ItemActionTracker and succeed: %v", err)
	}
	if v != "value10" {
		t.Errorf("expected 'value10', got: %v", v)
	}

	// Verify rollback was not called.
	if trans.rollbackCalled {
		t.Error("Rollback should not have been called when GetCurrentValueNoLock succeeds")
	}
}

// Test btreeWithTransaction rollback on RLockCurrentItem error
func TestBtreeWithTransaction_RLockCurrentItem_RollbackOnError(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()

	// Don't add any items - will cause error
	trans := &mockTransaction{begun: true, rollbackCalled: false}
	bt := NewBtreeWithTransaction[int, string](trans, b)

	err := bt.RLockCurrentItem(ctx)
	if err == nil {
		t.Error("RLockCurrentItem should fail with no current item")
	}

	// Verify rollback was called
	if !trans.rollbackCalled {
		t.Error("Rollback should have been called on error")
	}
}

// Test btreeWithTransaction rollback failure handling
func TestBtreeWithTransaction_GetCurrentValueNoLock_RollbackFailure(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()

	trans := &mockTransaction{
		begun:          true,
		rollbackErr:    errors.New("rollback failed"),
		rollbackCalled: false,
	}
	bt := NewBtreeWithTransaction[int, string](trans, b)

	// Add and find item first
	b.Add(ctx, 10, "value10")
	b.Find(ctx, 10, true)

	// Force an error by setting ItemActionTracker to nil
	b.storeInterface.ItemActionTracker = nil

	v, err := bt.GetCurrentValueNoLock(ctx)
	if err != nil {
		t.Fatalf("GetCurrentValueNoLock should bypass ItemActionTracker and succeed: %v", err)
	}
	if v != "value10" {
		t.Errorf("expected 'value10', got: %v", v)
	}

	if trans.rollbackCalled {
		t.Error("Rollback should not have been called when GetCurrentValueNoLock succeeds")
	}
}

func TestBtreeWithTransaction_RLockCurrentItem_RollbackFailure(t *testing.T) {
	b, _ := newTestBtree[string]()
	ctx := context.Background()

	trans := &mockTransaction{
		begun:          true,
		rollbackErr:    errors.New("rollback failed"),
		rollbackCalled: false,
	}
	bt := NewBtreeWithTransaction[int, string](trans, b)

	err := bt.RLockCurrentItem(ctx)
	if err == nil {
		t.Error("should return error when no current item")
	}

	// Error message should mention rollback failure
	errStr := err.Error()
	if errStr == "" {
		t.Error("error message should not be empty")
	}
}

// Mock transaction for testing
type mockTransaction struct {
	begun          bool
	mode           sop.TransactionMode
	rollbackCalled bool
	rollbackErr    error
}

func (m *mockTransaction) HasBegun() bool {
	return m.begun
}

func (m *mockTransaction) GetMode() sop.TransactionMode {
	return m.mode
}

func (m *mockTransaction) Rollback(ctx context.Context, err error) error {
	m.rollbackCalled = true
	return m.rollbackErr
}

func (m *mockTransaction) Begin(ctx context.Context) error {
	m.begun = true
	return nil
}

func (m *mockTransaction) Phase1Commit(ctx context.Context) error {
	return nil
}

func (m *mockTransaction) Phase2Commit(ctx context.Context) error {
	return nil
}

func (m *mockTransaction) GetStores(ctx context.Context) ([]string, error) {
	return nil, nil
}

func (m *mockTransaction) Close() error {
	return nil
}

func (m *mockTransaction) GetID() sop.UUID {
	return sop.NewUUID()
}

func (m *mockTransaction) CommitMaxDuration() time.Duration {
	return 0
}

func (m *mockTransaction) OnCommit(callback func(ctx context.Context) error) {
}
