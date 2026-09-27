package main

import (
	"sync"

	"github.com/sharedcode/joltrin/v5"
)

// transactionItem holds a transaction and its associated B-trees.
type transactionItem struct {
	Transaction sop.Transaction
	Btrees      map[sop.UUID]any
}

// transactionRegistry manages transactions and their associated B-trees.
type transactionRegistry struct {
	lookup map[sop.UUID]*transactionItem
	mu     sync.Mutex
}

// newTransactionRegistry creates a new transactionRegistry.
func newTransactionRegistry() *transactionRegistry {
	return &transactionRegistry{
		lookup: make(map[sop.UUID]*transactionItem),
	}
}

// Add registers a transaction and returns its UUID.
func (tr *transactionRegistry) Add(t sop.Transaction) sop.UUID {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	id := sop.NewUUID()
	tr.lookup[id] = &transactionItem{
		Transaction: t,
		Btrees:      make(map[sop.UUID]any),
	}
	return id
}

// Get retrieves a transaction by UUID.
func (tr *transactionRegistry) Get(id sop.UUID) (sop.Transaction, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	item, ok := tr.lookup[id]
	if !ok {
		return nil, false
	}
	return item.Transaction, true
}

// GetItem retrieves the transaction item (Transaction + Btree Map).
func (tr *transactionRegistry) GetItem(id sop.UUID) (*transactionItem, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	item, ok := tr.lookup[id]
	return item, ok
}

// Remove removes a transaction and all its associated B-trees.
func (tr *transactionRegistry) Remove(id sop.UUID) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	delete(tr.lookup, id)
}

// AddBtree adds a B-tree (or similar object) to a transaction.
func (tr *transactionRegistry) AddBtree(transID sop.UUID, btree any) (sop.UUID, error) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	item, ok := tr.lookup[transID]
	if !ok {
		return sop.NilUUID, nil
	}
	id := sop.NewUUID()
	item.Btrees[id] = btree
	return id, nil
}

// GetBtree retrieves a B-tree from a transaction.
func (tr *transactionRegistry) GetBtree(transID, btreeID sop.UUID) (any, bool) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	item, ok := tr.lookup[transID]
	if !ok {
		return nil, false
	}
	btree, ok := item.Btrees[btreeID]
	return btree, ok
}

// RemoveBtree removes a B-tree from a transaction.
func (tr *transactionRegistry) RemoveBtree(transID, btreeID sop.UUID) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	item, ok := tr.lookup[transID]
	if !ok {
		return
	}
	delete(item.Btrees, btreeID)
}
