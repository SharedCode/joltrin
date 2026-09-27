package common

import (
	"cmp"
	"context"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

var ctx = context.Background()

func Test_TransactionInducedErrorOnNew(t *testing.T) {
	t2, err := newMockTransaction(t, sop.ForWriting, -1)
	if err != nil {
		t.Fatal(err.Error())
	}
	t2.Begin(ctx)
	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	// Simulate having an existing fooStore store in the backend.
	si := sop.StoreOptions{
		Name:                     "fooStore",
		SlotLength:               5,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        true,
		Description:              "",
	}
	trans.StoreRepository.Add(ctx, *sop.NewStoreInfo(si))

	// This call should fail and cause rollback because slotLength is being asked to 99 which will
	// fail spec check vs the "existing" store created above (w/ slot length 5).
	NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "fooStore",
		SlotLength:               99,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        true,
		Description:              "",
	}, t2, cmp.Compare)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back after an error on NewBtree")
	}
}

func Test_TransactionInducedErrorOnOpen(t *testing.T) {
	trans, err := newMockTransaction(t, sop.ForWriting, -1)
	if err != nil {
		t.Fatal(err.Error())
	}
	trans.Begin(ctx)
	OpenBtree[int, string](ctx, "fooStore33", trans, cmp.Compare)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back after an error on OpenBtree")
	}
}

func Test_TransactionWithInducedErrorOnAdd(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 1
	b3t.Add(ctx, 1, "foo")
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnAddIfNotExist(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 2
	b3t.AddIfNotExist(ctx, 1, "foo")
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnUpdate(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 3
	b3t.Update(ctx, 1, "foo")
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnUpdateCurrentValue(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 4
	b3t.UpdateCurrentValue(ctx, "foo")
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnUpdateCurrentKey(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 16
	b3t.UpdateCurrentKey(ctx, 1)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnRemove(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 5
	b3t.Remove(ctx, 1)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnRemoveCurrentItem(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 6
	b3t.RemoveCurrentItem(ctx)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnFindOne(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 7
	b3t.Find(ctx, 1, false)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnFindOneWithID(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 8
	b3t.FindWithID(ctx, 1, sop.NewUUID())
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnGetCurrentValue(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 9
	b3t.GetCurrentValue(ctx)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnGetCurrentItem(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 10
	b3t.GetCurrentItem(ctx)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnFirst(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 11
	b3t.First(ctx)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnLast(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 12
	b3t.Last(ctx)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnNext(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 13
	b3t.Next(ctx)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnPrevious(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 14
	b3t.Previous(ctx)
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}

func Test_TransactionWithInducedErrorOnUpsert(t *testing.T) {
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2.Begin(ctx)

	var t3 interface{} = t2.GetPhasedTransaction()
	trans := t3.(*Transaction)

	b3 := newBTreeWithInducedErrors[int, string](t)
	b3t := btree.NewBtreeWithTransaction(trans, b3)
	b3.induceErrorOnMethod = 15
	b3t.Upsert(ctx, 1, "foo")
	if trans.HasBegun() {
		t.Error("Transaction is not rolled back.")
	}
}
