package btree

import (
	"context"
	"errors"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// iatGetErr returns error on Get to drive GetCurrentItem error branch.
type iatGetErr[TK Ordered, TV any] struct{}

func (iatGetErr[TK, TV]) Add(ctx context.Context, item *Item[TK, TV]) error    { return nil }
func (iatGetErr[TK, TV]) Update(ctx context.Context, item *Item[TK, TV]) error { return nil }
func (iatGetErr[TK, TV]) Remove(ctx context.Context, item *Item[TK, TV]) error { return nil }
func (iatGetErr[TK, TV]) Get(ctx context.Context, item *Item[TK, TV]) error {
	return errors.New("get error")
}

// iatRemoveErr2 returns error on Remove to drive fixVacatedSlot/RemoveCurrentItem error branch.
type iatRemoveErr2[TK Ordered, TV any] struct{}

func (iatRemoveErr2[TK, TV]) Add(ctx context.Context, item *Item[TK, TV]) error    { return nil }
func (iatRemoveErr2[TK, TV]) Update(ctx context.Context, item *Item[TK, TV]) error { return nil }
func (iatRemoveErr2[TK, TV]) Remove(ctx context.Context, item *Item[TK, TV]) error {
	return errors.New("remove error")
}
func (iatRemoveErr2[TK, TV]) Get(ctx context.Context, item *Item[TK, TV]) error { return nil }

func TestFind_ShortCircuitWithCurrentSelection(t *testing.T) {
	// Build minimal tree with one node and select it; then call Find with same key and firstItemWithKey=false
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	root.Slots[0] = Item[int, string]{Key: 10, Value: &v, ID: sop.NewUUID()}
	root.Count = 1
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 1

	// Select the item as current
	b.setCurrentItemID(root.ID, 0)

	if ok, err := b.Find(nil, 10, false); err != nil || !ok {
		t.Fatalf("Find short-circuit failed: ok=%v err=%v", ok, err)
	}
}

func TestGetCurrentItem_ErrorOnGet(t *testing.T) {
	// Arrange a current selection and an ItemActionTracker that errors on Get
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: iatGetErr[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "x"
	root.Slots[0] = Item[int, string]{Key: 1, Value: &v, ID: sop.NewUUID(), ValueNeedsFetch: true}
	root.Count = 1
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID
	b.setCurrentItemID(root.ID, 0)

	if _, err := b.GetCurrentItem(nil); err == nil {
		t.Fatalf("expected error from ItemActionTracker.Get")
	}
}

func TestFirstLast_Empty_ReturnsFalse(t *testing.T) {
	b, _ := newTestBtree[string]()
	// Ensure tree is empty
	b.StoreInfo.Count = 0
	b.StoreInfo.RootNodeID = sop.NilUUID
	if ok, err := b.First(nil); err != nil || ok {
		t.Fatalf("First on empty should be false, err=%v ok=%v", err, ok)
	}
	if ok, err := b.Last(nil); err != nil || ok {
		t.Fatalf("Last on empty should be false, err=%v ok=%v", err, ok)
	}
}

func TestSiblings_BoundaryLookups(t *testing.T) {
	b, fnr := newTestBtree[string]()
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	// prepare two children under parent
	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	parent.Count = 1 // one separator -> two children
	parent.ChildrenIDs = make([]sop.UUID, 2)
	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = right.ID
	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(right)

	// Leftmost child has no left sibling
	if sib, err := left.getLeftSibling(nil, b); err != nil || sib != nil {
		t.Fatalf("leftmost getLeftSibling expected nil, err=%v", err)
	}
	// Rightmost child has no right sibling
	if sib, err := right.getRightSibling(nil, b); err != nil || sib != nil {
		t.Fatalf("rightmost getRightSibling expected nil, err=%v", err)
	}

	// Also cover getIndexOfNode for root returning 0
	if idx, err := parent.getIndexOfNode(nil, b); err != nil || idx != 0 {
		t.Fatalf("getIndexOfNode on root want 0 got %d err=%v", idx, err)
	}
}

func TestRemoveCurrentItem_Leaf_RemoveError(t *testing.T) {
	// Arrange a leaf node selection and IAT that errors on Remove
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: iatRemoveErr2[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	leaf := newNode[int, string](b.getSlotLength())
	leaf.newID(sop.NilUUID)
	v := "z"
	leaf.Slots[0] = Item[int, string]{Key: 9, Value: &v, ID: sop.NewUUID()}
	leaf.Count = 1
	fnr.Add(leaf)
	b.StoreInfo.RootNodeID = leaf.ID
	b.StoreInfo.Count = 1
	b.setCurrentItemID(leaf.ID, 0)

	if ok, err := b.RemoveCurrentItem(nil); err == nil || ok {
		t.Fatalf("expected RemoveCurrentItem to propagate remove error, ok=%v err=%v", ok, err)
	}
	// Ensure count not decremented on failure
	if b.Count() != 1 {
		t.Fatalf("store count should remain 1 on error, got %d", b.Count())
	}
}

// Exercise Add error branch (ItemActionTracker.Add returns error) and ensure Count is not incremented.
func TestAdd_ItemActionTrackerError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: iatErr[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Seed an explicit root to avoid lazy creation differences.
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)

	if ok, err := b.Add(context.Background(), 1, "v"); err == nil || ok {
		t.Fatalf("expected Add to return error and not succeed")
	}
	if b.Count() != 0 {
		t.Fatalf("count changed on failed Add: %d", b.Count())
	}
}

// Find should short-circuit when current item is already selected and matches key with firstItemWithKey=false.
func TestFind_CurrentSelected_ShortCircuit(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	root.Slots[0] = Item[int, string]{Key: 10, Value: &v, ID: sop.NewUUID()}
	root.Count = 1
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 1
	b.setCurrentItemID(root.ID, 0)

	if ok, err := b.Find(context.Background(), 10, false); !ok || err != nil {
		t.Fatalf("expected Find to short-circuit to true,nil")
	}
}

// First/Last should return false when the store is empty.
func TestFirstLast_EmptyTree(t *testing.T) {
	b, _ := newTestBtree[string]()
	if ok, err := b.First(context.Background()); ok || err != nil {
		t.Fatalf("First on empty should be false,nil; got ok=%v err=%v", ok, err)
	}
	if ok, err := b.Last(context.Background()); ok || err != nil {
		t.Fatalf("Last on empty should be false,nil; got ok=%v err=%v", ok, err)
	}
}

// Table-driven Update cases: not found returns false; find error propagates.
func TestUpdate_NotFound_And_FindError(t *testing.T) {
	t.Run("notfound", func(t *testing.T) {
		b, _ := newTestBtree[string]()
		if ok, err := b.Update(context.Background(), 999, "v"); ok || err != nil {
			t.Fatalf("expected false,nil for missing key; got ok=%v err=%v", ok, err)
		}
	})
	t.Run("find error", func(t *testing.T) {
		store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
		fnr := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
		si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
		b, _ := New[int, string](store, &si, nil)
		// Force Find to attempt fetching root and error.
		b.StoreInfo.Count = 1
		b.StoreInfo.RootNodeID = sop.NewUUID()
		fnr.errs[b.StoreInfo.RootNodeID] = true
		if ok, err := b.Update(context.Background(), 1, "x"); err == nil || ok {
			t.Fatalf("expected Update to propagate Find error")
		}
	})
}

// Upsert should propagate error from underlying Add (via AddIfNotExist).
func TestUpsert_AddError_Propagates(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: iatErr[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)

	if ok, err := b.Upsert(context.Background(), 42, "z"); err == nil || ok {
		t.Fatalf("expected Upsert to return error via AddIfNotExist")
	}
}

// RemoveCurrentItem should return error when ItemActionTracker.Remove fails during leaf deletion.
func TestRemoveCurrentItem_RemoveError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: iatRemoveErr[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	v := "x"
	n.Slots[0] = Item[int, string]{Key: 1, Value: &v, ID: sop.NewUUID()}
	n.Count = 1
	fnr.Add(n)
	b.StoreInfo.RootNodeID = n.ID
	b.StoreInfo.Count = 1
	b.setCurrentItemID(n.ID, 0)

	if ok, err := b.RemoveCurrentItem(context.Background()); err == nil || ok {
		t.Fatalf("expected RemoveCurrentItem to return error when tracker.Remove fails")
	}
}

// getLeftSibling/getRightSibling edge cases: return nil at boundaries, and normal returns.
func TestSiblings_LeftRight_Edges(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	p.Count = 2 // two items => three children slots [0..2]
	p.ChildrenIDs = make([]sop.UUID, 3)
	left := newNode[int, string](b.getSlotLength())
	left.newID(p.ID)
	mid := newNode[int, string](b.getSlotLength())
	mid.newID(p.ID)
	right := newNode[int, string](b.getSlotLength())
	right.newID(p.ID)
	p.ChildrenIDs[0] = left.ID
	p.ChildrenIDs[1] = mid.ID
	p.ChildrenIDs[2] = right.ID
	fnr.Add(p)
	fnr.Add(left)
	fnr.Add(mid)
	fnr.Add(right)

	if ls, err := left.getLeftSibling(context.Background(), b); err != nil || ls != nil {
		t.Fatalf("leftmost sibling should have nil left; err=%v", err)
	}
	if rs, err := right.getRightSibling(context.Background(), b); err != nil || rs != nil {
		t.Fatalf("rightmost sibling should have nil right; err=%v", err)
	}
	if ls, err := mid.getLeftSibling(context.Background(), b); err != nil || ls == nil || ls.ID != left.ID {
		t.Fatalf("mid left sibling wrong: %+v err=%v", ls, err)
	}
	if rs, err := mid.getRightSibling(context.Background(), b); err != nil || rs == nil || rs.ID != right.ID {
		t.Fatalf("mid right sibling wrong: %+v err=%v", rs, err)
	}
}

// isThereVacantSlotInLeft should propagate errors from getLeftSibling.
func TestIsThereVacantSlotInLeft_ErrorPropagation(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, LeafLoadBalancing: true})
	fnr := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Parent with two children; left sibling fetch will error.
	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	p.Count = 1
	// ChildrenIDs should be sized to slotLength+1 to match implementation expectations
	p.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	left := newNode[int, string](b.getSlotLength())
	left.newID(p.ID)
	cur := newNode[int, string](b.getSlotLength())
	cur.newID(p.ID)
	// Make current node full and leaf (ChildrenIDs==nil) so scan proceeds to siblings.
	for i := 0; i < b.getSlotLength(); i++ {
		v := "v"
		vv := v
		cur.Slots[i] = Item[int, string]{Key: 100 + i, Value: &vv, ID: sop.NewUUID()}
	}
	cur.Count = b.getSlotLength()

	p.ChildrenIDs[0] = left.ID
	p.ChildrenIDs[1] = cur.ID
	fnr.Add(p)
	fnr.Add(left)
	fnr.Add(cur)
	// Force error when retrieving left sibling id
	fnr.errs[left.ID] = true

	unb := false
	if ok, err := cur.isThereVacantSlotInLeft(context.Background(), b, &unb); err == nil || ok {
		t.Fatalf("expected error propagation from getLeftSibling")
	}
}

