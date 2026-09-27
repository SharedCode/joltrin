package main

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// MockTransaction is a dummy implementation of sop.Transaction for testing.
type MockTransaction struct{}

func (m *MockTransaction) Begin(ctx interface{}) error    { return nil }
func (m *MockTransaction) Commit(ctx interface{}) error   { return nil }
func (m *MockTransaction) Rollback(ctx interface{}) error { return nil }

// Add other methods if required by the interface, but for registry storage, we might not need them.
// Since we are just storing it, we can cast nil to sop.Transaction or use a simple struct if the interface is large.
// Actually, let's just use nil for the transaction object in tests as the registry doesn't invoke methods.

func TestTransRegistry_Add_Get(t *testing.T) {
	tr := newTransactionRegistry()
	var trans sop.Transaction = nil // Using nil as we just test storage

	id := tr.Add(trans)
	if id.IsNil() {
		t.Error("Expected valid UUID, got nil")
	}

	retrieved, ok := tr.Get(id)
	if !ok {
		t.Error("Expected transaction to be found")
	}
	if retrieved != trans {
		t.Error("Expected retrieved transaction to match")
	}
}

func TestTransRegistry_Remove(t *testing.T) {
	tr := newTransactionRegistry()
	var trans sop.Transaction = nil
	id := tr.Add(trans)

	tr.Remove(id)

	_, ok := tr.Get(id)
	if ok {
		t.Error("Expected transaction to be removed")
	}
}

func TestTransRegistry_Btree_Operations(t *testing.T) {
	tr := newTransactionRegistry()
	var trans sop.Transaction = nil
	transID := tr.Add(trans)

	btree := "dummy_btree" // Using string as btree is 'any'

	btreeID, err := tr.AddBtree(transID, btree)
	if err != nil {
		t.Fatalf("AddBtree failed: %v", err)
	}
	if btreeID.IsNil() {
		t.Error("Expected valid Btree UUID")
	}

	retrievedBtree, ok := tr.GetBtree(transID, btreeID)
	if !ok {
		t.Error("Expected Btree to be found")
	}
	if retrievedBtree != btree {
		t.Errorf("Expected %v, got %v", btree, retrievedBtree)
	}

	tr.RemoveBtree(transID, btreeID)
	_, ok = tr.GetBtree(transID, btreeID)
	if ok {
		t.Error("Expected Btree to be removed")
	}
}

func TestTransRegistry_AddBtree_InvalidTransaction(t *testing.T) {
	tr := newTransactionRegistry()
	invalidID := sop.NewUUID()

	_, err := tr.AddBtree(invalidID, "btree")
	// The current implementation returns (nilUUID, nil) if transaction not found.
	// Let's verify that behavior.
	if err != nil {
		t.Error("Expected no error for invalid transaction, just failure to add")
	}
}

func TestTransRegistry_GetBtree_Invalid(t *testing.T) {
	tr := newTransactionRegistry()
	transID := tr.Add(nil)

	// Invalid Btree ID
	_, ok := tr.GetBtree(transID, sop.NewUUID())
	if ok {
		t.Error("Expected false for invalid Btree ID")
	}

	// Invalid Transaction ID
	_, ok = tr.GetBtree(sop.NewUUID(), sop.NewUUID())
	if ok {
		t.Error("Expected false for invalid Transaction ID")
	}
}
