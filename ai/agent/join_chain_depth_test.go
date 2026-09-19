package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	core_database "github.com/sharedcode/joltrin/database"
	"github.com/stretchr/testify/assert"
)

func TestToolJoin_Chained_ABCD(t *testing.T) {
	// 1. Setup Temp DB
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}

	// Initialize SystemDB
	_ = database.NewDatabase(dbOpts)

	// 2. Create Test Stores with Data
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	// Store A
	storeA, _ := core_database.NewBtree[string, any](ctx, dbOpts, "A", tx, nil)
	storeA.Add(ctx, "k1", map[string]any{"id": 1, "val": "A1"})

	// Store B
	storeB, _ := core_database.NewBtree[string, any](ctx, dbOpts, "B", tx, nil)
	storeB.Add(ctx, "k1", map[string]any{"id": 1, "val": "B1"})

	// Store C
	storeC, _ := core_database.NewBtree[string, any](ctx, dbOpts, "C", tx, nil)
	storeC.Add(ctx, "k1", map[string]any{"id": 1, "val": "C1"})

	// Store D
	storeD, _ := core_database.NewBtree[string, any](ctx, dbOpts, "D", tx, nil)
	storeD.Add(ctx, "k1", map[string]any{"id": 1, "val": "D1"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 3. Prepare Agent
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"testdb": dbOpts,
		},
	}
	sessionPayload := &ai.SessionPayload{
		CurrentDB: "testdb",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	// 4. Executing Script via ToolJoin calls (Simulating Chain)
	// We can't easily valid "toolJoin" chaining in one go because toolJoin handles ONE join.
	// But `script` tool handles chaining.
	// We should test `stageJoin` chaining or `script` execution.
	// The user likely means "Can I write a script that does A join B join C join D?".
	// Let's use `toolScript` to process a JSON script that does this.

	scriptJSON := `[
		{
			"op": "open_db",
			"args": {"name": "testdb"}
		},
		{
			"op": "begin_tx",
			"args": {"database": "testdb", "mode": "read"},
			"result_var": "tx1"
		},
		{
			"op": "open_store",
			"args": {"transaction": "tx1", "name": "A"},
			"result_var": "storeA"
		},
		{
			"op": "scan",
			"args": {"store": "storeA", "stream": true},
			"result_var": "cursorA"
		},
		{
			"op": "open_store",
			"args": {"transaction": "tx1", "name": "B"},
			"result_var": "storeB"
		},
		{
			"op": "join",
			"input_var": "cursorA",
			"args": {
				"with": "storeB",
				"on": {"id": "id"},
				"type": "inner"
			},
			"result_var": "res1"
		},
		{
			"op": "open_store",
			"args": {"transaction": "tx1", "name": "C"},
			"result_var": "storeC"
		},
		{
			"op": "join",
			"input_var": "res1",
			"args": {
				"with": "storeC",
				"on": {"B.id": "id"},
				"type": "inner"
			},
			"result_var": "res2"
		},
		{
			"op": "open_store",
			"args": {"transaction": "tx1", "name": "D"},
			"result_var": "storeD"
		},
		{
			"op": "join",
			"input_var": "res2",
			"args": {
				"with": "storeD",
				"on": {"C.id": "id"},
				"type": "inner"
			},
			"result_var": "final"
		}
	]`

	// We need to parse this script and run it using the agent's internal script runner
	// Or we can construct strict steps.
	// `toolScript` calls `ExecuteScript`.

	var steps []ai.ScriptStep
	if err := json.Unmarshal([]byte(scriptJSON), &steps); err != nil {
		t.Fatalf("JSON Unmarshal failed: %v", err)
	}

	// Mock Script Execution (since toolScript isn't exposed directly as public with struct args easily)
	// But we have `agent.toolScript`.
	// Actually `toolScript` takes "script" as string argument.

	args := map[string]any{
		"script": scriptJSON,
	}

	var streamed []any
	ctx = context.WithValue(ctx, ai.CtxKeyEventStreamer, func(eventType string, payload any) {
		if eventType == "record" {
			streamed = append(streamed, payload)
		}
	})

	result, err := agent.toolExecuteScript(ctx, args)
	if err != nil {
		t.Fatalf("toolExecuteScript failed: %v", err)
	}

	// Verify Result
	// The output format of toolScript depends on the last step result or explicit output.
	// If no implicit output, we might need to verify via side effects or ensure `final` is returned.
	// The toolScript usually returns the result of the last operation if it's a query.

	t.Logf("Result: %s", result)
	assert.Empty(t, result, "final cursor result should stay on the streaming path")

	if len(streamed) == 0 {
		t.Fatal("expected streamed records for the final cursor result")
	}

	for _, item := range streamed {
		b, _ := json.Marshal(item)
		itemJSON := string(b)
		if strings.Contains(itemJSON, "D1") && strings.Contains(itemJSON, "C1") && strings.Contains(itemJSON, "B1") && strings.Contains(itemJSON, "A1") {
			return
		}
	}

	t.Fatalf("expected streamed cursor rows to contain the chained join values, got: %+v", streamed)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[0:len(substr)] == substr || s[len(s)-len(substr):] == substr ||
		func() bool {
			for i := 0; i < len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		}()))
}
