//go:build stress
// +build stress

package common

// Consolidated stress scenarios from:
// - stress_streamingdata_test.go
// - stress_transaction_logging_test.go
// - stress_transaction_test.go
// - stress_value_data_separate_segment_test.go

import (
	"fmt"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// ---- Streaming data store ----
func TestStress_StreamingDataStoreRollbackShouldEraseTIDLogs(t *testing.T) {
	// Populate with good data.
	trans, _ := newMockTransactionWithLogging(t, sop.ForWriting, -1)
	trans.Begin(ctx)

	so := sop.ConfigureStore("xyz", true, 8, "Streaming data", sop.BigData, "")
	sds, _ := NewBtree[string, string](ctx, so, trans, nil)

	sds.Add(ctx, "fooVideo", "video content")
	trans.Commit(ctx)

	// Now, populate then rollback and validate TID logs are gone.
	trans, _ = newMockTransactionWithLogging(t, sop.ForWriting, -1)
	trans.Begin(ctx)
	sds, _ = OpenBtree[string, string](ctx, "xyz", trans, nil)
	sds.Add(ctx, "fooVideo2", "video content")

	tidLogs := trans.GetPhasedTransaction().(*Transaction).
		logger.TransactionLog.(*mocks.MockTransactionLog).GetTIDLogs(
		trans.GetPhasedTransaction().(*Transaction).logger.transactionID)

	if tidLogs == nil {
		t.Error("failed pre Rollback, got nil, want valid logs")
	}

	trans.Rollback(ctx)

	gotTidLogs := trans.GetPhasedTransaction().(*Transaction).
		logger.TransactionLog.(*mocks.MockTransactionLog).GetTIDLogs(
		trans.GetPhasedTransaction().(*Transaction).logger.transactionID)

	if gotTidLogs != nil {
		t.Errorf("failed Rollback, got %v, want nil", gotTidLogs)
	}
}

// ---- Transaction logging time-window behavior ----
func TestStress_TLog_FailOnFinalizeCommit(t *testing.T) {
	// Unwind time to yesterday.
	yesterday := time.Now().Add(time.Duration(-24 * time.Hour))
	// cas.Now = func() time.Time { return yesterday }
	sop.Now = func() time.Time { return yesterday }

	trans, _ := newMockTransactionWithLogging(t, sop.ForWriting, -1)
	trans.Begin(ctx)

	b3, _ := NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "tlogtable",
		SlotLength:               nodeSlotLength,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        false,
		Description:              "",
	}, trans, Compare)

	pk, p := newPerson("joe", "shroeger", "male", "email", "phone")
	b3.Add(ctx, pk, p)

	trans.Commit(ctx)

	trans, _ = newMockTransactionWithLogging(t, sop.ForWriting, -1)
	trans.Begin(ctx)

	b3, _ = OpenBtree[PersonKey, Person](ctx, "tlogtable", trans, Compare)
	pk, p = newPerson("joe", "shroeger", "male", "email2", "phone2")
	b3.Update(ctx, pk, p)

	pt := trans.GetPhasedTransaction()
	twoPhaseTrans := pt.(*Transaction)

	twoPhaseTrans.phase1Commit(ctx)

	// GetOne should not get anything as uncommitted transaction is still ongoing or not expired.
	tid, _, _, _ := twoPhaseTrans.logger.GetOne(ctx)
	if !tid.IsNil() {
		t.Errorf("Failed, got %v, want nil.", tid)
	}

	// Fast forward by a day to allow us to expire the uncommitted transaction.
	today := time.Now()
	// cas.Now = func() time.Time { return today }
	sop.Now = func() time.Time { return today }

	tid, _, _, _ = twoPhaseTrans.logger.GetOne(ctx)
	if tid.IsNil() {
		t.Errorf("Failed, got nil Tid, want valid Tid.")
	}

	if err := twoPhaseTrans.logger.processExpiredTransactionLogs(ctx, twoPhaseTrans); err != nil {
		t.Errorf("processExpiredTransactionLogs failed, got %v want nil.", err)
	}
}

