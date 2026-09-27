//go:build stress
// +build stress

package stresstests

import (
	"context"
	"fmt"
	log "log/slog"
	"testing"

	"golang.org/x/sync/errgroup"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

// Covers all of these cases:
// Two transactions updating same item.
// Two transactions updating different items with collision on 1 item.
// Transaction rolls back, new completes fine.
// Reader transaction succeeds.
func Test_TwoTransactionsUpdatesOnSameItem(t *testing.T) {
	ctx := context.Background()
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ := infs.NewTransaction(ctx, to)
	t2, _ := infs.NewTransaction(ctx, to)

	t1.Begin(ctx)
	t2.Begin(ctx)

	b3, err := infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb77",
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t1, Compare)
	if err != nil {
		t.Error(err.Error()) // most likely, the "persondb77" b-tree store has not been created yet.
		t.Fail()
	}

	pk, p := newPerson("peter", "swift", "male", "email", "phone")
	pk2, p2 := newPerson("peter", "parker", "male", "email", "phone")

	found, _ := b3.Find(ctx, pk, false)
	if !found {
		b3.Add(ctx, pk, p)
		b3.Add(ctx, pk2, p2)
		t1.Commit(ctx)
		t1, _ = infs.NewTransaction(ctx, to)
		t1.Begin(ctx)
		b3, _ = infs.OpenBtree[PersonKey, Person](ctx, "persondb77", t1, Compare)
	}

	b32, _ := infs.OpenBtree[PersonKey, Person](ctx, "persondb77", t2, Compare)

	// edit "peter parker" in both btrees.
	pk3, p3 := newPerson("gokue", "kakarot", "male", "email", "phone")
	b3.Add(ctx, pk3, p3)
	b3.Find(ctx, pk2, false)
	p2.SSN = "789"
	b3.UpdateCurrentValue(ctx, p2)

	b32.Find(ctx, pk2, false)
	p2.SSN = "xyz"
	b32.UpdateCurrentValue(ctx, p2)

	// Commit t1 & t2.
	err1 := t1.Commit(ctx)
	err2 := t2.Commit(ctx)
	if err1 != nil {
		t.Errorf("Commit #1, got = fail, want = success, details: %v", err1)
	}
	if err2 == nil {
		t.Error("Commit #2, got = succeess, want = fail.")
	}
	to2 := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForReading, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ = infs.NewTransaction(ctx, to2)
	t1.Begin(ctx)
	b3, _ = infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb77",
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t1, Compare)
	var person Person
	b3.Find(ctx, pk2, false)
	person, _ = b3.GetCurrentValue(ctx)
	if err1 == nil {
		if person.SSN != "789" {
			t.Errorf("Got SSN = %s, want = 789", person.SSN)
		}
	}
	if err2 == nil {
		if person.SSN != "xyz" {
			t.Errorf("Got SSN = %s, want = xyz", person.SSN)
		}
	}
	if err := t1.Commit(ctx); err != nil {
		t.Error(err.Error())
	}
}

// Two transactions updating different items with no collision but items'
// keys are sequential/contiguous between the two.
func Test_TwoTransactionsUpdatesOnSameNodeDifferentItems(t *testing.T) {
	ctx := context.Background()
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ := infs.NewTransaction(ctx, to)
	t2, _ := infs.NewTransaction(ctx, to)

	t1.Begin(ctx)
	t2.Begin(ctx)

	b3, err := infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb77",
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t1, Compare)
	if err != nil {
		t.Error(err.Error()) // most likely, the "persondb77" b-tree store has not been created yet.
		t.Fail()
	}

	pk, p := newPerson("joe", "pirelli", "male", "email", "phone")
	pk2, p2 := newPerson("joe2", "pirelli", "male", "email", "phone")

	found, _ := b3.Find(ctx, pk, false)
	if !found {
		b3.Add(ctx, pk, p)
		b3.Add(ctx, pk2, p2)
		t1.Commit(ctx)
		t1, _ = infs.NewTransaction(ctx, to)
		t1.Begin(ctx)
		b3, _ = infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
			Name:                     "persondb77",
			SlotLength:               nodeSlotLength,
			IsValueDataInNodeSegment: true,
		}, t1, Compare)
	}

	b32, _ := infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb77",
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t2, Compare)

	// edit both "pirellis" in both btrees, one each.
	b3.Find(ctx, pk, false)
	p.SSN = "789"
	b3.UpdateCurrentValue(ctx, p)

	b32.Find(ctx, pk2, false)
	p2.SSN = "abc"
	b32.UpdateCurrentValue(ctx, p2)

	// Commit t1 & t2.
	err1 := t1.Commit(ctx)
	err2 := t2.Commit(ctx)
	if err1 != nil {
		t.Error(err1)
	}
	if err2 != nil {
		t.Error(err2)
	}
}

