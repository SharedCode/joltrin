package btree

// Scenario file merged from: additem_duplicate_test.go, additem_error_test.go, additem_more_test.go
// NOTE: Pure content merge; no refactors. Original files removed.

import (
	"context"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// (from additem_duplicate_test.go)
// Ensure AddItem returns false on duplicate when IsUnique=true.
func TestAddItem_DuplicateUnique_ReturnsFalse(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)

	// Seed an item with key=7
	if ok, err := b.Add(nil, 7, "a"); err != nil || !ok {
		t.Fatalf("seed add err=%v ok=%v", err, ok)
	}
	// Attempt AddItem with same key, distinct ID; should be rejected when unique
	vv := "b"
	dup := &Item[int, string]{Key: 7, Value: &vv, ID: sop.NewUUID()}
	if ok, err := b.AddItem(nil, dup); err != nil {
		t.Fatalf("AddItem duplicate err: %v", err)
	} else if ok {
		t.Fatalf("expected AddItem duplicate to return false")
	}
}

// (from additem_error_test.go)
// Ensure AddItem returns an error when fetching the root node fails.
func TestAddItem_RootFetchError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Pre-create a root ID and set Count > 0 to force getRootNode to fetch via repo
	rootID := sop.NewUUID()
	b.StoreInfo.RootNodeID = rootID
	b.StoreInfo.Count = 1

	// Force repo to error for root fetch
	fnr.errs[rootID] = true

	v := "v"
	vv := v
	if ok, err := b.AddItem(nil, &Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}); err == nil || ok {
		t.Fatalf("expected AddItem to fail on root fetch error, got ok=%v err=%v", ok, err)
	}
}

// (from additem_error_test.go)
// Cover the path where AddItem triggers distribute and an error occurs during distribution.
func TestAddItem_DistributeError_Propagates(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, LeafLoadBalancing: true})
	fnr := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Build a parent with two children: left (full) and right (has vacancy)
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 1

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	// Make left full
	v := "v"
	vv := v
	for i := 0; i < b.getSlotLength(); i++ {
		left.Slots[i] = Item[int, string]{Key: 10 + i, Value: &vv, ID: sop.NewUUID()}
	}
	left.Count = b.getSlotLength()

	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	// right has vacancy (Count 0)

	parent.ChildrenIDs = make([]sop.UUID, 2)
	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = right.ID
	// Set a valid separator key on parent slot 0 to avoid nil deref during add traversal
	parent.Slots[0] = Item[int, string]{Key: 60, Value: &vv, ID: sop.NewUUID()}

	// Add to repo
	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(right)

	// Mark the right sibling to error when fetched during distributeToRight
	fnr.errs[right.ID] = true

	// Wire as the B-tree root
	b.StoreInfo.RootNodeID = parent.ID
	b.StoreInfo.Count = int64(parent.Count)

	// Add an item that will go into the left child and trigger distribution to right
	// Choose a key that will insert at the beginning (forcing tempSlots[0] path)
	if ok, err := b.AddItem(nil, &Item[int, string]{Key: 5, Value: &vv, ID: sop.NewUUID()}); err == nil || ok {
		t.Fatalf("expected AddItem to propagate distribute error, got ok=%v err=%v", ok, err)
	}
}

// (from additem_more_test.go)
// Covers AddItem path including distribute() and promote() controller loops.
func TestAddItem_CoversDistributeAndPromote(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true, LeafLoadBalancing: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Create root
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)

	// Insert enough items via AddItem to trigger split and possible distribution
	vals := []int{10, 20, 30, 40, 50, 60}
	for _, k := range vals {
		v := "v"
		vv := v
		ok, err := b.AddItem(nil, &Item[int, string]{Key: k, Value: &vv, ID: sop.NewUUID()})
		if err != nil || !ok {
			t.Fatalf("AddItem %d err=%v ok=%v", k, err, ok)
		}
	}
	if got := b.Count(); got != int64(len(vals)) {
		t.Fatalf("count=%d", got)
	}
	// Ensure tree has grown to have children at root (promote happened)
	rn, _ := b.getNode(nil, b.StoreInfo.RootNodeID)
	if !rn.hasChildren() {
		t.Fatalf("expected root to have children after AddItem operations")
	}
}

