package common

import (
	"context"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

func Test_GetCommitAndRollbackStoresInfo(t *testing.T) {
	ctx := context.Background()
	_ = ctx

	// Fixed time for deterministic timestamp checks
	origNow := sop.Now
	sop.Now = func() time.Time { return time.Unix(1_700_000_000, 0) }
	defer func() { sop.Now = origNow }()

	// Store and repo setup
	si := sop.StoreInfo{Name: "s1", Count: 100}
	sr := mocks.NewMockStoreRepository()
	_ = sr.Add(ctx, si)

	// Transaction with one backend store
	tx := &Transaction{StoreRepository: sr}
	nr := &nodeRepositoryBackend{count: 90}
	tx.btreesBackend = []btreeBackend{{
		getStoreInfo:   func() *sop.StoreInfo { return &si },
		nodeRepository: nr,
	}}

	// getCommitStoresInfo should compute CountDelta = 100 - 90 = 10 and set Timestamp
	cs, modified := tx.getCommitStoresInfo()
	if !modified || len(cs) != 1 || cs[0].Name != "s1" || cs[0].CountDelta != 10 {
		t.Fatalf("unexpected commit stores info: %+v", cs)
	}
	if cs[0].Timestamp == 0 { // basic sanity; exact value not asserted beyond non-zero
		t.Fatalf("commit timestamp not set")
	}

	// commitStores should merge delta into repository count
	if _, err := tx.commitStores(ctx); err != nil {
		t.Fatalf("commitStores error: %v", err)
	}
	// After update, repo count should be 110
	got, _ := sr.Get(ctx, "s1")
	if len(got) != 1 || got[0].Count != 110 {
		t.Fatalf("store repo not updated, got: %+v", got)
	}

	// getRollbackStoresInfo should compute CountDelta = 90 - 100 = -10
	rb := tx.getRollbackStoresInfo()
	if len(rb) != 1 || rb[0].CountDelta != -10 {
		t.Fatalf("unexpected rollback stores info: %+v", rb)
	}
}
