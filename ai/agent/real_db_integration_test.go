package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	core_database "github.com/sharedcode/joltrin/database"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/stretchr/testify/assert"
)

func TestRealDBIntegration_JoinFlow(t *testing.T) {
	tempDir := t.TempDir()
	// Configure "dev_db"
	ec := sop.ErasureCodingConfig{
		DataShardsCount:             1,
		ParityShardsCount:           1,
		BaseFolderPathsAcrossDrives: []string{tempDir + "/db1", tempDir + "/db2"},
	}

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{tempDir + "/db"},
		Type:          sop.Standalone,
		RedisConfig:   nil,
		ErasureConfig: map[string]sop.ErasureCodingConfig{"": ec},
	}
	databases := map[string]sop.DatabaseOptions{
		"dev_db": dbOpts,
	}

	cfg := Config{}
	agent := NewCopilotAgent(cfg, databases, nil)

	ctx := context.Background()
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{
		Variables: make(map[string]any),
		CurrentDB: "dev_db",
	})
	ctx = context.WithValue(ctx, RunnerSessionKey, &RunnerSession{Verbose: true})

	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	assert.NoError(t, err)
	defer tx.Rollback(ctx)

	users, err := jsondb.CreateObjectStore(ctx, dbOpts, "users", tx)
	assert.NoError(t, err)
	_, err = users.Add(ctx, "u1", map[string]any{"first_name": "John", "key": "u1"})
	assert.NoError(t, err)

	orders, err := jsondb.CreateObjectStore(ctx, dbOpts, "orders", tx)
	assert.NoError(t, err)
	_, err = orders.Add(ctx, "o1", map[string]any{"key": "o1", "total_amount": 123})
	assert.NoError(t, err)

	usersOrders, err := jsondb.CreateObjectStore(ctx, dbOpts, "users_orders", tx)
	assert.NoError(t, err)
	_, err = usersOrders.Add(ctx, "u1", "o1")
	assert.NoError(t, err)

	assert.NoError(t, tx.Commit(ctx))

	// Script
	scriptRaw := `[
		{"op": "open_db", "args": {"name": "dev_db"}},
{"op": "begin_tx", "args": {"database": "dev_db", "mode": "write"}, "result_var": "tx"},

		{"op": "open_store", "args": {"name": "users", "transaction": "tx"}, "result_var": "users"},
		{"op": "open_store", "args": {"name": "users_orders", "transaction": "tx"}, "result_var": "users_orders"},
		{"op": "open_store", "args": {"name": "orders", "transaction": "tx"}, "result_var": "orders"},

		{"op": "scan", "args": {"store": "users", "filter": {"first_name": "John"}, "stream": true}, "result_var": "a"},

		{"op": "join", "input_var": "a", "args": {"store": "users_orders", "alias": "b", "on": {"key": "key"}}, "result_var": "b_joined"},

		{"op": "join", "input_var": "b_joined", "args": {"store": "orders", "alias": "c", "on": {"value": "key"}}, "result_var": "final"},

		{"op": "project", "input_var": "final", "args": {"fields": [{"Dst": "Customer", "Src": "a.first_name"}, {"Dst": "Order", "Src": "c.key"}, {"Dst": "Total", "Src": "c.total_amount"}]}, "result_var": "view"},
		
		{"op": "return", "args": {"value": "view"}}
	]`

	resRaw, err := agent.toolExecuteScript(ctx, map[string]any{"script": scriptRaw})
	if err != nil {
		t.Fatalf("Execution failed: %v", err)
	}
	resStr, err := formatToolResult(ctx, resRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	fmt.Printf("Query Result:\n%s\n", resStr)

	var results []map[string]any
	err = json.Unmarshal([]byte(resStr), &results)
	assert.NoError(t, err)
	assert.NotEmpty(t, results, "expected explicit return to materialize the projected records")
}
