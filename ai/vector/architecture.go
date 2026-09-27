package vector

import (
	"context"
	"fmt"
	"sort"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/database"
)

// Architecture demonstrates the 3-B-Tree layout for optimal performance.
// It uses three separate B-Trees to optimize for different access patterns:
// 1. Directory (Centroids): Fast lookup of centroids for narrowing down the search space.
// 2. Library (Vectors): Fast scanning of vectors within a centroid bucket. Kept compact for cache efficiency.
// 3. Content (Data): Storage of the actual item data (JSON/Document), retrieved only for the final results.
type Architecture struct {
	// Centroids stores the centroid vectors.
	// Key: CentroidID (int) -> Value: Centroid struct
	// Name: "{domain}_centroids"
	Centroids btree.BtreeInterface[int, ai.Centroid]

	// Vectors stores the item vectors, indexed by centroid and distance.
	// Key: VectorKey{CentroidID, Distance, ItemID} -> Value: ItemVector ([]float32)
	// Name: "{domain}_vectors"
	// Optimization: Keeping this small allows more vectors to fit in CPU cache during scanning.
	Vectors btree.BtreeInterface[ai.VectorKey, []float32]

	// Content stores the actual item data.
	// Key: ContentKey (Metadata) -> Value: Document/JSON (string)
	// Name: "{domain}_content"
	Content btree.BtreeInterface[ai.ContentKey, string]

	// Lookup (Int -> ID): Maps integer IDs to string IDs, enabling efficient random sampling.
	// Key: SequenceID (int) -> Value: ItemID (string)
	// Name: "{domain}_lookup"
	Lookup btree.BtreeInterface[int, string]

	// TempVectors stores vectors temporarily during the ingestion phase.
	// Key: ItemID (string) -> Value: Vector ([]float32)
	// Name: "{domain}_temp_vectors"
	TempVectors btree.BtreeInterface[string, []float32]

	// Version tracks the current version of the index (Centroids/Vectors).
	Version int64
}

// newBtree is a helper to create a B-Tree that automatically selects between standard and replicated modes.
func newBtree[TK btree.Ordered, TV any](ctx context.Context, config Config, so sop.StoreOptions, t sop.Transaction, comparer btree.ComparerFunc[TK]) (btree.BtreeInterface[TK, TV], error) {
	return database.NewBtree[TK, TV](ctx, config.TransactionOptions.GetDatabaseOptions(), so.Name, t, comparer, so)
}

// OpenDomainStore initializes the B-Trees for the vertical.
// version is applied ONLY to Centroids and Vectors (the Index).
// Content, TempVectors, and Lookup are shared across versions.
func OpenDomainStore(ctx context.Context, trans sop.Transaction, domain string, version int64, config Config) (*Architecture, error) {
	suffix := ""
	if version > 0 {
		suffix = fmt.Sprintf("_%d", version)
	}

	// Helper to prefix store names with domain
	contentSize := config.ContentSize
	skipTempVectors := !config.EnableIngestionBuffer

	name := func(s string) string {
		return fmt.Sprintf("%s%s", domain, s)
	}

	// 1. Open Centroids Store (Versioned)
	centroids, err := newBtree[int, ai.Centroid](ctx, config, sop.ConfigureStore(name(centroidsSuffix+suffix), true, btree.DefaultSlotLength, centroidsDesc, sop.SmallData, ""), trans, func(a, b int) int { return a - b })
	if err != nil {
		return nil, err
	}

	// 2. Open Vectors Store (Versioned)
	vectors, err := newBtree[ai.VectorKey, []float32](ctx, config, sop.ConfigureStore(name(vectorsSuffix+suffix), true, 10000, vectorsDesc, sop.SmallData, ""), trans, compositeKeyComparer)
	if err != nil {
		return nil, err
	}

	// 3. Open Content Store (Shared)
	contentComparer := func(a, b ai.ContentKey) int {
		if a.ItemID < b.ItemID {
			return -1
		}
		if a.ItemID > b.ItemID {
			return 1
		}
		return 0
	}
	content, err := newBtree[ai.ContentKey, string](ctx, config, sop.ConfigureStore(name(dataSuffix), true, btree.DefaultSlotLength, dataDesc, contentSize, ""), trans, contentComparer)
	if err != nil {
		return nil, err
	}

	// 4. Open Lookup Store (Versioned)
	lookup, err := newBtree[int, string](ctx, config, sop.ConfigureStore(name(lookupSuffix+suffix), true, btree.DefaultSlotLength, lookupDesc, sop.SmallData, ""), trans, func(a, b int) int { return a - b })
	if err != nil {
		return nil, err
	}

	// 5. Open TempVectors Store (Shared)
	// Used as a buffer for Active Memory ingestion across all versions.
	var tempVectors btree.BtreeInterface[string, []float32]
	if !skipTempVectors {
		tempVectors, err = newBtree[string, []float32](ctx, config, sop.ConfigureStore(name(tempVectorsSuffix), true, 10000, tempVectorsDesc, sop.SmallData, ""), trans, func(a, b string) int {
			if a < b {
				return -1
			}
			if a > b {
				return 1
			}
			return 0
		})
		if err != nil {
			return nil, err
		}
	}

	return &Architecture{
		Centroids:   centroids,
		Vectors:     vectors,
		Content:     content,
		Lookup:      lookup,
		TempVectors: tempVectors,
		Version:     version,
	}, nil
}

