//go:build stress
// +build stress

package stresstests

import (
	"context"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

type KeyStruct struct {
	ID       int
	Metadata string
}

func compareKeyStruct(a, b KeyStruct) int {
	return a.ID - b.ID
}

func Test_KeyUpdate_Bundled(t *testing.T) {
	ctx := context.Background()
	dataPath, err := os.MkdirTemp("", "sop_stress_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dataPath)

	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ := infs.NewTransaction(ctx, to)
	t1.Begin(ctx)

	b3, err := infs.NewBtree[KeyStruct, string](ctx, sop.StoreOptions{
		Name:                     "key_update_bundled",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
	}, t1, compareKeyStruct)
	if err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}

	// Add item
	key := KeyStruct{ID: 1, Metadata: "initial"}
	b3.Add(ctx, key, "value1")

	// Update key immediately in same transaction
	b3.Find(ctx, key, false)
	newKey := KeyStruct{ID: 1, Metadata: "updated"}
	if ok, err := b3.UpdateCurrentKey(ctx, newKey); !ok || err != nil {
		t.Fatalf("UpdateCurrentKey failed: %v", err)
	}

	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify in new transaction
	t2, _ := infs.NewTransaction(ctx, to)
	t2.Begin(ctx)
	b3_2, _ := infs.OpenBtree[KeyStruct, string](ctx, "key_update_bundled", t2, compareKeyStruct)

	if found, _ := b3_2.Find(ctx, key, false); !found {
		t.Errorf("Could not find key")
	}

	gotKey := b3_2.GetCurrentKey().Key
	if gotKey.Metadata != "updated" {
		t.Errorf("Expected metadata 'updated', got '%s'", gotKey.Metadata)
	}

	val, _ := b3_2.GetCurrentValue(ctx)
	if val != "value1" {
		t.Errorf("Expected value 'value1', got '%s'", val)
	}
}

func Test_KeyUpdate_Separate(t *testing.T) {
	ctx := context.Background()
	dataPath, err := os.MkdirTemp("", "sop_stress_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dataPath)

	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}

	// Transaction 1: Add
	t1, _ := infs.NewTransaction(ctx, to)
	t1.Begin(ctx)
	b3, _ := infs.NewBtree[KeyStruct, string](ctx, sop.StoreOptions{
		Name:                     "key_update_separate",
		SlotLength:               8,
		IsValueDataInNodeSegment: false, // Changed to false to test lazy loading
	}, t1, compareKeyStruct)

	key := KeyStruct{ID: 1, Metadata: "initial"}
	b3.Add(ctx, key, "value1")
	t1.Commit(ctx)

	// Transaction 2: Update Key
	t2, _ := infs.NewTransaction(ctx, to)
	t2.Begin(ctx)
	b3_2, _ := infs.OpenBtree[KeyStruct, string](ctx, "key_update_separate", t2, compareKeyStruct)

	// Find but DO NOT fetch value
	if found, _ := b3_2.Find(ctx, key, false); !found {
		t.Fatalf("Could not find key 1 in T2")
	}

	newKey := KeyStruct{ID: 1, Metadata: "updated"}
	if ok, err := b3_2.UpdateCurrentKey(ctx, newKey); !ok || err != nil {
		t.Fatalf("UpdateCurrentKey failed: %v", err)
	}

	if err := t2.Commit(ctx); err != nil {
		t.Fatalf("Commit T2 failed: %v", err)
	}

	// Transaction 3: Verify
	t3, _ := infs.NewTransaction(ctx, to)
	t3.Begin(ctx)
	b3_3, _ := infs.OpenBtree[KeyStruct, string](ctx, "key_update_separate", t3, compareKeyStruct)

	if found, _ := b3_3.Find(ctx, key, false); !found {
		t.Errorf("Could not find key")
	}

	gotKey := b3_3.GetCurrentKey().Key
	if gotKey.Metadata != "updated" {
		t.Errorf("Expected metadata 'updated', got '%s'", gotKey.Metadata)
	}

	val, _ := b3_3.GetCurrentValue(ctx)
	if val != "value1" {
		t.Errorf("Expected value 'value1', got '%s'", val)
	}
}

func Test_KeyUpdate_WithFetch(t *testing.T) {
	ctx := context.Background()
	dataPath, err := os.MkdirTemp("", "sop_stress_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dataPath)

	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}

	// Transaction 1: Add
	t1, _ := infs.NewTransaction(ctx, to)
	t1.Begin(ctx)
	b3, _ := infs.NewBtree[KeyStruct, string](ctx, sop.StoreOptions{
		Name:                     "key_update_fetch",
		SlotLength:               8,
		IsValueDataInNodeSegment: false,
	}, t1, compareKeyStruct)

	key := KeyStruct{ID: 1, Metadata: "initial"}
	b3.Add(ctx, key, "value1")
	t1.Commit(ctx)

	// Transaction 2: Update Key WITH Fetch
	t2, _ := infs.NewTransaction(ctx, to)
	t2.Begin(ctx)
	b3_2, _ := infs.OpenBtree[KeyStruct, string](ctx, "key_update_fetch", t2, compareKeyStruct)

	// Find AND fetch value
	if found, _ := b3_2.Find(ctx, key, true); !found {
		t.Fatalf("Could not find key 1 in T2")
	}

	newKey := KeyStruct{ID: 1, Metadata: "updated"}
	if ok, err := b3_2.UpdateCurrentKey(ctx, newKey); !ok || err != nil {
		t.Fatalf("UpdateCurrentKey failed: %v", err)
	}

	if err := t2.Commit(ctx); err != nil {
		t.Fatalf("Commit T2 failed: %v", err)
	}

	// Transaction 3: Verify
	t3, _ := infs.NewTransaction(ctx, to)
	t3.Begin(ctx)
	b3_3, _ := infs.OpenBtree[KeyStruct, string](ctx, "key_update_fetch", t3, compareKeyStruct)

	if found, _ := b3_3.Find(ctx, newKey, false); !found {
		t.Errorf("Did not find new key")
	}

	val, _ := b3_3.GetCurrentValue(ctx)
	if val != "value1" {
		t.Errorf("Expected value 'value1', got '%s'", val)
	}
}

func Test_Remove_Lazy(t *testing.T) {
	ctx := context.Background()
	dataPath, err := os.MkdirTemp("", "sop_stress_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dataPath)

	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}

	// Transaction 1: Add
	t1, _ := infs.NewTransaction(ctx, to)
	t1.Begin(ctx)
	b3, _ := infs.NewBtree[KeyStruct, string](ctx, sop.StoreOptions{
		Name:                     "remove_lazy",
		SlotLength:               8,
		IsValueDataInNodeSegment: false,
	}, t1, compareKeyStruct)

	key := KeyStruct{ID: 1, Metadata: "initial"}
	b3.Add(ctx, key, "value1")
	t1.Commit(ctx)

	// Transaction 2: Remove Lazy
	t2, _ := infs.NewTransaction(ctx, to)
	t2.Begin(ctx)
	b3_2, _ := infs.OpenBtree[KeyStruct, string](ctx, "remove_lazy", t2, compareKeyStruct)

	// Find but DO NOT fetch value
	if found, _ := b3_2.Find(ctx, key, false); !found {
		t.Fatalf("Could not find key 1 in T2")
	}

	if ok, err := b3_2.RemoveCurrentItem(ctx); !ok || err != nil {
		t.Fatalf("RemoveCurrentItem failed: %v", err)
	}

	if err := t2.Commit(ctx); err != nil {
		t.Fatalf("Commit T2 failed: %v", err)
	}

	// Transaction 3: Verify
	t3, _ := infs.NewTransaction(ctx, to)
	t3.Begin(ctx)
	b3_3, _ := infs.OpenBtree[KeyStruct, string](ctx, "remove_lazy", t3, compareKeyStruct)

	if found, _ := b3_3.Find(ctx, key, false); found {
		t.Errorf("Found key 1, should have been removed")
	}
}