// distributeToLeft: early return when indexOfNode > parent.Count (child not registered in parent).
func TestDistributeToLeft_IndexBeyondParentCount(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, LeafLoadBalancing: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	p.Count = 1
	// ChildrenIDs must be sized to slotLength+1 because getIndexOfChild
	// iterates up to len(parent.Slots) and indexes ChildrenIDs at that index.
	p.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	// Note: do NOT register child ID in ChildrenIDs to trigger indexOfNode > parent.Count
	n := newNode[int, string](b.getSlotLength())
	n.newID(p.ID)
	// Fill node to mark as full
	for i := 0; i < b.getSlotLength(); i++ {
		v := "x"
		vv := v
		n.Slots[i] = Item[int, string]{Key: i + 1, Value: &vv, ID: sop.NewUUID()}
	}
	n.Count = b.getSlotLength()
	fnr.Add(p)
	fnr.Add(n)
	// Parent lookup must succeed
	b.StoreInfo.RootNodeID = p.ID

	if err := n.distributeToLeft(context.Background(), b, &Item[int, string]{Key: 999, Value: &[]string{"z"}[0], ID: sop.NewUUID()}); err != nil {
		t.Fatalf("unexpected error on early-return branch: %v", err)
	}
}

// moveToFirst: cover paths when first child is nil (stop early) and when a child exists (descend).
func TestMoveToFirst_Paths(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Case 1: first child nil -> stop at parent index 0
	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	v := "v"
	p.Slots[0] = Item[int, string]{Key: 5, Value: &v, ID: sop.NewUUID()}
	p.Count = 1
	p.ChildrenIDs = make([]sop.UUID, 2)
	p.ChildrenIDs[0] = sop.NilUUID
	fnr.Add(p)
	b.StoreInfo.RootNodeID = p.ID
	if ok, err := p.moveToFirst(context.Background(), b); err != nil || !ok {
		t.Fatalf("moveToFirst case1: %v", err)
	}
	if it, _ := b.GetCurrentItem(context.Background()); it.Key != 5 {
		t.Fatalf("want key 5 at parent, got %v", it.Key)
	}

	// Case 2: child exists -> descend to child index 0
	c := newNode[int, string](b.getSlotLength())
	c.newID(p.ID)
	c.Slots[0] = Item[int, string]{Key: 1, Value: &v, ID: sop.NewUUID()}
	c.Count = 1
	fnr.Add(c)
	p.ChildrenIDs[0] = c.ID
	if ok, err := p.moveToFirst(context.Background(), b); err != nil || !ok {
		t.Fatalf("moveToFirst case2: %v", err)
	}
	if it, _ := b.GetCurrentItem(context.Background()); it.Key != 1 {
		t.Fatalf("want key 1 at child, got %v", it.Key)
	}
}

