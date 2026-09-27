package ai

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

// TransactionPoolEntry tracks an active transaction, the mode it was opened with, and the configuration.
type TransactionPoolEntry struct {
	Transaction     sop.Transaction
	Mode            sop.TransactionMode
	DatabaseOptions sop.DatabaseOptions
	DatabaseName    string
	Exclusive       bool
}

// Begin proxies to the wrapped transaction.
func (e *TransactionPoolEntry) Begin(ctx context.Context) error {
	return e.Transaction.Begin(ctx)
}

// Commit proxies to the wrapped transaction.
func (e *TransactionPoolEntry) Commit(ctx context.Context) error {
	return e.Transaction.Commit(ctx)
}

// Rollback proxies to the wrapped transaction.
func (e *TransactionPoolEntry) Rollback(ctx context.Context) error {
	return e.Transaction.Rollback(ctx)
}

// HasBegun proxies to the wrapped transaction.
func (e *TransactionPoolEntry) HasBegun() bool {
	if e == nil || e.Transaction == nil {
		return false
	}
	return e.Transaction.HasBegun()
}

// GetPhasedTransaction proxies to the wrapped transaction.
func (e *TransactionPoolEntry) GetPhasedTransaction() sop.TwoPhaseCommitTransaction {
	return e.Transaction.GetPhasedTransaction()
}

// AddPhasedTransaction proxies to the wrapped transaction.
func (e *TransactionPoolEntry) AddPhasedTransaction(otherTransaction ...sop.TwoPhaseCommitTransaction) {
	e.Transaction.AddPhasedTransaction(otherTransaction...)
}

// GetStores proxies to the wrapped transaction.
func (e *TransactionPoolEntry) GetStores(ctx context.Context) ([]string, error) {
	return e.Transaction.GetStores(ctx)
}

// Close proxies to the wrapped transaction.
func (e *TransactionPoolEntry) Close() error {
	return e.Transaction.Close()
}

// GetID proxies to the wrapped transaction.
func (e *TransactionPoolEntry) GetID() sop.UUID {
	return e.Transaction.GetID()
}

// CommitMaxDuration proxies to the wrapped transaction.
func (e *TransactionPoolEntry) CommitMaxDuration() time.Duration {
	return e.Transaction.CommitMaxDuration()
}

// OnCommit proxies to the wrapped transaction.
func (e *TransactionPoolEntry) OnCommit(callback func(ctx context.Context) error) {
	e.Transaction.OnCommit(callback)
}

// TransactionPool caches and manages database transactions for the duration of a session or ReAct loop.
// This prevents the overhead of repeatedly calling BeginTransaction/Rollback on the same database.
type TransactionPool struct {
	mu           sync.Mutex
	transactions map[string]*TransactionPoolEntry
}

// NewTransactionPool initializes an empty TransactionPool.
func NewTransactionPool() *TransactionPool {
	return &TransactionPool{
		transactions: make(map[string]*TransactionPoolEntry),
	}
}

// Database interface abstracts the ability to begin a transaction and provide config.
// It is implemented by ai/database.Database.
type Database interface {
	BeginTransaction(ctx context.Context, mode sop.TransactionMode, maxTime ...time.Duration) (sop.Transaction, error)
	Config() sop.DatabaseOptions
}

// GetOrBegin tries to return an existing transaction for dbName.
// If one hasn't been opened yet, it creates one using the provided database and mode.
// If an existing transaction is found but it's Exclusive, or if the new request needs Exclusive access, it cannot be reused.
// Furthermore, if an existing shared transaction is ForReading and the new request needs ForWriting, it returns an error.
func (p *TransactionPool) GetOrBegin(ctx context.Context, dbName string, db Database, mode sop.TransactionMode, exclusive bool) (sop.Transaction, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	entry, exists := p.transactions[dbName]
	if exists {
		// Cannot reuse if the existing is exclusive or the new request demands exclusive access.
		if entry.Exclusive || exclusive {
			return nil, fmt.Errorf("transaction for db '%s' is marked exclusive and cannot be reused, or new request demands exclusive", dbName)
		}

		// Cannot reuse if we need write access, but it's only a read transaction.
		if mode == sop.ForWriting && entry.Mode != sop.ForWriting {
			return nil, fmt.Errorf("transaction for db '%s' is open for reading, cannot upgrade to writing", dbName)
		}
		// Return the proxy entry itself which implements sop.Transaction
		return entry, nil
	}

	// Not found, open a new one.
	tx, err := db.BeginTransaction(ctx, mode)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction for '%s': %w", dbName, err)
	}

	newEntry := &TransactionPoolEntry{
		Transaction:     tx,
		Mode:            mode,
		DatabaseOptions: db.Config(),
		DatabaseName:    dbName,
		Exclusive:       exclusive,
	}
	p.transactions[dbName] = newEntry

	return newEntry, nil
}

// RollbackAll iterates through all active pooled transactions and rolls them back.
// Useful to defer at the start of a ReAct/Copilot loop function.
func (p *TransactionPool) RollbackAll(ctx context.Context) {
	p.mu.Lock()
	defer p.mu.Unlock()

	for dbName, entry := range p.transactions {
		_ = entry.Transaction.Rollback(ctx)
		delete(p.transactions, dbName)
	}
}

// CommitAll iterations through all active pooled transactions and commits them.
// Note: In distributed DB contexts, a partial commit failure might leave things
// half-committed. You must evaluate if a distributed saga is needed for your use case.
func (p *TransactionPool) CommitAll(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	for dbName, entry := range p.transactions {
		if err := entry.Transaction.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit transaction for '%s': %w", dbName, err)
		}
		delete(p.transactions, dbName)
	}
	return nil
}

// Remove cleanly drops a transaction reference from the pool without calling rollback or commit.
// This allows caller to manually commit a specific database's transaction if needed.
func (p *TransactionPool) Remove(dbName string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.transactions, dbName)
}

// Has checks if a transaction is currently opened for the specific dbName
func (p *TransactionPool) Has(dbName string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, exists := p.transactions[dbName]
	return exists
}