// Reader transaction fails commit when an item read was modified by another transaction in-flight.
func Test_TwoTransactionsOneReadsAnotherWritesSameItem(t *testing.T) {
	ctx := context.Background()
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ := infs.NewTransaction(ctx, to)
	to2 := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForReading, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t2, _ := infs.NewTransaction(ctx, to2)

	t1.Begin(ctx)
	t2.Begin(ctx)

	b3, err := infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb77",
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t1, Compare)
	if err != nil {
		t.Error(err.Error()) // most likely, the "persondb77" b-tree store has not been created yet.
		t.Fail()
	}

	pk, p := newPerson("joe", "zoey", "male", "email", "phone")
	pk2, p2 := newPerson("joe2", "zoey", "male", "email", "phone")

	found, _ := b3.Find(ctx, pk, false)
	if !found {
		b3.Add(ctx, pk, p)
		b3.Add(ctx, pk2, p2)
		t1.Commit(ctx)
		t1, _ = infs.NewTransaction(ctx, to)
		t1.Begin(ctx)
		b3, _ = infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
			Name:                     "persondb77",
			SlotLength:               nodeSlotLength,
			IsValueDataInNodeSegment: true,
		}, t1, Compare)
	}

	b32, _ := infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb77",
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t2, Compare)

	// Read both records.
	b32.Find(ctx, pk2, false)
	b32.GetCurrentValue(ctx)
	b32.Find(ctx, pk, false)
	b32.GetCurrentValue(ctx)

	// update one of the two records read on the reader transaction.
	b3.Find(ctx, pk, false)
	p.SSN = "789"
	b3.UpdateCurrentValue(ctx, p)

	// Commit t1 & t2.
	if err := t1.Commit(ctx); err != nil {
		t.Errorf("t1 writer Commit got error, want success, details: %v.", err)
	}
	if err := t2.Commit(ctx); err == nil {
		t.Errorf("t2 reader Commit got success, want error.")
	}
}

