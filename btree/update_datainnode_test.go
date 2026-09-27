package btree

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

type UserKey struct {
	ID       int
	Username string
}

func compareUserKey(a, b UserKey) int {
	return a.ID - b.ID
}

func TestUpdateCurrentKey_DataInNode(t *testing.T) {
	ctx := context.Background()
	// IsValueDataInNodeSegment = true is the key configuration here
	store := sop.NewStoreInfo(sop.StoreOptions{
		SlotLength:               4,
		IsUnique:                 true,
		IsValueDataInNodeSegment: true,
	})

	// Use the fake NodeRepository and ItemActionTracker available in the package tests
	fnr := &fakeNR[UserKey, string]{n: map[sop.UUID]*Node[UserKey, string]{}}
	si := StoreInterface[UserKey, string]{
		NodeRepository:    fnr,
		ItemActionTracker: fakeIAT[UserKey, string]{},
	}

	b, err := New[UserKey, string](store, &si, compareUserKey)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// 1. Add an item
	key := UserKey{ID: 1, Username: "alice"}
	value := "alice_data"
	if ok, err := b.Add(ctx, key, value); !ok || err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// 2. Position cursor
	if found, err := b.Find(ctx, key, false); !found || err != nil {
		t.Fatalf("Find failed: %v", err)
	}

	// 3. Update Current Key (compatible update: ID stays same, Username changes)
	newKey := UserKey{ID: 1, Username: "alice_updated"}
	if ok, err := b.UpdateCurrentKey(ctx, newKey); !ok || err != nil {
		t.Fatalf("UpdateCurrentKey failed: %v", err)
	}

	// 4. Verify Update
	// Check in-memory current item
	currentItem, err := b.GetCurrentItem(ctx)
	if err != nil {
		t.Fatalf("GetCurrentItem failed: %v", err)
	}
	if currentItem.Key.Username != "alice_updated" {
		t.Errorf("Expected username 'alice_updated', got '%s'", currentItem.Key.Username)
	}
	// Verify value is still there (since it's in the node, it shouldn't be lost)
	if *currentItem.Value != "alice_data" {
		t.Errorf("Expected value 'alice_data', got '%s'", *currentItem.Value)
	}

	// 5. Verify Persistence (via fakeNR)
	// Move cursor away and back to ensure we aren't just reading cached current item
	b.First(ctx)
	if found, err := b.Find(ctx, newKey, false); !found || err != nil {
		t.Fatalf("Find with new key failed: %v", err)
	}

	gotItem, _ := b.GetCurrentItem(ctx)
	if gotItem.Key.Username != "alice_updated" {
		t.Errorf("After re-find, expected username 'alice_updated', got '%s'", gotItem.Key.Username)
	}
}

func TestUpdateCurrentItem_DataInNode(t *testing.T) {
	ctx := context.Background()
	// IsValueDataInNodeSegment = true is the key configuration here
	store := sop.NewStoreInfo(sop.StoreOptions{
		SlotLength:               4,
		IsUnique:                 true,
		IsValueDataInNodeSegment: true,
	})

	fnr := &fakeNR[UserKey, string]{n: map[sop.UUID]*Node[UserKey, string]{}}
	si := StoreInterface[UserKey, string]{
		NodeRepository:    fnr,
		ItemActionTracker: fakeIAT[UserKey, string]{},
	}

	b, err := New[UserKey, string](store, &si, compareUserKey)
	if err != nil {
		t.Fatalf("New failed: %v", err)
	}

	// 1. Add an item
	key := UserKey{ID: 1, Username: "bob"}
	value := "bob_data"
	if ok, err := b.Add(ctx, key, value); !ok || err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	// 2. Position cursor
	if found, err := b.Find(ctx, key, false); !found || err != nil {
		t.Fatalf("Find failed: %v", err)
	}

	// 3. Update Current Item (Key and Value)
	newKey := UserKey{ID: 1, Username: "bob_v2"}
	newValue := "bob_data_v2"
	if ok, err := b.UpdateCurrentItem(ctx, newKey, newValue); !ok || err != nil {
		t.Fatalf("UpdateCurrentItem failed: %v", err)
	}

	// 4. Verify Update
	currentItem, err := b.GetCurrentItem(ctx)
	if err != nil {
		t.Fatalf("GetCurrentItem failed: %v", err)
	}
	if currentItem.Key.Username != "bob_v2" {
		t.Errorf("Expected username 'bob_v2', got '%s'", currentItem.Key.Username)
	}
	if *currentItem.Value != "bob_data_v2" {
		t.Errorf("Expected value 'bob_data_v2', got '%s'", *currentItem.Value)
	}

	// 5. Verify Persistence
	b.First(ctx)
	if found, err := b.Find(ctx, newKey, false); !found || err != nil {
		t.Fatalf("Find with new key failed: %v", err)
	}

	gotItem, _ := b.GetCurrentItem(ctx)
	if gotItem.Key.Username != "bob_v2" {
		t.Errorf("After re-find, expected username 'bob_v2', got '%s'", gotItem.Key.Username)
	}
	if *gotItem.Value != "bob_data_v2" {
		t.Errorf("After re-find, expected value 'bob_data_v2', got '%s'", *gotItem.Value)
	}
}
