package memory

import (
	"context"
	"fmt"

	log "log/slog"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
)

// MigrateFromVector imports all Centroids and Vectors from a legacy ai.VectorStore
// (which uses pure math K-Means flat clustering) into the ai/memory MemoryStore
// as flat Categories and Items.
//
// This allows users to do bulk ingestion using the ultra-fast offline math pipeline,
// and then migrate the finished taxonomy into the rich Memory DAG for LLM enrichment.
func MigrateFromVector[T any](ctx context.Context, source ai.VectorStore[T], target MemoryStore[T]) error {
	log.Info("Starting migration from legacy vector store to active memory database")

	// 1. Get the centroids (which become the Categories)
	centroidsBtree, err := source.Centroids(ctx)
	if err != nil {
		return fmt.Errorf("failed to get active centroids from source: %w", err)
	}

	// Map legacy integer Centroid IDs to new UUID Categories
	centroidMap := make(map[int]sop.UUID)

	ok, err := centroidsBtree.First(ctx)
	if err != nil {
		return fmt.Errorf("failed to read first centroid: %w", err)
	}

	if ok {
		for {
			cIDItem := centroidsBtree.GetCurrentKey()
			cID := cIDItem.Key
			centroidVal, err := centroidsBtree.GetCurrentValue(ctx)
			if err != nil {
				return fmt.Errorf("failed to read centroid value: %w", err)
			}

			newCatID := sop.NewUUID()
			centroidMap[cID] = newCatID

			// Create the Category object in target memory store
			cat := &Category{
				ID:           newCatID,
				CenterVector: centroidVal.Vector,
				Name:         fmt.Sprintf("Mathematical Cluster %d", cID),
				Description:  "Migrated from flat K-Means vector storage.",
				ItemCount:    centroidVal.VectorCount,
			}

			_, err = target.AddCategory(ctx, cat)
			if err != nil {
				return fmt.Errorf("failed to add category to target store: %w", err)
			}

			if ok, err = centroidsBtree.Next(ctx); err != nil {
				return fmt.Errorf("failed to read next centroid: %w", err)
			}
			if !ok {
				break
			}
		}
	}

	log.Info("Migrated Centroids to Categories", "category_count", len(centroidMap))

	// 2. Iterate vectors, extract original payload, and Upsert to target
	vectorsBtree, err := source.Vectors(ctx)
	if err != nil {
		return fmt.Errorf("failed to get vectors from source: %w", err)
	}

	ok, err = vectorsBtree.First(ctx)
	if err != nil {
		return fmt.Errorf("failed to read first vector: %w", err)
	}

	if ok {
		count := 0
		for {
			vItem := vectorsBtree.GetCurrentKey()
			vKey := vItem.Key

			// Tombstones are skipped
			if vKey.IsDeleted {
				if ok, err = vectorsBtree.Next(ctx); err != nil {
					return fmt.Errorf("failed to read next vector: %w", err)
				}
				if !ok {
					break
				}
				continue
			}

			vecVal, err := vectorsBtree.GetCurrentValue(ctx)
			if err != nil {
				return fmt.Errorf("failed to read vector value: %w", err)
			}

			// Get the corresponding Payload from source
			sourceItem, err := source.Get(ctx, vKey.ItemID)
			if err != nil {
				return fmt.Errorf("failed to get source item payload for %s: %w", vKey.ItemID, err)
			}

			// Find mapping
			mappedCatID, exists := centroidMap[vKey.CentroidID]
			if !exists {
				// Fallback safety if integrity is slightly off in source
				mappedCatID = sop.NewUUID()
				centroidMap[vKey.CentroidID] = mappedCatID
			}

			itemUUID := sop.NewUUID()

			newItem := Item[T]{
				ID:         itemUUID,
				CategoryID: mappedCatID,
				Data:       sourceItem.Payload,
			}

			// Upsert to target store (using UpsertByCategory to explicitly assign it to the math bucket)
			// UpsertByCategory routes it to the specific Category ID directly, preserving the cluster boundary.
			// However since the target interface expects categoryName as string, we can just use normal Upsert
			// and let the target spatial routing naturally place it, or we bypass.
			// Wait, the MemoryStore interface UpsertByCategory takes `categoryName string`.
			// If we just use standard Upsert, the MemoryStore will dynamically route it based on the vector.
			err = target.Upsert(ctx, newItem, vecVal)
			if err != nil {
				return fmt.Errorf("failed to upsert item to target: %w", err)
			}

			count++

			if ok, err = vectorsBtree.Next(ctx); err != nil {
				return fmt.Errorf("failed to read next vector: %w", err)
			}
			if !ok {
				break
			}
		}

		log.Info("Migrated Vectors to Active Memory Items", "item_count", count)
	}

	log.Info("Migration complete. Data is now ready for LLM semantic enrichment.")
	return nil
}