// Node merging and row(or item) level conflict detection.
// Case: Reader transaction succeeds commit, while another item in same Node got updated by another transaction.
func Test_TwoTransactionsOneReadsAnotherWritesAnotherItemOnSameNode(t *testing.T) {
	ctx := context.Background()
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ := infs.NewTransaction(ctx, to)
	to2 := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForReading, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t2, _ := infs.NewTransaction(ctx, to2)

	t1.Begin(ctx)
	t2.Begin(ctx)

	b3, err := infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb77",
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t1, Compare)
	if err != nil {
		t.Error(err.Error()) // most likely, the "persondb77" b-tree store has not been created yet.
		t.Fail()
	}

	pk, p := newPerson("joe", "zoeya", "male", "email", "phone")
	pk2, p2 := newPerson("joe2", "zoeya", "male", "email", "phone")
	pk3, p3 := newPerson("joe3", "zoeya", "male", "email", "phone")

	found, _ := b3.Find(ctx, pk, false)
	if !found {
		b3.Add(ctx, pk, p)
		b3.Add(ctx, pk2, p2)
		b3.Add(ctx, pk3, p3)
		t1.Commit(ctx)
		t1, _ = infs.NewTransaction(ctx, to)
		t1.Begin(ctx)
		b3, _ = infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
			Name:                     "persondb77",
			SlotLength:               nodeSlotLength,
			IsValueDataInNodeSegment: true,
		}, t1, Compare)
	}

	b32, _ := infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb77",
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t2, Compare)

	// Read both records.
	b32.Find(ctx, pk2, false)
	b32.GetCurrentValue(ctx)
	b32.Find(ctx, pk, false)
	b32.GetCurrentValue(ctx)

	// update item #3 that should be on same node.
	b3.Find(ctx, pk3, false)
	p.SSN = "789"
	b3.UpdateCurrentValue(ctx, p)

	// Commit t1 & t2.
	if err := t1.Commit(ctx); err != nil {
		t.Errorf("t1 writer Commit got error, want success, details: %v.", err)
	}
	if err := t2.Commit(ctx); err != nil {
		t.Errorf("t2 reader Commit got error, want success, details: %v.", err)
	}
}

// One transaction updates a colliding item in 1st and a 2nd trans.
func Test_TwoTransactionsOneUpdateItemOneAnotherUpdateItemLast(t *testing.T) {
	ctx := context.Background()
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ := infs.NewTransaction(ctx, to)
	t2, _ := infs.NewTransaction(ctx, to)

	t1.Begin(ctx)
	t2.Begin(ctx)

	b3, err := infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb77",
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t1, Compare)
	if err != nil {
		t.Error(err.Error()) // most likely, the "persondb77" b-tree store has not been created yet.
		t.Fail()
	}

	pk, p := newPerson("joe", "zoeyb", "male", "email", "phone")
	pk2, p2 := newPerson("joe2", "zoeyb", "male", "email", "phone")
	pk3, p3 := newPerson("joe3", "zoeyb", "male", "email", "phone")
	pk4, p4 := newPerson("joe4", "zoeyb", "male", "email", "phone")
	pk5, p5 := newPerson("joe5", "zoeyb", "male", "email", "phone")

	found, _ := b3.Find(ctx, pk, false)
	if !found {
		b3.Add(ctx, pk, p)
		b3.Add(ctx, pk2, p2)
		b3.Add(ctx, pk3, p3)
		b3.Add(ctx, pk4, p4)
		b3.Add(ctx, pk5, p5)
		t1.Commit(ctx)
		t1, _ = infs.NewTransaction(ctx, to)
		t1.Begin(ctx)
		b3, _ = infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
			Name:                     "persondb77",
			SlotLength:               nodeSlotLength,
			IsValueDataInNodeSegment: true,
		}, t1, Compare)
	}

	b32, _ := infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "persondb77",
		SlotLength:               nodeSlotLength,
		IsValueDataInNodeSegment: true,
	}, t2, Compare)

	b3.Find(ctx, pk, false)
	ci, _ := b3.GetCurrentItem(ctx)
	itemID := ci.ID
	p.SSN = "789"
	b3.UpdateCurrentValue(ctx, p)

	// Cause an update to "joe zoeyb" on t2, 'should generate conflict!
	b32.FindWithID(ctx, pk, itemID)
	p.SSN = "555"
	b32.UpdateCurrentValue(ctx, p)

	b3.Find(ctx, pk2, false)
	b3.GetCurrentValue(ctx)
	b3.Find(ctx, pk3, false)
	b3.GetCurrentValue(ctx)
	b3.Find(ctx, pk4, false)
	b3.GetCurrentValue(ctx)
	b3.Find(ctx, pk5, false)
	b3.GetCurrentValue(ctx)

	b32.Find(ctx, pk5, false)
	p.SSN = "789"
	b32.UpdateCurrentValue(ctx, p)

	b32.Find(ctx, pk4, false)
	b32.GetCurrentValue(ctx)
	b32.Find(ctx, pk3, false)
	b32.GetCurrentValue(ctx)
	b32.Find(ctx, pk2, false)
	b32.GetCurrentValue(ctx)

	// Commit t1 & t2.
	err1 := t1.Commit(ctx)
	err2 := t2.Commit(ctx)

	if err1 == nil && err2 == nil {
		t.Errorf("T1 & T2 Commits got 2 success, want 1 fail.")
	}
	if err1 != nil {
		t.Log(err1.Error())
	}
	if err2 != nil {
		t.Log(err2.Error())
	}
}

