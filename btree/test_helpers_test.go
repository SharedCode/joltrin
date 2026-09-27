package btree

import (
	"context"
	"fmt"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

// helper for building a small tree with known structure for deletion/rotation tests
func buildSmallTree() (*Btree[int, string], *fakeNR[int, string], *Node[int, string]) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true, LeafLoadBalancing: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// create root and insert a few keys to trigger children
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)

	// Add enough items to split root into two children: keys 10..50
	for _, k := range []int{10, 20, 30, 40, 50} {
		v := "v"
		vv := v
		b.AddItem(nil, &Item[int, string]{Key: k, Value: &vv, ID: sop.NewUUID()})
	}
	return b, fnr, root
}

// mockTx is a minimal TwoPhaseCommitTransaction to verify wrapper behavior.
type mockTx struct {
	begun           bool
	mode            sop.TransactionMode
	rollbackCount   int
	lastRollbackErr error
}

func (m *mockTx) Begin(ctx context.Context) error        { m.begun = true; return nil }
func (m *mockTx) Phase1Commit(ctx context.Context) error { return nil }
func (m *mockTx) Phase2Commit(ctx context.Context) error { return nil }
func (m *mockTx) Rollback(ctx context.Context, err error) error {
	m.rollbackCount++
	m.lastRollbackErr = err
	m.begun = false
	return nil
}
func (m *mockTx) HasBegun() bool                                    { return m.begun }
func (m *mockTx) GetMode() sop.TransactionMode                      { return m.mode }
func (m *mockTx) GetStores(ctx context.Context) ([]string, error)   { return nil, nil }
func (m *mockTx) Close() error                                      { return nil }
func (m *mockTx) GetID() sop.UUID                                   { return sop.NewUUID() }
func (m *mockTx) CommitMaxDuration() time.Duration                  { return time.Minute }
func (m *mockTx) OnCommit(callback func(ctx context.Context) error) {}

// iatErr induces an error on Add to exercise rollback path in the wrapper.
type iatErr[TK Ordered, TV any] struct{}

func (iatErr[TK, TV]) Add(ctx context.Context, item *Item[TK, TV]) error {
	return fmt.Errorf("add error")
}
func (iatErr[TK, TV]) Update(ctx context.Context, item *Item[TK, TV]) error { return nil }
func (iatErr[TK, TV]) Remove(ctx context.Context, item *Item[TK, TV]) error { return nil }
func (iatErr[TK, TV]) Get(ctx context.Context, item *Item[TK, TV]) error    { return nil }
