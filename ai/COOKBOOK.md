# SOP AI Cookbook

This cookbook provides practical examples and patterns for using the SOP AI library.

## Table of Contents
1.  [Knowledge Base API (Recommended)](#knowledge-base-api-recommended)
    *   [Data Ingestion (ImportJSON/ExportJSON)](#data-ingestion-importjsonexportjson)
2.  [Vector Store API (Low-Level / Legacy)](#vector-store-api-low-level--legacy)
    *   [Basic Setup (Standalone)](#basic-setup-standalone)
    *   [Ingesting Data](#ingesting-data)
    *   [Searching (Query)](#searching-query)
    *   [Filtering Results](#filtering-results)
    *   [Deleting Items](#deleting-items)
3.  [Model Store API](#model-store-api)
    *   [Saving a Model](#saving-a-model)
    *   [Loading a Model](#loading-a-model)
    *   [Listing Models](#listing-models)
    *   [Transactional Model Updates](#transactional-model-updates)

---

## Knowledge Base API (Recommended)

The Knowledge Base is the recommended, higher-level abstraction over the Vector Store that provides human-readable ontologies, deterministic category pre-filtering, and dynamic structuring via an LLM from the `ai/memory` package.

### Data Ingestion (ImportJSON/ExportJSON)

You can easily ingest data into a Knowledge Base from external sources (or export it) using standard JSON payloads. The system handles categorizing and vectorizing the imported items natively.

#### Ideal JSON Payload Structure
The API expects the data to be structured cleanly into `Categories` (optional) and `Items`. The ideal structure maps to the `ExportData` and `ExportItem` Go structs:

```go
// ExportData defines the structure of the KnowledgeBase JSON payload.
type ExportData[T any] struct {
	Categories []*Category     `json:"categories"`
	Items      []ExportItem[T] `json:"items"`
}

// ExportItem dictates what fields from the item are serialized.
type ExportItem[T any] struct {
	Category         string      `json:"category"`
	Data             T           `json:"data"`
	Summaries        []string    `json:"summaries,omitempty"`
	SummariesVectors [][]float32 `json:"summaries_vectors,omitempty"`
}
```

#### Sample JSON Payload

Here is an example payload representing a batch of documents ready for ingestion. This payload can be fed directly to `kb.ImportJSON()`:

```json
{
  "categories": [
    {
      "name": "Engineering",
      "description": "Technical documentation and architecture plans."
    },
    {
      "name": "HR",
      "description": "Human resources and company policies."
    }
  ],
  "items": [
    {
      "category": "Engineering",
      "data": {
        "title": "System Architecture v2",
        "content": "The new architecture replaces monolithic vector stores with compartmentalized B-Tree namespaces."
      },
      "summaries": [
        "Architecture overview",
        "B-Tree namespaces"
      ]
    },
    {
      "category": "HR",
      "data": {
        "title": "Remote Work Policy",
        "content": "Employees are permitted to work remotely up to 3 days a week pending manager approval."
      }
    }
  ]
}
```

#### Ingestion Code Example

```go
func IngestKnowledgeBase(ctx context.Context, kb *memory.KnowledgeBase[map[string]any], jsonPayload io.Reader) error {
	// Import the JSON byte stream into the Knowledge Base
	persona := "Default Ingest Persona"
	if err := kb.ImportJSON(ctx, jsonPayload, persona); err != nil {
		return err
	}
	return nil
}
```

---

## Vector Store API (Low-Level / Legacy)

The Vector Store is the low-level, core component for RAG and semantic search. It lacks the semantic structuring properties provided by the Knowledge Base.

### Basic Setup (Standalone)

Initialize a local, file-backed vector database.

```go
package main

import (
	"fmt"
	"context"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
)

func main() {
	// 1. Create the Database Manager
	// Standalone mode = Local Filesystem + In-Memory Cache
	db := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./my_vector_db"},
	})
	
	// 2. Configure Storage Path
	// db.SetStoragePath("./my_vector_db") // Done in NewDatabase

	// 3. Open a Domain (Index)
	// This creates/opens a specific "table" or "collection" named "documents"
	ctx := context.Background()
	trans, _ := db.BeginTransaction(ctx, sop.ForWriting)
	idx, _ := db.OpenVectorStore(ctx, "documents", trans, vector.Config{})
	
	fmt.Println("Vector Store opened successfully!")
	
	// Don't forget to commit if you made changes (though Open just initializes)
	trans.Commit(ctx)
}
```

### Basic Setup (Clustered)

Initialize a clustered vector database using Redis for caching.

```go
func main() {
	// 1. Create the Database Manager
	// Clustered mode = Local Filesystem (Shared) + Redis Cache
	// Note: Redis is NOT used for data storage, just for coordination & to offer built-in caching.
	// Ensure Redis is running on localhost:6379
	db := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Clustered,
		StoresFolders: []string{"./my_cluster_db"},
	})
	
	// 2. Configure Storage Path
	// db.SetStoragePath("./my_cluster_db") // Done in NewDatabase

	// 3. Open a Domain (Index)
	ctx := context.Background()
	trans, _ := db.BeginTransaction(ctx, sop.ForWriting)
	idx, _ := db.OpenVectorStore(ctx, "documents", trans, vector.Config{})
	
	fmt.Println("Clustered Vector Store opened successfully!")
	trans.Commit(ctx)
}
```

### Ingesting Data

Add items (vectors + metadata) to the store.

```go
func ingestData(ctx context.Context, idx ai.VectorStore[map[string]any]) {
	// Define an item
	item := ai.Item[map[string]any]{
		ID:     "doc-101",
		Vector: []float32{0.1, 0.2, 0.3, 0.4}, // Normalized vector
		Payload: map[string]any{
			"title": "SOP Guide",
			"tags":  []string{"tech", "database"},
		},
	}

	// Upsert (Insert or Update)
	if err := idx.Upsert(ctx, item); err != nil {
		panic(err)
	}

	// Batch Upsert (More efficient for bulk load)
	items := []ai.Item[map[string]any]{
		{ID: "doc-102", Vector: []float32{0.5, 0.5, 0.5, 0.5}, Payload: map[string]any{"title": "AI Agents"}},
		{ID: "doc-103", Vector: []float32{0.9, 0.1, 0.1, 0.9}, Payload: map[string]any{"title": "Vector Search"}},
	}
	if err := idx.UpsertBatch(ctx, items); err != nil {
		panic(err)
	}
}

### Performance Tuning: Deduplication

By default, the Vector Store checks if an item ID already exists before inserting (to prevent duplicates). If you know your data is unique (e.g., during initial bulk load), you can disable this check to improve ingestion speed.

```go
func fastIngest(ctx context.Context, idx ai.VectorStore[map[string]any]) {
    // Disable deduplication check for raw speed
    // WARNING: Only do this if you are certain IDs are unique!
    if store, ok := idx.(interface{ SetDeduplication(bool) }); ok {
        store.SetDeduplication(false)
    }

    // ... perform bulk upsert ...
}
```

### Optimizing the Index

Rebalance the vector clusters for optimal search performance. This is especially important after bulk ingestion or significant updates.

**Note:** The `Optimize` process puts the store into a **Read-Only** mode. Any `Upsert` or `Delete` calls during this time will fail.

```go
func optimizeIndex(ctx context.Context, idx ai.VectorStore[map[string]any]) {
    // Triggers a rebalancing of the centroids.
    // This runs in batches and can handle millions of records.
    if err := idx.Optimize(ctx); err != nil {
        panic(err)
    }
    fmt.Println("Index optimized successfully.")
}

func handleReadOnlyError(err error) {
    // If you try to write during optimization, you'll get an error.
    if err != nil && err.Error() == "Vector Store is currently optimizing (Read-Only mode)" {
        fmt.Println("Store is busy optimizing. Please try again later.")
    }
}
```

### Searching (Query)
```

### Searching (Query)

Find the nearest neighbors to a query vector.

```go
func search(idx ai.VectorStore[map[string]any]) {
	queryVec := []float32{0.1, 0.2, 0.3, 0.4}
	k := 5 // Number of results

	// Perform Search
	hits, err := idx.Query(context.Background(), queryVec, k, nil)
	if err != nil {
		panic(err)
	}

	for _, hit := range hits {
		fmt.Printf("ID: %s, Score: %.4f, Title: %v\n", 
			hit.ID, hit.Score, hit.Payload["title"])
	}
}
```

### Filtering Results

Apply metadata filters to search results.

```go
func searchWithFilter(idx ai.VectorStore[map[string]any]) {
	queryVec := []float32{0.1, 0.2, 0.3, 0.4}

	// Define Filter Function
	filter := func(payload map[string]any) bool {
		// Only return items with "tech" tag
		tags, ok := payload["tags"].([]string)
		if !ok {
			return false
		}
		for _, t := range tags {
			if t == "tech" {
				return true
			}
		}
		return false
	}

	hits, _ := idx.Query(context.Background(), queryVec, 5, filter)
	// ... process hits
}
```

### Deleting Items

Remove items by ID.

```go
func deleteItem(idx ai.VectorStore[map[string]any]) {
	if err := idx.Delete(context.Background(), "doc-101"); err != nil {
		panic(err)
	}
}
```

---

## Model Store API

The Model Store allows you to persist AI model artifacts (weights, configs) alongside your data.

### Saving a Model

Save a struct or any serializable object as a named model.

```go
import (
	"context"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
)

type MyPerceptron struct {
	Weights []float64
	Bias    float64
}

func saveModel() {
	// Initialize Database
	db := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./my_models"},
	})
	ctx := context.Background()
	trans, _ := db.BeginTransaction(ctx, sop.ForWriting)
	store, _ := db.OpenModelStore(ctx, "default", trans)

	model := MyPerceptron{
		Weights: []float64{0.5, -0.2, 1.0},
		Bias:    0.1,
	}

	// Save
	if err := store.Save(ctx, "classifiers", "perceptron_v1", model); err != nil {
		panic(err)
	}
	trans.Commit(ctx)
}
```

### Loading a Model

Retrieve a model by name.

```go
func loadModel() {
	db := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./my_models"},
	})
	ctx := context.Background()
	trans, _ := db.BeginTransaction(ctx, sop.ForReading)
	store, _ := db.OpenModelStore(ctx, "default", trans)

	var loadedModel MyPerceptron
	if err := store.Load(ctx, "classifiers", "perceptron_v1", &loadedModel); err != nil {
		panic(err)
	}
	trans.Commit(ctx)

	fmt.Printf("Loaded Bias: %f\n", loadedModel.Bias)
}
```

### Listing Models

See what's in the store.

```go
func listModels() {
	db := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./my_models"},
	})
	ctx := context.Background()
	trans, _ := db.BeginTransaction(ctx, sop.ForReading)
	store, _ := db.OpenModelStore(ctx, "default", trans)
	names, _ := store.List(ctx, "classifiers")
	trans.Commit(ctx)
	
	for _, name := range names {
		fmt.Println("Found model:", name)
	}
}
```

### Transactional Model Updates

Use `BTreeModelStore` to update models and vectors atomically. This ensures that your model weights always match the vector index version.

```go
import (
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5/database"
	"github.com/sharedcode/joltrin/ai/vector"
)

func atomicUpdate() {
	// 1. Start a Transaction
	// (Assuming we have a transaction factory or manager set up)
	trans, _ := db.BeginTransaction(ctx, sop.ForWriting)

	// 2. Open Transactional Stores
	// Vector Store (bound to transaction)
	vecStore, _ := db.OpenVectorStore(ctx, "documents", trans, vector.Config{})
	
	// Model Store (bound to transaction)
	modelStore, _ := db.OpenModelStore(ctx, "default", trans)

	// 3. Perform Updates
	// Update Vector
	vecStore.Upsert(ctx, ai.Item[any]{ID: "doc-1", Vector: newVec, Payload: "updated"})

	// Update Model
	newWeights := trainOn(newVec)
	modelStore.Save(ctx, "classifiers", "embedding_model", newWeights)

	// 4. Commit (All or Nothing)
	// If this fails, neither the vector nor the model is updated.
	trans.Commit(ctx)
}

### Transaction Hooks (OnCommit)

You can register callbacks to be executed only after a successful commit. This is useful for cleaning up temporary resources or triggering side effects (like notifications) that should only happen if the data is safely persisted.

```go
func transactionWithHook(ctx context.Context, trans sop.Transaction) {
    trans.Begin(ctx)

    // Perform updates...
    // ...

    // Register a hook
    trans.OnCommit(func(ctx context.Context) error {
        fmt.Println("Transaction committed successfully! Sending notification...")
        // sendNotification()
        return nil
    })

    // If Commit fails, the hook is NOT executed.
    if err := trans.Commit(ctx); err != nil {
        fmt.Println("Commit failed, hook skipped.")
    }
}
```
```
