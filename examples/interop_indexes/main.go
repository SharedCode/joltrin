package main

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
	"github.com/sharedcode/joltrin/v5/encoding"
)

// Product is the value we want to store.
type Product struct {
	ID    string  `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
}

func main() {
	fmt.Println("--- Interoperability Demo, Indexed Fields ---")
	fmt.Println("This example creates a database with a composite index on the Key.")
	fmt.Println("The Key is a map[string]any, allowing dynamic fields.")

	ctx := context.Background()

	// 1. Initialize Database Options
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./data/interop_indexes"},
	}

	// Setup the database folder.
	database.Setup(ctx, dbOpts)

	// 2. Define Index Specification
	// We want to index by "category" (Ascending) and then "price" (Descending).
	// This means products will be grouped by category, and within each category, expensive ones come first.
	idxSpec := jsondb.NewIndexSpecification([]jsondb.IndexFieldSpecification{
		{FieldName: "category", AscendingSortOrder: true},
		{FieldName: "price", AscendingSortOrder: false},
	})

	// Marshal spec to JSON string
	specBytes, _ := encoding.DefaultMarshaler.Marshal(idxSpec)
	specStr := string(specBytes)

	// 3. Start Transaction
	trans, err := database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		panic(err)
	}

	// 4. Create Store with Index Specification
	// Note: We use NewJsonBtreeMapKey for dynamic keys.
	// This mode is highly performant and fully interoperable with other language bindings.
	storeOpts := sop.StoreOptions{
		Name:        "products_by_category_price",
		SlotLength:  100,
		Description: "A store containing products indexed by category and price.",
	}
	store, err := jsondb.NewJsonBtreeMapKey(ctx, dbOpts, storeOpts, trans, specStr)
	if err != nil {
		panic(err)
	}

	// 4b. Create a second store with 3 index fields for testing display
	idxSpec3 := jsondb.NewIndexSpecification([]jsondb.IndexFieldSpecification{
		{FieldName: "category", AscendingSortOrder: true},
		{FieldName: "price", AscendingSortOrder: false},
		{FieldName: "name", AscendingSortOrder: true},
	})
	specBytes3, _ := encoding.DefaultMarshaler.Marshal(idxSpec3)
	storeOpts3 := sop.StoreOptions{
		Name:        "products_complex_sort",
		SlotLength:  100,
		Description: "A store demonstrating complex sorting with three fields.",
	}
	store3, err := jsondb.NewJsonBtreeMapKey(ctx, dbOpts, storeOpts3, trans, string(specBytes3))
	if err != nil {
		panic(err)
	}

	// 5. Add Data
	// We construct keys that match our index specification.
	// Extra fields in the key (like "id") are allowed but ignored by the sorter unless added to spec.

	categories := []string{"Electronics", "Books", "Clothing", "Home", "Garden"}
	var products []struct {
		Category string
		Product  Product
	}

	for i := 0; i < 500; i++ {
		cat := categories[i%len(categories)]
		// Generate a price that varies
		price := float64((i*17)%1000) + 0.99
		id := fmt.Sprintf("prod-%d", i)
		name := fmt.Sprintf("Product %d", i)

		products = append(products, struct {
			Category string
			Product  Product
		}{
			Category: cat,
			Product:  Product{ID: id, Name: name, Price: price},
		})
	}

	items := make([]jsondb.Item[map[string]any, any], len(products))
	for i, p := range products {
		// Key must contain the fields defined in IndexSpecification
		key := map[string]any{
			"category": p.Category,
			"price":    p.Product.Price,
			"id":       p.Product.ID, // Optional, useful for uniqueness if needed
		}
		val := any(p.Product)
		items[i] = jsondb.Item[map[string]any, any]{
			Key:   key,
			Value: &val,
		}
	}

	if _, err := store.Add(ctx, items); err != nil {
		fmt.Printf("Error adding items to store: %v\n", err)
		panic(err)
	}

	// Prepare items for store3
	items3 := make([]jsondb.Item[map[string]any, any], len(products))
	for i, p := range products {
		key := map[string]any{
			"category": p.Category,
			"price":    p.Product.Price,
			"name":     p.Product.Name,
		}
		val := any(p.Product)
		items3[i] = jsondb.Item[map[string]any, any]{
			Key:   key,
			Value: &val,
		}
	}
	if _, err := store3.Add(ctx, items3); err != nil {
		fmt.Printf("Error adding items to store3: %v\n", err)
		panic(err)
	}

	// 4c. Create a store with Blob values
	storeOptsBlob := sop.StoreOptions{
		Name:        "blob_store",
		SlotLength:  100,
		Description: "A store containing binary blob values to test Base64 encoding display.",
	}
	storeBlob, err := jsondb.NewJsonBtreeMapKey(ctx, dbOpts, storeOptsBlob, trans, "")
	if err != nil {
		panic(err)
	}

	itemsBlob := make([]jsondb.Item[map[string]any, any], 10)
	for i := 0; i < 10; i++ {
		key := map[string]any{"id": i}
		// Create a blob (byte slice)
		blob := []byte(fmt.Sprintf("This is a binary blob for item %d. It will be base64 encoded.", i))
		val := any(blob)
		itemsBlob[i] = jsondb.Item[map[string]any, any]{
			Key:   key,
			Value: &val,
		}
	}
	storeBlob.Add(ctx, itemsBlob)

	if err := trans.Commit(ctx); err != nil {
		panic(err)
	}
	fmt.Println("Wrote 500 products to the database.")

	// 6. Read Data Back (Verify Order)
	transRead, _ := database.BeginTransaction(ctx, dbOpts, sop.ForReading)
	storeRead, _ := jsondb.OpenJsonBtreeMapKey(ctx, dbOpts, "products_by_category_price", transRead)

	fmt.Println("\nReading all items (Should be sorted by Category ASC, Price DESC):")

	// Iterate through the store
	storeRead.First(ctx)
	for {
		k := storeRead.BtreeInterface.GetCurrentKey()
		v, _ := storeRead.GetCurrentValue(ctx) // Returns JSON string of the item

		// We can also get the Go value directly
		// val, _ := storeRead.BtreeInterface.GetCurrentValue(ctx)

		fmt.Printf("Key: %-15s | Price: %-8v | JSON: %s\n",
			k.Key["category"],
			k.Key["price"],
			v)

		if ok, _ := storeRead.Next(ctx); !ok {
			break
		}
	}

	transRead.Commit(ctx)
	fmt.Println("\nSuccess! The data is sorted according to the IndexSpecification.")
}