// moveArrayElements: exercise bounds guard paths (no panics, no changes outside range).
func TestMoveArrayElements_Bounds(t *testing.T) {
	arr := []int{1, 2, 3}
	// Out-of-range indices should be ignored safely.
	moveArrayElements(arr, 5, 10, 2)
	if arr[0] != 1 || arr[1] != 2 || arr[2] != 3 {
		t.Fatalf("array unexpectedly changed: %v", arr)
	}
}

// FindWithID should walk duplicates and stop when the ID is matched; when not found it returns false,nil.
func TestFindWithID_DuplicateMatch_And_NotFound(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: false})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Root with three duplicates for key 7
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)

	var ids []sop.UUID
	for _, val := range []string{"a", "b", "c"} {
		id := sop.NewUUID()
		vv := val
		b.AddItem(context.Background(), &Item[int, string]{Key: 7, Value: &vv, ID: id})
		ids = append(ids, id)
	}
	// Also add a different key to ensure Next eventually ends
	v := "z"
	vv := v
	b.AddItem(context.Background(), &Item[int, string]{Key: 9, Value: &vv, ID: sop.NewUUID()})

	// Success: ask for the last duplicate's ID; requires stepping Next a few times
	ok, err := b.FindWithID(context.Background(), 7, ids[2])
	if err != nil || !ok {
		t.Fatalf("FindWithID expected true,nil; got ok=%v err=%v", ok, err)
	}

	// Not-found: ask for a random ID; should walk to end and return false,nil
	ok, err = b.FindWithID(context.Background(), 7, sop.NewUUID())
	if err != nil || ok {
		t.Fatalf("FindWithID not-found expected false,nil; got ok=%v err=%v", ok, err)
	}
}

