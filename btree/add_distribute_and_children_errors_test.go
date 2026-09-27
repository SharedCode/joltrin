package btree

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// Drive btree.Add through a distribute-to-left success path to cover Add + distribute controller.
func TestAdd_DistributeToLeft_Success_ViaAdd(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, LeafLoadBalancing: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Parent with three children [left, cur, right]; cur is full, left has room -> distribute left.
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 2
	parent.ChildrenIDs = make([]sop.UUID, 3)

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	// left has room
	lv := "l"
	// Left is full: [1,2,3,4]
	for i, k := range []int{1, 2, 3, 4} {
		vv := lv
		left.Slots[i] = Item[int, string]{Key: k, Value: &vv, ID: sop.NewUUID()}
	}
	left.Count = b.getSlotLength()

	cur := newNode[int, string](b.getSlotLength())
	cur.newID(parent.ID)
	// Make current node full
	for i := 0; i < b.getSlotLength(); i++ {
		v := "c"
		vv := v
		cur.Slots[i] = Item[int, string]{Key: 10 + i, Value: &vv, ID: sop.NewUUID()}
	}
	cur.Count = b.getSlotLength()

	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	// right can be anything; not used in left distribution

	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = cur.ID
	parent.ChildrenIDs[2] = right.ID
	// separators [slot0, slot1]
	s0 := "s0"
	s1 := "s1"
	// separator keys chosen between left and cur, and cur and right
	parent.Slots[0] = Item[int, string]{Key: 15, Value: &s0, ID: sop.NewUUID()}
	parent.Slots[1] = Item[int, string]{Key: 35, Value: &s1, ID: sop.NewUUID()}

	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(cur)
	fnr.Add(right)
	b.StoreInfo.RootNodeID = parent.ID
	b.StoreInfo.Count = 3 // arbitrary non-zero to indicate non-empty tree

	// Add a key that goes into cur (less than parent.Slots[1], greater than parent.Slots[0])
	// This triggers addOnLeaf full path -> schedule distribution -> Add() calls distribute() -> left append
	if ok, err := b.Add(context.Background(), 16, "x"); err != nil || !ok {
		t.Fatalf("Add distribute success expected; ok=%v err=%v", ok, err)
	}
	// Count should increment
	if b.Count() != 4 {
		t.Fatalf("expected Count=4 after Add, got %d", b.Count())
	}
	// Left should have received the distributed item (count increased)
	if left.Count < 2 {
		t.Fatalf("left sibling did not receive distributed item; count=%d", left.Count)
	}
}

// getChildren should propagate repository errors from fetching child IDs.
func TestGetChildren_ErrorPropagation(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	repo := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: repo, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	n.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	// Assign one child that will error when fetched
	bad := sop.NewUUID()
	n.ChildrenIDs[0] = bad
	repo.errs[bad] = true
	repo.Add(n)

	if _, err := n.getChildren(context.Background(), b); err == nil {
		t.Fatalf("expected error propagated from repo.Get")
	}
}

