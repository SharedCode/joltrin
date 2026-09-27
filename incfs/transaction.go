package incfs

import (
	"context"
	"fmt"

	cas "github.com/sharedcode/joltrin/adapters/cassandra"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/fs"
	"github.com/sharedcode/joltrin/v5/inredck"
)

// NewTransaction is a convenience function to create an end-user facing transaction object that wraps the two-phase commit transaction.
func NewTransaction(ctx context.Context, config sop.TransactionOptions) (sop.Transaction, error) {
	fio := fs.NewFileIO()
	var folder string
	if len(config.StoresFolders) > 0 {
		folder = config.StoresFolders[0]
	}
	bs := fs.NewBlobStore(folder, nil, fio)
	mbsf := fs.NewManageStoreFolder(fio)

	var conn *cas.Connection
	var err error
	var needsClose bool
	if config.IsCassandraHybrid() {
		conn, err = cas.GetConnection(config.Keyspace)
		if err != nil {
			return nil, err
		}
		needsClose = true
	}

	if config.CacheType == sop.NoCache {
		config.CacheType = sop.Redis
	}
	cache := sop.GetL2Cache(config)
	twoPT, err := inredck.NewTwoPhaseCommitTransaction(config.Mode, config.MaxTime, bs, cas.NewStoreRepository(mbsf, conn, cache), conn)
	if err != nil {
		return nil, err
	}
	if needsClose {
		// Close the Cassandra connection.
		twoPT.OnCommit(func(context.Context) error {
			conn.Close()
			return nil
		})
	}
	return sop.NewTransaction(config.Mode, twoPT)
}

// NewTransactionWithReplication creates a transaction that supports Erasure Coding file IO.
func NewTransactionWithReplication(ctx context.Context, config sop.TransactionOptions) (sop.Transaction, error) {
	if config.ErasureConfig == nil {
		config.ErasureConfig = fs.GetGlobalErasureConfig()
		if config.ErasureConfig == nil {
			return nil, fmt.Errorf("ErasureConfig can't be nil")
		}
	}
	fio := fs.NewFileIO()
	bs, err := fs.NewBlobStoreWithEC(fs.DefaultToFilePath, fio, config.ErasureConfig)
	if err != nil {
		return nil, err
	}
	mbsf := fs.NewManageStoreFolder(fio)

	var conn *cas.Connection
	var needsClose bool
	if config.IsCassandraHybrid() {
		conn, err = cas.GetConnection(config.Keyspace)
		if err != nil {
			return nil, err
		}
		needsClose = true
	}

	if config.CacheType == sop.NoCache {
		config.CacheType = sop.Redis
	}
	cache := sop.GetL2Cache(config)
	twoPT, err := inredck.NewTwoPhaseCommitTransaction(config.Mode, config.MaxTime, bs, cas.NewStoreRepository(mbsf, conn, cache), conn)
	if err != nil {
		return nil, err
	}
	// Close the Cassandra connection.
	if needsClose {
		twoPT.OnCommit(func(context.Context) error {
			conn.Close()
			return nil
		})
	}
	return sop.NewTransaction(config.Mode, twoPT)
}
