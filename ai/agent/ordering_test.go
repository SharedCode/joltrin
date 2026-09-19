package agent_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
)

func TestExecuteScriptOrdering(t *testing.T) {
	// 1. Setup Test Database
	dbPath := "/tmp/test_ordering_sop"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
	}
	db := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, _ := db.BeginTransaction(ctx, sop.ForWriting)

	// Create Store with Index Spec: region THEN department
	specJSON := `{"index_fields": [{"field_name": "region", "ascending_sort_order": true}, {"field_name": "department", "ascending_sort_order": true}]}`

	store, err := jsondb.NewJsonBtreeMapKey(ctx, db.Options(), sop.StoreOptions{Name: "employees"}, tx, specJSON)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Add item
	val := map[string]any{"id": "e1"}
	key := map[string]any{"department": "Sales", "region": "US-West"} // Alphabetical: d, r. Spec: r, d.

	var valAny any = val
	item := jsondb.Item[map[string]any, any]{
		Key:   key,
		Value: &valAny,
		ID:    uuid.New(),
	}
	store.Add(ctx, []jsondb.Item[map[string]any, any]{item})
	tx.Commit(ctx)

	// 2. Execute Script
	cfg := agent.Config{Verbose: true}
	databases := make(map[string]sop.DatabaseOptions)
	databases["mydb"] = dbOpts
	sysDB := database.NewDatabase(sop.DatabaseOptions{StoresFolders: []string{"/tmp/sysdb_ord"}})
	adminAgent := agent.NewCopilotAgent(cfg, databases, sysDB)
	execCtx := context.WithValue(ctx, agent.SessionPayloadKey, &ai.SessionPayload{CurrentDB: "mydb"})
	if err := adminAgent.Open(execCtx); err != nil {
		t.Fatalf("Failed to open agent: %v", err)
	}

	scriptSteps := []any{
		map[string]any{"op": "open_db", "args": map[string]any{"name": "mydb"}, "result_var": "db"},
		map[string]any{"op": "begin_tx", "args": map[string]any{"database": "db", "mode": "read"}, "result_var": "tx"},
		map[string]any{"op": "open_store", "args": map[string]any{"name": "employees", "transaction": "tx"}, "result_var": "employees"},
		map[string]any{"op": "scan", "args": map[string]any{"store": "employees", "stream": true}, "result_var": "stream"},
		// Limit wraps StoreCursor
		map[string]any{"op": "limit", "args": map[string]any{"limit": 10}, "input_var": "stream", "result_var": "output"},
		map[string]any{"op": "commit_tx", "args": map[string]any{"transaction": "tx"}},
	}

	res, err := adminAgent.Execute(execCtx, "execute_script", map[string]any{"script": scriptSteps})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	// 3. Verify Order in JSON String
	resStr := fmt.Sprintf("%v", res)
	// We expect "region": ... before "department": ...
	// Key structure in JSON: "key": { "region": "US-West", "department": "Sales" }

	// Locate the key object
	// Note: Output is now flat due to renderItem logic
	keyIdx := strings.Index(resStr, "\"key\": {")

	// If key wrapper found, use it as offset
	offset := 0
	if keyIdx != -1 {
		offset = keyIdx
	}

	regionIdx := strings.Index(resStr[offset:], "\"region\"")
	deptIdx := strings.Index(resStr[offset:], "\"department\"")

	if regionIdx == -1 || deptIdx == -1 {
		t.Fatalf("Missing fields in output: %s", resStr)
	}

	// With IndexSpecification support removed from renderItem/reorderItem,
	// the output assumes standard alphabetical sorting (from Go map/json).
	// department < region
	if deptIdx > regionIdx {
		t.Errorf("Ordering failed! Expected department before region (alpha sort).\nIndices: region=%d, dept=%d\nOutput: %s", regionIdx, deptIdx, resStr)
	} else {
		fmt.Println("Ordering Verified: Department appears before Region (Alpha Sort).")
	}
}
