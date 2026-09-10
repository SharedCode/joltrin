package btree

import (
	"context"
	"errors"
	"testing"

	"github.com/sharedcode/joltrin"
)

// Covers btreeWithTransaction wrapper methods left untested: UpdateKey,
// UpdateCurrentItem, UpdateCurrentKey (delegated-error and success paths),
// and GetCurrentItemNoLock. Follows the same not-begun/non-writer/
// delegated-error/success shape as the existing Update/Upsert coverage in
// withtransaction_more_test.go.

func TestWithTransaction_UpdateKey_AllPaths(t *testing.T) {
	b := newTestKeyStructBtree(t)
	key := KeyStruct{ID: 1, Metadata: "initial"}
	if ok, err := b.Add(context.Background(), key, "v"); !ok || err != nil {
		t.Fatalf("seed add: %v", err)
	}

	// Not begun
	tx1 := &mockTx{begun: false, mode: sop.ForWriting}
	w1 := NewBtreeWithTransaction[KeyStruct, string](tx1, b)
	if _, err := w1.UpdateKey(context.Background(), key); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("not-begun expected errTransHasNotBegunMsg, got %v", err)
	}

	// Non-writer
	tx2 := &mockTx{begun: true, mode: sop.ForReading}
	w2 := NewBtreeWithTransaction[KeyStruct, string](tx2, b)
	if _, err := w2.UpdateKey(context.Background(), key); err == nil {
		t.Fatal("expected error on non-writer UpdateKey")
	}
	if tx2.rollbackCount != 1 {
		t.Fatalf("expected rollback on non-writer UpdateKey, got %d", tx2.rollbackCount)
	}

	// Delegated error
	fnrErr := &fakeNR[KeyStruct, string]{n: map[sop.UUID]*Node[KeyStruct, string]{}}
	siErr := StoreInterface[KeyStruct, string]{NodeRepository: fnrErr, ItemActionTracker: iatUpdateErr[KeyStruct, string]{}}
	bErr, err := New[KeyStruct, string](sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true}), &siErr, compareKeyStruct)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ok, err := bErr.Add(context.Background(), key, "v"); !ok || err != nil {
		t.Fatalf("seed add: %v", err)
	}
	tx3 := &mockTx{begun: true, mode: sop.ForWriting}
	w3 := NewBtreeWithTransaction[KeyStruct, string](tx3, bErr)
	if _, err := w3.UpdateKey(context.Background(), key); err == nil {
		t.Fatal("expected delegated UpdateKey error")
	}
	if tx3.rollbackCount != 1 {
		t.Fatalf("expected rollback on delegated UpdateKey error, got %d", tx3.rollbackCount)
	}

	// Success
	tx4 := &mockTx{begun: true, mode: sop.ForWriting}
	w4 := NewBtreeWithTransaction[KeyStruct, string](tx4, b)
	newKey := KeyStruct{ID: 1, Metadata: "updated"}
	if ok, err := w4.UpdateKey(context.Background(), newKey); !ok || err != nil {
		t.Fatalf("UpdateKey success expected, ok=%v err=%v", ok, err)
	}
	if tx4.rollbackCount != 0 {
		t.Fatalf("success path should not rollback, got %d", tx4.rollbackCount)
	}
}

func TestWithTransaction_UpdateCurrentItem_AllPaths(t *testing.T) {
	b := newTestKeyStructBtree(t)
	key := KeyStruct{ID: 1, Metadata: "initial"}
	if ok, err := b.Add(context.Background(), key, "v"); !ok || err != nil {
		t.Fatalf("seed add: %v", err)
	}
	b.Find(context.Background(), key, false)

	// Not begun
	tx1 := &mockTx{begun: false, mode: sop.ForWriting}
	w1 := NewBtreeWithTransaction[KeyStruct, string](tx1, b)
	if _, err := w1.UpdateCurrentItem(context.Background(), key, "v2"); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("not-begun expected errTransHasNotBegunMsg, got %v", err)
	}

	// Non-writer
	tx2 := &mockTx{begun: true, mode: sop.ForReading}
	w2 := NewBtreeWithTransaction[KeyStruct, string](tx2, b)
	if _, err := w2.UpdateCurrentItem(context.Background(), key, "v2"); err == nil {
		t.Fatal("expected error on non-writer UpdateCurrentItem")
	}
	if tx2.rollbackCount != 1 {
		t.Fatalf("expected rollback on non-writer UpdateCurrentItem, got %d", tx2.rollbackCount)
	}

	// Delegated error
	fnrErr := &fakeNR[KeyStruct, string]{n: map[sop.UUID]*Node[KeyStruct, string]{}}
	siErr := StoreInterface[KeyStruct, string]{NodeRepository: fnrErr, ItemActionTracker: iatUpdateErr[KeyStruct, string]{}}
	bErr, err := New[KeyStruct, string](sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true}), &siErr, compareKeyStruct)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ok, err := bErr.Add(context.Background(), key, "v"); !ok || err != nil {
		t.Fatalf("seed add: %v", err)
	}
	bErr.Find(context.Background(), key, false)
	tx3 := &mockTx{begun: true, mode: sop.ForWriting}
	w3 := NewBtreeWithTransaction[KeyStruct, string](tx3, bErr)
	if _, err := w3.UpdateCurrentItem(context.Background(), key, "v2"); err == nil {
		t.Fatal("expected delegated UpdateCurrentItem error")
	}
	if tx3.rollbackCount != 1 {
		t.Fatalf("expected rollback on delegated UpdateCurrentItem error, got %d", tx3.rollbackCount)
	}

	// Success
	tx4 := &mockTx{begun: true, mode: sop.ForWriting}
	w4 := NewBtreeWithTransaction[KeyStruct, string](tx4, b)
	newKey := KeyStruct{ID: 1, Metadata: "updated_item"}
	if ok, err := w4.UpdateCurrentItem(context.Background(), newKey, "v2"); !ok || err != nil {
		t.Fatalf("UpdateCurrentItem success expected, ok=%v err=%v", ok, err)
	}
	if tx4.rollbackCount != 0 {
		t.Fatalf("success path should not rollback, got %d", tx4.rollbackCount)
	}
}

