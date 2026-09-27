package common

import (
	"cmp"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/v5"
)

func Test_TwoPhaseCommitRolledback(t *testing.T) {
	t1, _ := newMockTransaction(t, sop.ForWriting, -1)
	t1.Begin(ctx)

	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "twophase",
		SlotLength:               8,
		IsUnique:                 false,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
		Description:              "",
	}, t1, cmp.Compare)

	b3.Add(ctx, 5000, "I am the value with 5000 key.")
	b3.Add(ctx, 5001, "I am the value with 5001 key.")
	b3.Add(ctx, 5000, "I am also a value with 5000 key.")

	twoPhase := t1.GetPhasedTransaction()

	if err := twoPhase.Phase1Commit(ctx); err == nil {
		if err2 := my3rdPartyDBlogic(true); err2 != nil {
			twoPhase.Rollback(ctx, err2)
			return
		}
		t.Error("Should not go here.")
	} else {
		t.Error("Should not go here.")
	}
}

func Test_TwoPhaseCommitCommitted(t *testing.T) {
	t1, _ := newMockTransaction(t, sop.ForWriting, -1)
	t1.Begin(ctx)

	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "twophase1",
		SlotLength:               8,
		IsUnique:                 false,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
		Description:              "",
	}, t1, cmp.Compare)

	b3.Add(ctx, 5000, "I am the value with 5000 key.")
	b3.Add(ctx, 5001, "I am the value with 5001 key.")
	b3.Add(ctx, 5000, "I am also a value with 5000 key.")

	twoPhase := t1.GetPhasedTransaction()

	if err := twoPhase.Phase1Commit(ctx); err == nil {
		if err2 := my3rdPartyDBlogic(false); err2 != nil {
			t.Error("Should not go here.")
			return
		}
		twoPhase.Phase2Commit(ctx)

		t1, _ = newMockTransaction(t, sop.ForReading, -1)
		t1.Begin(ctx)
		b3, _ = OpenBtree[int, string](ctx, "twophase1", t1, cmp.Compare)
		twoPhase = t1.GetPhasedTransaction()

		if ok, _ := b3.Find(ctx, 5000, true); !ok || b3.GetCurrentKey().Key != 5000 {
			t.Errorf("FindOne(5000, true) failed, got = %v, want = 5000", b3.GetCurrentKey().Key)
		}
		if ok, _ := b3.Next(ctx); !ok || b3.GetCurrentKey().Key != 5000 {
			t.Errorf("Next() failed, got = %v, want = 5000", b3.GetCurrentKey().Key)
		}
		if ok, _ := b3.Next(ctx); !ok || b3.GetCurrentKey().Key != 5001 {
			t.Errorf("Next() failed, got = %v, want = 5001", b3.GetCurrentKey().Key)
		}
		// Call the two phase committers just for demo, but t1.Commit(..) will work fine too.
		if err = twoPhase.Phase1Commit(ctx); err != nil {
			t.Error(err)
		}
		if err = twoPhase.Phase2Commit(ctx); err != nil {
			t.Error(err)
		}
	} else {
		t.Error("Should not go here.")
	}
}

func Test_TwoPhaseCommitRolledbackThenCommitted(t *testing.T) {
	t1, _ := newMockTransaction(t, sop.ForWriting, -1)
	t1.Begin(ctx)

	b3, _ := NewBtree[int, string](ctx, sop.StoreOptions{
		Name:                     "twophase2",
		SlotLength:               8,
		IsUnique:                 true,
		IsValueDataInNodeSegment: true,
		LeafLoadBalancing:        true,
		Description:              "",
	}, t1, cmp.Compare)

	b3.Add(ctx, 5000, "I am the value with 5000 key.")
	b3.Add(ctx, 5001, "I am the value with 5001 key.")

	twoPhase := t1.GetPhasedTransaction()

	if err := twoPhase.Phase1Commit(ctx); err == nil {
		// Call 3rd party DB integration, failure.
		if err2 := my3rdPartyDBlogic(true); err2 != nil {
			twoPhase.Rollback(ctx, err2)

			t1, _ = newMockTransaction(t, sop.ForWriting, -1)
			t1.Begin(ctx)
			twoPhase := t1.GetPhasedTransaction()

			b3, err := NewBtree[int, string](ctx, sop.StoreOptions{
				Name:                     "twophase2",
				SlotLength:               8,
				IsUnique:                 true,
				IsValueDataInNodeSegment: true,
				LeafLoadBalancing:        true,
				Description:              "",
			}, t1, cmp.Compare)
			if err != nil {
				t.Fatalf("NewBtree failed: %v", err)
			}

			if ok, err := b3.Add(ctx, 5000, "I am the value with 5000 key."); !ok || err != nil {
				t.Fatalf("Add(5000) failed: %v, %v", ok, err)
			}
			if ok, err := b3.Add(ctx, 5001, "I am the value with 5001 key."); !ok || err != nil {
				t.Fatalf("Add(5001) failed: %v, %v", ok, err)
			}

			if ok, _ := b3.Find(ctx, 5000, true); !ok || b3.GetCurrentKey().Key != 5000 {
				t.Errorf("FindOne(5000, true) failed, got = %v, want = 5000", b3.GetCurrentKey().Key)
			}
			if ok, _ := b3.Next(ctx); !ok || b3.GetCurrentKey().Key != 5001 {
				t.Errorf("Next() failed, got = %v, want = 5001", b3.GetCurrentKey().Key)
			}

			// Call 1st phase commit.
			if err = twoPhase.Phase1Commit(ctx); err != nil {
				t.Error(err)
			}

			// Call 3rd party DB integration, success.
			if err := my3rdPartyDBlogic(false); err != nil {
				t.Error(err)
			}
			// Call 2nd phase commit.
			if err = twoPhase.Phase2Commit(ctx); err != nil {
				t.Error(err)
			}
			return
		}
		t.Error("Should not go here.")
	} else {
		t.Error("Should not go here.")
	}
}

func my3rdPartyDBlogic(induceError bool) error {
	if induceError {
		return fmt.Errorf("Simulate error in 3rd party DB call/interaction.")
	}
	return nil
}
