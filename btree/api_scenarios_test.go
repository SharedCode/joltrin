package btree

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestFindWithID_Table(t *testing.T) {
	b, _, _ := buildSmallTree()
	// Allow duplicates
	b.StoreInfo.IsUnique = false
	if ok, err := b.Add(nil, 100, "x1"); err != nil || !ok {
		t.Fatalf("add 100 #1 err=%v", err)
	}
	if ok, err := b.Add(nil, 100, "x2"); err != nil || !ok {
		t.Fatalf("add 100 #2 err=%v", err)
	}

	// Capture both IDs via traversal
	if ok, _ := b.Find(nil, 100, true); !ok {
		t.Fatalf("find 100 first")
	}
	it, _ := b.GetCurrentItem(nil)
	id1 := it.ID
	// Move to next duplicate (same key)
	if ok, _ := b.Next(nil); !ok {
		t.Fatalf("next to second duplicate")
	}
	it2, _ := b.GetCurrentItem(nil)
	if it2.Key != 100 {
		t.Fatalf("expected same key 100 for second duplicate")
	}
	id2 := it2.ID

	cases := []struct {
		key int
		id  sop.UUID
	}{
		{100, id1},
		{100, id2},
	}
	for _, c := range cases {
		if ok, err := b.FindWithID(nil, c.key, c.id); err != nil || !ok {
			t.Fatalf("FindWithID(%d,%v) failed: %v", c.key, c.id, err)
		}
		got, _ := b.GetCurrentItem(nil)
		if got.ID != c.id {
			t.Fatalf("FindWithID got wrong id: %v want %v", got.ID, c.id)
		}
	}
}

func TestAPI_Last_AddIfNotExist_Upsert_Update_Remove(t *testing.T) {
	b, _ := newTestBtree[string]()

	// Seed
	for _, kv := range []struct {
		k int
		v string
	}{{2, "b"}, {1, "a"}, {3, "c"}} {
		if ok, err := b.Add(nil, kv.k, kv.v); err != nil || !ok {
			t.Fatalf("add %d err=%v", kv.k, err)
		}
	}

	// Last()
	if ok, err := b.Last(nil); err != nil || !ok {
		t.Fatalf("Last err=%v", err)
	}
	if k := b.GetCurrentKey().Key; k != 3 {
		t.Fatalf("Last want 3 got %d", k)
	}

	// AddIfNotExist
	if ok, err := b.AddIfNotExist(nil, 2, "bb"); err != nil {
		t.Fatalf("AddIfNotExist err=%v", err)
	} else if ok {
		t.Fatalf("AddIfNotExist should be false for existing key")
	}
	if ok, err := b.AddIfNotExist(nil, 4, "d"); err != nil || !ok {
		t.Fatalf("AddIfNotExist new failed: %v", err)
	}

	// Upsert add then update
	if ok, err := b.Upsert(nil, 5, "e"); err != nil || !ok {
		t.Fatalf("Upsert add failed: %v", err)
	}
	if ok, err := b.Upsert(nil, 5, "ee"); err != nil || !ok {
		t.Fatalf("Upsert update failed: %v", err)
	}
	if ok, err := b.Find(nil, 5, false); err != nil || !ok {
		t.Fatalf("Find 5 after upsert: %v", err)
	}
	if it, _ := b.GetCurrentItem(nil); *it.Value != "ee" {
		t.Fatalf("Upsert did not update value")
	}

	// Update by key
	if ok, err := b.Update(nil, 1, "aa"); err != nil || !ok {
		t.Fatalf("Update err=%v", err)
	}
	if ok, _ := b.Find(nil, 1, false); !ok {
		t.Fatalf("Find 1 after update")
	}
	if it, _ := b.GetCurrentItem(nil); *it.Value != "aa" {
		t.Fatalf("Update did not apply")
	}

	// Remove by key
	if ok, err := b.Remove(nil, 2); err != nil || !ok {
		t.Fatalf("Remove 2 err=%v", err)
	}
	if ok, _ := b.Find(nil, 2, false); ok {
		t.Fatalf("2 should be gone")
	}
}

func TestGetters_And_Lock(t *testing.T) {
	b, _ := newTestBtree[string]()
	if !b.IsUnique() {
		t.Fatalf("IsUnique should be true for test setup")
	}
	si := b.GetStoreInfo()
	if si.SlotLength == 0 {
		t.Fatalf("GetStoreInfo returned zero SlotLength")
	}
	if err := b.Lock(nil, false); err != nil {
		t.Fatalf("Lock returned error: %v", err)
	}
}

