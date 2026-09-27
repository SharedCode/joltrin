package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

// BulkUpsertCategories upserts multiple categories (first-class bulk API)
func (a *CopilotAgent) BulkUpsertCategories(ctx context.Context, args BulkUpsertCategoriesArgs) (*SpaceBulkOperationResult, error) {
	if args.KBName == "" {
		return nil, fmt.Errorf("kb_name required")
	}
	if len(args.Parameters) == 0 {
		return nil, fmt.Errorf("parameters required")
	}

	db, err := a.getDatabase(args.Database)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	result := &SpaceBulkOperationResult{
		Metrics: SpaceBulkOperationMetrics{TotalItems: len(args.Parameters)},
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return nil, err
	}

	// Process all categories at once (KB method supports bulk)
	err = kb.UpsertCategories(ctx, args.Parameters)
	if err != nil {
		result.Failed = len(args.Parameters)
		result.Errors = append(result.Errors, SpaceBulkOperationError{
			Index:   0,
			Message: fmt.Sprintf("bulk upsert failed: %v", err),
		})
		result.Duration = time.Since(start)
		return result, err
	}

	tx.Commit(ctx)
	result.Processed = len(args.Parameters)
	result.Success = true
	result.Duration = time.Since(start)

	if result.Duration > 0 {
		result.Metrics.ItemsPerSecond = float64(result.Processed) / result.Duration.Seconds()
		result.Metrics.AvgItemTime = result.Duration / time.Duration(result.Processed)
	}

	return result, nil
}

// BulkDeleteCategories deletes multiple categories (first-class bulk API)
func (a *CopilotAgent) BulkDeleteCategories(ctx context.Context, args BulkDeleteCategoriesArgs) (*SpaceBulkOperationResult, error) {
	if args.KBName == "" {
		return nil, fmt.Errorf("kb_name required")
	}
	if len(args.CategoryIDs) == 0 {
		return nil, fmt.Errorf("category_ids required")
	}

	db, err := a.getDatabase(args.Database)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	result := &SpaceBulkOperationResult{
		Metrics: SpaceBulkOperationMetrics{TotalItems: len(args.CategoryIDs)},
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return nil, err
	}

	// Process all deletions at once
	err = kb.DeleteCategories(ctx, args.CategoryIDs)
	if err != nil {
		result.Failed = len(args.CategoryIDs)
		result.Errors = append(result.Errors, SpaceBulkOperationError{
			Index:   0,
			Message: fmt.Sprintf("bulk delete failed: %v", err),
		})
		result.Duration = time.Since(start)
		return result, err
	}

	tx.Commit(ctx)
	result.Processed = len(args.CategoryIDs)
	result.Success = true
	result.Duration = time.Since(start)

	if result.Duration > 0 {
		result.Metrics.ItemsPerSecond = float64(result.Processed) / result.Duration.Seconds()
		result.Metrics.AvgItemTime = result.Duration / time.Duration(result.Processed)
	}

	return result, nil
}

// BulkUpsertItems upserts multiple items (first-class bulk API)
func (a *CopilotAgent) BulkUpsertItems(ctx context.Context, args BulkUpsertItemsArgs) (*SpaceBulkOperationResult, error) {
	if args.KBName == "" {
		return nil, fmt.Errorf("kb_name required")
	}
	if len(args.Parameters) == 0 {
		return nil, fmt.Errorf("parameters required")
	}

	db, err := a.getDatabase(args.Database)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	result := &SpaceBulkOperationResult{
		Metrics: SpaceBulkOperationMetrics{TotalItems: len(args.Parameters)},
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return nil, err
	}

	// Process all items at once
	err = kb.UpsertItems(ctx, args.Parameters)
	if err != nil {
		result.Failed = len(args.Parameters)
		result.Errors = append(result.Errors, SpaceBulkOperationError{
			Index:   0,
			Message: fmt.Sprintf("bulk upsert failed: %v", err),
		})
		result.Duration = time.Since(start)
		return result, err
	}

	tx.Commit(ctx)
	result.Processed = len(args.Parameters)
	result.Success = true
	result.Duration = time.Since(start)

	if result.Duration > 0 {
		result.Metrics.ItemsPerSecond = float64(result.Processed) / result.Duration.Seconds()
		result.Metrics.AvgItemTime = result.Duration / time.Duration(result.Processed)
	}

	return result, nil
}

