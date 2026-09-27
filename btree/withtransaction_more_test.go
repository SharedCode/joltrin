package btree

import (
	"context"
	"errors"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// fakeNRWithErr extends fakeNR to optionally return an error on Get.
type fakeNRWithErr[TK Ordered, TV any] struct {
	fakeNR[TK, TV]
	errOnGet bool
}

func (f *fakeNRWithErr[TK, TV]) Get(ctx context.Context, id sop.UUID) (*Node[TK, TV], error) {
	if f.errOnGet {
		return nil, errors.New("nr get error")
	}
	return f.fakeNR.Get(ctx, id)
}

// iatUpdateErr triggers error on Update to exercise wrapper rollback for Update.
type iatUpdateErr[TK Ordered, TV any] struct{}

func (iatUpdateErr[TK, TV]) Add(ctx context.Context, item *Item[TK, TV]) error    { return nil }
func (iatUpdateErr[TK, TV]) Get(ctx context.Context, item *Item[TK, TV]) error    { return nil }
func (iatUpdateErr[TK, TV]) Remove(ctx context.Context, item *Item[TK, TV]) error { return nil }
func (iatUpdateErr[TK, TV]) Update(ctx context.Context, item *Item[TK, TV]) error {
	return errors.New("update error")
}

// iatRemoveErr triggers error on Remove to exercise wrapper rollback for Remove/RemoveCurrentItem.
type iatRemoveErr[TK Ordered, TV any] struct{}

func (iatRemoveErr[TK, TV]) Add(ctx context.Context, item *Item[TK, TV]) error    { return nil }
func (iatRemoveErr[TK, TV]) Get(ctx context.Context, item *Item[TK, TV]) error    { return nil }
func (iatRemoveErr[TK, TV]) Update(ctx context.Context, item *Item[TK, TV]) error { return nil }
func (iatRemoveErr[TK, TV]) Remove(ctx context.Context, item *Item[TK, TV]) error {
	return errors.New("remove error")
}

func TestWithTransaction_WritePreconditions(t *testing.T) {
	// Not begun: should return errTransHasNotBegunMsg and not call Rollback.
	tx := &mockTx{begun: false, mode: sop.ForReading}
	// Underlying btree is not needed for precondition, can be nil.
	var underlying BtreeInterface[int, string]
	w := NewBtreeWithTransaction[int, string](tx, underlying)
	if _, err := w.Add(context.Background(), 1, "a"); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("Add not-begun: %v", err)
	}
	if tx.rollbackCount != 0 {
		t.Fatalf("Add not-begun should not rollback")
	}
	if _, err := w.AddIfNotExist(context.Background(), 1, "a"); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("AddIfNotExist not-begun: %v", err)
	}
	if _, err := w.Upsert(context.Background(), 1, "a"); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("Upsert not-begun: %v", err)
	}
	if _, err := w.Update(context.Background(), 1, "b"); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("Update not-begun: %v", err)
	}
	if _, err := w.Remove(context.Background(), 1); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("Remove not-begun: %v", err)
	}
	if _, err := w.RemoveCurrentItem(context.Background()); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("RemoveCurrentItem not-begun: %v", err)
	}

	// Read ops: not-begun must call Rollback and return error
	if _, err := w.Find(context.Background(), 1, true); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("Find not-begun: %v", err)
	}
	if _, err := w.FindWithID(context.Background(), 1, sop.NilUUID); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("FindWithID not-begun: %v", err)
	}
	if _, err := w.GetCurrentValue(context.Background()); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("GetCurrentValue not-begun: %v", err)
	}
	if _, err := w.GetCurrentItem(context.Background()); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("GetCurrentItem not-begun: %v", err)
	}
	if _, err := w.First(context.Background()); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("First not-begun: %v", err)
	}
	if _, err := w.Last(context.Background()); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("Last not-begun: %v", err)
	}
	if _, err := w.Next(context.Background()); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("Next not-begun: %v", err)
	}
	if _, err := w.Previous(context.Background()); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("Previous not-begun: %v", err)
	}
	if tx.rollbackCount == 0 {
		t.Fatalf("read ops not-begun should rollback")
	}
}

