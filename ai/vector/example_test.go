package vector_test

import (
	"context"
	"fmt"
	"os"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
	core_database "github.com/sharedcode/joltrin/v5/database"
)

// ExampleVectorStore demonstrates how to use the Vector Store for semantic search.
func ExampleVectorStore() {
	// 1. Setup Database
	// In a real app, use a persistent path.
	tmpDir, _ := os.MkdirTemp("", "sop-vector-example-*")
	defer os.RemoveAll(tmpDir)

	db := database.NewDatabase(core_database.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})

	ctx := context.Background()

	// 2. Start Transaction (Write)
	tx, _ := db.BeginTransaction(ctx, sop.ForWriting)

	// 3. Open Vector Store
	// We use map[string]any as the payload type.
	// UsageMode: Dynamic allows the store to adapt to the dataset size.
	store, _ := db.OpenVectorStore(ctx, "demo_vectors", tx, vector.Config{
		UsageMode: ai.Dynamic,
	})

	// 4. Add Items
	// Vectors should be normalized for Cosine Similarity.
	store.Upsert(ctx, ai.Item[map[string]any]{
		ID:      "item1",
		Vector:  []float32{1.0, 0.0, 0.0},
		Payload: map[string]any{"label": "X-Axis"},
	})
	store.Upsert(ctx, ai.Item[map[string]any]{
		ID:      "item2",
		Vector:  []float32{0.0, 1.0, 0.0},
		Payload: map[string]any{"label": "Y-Axis"},
	})

	// 5. Commit Transaction
	tx.Commit(ctx)

	// 6. Search (Read Transaction)
	txRead, _ := db.BeginTransaction(ctx, sop.ForReading)
	storeRead, _ := db.OpenVectorStore(ctx, "demo_vectors", txRead, vector.Config{})

	// Search for vector close to X-Axis
	// Query returns nearest neighbors.
	results, _ := storeRead.Query(ctx, []float32{0.9, 0.1, 0.0}, 1, nil)

	for _, match := range results {
		// Score is Cosine Similarity (1.0 is perfect match)
		// We print a simplified score for deterministic output testing
		if match.Score > 0.9 {
			fmt.Printf("Found: %s\n", match.ID)
		}
	}
	txRead.Commit(ctx)

	// Output:
	// sysStore.Add returned: true, <nil>
	// Found: item1
}
