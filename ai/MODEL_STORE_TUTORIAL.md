# SOP Model Store Tutorial

The **Model Store** is a specialized component of the SOP AI Kit designed to persist AI model artifacts - such as weights, configurations, prompts, and hyperparameters - alongside your data.

Unlike a generic file system, the Model Store is **Transactional**. This means you can update your AI model and your Vector Database in a single, atomic operation. If your training process crashes halfway through, you are guaranteed never to have a "ghost" state where the index is updated but the model weights are not.

<h2>Key Features</h2>
*   **ACID Compliance**: Updates are atomic, consistent, isolated, and durable.
*   **Categorization**: Models are organized by `Category` and `Name`.
*   **JSON Serialization**: Automatically handles marshaling/unmarshaling of any Go struct.
*   **Unified Storage**: Uses the same B-Tree engine as the Vector Store, simplifying deployment.

---

<h2>1. Basic Usage: Saving and Loading</h2>

### Step 1: Define Your Model
You can store any serializable Go struct.

```go
package main

import (
	"context"
	"fmt"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/ai/database"
)

// A simple configuration for an LLM Agent
type AgentConfig struct {
	SystemPrompt string  `json:"system_prompt"`
	Temperature  float64 `json:"temperature"`
	ModelName    string  `json:"model_name"`
}

func main() {
	// 1. Initialize Database
	db := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./data/ai_registry"},
	})
	ctx := context.Background()

	// 2. Start a Transaction
	trans, _ := db.BeginTransaction(ctx, sop.ForWriting)
	defer trans.Rollback(ctx)

	// 3. Open the Model Store
	// "registry" is the name of this specific store instance
	store, _ := db.OpenModelStore(ctx, "registry", trans)

	// 4. Save a Model
	config := AgentConfig{
		SystemPrompt: "You are a helpful coding assistant.",
		Temperature:  0.7,
		ModelName:    "llama-3-8b",
	}

	// Save under Category="agents", Name="coder_v1"
	if err := store.Save(ctx, "agents", "coder_v1", config); err != nil {
		panic(err)
	}

	// 5. Commit
	trans.Commit(ctx)
	fmt.Println("Agent config saved!")
}
```

### Step 2: Load the Model

```go
func load() {
	db := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./data/ai_registry"},
	})
	ctx := context.Background()
	
	// Read-Only Transaction
	trans, _ := db.BeginTransaction(ctx, sop.ForReading)
	defer trans.Rollback(ctx)
	
	store, _ := db.OpenModelStore(ctx, "registry", trans)

	var loadedConfig AgentConfig
	if err := store.Load(ctx, "agents", "coder_v1", &loadedConfig); err != nil {
		panic(err)
	}

	fmt.Printf("Loaded Agent: %s (Temp: %.1f)\n", loadedConfig.ModelName, loadedConfig.Temperature)
}
```

---

<h2>2. Advanced: Storing Model Weights (LoRA Adapters)</h2>

The Model Store is perfect for storing small-to-medium sized model artifacts, such as LoRA (Low-Rank Adaptation) adapters or simple neural network weights.

```go
type LoRAAdapter struct {
	Rank    int         `json:"rank"`
	Alpha   float64     `json:"alpha"`
	Weights [][]float64 `json:"weights"` // 2D Matrix
}

func saveAdapter(ctx context.Context, db *database.Database, name string, weights [][]float64) error {
	trans, _ := db.BeginTransaction(ctx, sop.ForWriting)
	defer trans.Rollback(ctx)

	store, _ := db.OpenModelStore(ctx, "models", trans)

	adapter := LoRAAdapter{
		Rank:    8,
		Alpha:   16.0,
		Weights: weights,
	}

	if err := store.Save(ctx, "adapters", name, adapter); err != nil {
		return err
	}

	return trans.Commit(ctx)
}
```

> **Note**: For massive model files (e.g., 10GB+ `.gguf` files), consider using SOP's `StreamingDataStore` (Blob Store) instead, and use the Model Store to keep the metadata/pointers to those blobs.

---

<h2>3. The "Atomic Training" Pattern</h2>

This is the most powerful pattern enabled by SOP. When you fine-tune a model or update an embedding index, you often need to update two things at once:
1.  The **Vector Index** (new embeddings).
2.  The **Model Registry** (version number, training metrics, or new weights).

With SOP, you can do this atomically.

```go
func UpdateKnowledgeBase(ctx context.Context, db *database.Database, docID string, text string, newVersion int) error {
	// 1. Start ONE Transaction
	trans, _ := db.BeginTransaction(ctx, sop.ForWriting)
	defer trans.Rollback(ctx)

	// 2. Open Both Stores bound to the SAME transaction
    // Open the legacy Vector Store.
	vecStore, _ := db.OpenVectorStore(ctx, "documents", trans, vector.Config{})
	metaStore, _ := db.OpenModelStore(ctx, "metadata", trans)

	// 3. Update Vector Store
	// (Assume we have an embedder)
	vec := embed(text)
	if err := vecStore.Upsert(ctx, ai.Item[any]{ID: docID, Vector: vec}); err != nil {
		return err
	}

	// 4. Update Metadata (Model Store)
	info := map[string]any{
		"last_updated": time.Now(),
		"version":      newVersion,
		"status":       "active",
	}
	if err := metaStore.Save(ctx, "system", "kb_status", info); err != nil {
		return err
	}

	// 5. Commit
	// Either BOTH happen, or NEITHER happens.
	return trans.Commit(ctx)
}
```

<h2>4. Listing and Management</h2>

You can use the `List` and `Delete` methods to manage your model registry.

```go
func listAgents(ctx context.Context, db *database.Database) {
	trans, _ := db.BeginTransaction(ctx, sop.ForReading)
	defer trans.Rollback(ctx)
	
	store, _ := db.OpenModelStore(ctx, "registry", trans)

	// List all models in the "agents" category
	names, _ := store.List(ctx, "agents")
	
	fmt.Println("Available Agents:")
	for _, name := range names {
		fmt.Println("-", name)
	}
}
```
