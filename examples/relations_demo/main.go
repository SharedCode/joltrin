package main

import (
	"context"
	"fmt"
	"os"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

// User represents a user in the system.
type User struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Order represents an order linked to a user.
type Order struct {
	ID     string  `json:"id"`
	UserID string  `json:"user_id"`
	Amount float64 `json:"amount"`
}

func main() {
	fmt.Println("--- Relational Intelligence Demo (Go) ---")
	ctx := context.Background()

	// 1. Setup Database
	dbPath := "data/relations_demo_go"
	os.RemoveAll(dbPath) // Clean start
	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		Type:          sop.Standalone,
	}

	// 2. Begin Transaction
	trans, err := database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		panic(err)
	}

	// 3. Create 'Users' Store (Target)
	fmt.Println("Creating 'users' store...")
	users, err := database.NewBtree[string, User](ctx, dbOpts, "users", trans, nil)
	if err != nil {
		panic(err)
	}
	users.Add(ctx, "user_1", User{ID: "user_1", Name: "Alice"})

	// 4. Create 'Orders' Store with Relation Metadata (Source)
	fmt.Println("Creating 'orders' store with Relation metadata...")

	// Define relation: orders.user_id -> users.id
	rel := sop.Relation{
		SourceFields: []string{"user_id"},
		TargetStore:  "users",
		TargetFields: []string{"id"},
	}

	orderOpts := sop.StoreOptions{
		Relations: []sop.Relation{rel},
	}

	orders, err := database.NewBtree[string, Order](ctx, dbOpts, "orders", trans, nil, orderOpts)
	if err != nil {
		panic(err)
	}

	// Add data (Value is now a strongly-typed Struct)
	orders.Add(ctx, "order_A", Order{ID: "order_A", UserID: "user_1", Amount: 100})

	// 5. Create 'Log' Store with Composite Struct Key & IndexSpecification
	// This showcases "Data Manager Compliance":
	// 1. Using a Struct Key allows composite keys (e.g. Timestamp + ServiceName).
	// 2. Setting MapKeyIndexSpecification tells SOP (and Data Manager) exactly how to index/sort these fields.
	//    This enables the Data Manager UI to display, sort, and filter this store correctly without any extra configuration.
	fmt.Println("Creating 'app_logs' store with Struct Key and IndexSpecification...")

	type LogKey struct {
		Timestamp int64  `json:"ts"`
		Service   string `json:"svc"`
	}

	logOpts := sop.StoreOptions{
		// MapKeyIndexSpecification is the ONLY piece needed to be 100% Data Manager compliant
		// whilst staying idiomatic Go (Generics + Structs).
		MapKeyIndexSpecification: `{
			"index_fields": [
				{"field_name": "ts", "ascending_sort_order": false},  // Descending time
				{"field_name": "svc", "ascending_sort_order": true}
			]
		}`,
	}

	logs, err := database.NewBtree[LogKey, string](ctx, dbOpts, "app_logs", trans, nil, logOpts)
	if err != nil {
		panic(err)
	}

	// Add log entry
	logs.Add(ctx, LogKey{Timestamp: 1700000000, Service: "auth"}, "User login attempt")

	// 6. Commit
	if err := trans.Commit(ctx); err != nil {
		panic(err)
	}

	fmt.Println("Successfully created stores with Relations and IndexSpecs!")
	fmt.Printf("Database created at: %s\n", dbPath)
}