// Drive btree.Add through the promote controller path when the target leaf is full
// and no sibling has vacancy (leaf load balancing disabled). This exercises the
// "All slots are occupied ... prepare right sibling and promote" branch.
func TestAdd_Promote_AllFull_SplitsAndPromotes(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, LeafLoadBalancing: false})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Build a small tree: parent with left and cur children; cur is full.
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 1
	parent.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	sep := "sep"
	parent.Slots[0] = Item[int, string]{Key: 9, Value: &sep, ID: sop.NewUUID()}

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	l1, l2 := "l1", "l2"
	left.Slots[0] = Item[int, string]{Key: 1, Value: &l1, ID: sop.NewUUID()}
	left.Slots[1] = Item[int, string]{Key: 2, Value: &l2, ID: sop.NewUUID()}
	left.Count = 2

	cur := newNode[int, string](b.getSlotLength())
	cur.newID(parent.ID)
	// cur is full: [10,20,30,40]
	for i, k := range []int{10, 20, 30, 40} {
		v := "c"
		vv := v
		cur.Slots[i] = Item[int, string]{Key: k, Value: &vv, ID: sop.NewUUID()}
	}
	cur.Count = b.getSlotLength()

	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = cur.ID

	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(cur)
	b.StoreInfo.RootNodeID = parent.ID
	b.StoreInfo.Count = 2

	// Insert 25 so the full 'cur' splits and parent gets promoted separator 25.
	if ok, err := b.Add(context.Background(), 25, "x"); err != nil || !ok {
		t.Fatalf("Add promote success expected; ok=%v err=%v", ok, err)
	}

	if got, want := b.Count(), int64(3); got != want {
		t.Fatalf("expected Count=%d after Add, got %d", want, got)
	}

	// Reload parent and children from repo to observe post-promote structure.
	p, _ := b.getNode(context.Background(), parent.ID)
	if p.Count != 2 {
		t.Fatalf("expected parent.Count=2 after promote, got %d", p.Count)
	}
	// One of parent's slots should be 25 (the promoted separator).
	if !(p.Count > 0 && p.Slots[0].Key == 9 || p.Count > 1 && p.Slots[1].Key == 25) &&
		!(p.Count > 0 && p.Slots[0].Key == 25 || p.Count > 1 && p.Slots[1].Key == 9) {
		t.Fatalf("parent slots do not contain expected keys 9 and 25: got [%v,%v]",
			slotKey(p.Slots[0]), slotKey(p.Slots[1]))
	}

	// cur should now have only left half [10,20].
	c, _ := b.getNode(context.Background(), cur.ID)
	if c.Count != 2 || c.Slots[0].Key != 10 || c.Slots[1].Key != 20 {
		t.Fatalf("expected cur=[10,20], got count=%d k0=%v k1=%v", c.Count, slotKey(c.Slots[0]), slotKey(c.Slots[1]))
	}
	// The new right sibling is at parent.ChildrenIDs[2]; it should have [30,40].
	rid := p.ChildrenIDs[2]
	if rid.IsNil() {
		t.Fatalf("expected right sibling child at index 2")
	}
	r, _ := b.getNode(context.Background(), rid)
	if r == nil || r.Count != 2 || r.Slots[0].Key != 30 || r.Slots[1].Key != 40 {
		t.Fatalf("expected right sibling=[30,40], got count=%v k0=%v k1=%v", rCount(r), slotKey(r.Slots[0]), slotKey(r.Slots[1]))
	}
}

// helpers for succinct error messages
func slotKey[T any](it Item[int, T]) any {
	return it.Key
}
func rCount[T any](n *Node[int, T]) any {
	if n == nil {
		return nil
	}
	return n.Count
}

// When the target right child is nil and the node is not root, the helper should
// climb to the parent and select the parent's separator at that index.
func TestGoRightUpItemOnNodeWithNilChild_ClimbAndSelectParent(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	repo := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: repo, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Parent with two separators and three children, focus on middle child 'cur'.
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 2
	parent.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	s0, s1 := "s0", "s1"
	parent.Slots[0] = Item[int, string]{Key: 5, Value: &s0, ID: sop.NewUUID()}
	parent.Slots[1] = Item[int, string]{Key: 15, Value: &s1, ID: sop.NewUUID()}

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	l := "l"
	left.Slots[0] = Item[int, string]{Key: 1, Value: &l, ID: sop.NewUUID()}
	left.Count = 1

	cur := newNode[int, string](b.getSlotLength())
	cur.newID(parent.ID)
	cur.Count = 1
	cur.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	// Make the target child at index==1 nil to trigger upward walk.
	cur.ChildrenIDs[1] = sop.NilUUID

	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	r := "r"
	right.Slots[0] = Item[int, string]{Key: 20, Value: &r, ID: sop.NewUUID()}
	right.Count = 1

	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = cur.ID
	parent.ChildrenIDs[2] = right.ID

	repo.Add(parent)
	repo.Add(left)
	repo.Add(cur)
	repo.Add(right)
	b.StoreInfo.RootNodeID = parent.ID

	// Request moving right from cur at index 1; should select parent.Slots[1] (key 15) and return true.
	ok, err := cur.goRightUpItemOnNodeWithNilChild(context.Background(), b, 1)
	if err != nil || !ok {
		t.Fatalf("expected ok=true err=nil, got ok=%v err=%v", ok, err)
	}
	// Fetch current key to verify selection.
	if item, _ := b.getCurrentItem(context.Background()); item == nil || item.Key != 15 {
		t.Fatalf("expected current key=15 from parent separator, got %v", slotKey(*item))
	}
}