func TestWithTransaction_NonWriterRollbackAndDelegatedErrors(t *testing.T) {
	// Build a real underlying btree.
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	// Prepare root
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)

	// Non-writer paths should rollback; re-begin between calls to simulate client retry
	tx := &mockTx{begun: true, mode: sop.ForReading}
	w := NewBtreeWithTransaction[int, string](tx, b)
	calls := []func() error{
		func() error { tx.begun = true; _, err := w.Add(context.Background(), 1, "a"); return err },
		func() error { tx.begun = true; _, err := w.AddIfNotExist(context.Background(), 2, "a"); return err },
		func() error { tx.begun = true; _, err := w.Update(context.Background(), 1, "b"); return err },
		func() error { tx.begun = true; _, err := w.Upsert(context.Background(), 1, "c"); return err },
		func() error { tx.begun = true; _, err := w.Remove(context.Background(), 1); return err },
		func() error { tx.begun = true; _, err := w.RemoveCurrentItem(context.Background()); return err },
	}
	for i, f := range calls {
		if err := f(); err == nil {
			t.Fatalf("expected error on non-writer call %d", i)
		}
	}
	if tx.rollbackCount != len(calls) {
		t.Fatalf("expected %d rollbacks for non-writer writes, got %d", len(calls), tx.rollbackCount)
	}

	// Delegated error on Add: use ItemActionTracker that errors on Add
	tx2 := &mockTx{begun: true, mode: sop.ForWriting}
	fnr2 := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si2 := StoreInterface[int, string]{NodeRepository: fnr2, ItemActionTracker: iatErr[int, string]{}}
	b2, _ := New[int, string](store, &si2, nil)
	root2 := newNode[int, string](b2.getSlotLength())
	root2.newID(sop.NilUUID)
	b2.StoreInfo.RootNodeID = root2.ID
	fnr2.Add(root2)
	w2 := NewBtreeWithTransaction[int, string](tx2, b2)
	if _, err := w2.Add(context.Background(), 1, "x"); err == nil {
		t.Fatalf("expected delegated Add error")
	}
	if tx2.rollbackCount != 1 {
		t.Fatalf("expected rollback on delegated Add error")
	}

	// Delegated error on AddIfNotExist (uses underlying Add) -> rollback
	tx2b := &mockTx{begun: true, mode: sop.ForWriting}
	fnr2b := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si2b := StoreInterface[int, string]{NodeRepository: fnr2b, ItemActionTracker: iatErr[int, string]{}}
	b2b, _ := New[int, string](store, &si2b, nil)
	root2b := newNode[int, string](b2b.getSlotLength())
	root2b.newID(sop.NilUUID)
	b2b.StoreInfo.RootNodeID = root2b.ID
	fnr2b.Add(root2b)
	w2b := NewBtreeWithTransaction[int, string](tx2b, b2b)
	if _, err := w2b.AddIfNotExist(context.Background(), 2, "y"); err == nil {
		t.Fatalf("expected delegated AddIfNotExist error")
	}
	if tx2b.rollbackCount != 1 {
		t.Fatalf("expected rollback on delegated AddIfNotExist error")
	}

	// Delegated error on Update: simulate by making Update path hit ItemActionTracker.Update error
	tx3 := &mockTx{begun: true, mode: sop.ForWriting}
	fnr3 := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si3 := StoreInterface[int, string]{NodeRepository: fnr3, ItemActionTracker: iatUpdateErr[int, string]{}}
	b3, _ := New[int, string](store, &si3, nil)
	root3 := newNode[int, string](b3.getSlotLength())
	root3.newID(sop.NilUUID)
	b3.StoreInfo.RootNodeID = root3.ID
	fnr3.Add(root3)
	// Seed an item to update
	if ok, err := b3.Add(context.Background(), 10, "v"); !ok || err != nil {
		t.Fatalf("seed add: %v", err)
	}
	w3 := NewBtreeWithTransaction[int, string](tx3, b3)
	if _, err := w3.Update(context.Background(), 10, "v2"); err == nil {
		t.Fatalf("expected delegated Update error")
	}
	if tx3.rollbackCount != 1 {
		t.Fatalf("expected rollback on delegated Update error")
	}

	// Delegated error on Remove via NodeRepository.Get during Find
	tx4 := &mockTx{begun: true, mode: sop.ForWriting}
	fnr4 := &fakeNRWithErr[int, string]{fakeNR: fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}, errOnGet: true}
	si4 := StoreInterface[int, string]{NodeRepository: fnr4, ItemActionTracker: fakeIAT[int, string]{}}
	b4, _ := New[int, string](store, &si4, nil)
	// fabricate a non-empty tree state to force Find to call into NodeRepository.Get
	b4.StoreInfo.Count = 1
	b4.StoreInfo.RootNodeID = sop.NewUUID()
	w4 := NewBtreeWithTransaction[int, string](tx4, b4)
	if _, err := w4.Remove(context.Background(), 999); err == nil {
		t.Fatalf("expected delegated Remove error from Find")
	}
	if tx4.rollbackCount != 1 {
		t.Fatalf("expected rollback on delegated Remove error")
	}

	// Delegated error on RemoveCurrentItem via NodeRepository.Get
	tx5 := &mockTx{begun: true, mode: sop.ForWriting}
	fnr5 := &fakeNRWithErr[int, string]{fakeNR: fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}, errOnGet: true}
	si5 := StoreInterface[int, string]{NodeRepository: fnr5, ItemActionTracker: iatRemoveErr[int, string]{}}
	b5, _ := New[int, string](store, &si5, nil)
	// fabricate selection so RemoveCurrentItem will try to load node and hit error
	b5.currentItemRef = currentItemRef{nodeID: sop.NewUUID(), nodeItemIndex: 0}
	w5 := NewBtreeWithTransaction[int, string](tx5, b5)
	if _, err := w5.RemoveCurrentItem(context.Background()); err == nil {
		t.Fatalf("expected delegated RemoveCurrentItem error")
	}
	if tx5.rollbackCount != 1 {
		t.Fatalf("expected rollback on delegated RemoveCurrentItem error")
	}
}

