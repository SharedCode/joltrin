//go:build stress
// +build stress

package stresstests

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

type PersonKeyWithMeta struct {
	Firstname string
	Lastname  string
	Metadata  string
}

func ComparePersonKeyWithMeta(x PersonKeyWithMeta, y PersonKeyWithMeta) int {
	i := cmp.Compare[string](x.Lastname, y.Lastname)
	if i != 0 {
		return i
	}
	return cmp.Compare[string](x.Firstname, y.Firstname)
}

func Test_ConcurrentKeyUpdates_DifferentItems(t *testing.T) {
	ctx := context.Background()
	dataPath, err := os.MkdirTemp("", "sop_stress_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dataPath)

	// Use a separate table name for this test
	tableName := fmt.Sprintf("person_key_update_test_%d", time.Now().UnixNano())

	// 1. Setup: Create store and add an initial item
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, err := infs.NewTransaction(ctx, to)
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}
	t1.Begin(ctx)

	b3, err := infs.NewBtree[PersonKeyWithMeta, Person](ctx, sop.StoreOptions{
		Name:                     tableName,
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t1, ComparePersonKeyWithMeta)
	if err != nil {
		t.Fatalf("Failed to create btree: %v", err)
	}

	pk1 := PersonKeyWithMeta{Firstname: "john", Lastname: "doe", Metadata: "initial_meta"}
	p1 := Person{Gender: "male", Email: "john.doe@example.com", Phone: "123-456-7890"}

	pk2 := PersonKeyWithMeta{Firstname: "jane", Lastname: "doe", Metadata: "initial_meta"}
	p2 := Person{Gender: "female", Email: "jane.doe@example.com", Phone: "098-765-4321"}

	if ok, err := b3.Add(ctx, pk1, p1); !ok || err != nil {
		t.Fatalf("Add pk1 failed: %v", err)
	}
	if ok, err := b3.Add(ctx, pk2, p2); !ok || err != nil {
		t.Fatalf("Add pk2 failed: %v", err)
	}

	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("Commit 1 failed: %v", err)
	}

	// 2. Concurrent Transactions
	// T2 will update Key Metadata for pk1
	// T3 will update Value Phone for pk2

	t2, _ := infs.NewTransaction(ctx, to)
	t3, _ := infs.NewTransaction(ctx, to)

	t2.Begin(ctx)
	t3.Begin(ctx)

	b3_t2, _ := infs.OpenBtree[PersonKeyWithMeta, Person](ctx, tableName, t2, ComparePersonKeyWithMeta)
	b3_t3, _ := infs.OpenBtree[PersonKeyWithMeta, Person](ctx, tableName, t3, ComparePersonKeyWithMeta)

	// T2 reads and updates Key for pk1
	if ok, err := b3_t2.Find(ctx, pk1, false); !ok || err != nil {
		t.Fatalf("T2 Find pk1 failed: %v", err)
	}
	newKey1 := PersonKeyWithMeta{Firstname: "john", Lastname: "doe", Metadata: "updated_meta_by_t2"}
	if ok, err := b3_t2.UpdateCurrentKey(ctx, newKey1); !ok || err != nil {
		t.Fatalf("T2 UpdateCurrentKey failed: %v", err)
	}

	// T3 reads and updates Value for pk2
	if ok, err := b3_t3.Find(ctx, pk2, false); !ok || err != nil {
		t.Fatalf("T3 Find pk2 failed: %v", err)
	}
	newValue2 := Person{Gender: "female", Email: "jane.doe@example.com", Phone: "999-999-9999"}
	if ok, err := b3_t3.UpdateCurrentValue(ctx, newValue2); !ok || err != nil {
		t.Fatalf("T3 UpdateCurrentValue failed: %v", err)
	}

	// 3. Commit T2 first
	if err := t2.Commit(ctx); err != nil {
		t.Fatalf("T2 Commit failed: %v", err)
	}

	// 4. Commit T3 (should succeed as it touches a different item)
	if err := t3.Commit(ctx); err != nil {
		t.Fatalf("T3 Commit failed: %v", err)
	}

	// 5. Verify Final State
	t4, _ := infs.NewTransaction(ctx, to)
	t4.Begin(ctx)
	b3_t4, _ := infs.OpenBtree[PersonKeyWithMeta, Person](ctx, tableName, t4, ComparePersonKeyWithMeta)

	// Verify pk1 update
	if ok, err := b3_t4.Find(ctx, pk1, false); !ok || err != nil {
		t.Fatalf("T4 Find pk1 failed: %v", err)
	}
	finalKey1 := b3_t4.GetCurrentKey().Key
	if finalKey1.Metadata != "updated_meta_by_t2" {
		t.Errorf("Expected pk1 metadata 'updated_meta_by_t2', got '%s'", finalKey1.Metadata)
	}

	// Verify pk2 update
	if ok, err := b3_t4.Find(ctx, pk2, false); !ok || err != nil {
		t.Fatalf("T4 Find pk2 failed: %v", err)
	}
	finalValue2, _ := b3_t4.GetCurrentValue(ctx)
	if finalValue2.Phone != "999-999-9999" {
		t.Errorf("Expected pk2 phone '999-999-9999', got '%s'", finalValue2.Phone)
	}

	t4.Commit(ctx)
}