func Test_Concurrent2CommitsOnNewBtree(t *testing.T) {
	// Needs a new context so our runs don't affect one another.
	ctx := context.Background()
	ec := fs.GetGlobalErasureConfig()
	infs.RemoveBtree(ctx, "twophase3", []string{dataPath}, ec, sop.Redis)

	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ := infs.NewTransaction(ctx, to)
	t1.Begin(ctx)
	b3, _ := infs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "twophase3",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
	}, t1, nil)
	// Add a single item so we persist "root node".
	b3.Add(ctx, 500, "I am the value with 500 key.")
	t1.Commit(ctx)

	eg, ctx2 := errgroup.WithContext(ctx)
	eg2, ctx3 := errgroup.WithContext(ctx)

	f1 := func() error {
		t1, _ := infs.NewTransaction(ctx, to)
		t1.Begin(ctx)
		b3, _ := infs.NewBtree[int, string](ctx2, sop.StoreOptions{
			Name:                     "twophase3",
			SlotLength:               8,
			IsValueDataInNodeSegment: true,
			LeafLoadBalancing:        true,
		}, t1, nil)
		b3.Add(ctx2, 5000, "I am the value with 5000 key.")
		b3.Add(ctx2, 5001, "I am the value with 5001 key.")
		b3.Add(ctx2, 5002, "I am also a value with 5000 key.")
		return t1.Commit(ctx2)
	}

	f2 := func() error {
		t2, _ := infs.NewTransaction(ctx, to)
		t2.Begin(ctx)
		b32, _ := infs.NewBtree[int, string](ctx3, sop.StoreOptions{
			Name:                     "twophase3",
			SlotLength:               8,
			IsValueDataInNodeSegment: true,
			LeafLoadBalancing:        true,
		}, t2, nil)
		b32.Add(ctx3, 5500, "I am the value with 5000 key.")
		b32.Add(ctx3, 5501, "I am the value with 5001 key.")
		b32.Add(ctx3, 5502, "I am also a value with 5000 key.")
		return t2.Commit(ctx3)
	}

	eg.Go(f1)
	eg2.Go(f2)

	err := eg.Wait()
	err2 := eg2.Wait()
	if err != nil && err2 != nil {
		t.Errorf("expected at least one success, but got err: %v, err2: %v", err, err2)
		t.FailNow()
		return
	}

	to2 := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForReading, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ = infs.NewTransaction(ctx, to2)
	t1.Begin(ctx)

	b3, _ = infs.OpenBtree[int, string](ctx, "twophase3", t1, nil)

	b3.First(ctx)
	i := 1
	for {
		if ok, err := b3.Next(ctx); err != nil {
			t.Error(err)
		} else if !ok {
			break
		}
		i++
	}
	if i != 4 && i < 6 {
		t.Errorf("Failed, traversing/counting all records, got %d, want 4 or 6+.", i)
	}
}

