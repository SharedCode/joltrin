package common_test

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
	_ "github.com/sharedcode/joltrin/v5/cache"
)

func TestOnCommit_FiresOnCommit(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	trans, err := infs.NewTransaction(ctx, sop.TransactionOptions{
		Mode:          sop.ForWriting,
		MaxTime:       -1,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	})
	if err != nil {
		t.Fatalf("NewTransaction failed: %v", err)
	}

	if err := trans.Begin(ctx); err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	fired := false
	trans.OnCommit(func(ctx context.Context) error {
		fired = true
		return nil
	})

	if err := trans.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if !fired {
		t.Error("OnCommit hook was not fired after Commit")
	}
}

func TestOnCommit_DoesNotFireOnRollback(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()
	trans, err := infs.NewTransaction(ctx, sop.TransactionOptions{
		Mode:          sop.ForWriting,
		MaxTime:       -1,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	})
	if err != nil {
		t.Fatalf("NewTransaction failed: %v", err)
	}

	if err := trans.Begin(ctx); err != nil {
		t.Fatalf("Begin failed: %v", err)
	}

	fired := false
	trans.OnCommit(func(ctx context.Context) error {
		fired = true
		return nil
	})

	if err := trans.Rollback(ctx); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}

	if fired {
		t.Error("OnCommit hook was fired after Rollback, expected it not to")
	}
}