// helper: fill a node with sequential keys starting at base
func fillFullNode(b *Btree[int, string], n *Node[int, string], base int) {
	v := "v"
	for i := 0; i < b.getSlotLength(); i++ {
		vv := v
		n.Slots[i] = Item[int, string]{Key: base + i, Value: &vv, ID: sop.NewUUID()}
	}
	n.Count = b.getSlotLength()
}

func TestAddOnLeaf_NotFull_InsertsAndShifts(t *testing.T) {
	b, fnr := newTestBtree[string]()
	leaf := newNode[int, string](b.getSlotLength())
	leaf.newID(sop.NewUUID())
	// Preload two items [10, 30] leaving a gap at index 1
	v := "v"
	vv := v
	leaf.Slots[0] = Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()}
	leaf.Slots[1] = Item[int, string]{Key: 30, Value: &vv, ID: sop.NewUUID()}
	leaf.Count = 2
	fnr.Add(leaf)
	// Insert 20 at index 1
	item := &Item[int, string]{Key: 20, Value: &vv, ID: sop.NewUUID()}
	if err := leaf.addOnLeaf(nil, b, item, 1); err != nil {
		t.Fatalf("addOnLeaf err: %v", err)
	}
	if leaf.Count != 3 || leaf.Count < 2 || leaf.Slots[1].Key != 20 {
		t.Fatalf("expected insert+shift; got count=%d mid=%v", leaf.Count, leaf.Slots[1])
	}
}

func TestAddOnLeaf_Full_VacancyLeft_SetsDistributeLeft(t *testing.T) {
	b, fnr := newTestBtree[string]()
	b.StoreInfo.LeafLoadBalancing = true
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 2

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	// left has room
	v := "v"
	vv := v
	left.Slots[0] = Item[int, string]{Key: 1, Value: &vv, ID: sop.NewUUID()}
	left.Count = 1

	cur := newNode[int, string](b.getSlotLength())
	cur.newID(parent.ID)
	fillFullNode(b, cur, 10)

	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	fillFullNode(b, right, 100)

	parent.ChildrenIDs = make([]sop.UUID, 3)
	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = cur.ID
	parent.ChildrenIDs[2] = right.ID
	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(cur)
	fnr.Add(right)

	item := &Item[int, string]{Key: 15, Value: &vv, ID: sop.NewUUID()}
	if err := cur.addOnLeaf(nil, b, item, 1); err != nil {
		t.Fatalf("addOnLeaf err: %v", err)
	}
	if b.distributeAction.sourceNode != cur || !b.distributeAction.distributeToLeft || b.distributeAction.item.ID.IsNil() {
		t.Fatalf("expected distribute left to be set")
	}
}

func TestAddOnLeaf_Full_VacancyRight_SetsDistributeRight(t *testing.T) {
	b, fnr := newTestBtree[string]()
	b.StoreInfo.LeafLoadBalancing = true
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 2

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	fillFullNode(b, left, 1)

	cur := newNode[int, string](b.getSlotLength())
	cur.newID(parent.ID)
	fillFullNode(b, cur, 10)

	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	// right has room
	v := "v"
	vv := v
	right.Slots[0] = Item[int, string]{Key: 200, Value: &vv, ID: sop.NewUUID()}
	right.Count = 1

	parent.ChildrenIDs = make([]sop.UUID, 3)
	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = cur.ID
	parent.ChildrenIDs[2] = right.ID
	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(cur)
	fnr.Add(right)

	item := &Item[int, string]{Key: 15, Value: &vv, ID: sop.NewUUID()}
	if err := cur.addOnLeaf(nil, b, item, 1); err != nil {
		t.Fatalf("addOnLeaf err: %v", err)
	}
	if b.distributeAction.sourceNode != cur || b.distributeAction.distributeToLeft || b.distributeAction.item.ID.IsNil() {
		t.Fatalf("expected distribute right to be set")
	}
}

