package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
)

func TestFindNearest_Ordering(t *testing.T) {
	// 1. Setup DB
	tmpDir := t.TempDir()
	t.Logf("Using temp dir: %s", tmpDir)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	ctx := context.Background()
	db := database.NewDatabase(dbOpts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	// Define Index Spec: Age (Desc), Name (Asc)
	indexSpec := `{
		"index_fields": [
			{"field_name": "age", "ascending_sort_order": false},
			{"field_name": "name", "ascending_sort_order": true}
		]
	}`

	store, err := jsondb.NewJsonBtreeMapKey(ctx, dbOpts, sop.StoreOptions{Name: "find_order_test"}, tx, indexSpec)
	if err != nil {
		t.Fatalf("NewJsonBtreeMapKey failed: %v", err)
	}

	val1 := any("val1")
	store.Add(ctx, []jsondb.Item[map[string]any, any]{
		{Key: map[string]any{"name": "A", "age": 30.0}, Value: &val1},
	})
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Setup Agent
	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{"testdb": dbOpts}, nil)

	// Session Payload
	// Ensure no transaction is carried over
	payload := &ai.SessionPayload{CurrentDB: "testdb", Transaction: nil}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)
	agent.Open(ctx)

	// 3. Execute FindNearest
	args := map[string]any{
		"store": "find_order_test",
		"key":   map[string]any{"name": "A", "age": 30.0},
	}

	t.Log("Calling toolFindNearest...")
	resultJSON, err := agent.toolFindNearest(ctx, args)
	if err != nil {
		t.Fatalf("toolFindNearest failed: %v", err)
	}

	t.Logf("Result: %s", resultJSON)

	// 4. Verify Ordering in JSON
	// The result is now a JSON array containing one item
	var results []struct {
		Key map[string]any `json:"key"`
	}
	if err := json.Unmarshal([]byte(resultJSON), &results); err != nil {
		t.Fatalf("Failed to unmarshal result: %v", err)
	}

	if len(results) == 0 {
		t.Fatalf("Expected at least one result")
	}

	idxAge := strings.Index(resultJSON, `"age":30`)
	idxName := strings.Index(resultJSON, `"name":"A"`)

	if idxAge == -1 || idxName == -1 {
		t.Errorf("Missing fields in JSON: %s", resultJSON)
	} else if idxAge > idxName {
		t.Errorf("Expected 'age' before 'name' in JSON key, got: %s", resultJSON)
	}
}