func Test_ConcurrentKeyUpdates_SameItem_Conflict(t *testing.T) {
	ctx := context.Background()
	dataPath, err := os.MkdirTemp("", "sop_stress_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dataPath)

	tableName := fmt.Sprintf("person_key_update_conflict_test_%d", time.Now().UnixNano())

	// 1. Setup
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, err := infs.NewTransaction(ctx, to)
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}
	t1.Begin(ctx)

	b3, err := infs.NewBtree[PersonKeyWithMeta, Person](ctx, sop.StoreOptions{
		Name:                     tableName,
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t1, ComparePersonKeyWithMeta)
	if err != nil {
		t.Fatalf("Failed to create btree: %v", err)
	}

	pk1 := PersonKeyWithMeta{Firstname: "john", Lastname: "doe", Metadata: "initial_meta"}
	p1 := Person{Gender: "male", Email: "john.doe@example.com", Phone: "123-456-7890"}

	if ok, err := b3.Add(ctx, pk1, p1); !ok || err != nil {
		t.Fatalf("Add pk1 failed: %v", err)
	}
	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("Commit 1 failed: %v", err)
	}

	// 2. Concurrent Transactions on SAME item
	t2, _ := infs.NewTransaction(ctx, to)
	t3, _ := infs.NewTransaction(ctx, to)

	t2.Begin(ctx)
	t3.Begin(ctx)

	b3_t2, _ := infs.OpenBtree[PersonKeyWithMeta, Person](ctx, tableName, t2, ComparePersonKeyWithMeta)
	b3_t3, _ := infs.OpenBtree[PersonKeyWithMeta, Person](ctx, tableName, t3, ComparePersonKeyWithMeta)

	// T2 updates pk1
	if ok, err := b3_t2.Find(ctx, pk1, false); !ok || err != nil {
		t.Fatalf("T2 Find pk1 failed: %v", err)
	}
	newKey1 := PersonKeyWithMeta{Firstname: "john", Lastname: "doe", Metadata: "updated_by_t2"}
	if ok, err := b3_t2.UpdateCurrentKey(ctx, newKey1); !ok || err != nil {
		t.Fatalf("T2 UpdateCurrentKey failed: %v", err)
	}

	// T3 updates pk1 (Key update)
	if ok, err := b3_t3.Find(ctx, pk1, false); !ok || err != nil {
		t.Fatalf("T3 Find pk1 failed: %v", err)
	}
	newKey1_T3 := PersonKeyWithMeta{Firstname: "john", Lastname: "doe", Metadata: "updated_by_t3"}
	if ok, err := b3_t3.UpdateCurrentKey(ctx, newKey1_T3); !ok || err != nil {
		t.Fatalf("T3 UpdateCurrentKey failed: %v", err)
	}

	// 3. Commit T2 first (should succeed)
	if err := t2.Commit(ctx); err != nil {
		t.Fatalf("T2 Commit failed: %v", err)
	}

	// 4. Commit T3 (should FAIL due to conflict)
	if err := t3.Commit(ctx); err == nil {
		t.Fatalf("T3 Commit succeeded but expected failure due to conflict")
	} else {
		t.Logf("T3 Commit failed as expected: %v", err)
	}
}

