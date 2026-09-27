package nn

import (
	"context"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

func TestPerceptronPersistenceWithModelStore(t *testing.T) {
	// Setup temporary directory for model store
	tmpDir := "./test_models_persistence"
	if err := os.MkdirAll(tmpDir, 0755); err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	db := database.NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{tmpDir},
	})
	ctx := context.Background()

	// 1. Create and Train a Perceptron (OR gate)
	p := NewPerceptron(2, 0.1)
	inputs := [][]float64{
		{0, 0},
		{0, 1},
		{1, 0},
		{1, 1},
	}
	targets := []float64{0, 1, 1, 1}

	// Train for a bit
	for epoch := 0; epoch < 2000; epoch++ {
		for i, input := range inputs {
			p.Train(input, targets[i])
		}
	}

	// Verify it learned
	if p.Predict([]float64{0, 1}) != 1.0 {
		t.Fatal("Perceptron failed to learn OR gate before saving")
	}

	// 2. Save it using the ModelStore
	modelName := "or_gate_skill"
	category := "test_category"

	// Transaction 1: Save
	t1, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to begin transaction 1: %v", err)
	}
	store1, err := db.OpenModelStore(ctx, "test_store", t1)
	if err != nil {
		t.Fatalf("Failed to create model store 1: %v", err)
	}

	if err := store1.Save(ctx, category, modelName, p); err != nil {
		t.Fatalf("Failed to save model: %v", err)
	}
	if err := t1.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit transaction 1: %v", err)
	}

	// 3. Load it back into a new instance
	// Transaction 2: Load
	t2, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("Failed to begin transaction 2: %v", err)
	}
	defer t2.Rollback(ctx)

	store2, err := db.OpenModelStore(ctx, "test_store", t2)
	if err != nil {
		t.Fatalf("Failed to create model store 2: %v", err)
	}

	var loadedP Perceptron
	if err := store2.Load(ctx, category, modelName, &loadedP); err != nil {
		t.Fatalf("Failed to load model: %v", err)
	}

	// 4. Verify loaded model works exactly as the original
	// Check internal state
	if loadedP.Bias != p.Bias {
		t.Errorf("Bias mismatch. Original: %v, Loaded: %v", p.Bias, loadedP.Bias)
	}
	if len(loadedP.Weights) != len(p.Weights) {
		t.Fatalf("Weights length mismatch")
	}
	for i, w := range p.Weights {
		if loadedP.Weights[i] != w {
			t.Errorf("Weight %d mismatch. Original: %v, Loaded: %v", i, w, loadedP.Weights[i])
		}
	}

	// Check behavior
	if loadedP.Predict([]float64{0, 1}) != 1.0 {
		t.Errorf("Loaded perceptron failed to predict correctly for [0, 1]")
	}
	if loadedP.Predict([]float64{0, 0}) != 0.0 {
		t.Errorf("Loaded perceptron failed to predict correctly for [0, 0]")
	}

	// 5. Test List
	names, err := store2.List(ctx, category)
	if err != nil {
		t.Fatalf("Failed to list models: %v", err)
	}
	if len(names) != 1 || names[0] != modelName {
		t.Errorf("List failed. Expected [%s], got %v", modelName, names)
	}

	// 6. Test Delete (Need Write Transaction)
	t2.Rollback(ctx) // End Read Trans

	t3, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to begin transaction 3: %v", err)
	}
	store3, err := db.OpenModelStore(ctx, "test_store", t3)
	if err != nil {
		t.Fatalf("Failed to create model store 3: %v", err)
	}

	if err := store3.Delete(ctx, category, modelName); err != nil {
		t.Fatalf("Failed to delete model: %v", err)
	}

	// Verify Delete
	names, _ = store3.List(ctx, category)
	if len(names) != 0 {
		t.Errorf("Delete failed. Expected empty list, got %v", names)
	}
	t3.Commit(ctx)
}