// Add demonstrates how to write to all 3 stores transactionally.
func (a *Architecture) Add(ctx context.Context, id string, vector []float32, data string) error {
	// Step 1: Assign to a Centroid (Logic omitted for brevity)
	centroidID := 1 // assume we found the best centroid

	// Step 2: Write to Vector Store (The Index)
	// This is lightweight because we only store the vector, not the data.
	vecKey := ai.VectorKey{CentroidID: centroidID, DistanceToCentroid: 0.0, ItemID: id}
	if _, err := a.Vectors.Add(ctx, vecKey, vector); err != nil {
		return err
	}

	// Step 3: Write to Content Store (The Data)
	// We use a default key for the demo.
	key := ai.ContentKey{ItemID: id, CentroidID: centroidID}
	if _, err := a.Content.Add(ctx, key, data); err != nil {
		return err
	}

	return nil
}

// Search demonstrates the optimized retrieval pipeline.
func (a *Architecture) Search(ctx context.Context, query []float32, k int) ([]string, error) {
	// Step 1: Scan Centroids to find the best bucket (e.g., ID 1)
	targetCentroid := 1

	// Step 2: Scan ONLY the Vectors in that bucket
	// We don't load the heavy content here, making this very fast.
	startKey := ai.VectorKey{CentroidID: targetCentroid, DistanceToCentroid: -1.0, ItemID: ""}

	type candidate struct {
		ID    string
		Score float32
	}
	var candidates []candidate

	// Find positions the cursor. If exact match not found, it positions at nearest.
	if _, err := a.Vectors.Find(ctx, startKey, true); err != nil {
		return nil, err
	}

	for {
		item, err := a.Vectors.GetCurrentItem(ctx)
		if err != nil {
			return nil, err
		}
		if item.ID.IsNil() {
			break
		}

		key := item.Key
		// If we are before the startKey (e.g. previous centroid), move next
		if compositeKeyComparer(key, startKey) < 0 {
			if ok, err := a.Vectors.Next(ctx); !ok || err != nil {
				break
			}
			continue
		}

		if key.CentroidID != targetCentroid {
			break // Left the bucket
		}

		// Compute Score
		itemVec := *item.Value
		score := cosine(query, itemVec)
		candidates = append(candidates, candidate{ID: key.ItemID, Score: score})

		if ok, _ := a.Vectors.Next(ctx); !ok {
			break
		}
	}

	// Sort by Score
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Score > candidates[j].Score })

	// Top K
	if len(candidates) > k {
		candidates = candidates[:k]
	}

	// Step 3: Fetch Content ONLY for the winners
	var results []string
	for _, c := range candidates {
		searchKey := ai.ContentKey{ItemID: c.ID}
		found, err := a.Content.Find(ctx, searchKey, false)
		if err != nil {
			return nil, err
		}
		if found {
			data, _ := a.Content.GetCurrentValue(ctx)
			results = append(results, data)
		}
	}

	return results, nil
}