// Cover updateChildrenParent and getChildren
func TestUpdateChildrenParent_And_GetChildren(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	// Two children
	c1 := newNode[int, string](b.getSlotLength())
	c1.newID(parent.ID)
	c2 := newNode[int, string](b.getSlotLength())
	c2.newID(parent.ID)
	parent.ChildrenIDs[0] = c1.ID
	parent.ChildrenIDs[1] = c2.ID
	fnr.Add(parent)
	fnr.Add(c1)
	fnr.Add(c2)
	if err := parent.updateChildrenParent(nil, b); err != nil {
		t.Fatalf("updateChildrenParent err: %v", err)
	}
	if c1.ParentID != parent.ID || c2.ParentID != parent.ID {
		t.Fatalf("children parent not updated")
	}
	kids, err := parent.getChildren(nil, b)
	if err != nil || len(kids) < 2 || kids[0] == nil || kids[1] == nil {
		t.Fatalf("getChildren failed: %v", err)
	}
}

// Cover UpdateCurrentNodeItem success path and IsValueDataInNodeSegment getter.
func TestUpdateCurrentNodeItem_And_FlagsGetter(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v1 := "old"
	it1 := &Item[int, string]{Key: 1, Value: &v1, ID: sop.NewUUID()}
	root.Slots[0] = *it1
	root.Count = 1
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)
	b.setCurrentItemID(root.ID, 0)

	v2 := "new"
	it2 := &Item[int, string]{Key: 1, Value: &v2, ID: sop.NewUUID()}
	if ok, err := b.UpdateCurrentItemWithItem(nil, it2); !ok || err != nil {
		t.Fatalf("UpdateCurrentNodeItem failed: %v", err)
	}
	if root.Slots[0] != *it2 {
		t.Fatalf("node slot not replaced")
	}
	if b.currentItem != it2 {
		t.Fatalf("btree currentItem not updated")
	}

	// Getter coverage
	_ = b.IsValueDataInNodeSegment()
}

// --- Transaction wrapper coverage: preconditions, rollback-on-error, and happy path ---

func TestWithTransaction_PreconditionsAndErrors(t *testing.T) {
	// Base btree for delegation
	b, _ := newTestBtree[string]()

	// Case 1: read op without begun -> rollback and error
	tx1 := &mockTx{begun: false, mode: sop.ForReading}
	w1 := NewBtreeWithTransaction(tx1, b)
	if ok, err := w1.Find(nil, 1, true); ok || err == nil {
		t.Fatalf("Find without begun should fail")
	}
	if tx1.rollbackCount != 1 {
		t.Fatalf("expected rollback on read without begun")
	}

	// Case 2: write op with wrong mode -> rollback and error
	tx2 := &mockTx{begun: true, mode: sop.ForReading}
	w2 := NewBtreeWithTransaction(tx2, b)
	if ok, err := w2.Add(nil, 1, "x"); ok || err == nil {
		t.Fatalf("Add in read mode should fail and not succeed")
	}
	if tx2.rollbackCount != 1 {
		t.Fatalf("expected rollback on write in read mode")
	}

	// Case 3: delegate error triggers rollback
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: iatErr[int, string]{}}
	bErr, _ := New[int, string](store, &si, nil)
	tx3 := &mockTx{begun: true, mode: sop.ForWriting}
	w3 := NewBtreeWithTransaction(tx3, bErr)
	if ok, err := w3.Add(nil, 1, "x"); ok || err == nil {
		t.Fatalf("expected underlying Add error to bubble and not succeed")
	}
	if tx3.rollbackCount != 1 {
		t.Fatalf("expected rollback when delegate returns error")
	}

	// Case 4: happy path, writer mode, begun
	tx4 := &mockTx{begun: true, mode: sop.ForWriting}
	w4 := NewBtreeWithTransaction(tx4, b)
	if ok, err := w4.Add(nil, 2, "y"); !ok || err != nil {
		t.Fatalf("Add happy path failed: %v", err)
	}
	if tx4.rollbackCount != 0 {
		t.Fatalf("unexpected rollback on happy path")
	}
}

