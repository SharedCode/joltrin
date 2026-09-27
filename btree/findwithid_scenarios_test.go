package btree

// Scenario file merged from: findwithid_success_test.go, findwithid_error_test.go, findwithid_more_test.go, findwithid_table_test.go
// NOTE: Pure content merge; originals removed.

import (
	"context"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

// (from findwithid_success_test.go)
// Ensure FindWithID returns true when duplicates exist and one matches later.
func TestFindWithID_Duplicates_Success(t *testing.T) {
	b, _ := newTestBtree[string]()
	// Allow duplicates for this test
	b.StoreInfo.IsUnique = false
	v := "v"
	vv := v
	a := sop.NewUUID()
	b1 := sop.NewUUID()
	c := sop.NewUUID()
	// Insert three duplicates; ensure the match is the middle to force iteration
	if _, err := b.AddItem(nil, &Item[int, string]{Key: 10, Value: &vv, ID: a}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddItem(nil, &Item[int, string]{Key: 10, Value: &vv, ID: b1}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.AddItem(nil, &Item[int, string]{Key: 10, Value: &vv, ID: c}); err != nil {
		t.Fatal(err)
	}

	if ok, err := b.FindWithID(nil, 10, b1); !ok || err != nil {
		t.Fatalf("expected true,nil matching middle duplicate, got ok=%v err=%v", ok, err)
	}
}

// (from findwithid_error_test.go)
// Simulate Next() error propagation inside FindWithID loop.
func TestFindWithID_NextError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: false})
	fnr := &fakeNRSelectiveErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Construct a root with two duplicates and a right child setup so Next will attempt to fetch it and error.
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	vv := v
	id1, id2 := sop.NewUUID(), sop.NewUUID()
	root.Slots[0] = Item[int, string]{Key: 5, Value: &vv, ID: id1}
	root.Slots[1] = Item[int, string]{Key: 5, Value: &vv, ID: id2}
	root.Count = 2

	// Add a right child id to force a right-walk on Next and inject an error on fetching that child
	right := newNode[int, string](b.getSlotLength())
	right.newID(root.ID)
	root.ChildrenIDs = make([]sop.UUID, 3)
	root.ChildrenIDs[0] = sop.NilUUID
	root.ChildrenIDs[1] = sop.NilUUID
	root.ChildrenIDs[2] = right.ID

	fnr.Add(root)
	fnr.Add(right)
	// Next will attempt to traverse right child when positioned at index 1; force error
	fnr.errs[right.ID] = true

	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 2

	// Start search for the first duplicate and then walk to find an arbitrary new ID, which will loop to Next
	if ok, err := b.FindWithID(nil, 5, sop.NewUUID()); ok || err == nil {
		t.Fatalf("expected FindWithID to propagate Next error; got ok=%v err=%v", ok, err)
	}
}

// Ensure GetCurrentItem returns zero item when current slot is nil.
func TestGetCurrentItem_SelectedSlotNil_ReturnsZero(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	// Intentionally leave slot 0 nil but set Count to 1 to simulate an empty slot selection
	root.Count = 1
	fnr.Add(root)
	b.StoreInfo.RootNodeID = root.ID

	// Select the nil slot
	b.setCurrentItemID(root.ID, 0)
	it, err := b.GetCurrentItem(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if it.ID != sop.NilUUID || it.Value != nil {
		t.Fatalf("expected zero Item when selected slot is nil")
	}
}

// (from findwithid_more_test.go)
// Duplicates exist but the requested ID is never matched; ensure FindWithID returns false.
func TestFindWithID_Duplicates_NotFound(t *testing.T) {
	b, _ := newTestBtree[string]()
	// Insert duplicate keys with different IDs using AddItem to bypass uniqueness check
	v := "v"
	vv := v
	for i := 0; i < 3; i++ {
		_, err := b.AddItem(nil, &Item[int, string]{Key: 10, Value: &vv, ID: sop.NewUUID()})
		if err != nil {
			t.Fatalf("add dup %d: %v", i, err)
		}
	}
	// Pick a random non-existent ID
	missing := sop.NewUUID()
	if ok, err := b.FindWithID(nil, 10, missing); err != nil || ok {
		t.Fatalf("expected false,nil when ID not found among duplicates")
	}
}

// (from findwithid_table_test.go)
func TestFindWithID_TableMore(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: false})
	fnr := &fakeNR[int, string]{n: map[sop.UUID]*Node[int, string]{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Root node
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	b.StoreInfo.RootNodeID = root.ID
	fnr.Add(root)

	// Insert three duplicates with known IDs
	ids := []sop.UUID{sop.NewUUID(), sop.NewUUID(), sop.NewUUID()}
	for _, id := range ids {
		v := "v"
		vv := v
		if ok, err := b.AddItem(nil, &Item[int, string]{Key: 5, Value: &vv, ID: id}); !ok || err != nil {
			t.Fatalf("seed dup add err=%v", err)
		}
	}

	tests := []struct {
		name   string
		id     sop.UUID
		wantOK bool
		wantID sop.UUID
	}{
		{"first", ids[0], true, ids[0]},
		{"middle", ids[1], true, ids[1]},
		{"last", ids[2], true, ids[2]},
		{"notfound", sop.NewUUID(), false, sop.NilUUID},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, err := b.FindWithID(nil, 5, tc.id)
			if err != nil {
				t.Fatalf("FindWithID err: %v", err)
			}
			if ok != tc.wantOK {
				t.Fatalf("ok=%v want %v", ok, tc.wantOK)
			}
			if ok {
				got, _ := b.GetCurrentItem(nil)
				if got.ID != tc.wantID {
					t.Fatalf("got ID %v want %v", got.ID, tc.wantID)
				}
			}
		})
	}
}

