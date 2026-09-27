package common

import (
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/cache"
	"github.com/sharedcode/joltrin/v5/common/mocks"
)

// Global mock repositories will allow us to simulate repositories that persists state
// between transaction(commit).
var mockStoreRepository = mocks.NewMockStoreRepository()
var mockRegistry = mocks.NewMockRegistry(false)
var mockRedisCache = mocks.NewMockClient()
var mockNodeBlobStore = mocks.NewMockBlobStore()

// newMockTransaction instantiates a mocked transaction, i.e. - it uses in-memory Repositories as backend, not Cassandra.
func newMockTransaction(t *testing.T, mode sop.TransactionMode, maxTime time.Duration) (sop.Transaction, error) {
	t.Helper()
	// Ensure global L1 cache uses the mock Redis client to avoid real Redis dependency in tests.
	cache.GetGlobalL1Cache(mockRedisCache)
	twoPhase, _ := newMockTwoPhaseCommitTransaction(t, mode, maxTime, false)
	return sop.NewTransaction(mode, twoPhase)
}

// NewMockTransaction with logging turned on.
func newMockTransactionWithLogging(t *testing.T, mode sop.TransactionMode, maxTime time.Duration) (sop.Transaction, error) {
	t.Helper()
	// Ensure global L1 cache uses the mock Redis client to avoid real Redis dependency in tests.
	cache.GetGlobalL1Cache(mockRedisCache)
	twoPhase, _ := newMockTwoPhaseCommitTransaction(t, mode, maxTime, true)
	return sop.NewTransaction(mode, twoPhase)
}

func newMockTwoPhaseCommitTransaction(t *testing.T, mode sop.TransactionMode, maxTime time.Duration, logging bool) (sop.TwoPhaseCommitTransaction, error) {
	t.Helper()
	if maxTime <= 0 {
		m := 15
		maxTime = time.Duration(m * int(time.Minute))
	}
	return &Transaction{
		mode:            mode,
		maxTime:         maxTime,
		StoreRepository: mockStoreRepository,
		registry:        mockRegistry,
		l2Cache:         mockRedisCache,
		// Use the global L1 cache which has been initialized with the mock redis client above.
		l1Cache:   cache.GetGlobalL1Cache(mockRedisCache),
		blobStore: mockNodeBlobStore,
		logger:    newTransactionLogger(mocks.NewMockTransactionLog(), logging),
		phaseDone: -1,
		// Assign a real transaction ID so logs are recorded under a valid TID (non-nil).
		id: sop.NewUUID(),
	}, nil
}
