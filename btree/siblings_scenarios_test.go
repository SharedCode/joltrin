package btree

// Consolidated from: siblings_more_test.go, siblings_table_test.go
import (
	"context"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func TestGetLeftRightSibling_Edges(t *testing.T) {
	b, fnr := newTestBtree[string]()
	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	p.Count = 2
	// Three children; test leftmost and rightmost sibling returns nil
	c0 := newNode[int, string](b.getSlotLength())
	c0.newID(p.ID)
	c1 := newNode[int, string](b.getSlotLength())
	c1.newID(p.ID)
	c2 := newNode[int, string](b.getSlotLength())
	c2.newID(p.ID)
	p.ChildrenIDs = []sop.UUID{c0.ID, c1.ID, c2.ID}
	fnr.Add(p)
	fnr.Add(c0)
	fnr.Add(c1)
	fnr.Add(c2)

	if ls, err := c0.getLeftSibling(nil, b); err != nil || ls != nil {
		t.Fatalf("leftmost should have no left sibling")
	}
	if rs, err := c2.getRightSibling(nil, b); err != nil || rs != nil {
		t.Fatalf("rightmost should have no right sibling")
	}
}

func TestSiblings_Table(t *testing.T) {
	b, fnr := newTestBtree[string]()
	// Build parent with three children
	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = p.ID
	p.Slots[0] = Item[int, string]{Key: 10, Value: &[]string{"a"}[0], ID: sop.NewUUID()}
	p.Slots[1] = Item[int, string]{Key: 20, Value: &[]string{"b"}[0], ID: sop.NewUUID()}
	p.Count = 2
	c0 := newNode[int, string](b.getSlotLength())
	c0.newID(p.ID)
	c1 := newNode[int, string](b.getSlotLength())
	c1.newID(p.ID)
	c2 := newNode[int, string](b.getSlotLength())
	c2.newID(p.ID)
	p.ChildrenIDs = make([]sop.UUID, 3)
	p.ChildrenIDs[0] = c0.ID
	p.ChildrenIDs[1] = c1.ID
	p.ChildrenIDs[2] = c2.ID
	fnr.Add(p)
	fnr.Add(c0)
	fnr.Add(c1)
	fnr.Add(c2)

	tests := []struct {
		name    string
		child   *Node[int, string]
		left    bool
		wantNil bool
	}{
		{"leftmost has no left", c0, true, true},
		{"middle has left", c1, true, false},
		{"rightmost has no right", c2, false, true},
		{"middle has right", c1, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.left {
				n, err := tc.child.getLeftSibling(nil, b)
				_ = err
				if (n == nil) != tc.wantNil {
					t.Fatalf("left sibling nil=%v want %v", n == nil, tc.wantNil)
				}
			} else {
				n, err := tc.child.getRightSibling(nil, b)
				_ = err
				if (n == nil) != tc.wantNil {
					t.Fatalf("right sibling nil=%v want %v", n == nil, tc.wantNil)
				}
			}
		})
	}
}

// Repo that errors on specific IDs via Get.
type repoErrOnIDs[TK Ordered, TV any] struct {
	fakeNR[TK, TV]
	errs map[sop.UUID]bool
}

func (r *repoErrOnIDs[TK, TV]) Get(ctx context.Context, id sop.UUID) (*Node[TK, TV], error) {
	if r.errs[id] {
		return nil, fmt.Errorf("forced get error for %v", id)
	}
	return r.fakeNR.Get(ctx, id)
}

func TestGetSibling_PropagatesParentGetError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	repo := &repoErrOnIDs[int, string]{fakeNR: fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: repo, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	parentID := sop.NewUUID()
	// Any attempt to fetch parent should error
	repo.errs[parentID] = true

	child := newNode[int, string](b.getSlotLength())
	child.ID = sop.NewUUID()
	child.ParentID = parentID
	repo.Add(child)

	if _, err := child.getLeftSibling(nil, b); err == nil {
		t.Fatalf("expected error from getLeftSibling via parent.Get")
	}
	if _, err := child.getRightSibling(nil, b); err == nil {
		t.Fatalf("expected error from getRightSibling via parent.Get")
	}
}

func TestGetLeftSibling_RightmostHasLeft(t *testing.T) {
	b, fnr := newTestBtree[string]()
	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	p.Count = 2 // three children
	c0 := newNode[int, string](b.getSlotLength())
	c0.newID(p.ID)
	c1 := newNode[int, string](b.getSlotLength())
	c1.newID(p.ID)
	c2 := newNode[int, string](b.getSlotLength())
	c2.newID(p.ID)
	p.ChildrenIDs = []sop.UUID{c0.ID, c1.ID, c2.ID}
	fnr.Add(p)
	fnr.Add(c0)
	fnr.Add(c1)
	fnr.Add(c2)

	if ls, err := c2.getLeftSibling(nil, b); err != nil || ls == nil || ls.ID != c1.ID {
		t.Fatalf("rightmost left sibling expected mid child, err=%v", err)
	}
}

func TestGetRightSibling_LeftmostHasRight(t *testing.T) {
	b, fnr := newTestBtree[string]()
	p := newNode[int, string](b.getSlotLength())
	p.newID(sop.NilUUID)
	p.Count = 2 // three children
	c0 := newNode[int, string](b.getSlotLength())
	c0.newID(p.ID)
	c1 := newNode[int, string](b.getSlotLength())
	c1.newID(p.ID)
	c2 := newNode[int, string](b.getSlotLength())
	c2.newID(p.ID)
	p.ChildrenIDs = []sop.UUID{c0.ID, c1.ID, c2.ID}
	fnr.Add(p)
	fnr.Add(c0)
	fnr.Add(c1)
	fnr.Add(c2)

	if rs, err := c0.getRightSibling(nil, b); err != nil || rs == nil || rs.ID != c1.ID {
		t.Fatalf("leftmost right sibling expected mid child, err=%v", err)
	}
}

// Ensure getLeftSibling/getRightSibling return nil when called on a root node (no parent).
func TestSiblings_OnRoot_ReturnNil(t *testing.T) {
	b, fnr := newTestBtree[string]()
	root := newNode[int, string](b.getSlotLength())
	// Root has no parent by default; just register it so repo can resolve it if needed.
	root.newID(sop.NilUUID)
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID

	if ls, err := root.getLeftSibling(nil, b); err != nil || ls != nil {
		t.Fatalf("root getLeftSibling should be nil, err=%v ls=%v", err, ls)
	}
	if rs, err := root.getRightSibling(nil, b); err != nil || rs != nil {
		t.Fatalf("root getRightSibling should be nil, err=%v rs=%v", err, rs)
	}
}
