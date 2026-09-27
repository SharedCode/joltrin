package agent

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
	sopdb "github.com/sharedcode/joltrin/v5/database"
)

func TestMultiJoinBehavior(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	dbPath := "test_multi_join"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}

	// Create DB
	db := database.NewDatabase(dbOpts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Create Stores
	// Users: key=u1, value={name: Alice}
	// Orders: key=o1, value={user_id: u1, product_id: p1}
	// Products: key=p1, value={name: Book, price: 10}

	sopdb.NewBtree[string, any](ctx, dbOpts, "users", tx, nil, sop.StoreOptions{Name: "users", SlotLength: 10, IsPrimitiveKey: true})
	sopdb.NewBtree[string, any](ctx, dbOpts, "orders", tx, nil, sop.StoreOptions{Name: "orders", SlotLength: 10, IsPrimitiveKey: true})
	sopdb.NewBtree[string, any](ctx, dbOpts, "products", tx, nil, sop.StoreOptions{Name: "products", SlotLength: 10, IsPrimitiveKey: true})
	tx.Commit(ctx)

	// Populate
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	users, _ := jsondb.OpenStore(ctx, dbOpts, "users", tx)
	orders, _ := jsondb.OpenStore(ctx, dbOpts, "orders", tx)
	products, _ := jsondb.OpenStore(ctx, dbOpts, "products", tx)

	users.Add(ctx, "u1", map[string]any{"name": "Alice"})

	orders.Add(ctx, "o1", map[string]any{"user_id": "u1", "product_id": "p1"})
	orders.Add(ctx, "o2", map[string]any{"user_id": "u1", "product_id": "p2"})

	products.Add(ctx, "p1", map[string]any{"name": "Book", "price": 10})
	products.Add(ctx, "p2", map[string]any{"name": "Pen", "price": 5})

	tx.Commit(ctx)

	// 2. Prepare Agent
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"testdb": dbOpts,
		},
	}

	// 3. Execute Pipeline Script
	// Scan(Users) -> JoinRight(Orders) -> JoinRight(Products)

	script := []map[string]any{
		{"op": "open_db", "args": map[string]any{"name": "testdb"}},
		{"op": "begin_tx", "args": map[string]any{"database": "testdb", "mode": "read"}, "result_var": "tx"},
		{"op": "open_store", "args": map[string]any{"transaction": "tx", "name": "users", "database": "testdb"}, "result_var": "users"},
		{"op": "open_store", "args": map[string]any{"transaction": "tx", "name": "orders", "database": "testdb"}, "result_var": "orders"},
		{"op": "open_store", "args": map[string]any{"transaction": "tx", "name": "products", "database": "testdb"}, "result_var": "products"},

		// Step 1: Scan Users
		{"op": "scan", "args": map[string]any{"store": "users", "stream": true}, "result_var": "s1"},

		// Step 2: Join Orders (on users.key = orders.user_id)
		// Note: orders.user_id is in value, so this will be a Scan Join (Nested Loop)
		{"op": "join_right", "args": map[string]any{"store": "orders", "on": map[string]any{"key": "user_id"}}, "input_var": "s1", "result_var": "s2"},

		// Step 3: Join Products (on orders.product_id = products.key)
		// Note: products.key is the Key, so this will be a Lookup Join (Probe)
		// The input 's2' has merged fields. 'product_id' comes from orders value.
		{"op": "join_right", "args": map[string]any{"store": "products", "on": map[string]any{"orders.product_id": "key"}}, "input_var": "s2", "result_var": "s3"},

		// Step 4: Collect
		{"op": "limit", "args": map[string]any{"limit": 10}, "input_var": "s3", "result_var": "final"},

		{"op": "commit_tx", "args": map[string]any{"transaction": "tx"}},
	}

	scriptJSON, _ := json.Marshal(script)
	res, err := agent.toolExecuteScript(ctx, map[string]any{"script": string(scriptJSON)})
	if err != nil {
		t.Fatalf("ExecuteScript failed: %v", err)
	}

	t.Logf("Result: %s", res)
}
