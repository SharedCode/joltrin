package database

import (
	"context"
	"encoding/json"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/embed"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

// VectorizeItems processes specific items or an entire category within a single isolated transaction.
// It is intended for real-time web-handlers and granular hooks, avoiding the full Space scan.
func (db *Database) VectorizeItems(
	ctx context.Context,
	kbName string,
	llm ai.Generator,
	embedder ai.Embeddings,
	batchSize int,
	categoryID sop.UUID,
	itemIDs []sop.UUID,
) error {
	if len(itemIDs) == 0 {
		return db.VectorizeCategories(ctx, kbName, llm, embedder, batchSize, []sop.UUID{categoryID})
	}

	embedder = embed.NewResilientEmbedder(embedder)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	kb, err := db.OpenKnowledgeBase(ctx, kbName, tx, llm, embedder, false)
	if err != nil {
		return err
	}

	itemsBtree, err := kb.Store.Items(ctx)
	if err != nil {
		return err
	}

	catBtree, err := kb.Store.Categories(ctx)
	if err != nil {
		return err
	}

	var category *memory.Category
	if ok, _ := catBtree.Find(ctx, categoryID, false); ok {
		cat, _ := catBtree.GetCurrentValue(ctx)
		category = cat
	}
	if category == nil || category.Name == "" {
		return nil
	}

	embedderDim := embedder.Dim()

	embedText := category.Description
	if embedText == "" {
		embedText = category.Name
	}

	expectedCatHash := memory.ComputeVectorHash(embedderDim, embedText)
	if category.VectorHash != expectedCatHash || len(category.CenterVector) == 0 {
		// Category vector changed.
		// We must update the whole category because the center vector affects the spatial distance of ALL items.
		tx.Rollback(ctx)
		return db.VectorizeCategories(ctx, kbName, llm, embedder, batchSize, []sop.UUID{categoryID})
	}

	var itemsToUpdate []*memory.Item[map[string]any]

	for _, id := range itemIDs {
		if ok, _ := itemsBtree.Find(ctx, memory.ItemKey{CategoryID: categoryID, ItemID: id}, false); ok {
			item, err := itemsBtree.GetCurrentValue(ctx)
			if err == nil && item.CategoryID == categoryID {
				dataStr := ""
				if len(item.Summaries) == 0 {
					if str, isStr := any(item.Data).(string); isStr {
						dataStr = str
					} else {
						b, _ := json.Marshal(item.Data)
						dataStr = string(b)
					}
				}
				hashTexts := item.Summaries
				if len(hashTexts) == 0 {
					hashTexts = []string{dataStr}
				}
				expectedHash := memory.ComputeVectorHash(embedderDim, hashTexts...)

				itemCopy := item
				if item.VectorHash != expectedHash || len(item.Positions) == 0 {
					itemCopy.VectorHash = expectedHash
					itemsToUpdate = append(itemsToUpdate, &itemCopy)
				}
			}
		}
	}

	for i := 0; i < len(itemsToUpdate); i += batchSize {
		end := i + batchSize
		if end > len(itemsToUpdate) {
			end = len(itemsToUpdate)
		}

		batch := itemsToUpdate[i:end]
		var batchSummaries []string
		var itemSummaryCounts []int

		for _, item := range batch {
			if len(item.Summaries) == 0 {
				dataStr := ""
				if str, isStr := any(item.Data).(string); isStr {
					dataStr = str
				} else {
					b, _ := json.Marshal(item.Data)
					dataStr = string(b)
				}
				item.Summaries = []string{dataStr}
			}
			batchSummaries = append(batchSummaries, item.Summaries...)
			itemSummaryCounts = append(itemSummaryCounts, len(item.Summaries))
		}

		if len(batchSummaries) == 0 {
			continue
		}

		// Embed as DocumentTexts for item storage (768 dim)
		allVecs, err := embed.DocumentTexts(ctx, embedder, batchSummaries)
		if err != nil {
			return err
		}
		// Embed as CategoryTexts for classification (256 dim)
		allClassificationVecs, err := embed.CategoryTexts(ctx, embedder, batchSummaries)
		if err != nil {
			return err
		}

		vecIdx := 0
		for j, item := range batch {
			count := itemSummaryCounts[j]
			itemVecs := allVecs[vecIdx : vecIdx+count]
			classificationVecs := allClassificationVecs[vecIdx : vecIdx+count]
			vecIdx += count

			err = kb.Store.UpsertByCategoryID(ctx, categoryID, category.CenterVector, *item, itemVecs, classificationVecs)
			if err != nil {
				return err
			}
		}
	}

	return tx.Commit(ctx)
}
