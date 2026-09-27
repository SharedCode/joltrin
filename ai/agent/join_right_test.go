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

func TestJoinRightBehavior(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	dbPath := "test_join_right"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}

	// Create DB and Store
	db := database.NewDatabase(dbOpts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Create Stores
	// Users: id, name
	// Orders: id, user_id, item

	userStoreOpts := sop.StoreOptions{Name: "users", SlotLength: 10}
	orderStoreOpts := sop.StoreOptions{Name: "orders", SlotLength: 10}

	sopdb.NewBtree[string, any](ctx, dbOpts, "users", tx, nil, userStoreOpts)
	sopdb.NewBtree[string, any](ctx, dbOpts, "orders", tx, nil, orderStoreOpts)
	tx.Commit(ctx)

	// Populate
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	users, _ := jsondb.OpenStore(ctx, dbOpts, "users", tx)
	orders, _ := jsondb.OpenStore(ctx, dbOpts, "orders", tx)

	users.Add(ctx, "u1", map[string]any{"name": "Alice"})
	users.Add(ctx, "u2", map[string]any{"name": "Bob"})

	orders.Add(ctx, "o1", map[string]any{"user_id": "u1", "item": "Book"})
	orders.Add(ctx, "o2", map[string]any{"user_id": "u1", "item": "Pen"})
	orders.Add(ctx, "o3", map[string]any{"user_id": "u2", "item": "Phone"})

	tx.Commit(ctx)

	// 2. Prepare Agent
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"testdb": dbOpts,
		},
	}

	// 3. Execute Script: Scan Users -> JoinRight Orders
	// Note: JoinRight on "user_id" (value in orders) requires scan if not indexed.
	// But here we are joining Users(key) -> Orders(user_id).
	// Orders key is o1, o2...
	// So we can't use FindOne on Orders unless we join on Orders Key.
	// But let's test the Scan fallback of JoinRightCursor.

	script := []map[string]any{
		{"op": "open_db", "args": map[string]any{"name": "testdb"}},
		{"op": "begin_tx", "args": map[string]any{"database": "testdb", "mode": "read"}, "result_var": "tx"},
		{"op": "open_store", "args": map[string]any{"transaction": "tx", "name": "users", "database": "testdb"}, "result_var": "users"},
		{"op": "open_store", "args": map[string]any{"transaction": "tx", "name": "orders", "database": "testdb"}, "result_var": "orders"},

		{"op": "scan", "args": map[string]any{"store": "users", "stream": true}, "result_var": "stream"},
		{"op": "join_right", "args": map[string]any{"store": "orders", "on": map[string]any{"key": "user_id"}}, "input_var": "stream", "result_var": "stream"},

		{"op": "commit_tx", "args": map[string]any{"transaction": "tx"}},
	}

	scriptJSON, _ := json.Marshal(script)
	res, err := agent.toolExecuteScript(ctx, map[string]any{"script": string(scriptJSON)})
	if err != nil {
		t.Fatalf("ExecuteScript failed: %v", err)
	}

	t.Logf("Result: %s", res)
}