// fakeNRNextErr returns the node on first Get(id) and errors on subsequent Get(id) calls.
type fakeNRNextErr[TK Ordered, TV any] struct {
	n      map[sop.UUID]*Node[TK, TV]
	seen   map[sop.UUID]int
	errIDs map[sop.UUID]bool
}

func (f *fakeNRNextErr[TK, TV]) Add(node *Node[TK, TV])    { f.n[node.ID] = node }
func (f *fakeNRNextErr[TK, TV]) Update(node *Node[TK, TV]) { f.n[node.ID] = node }
func (f *fakeNRNextErr[TK, TV]) Remove(id sop.UUID)        { delete(f.n, id) }
func (f *fakeNRNextErr[TK, TV]) Fetched(id sop.UUID)       {}
func (f *fakeNRNextErr[TK, TV]) Get(_ context.Context, id sop.UUID) (*Node[TK, TV], error) {
	if f.seen == nil {
		f.seen = map[sop.UUID]int{}
	}
	f.seen[id]++
	if f.errIDs[id] && f.seen[id] > 1 { // error on second and subsequent access
		return nil, fmt.Errorf("forced next error for %v", id)
	}
	return f.n[id], nil
}

func TestFindWithID_PropagatesNextError(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: false})
	fnr := &fakeNRNextErr[int, string]{n: map[sop.UUID]*Node[int, string]{}, errIDs: map[sop.UUID]bool{}}
	si := StoreInterface[int, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, string]{}}
	b, _ := New[int, string](store, &si, nil)

	// Build a root leaf with two duplicates so that FindWithID enters the loop and then calls Next
	root := newNode[int, string](b.getSlotLength())
	root.newID(sop.NilUUID)
	v := "v"
	vv := v
	root.Slots[0] = Item[int, string]{Key: 9, Value: &vv, ID: sop.NewUUID()}
	root.Slots[1] = Item[int, string]{Key: 9, Value: &vv, ID: sop.NewUUID()}
	root.Count = 2
	b.StoreInfo.RootNodeID = root.ID
	b.StoreInfo.Count = 2
	fnr.Add(root)
	// Cause an error when Next tries to read the current node again
	fnr.errIDs[root.ID] = true

	// Ask for a missing ID so loop will attempt Next and hit the error
	if ok, err := b.FindWithID(nil, 9, sop.NewUUID()); err == nil || ok {
		t.Fatalf("expected error propagated from Next, got ok=%v err=%v", ok, err)
	}
}

// helper to construct a non-unique btree for duplicate key scenarios
func newNonUniqueBtree[T any]() (*Btree[int, T], *fakeNR[int, T]) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: false, IsValueDataInNodeSegment: true})
	fnr := &fakeNR[int, T]{n: map[sop.UUID]*Node[int, T]{}}
	si := StoreInterface[int, T]{NodeRepository: fnr, ItemActionTracker: fakeIAT[int, T]{}}
	b, err := New[int, T](store, &si, nil)
	if err != nil {
		panic(err)
	}
	return b, fnr
}

func TestFindWithID_SuccessAcrossDuplicates(t *testing.T) {
	b, _ := newNonUniqueBtree[string]()
	// Insert three items with the same key
	if ok, _ := b.Add(nil, 5, "a"); !ok {
		t.Fatal("add1")
	}
	if ok, _ := b.Add(nil, 5, "b"); !ok {
		t.Fatal("add2")
	}
	if ok, _ := b.Add(nil, 5, "c"); !ok {
		t.Fatal("add3")
	}

	// Position to first duplicate, then capture the ID of the second duplicate
	if ok, err := b.Find(nil, 5, true); err != nil || !ok {
		t.Fatalf("find: %v ok=%v", err, ok)
	}
	// Move to second duplicate
	if ok, err := b.Next(nil); err != nil || !ok {
		t.Fatalf("next: %v ok=%v", err, ok)
	}
	it2, _ := b.GetCurrentItem(nil)

	// Now use FindWithID to locate the specific duplicate by ID
	if ok, err := b.FindWithID(nil, 5, it2.ID); err != nil || !ok {
		t.Fatalf("FindWithID should succeed for existing duplicate: err=%v ok=%v", err, ok)
	}
}

func TestFindWithID_NotFoundAfterDuplicatesExhausted(t *testing.T) {
	b, _ := newNonUniqueBtree[string]()
	// Insert two items with the same key
	if ok, _ := b.Add(nil, 5, "a"); !ok {
		t.Fatal("add1")
	}
	if ok, _ := b.Add(nil, 5, "b"); !ok {
		t.Fatal("add2")
	}

	// Ask for a random ID that doesn't exist
	missing := sop.NewUUID()
	if ok, err := b.FindWithID(nil, 5, missing); err != nil || ok {
		t.Fatalf("FindWithID should return (false,nil) when ID not found among duplicates: err=%v ok=%v", err, ok)
	}
}
