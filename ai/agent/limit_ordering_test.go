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

func TestProjectLimitOrdering(t *testing.T) {
	// 1. Setup Data
	dbPath := "/tmp/project_limit_ordering"
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

	deptStore, _ := jsondb.NewJsonBtreeMapKey(ctx, db.Options(), sop.StoreOptions{Name: "department"}, tx, "")

	// Insert 3 items
	for i := 0; i < 3; i++ {
		item := map[string]any{"id": i, "name": "Dept"}
		var val any = item
		kvItem := jsondb.Item[map[string]any, any]{
			Key:   map[string]any{"id": i},
			Value: &val,
			ID:    uuid.New(),
		}
		deptStore.Add(ctx, []jsondb.Item[map[string]any, any]{kvItem})
	}

	tx.Commit(ctx)

	// Mock Store Setup
	mockStores := make(map[string]*MockStore)
	deptMock := NewMockStore("department")
	// Add data
	for i := 0; i < 3; i++ {
		item := map[string]any{"id": i, "name": "Dept"}
		var val any = item
		// Key must be comparable for MockStore FindOne (simple string check)
		deptMock.Add(ctx, map[string]any{"id": i}, &val)
	}
	mockStores["department"] = deptMock

	// 2. Script: Scan -> Project (Alias) -> Limit
	// Using "as" format which triggered user complaints
	script := []map[string]any{
		{"op": "open_db", "args": map[string]any{"name": "default"}, "result_var": "db"},
		{"op": "begin_tx", "args": map[string]any{"database": "db", "mode": "read"}, "result_var": "tx"},
		// Note: open_store Name must match Mock Key
		{"op": "open_store", "args": map[string]any{"name": "department", "transaction": "tx"}, "result_var": "s"},
		{"op": "scan", "args": map[string]any{"store": "s"}, "result_var": "scan"},

		// Project with Map rule (Target: Source)
		{"op": "project", "args": map[string]any{
			"fields": map[string]any{
				"my_alias": "name",
				"id_alias": "id",
			},
		}, "input_var": "scan", "result_var": "proj"},

		// Limit (Should preserve ordering!)
		{"op": "limit", "args": map[string]any{"limit": 1}, "input_var": "proj", "result_var": "output"},
		{"op": "commit_tx", "args": map[string]any{"transaction": "tx"}},
		{"op": "return", "args": map[string]any{"value": "output"}},
	}

	// 3. Execute
	cfg := agent.Config{StubMode: false}
	dbs := map[string]sop.DatabaseOptions{"default": dbOpts}
	ag := agent.NewCopilotAgent(cfg, dbs, nil)

	// Inject Mock Opener
	ag.StoreOpener = func(ctx context.Context, opts sop.DatabaseOptions, name string, tx sop.Transaction) (jsondb.StoreAccessor, error) {
		if s, ok := mockStores[name]; ok {
			return s, nil
		}
		return nil, fmt.Errorf("mock store not found: %s", name)
	}

	payload := &ai.SessionPayload{CurrentDB: "default"}
	ctx = context.WithValue(ctx, agent.SessionPayloadKey, payload)
	ag.Open(ctx)

	args := map[string]any{"script": script}
	b, _ := json.Marshal(args)
	var inputArgs map[string]any
	json.Unmarshal(b, &inputArgs)

	resStr, err := ag.Execute(ctx, "execute_script", inputArgs)
	if err != nil {
		t.Fatalf("Agent Execute failed: %v", err)
	}

	t.Logf("Result: %s", resStr)

	var resList []map[string]any
	json.Unmarshal([]byte(resStr), &resList)

	if len(resList) == 0 {
		t.Fatal("No results")
	}

	// Check keys are ALIASES
	if _, ok := resList[0]["my_alias"]; !ok {
		t.Error("Expected key 'my_alias' from alias mapping")
	}
	if _, ok := resList[0]["name"]; ok {
		t.Error("Did NOT expect key 'name' (source field)")
	}
}
