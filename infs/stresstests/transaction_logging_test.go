//go:build stress
// +build stress

package stresstests

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
)

func MultipleExpiredTransCleanup(t *testing.T) {
	ctx := context.Background()
	ec := fs.GetGlobalErasureConfig()
	infs.RemoveBtree(ctx, "ztab1", []string{dataPath}, ec, sop.Redis)

	// Seed with good records.
	yesterday := time.Now().Add(time.Duration(-48 * time.Hour))
	sop.Now = func() time.Time { return yesterday }

	to := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForWriting, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	trans, _ := infs.NewTransaction(ctx, to)
	trans.Begin(ctx)

	b3, _ := infs.NewBtree[PersonKey, Person](ctx, sop.StoreOptions{
		Name:                     "ztab1",
		SlotLength:               8,
		IsValueDataInNodeSegment: true,
	}, trans, Compare)

	for i := 0; i < 50; i++ {
		pk, p := newPerson("joe", fmt.Sprintf("krueger%d", i), "male", "email", "phone")
		b3.Add(ctx, pk, p)
	}

	trans.Commit(ctx)

	// Create & leave transaction 1 resources for cleanup.
	yesterday = time.Now().Add(time.Duration(-47 * time.Hour))
	sop.Now = func() time.Time { return yesterday }

	trans, _ = infs.NewTransaction(ctx, to)
	trans.Begin(ctx)

	b3, _ = infs.OpenBtree[PersonKey, Person](ctx, "ztab1", trans, Compare)
	pk, p := newPerson("joe", "krueger77", "male", "email", "phone")
	b3.Add(ctx, pk, p)

	trans.GetPhasedTransaction().Phase1Commit(ctx)

	// Create & leave transaction 2 resources for cleanup.
	yesterday = time.Now().Add(time.Duration(-46 * time.Hour))
	sop.Now = func() time.Time { return yesterday }

	trans, _ = infs.NewTransaction(ctx, to)
	trans.Begin(ctx)

	b3, _ = infs.OpenBtree[PersonKey, Person](ctx, "ztab1", trans, Compare)
	pk, p = newPerson("joe", "krueger47", "male", "email2", "phone")
	b3.Update(ctx, pk, p)

	trans.GetPhasedTransaction().Phase1Commit(ctx)

	yesterday = time.Now()
	sop.Now = func() time.Time { return yesterday }

	trans, _ = infs.NewTransaction(ctx, to)

	// Cleanup should be launched from this call.
	trans.Begin(ctx)
}

func Cleanup(t *testing.T) {
	ctx := context.Background()
	yesterday := time.Now().Add(time.Duration(-24 * time.Hour))
	sop.Now = func() time.Time { return yesterday }

	to2 := sop.TransactionOptions{StoresFolders: []string{dataPath}, Mode: sop.ForReading, MaxTime: -1, RegistryHashModValue: fs.MinimumModValue, CacheType: l2Cache}
	trans, _ := infs.NewTransaction(ctx, to2)
	trans.Begin(ctx)
	_, _ = infs.OpenBtree[PersonKey, Person](ctx, "ztab1", trans, Compare)
	trans.Commit(ctx)

	yesterday = time.Now().Add(-time.Duration(23*time.Hour + 54*time.Minute))
	sop.Now = func() time.Time { return yesterday }

	trans, _ = infs.NewTransaction(ctx, to2)
	trans.Begin(ctx)
	_, _ = infs.OpenBtree[PersonKey, Person](ctx, "ztab1", trans, Compare)
	trans.Commit(ctx)
}