// ---- Heavy transaction scenarios ----
func TestStress_TwoTransactionsUpdatesOnSameItem(t *testing.T) {
	t1, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)

	t1.Begin(ctx)
	t2.Begin(ctx)

	b3, err := NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb",
		SlotLength:               nodeSlotLength,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        false,
		Description:              "",
	}, t1, Compare)

	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}

	pk, p := newPerson("peter", "swift", "male", "email", "phone")
	pk2, p2 := newPerson("peter", "parker", "male", "email", "phone")

	found, _ := b3.Find(ctx, pk, false)
	if !found {
		b3.Add(ctx, pk, p)
		b3.Add(ctx, pk2, p2)
		t1.Commit(ctx)
		t1, _ = newMockTransaction(t, sop.ForWriting, -1)
		t1.Begin(ctx)
		b3, _ = NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
			Name:                     "persondb",
			SlotLength:               nodeSlotLength,
			IsUnique:                 false,
			IsValueDataInNodeSegment: false,
			LeafLoadBalancing:        false,
			Description:              "",
		}, t1, Compare)
	}

	b32, _ := NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb",
		SlotLength:               nodeSlotLength,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        false,
		Description:              "",
	}, t2, Compare)

	// edit "peter parker" in both btrees.
	pk3, p3 := newPerson("gokue", "kakarot", "male", "email", "phone")
	b3.Add(ctx, pk3, p3)
	b3.Find(ctx, pk2, false)
	p2.SSN = "789"
	b3.UpdateCurrentItem(ctx, p2)

	b32.Find(ctx, pk2, false)
	p2.SSN = "xyz"
	b32.UpdateCurrentItem(ctx, p2)

	// Commit t1 & t2.
	_ = t1.Commit(ctx)
	_ = t2.Commit(ctx)
}

func TestStress_TwoTransactionsUpdatesOnSameNodeDifferentItems(t *testing.T) {
	t1, _ := newMockTransaction(t, sop.ForWriting, -1)
	t2, _ := newMockTransaction(t, sop.ForWriting, -1)

	t1.Begin(ctx)
	t2.Begin(ctx)

	b3, err := NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb",
		SlotLength:               nodeSlotLength,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        false,
		Description:              "",
	}, t1, Compare)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
	}

	pk, p := newPerson("joe", "pirelli", "male", "email", "phone")
	pk2, p2 := newPerson("joe2", "pirelli", "male", "email", "phone")

	found, _ := b3.Find(ctx, pk, false)
	if !found {
		b3.Add(ctx, pk, p)
		b3.Add(ctx, pk2, p2)
		t1.Commit(ctx)
		t1, _ = newMockTransaction(t, sop.ForWriting, -1)
		t1.Begin(ctx)
		b3, _ = NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
			Name:                     "persondb",
			SlotLength:               nodeSlotLength,
			IsUnique:                 false,
			IsValueDataInNodeSegment: false,
			LeafLoadBalancing:        false,
			Description:              "",
		}, t1, Compare)
	}

	b32, _ := NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb",
		SlotLength:               nodeSlotLength,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        false,
		Description:              "",
	}, t2, Compare)

	// edit both "pirellis" in both btrees, one each.
	b3.Find(ctx, pk, false)
	p.SSN = "789"
	b3.UpdateCurrentItem(ctx, p)

	b32.Find(ctx, pk2, false)
	p2.SSN = "abc"
	b32.UpdateCurrentItem(ctx, p2)

	_ = t1.Commit(ctx)
	_ = t2.Commit(ctx)
}

func TestStress_AddAndSearchManyPersons(t *testing.T) {
	trans, err := newMockTransaction(t, sop.ForWriting, -1)
	if err != nil {
		t.Fatal(err.Error())
	}

	trans.Begin(ctx)
	b3, err := NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb",
		SlotLength:               nodeSlotLength,
		IsUnique:                 false,
		IsValueDataInNodeSegment: false,
		LeafLoadBalancing:        false,
		Description:              "",
	}, trans, Compare)
	if err != nil {
		t.Errorf("Error instantiating Btree, details: %v.", err)
		t.Fail()
	}

	const start = 1
	end := start + batchSize

	for i := start; i < end; i++ {
		pk, p := newPerson(fmt.Sprintf("tracy%d", i), "swift", "female", "email", "phone")
		if ok, err := b3.Add(ctx, pk, p); !ok || err != nil {
			t.Errorf("b3.Add('tracy') failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
			return
		}
	}
	if err := trans.Commit(ctx); err != nil {
		t.Error(err.Error())
		t.Fail()
		return
	}
}

