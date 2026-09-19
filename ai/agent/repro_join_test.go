package agent_test

import (
	"context"
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

// MockGenerator bypasses LLM and returns predefined scripts for specific queries.
type MockGenerator struct {
	responses map[string]string
}

func (m *MockGenerator) Name() string {
	return "MockGenerator"
}

func (m *MockGenerator) EstimateCost(inTokens, outTokens int) float64 {
	return 0.0
}

func (m *MockGenerator) Generate(ctx context.Context, prompt string, options ai.GenOptions) (ai.GenOutput, error) {
	// Simple matching
	for k, v := range m.responses {
		if prompt == k || (len(prompt) > len(k) && prompt[len(prompt)-len(k):] == k) {
			return ai.GenOutput{Text: v}, nil
		}
	}
	// Default: return empty tool call or specific response
	return ai.GenOutput{Text: "I don't know how to handle this."}, nil
}

func TestJoinRightReproduction(t *testing.T) {
	// 1. Setup Test Database
	dbPath := "/tmp/repro_join_right"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
	}

	// Open DB to seed data
	db := database.NewDatabase(dbOpts)

	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to begin tx: %v", err)
	}

	// Create Stores
	// Use NewJsonBtreeMapKey to create if not exists
	deptStore, err := jsondb.NewJsonBtreeMapKey(ctx, db.Options(), sop.StoreOptions{Name: "department"}, tx, "")
	if err != nil {
		t.Fatalf("Failed to create department store: %v", err)
	}
	empStore, err := jsondb.NewJsonBtreeMapKey(ctx, db.Options(), sop.StoreOptions{Name: "employees"}, tx, "")
	if err != nil {
		t.Fatalf("Failed to create employees store: %v", err)
	}
	// Add Index to employees for Optimization (though not strictly required for correctness, helps replication)
	// We need an index on 'department' and 'region' to allow efficient joining if the engine picks it up?
	// The problem described: JoinRight might fail/return empty.
	// We'll populate data now.

	// Department Data
	depts := []struct {
		ID         string
		Department string
		Region     string
	}{
		{"d1", "Sales", "US-West"},
		{"d2", "Support", "US-East"},
		{"d3", "Sales", "US-East"},
	}

	for _, d := range depts {
		val := map[string]any{
			"id":         d.ID,
			"department": d.Department,
			"region":     d.Region,
		}
		// Key must be map[string]any for JsonDBMapKey
		key := map[string]any{"id": d.ID}

		var valAny any = val
		item := jsondb.Item[map[string]any, any]{
			Key:   key,
			Value: &valAny,
			ID:    uuid.New(),
		}
		if _, err := deptStore.Add(ctx, []jsondb.Item[map[string]any, any]{item}); err != nil {
			t.Fatalf("Failed to add dept: %v", err)
		}
	}

	// Employees Data
	emps := []struct {
		ID         string
		Name       string
		Department string
		Region     string
	}{
		{"e1", "Alice", "Sales", "US-West"},      // Should match d1
		{"e2", "Bob", "Support", "US-East"},      // Should match d2
		{"e3", "Charlie", "Sales", "US-East"},    // Should match d3
		{"e4", "David", "Marketing", "US-North"}, // No match
	}

	for _, e := range emps {
		val := map[string]any{
			"id":         e.ID,
			"name":       e.Name,
			"department": e.Department,
			"region":     e.Region,
		}
		key := map[string]any{"id": e.ID}
		var valAny any = val
		item := jsondb.Item[map[string]any, any]{
			Key:   key,
			Value: &valAny,
			ID:    uuid.New(),
		}
		if _, err := empStore.Add(ctx, []jsondb.Item[map[string]any, any]{item}); err != nil {
			t.Fatalf("Failed to add emp: %v", err)
		}
	}

	tx.Commit(ctx)

	// 2. Setup CopilotAgent
	cfg := agent.Config{
		Verbose: true,
	}
	databases := make(map[string]sop.DatabaseOptions)
	databases["mydb"] = dbOpts

	// Mock System DB (required by agent)
	sysDB := database.NewDatabase(sop.DatabaseOptions{
		StoresFolders: []string{"/tmp/sysdb_repro"},
	})

	execCtx := context.WithValue(ctx, agent.SessionPayloadKey, &ai.SessionPayload{
		CurrentDB: "mydb",
	})

	adminAgent := agent.NewCopilotAgent(cfg, databases, sysDB)
	if err := adminAgent.Open(execCtx); err != nil {
		t.Fatalf("Failed to open agent: %v", err)
	}

	// Actually, let's use the exact script structure from the user logs.
	scriptSteps := []any{
		map[string]any{"op": "open_db", "args": map[string]any{"name": "mydb"}, "result_var": "db"},
		map[string]any{"op": "begin_tx", "args": map[string]any{"database": "db", "mode": "read"}, "result_var": "tx"},
		map[string]any{"op": "open_store", "args": map[string]any{"name": "department", "transaction": "tx"}, "result_var": "department"},
		map[string]any{"op": "open_store", "args": map[string]any{"name": "employees", "transaction": "tx"}, "result_var": "employees"},
		map[string]any{"op": "scan", "args": map[string]any{"store": "department", "stream": true}, "result_var": "stream"},
		map[string]any{"op": "join_right", "args": map[string]any{
			"store": "employees",
			"type":  "inner",
			"on": map[string]any{
				"department": "department",
				"region":     "region",
			},
		}, "input_var": "stream", "result_var": "stream"},
		map[string]any{"op": "limit", "args": map[string]any{"limit": 7}, "input_var": "stream", "result_var": "output"},
		map[string]any{"op": "commit_tx", "args": map[string]any{"transaction": "tx"}},
	}

	fmt.Println("Executing Script...")
	res, err := adminAgent.Execute(execCtx, "execute_script", map[string]any{
		"script": scriptSteps,
	})

	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	fmt.Printf("Result: %s\n", res)

	if res == "[]" || res == "null" {
		t.Fatalf("Expected results, got empty: %s", res)
	}
}
