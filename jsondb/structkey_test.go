package jsondb

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

type ProductKey struct {
	Category string  `json:"category"`
	Price    float64 `json:"price"`
	ID       string  `json:"id"`
}

type ProductValue struct {
	Name string `json:"name"`
}

func TestStructKey(t *testing.T) {
	ctx := context.Background()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./data/struct_key_test"},
	}

	// Clean up previous run
	os.RemoveAll("./data/struct_key_test") // In a real test we might want to clean up

	trans, err := database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	idxSpec := NewIndexSpecification([]IndexFieldSpecification{
		{FieldName: "category", AscendingSortOrder: true},
		{FieldName: "price", AscendingSortOrder: false},
	})

	storeOpts := sop.StoreOptions{
		Name:       "products_struct_key",
		SlotLength: 100,
	}

	store, err := NewJsonBtreeStructKey[ProductKey, ProductValue](ctx, dbOpts, storeOpts, trans, idxSpec)
	if err != nil {
		t.Fatalf("NewJsonBtreeStructKey failed: %v", err)
	}

	// Add items
	items := []Item[ProductKey, ProductValue]{
		{
			Key:   ProductKey{Category: "Electronics", Price: 100.0, ID: "1"},
			Value: &ProductValue{Name: "Cheap Gadget"},
		},
		{
			Key:   ProductKey{Category: "Electronics", Price: 500.0, ID: "2"},
			Value: &ProductValue{Name: "Expensive Gadget"},
		},
		{
			Key:   ProductKey{Category: "Books", Price: 20.0, ID: "3"},
			Value: &ProductValue{Name: "Novel"},
		},
	}

	if _, err := store.Add(ctx, items); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	if err := trans.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Read back
	transRead, _ := database.BeginTransaction(ctx, dbOpts, sop.ForReading)
	storeRead, err := OpenJsonBtreeStructKey[ProductKey, ProductValue](ctx, dbOpts, "products_struct_key", transRead)
	if err != nil {
		t.Fatalf("OpenJsonBtreeStructKey failed: %v", err)
	}

	if ok, err := storeRead.First(ctx); !ok || err != nil {
		t.Fatalf("First failed: %v", err)
	}

	// Expected order: Books (20), Electronics (500), Electronics (100)
	// Because Category ASC, Price DESC.

	// 1. Books
	k := storeRead.GetCurrentKey()
	if k.Category != "Books" {
		t.Errorf("Expected first item category 'Books', got '%s'", k.Category)
	}

	if ok, err := storeRead.Next(ctx); !ok || err != nil {
		t.Errorf("Next failed: %v, %v", ok, err)
	}
	// 2. Electronics 500
	k = storeRead.GetCurrentKey()
	if k.Category != "Electronics" || k.Price != 500.0 {
		t.Errorf("Expected second item 'Electronics' 500.0, got '%s' %f", k.Category, k.Price)
	}

	if ok, err := storeRead.Next(ctx); !ok || err != nil {
		t.Errorf("Next failed: %v, %v", ok, err)
	}
	// 3. Electronics 100
	k = storeRead.GetCurrentKey()
	if k.Category != "Electronics" || k.Price != 100.0 {
		t.Errorf("Expected third item 'Electronics' 100.0, got '%s' %f", k.Category, k.Price)
	}

	// Verify Value
	v, err := storeRead.GetCurrentValue(ctx)
	if err != nil {
		t.Fatalf("GetCurrentValue failed: %v", err)
	}
	if v.Name != "Cheap Gadget" {
		t.Errorf("Expected value 'Cheap Gadget', got '%s'", v.Name)
	}

	transRead.Commit(ctx)
	fmt.Println("StructKey test passed!")
}
