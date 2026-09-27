package model_test

import (
	"context"
	"fmt"
	"os"

	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

// MyModel is a sample struct to demonstrate storage.
type MyModel struct {
	Name       string         `json:"name"`
	Version    string         `json:"version"`
	Format     string         `json:"format"`
	Parameters map[string]int `json:"parameters"`
}

// Example demonstrates how to use the Model Store to version and retrieve AI models.
func Example() {
	// 1. Setup Database
	tmpDir, _ := os.MkdirTemp("", "sop-model-example-*")
	defer os.RemoveAll(tmpDir)

	db := database.NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})

	ctx := context.Background()

	// 2. Start Transaction (Write)
	tx, _ := db.BeginTransaction(ctx, sop.ForWriting)

	// 3. Open Model Store
	store, _ := db.OpenModelStore(ctx, "demo_models", tx)

	// 4. Save a Model
	m := MyModel{
		Name:    "gpt-4-mini",
		Version: "1.0.0",
		Format:  "gguf",
		Parameters: map[string]int{
			"layers": 12,
			"heads":  8,
		},
	}
	// Save under category "llm" with name "gpt-4-mini-v1"
	store.Save(ctx, "llm", "gpt-4-mini-v1", m)

	// 5. Commit
	tx.Commit(ctx)

	// 6. Load Model (Read Transaction)
	txRead, _ := db.BeginTransaction(ctx, sop.ForReading)
	storeRead, _ := db.OpenModelStore(ctx, "demo_models", txRead)

	var loaded MyModel
	storeRead.Load(ctx, "llm", "gpt-4-mini-v1", &loaded)

	fmt.Printf("Loaded: %s v%s (Format: %s)\n", loaded.Name, loaded.Version, loaded.Format)

	txRead.Commit(ctx)

	// Output:
	// Loaded: gpt-4-mini v1.0.0 (Format: gguf)
}
