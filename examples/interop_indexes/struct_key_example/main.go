package main

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

// ProductKey is the struct we want to use as the Key.
// We use JSON tags to match the IndexSpecification fields.
type ProductKey struct {
	Category string  `json:"category"`
	Price    float64 `json:"price"`
	ID       string  `json:"id"`
}

// ProductValue is the value we want to store.
type ProductValue struct {
	Name string `json:"name"`
}

func main() {
	fmt.Println("--- Interoperability Demo (Struct Key Wrapper) ---")
	fmt.Println("This example uses a Go struct as the Key, but still supports IndexSpecification.")
	fmt.Println("It generates 500 records to serve as a data source for the SOP Data Browser.")

	ctx := context.Background()

	// 1. Initialize Database Options
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./data/struct_key_demo"},
	}

	// 2. Define Index Specification
	// We want to index by "category" (Ascending) and then "price" (Descending).
	idxSpec := jsondb.NewIndexSpecification([]jsondb.IndexFieldSpecification{
		{FieldName: "category", AscendingSortOrder: true},
		{FieldName: "price", AscendingSortOrder: false},
	})

	// 3. Start Transaction
	trans, err := database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		panic(err)
	}

	// 4. Create Store using NewJsonBtreeStructKey
	storeOpts := sop.StoreOptions{
		Name:        "products_struct",
		SlotLength:  100,
		Description: "A store using struct keys with secondary indexes.",
	}

	// Note: We pass the struct types [ProductKey, ProductValue]
	store, err := jsondb.NewJsonBtreeStructKey[ProductKey, ProductValue](ctx, dbOpts, storeOpts, trans, idxSpec)
	if err != nil {
		panic(err)
	}

	// 5. Add Data
	// We can use Go structs directly!
	fmt.Println("Generating 500 products...")
	categories := []string{"Electronics", "Books", "Clothing", "Home", "Garden"}
	items := make([]jsondb.Item[ProductKey, ProductValue], 500)

	for i := 0; i < 500; i++ {
		cat := categories[i%len(categories)]
		// Generate a price that varies
		price := float64((i*17)%1000) + 0.99
		id := fmt.Sprintf("prod-%d", i)
		name := fmt.Sprintf("Product %d", i)

		items[i] = jsondb.Item[ProductKey, ProductValue]{
			Key:   ProductKey{Category: cat, Price: price, ID: id},
			Value: &ProductValue{Name: name},
		}
	}

	if _, err := store.Add(ctx, items); err != nil {
		panic(err)
	}

	if err := trans.Commit(ctx); err != nil {
		panic(err)
	}
	fmt.Println("Wrote 500 products using struct keys.")

	// 6. Read Data Back
	transRead, _ := database.BeginTransaction(ctx, dbOpts, sop.ForReading)
	storeRead, _ := jsondb.OpenJsonBtreeStructKey[ProductKey, ProductValue](ctx, dbOpts, "products_struct", transRead)

	fmt.Println("\nReading all items (Should be sorted by Category ASC, Price DESC):")

	storeRead.First(ctx)
	for {
		// GetCurrentKey returns ProductKey struct!
		k := storeRead.GetCurrentKey()
		// GetCurrentValue returns ProductValue struct!
		v, _ := storeRead.GetCurrentValue(ctx)

		fmt.Printf("Category: %-12s | Price: %-6.2f | Name: %s\n",
			k.Category, k.Price, v.Name)

		if ok, _ := storeRead.Next(ctx); !ok {
			break
		}
	}

	transRead.Commit(ctx)
}