func TestWithTransaction_UpdateCurrentKey_DelegatedErrorAndSuccess(t *testing.T) {
	b := newTestKeyStructBtree(t)
	key := KeyStruct{ID: 1, Metadata: "initial"}
	if ok, err := b.Add(context.Background(), key, "v"); !ok || err != nil {
		t.Fatalf("seed add: %v", err)
	}
	b.Find(context.Background(), key, false)

	// Delegated error
	fnrErr := &fakeNR[KeyStruct, string]{n: map[sop.UUID]*Node[KeyStruct, string]{}}
	siErr := StoreInterface[KeyStruct, string]{NodeRepository: fnrErr, ItemActionTracker: iatUpdateErr[KeyStruct, string]{}}
	bErr, err := New[KeyStruct, string](sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true}), &siErr, compareKeyStruct)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ok, err := bErr.Add(context.Background(), key, "v"); !ok || err != nil {
		t.Fatalf("seed add: %v", err)
	}
	bErr.Find(context.Background(), key, false)
	tx1 := &mockTx{begun: true, mode: sop.ForWriting}
	w1 := NewBtreeWithTransaction[KeyStruct, string](tx1, bErr)
	newKey := KeyStruct{ID: 1, Metadata: "updated"}
	if _, err := w1.UpdateCurrentKey(context.Background(), newKey); err == nil {
		t.Fatal("expected delegated UpdateCurrentKey error")
	}
	if tx1.rollbackCount != 1 {
		t.Fatalf("expected rollback on delegated UpdateCurrentKey error, got %d", tx1.rollbackCount)
	}

	// Success
	tx2 := &mockTx{begun: true, mode: sop.ForWriting}
	w2 := NewBtreeWithTransaction[KeyStruct, string](tx2, b)
	if ok, err := w2.UpdateCurrentKey(context.Background(), newKey); !ok || err != nil {
		t.Fatalf("UpdateCurrentKey success expected, ok=%v err=%v", ok, err)
	}
	if tx2.rollbackCount != 0 {
		t.Fatalf("success path should not rollback, got %d", tx2.rollbackCount)
	}
}

func TestWithTransaction_GetCurrentItemNoLock_AllPaths(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNRWithErr[int, string]{fakeNR: fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}, errOnGet: true}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	b.currentItemRef = currentItemRef{nodeID: sop.NewUUID(), nodeItemIndex: 0}

	// Not begun
	tx1 := &mockTx{begun: false, mode: sop.ForReading}
	w1 := NewBtreeWithTransaction[int, string](tx1, b)
	if _, err := w1.GetCurrentItemNoLock(context.Background()); !errors.Is(err, errTransHasNotBegunMsg) {
		t.Fatalf("not-begun expected errTransHasNotBegunMsg, got %v", err)
	}
	if tx1.rollbackCount != 1 {
		t.Fatalf("not-begun should rollback, got %d", tx1.rollbackCount)
	}

	// Delegated error (fakeNRWithErr errors on Get)
	tx2 := &mockTx{begun: true, mode: sop.ForReading}
	w2 := NewBtreeWithTransaction[int, string](tx2, b)
	if _, err := w2.GetCurrentItemNoLock(context.Background()); err == nil {
		t.Fatal("expected delegated GetCurrentItemNoLock error")
	}
	if tx2.rollbackCount != 1 {
		t.Fatalf("expected rollback on delegated GetCurrentItemNoLock error, got %d", tx2.rollbackCount)
	}

	// Success
	fnrOK := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	siOK := StoreInterface[int, string]{NodeRepository: fnrOK, ItemActionTracker: fakeIAT[int, string]{}}
	bOK, _ := New[int, string](store, &siOK, nil)
	root := newNode[int, string](bOK.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	root.Slots[0] = Item[int, string]{Key: 1, Value: &v, ID: sop.NewUUID()}
	root.Count = 1
	bOK.StoreInfo.RootNodeID = root.ID
	fnrOK.Add(root)
	bOK.setCurrentItemID(root.ID, 0)

	tx3 := &mockTx{begun: true, mode: sop.ForReading}
	w3 := NewBtreeWithTransaction[int, string](tx3, bOK)
	item, err := w3.GetCurrentItemNoLock(context.Background())
	if err != nil {
		t.Fatalf("GetCurrentItemNoLock success expected, got err=%v", err)
	}
	if item.Key != 1 {
		t.Errorf("expected key 1, got %d", item.Key)
	}
	if tx3.rollbackCount != 0 {
		t.Fatalf("success path should not rollback, got %d", tx3.rollbackCount)
	}
}