/*
- A commit with no conflict: commit success
- A commit with partial conflict: retry success
- A commit with full conflict: retry success
*/
func Test_ConcurrentCommitsComplexDupeAllowed(t *testing.T) {
	// for {
	ctx := context.Background()
	ec := fs.GetGlobalErasureConfig()
	infs.RemoveBtree(ctx, "tablex", []string{dataPath}, ec, sop.Redis)

	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ := infs.NewTransaction(ctx, to)
	t1.Begin(ctx)
	b3, _ := infs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "tablex",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
	}, t1, nil)
	// Add a single item so we persist "root node".
	b3.Add(ctx, 1, "I am the value with 500 key.")
	t1.Commit(ctx)

	eg2, ctx2 := errgroup.WithContext(ctx)
	eg3, ctx3 := errgroup.WithContext(ctx)
	eg4, ctx4 := errgroup.WithContext(ctx)

	f1 := func() error {
		t1, _ := infs.NewTransaction(ctx, to)
		t1.Begin(ctx)
		log.Info(fmt.Sprintf("T1 ID: %v", t1.GetID()))
		b3, _ := infs.OpenBtree[int, string](ctx2, "tablex", t1, nil)
		b3.Add(ctx2, 50, "I am the value with 5000 key.")
		b3.Add(ctx2, 51, "I am the value with 5001 key.")
		b3.Add(ctx2, 52, "I am also a value with 5000 key.")
		log.Debug(fmt.Sprintf("********** before f1 commit, tid: %v", t1.GetID()))
		return t1.Commit(ctx2)
	}

	f2 := func() error {
		t2, _ := infs.NewTransaction(ctx, to)
		t2.Begin(ctx)
		log.Info(fmt.Sprintf("T2 ID: %v", t2.GetID()))
		b32, _ := infs.OpenBtree[int, string](ctx3, "tablex", t2, nil)
		b32.Add(ctx3, 550, "I am the value with 5000 key.")
		b32.Add(ctx3, 551, "I am the value with 5001 key.")
		b32.Add(ctx3, 552, "I am also a value with 5000 key.")
		log.Debug(fmt.Sprintf("********** before f2 commit, tid: %v", t2.GetID()))
		return t2.Commit(ctx3)
	}

	f3 := func() error {
		t3, _ := infs.NewTransaction(ctx, to)
		t3.Begin(ctx)
		log.Info(fmt.Sprintf("T3 ID: %v", t3.GetID()))
		b32, _ := infs.OpenBtree[int, string](ctx4, "tablex", t3, nil)
		b32.Add(ctx4, 550, "random foo.")
		b32.Add(ctx4, 551, "bar hello.")
		log.Debug(fmt.Sprintf("********** before f3 commit, tid: %v", t3.GetID()))
		return t3.Commit(ctx4)
	}

	eg2.Go(f1)
	eg3.Go(f2)
	eg4.Go(f3)

	failCount := 0
	if err := eg2.Wait(); err != nil {
		failCount++
	}
	if err := eg3.Wait(); err != nil {
		failCount++
	}
	if err := eg4.Wait(); err != nil {
		failCount++
	}

	if failCount > 0 {
		t.Errorf("commit should all succeed, failed: %d. Errors: %v, %v, %v", failCount, eg2.Wait(), eg3.Wait(), eg4.Wait())
		t.FailNow()
	}

	to2 := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForReading, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ = infs.NewTransaction(ctx, to2)
	t1.Begin(ctx)

	b3, _ = infs.OpenBtree[int, string](ctx, "tablex", t1, nil)
	b3.First(ctx)
	i := 1
	for {
		k := b3.GetCurrentKey()
		log.Info(fmt.Sprintf("Found Item: Key=%v, ID=%v", k.Key, k.ID))
		if ok, err := b3.Next(ctx); err != nil {
			t.Error(err)
			t.FailNow()
		} else if !ok {
			break
		}
		i++
	}
	if i < 5 {
		t.Errorf("Failed, traversing/counting all records, got %d, want at least 5.", i)
		// break
	}
	if i != int(b3.Count()) {
		t.Errorf("Failed, traversing/counting all records, got %d, but Count() returned %d.", i, b3.Count())
		// break
	}
	log.Info("loop end, PASSED")
	log.Info("")
	log.Info("")
	// }
}

