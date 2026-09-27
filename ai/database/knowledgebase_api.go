package database

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

// NewKnowledgeBase opens a KnowledgeBase using the filesystem-backed default path.
//
// It is the simple, reusable entry point for the common case: one constructor
// call and then standard CRUD/search methods. Advanced callers can still use
// OpenKnowledgeBase for explicit control over transactions, managers, and low-level stores.
func NewKnowledgeBase(
	ctx context.Context,
	name string,
	config sop.DatabaseOptions,
	llm ai.Generator,
	embedder ai.Embeddings,
	documentMode bool,
	enableTextSearch ...bool,
) (*memory.KnowledgeBase[map[string]any], error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if name == "" {
		return nil, fmt.Errorf("knowledge base name is required")
	}
	if len(config.StoresFolders) == 0 {
		config.StoresFolders = []string{"./data"}
	}

	db := NewDatabase(config)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return nil, err
	}
	kb, err := db.OpenKnowledgeBase(ctx, name, tx, llm, embedder, documentMode, enableTextSearch...)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	kb.SetTransaction(tx)
	return kb, nil
}
