//go:build integration
// +build integration

package integrationtests

import (
	"cmp"
	"context"
	"testing"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

var l2Cache = sop.Redis

func Test_Basic_EC_Short(t *testing.T) {
	ctx := context.Background()
	to := sop.TransactionOptions{Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, StoresFolders: storesFolders, CacheType: l2Cache}
	trans, err := infs.NewTransactionWithReplication(ctx, to)
	if err != nil {
		t.Fatal(err)
	}
	if err := trans.Begin(ctx); err != nil {
		t.Fatal(err)
	}
	b3, err := infs.NewBtreeWithReplication[int, string](ctx, sop.StoreOptions{
		// Use a unique name to avoid colliding with prior runs/configs
		Name: "barstoreec_short_it", SlotLength: 8, IsValueDataInNodeSegment: true,
	}, trans, cmp.Compare)
	if err != nil {
		t.Fatal(err)
	}

	if ok, err := b3.Add(ctx, 1, "hello world"); !ok || err != nil {
		t.Fatalf("add err: %v", err)
	}
	if ok, err := b3.Find(ctx, 1, false); !ok || err != nil {
		t.Fatalf("find err: %v", err)
	}
	if v, err := b3.GetCurrentValue(ctx); err != nil || v != "hello world" {
		t.Fatalf("got %v err %v", v, err)
	}

	if err := trans.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}
