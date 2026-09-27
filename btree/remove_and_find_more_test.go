package btree

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// Covers RemoveCurrentItem path where node has children and we replace with next leaf item.
func TestRemoveCurrentItem_InternalReplaceThenLeafDelete(t *testing.T) {
	b, fnr, root := buildSmallTree()
	// Move to key 30 (middle in initial set) ensuring current is internal before deletion
	if ok, _ := b.Find(nil, 30, false); !ok {
		t.Fatal("find 30")
	}
	// Root should have children after buildSmallTree; removing 30 will exercise moveToNext and leaf delete path
	if n, _ := b.getNode(nil, b.currentItemRef.getNodeID()); !n.hasChildren() {
		t.Fatalf("expected current node to have children for internal delete path")
	}
	if ok, err := b.RemoveCurrentItem(nil); err != nil || !ok {
		t.Fatalf("RemoveCurrentItem err=%v ok=%v", err, ok)
	}
	if got := b.Count(); got != 4 {
		t.Fatalf("count after remove=%d", got)
	}
	// Sanity: key 30 should no longer be present
	if ok, _ := b.Find(nil, 30, false); ok {
		t.Fatalf("30 should be removed")
	}
	// Avoid unused warnings
	_ = fnr
	_ = root
}

// Covers FindWithID success for a non-first duplicate.
func TestFindWithID_DuplicateNonFirst(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: false})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)

	// Add duplicates for key 5
	ids := []sop.UUID{}
	for i := 0; i < 3; i++ {
		v := "v"
		vv := v
		id := sop.NewUUID()
		ids = append(ids, id)
		if ok, err := b.AddItem(nil, &Item[int, string]{Key: 5, Value: &vv, ID: id}); err != nil || !ok {
			t.Fatalf("add dup %d", i)
		}
	}
	// FindWithID should walk Next until matching id
	if ok, err := b.FindWithID(nil, 5, ids[2]); err != nil || !ok {
		t.Fatalf("FindWithID err=%v ok=%v", err, ok)
	}
	it, _ := b.GetCurrentItem(nil)
	if it.ID != ids[2] {
		t.Fatalf("expected id match on third duplicate")
	}
}

// Covers GetCurrentValue and GetCurrentItem when no item is selected.
func TestCurrent_Fetch_NoSelection(t *testing.T) {
	b, _ := newTestBtree[string]()
	// No selection: both should return zero values with no error
	if _, err := b.GetCurrentValue(nil); err != nil {
		t.Fatalf("GetCurrentValue unexpected err: %v", err)
	}
	if _, err := b.GetCurrentItem(nil); err != nil {
		t.Fatalf("GetCurrentItem unexpected err: %v", err)
	}
}

// Exercise removeItemOnNodeWithNilChild path where right child is nil (index+1) and node is non-root
// so it promotes its single child to parent, rewiring references.
func TestRemoveItemOnNodeWithNilChild_RightBranch_PromoteChild(t *testing.T) {
	b, fnr := newTestBtree[string]()
	// Parent with one slot -> two children
	parent := newNode[int, string](b.getSlotLength())
	parent.newID(sop.NilUUID)
	parent.Slots[0] = Item[int, string]{Key: 100, ID: sop.NewUUID()}
	parent.Count = 1

	// Target node as left child; it has one slot and exactly one non-nil child on the left,
	// and a nil right child to activate the right-branch in removeItemOnNodeWithNilChild.
	n := newNode[int, string](b.getSlotLength())
	n.newID(parent.ID)
	n.Slots[0] = Item[int, string]{Key: 10, ID: sop.NewUUID()}
	n.Count = 1
	n.ChildrenIDs = make([]sop.UUID, 2)
	// left child exists
	lc := newNode[int, string](b.getSlotLength())
	lc.newID(n.ID)
	n.ChildrenIDs[0] = lc.ID
	// right child nil
	n.ChildrenIDs[1] = sop.NilUUID

	// Wire parent children (n is left child, right child arbitrary leaf)
	rc := newNode[int, string](b.getSlotLength())
	rc.newID(parent.ID)
	parent.ChildrenIDs = []sop.UUID{n.ID, rc.ID}

	// Persist
	fnr.Add(parent)
	fnr.Add(n)
	fnr.Add(lc)
	fnr.Add(rc)

	// Remove at index 0 on n; it should shift via right-branch and then promote single child
	ok, err := n.removeItemOnNodeWithNilChild(nil, b, 0)
	if err != nil || !ok {
		t.Fatalf("removeItemOnNodeWithNilChild expected ok, err=%v ok=%v", err, ok)
	}
	// After promotion, parent's left child should be lc
	if parent.ChildrenIDs[0] != lc.ID {
		t.Fatalf("expected parent left child to be promoted child")
	}
}
