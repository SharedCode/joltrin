package main

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

// Person is a struct we want to store.
// Note: In jsondb, this will be serialized to JSON automatically.
type Person struct {
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Age       int    `json:"age"`
}

func main() {
	fmt.Println("--- Interoperability Demo (Go jsondb) ---")
	fmt.Println("This example creates a database that is fully compatible with Python/C# bindings.")

	ctx := context.Background()

	// 1. Initialize Database Options
	// We use Standalone for simplicity (local file), but Clustered (Redis) works the same way.
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./data/interop_demo"},
	}

	// 2. Start a Transaction
	// database.BeginTransaction is a wrapper that ensures the correct transaction type is created.
	trans, err := database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		panic(err)
	}

	// 3. Create/Open a Store
	// We use NewJsonBtree which wraps the standard B-Tree.
	// We specify [string, Person] generics so Go knows how to work with it,
	// but the underlying storage is compatible with the universal format.
	// Performance Note: This has negligible overhead compared to native Go structs
	// because SOP serializes entire nodes at once.
	storeOpts := sop.StoreOptions{
		Name:       "people",
		SlotLength: 100,
	}
	store, err := jsondb.NewJsonBtree[string, Person](ctx, dbOpts, storeOpts, trans, nil)
	if err != nil {
		panic(err)
	}

	// 4. Add Data
	// We can pass our struct directly. jsondb handles the JSON marshaling.
	p1 := Person{FirstName: "John", LastName: "Doe", Age: 30}
	p2 := Person{FirstName: "Jane", LastName: "Smith", Age: 25}

	// Add items. The keys are strings.
	// Note: jsondb.Add takes a slice of Items
	items := []jsondb.Item[string, Person]{
		{Key: "user:1", Value: &p1},
		{Key: "user:2", Value: &p2},
	}
	store.Add(ctx, items)

	// 5. Commit
	if err := trans.Commit(ctx); err != nil {
		panic(err)
	}
	fmt.Println("Wrote 2 people to the database.")

	// 6. Read Data Back
	transRead, _ := database.BeginTransaction(ctx, dbOpts, sop.ForReading)
	storeRead, _ := jsondb.OpenJsonBtree[string, Person](ctx, dbOpts, "people", transRead, nil)

	// We can read it back into our struct
	// FindOne is not directly on JsonDBAnyKey, we use Find + GetCurrentValue
	found, err := storeRead.Find(ctx, "user:1", false)
	if err != nil {
		panic(err)
	}
	if found {
		// GetCurrentValue in jsondb returns a JSON string representing the item (Key, Value, ID).
		// This is useful for interoperability or API responses.
		jsonStr, _ := storeRead.GetCurrentValue(ctx)
		fmt.Printf("Found User 1 (JSON): %s\n", jsonStr)

		// If you want the Go struct directly, you can access the underlying B-Tree:
		person, _ := storeRead.BtreeInterface.GetCurrentValue(ctx)
		fmt.Printf("Found User 1 (Struct): %+v\n", person)
	}

	transRead.Commit(ctx)

	fmt.Println("\nSuccess! You can now open './data/interop_demo' using Python/C# bindings and read this data.")
}