func TestWithTransaction_ReadDelegatedErrorsAndGetCurrentKey(t *testing.T) {
	// Setup underlying btree that will error on Get root
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNRWithErr[int, string]{fakeNR: fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}, errOnGet: true}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	b.StoreInfo.Count = 1
	b.StoreInfo.RootNodeID = sop.NewUUID()

	tx := &mockTx{begun: true, mode: sop.ForReading}
	w := NewBtreeWithTransaction[int, string](tx, b)
	if _, err := w.Find(context.Background(), 5, true); err == nil {
		t.Fatalf("expected delegated Find error")
	}
	if tx.rollbackCount != 1 {
		t.Fatalf("expected rollback on Find error")
	}

	// GetCurrentKey when tx not begun returns zero value.
	tx2 := &mockTx{begun: false, mode: sop.ForReading}
	w2 := NewBtreeWithTransaction[int, string](tx2, b)
	it := w2.GetCurrentKey()
	if it.ID != sop.NilUUID || it.Key != 0 {
		t.Fatalf("expected zero current key when tx not begun")
	}

	// Error on GetCurrentItem path should rollback
	tx3 := &mockTx{begun: true, mode: sop.ForReading}
	w3 := NewBtreeWithTransaction[int, string](tx3, b)
	// fabricate selection to force getCurrentItem to fetch from NR and error
	b.currentItemRef = currentItemRef{nodeID: sop.NewUUID(), nodeItemIndex: 0}
	if _, err := w3.GetCurrentItem(context.Background()); err == nil {
		t.Fatalf("expected delegated GetCurrentItem error")
	}
	if tx3.rollbackCount != 1 {
		t.Fatalf("expected rollback on GetCurrentItem error")
	}
}