// Adding a duplicate key on a unique tree where the target index equals Count should
// detect the duplicate at the last slot and return ok=false with current item set.
func TestAdd_DuplicateAtEnd_Unique_ReturnsFalse(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	repo := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: repo, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Single leaf with [10,20]
	leaf := newNode[int, string](b.getSlotLength())
	leaf.newID(sop.NilUUID)
	v1, v2 := "a", "b"
	leaf.Slots[0] = Item[int, string]{Key: 10, Value: &v1, ID: sop.NewUUID()}
	leaf.Slots[1] = Item[int, string]{Key: 20, Value: &v2, ID: sop.NewUUID()}
	leaf.Count = 2
	repo.Add(leaf)
	b.StoreInfo.RootNodeID = leaf.ID
	b.StoreInfo.Count = 2

	ok, err := b.Add(context.Background(), 20, "dup")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if ok {
		t.Fatalf("expected ok=false for duplicate insert on unique tree")
	}
	if b.Count() != 2 {
		t.Fatalf("store count changed on duplicate; got %d", b.Count())
	}
	// Ensure current item points to the existing 20
	item, err := b.getCurrentItem(context.Background())
	if err != nil || item == nil || item.Key != 20 {
		t.Fatalf("expected current item key=20; got item=%v err=%v", item, err)
	}
}

// Drive btree.Add through a distribute-to-right success path to cover Add + distribute controller.
func TestDistributeToRight_Rotation_Full(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, LeafLoadBalancing: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Parent with three children [left, cur, right]; cur is full, right has room -> distribute right.
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 2
	parent.ChildrenIDs = make([]sop.UUID, 3)

	left := newNode[int, string](b.getSlotLength())
	left.newID(parent.ID)
	lv := "l"
	left.Slots[0] = Item[int, string]{Key: 1, Value: &lv, ID: sop.NewUUID()}
	left.Count = 1

	cur := newNode[int, string](b.getSlotLength())
	cur.newID(parent.ID)
	// Make current node full with ascending keys [20,30,40,50]
	for i, k := range []int{20, 30, 40, 50} {
		v := "c"
		vv := v
		cur.Slots[i] = Item[int, string]{Key: k, Value: &vv, ID: sop.NewUUID()}
	}
	cur.Count = b.getSlotLength()

	right := newNode[int, string](b.getSlotLength())
	right.newID(parent.ID)
	// Right has room at front (empty)

	parent.ChildrenIDs[0] = left.ID
	parent.ChildrenIDs[1] = cur.ID
	parent.ChildrenIDs[2] = right.ID
	// separators [slot0, slot1]
	s0 := "s0"
	s1 := "s1"
	parent.Slots[0] = Item[int, string]{Key: 15, Value: &s0, ID: sop.NewUUID()}
	parent.Slots[1] = Item[int, string]{Key: 60, Value: &s1, ID: sop.NewUUID()}

	fnr.Add(parent)
	fnr.Add(left)
	fnr.Add(cur)
	fnr.Add(right)
	b.StoreInfo.RootNodeID = parent.ID
	b.StoreInfo.Count = 3

	// Directly exercise distributeToRight rotation and controller scheduling
	item := &Item[int, string]{Key: 18, Value: &[]string{"x"}[0], ID: sop.NewUUID()}
	if err := cur.distributeToRight(context.Background(), b, item); err != nil {
		t.Fatalf("distributeToRight rotation err: %v", err)
	}
	// Controller prepared: next distribution should be to right sibling with old parent separator
	if b.distributeAction.sourceNode == nil || b.distributeAction.sourceNode.ID != right.ID || b.distributeAction.distributeToLeft {
		t.Fatalf("expected distribution scheduled to right sibling")
	}
	// Parent slot1 updated to cur's last (50)
	if parent.Slots[1].Key != 50 {
		t.Fatalf("parent slot1 not updated; got %v want 50", parent.Slots[1].Key)
	}
	// Service the scheduled distribution to actually move the item into right sibling.
	if err := b.distribute(context.Background()); err != nil {
		t.Fatalf("distribute err: %v", err)
	}
	r2, _ := b.getNode(context.Background(), right.ID)
	if r2.Count != 1 || r2.Count == 0 || r2.Slots[0].Key != 60 {
		t.Fatalf("right sibling did not receive scheduled item; count=%d key0=%v", r2.Count, slotKey(r2.Slots[0]))
	}
}