// Cover UpdateCurrentValue path in withtransaction wrapper (was 0%).
func TestWithTransaction_UpdateCurrentValue(t *testing.T) {
	b, _ := newTestBtree[string]()
	tx := &mockTx{mode: sop.ForWriting}
	w := NewBtreeWithTransaction[int, string](tx, b)

	// Begin transaction and add one item to select it.
	_ = tx.Begin(context.Background())
	if ok, err := w.Add(nil, 9, "v"); !ok || err != nil {
		t.Fatalf("add err=%v", err)
	}
	if ok, err := w.Find(nil, 9, false); !ok || err != nil {
		t.Fatalf("find err=%v", err)
	}
	if ok, err := w.UpdateCurrentValue(nil, "vv"); !ok || err != nil {
		t.Fatalf("UpdateCurrentValue err=%v", err)
	}
	if it, _ := w.GetCurrentItem(nil); *it.Value != "vv" {
		t.Fatalf("value not updated")
	}
}

// Cover FindWithID not-found branch in Btree (loop exits via Next returning false).
func TestFindWithID_NotFound(t *testing.T) {
	b, _ := newTestBtree[string]()
	// Add two items with same key; search for a random ID that doesn't exist.
	_, _ = b.Add(nil, 1, "a")
	_, _ = b.Add(nil, 1, "b")
	missing := sop.NewUUID() // guaranteed not to match
	if ok, err := b.FindWithID(nil, 1, missing); ok || err != nil {
		t.Fatalf("expected not found, err=%v", err)
	}
}

// Exercise wrapper FindWithID and Lock to cover remaining withtransaction paths.
func TestWithTransaction_FindWithID_And_Lock(t *testing.T) {
	// Underlying tree allowing duplicates
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: false, IsValueDataInNodeSegment: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	// Seed duplicates
	if ok, _ := b.Add(nil, 7, "a"); !ok {
		t.Fatalf("add 7 a")
	}
	if ok, _ := b.Add(nil, 7, "b"); !ok {
		t.Fatalf("add 7 b")
	}
	if ok, _ := b.Find(nil, 7, true); !ok {
		t.Fatalf("find 7")
	}
	first, _ := b.GetCurrentItem(nil)
	if ok, _ := b.Next(nil); !ok {
		t.Fatalf("next dup")
	}
	second, _ := b.GetCurrentItem(nil)

	// Read-mode transaction, begun
	tx := &mockTx{begun: true, mode: sop.ForReading}
	w := NewBtreeWithTransaction(tx, b)

	// FindWithID should select the requested duplicate
	if ok, err := w.FindWithID(nil, 7, second.ID); !ok || err != nil {
		t.Fatalf("FindWithID failed: %v", err)
	}
	got := w.GetCurrentKey()
	if got.ID != second.ID || got.ID == first.ID {
		t.Fatalf("FindWithID positioned wrong duplicate")
	}
}

// Cover the rest of withtransaction read/write delegates to increase coverage.
func TestWithTransaction_CoverAllDelegates(t *testing.T) {
	b, _ := newTestBtree[string]()
	// Seed some values
	for _, kv := range []struct {
		k int
		v string
	}{{1, "a"}, {2, "b"}, {3, "c"}} {
		if ok, _ := b.Add(nil, kv.k, kv.v); !ok {
			t.Fatalf("seed add")
		}
	}

	// Writer-mode tx
	tx := &mockTx{begun: true, mode: sop.ForWriting}
	w := NewBtreeWithTransaction(tx, b)

	// AddIfNotExist (existing -> false) and Upsert, Update, Remove, RemoveCurrentItem
	if ok, err := w.AddIfNotExist(nil, 1, "aa"); err != nil || ok {
		t.Fatalf("AddIfNotExist existing should be false")
	}
	if ok, err := w.Upsert(nil, 4, "d"); err != nil || !ok {
		t.Fatalf("Upsert add failed: %v", err)
	}
	if ok, err := w.Update(nil, 2, "bb"); err != nil || !ok {
		t.Fatalf("Update failed: %v", err)
	}
	if ok, err := w.Remove(nil, 3); err != nil || !ok {
		t.Fatalf("Remove by key failed: %v", err)
	}

	// Position and RemoveCurrentItem
	if ok, err := w.Find(nil, 1, false); err != nil || !ok {
		t.Fatalf("Find 1: %v", err)
	}
	if ok, err := w.RemoveCurrentItem(nil); err != nil || !ok {
		t.Fatalf("RemoveCurrentItem: %v", err)
	}

	// Read-only delegates: First/Last/Next/Previous/GetCurrentValue/Item
	txRead := &mockTx{begun: true, mode: sop.ForReading}
	rw := NewBtreeWithTransaction(txRead, b)
	if ok, err := rw.First(nil); err != nil || !ok {
		t.Fatalf("First: %v", err)
	}
	if ok, err := rw.Last(nil); err != nil || !ok {
		t.Fatalf("Last: %v", err)
	}
	// Ensure there is at least one item
	if ok, err := rw.Find(nil, 4, false); err != nil || !ok {
		t.Fatalf("Find 4: %v", err)
	}
	if _, err := rw.GetCurrentItem(nil); err != nil {
		t.Fatalf("GetCurrentItem: %v", err)
	}
	if _, err := rw.GetCurrentValue(nil); err != nil {
		t.Fatalf("GetCurrentValue: %v", err)
	}
	if ok, err := rw.Next(nil); err != nil {
		t.Fatalf("Next: %v", err)
	} else {
		_ = ok
	}
	if ok, err := rw.Previous(nil); err != nil {
		t.Fatalf("Previous: %v", err)
	} else {
		_ = ok
	}
}