// BulkDeleteItems deletes multiple items (first-class bulk API)
func (a *CopilotAgent) BulkDeleteItems(ctx context.Context, args BulkDeleteItemsArgs) (*SpaceBulkOperationResult, error) {
	if args.KBName == "" {
		return nil, fmt.Errorf("kb_name required")
	}
	if len(args.ItemIDs) == 0 {
		return nil, fmt.Errorf("item_ids required")
	}

	db, err := a.getDatabase(args.Database)
	if err != nil {
		return nil, err
	}

	start := time.Now()
	result := &SpaceBulkOperationResult{
		Metrics: SpaceBulkOperationMetrics{TotalItems: len(args.ItemIDs)},
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return nil, err
	}

	// Process all deletions at once
	itemKeys := make([]memory.ItemKey, len(args.ItemIDs))
	for i, itemID := range args.ItemIDs {
		itemKeys[i] = memory.ItemKey{CategoryID: args.CategoryID, ItemID: itemID}
	}
	err = kb.DeleteItems(ctx, itemKeys)
	if err != nil {
		result.Failed = len(args.ItemIDs)
		result.Errors = append(result.Errors, SpaceBulkOperationError{
			Index:   0,
			Message: fmt.Sprintf("bulk delete failed: %v", err),
		})
		result.Duration = time.Since(start)
		return result, err
	}

	tx.Commit(ctx)
	result.Processed = len(args.ItemIDs)
	result.Success = true
	result.Duration = time.Since(start)

	if result.Duration > 0 {
		result.Metrics.ItemsPerSecond = float64(result.Processed) / result.Duration.Seconds()
		result.Metrics.AvgItemTime = result.Duration / time.Duration(result.Processed)
	}

	return result, nil
}

// BulkVectorizeCategories vectorizes multiple categories (first-class bulk API)
func (a *CopilotAgent) BulkVectorizeCategories(ctx context.Context, args BulkVectorizeCategoriesArgs) (*SpaceBulkOperationResult, error) {
	if args.KBName == "" {
		return nil, fmt.Errorf("kb_name required")
	}
	if len(args.CategoryIDs) == 0 {
		return nil, fmt.Errorf("category_ids required")
	}

	db, err := a.getDatabase(args.Database)
	if err != nil {
		return nil, err
	}

	batchSize := args.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	start := time.Now()
	result := &SpaceBulkOperationResult{
		Metrics: SpaceBulkOperationMetrics{TotalItems: len(args.CategoryIDs)},
	}

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}

	err = db.VectorizeCategories(ctx, args.KBName, a.brain, embedder, batchSize, args.CategoryIDs)
	if err != nil {
		result.Failed = len(args.CategoryIDs)
		result.Errors = append(result.Errors, SpaceBulkOperationError{
			Index:   0,
			Message: fmt.Sprintf("bulk vectorization failed: %v", err),
		})
		result.Duration = time.Since(start)
		return result, err
	}

	result.Processed = len(args.CategoryIDs)
	result.Success = true
	result.Duration = time.Since(start)

	if result.Duration > 0 {
		result.Metrics.ItemsPerSecond = float64(result.Processed) / result.Duration.Seconds()
		result.Metrics.AvgItemTime = result.Duration / time.Duration(result.Processed)
	}

	return result, nil
}

// BulkVectorizeItems vectorizes multiple items (first-class bulk API)
func (a *CopilotAgent) BulkVectorizeItems(ctx context.Context, args BulkVectorizeItemsArgs) (*SpaceBulkOperationResult, error) {
	if args.KBName == "" {
		return nil, fmt.Errorf("kb_name required")
	}

	db, err := a.getDatabase(args.Database)
	if err != nil {
		return nil, err
	}

	batchSize := args.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	start := time.Now()
	totalItems := len(args.ItemIDs)
	if totalItems == 0 {
		totalItems = 1 // For metrics when vectorizing all items in a category
	}
	result := &SpaceBulkOperationResult{
		Metrics: SpaceBulkOperationMetrics{TotalItems: totalItems},
	}

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}

	err = db.VectorizeItems(ctx, args.KBName, a.brain, embedder, batchSize, *args.CategoryID, args.ItemIDs)
	if err != nil {
		result.Failed = totalItems
		result.Errors = append(result.Errors, SpaceBulkOperationError{
			Index:   0,
			Message: fmt.Sprintf("bulk vectorization failed: %v", err),
		})
		result.Duration = time.Since(start)
		return result, err
	}

	result.Processed = totalItems
	result.Success = true
	result.Duration = time.Since(start)

	if result.Duration > 0 {
		result.Metrics.ItemsPerSecond = float64(result.Processed) / result.Duration.Seconds()
		result.Metrics.AvgItemTime = result.Duration / time.Duration(result.Processed)
	}

	return result, nil
}