func TestWithTransaction_FindWithID_DelegatedError(t *testing.T) {
	// Underlying btree that will error on Get root during FindWithID
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNRWithErr[int, string]{fakeNR: fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}, errOnGet: true}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	b.StoreInfo.Count = 1
	b.StoreInfo.RootNodeID = sop.NewUUID()

	tx := &mockTx{begun: true, mode: sop.ForReading}
	w := NewBtreeWithTransaction[int, string](tx, b)
	if _, err := w.FindWithID(context.Background(), 5, sop.NewUUID()); err == nil {
		t.Fatalf("expected delegated FindWithID error")
	}
	if tx.rollbackCount != 1 {
		t.Fatalf("expected rollback on FindWithID error")
	}
}

func TestWithTransaction_GetCurrentValue_DelegatedError(t *testing.T) {
	// Underlying btree errors on Get for selected node
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNRWithErr[int, string]{fakeNR: fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}, errOnGet: true}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	b.currentItemRef = currentItemRef{nodeID: sop.NewUUID(), nodeItemIndex: 0}

	tx := &mockTx{begun: true, mode: sop.ForReading}
	w := NewBtreeWithTransaction[int, string](tx, b)
	if _, err := w.GetCurrentValue(context.Background()); err == nil {
		t.Fatalf("expected delegated GetCurrentValue error")
	}
	if tx.rollbackCount != 1 {
		t.Fatalf("expected rollback on GetCurrentValue error")
	}
}

func TestWithTransaction_FirstLastNextPrevious_DelegatedErrors(t *testing.T) {
	// Repo errors on Get so underlying btree methods fail
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNRWithErr[int, string]{fakeNR: fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}, errOnGet: true}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	b.StoreInfo.Count = 1
	b.StoreInfo.RootNodeID = sop.NewUUID()
	// Also fabricate a selection for Next/Previous that will error
	b.currentItemRef = currentItemRef{nodeID: sop.NewUUID(), nodeItemIndex: 0}

	tx := &mockTx{begun: true, mode: sop.ForReading}
	w := NewBtreeWithTransaction[int, string](tx, b)

	// First
	if _, err := w.First(context.Background()); err == nil {
		t.Fatalf("expected First error")
	}
	// Last
	tx.begun = true
	if _, err := w.Last(context.Background()); err == nil {
		t.Fatalf("expected Last error")
	}
	// Next
	tx.begun = true
	if _, err := w.Next(context.Background()); err == nil {
		t.Fatalf("expected Next error")
	}
	// Previous
	tx.begun = true
	if _, err := w.Previous(context.Background()); err == nil {
		t.Fatalf("expected Previous error")
	}

	if tx.rollbackCount != 4 {
		t.Fatalf("expected 4 rollbacks for First/Last/Next/Previous errors, got %d", tx.rollbackCount)
	}
}

func TestWithTransaction_UpdateCurrentValue_ModesAndError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	// Base tree to select an item
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	root.Slots[0] = Item[int, string]{Key: 1, Value: &v, ID: sop.NewUUID()}
	root.Count = 1
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)
	b.setCurrentItemID(root.ID, 0)

	// Not begun
	tx1 := &mockTx{begun: false, mode: sop.ForWriting}
	w1 := NewBtreeWithTransaction[int, string](tx1, b)
	if _, err := w1.UpdateCurrentValue(context.Background(), "x"); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("not-begun expected err, got %v", err)
	}

	// Non-writer
	tx2 := &mockTx{begun: true, mode: sop.ForReading}
	w2 := NewBtreeWithTransaction[int, string](tx2, b)
	if _, err := w2.UpdateCurrentValue(context.Background(), "x"); err == nil {
		t.Fatalf("expected error on non-writer UpdateCurrentValue")
	}
	if tx2.rollbackCount != 1 {
		t.Fatalf("expected rollback on non-writer UpdateCurrentValue")
	}

	// Delegated error via ItemActionTracker.Update
	fnr3 := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si3 := StoreInterface[int, string]{NodeRepository: fnr3, ItemActionTracker: iatUpdateErr[int, string]{}}
	b3, _ := New[int, string](store, &si3, nil)
	r3 := newNode[int, string](b3.getSlotLength())
	r3.newID(sop.NilUUID)
	vv := "v"
	r3.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	r3.Count = 1
	b3.StoreInfo.RootNodeID = r3.ID
	fnr3.Add(r3)
	b3.setCurrentItemID(r3.ID, 0)
	tx3 := &mockTx{begun: true, mode: sop.ForWriting}
	w3 := NewBtreeWithTransaction[int, string](tx3, b3)
	if _, err := w3.UpdateCurrentValue(context.Background(), "x"); err == nil {
		t.Fatalf("expected delegated UpdateCurrentValue error")
	}
	if tx3.rollbackCount != 1 {
		t.Fatalf("expected rollback on delegated UpdateCurrentValue error")
	}
}

