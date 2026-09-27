package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
	"github.com/sharedcode/joltrin/v5"
	sopdb "github.com/sharedcode/joltrin/v5/database"
)

func TestManyJoinsBehavior(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	dbPath := "test_many_joins"
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

	// Create 6 Stores: t1 -> t2 -> t3 -> t4 -> t5 -> t6
	// Each store has a 'next_id' pointing to the key of the next store.
	// We will join t1.next_id = t2.key, t2.next_id = t3.key, etc.

	for i := 1; i <= 6; i++ {
		name := fmt.Sprintf("t%d", i)
		sopdb.NewBtree[string, any](ctx, dbOpts, name, tx, nil, sop.StoreOptions{Name: name, SlotLength: 10, IsPrimitiveKey: true})
	}
	tx.Commit(ctx)

	// Populate
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)

	// t1: key="1", val={next_id: "2", data1: "v1"}
	t1, _ := jsondb.OpenStore(ctx, dbOpts, "t1", tx)
	t1.Add(ctx, "1", map[string]any{"next_id": "2", "data1": "v1"})

	// t2: key="2", val={next_id: "3", data2: "v2"}
	t2, _ := jsondb.OpenStore(ctx, dbOpts, "t2", tx)
	t2.Add(ctx, "2", map[string]any{"next_id": "3", "data2": "v2"})

	// t3: key="3", val={next_id: "4", data3: "v3"}
	t3, _ := jsondb.OpenStore(ctx, dbOpts, "t3", tx)
	t3.Add(ctx, "3", map[string]any{"next_id": "4", "data3": "v3"})

	// t4: key="4", val={next_id: "5", data4: "v4"}
	t4, _ := jsondb.OpenStore(ctx, dbOpts, "t4", tx)
	t4.Add(ctx, "4", map[string]any{"next_id": "5", "data4": "v4"})

	// t5: key="5", val={next_id: "6", data5: "v5"}
	t5, _ := jsondb.OpenStore(ctx, dbOpts, "t5", tx)
	t5.Add(ctx, "5", map[string]any{"next_id": "6", "data5": "v5"})

	// t6: key="6", val={data6: "v6"} (End of chain)
	t6, _ := jsondb.OpenStore(ctx, dbOpts, "t6", tx)
	t6.Add(ctx, "6", map[string]any{"data6": "v6"})

	tx.Commit(ctx)

	// 2. Prepare Agent
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"testdb": dbOpts,
		},
	}

	// 3. Execute Pipeline Script
	// Scan(t1) -> Join(t2) -> Join(t3) -> Join(t4) -> Join(t5) -> Join(t6)

	script := []map[string]any{
		{"op": "open_db", "args": map[string]any{"name": "testdb"}},
		{"op": "begin_tx", "args": map[string]any{"database": "testdb", "mode": "read"}, "result_var": "tx"},

		{"op": "open_store", "args": map[string]any{"transaction": "tx", "name": "t1", "database": "testdb"}, "result_var": "t1"},
		{"op": "open_store", "args": map[string]any{"transaction": "tx", "name": "t2", "database": "testdb"}, "result_var": "t2"},
		{"op": "open_store", "args": map[string]any{"transaction": "tx", "name": "t3", "database": "testdb"}, "result_var": "t3"},
		{"op": "open_store", "args": map[string]any{"transaction": "tx", "name": "t4", "database": "testdb"}, "result_var": "t4"},
		{"op": "open_store", "args": map[string]any{"transaction": "tx", "name": "t5", "database": "testdb"}, "result_var": "t5"},
		{"op": "open_store", "args": map[string]any{"transaction": "tx", "name": "t6", "database": "testdb"}, "result_var": "t6"},

		// Pipeline
		{"op": "scan", "args": map[string]any{"store": "t1", "stream": true}, "result_var": "s1"},

		{"op": "join_right", "args": map[string]any{"store": "t2", "on": map[string]any{"next_id": "key"}}, "input_var": "s1", "result_var": "s2"},
		{"op": "join_right", "args": map[string]any{"store": "t3", "on": map[string]any{"t2.next_id": "key"}}, "input_var": "s2", "result_var": "s3"},
		{"op": "join_right", "args": map[string]any{"store": "t4", "on": map[string]any{"t3.next_id": "key"}}, "input_var": "s3", "result_var": "s4"},
		{"op": "join_right", "args": map[string]any{"store": "t5", "on": map[string]any{"t4.next_id": "key"}}, "input_var": "s4", "result_var": "s5"},
		{"op": "join_right", "args": map[string]any{"store": "t6", "on": map[string]any{"t5.next_id": "key"}}, "input_var": "s5", "result_var": "s6"},

		{"op": "limit", "args": map[string]any{"limit": 10}, "input_var": "s6", "result_var": "final"},

		{"op": "commit_tx", "args": map[string]any{"transaction": "tx"}},
	}

	scriptJSON, _ := json.Marshal(script)
	res, err := agent.toolExecuteScript(ctx, map[string]any{"script": string(scriptJSON)})
	if err != nil {
		t.Fatalf("ExecuteScript failed: %v", err)
	}

	t.Logf("Result: %s", res)
}
