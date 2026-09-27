package agent

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
	sopdb "github.com/sharedcode/joltrin/v5/database"
)

func TestReproEmptyColumns(t *testing.T) {
	// 1. Setup DB
	ctx := context.Background()
	dbPath := "test_repro_empty_cols"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}
	db := database.NewDatabase(dbOpts)
	tx, _ := db.BeginTransaction(ctx, sop.ForWriting)

	// 2. Populate Stores
	// "users" -> key: UserID, val: UserObject
	// "users_by_age" -> key: Age (string for simple btree), val: UserID

	users, _ := sopdb.NewBtree[string, any](ctx, dbOpts, "users", tx, nil, sop.StoreOptions{Name: "users", SlotLength: 10, IsPrimitiveKey: true})
	users.Add(ctx, "u1", map[string]any{"name": "Alice", "role": "admin"})
	users.Add(ctx, "u2", map[string]any{"name": "Bob", "role": "user"})

	usersByAge, _ := sopdb.NewBtree[string, any](ctx, dbOpts, "users_by_age", tx, nil, sop.StoreOptions{Name: "users_by_age", SlotLength: 10, IsPrimitiveKey: true})
	usersByAge.Add(ctx, "30", "u1")
	usersByAge.Add(ctx, "25", "u2")

	tx.Commit(ctx)

	// 3. Script Execution
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"dev_db": dbOpts,
		},
	}

	script := []map[string]any{
		{"op": "open_db", "args": map[string]any{"name": "dev_db"}, "result_var": "db"},
		{"op": "begin_tx", "args": map[string]any{"database": "db", "mode": "read"}, "result_var": "tx"},

		{"op": "open_store", "args": map[string]any{"name": "users_by_age", "transaction": "tx"}, "result_var": "users_by_age"},
		{"op": "open_store", "args": map[string]any{"name": "users", "transaction": "tx"}, "result_var": "users"},

		// Scan users_by_age
		{"op": "scan", "args": map[string]any{"store": "users_by_age", "direction": "desc"}, "result_var": "stream"},

		// Join with users
		// Expectation: Left (stream) value == Right (users) key
		{"op": "join_right",
			"args": map[string]any{
				"store": "users",
				"on":    map[string]any{"value": "key"},
			},
			"input_var":  "stream",
			"result_var": "joined_stream",
		},

		// Project
		// Corrected: Use store name prefix instead of "l." or "r."
		{"op": "project",
			"args": map[string]any{
				"fields": []string{"users_by_age.key as age", "users.*"},
			},
			"input_var":  "joined_stream",
			"result_var": "result",
		},

		{"op": "commit_tx", "args": map[string]any{"transaction": "tx"}},
		{"op": "return", "args": map[string]any{"value": "result"}},
	}

	scriptJSON, _ := json.Marshal(script)
	res, err := agent.toolExecuteScript(ctx, map[string]any{"script": string(scriptJSON)})

	if err != nil {
		t.Fatalf("Script failed: %v", err)
	}

	t.Logf("Result: %s", res)

	// We expect "age": "30" etc.
	// If we see empty/nulls, the test reproduces the user issue.
}