/*
- A commit with no conflict: commit success
- A commit with partial conflict.
- A commit with full conflict.
*/
func Test_ConcurrentCommitsComplexDupeNotAllowed(t *testing.T) {
	ctx := context.Background()
	ec := fs.GetGlobalErasureConfig()
	infs.RemoveBtree(ctx, "tablex2", []string{dataPath}, ec, sop.Redis)

	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ := infs.NewTransaction(ctx, to)
	t1.Begin(ctx)
	b3, _ := infs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "tablex2",
		SlotLength:               8,
		IsUnique:                 true,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
	}, t1, nil)
	// Add a single item so we persist "root node".
	b3.Add(ctx, 1, "I am the value with 500 key.")
	t1.Commit(ctx)

	eg, ctx2 := errgroup.WithContext(ctx)
	eg3, ctx3 := errgroup.WithContext(ctx)
	eg4, ctx4 := errgroup.WithContext(ctx)

	f1 := func() error {
		t1, _ := infs.NewTransaction(ctx2, to)
		t1.Begin(ctx)
		b3, _ := infs.OpenBtree[int, string](ctx2, "tablex2", t1, nil)
		b3.Add(ctx2, 50, "I am the value with 5000 key.")
		b3.Add(ctx2, 51, "I am the value with 5001 key.")
		b3.Add(ctx2, 52, "I am also a value with 5000 key.")
		return t1.Commit(ctx2)
	}

	f2 := func() error {
		t2, _ := infs.NewTransaction(ctx3, to)
		t2.Begin(ctx)
		b32, _ := infs.OpenBtree[int, string](ctx3, "tablex2", t2, nil)
		if ok, err := b32.Add(ctx3, 550, "I am the value with 5000 key."); !ok || err != nil {
			if !ok {
				err = fmt.Errorf("expected error")
			}
			t2.Rollback(ctx3)
			return err
		}
		if ok, err := b32.Add(ctx3, 551, "I am the value with 5001 key."); !ok || err != nil {
			if !ok {
				err = fmt.Errorf("expected error")
			}
			t2.Rollback(ctx3)
			return err
		}
		b32.Add(ctx3, 552, "I am the value with 5001 key.")
		return t2.Commit(ctx3)
	}

	f3 := func() error {
		t3, _ := infs.NewTransaction(ctx4, to)
		t3.Begin(ctx)
		b32, _ := infs.OpenBtree[int, string](ctx4, "tablex2", t3, nil)
		if ok, err := b32.Add(ctx4, 550, "random foo."); !ok || err != nil {
			if !ok {
				err = fmt.Errorf("expected error")
			}
			t3.Rollback(ctx4)
			return err
		}
		if ok, err := b32.Add(ctx4, 551, "bar hello."); !ok || err != nil {
			if !ok {
				err = fmt.Errorf("expected error")
			}
			t3.Rollback(ctx4)
			return err
		}
		return t3.Commit(ctx4)
	}

	eg.Go(f1)
	if err := eg.Wait(); err != nil {
		t.Error(err)
		t.FailNow()
	}

	eg3.Go(f2)
	eg4.Go(f3)

	var err3 error
	var err4 error

	err3 = eg3.Wait()
	err4 = eg4.Wait()

	if err3 == nil && err4 == nil {
		t.Error("failed, got no error on err3 & err4, want an error")
		t.FailNow()
	}

	to2 := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForReading, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ = infs.NewTransaction(ctx, to2)
	t1.Begin(ctx)

	b3, _ = infs.OpenBtree[int, string](ctx, "tablex2", t1, nil)
	b3.First(ctx)
	i := 1
	for {
		fmt.Printf("Item with key: %v\n", b3.GetCurrentKey().Key)
		if ok, err := b3.Next(ctx); err != nil {
			t.Error(err)
		} else if !ok {
			break
		}
		i++
	}
	if i < 3 || i > 7 {
		t.Errorf("Failed, traversing/counting all records, got %d, want 3 to 7", i)
	}
	fmt.Printf("Count of records: %d\n", i)
}

