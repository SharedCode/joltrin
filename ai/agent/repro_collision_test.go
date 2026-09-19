package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
)

func TestReproJoinCollision(t *testing.T) {
	// 1. Setup Data
	dbPath := "/tmp/repro_collision"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
	}
	db := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Create Stores
	deptStore, _ := jsondb.NewJsonBtreeMapKey(ctx, db.Options(), sop.StoreOptions{Name: "department"}, tx, "")
	empStore, _ := jsondb.NewJsonBtreeMapKey(ctx, db.Options(), sop.StoreOptions{Name: "employees"}, tx, "")

	// Insert Data
	deptItem := map[string]any{
		"id": "d1", "department": "HR", "region": "APAC", "name": "HR",
	}
	empItem := map[string]any{
		"id": "e1", "name": "John", "department": "HR", "region": "APAC",
	}

	var dVal any = deptItem
	dItem := jsondb.Item[map[string]any, any]{
		Key:   map[string]any{"id": "d1"},
		Value: &dVal,
		ID:    uuid.New(),
	}

	var eVal any = empItem
	eItem := jsondb.Item[map[string]any, any]{
		Key:   map[string]any{"id": "e1"},
		Value: &eVal,
		ID:    uuid.New(),
	}

	deptStore.Add(ctx, []jsondb.Item[map[string]any, any]{dItem})
	empStore.Add(ctx, []jsondb.Item[map[string]any, any]{eItem})

	tx.Commit(ctx)

	// 2. Prepare Script
	script := []map[string]any{
		{"op": "open_db", "args": map[string]any{"name": "default"}, "result_var": "db"},
		{"op": "begin_tx", "args": map[string]any{"database": "db", "mode": "read"}, "result_var": "tx"},
		{"op": "open_store", "args": map[string]any{"name": "department", "transaction": "tx"}, "result_var": "a"},
		{"op": "open_store", "args": map[string]any{"name": "employees", "transaction": "tx"}, "result_var": "b"},
		{"op": "scan", "args": map[string]any{"store": "a", "stream": true}, "result_var": "stream"},
		{"op": "join_right", "args": map[string]any{
			"on":    map[string]any{"department": "department", "region": "region"},
			"store": "b",
			"type":  "inner",
			"alias": "b",
		}, "input_var": "stream", "result_var": "stream"},
		{"op": "project", "args": map[string]any{
			"fields": []any{
				map[string]any{"employee": "b.name"},
			},
		}, "input_var": "stream", "result_var": "output"},
		{"op": "commit_tx", "args": map[string]any{"transaction": "tx"}},
	}

	// 3. Setup Agent
	cfg := agent.Config{StubMode: false}
	dbs := map[string]sop.DatabaseOptions{"default": dbOpts}
	ag := agent.NewCopilotAgent(cfg, dbs, nil)

	// Inject Session Payload
	payload := &ai.SessionPayload{
		CurrentDB: "default",
	}
	ctx = context.WithValue(ctx, agent.SessionPayloadKey, payload)
	ag.Open(ctx)

	// 4. Execute
	args := map[string]any{"script": script}
	b, _ := json.Marshal(args)
	var inputArgs map[string]any
	json.Unmarshal(b, &inputArgs)

	resStr, err := ag.Execute(ctx, "execute_script", inputArgs)
	if err != nil {
		fmt.Printf("Execute failed: %v", err)
		t.Fatalf("Execute failed: %v", err)
	}

	t.Logf("Result: %s", resStr)

	// 5. Verify
	var results []map[string]any
	if err := json.Unmarshal([]byte(resStr), &results); err != nil {
		t.Fatalf("Failed to parse JSON: %v", err)
	}

	if len(results) == 0 {
		t.Fatal("No results found")
	}

	const Expected = "John"

	if val, ok := results[0]["employee"]; ok {
		if val != Expected {
			t.Errorf("Got employee='%v', expected '%v'", val, Expected)
		}
	} else {
		keys := make([]string, 0, len(results[0]))
		for k := range results[0] {
			keys = append(keys, k)
		}
		t.Fatalf("Missing 'employee' key. Found keys: %v. Raw: %s", keys, resStr)
	}
}
