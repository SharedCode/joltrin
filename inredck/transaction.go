package inredck

import (
	"time"

	cas "github.com/sharedcode/joltrin/adapters/cassandra"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common"
)

// NewTransaction is a convenience function to create an end-user facing transaction object that wraps the two-phase commit transaction.
func NewTransaction(mode sop.TransactionMode, maxTime time.Duration, logging bool) (sop.Transaction, error) {
	// inredck assumes Redis for caching.
	cache := sop.GetL2Cache(sop.TransactionOptions{CacheType: sop.Redis})
	twoPT, err := NewTwoPhaseCommitTransaction(mode, maxTime, cas.NewBlobStore(nil), cas.NewStoreRepository(nil, nil, cache), nil)
	if err != nil {
		return nil, err
	}
	return sop.NewTransaction(mode, twoPT)
}

// NewTwoPhaseCommitTransaction instantiates a transaction for the given mode.
// Pass -1 for maxTime to default to 15 minutes. If logging is on, changes are logged for recovery at the cost of performance.
func NewTwoPhaseCommitTransaction(mode sop.TransactionMode, maxTime time.Duration,
	blobStore sop.BlobStore, storeRepository sop.StoreRepository, customConnection *cas.Connection) (sop.TwoPhaseCommitTransaction, error) {
	l2c := sop.GetL2Cache(sop.TransactionOptions{CacheType: sop.Redis})
	return common.NewTwoPhaseCommitTransaction(mode, maxTime, blobStore, storeRepository, cas.NewRegistry(customConnection, l2c), l2c, cas.NewTransactionLog(customConnection, l2c))
}