/*
- A commit with no conflict: commit success
- A commit with partial conflict on update: rollback
- A commit with full conflict on update: rollback
*/
func Test_ConcurrentCommitsComplexUpdateConflicts(t *testing.T) {
	ctx := context.Background()
	ec := fs.GetGlobalErasureConfig()
	infs.RemoveBtree(ctx, "tabley", []string{dataPath}, ec, sop.Redis)

	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	t1, _ := infs.NewTransaction(ctx, to)
	t1.Begin(ctx)
	b3, _ := infs.NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "tabley",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
	}, t1, nil)
	// Add a single item so we persist "root node".
	b3.Add(ctx, 1, "I am the value with 500 key.")
	b3.Add(ctx, 550, "I am the value with 5000 key.")
	b3.Add(ctx, 551, "I am the value with 5001 key.")
	b3.Add(ctx, 552, "I am the value with 5001 key.")
	t1.Commit(ctx)

	eg, ctx2 := errgroup.WithContext(ctx)
	eg2, ctx3 := errgroup.WithContext(ctx)
	eg3, ctx4 := errgroup.WithContext(ctx)

	f1 := func() error {
		t1, _ := infs.NewTransaction(ctx3, to)
		t1.Begin(ctx)
		b3, _ := infs.OpenBtree[int, string](ctx3, "tabley", t1, nil)
		b3.Add(ctx3, 50, "I am the value with 5000 key.")
		b3.Add(ctx3, 51, "I am the value with 5001 key.")
		b3.Add(ctx3, 52, "I am also a value with 5000 key.")
		return t1.Commit(ctx3)
	}

	f2 := func() error {
		t2, _ := infs.NewTransaction(ctx2, to)
		t2.Begin(ctx)
		b32, _ := infs.OpenBtree[int, string](ctx2, "tabley", t2, nil)
		b32.Update(ctx2, 550, "I am the value with 5000 key.")
		b32.Update(ctx2, 551, "I am the value with 5001 key.")
		b32.Update(ctx2, 552, "I am the value with 5001 key.")
		return t2.Commit(ctx2)
	}

	f3 := func() error {
		t3, _ := infs.NewTransaction(ctx4, to)
		t3.Begin(ctx)
		b32, _ := infs.OpenBtree[int, string](ctx4, "tabley", t3, nil)
		b32.Update(ctx4, 550, "random foo.")
		b32.Update(ctx4, 551, "bar hello.")
		return t3.Commit(ctx4)
	}

	eg2.Go(f1)
	eg.Go(f2)
	eg3.Go(f3)

	if err := eg2.Wait(); err != nil {
		t.Error(err)
		t.FailNow()
		return
	}
	err := eg.Wait()
	err3 := eg3.Wait()

	if err == nil && err3 == nil {
		t.Error("err or err3 should have errored but both did not")
		t.FailNow()
	}
	if err != nil {
		log.Info(err.Error())
	}
	if err3 != nil {
		log.Info(err3.Error())
	}

	to2 := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForReading, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}

	t1, _ = infs.NewTransaction(ctx, to2)
	t1.Begin(ctx)

	var errOpen error
	b3, errOpen = infs.OpenBtree[int, string](ctx, "tabley", t1, nil)
	if errOpen != nil {
		t.Errorf("OpenBtree failed: %v", errOpen)
		t.FailNow()
	}
	if ok, err := b3.First(ctx); err != nil {
		t.Errorf("First failed: %v", err)
		t.FailNow()
	} else if !ok {
		t.Errorf("First returned false, store might be empty")
	}

	i := 1
	for {
		fmt.Printf("Item with key: %v\n", b3.GetCurrentKey().Key)
		if ok, err := b3.Next(ctx); err != nil {
			t.Error(err)
			break
		} else if !ok {
			break
		}
		i++
	}
	if i != 7 {
		t.Errorf("Failed, traversing/counting all records, got %d, want 7.", i)
	}
}