// When the target right child at index is nil and i < node.Count,
// the helper should select the in-node item at that index and return true (no climb).
func TestGoRightUpItemOnNodeWithNilChild_SelectInNode(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	repo := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: repo, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	n := newNode[int, string](b.getSlotLength())
	n.newID(sop.NilUUID)
	v1, v2 := "a", "b"
	n.Slots[0] = Item[int, string]{Key: 10, Value: &v1, ID: sop.NewUUID()}
	n.Slots[1] = Item[int, string]{Key: 20, Value: &v2, ID: sop.NewUUID()}
	n.Count = 2
	n.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	// Make child at index 1 nil to trigger short path
	n.ChildrenIDs[1] = sop.NilUUID

	repo.Add(n)
	b.StoreInfo.RootNodeID = n.ID

	ok, err := n.goRightUpItemOnNodeWithNilChild(context.Background(), b, 1)
	if err != nil || !ok {
		t.Fatalf("expected ok=true err=nil; got ok=%v err=%v", ok, err)
	}
	it, _ := b.GetCurrentItem(context.Background())
	if it.Key != 20 {
		t.Fatalf("expected current key=20; got %v", it.Key)
	}
}

// When trying to move right but the child is nil and the node is the rightmost child up to the root,
// goRightUpItemOnNodeWithNilChild should return false and clear current selection (end of tree).
// promoteSingleChildAsParentChild should propagate parent fetch errors.
func TestPromoteSingleChildAsParentChild_GetParentError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	repo := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: repo, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Count = 1
	parent.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)

	child := newNode[int, string](b.getSlotLength())
	child.newID(parent.ID)
	child.Count = 0
	child.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	// single grandchild to satisfy precondition; value doesn't matter for this branch
	gc := newNode[int, string](b.getSlotLength())
	gc.newID(child.ID)
	child.ChildrenIDs[0] = gc.ID

	// Wire parent->child
	parent.ChildrenIDs[0] = child.ID

	// Add to repo and set error on fetching parent.
	repo.Add(gc)
	repo.Add(child)
	repo.Add(parent)
	// Force getParent to error by making repo.Get(parent.ID) fail
	repo.errs[parent.ID] = true

	if ok, err := child.promoteSingleChildAsParentChild(context.Background(), b); err == nil || ok {
		t.Fatalf("expected error from getParent, got ok=%v err=%v", ok, err)
	}
}

// When a node has children and the target child slot is nil, add() should create
// a new child at that position and insert the item there via addItemOnNodeWithNilChild.
func TestAdd_CreatesChildWhenNilChildAtIndex(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	repo := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: repo, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	// Root has children array but no separators; child slot 0 is nil.
	root.ChildrenIDs = make([]sop.UUID, b.getSlotLength()+1)
	repo.Add(root)
	b.StoreInfo.RootNodeID = root.ID

	ok, err := b.Add(context.Background(), 10, "v")
	if err != nil || !ok {
		t.Fatalf("expected Add to succeed via addItemOnNodeWithNilChild; ok=%v err=%v", ok, err)
	}
	if b.Count() != 1 {
		t.Fatalf("store count not incremented; got %d", b.Count())
	}
	// Root should now point to a created child at index 0 containing the item.
	r, _ := b.getNode(context.Background(), root.ID)
	cid := r.ChildrenIDs[0]
	if cid.IsNil() {
		t.Fatalf("expected child created at index 0")
	}
	c, _ := b.getNode(context.Background(), cid)
	if c == nil || c.Count != 1 || c.Slots[0].Key != 10 {
		t.Fatalf("expected child with single item key=10; got count=%v key0=%v", rCount(c), slotKey(c.Slots[0]))
	}
}
