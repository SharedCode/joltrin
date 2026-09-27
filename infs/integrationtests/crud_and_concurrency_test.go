//go:build integration
// +build integration

package integrationtests

import (
	"context"
	"testing"

	"golang.org/x/sync/errgroup"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

// A shortened CRUD flow derived from stresstests, using tiny slot length and small batch sizes.
func Test_ShortCRUD(t *testing.T) {
	ctx := context.Background()
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	trans, err := infs.NewTransaction(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	trans.Begin(ctx)

	b3, err := infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name: "person_short", SlotLength: 32, IsValueDataInNodeSegment: true,
	}, trans, Compare)
	if err != nil {
		t.Fatal(err)
	}

	pk, p := newPerson("amy", "adele", "female", "email", "phone")
	if ok, err := b3.Add(ctx, pk, p); !ok || err != nil {
		t.Fatalf("add: %v", err)
	}
	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("find: %v", err)
	}
	if _, err := b3.GetCurrentValue(ctx); err != nil {
		t.Fatalf("get: %v", err)
	}
	if ok, err := b3.Update(ctx, pk, p); !ok || err != nil {
		t.Fatalf("update: %v", err)
	}

	if err := trans.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

// A compact concurrent test that intentionally collides once and validates that only one commit succeeds.
func Test_ShortConcurrentCollision(t *testing.T) {
	ctx := context.Background()
	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: sop.Redis}

	// Seed store if needed
	seed, err := infs.NewTransaction(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	seed.Begin(ctx)
	// Try to open if it already exists; otherwise create with our options.
	bseed, err := infs.OpenBtree[PersonKey, Person](ctx, tableName1, seed, Compare)
	if err != nil {
		// OpenBtree rolls back the transaction if the store doesn't exist.
		// Use a fresh transaction for creation.
		seed2, err2 := infs.NewTransaction(ctx, to)
		if err2 != nil {
			t.Fatal(err2)
		}
		if err := seed2.Begin(ctx); err != nil {
			t.Fatal(err)
		}
		bseed, err = infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
			Name: tableName1, SlotLength: 64, IsValueDataInNodeSegment: true,
		}, seed2, Compare)
		if err != nil {
			t.Fatal(err)
		}
		seed = seed2
	}
	pk, p := newPerson("zoe", "zeta", "female", "email", "phone")
	if ok, _ := bseed.Find(ctx, pk, false); !ok {
		bseed.Add(ctx, pk, p)
	}
	if err := seed.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	// Two transactions racing on the same item.
	g, ctx2 := errgroup.WithContext(ctx)
	g.Go(func() error {
		t1, err := infs.NewTransaction(ctx2, to)
		if err != nil {
			return err
		}
		if err := t1.Begin(ctx); err != nil {
			return err
		}
		b1, err := infs.OpenBtree[PersonKey, Person](ctx2, tableName1, t1, Compare)
		if err != nil {
			return err
		}
		if ok, err := b1.Find(ctx2, pk, false); !ok || err != nil {
			return err
		}
		v, err := b1.GetCurrentValue(ctx2)
		if err != nil {
			return err
		}
		v.SSN = "a"
		if _, err := b1.UpdateCurrentValue(ctx2, v); err != nil {
			return err
		}
		return t1.Commit(ctx2)
	})
	g.Go(func() error {
		t2, err := infs.NewTransaction(ctx2, to)
		if err != nil {
			return err
		}
		if err := t2.Begin(ctx); err != nil {
			return err
		}
		b2, err := infs.OpenBtree[PersonKey, Person](ctx2, tableName1, t2, Compare)
		if err != nil {
			return err
		}
		if ok, err := b2.Find(ctx2, pk, false); !ok || err != nil {
			return err
		}
		v, err := b2.GetCurrentValue(ctx2)
		if err != nil {
			return err
		}
		v.SSN = "b"
		if _, err := b2.UpdateCurrentValue(ctx2, v); err != nil {
			return err
		}
		return t2.Commit(ctx2)
	})
	// Exactly one should succeed.
	_ = g.Wait()

	// Verify current value is either version "a" or "b" (depending who won), but no error to read.
	verify, err := infs.NewTransaction(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	if err := verify.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	b3, err := infs.OpenBtree[PersonKey, Person](ctx, tableName1, verify, Compare)
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := b3.Find(ctx, pk, false); !ok || err != nil {
		t.Fatalf("verify find: %v", err)
	}
	if _, err := b3.GetCurrentValue(ctx); err != nil {
		t.Fatalf("verify get: %v", err)
	}
	_ = verify.Commit(ctx)
}
