package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	sopdb "github.com/sharedcode/joltrin/database"
)

func TestToolJoin_OrderBy_LeftStoreDirection(t *testing.T) {
	// Setup
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)

	cfg := Config{Name: "TestAgent"}
	dbs := make(map[string]sop.DatabaseOptions)
	agent := NewCopilotAgent(cfg, dbs, sysDB)

	ctx := context.Background()
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "system"})
	agent.Open(ctx)

	// Create Stores and Data
	t2, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)

	// Left Store: Departments
	leftOpts := sop.StoreOptions{Name: "departments", SlotLength: 10, IsPrimitiveKey: true}
	left, _ := sopdb.NewBtree[string, any](ctx, dbOpts, "departments", t2, nil, leftOpts)
	left.Add(ctx, "D1", map[string]any{"id": "D1", "name": "Sales"})
	left.Add(ctx, "D2", map[string]any{"id": "D2", "name": "Marketing"})
	left.Add(ctx, "D3", map[string]any{"id": "D3", "name": "Engineering"})

	// Right Store: Employees
	rightOpts := sop.StoreOptions{Name: "employees", SlotLength: 10, IsPrimitiveKey: true}
	right, _ := sopdb.NewBtree[string, any](ctx, dbOpts, "employees", t2, nil, rightOpts)
	// Add in order E1, E2, E3
	right.Add(ctx, "E1", map[string]any{"id": "E1", "dept_id": "D1", "name": "Alice"})
	right.Add(ctx, "E2", map[string]any{"id": "E2", "dept_id": "D2", "name": "Bob"})
	right.Add(ctx, "E3", map[string]any{"id": "E3", "dept_id": "D3", "name": "Charlie"})

	t2.Commit(ctx)

	// Test DESC
	args := map[string]any{
		"left_store":        "departments",
		"right_store":       "employees",
		"left_join_fields":  []string{"id"},
		"right_join_fields": []string{"dept_id"},
		"order_by":          "key desc",
		"fields":            []string{"departments.id"},
	}
	res, err := agent.Execute(ctx, "join", args)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	var items []map[string]any
	json.Unmarshal([]byte(res), &items)

	// We expect 3 items
	if len(items) != 3 {
		t.Fatalf("Expected 3 items, got %d", len(items))
	}

	// Check Order: Should be D3, D2, D1
	getVal := func(item map[string]any) string {
		// Handle both nested "key" layout and flat layout
		if k, ok := item["key"]; ok && k != nil {
			if v, ok := k.(map[string]any); ok {
				if val, ok := v["Id"]; ok {
					return val.(string)
				}
				if val, ok := v["id"]; ok {
					return val.(string)
				}
			}
		}
		// Flat layout fallback
		if val, ok := item["Id"]; ok {
			return val.(string)
		}
		if val, ok := item["id"]; ok {
			return val.(string)
		}
		return ""
	}

	if getVal(items[0]) != "D3" {
		t.Errorf("Expected first item 'D3', got '%s'", getVal(items[0]))
	}
	if getVal(items[1]) != "D2" {
		t.Errorf("Expected second item 'D2', got '%s'", getVal(items[1]))
	}
	if getVal(items[2]) != "D1" {
		t.Errorf("Expected third item 'D1', got '%s'", getVal(items[2]))
	}
}