// ---- Value-data separate segment heavy/scaled runs ----
func TestStress_ValueDataInSeparateSegment_TwoTransactionsWithNoConflict(t *testing.T) {
	trans, err := newMockTransaction(t, sop.ForWriting, -1)
	if err != nil {
		t.Fatal(err.Error())
	}

	// Prepare second transaction handle but don't begin yet; we'll run it after committing the first.
	trans2, _ := newMockTransaction(t, sop.ForWriting, -1)

	trans.Begin(ctx)

	pk, p := newPerson("tracy", "swift", "female", "email", "phone")
	b3, err := NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                         "persondb7",
		SlotLength:                   nodeSlotLength,
		IsUnique:                     false,
		IsValueDataInNodeSegment:     false,
		IsValueDataActivelyPersisted: false,
		IsValueDataGloballyCached:    true,
		LeafLoadBalancing:            true,
		Description:                  "",
	}, trans, Compare)
	if err != nil {
		t.Errorf("Error instantiating Btree, details: %v.", err)
		t.Fail()
	}
	if ok, err := b3.Add(ctx, pk, p); !ok || err != nil {
		t.Errorf("b3.Add('tracy') failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		return
	}

	// Commit the first transaction before starting the second to avoid in-process concurrent writer instability.
	if err := trans.Commit(ctx); err != nil {
		t.Errorf("Commit 1 returned error, details: %v.", err)
	}

	// Now begin and use the second writer transaction with a different key.
	trans2.Begin(ctx)

	b32, err := NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                         "persondb7",
		SlotLength:                   nodeSlotLength,
		IsUnique:                     false,
		IsValueDataInNodeSegment:     false,
		IsValueDataActivelyPersisted: false,
		IsValueDataGloballyCached:    true,
		LeafLoadBalancing:            true,
		Description:                  "",
	}, trans2, Compare)
	if err != nil {
		t.Errorf("Error instantiating Btree, details: %v.", err)
		t.Fail()
	}
	pk2, p2 := newPerson("tracy2", "swift", "female", "email", "phone")
	if ok, err := b32.Add(ctx, pk2, p2); !ok || err != nil {
		t.Errorf("b32.Add('tracy2') failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
		return
	}

	if err := trans2.Commit(ctx); err != nil {
		t.Errorf("Commit 2 returned error, details: %v.", err)
	}
}

func TestStress_ValueDataInSeparateSegment_AddAndSearchManyPersons(t *testing.T) {
	trans, err := newMockTransaction(t, sop.ForWriting, -1)
	if err != nil {
		t.Fatal(err.Error())
	}

	trans.Begin(ctx)
	b3, err := NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                         "persondb7",
		SlotLength:                   nodeSlotLength,
		IsUnique:                     false,
		IsValueDataInNodeSegment:     false,
		IsValueDataActivelyPersisted: false,
		IsValueDataGloballyCached:    true,
		LeafLoadBalancing:            true,
		Description:                  "",
	}, trans, Compare)
	if err != nil {
		t.Errorf("Error instantiating Btree, details: %v.", err)
		t.Fail()
	}

	const start = 1
	end := start + batchSize

	for i := start; i < end; i++ {
		pk, p := newPerson(fmt.Sprintf("tracy%d", i), "swift", "female", "email", "phone")
		if ok, err := b3.Add(ctx, pk, p); !ok || err != nil {
			t.Errorf("b3.Add('tracy') failed, got(ok, err) = %v, %v, want = true, nil.", ok, err)
			return
		}
	}
	if err := trans.Commit(ctx); err != nil {
		t.Error(err.Error())
		t.Fail()
		return
	}

	trans, err = newMockTransaction(t, sop.ForReading, -1)
	if err != nil {
		t.Error(err.Error())
		t.Fail()
		return
	}

	if err := trans.Begin(ctx); err != nil {
		t.Error(err.Error())
		t.Fail()
		return
	}

	b3, err = OpenBtree[PersonKey, Person](ctx, "persondb7", trans, Compare)
	if err != nil {
		t.Errorf("Error instantiating Btree, details: %v.", err)
		t.Fail()
	}
	for i := start; i < end; i++ {
		pk, _ := newPerson(fmt.Sprintf("tracy%d", i), "swift", "female", "email", "phone")
		if ok, err := b3.Find(ctx, pk, true); !ok || err != nil {
			t.Errorf("b3.FIndOne('%s') failed, got(ok, err) = %v, %v, want = true, nil.", pk.Firstname, ok, err)
			return
		}
	}

	trans.Commit(ctx)
}