// distributeToLeft when node is not full should append the item at the end and save the node.
func TestDistributeToLeft_NotFull_Appends(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, LeafLoadBalancing: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	// Partially filled leaf (no children)
	for i := 0; i < 2; i++ {
		v := "v"
		vv := v
		n.Slots[i] = Item[int, string]{Key: i + 1, Value: &vv, ID: sop.NewUUID()}
	}
	n.Count = 2
	fnr.Add(n)

	itemVal := "x"
	item := &Item[int, string]{Key: 99, Value: &itemVal, ID: sop.NewUUID()}
	if err := n.distributeToLeft(context.Background(), b, item); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.Count != 3 || n.Slots[2].Key != 99 {
		t.Fatalf("expected item appended at end; count=%d slot2=%v", n.Count, n.Slots[2])
	}
}

func TestFindWithID_KeyAbsent_ElseBranch(t *testing.T) {
	b, _ := newNonUniqueBtree[string]()
	// Tree is empty; ensure Find returns false and FindWithID returns (false,nil)
	if ok, err := b.FindWithID(nil, 123, sop.NewUUID()); err != nil || ok {
		t.Fatalf("expected (false,nil) when key absent, got ok=%v err=%v", ok, err)
	}
}

func TestRemoveCurrentItem_NoSelection_ReturnsFalse(t *testing.T) {
	b, _ := newTestBtree[string]()
	// No current selection
	if ok, err := b.RemoveCurrentItem(nil); err != nil || ok {
		t.Fatalf("expected (false,nil) when no selection, got ok=%v err=%v", ok, err)
	}
}

func TestRemoveCurrentItem_NodeNotFound_ReturnsFalse(t *testing.T) {
	b, _ := newTestBtree[string]()
	// Set selection to a non-existent node ID
	b.setCurrentItemID(sop.NewUUID(), 0)
	if ok, err := b.RemoveCurrentItem(nil); err != nil || ok {
		t.Fatalf("expected (false,nil) when node not found, got ok=%v err=%v", ok, err)
	}
}

func TestSiblings_HappyPaths(t *testing.T) {
	b, fnr := newTestBtree[string]()
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	mid := newNode[int, string](b.getSlotLength())
	mid.newID(parent.ID)
	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)

	parent.Count = 2 // two separators -> three children
	parent.ChildrenIDs = make([]sop.UUID, 3)
	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = mid.ID
	parent.ChildrenIDs[2] = right.ID
	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(mid)
	fnr.Add(right)

	if sib, err := mid.getLeftSibling(nil, b); err != nil || sib == nil || sib.ID != left.ID {
		t.Fatalf("expected left sibling to be left child, err=%v", err)
	}
	if sib, err := mid.getRightSibling(nil, b); err != nil || sib == nil || sib.ID != right.ID {
		t.Fatalf("expected right sibling to be right child, err=%v", err)
	}
}