func TestAddOnLeaf_Full_Unbalanced_SplitsIntoChildren(t *testing.T) {
	b, fnr := newTestBtree[string]()
	b.StoreInfo.LeafLoadBalancing = true
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 1

	cur := newNode[int, string](b.getSlotLength())
	cur.newID(parent.ID)
	fillFullNode(b, cur, 10)

	// Make a right sibling that is an inner node (ChildrenIDs non-nil) to mark branch unbalanced via right-side scan.
	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	right.ChildrenIDs = make([]sop.UUID, 1)
	// avoid nodeHasNilChild shortcut; mark as inner node without nil child
	right.ChildrenIDs[0] = sop.NewUUID()

	parent.ChildrenIDs = make([]sop.UUID, 2)
	parent.ChildrenIDs[0] = cur.ID
	parent.ChildrenIDs[1] = right.ID
	fnr.Add(parent)
	fnr.Add(right)
	fnr.Add(cur)

	v := "v"
	vv := v
	item := &Item[int, string]{Key: 15, Value: &vv, ID: sop.NewUUID()}
	if err := cur.addOnLeaf(nil, b, item, 1); err != nil {
		t.Fatalf("addOnLeaf err: %v", err)
	}

	// cur should now have children (split into two); do not assert exact count due to implementation detail
	if cur.ChildrenIDs == nil || len(cur.ChildrenIDs) == 0 || cur.ChildrenIDs[0] == sop.NilUUID || cur.ChildrenIDs[1] == sop.NilUUID {
		t.Fatalf("expected split into children; children=%v", cur.ChildrenIDs)
	}
}

func TestAddOnLeaf_Full_AllFull_PromoteRequested(t *testing.T) {
	b, fnr := newTestBtree[string]()
	b.StoreInfo.LeafLoadBalancing = true
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 2

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	fillFullNode(b, left, 1)
	cur := newNode[int, string](b.getSlotLength())
	cur.newID(parent.ID)
	fillFullNode(b, cur, 10)
	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	fillFullNode(b, right, 100)

	parent.ChildrenIDs = make([]sop.UUID, 3)
	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = cur.ID
	parent.ChildrenIDs[2] = right.ID
	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(cur)
	fnr.Add(right)

	v := "v"
	vv := v
	item := &Item[int, string]{Key: 15, Value: &vv, ID: sop.NewUUID()}
	if err := cur.addOnLeaf(nil, b, item, 1); err != nil {
		t.Fatalf("addOnLeaf err: %v", err)
	}

	if b.promoteAction.targetNode == nil || b.promoteAction.targetNode.ID != parent.ID {
		t.Fatalf("expected promote action to target parent")
	}
}

func TestAddOnLeaf_Full_RootBreaksIntoChildren(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	fillFullNode(b, root, 10)
	fnr.Add(root)

	v := "v"
	vv := v
	item := &Item[int, string]{Key: 15, Value: &vv, ID: sop.NewUUID()}
	if err := root.addOnLeaf(nil, b, item, 1); err != nil {
		t.Fatalf("addOnLeaf err: %v", err)
	}

	if root.ChildrenIDs == nil || root.Count != 1 {
		t.Fatalf("expected root to split into two children; count=%d children=%v", root.Count, root.ChildrenIDs)
	}
}

type iatAddErr[TK Ordered, TV any] struct{}

func (iatAddErr[TK, TV]) Add(ctx context.Context, item *Item[TK, TV]) error {
	return fmt.Errorf("iat add error")
}
func (iatAddErr[TK, TV]) Update(ctx context.Context, item *Item[TK, TV]) error { return nil }
func (iatAddErr[TK, TV]) Remove(ctx context.Context, item *Item[TK, TV]) error { return nil }
func (iatAddErr[TK, TV]) Get(ctx context.Context, item *Item[TK, TV]) error    { return nil }

// Covers btree.Add error branch when ItemActionTracker.Add returns an error after a successful node.add
func TestAdd_ItemActionTrackerError_Alt(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: iatAddErr[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	if ok, err := b.Add(nil, 1, "a"); err == nil || ok {
		t.Fatalf("expected error from IAT.Add, got ok=%v err=%v", ok, err)
	}
	if b.Count() != 0 {
		t.Fatalf("count should remain 0 on Add error, got %d", b.Count())
	}
}

// Cover btree.Add duplicate path when IsUnique=true: second Add returns (false, nil) and Count unchanged.
func TestAdd_Duplicate_ReturnsFalse(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	if ok, err := b.Add(context.Background(), 42, "a"); err != nil || !ok {
		t.Fatalf("first Add should succeed: ok=%v err=%v", ok, err)
	}
	// Duplicate key
	ok, err := b.Add(context.Background(), 42, "b")
	if err != nil || ok {
		t.Fatalf("duplicate Add should return false,nil; got ok=%v err=%v", ok, err)
	}
	if b.Count() != 1 {
		t.Fatalf("Count should remain 1 after duplicate Add, got %d", b.Count())
	}
}
