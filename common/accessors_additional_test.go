package common

import (
	"testing"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/common/mocks"
)

// Test_TwoPC_GetStores_And_CommitMaxDuration covers two accessor methods
// that were only ever exercised through their underlying dependency
// (tr.GetStoreRepository().GetAll directly) rather than through the
// Transaction wrapper methods themselves.
func Test_TwoPC_GetStores_And_CommitMaxDuration(t *testing.T) {
	tr, err := NewTwoPhaseCommitTransaction(sop.ForReading, 0, mockNodeBlobStore, mockStoreRepository, mockRegistry, mockRedisCache, mocks.NewMockTransactionLog())
	if err != nil {
		t.Fatalf("ctor error: %v", err)
	}

	if got := tr.CommitMaxDuration(); got <= 0 {
		t.Fatalf("expected a positive default CommitMaxDuration, got %v", got)
	}

	names, err := tr.GetStores(ctx)
	if err != nil {
		t.Fatalf("GetStores error: %v", err)
	}
	// GetStores just forwards to StoreRepository.GetAll; verifying it
	// returns without error and matches the repository directly is enough
	// to prove the delegation, without depending on other tests' seeded state.
	directNames, err := tr.GetStoreRepository().GetAll(ctx)
	if err != nil {
		t.Fatalf("direct GetAll error: %v", err)
	}
	if len(names) != len(directNames) {
		t.Fatalf("GetStores should forward to StoreRepository.GetAll: got %d names, repository has %d", len(names), len(directNames))
	}

	// CommitMaxDuration with an explicit, non-default value.
	tr2, err := NewTwoPhaseCommitTransaction(sop.ForReading, 10*time.Minute, mockNodeBlobStore, mockStoreRepository, mockRegistry, mockRedisCache, mocks.NewMockTransactionLog())
	if err != nil {
		t.Fatalf("ctor error: %v", err)
	}
	if got := tr2.CommitMaxDuration(); got != 10*time.Minute {
		t.Fatalf("expected CommitMaxDuration to reflect the configured 10m, got %v", got)
	}
}
