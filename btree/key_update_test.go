package btree

import (
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

type KeyStruct struct {
	ID       int
	Metadata string
}

func compareKeyStruct(a, b KeyStruct) int {
	return a.ID - b.ID
}

func TestUpdateCurrentKey_Compatible(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &fakeNR[KeyStruct, string]{n: map[sop.UUID]*Node[KeyStruct, string]{}}
	si := StoreInterface[KeyStruct, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[KeyStruct, string]{}}
	b, err := New[KeyStruct, string](store, &si, compareKeyStruct)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	key := KeyStruct{ID: 1, Metadata: "initial"}
	b.Add(nil, key, "value")

	b.Find(nil, key, false)

	// Update with compatible key (same ID, different Metadata)
	newKey := KeyStruct{ID: 1, Metadata: "updated"}
	if ok, err := b.UpdateCurrentKey(nil, newKey); !ok || err != nil {
		t.Fatalf("UpdateCurrentKey failed: %v", err)
	}

	// Verify update
	gotKey := b.GetCurrentKey().Key
	if gotKey.Metadata != "updated" {
		t.Errorf("expected metadata 'updated', got '%s'", gotKey.Metadata)
	}

	// Update with incompatible key (different ID)
	badKey := KeyStruct{ID: 2, Metadata: "bad"}
	if ok, err := b.UpdateCurrentKey(nil, badKey); ok || err == nil {
		t.Errorf("UpdateCurrentKey with different ID should fail")
	}
}

func TestUpdateKey_Compatible(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &fakeNR[KeyStruct, string]{n: map[sop.UUID]*Node[KeyStruct, string]{}}
	si := StoreInterface[KeyStruct, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[KeyStruct, string]{}}
	b, err := New[KeyStruct, string](store, &si, compareKeyStruct)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	key := KeyStruct{ID: 1, Metadata: "initial"}
	b.Add(nil, key, "value")

	// UpdateKey finds the item by key (ID=1) and updates it
	newKey := KeyStruct{ID: 1, Metadata: "updated_via_UpdateKey"}
	if ok, err := b.UpdateKey(nil, newKey); !ok || err != nil {
		t.Fatalf("UpdateKey failed: %v", err)
	}

	// Verify
	b.Find(nil, key, false)
	gotKey := b.GetCurrentKey().Key
	if gotKey.Metadata != "updated_via_UpdateKey" {
		t.Errorf("expected metadata 'updated_via_UpdateKey', got '%s'", gotKey.Metadata)
	}
}

func TestUpdateCurrentItem_CompatibleKey(t *testing.T) {
	store := sop.NewStoreInfo(sop.StoreOptions{SlotLength: 4, IsUnique: true, IsValueDataInNodeSegment: true})
	fnr := &fakeNR[KeyStruct, string]{n: map[sop.UUID]*Node[KeyStruct, string]{}}
	si := StoreInterface[KeyStruct, string]{NodeRepository: fnr, ItemActionTracker: fakeIAT[KeyStruct, string]{}}
	b, err := New[KeyStruct, string](store, &si, compareKeyStruct)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	key := KeyStruct{ID: 1, Metadata: "initial"}
	b.Add(nil, key, "value")

	b.Find(nil, key, false)

	// Update both Key (compatible) and Value
	newKey := KeyStruct{ID: 1, Metadata: "updated_item"}
	if ok, err := b.UpdateCurrentItem(nil, newKey, "new_value"); !ok || err != nil {
		t.Fatalf("UpdateCurrentItem failed: %v", err)
	}

	// Verify
	item, _ := b.GetCurrentItem(nil)
	if item.Key.Metadata != "updated_item" {
		t.Errorf("expected key metadata 'updated_item', got '%s'", item.Key.Metadata)
	}
	if *item.Value != "new_value" {
		t.Errorf("expected value 'new_value', got '%s'", *item.Value)
	}

	// Update with incompatible key
	badKey := KeyStruct{ID: 2, Metadata: "bad"}
	if ok, err := b.UpdateCurrentItem(nil, badKey, "val"); ok || err == nil {
		t.Errorf("UpdateCurrentItem with different ID should fail")
	}
}
