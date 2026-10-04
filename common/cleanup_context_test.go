package common

import (
	"context"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

type cleanupCtxKey struct{}

func Test_CleanupContext_IgnoresCancellationKeepsValuesAndHasDeadline(t *testing.T) {
	parent, cancel := context.WithCancel(context.WithValue(context.Background(), cleanupCtxKey{}, "kept"))
	cancel()

	ctx, release := cleanupContext(parent)
	defer release()

	if err := ctx.Err(); err != nil {
		t.Fatalf("cleanup context must not inherit the caller's cancellation, got %v", err)
	}
	if got := ctx.Value(cleanupCtxKey{}); got != "kept" {
		t.Errorf("cleanup context lost the caller's values, got %v", got)
	}
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("cleanup context must be bounded by a deadline")
	}
	if remaining := time.Until(dl); remaining <= 0 || remaining > cleanupMaxDuration {
		t.Errorf("deadline %v away, want within (0, %v]", remaining, cleanupMaxDuration)
	}
}

// cancelAwareCache behaves like a real network cache: a call made with a
// canceled context fails at once and does nothing.
type cancelAwareCache struct {
	sop.L2Cache
	unlocked bool
}

func (c *cancelAwareCache) Unlock(ctx context.Context, keys []*sop.LockKey) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.unlocked = true
	return c.L2Cache.Unlock(ctx, keys)
}

// A transaction whose caller context was canceled (for example, a sibling
// goroutine failed and its errgroup canceled the rest) must still release its
// node locks. Otherwise they stay held until their TTL, up to the commit
// maxTime, and every other transaction on those nodes is starved.
func Test_Rollback_ReleasesNodeLocksEvenWhenCallerContextIsCanceled(t *testing.T) {
	l2 := &cancelAwareCache{L2Cache: mocks.NewMockClient()}
	tx := &Transaction{
		l2Cache:         l2,
		l1Cache:         cache.GetGlobalL1Cache(l2),
		blobStore:       mocks.NewMockBlobStore(),
		StoreRepository: mocks.NewMockStoreRepository(),
		registry:        mocks.NewMockRegistry(false),
		logger:          newTransactionLogger(mocks.NewMockTransactionLog(), true),
	}
	tx.nodesKeys = []*sop.LockKey{{Key: "L" + sop.NewUUID().String(), LockID: sop.NewUUID(), IsLockOwner: true}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := tx.rollback(ctx, true); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if !l2.unlocked {
		t.Error("node locks were not released when the caller's context was canceled")
	}
	if tx.nodesKeysExist() {
		t.Error("node keys should be cleared after rollback")
	}
}