func TestWithTransaction_Upsert_DelegatedError(t *testing.T) {
	// Underlying Upsert will hit Update path and ItemActionTracker.Update will error
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: iatUpdateErr[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	// Seed an existing item so AddIfNotExist returns false and Upsert goes to Update path
	if ok, err := b.Add(context.Background(), 10, "v"); !ok || err != nil {
		t.Fatalf("seed add: %v", err)
	}
	tx := &mockTx{begun: true, mode: sop.ForWriting}
	w := NewBtreeWithTransaction[int, string](tx, b)
	if _, err := w.Upsert(context.Background(), 10, "v2"); err == nil {
		t.Fatalf("expected delegated Upsert error via Update")
	}
	if tx.rollbackCount != 1 {
		t.Fatalf("expected rollback on delegated Upsert error")
	}
}

func TestWithTransaction_Success_Paths(t *testing.T) {
	// Underlying btree with couple of items
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	// Seed items
	if ok, err := b.Add(context.Background(), 1, "a"); !ok || err != nil {
		t.Fatalf("seed add1: %v", err)
	}
	if ok, err := b.Add(context.Background(), 2, "b"); !ok || err != nil {
		t.Fatalf("seed add2: %v", err)
	}

	// Writer tx for AddIfNotExist and Upsert
	txW := &mockTx{begun: true, mode: sop.ForWriting}
	w := NewBtreeWithTransaction[int, string](txW, b)

	if ok, err := w.AddIfNotExist(context.Background(), 3, "c"); err != nil || !ok {
		t.Fatalf("AddIfNotExist success expected, ok=%v err=%v", ok, err)
	}
	if ok, err := w.Upsert(context.Background(), 4, "d"); err != nil || !ok {
		t.Fatalf("Upsert success expected, ok=%v err=%v", ok, err)
	}

	// Read tx for read-only operations
	txR := &mockTx{begun: true, mode: sop.ForReading}
	r := NewBtreeWithTransaction[int, string](txR, b)
	if ok, err := r.First(context.Background()); err != nil || !ok {
		t.Fatalf("First: %v", err)
	}
	if ok, err := r.Next(context.Background()); err != nil || !ok {
		t.Fatalf("Next: %v", err)
	}
	if ok, err := r.Previous(context.Background()); err != nil || !ok {
		t.Fatalf("Previous: %v", err)
	}
	if ok, err := r.Last(context.Background()); err != nil || !ok {
		t.Fatalf("Last: %v", err)
	}

	// Position on a known key and test FindWithID success
	if ok, err := r.Find(context.Background(), 2, true); err != nil || !ok {
		t.Fatalf("Find: %v", err)
	}
	it, err := r.GetCurrentItem(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentItem after Find: %v", err)
	}
	if ok, err := r.FindWithID(context.Background(), 2, it.ID); err != nil || !ok {
		t.Fatalf("FindWithID success path failed, ok=%v err=%v", ok, err)
	}

	// Exercise GetCurrentValue success path through wrapper
	if _, err := r.GetCurrentValue(context.Background()); err != nil {
		t.Fatalf("GetCurrentValue success path error: %v", err)
	}
}