func TestAddItem_DuplicateReturnsFalse(t *testing.T) {
	b, _ := newTestBtree[string]()
	// Prepare root
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	// First insert via AddItem
	v := "v"
	item1 := &Item[int, string]{Key: 10, Value: &v, ID: sop.NewUUID()}
	if ok, err := b.AddItem(nil, item1); err != nil || !ok {
		t.Fatalf("AddItem #1 err=%v ok=%v", err, ok)
	}
	// Duplicate key should return false
	item2 := &Item[int, string]{Key: 10, Value: &v, ID: sop.NewUUID()}
	if ok, err := b.AddItem(nil, item2); err != nil || ok {
		t.Fatalf("AddItem duplicate should be false,nil; got ok=%v err=%v", ok, err)
	}
}

func TestGetCurrentKey_NoSelection(t *testing.T) {
	b, _ := newTestBtree[string]()
	k := b.GetCurrentKey()
	if k.ID != sop.NilUUID || k.Key != 0 {
		t.Fatalf("expected zero key and NilUUID when no selection")
	}
}

func TestUpdateCurrentNodeItem_Guards(t *testing.T) {
	b, fnr := newTestBtree[string]()
	// Nil selection
	b.setCurrentItemID(sop.NilUUID, 0)
	if ok, err := b.UpdateCurrentItemWithItem(nil, &Item[int, string]{}); err != nil || ok {
		t.Fatalf("expected false,nil on nil selection")
	}
	// Selection points to nil slot
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 1
	b.setCurrentItemID(root.ID, 0)
	if ok, err := b.UpdateCurrentItemWithItem(nil, &Item[int, string]{}); err != nil || ok {
		t.Fatalf("expected false,nil on nil slot selection")
	}
}

func TestUpdateCurrentValue_Guards(t *testing.T) {
	b, fnr := newTestBtree[string]()
	// Nil selection
	if ok, err := b.UpdateCurrentValue(nil, "x"); err != nil || ok {
		t.Fatalf("expected false,nil on nil selection")
	}
	// Selection points to nil slot
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 1
	b.setCurrentItemID(root.ID, 0)
	if ok, err := b.UpdateCurrentValue(nil, "y"); err != nil || ok {
		t.Fatalf("expected false,nil on nil slot selection")
	}
}

func TestFirstLast_EmptyReturnsFalse(t *testing.T) {
	b, _ := newTestBtree[string]()
	b.StoreInfo.Count = 0
	if ok, err := b.First(nil); err != nil || ok {
		t.Fatalf("First on empty should be false,nil")
	}
	if ok, err := b.Last(nil); err != nil || ok {
		t.Fatalf("Last on empty should be false,nil")
	}
}

// Update/Remove not-found paths should return false without error.
func TestAPI_Update_Remove_NotFound(t *testing.T) {
	b, _ := newTestBtree[string]()
	if ok, err := b.Update(nil, 42, "x"); err != nil || ok {
		t.Fatalf("Update not-found should be false,nil, got ok=%v err=%v", ok, err)
	}
	if ok, err := b.Remove(nil, 42); err != nil || ok {
		t.Fatalf("Remove not-found should be false,nil, got ok=%v err=%v", ok, err)
	}
}