func Test_ConcurrentValueUpdates_SameItem_Conflict(t *testing.T) {
	ctx := context.Background()
	dataPath, err := os.MkdirTemp("", "sop_stress_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dataPath)

	tableName := fmt.Sprintf("person_value_update_conflict_test_%d", time.Now().UnixNano())

	// 1. Setup
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, err := infs.NewTransaction(ctx, to)
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}
	t1.Begin(ctx)

	b3, err := infs.NewBtree[PersonKeyWithMeta, Person](ctx, sop.StoreOptions{
		Name:                     tableName,
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t1, ComparePersonKeyWithMeta)
	if err != nil {
		t.Fatalf("Failed to create btree: %v", err)
	}

	pk1 := PersonKeyWithMeta{Firstname: "john", Lastname: "doe", Metadata: "initial_meta"}
	p1 := Person{Gender: "male", Email: "john.doe@example.com", Phone: "123-456-7890"}

	if ok, err := b3.Add(ctx, pk1, p1); !ok || err != nil {
		t.Fatalf("Add pk1 failed: %v", err)
	}
	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("Commit 1 failed: %v", err)
	}

	// 2. Concurrent Transactions on SAME item
	t2, _ := infs.NewTransaction(ctx, to)
	t3, _ := infs.NewTransaction(ctx, to)

	t2.Begin(ctx)
	t3.Begin(ctx)

	b3_t2, _ := infs.OpenBtree[PersonKeyWithMeta, Person](ctx, tableName, t2, ComparePersonKeyWithMeta)
	b3_t3, _ := infs.OpenBtree[PersonKeyWithMeta, Person](ctx, tableName, t3, ComparePersonKeyWithMeta)

	// T2 updates pk1 Value
	if ok, err := b3_t2.Find(ctx, pk1, false); !ok || err != nil {
		t.Fatalf("T2 Find pk1 failed: %v", err)
	}
	newValue1_T2 := Person{Gender: "male", Email: "john.doe@example.com", Phone: "111-111-1111"}
	if ok, err := b3_t2.UpdateCurrentValue(ctx, newValue1_T2); !ok || err != nil {
		t.Fatalf("T2 UpdateCurrentValue failed: %v", err)
	}

	// T3 updates pk1 Value
	if ok, err := b3_t3.Find(ctx, pk1, false); !ok || err != nil {
		t.Fatalf("T3 Find pk1 failed: %v", err)
	}
	newValue1_T3 := Person{Gender: "male", Email: "john.doe@example.com", Phone: "222-222-2222"}
	if ok, err := b3_t3.UpdateCurrentValue(ctx, newValue1_T3); !ok || err != nil {
		t.Fatalf("T3 UpdateCurrentValue failed: %v", err)
	}

	// 3. Commit T2 first (should succeed)
	if err := t2.Commit(ctx); err != nil {
		t.Fatalf("T2 Commit failed: %v", err)
	}

	// 4. Commit T3 (should FAIL due to conflict)
	if err := t3.Commit(ctx); err == nil {
		t.Fatalf("T3 Commit succeeded but expected failure due to conflict")
	} else {
		t.Logf("T3 Commit failed as expected: %v", err)
	}
}

func Test_ConcurrentKeyAndValueUpdates_SameItem_Conflict(t *testing.T) {
	ctx := context.Background()
	dataPath, err := os.MkdirTemp("", "sop_stress_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(dataPath)

	tableName := fmt.Sprintf("person_key_value_update_conflict_test_%d", time.Now().UnixNano())

	// 1. Setup
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, err := infs.NewTransaction(ctx, to)
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}
	t1.Begin(ctx)

	b3, err := infs.NewBtree[PersonKeyWithMeta, Person](ctx, sop.StoreOptions{
		Name:                     tableName,
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t1, ComparePersonKeyWithMeta)
	if err != nil {
		t.Fatalf("Failed to create btree: %v", err)
	}

	pk1 := PersonKeyWithMeta{Firstname: "john", Lastname: "doe", Metadata: "initial_meta"}
	p1 := Person{Gender: "male", Email: "john.doe@example.com", Phone: "123-456-7890"}

	if ok, err := b3.Add(ctx, pk1, p1); !ok || err != nil {
		t.Fatalf("Add pk1 failed: %v", err)
	}
	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("Commit 1 failed: %v", err)
	}

	// 2. Concurrent Transactions on SAME item
	t2, _ := infs.NewTransaction(ctx, to)
	t3, _ := infs.NewTransaction(ctx, to)

	t2.Begin(ctx)
	t3.Begin(ctx)

	b3_t2, _ := infs.OpenBtree[PersonKeyWithMeta, Person](ctx, tableName, t2, ComparePersonKeyWithMeta)
	b3_t3, _ := infs.OpenBtree[PersonKeyWithMeta, Person](ctx, tableName, t3, ComparePersonKeyWithMeta)

	// T2 updates pk1 Key
	if ok, err := b3_t2.Find(ctx, pk1, false); !ok || err != nil {
		t.Fatalf("T2 Find pk1 failed: %v", err)
	}
	newKey1_T2 := PersonKeyWithMeta{Firstname: "john", Lastname: "doe", Metadata: "updated_by_t2"}
	if ok, err := b3_t2.UpdateCurrentKey(ctx, newKey1_T2); !ok || err != nil {
		t.Fatalf("T2 UpdateCurrentKey failed: %v", err)
	}

	// T3 updates pk1 Value
	if ok, err := b3_t3.Find(ctx, pk1, false); !ok || err != nil {
		t.Fatalf("T3 Find pk1 failed: %v", err)
	}
	newValue1_T3 := Person{Gender: "male", Email: "john.doe@example.com", Phone: "333-333-3333"}
	if ok, err := b3_t3.UpdateCurrentValue(ctx, newValue1_T3); !ok || err != nil {
		t.Fatalf("T3 UpdateCurrentValue failed: %v", err)
	}

	// 3. Commit T2 first (should succeed)
	if err := t2.Commit(ctx); err != nil {
		t.Fatalf("T2 Commit failed: %v", err)
	}

	// 4. Commit T3 (should FAIL due to conflict)
	if err := t3.Commit(ctx); err == nil {
		t.Fatalf("T3 Commit succeeded but expected failure due to conflict")
	} else {
		t.Logf("T3 Commit failed as expected: %v", err)
	}
}
