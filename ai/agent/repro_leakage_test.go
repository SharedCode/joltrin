package agent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/ai/database"
)

// SmartMockGenerator returns specific tool calls for specific prompts
type SmartMockGenerator struct {
	Scripts  map[string]string
	IsNative bool
}

func (m *SmartMockGenerator) Name() string                     { return "mock" }
func (m *SmartMockGenerator) EstimateCost(in, out int) float64 { return 0 }

func (m *SmartMockGenerator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}
func (m *SmartMockGenerator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	var scriptBody string
	// Instead of naive strings.Contains which can trigger on history context,
	// we should only check the last line or lines after the last "User: " prompt.
	parts := strings.Split(prompt, "User: ")
	lastPrompt := parts[len(parts)-1]

	if strings.Contains(lastPrompt, "Analyze the tool response") {
		// Mock the LLM extracting the JSON result and presenting it to the user.
		// We can just extract the [System Tool Response] block and return it as text.
		blocks := strings.Split(prompt, "[System Tool Response]:\n")
		if len(blocks) > 1 {
			sysResp := strings.SplitN(blocks[len(blocks)-1], "\n\nUser:", 2)[0]
			return ai.GenOutput{Text: strings.TrimSpace(sysResp)}, nil
		}
		// Fallback
		return ai.GenOutput{Text: "Task complete."}, nil
	}

	if strings.Contains(lastPrompt, "Show me all users") {
		scriptBody = m.Scripts["sort"]
	} else if strings.Contains(lastPrompt, "Find orders for user") {
		scriptBody = m.Scripts["join"]
	} else if strings.Contains(lastPrompt, "List products") {
		scriptBody = m.Scripts["list"]
	} else if strings.Contains(lastPrompt, "Run alias test") {
		scriptBody = m.Scripts["alias"]
	} else {
		return ai.GenOutput{Text: "I don't know how to do that."}, nil
	}

	// Format as a tool call
	// The CopilotAgent expects a JSON object with "tool" and "args"
	// or a list of such objects.
	toolCall := map[string]any{
		"tool": "execute_script",
		"args": map[string]any{
			"script": scriptBody, // The script body is already a JSON string, but we usually pass object/array
		},
	}

	// We need to parse scriptBody (which is JSON string) into actual object for the args
	var scriptObj any
	if err := json.Unmarshal([]byte(scriptBody), &scriptObj); err == nil {
		toolCall["args"] = map[string]any{"script": scriptObj}
	}

	if m.IsNative {
		var scriptObj any
		if err := json.Unmarshal([]byte(scriptBody), &scriptObj); err == nil {
			return ai.GenOutput{
				ToolCalls: []ai.ToolCall{
					{
						Name: "execute_script",
						Args: map[string]any{"script": scriptObj},
					},
				},
			}, nil
		}
	}

	bytes, _ := json.MarshalIndent(toolCall, "", "  ")
	return ai.GenOutput{Text: fmt.Sprintf("```json\n%s\n```", string(bytes))}, nil
}

func populateDevDB(t *testing.T, ctx context.Context, db *database.Database) {
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("Failed to begin tx: %v", err)
	}
	defer tx.Rollback(ctx)

	users, _ := db.NewBtree(ctx, "users", tx)
	users.Add(ctx, "u1", map[string]any{"id": "u1", "name": "Alice"})
	users.Add(ctx, "u2", map[string]any{"id": "u2", "name": "Bob"})

	orders, _ := db.NewBtree(ctx, "orders", tx)
	orders.Add(ctx, "o1", map[string]any{"id": "o1", "user_id": "u1", "total_amount": 100})
	orders.Add(ctx, "o2", map[string]any{"id": "o2", "user_id": "u2", "total_amount": 600})

	products, _ := db.NewBtree(ctx, "products", tx)
	products.Add(ctx, "p1", map[string]any{"id": "p1", "category": "Electronics", "name": "Laptop"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Failed to commit seed data: %v", err)
	}
}

func TestRepro_Leakage_StateCleaning_Legacy(t *testing.T) {
	t.Skip("Legacy tools removed")
	// 1. Setup Environment
	tempDir := t.TempDir()

	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tempDir},
	}

	devDB := database.NewDatabase(dbOpts)
	systemDB := database.NewDatabase(dbOpts) // Using same opts/dir for simplicity

	ctx := context.Background()
	populateDevDB(t, ctx, devDB)

	// 2. Prepare Scripts (Added @ to return values to ensure variable resolution)
	scripts := map[string]string{
		"sort": `[
			{"op": "open_db", "args": {"name": "dev_db"}, "result_var": "db"},
			{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
			{"op": "open_store", "args": {"transaction": "tx", "name": "users"}, "result_var": "users"},
			{"op": "scan", "args": {"store": "users"}, "result_var": "output"},
			{"op": "commit_tx", "args": {"transaction": "tx"}},
			{"op": "return", "args": {"value": "@output"}}
		]`,
		"join": `[
			{"op": "open_db", "args": {"name": "dev_db"}, "result_var": "db"},
			{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
			{"op": "open_store", "args": {"transaction": "tx", "name": "users"}, "result_var": "users"},
			{"op": "open_store", "args": {"transaction": "tx", "name": "orders"}, "result_var": "orders"},
			{"op": "scan", "args": {"store": "orders", "filter": {"total_amount": {"$gt": 500}}}, "result_var": "output"},
			{"op": "commit_tx", "args": {"transaction": "tx"}},
			{"op": "return", "args": {"value": "@output"}}
		]`,
		"list": `[
			{"op": "open_db", "args": {"name": "dev_db"}, "result_var": "db"},
			{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
			{"op": "open_store", "args": {"transaction": "tx", "name": "products"}, "result_var": "products"},
			{"op": "scan", "args": {"store": "products", "filter": {"category": {"$eq": "Electronics"}}}, "result_var": "output"},
			{"op": "commit_tx", "args": {"transaction": "tx"}},
			{"op": "return", "args": {"value": "@output"}}
		]`,
	}

	mockBrain := &SmartMockGenerator{Scripts: scripts}

	// 3. Initialize Service
	// We pass "dev_db" as the name of the DB we want to use.
	// We need to inject the SessionPayload via Context for Ask to know CurrentDB.

	databases := map[string]sop.DatabaseOptions{"dev_db": dbOpts}
	svc := agent.NewCopilotAgent(agent.Config{
		ID:   "repro-agent",
		Name: "Repro",
	}, databases, systemDB)

	ctx = context.WithValue(context.Background(), agent.SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db"})
	svc.Open(ctx)

	svc.SetGenerator(mockBrain)
	// svc.SetFeature("history_injection", false) // Cannot access directly, assuming default or ignoring.

	// 4. Run Scenarios

	// Set session payload
	payload := &ai.SessionPayload{
		CurrentDB: "dev_db",
	}
	runCtx := context.WithValue(ctx, agent.SessionPayloadKey, payload)

	// Cmd 1: Sort Users
	// Expecting "all_users" (2 users)
	t.Logf("Running Cmd 1: Show me all users")
	res1, err := svc.Ask(runCtx, "Show me all users", nil)
	if err != nil {
		t.Fatalf("Cmd 1 failed: %v", err)
	}
	t.Logf("Result 1: %s", res1) // Should look like JSON array of 2 users
	if !strings.Contains(res1, "Alice") || !strings.Contains(res1, "Bob") {
		t.Errorf("Cmd 1 did not return users. Got: %s", res1)
	}

	// Cmd 2: Find Orders
	// Expecting "big_orders" (1 order > 500)
	t.Logf("Running Cmd 2: Find orders for user")
	res2, err := svc.Ask(runCtx, "Find orders for user", nil)
	if err != nil {
		t.Fatalf("Cmd 2 failed: %v", err)
	}
	t.Logf("Result 2: %s", res2)

	// Fails if Result 2 contains the Result 1 data
	if strings.Contains(res2, "Alice") && !strings.Contains(res2, "total_amount") {
		t.Errorf("LEAKAGE DETECTED: Cmd 2 returned users instead of orders! Result: %s", res2)
	}

	// It should contain order info
	if !strings.Contains(res2, "600") {
		t.Errorf("Cmd 2 did not return correct order. Got: %s", res2)
	}

	// Cmd 3: List Products
	t.Logf("Running Cmd 3: List products")
	res3, err := svc.Ask(runCtx, "List products", nil)
	t.Logf("Result 3: %s", res3)
	if strings.Contains(res3, "Alice") {
		t.Errorf("LEAKAGE DETECTED: Cmd 3 returned users!")
	}
	if !strings.Contains(res3, "Laptop") {
		t.Errorf("Cmd 3 did not return products.")
	}
}

func TestRepro_Leakage_StateCleaning_Native(t *testing.T) {
	// 1. Setup Environment
	tempDir := t.TempDir()

	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tempDir},
	}

	devDB := database.NewDatabase(dbOpts)
	systemDB := database.NewDatabase(dbOpts) // Using same opts/dir for simplicity

	ctx := context.Background()
	populateDevDB(t, ctx, devDB)

	// 2. Prepare Scripts (Added @ to return values to ensure variable resolution)
	scripts := map[string]string{
		"sort": `[
			{"op": "open_db", "args": {"name": "dev_db"}, "result_var": "db"},
			{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
			{"op": "open_store", "args": {"transaction": "tx", "name": "users"}, "result_var": "users"},
			{"op": "scan", "args": {"store": "users"}, "result_var": "output"},
			{"op": "commit_tx", "args": {"transaction": "tx"}},
			{"op": "return", "args": {"value": "@output"}}
		]`,
		"join": `[
			{"op": "open_db", "args": {"name": "dev_db"}, "result_var": "db"},
			{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
			{"op": "open_store", "args": {"transaction": "tx", "name": "users"}, "result_var": "users"},
			{"op": "open_store", "args": {"transaction": "tx", "name": "orders"}, "result_var": "orders"},
			{"op": "scan", "args": {"store": "orders", "filter": {"total_amount": {"$gt": 500}}}, "result_var": "output"},
			{"op": "commit_tx", "args": {"transaction": "tx"}},
			{"op": "return", "args": {"value": "@output"}}
		]`,
		"list": `[
			{"op": "open_db", "args": {"name": "dev_db"}, "result_var": "db"},
			{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
			{"op": "open_store", "args": {"transaction": "tx", "name": "products"}, "result_var": "products"},
			{"op": "scan", "args": {"store": "products", "filter": {"category": {"$eq": "Electronics"}}}, "result_var": "output"},
			{"op": "commit_tx", "args": {"transaction": "tx"}},
			{"op": "return", "args": {"value": "@output"}}
		]`,
	}

	mockBrain := &SmartMockGenerator{Scripts: scripts, IsNative: true}

	// 3. Initialize Service
	// We pass "dev_db" as the name of the DB we want to use.
	// We need to inject the SessionPayload via Context for Ask to know CurrentDB.

	databases := map[string]sop.DatabaseOptions{"dev_db": dbOpts}
	svc := agent.NewCopilotAgent(agent.Config{
		ID:   "repro-agent",
		Name: "Repro",
	}, databases, systemDB)

	ctx = context.WithValue(context.Background(), agent.SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db"})
	svc.Open(ctx)

	svc.SetGenerator(mockBrain)
	// svc.SetFeature("history_injection", false) // Cannot access directly, assuming default or ignoring.

	// 4. Run Scenarios

	// Set session payload
	payload := &ai.SessionPayload{
		CurrentDB: "dev_db",
	}
	runCtx := context.WithValue(ctx, agent.SessionPayloadKey, payload)

	// Cmd 1: Sort Users
	// Expecting "all_users" (2 users)
	t.Logf("Running Cmd 1: Show me all users")
	res1, err := svc.Ask(runCtx, "Show me all users", nil)
	if err != nil {
		t.Fatalf("Cmd 1 failed: %v", err)
	}
	t.Logf("Result 1: %s", res1) // Should look like JSON array of 2 users
	if !strings.Contains(res1, "Alice") || !strings.Contains(res1, "Bob") {
		t.Errorf("Cmd 1 did not return users. Got: %s", res1)
	}

	// Cmd 2: Find Orders
	// Expecting "big_orders" (1 order > 500)
	t.Logf("Running Cmd 2: Find orders for user")
	res2, err := svc.Ask(runCtx, "Find orders for user", nil)
	if err != nil {
		t.Fatalf("Cmd 2 failed: %v", err)
	}
	t.Logf("Result 2: %s", res2)

	// Fails if Result 2 contains the Result 1 data
	if strings.Contains(res2, "Alice") && !strings.Contains(res2, "total_amount") {
		t.Errorf("LEAKAGE DETECTED: Cmd 2 returned users instead of orders! Result: %s", res2)
	}

	// It should contain order info
	if !strings.Contains(res2, "600") {
		t.Errorf("Cmd 2 did not return correct order. Got: %s", res2)
	}

	// Cmd 3: List Products
	t.Logf("Running Cmd 3: List products")
	res3, err := svc.Ask(runCtx, "List products", nil)
	t.Logf("Result 3: %s", res3)
	if strings.Contains(res3, "Alice") {
		t.Errorf("LEAKAGE DETECTED: Cmd 3 returned users!")
	}
	if !strings.Contains(res3, "Laptop") {
		t.Errorf("Cmd 3 did not return products.")
	}
}

func TestAliasProjection_JoinRight_Legacy(t *testing.T) {
	t.Skip("Legacy tools removed")
	// Setup
	tempDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tempDir},
	}
	devDB := database.NewDatabase(dbOpts)
	systemDB := database.NewDatabase(dbOpts)
	ctx := context.Background()
	populateDevDB(t, ctx, devDB)

	// Script: Join Right using a variable that is a Store.
	// Users are in "users" (from populateDevDB).
	// Orders are in "orders" (from populateDevDB).
	// We load "orders" into "orders_var".
	// We Scan "users" -> "users_scan".
	// We Join "users_scan" with "orders_var" (Right Side).
	// Fix Requirement: The join must resolve alias to "orders" (store name), not "orders_var".
	// Projection: "orders.*" should work.

	script := `[
		{"op": "open_db", "args": {"name": "dev_db"}, "result_var": "db"},
		{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
		{"op": "open_store", "args": {"transaction": "tx", "name": "users"}, "result_var": "users"},
		{"op": "open_store", "args": {"transaction": "tx", "name": "orders"}, "result_var": "orders_var"},
		{"op": "scan", "args": {"store": "users"}, "result_var": "users_scan"},
		{"op": "join", "args": {"with": "@orders_var", "on": {"id": "user_id"}}, "input_var": "users_scan", "result_var": "joined"},
		{"op": "project", "args": {"fields": ["orders.*"]}, "input_var": "joined", "result_var": "output"},
		{"op": "commit_tx", "args": {"transaction": "tx"}},
		{"op": "return", "args": {"value": "@output"}}
	]`

	scripts := map[string]string{
		"alias": script,
	}

	mockBrain := &SmartMockGenerator{Scripts: scripts}

	databases := map[string]sop.DatabaseOptions{"dev_db": dbOpts}
	svc := agent.NewCopilotAgent(agent.Config{
		ID:   "repro-agent-alias",
		Name: "ReproAlias",
	}, databases, systemDB)
	svc.Open(ctx)
	svc.SetGenerator(mockBrain)

	payload := &ai.SessionPayload{CurrentDB: "dev_db"}
	runCtx := context.WithValue(ctx, agent.SessionPayloadKey, payload)

	res, err := svc.Ask(runCtx, "Run alias test", nil)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}

	// Validation
	// Should contain orders info (total_amount 100 or 600)
	// And keys should be clean (total_amount, not orders.total_amount)
	t.Logf("Legacy Result: %s", res)
	t.Logf("Legacy Result: %s", res)
	if !strings.Contains(res, "600") {
		t.Errorf("Result expected to contain '600', got: %s", res)
	}
	if !strings.Contains(res, "total_amount") {
		t.Errorf("Result expected to contain 'total_amount', got: %s", res)
	}
}

func TestAliasProjection_JoinRight_Native(t *testing.T) {
	// Setup
	tempDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tempDir},
	}
	devDB := database.NewDatabase(dbOpts)
	systemDB := database.NewDatabase(dbOpts)
	ctx := context.Background()
	populateDevDB(t, ctx, devDB)

	// Script: Join Right using a variable that is a Store.
	// Users are in "users" (from populateDevDB).
	// Orders are in "orders" (from populateDevDB).
	// We load "orders" into "orders_var".
	// We Scan "users" -> "users_scan".
	// We Join "users_scan" with "orders_var" (Right Side).
	// Fix Requirement: The join must resolve alias to "orders" (store name), not "orders_var".
	// Projection: "orders.*" should work.

	script := `[
		{"op": "open_db", "args": {"name": "dev_db"}, "result_var": "db"},
		{"op": "begin_tx", "args": {"database": "db", "mode": "read"}, "result_var": "tx"},
		{"op": "open_store", "args": {"transaction": "tx", "name": "users"}, "result_var": "users"},
		{"op": "open_store", "args": {"transaction": "tx", "name": "orders"}, "result_var": "orders_var"},
		{"op": "scan", "args": {"store": "users"}, "result_var": "users_scan"},
		{"op": "join", "args": {"with": "@orders_var", "on": {"id": "user_id"}}, "input_var": "users_scan", "result_var": "joined"},
		{"op": "project", "args": {"fields": ["orders.total_amount"]}, "input_var": "joined", "result_var": "output"},
		{"op": "commit_tx", "args": {"transaction": "tx"}},
		{"op": "return", "args": {"value": "@output"}}
	]`

	scripts := map[string]string{
		"alias": script,
	}

	mockBrain := &SmartMockGenerator{Scripts: scripts, IsNative: true}

	databases := map[string]sop.DatabaseOptions{"dev_db": dbOpts}
	svc := agent.NewCopilotAgent(agent.Config{
		ID:   "repro-agent-alias",
		Name: "ReproAlias",
	}, databases, systemDB)
	svc.Open(ctx)
	svc.SetGenerator(mockBrain)

	payload := &ai.SessionPayload{CurrentDB: "dev_db"}
	runCtx := context.WithValue(ctx, agent.SessionPayloadKey, payload)

	res, err := svc.Ask(runCtx, "Run alias test", nil)
	if err != nil {
		t.Fatalf("Ask failed: %v", err)
	}

	// Validation
	// Should contain orders info (total_amount 100 or 600)
	// And keys should be clean (total_amount, not orders.total_amount)
	t.Logf("Legacy Result: %s", res)
	t.Logf("Legacy Result: %s", res)
	if !strings.Contains(res, "600") {
		t.Errorf("Result expected to contain '600', got: %s", res)
	}
	if !strings.Contains(res, "total_amount") {
		t.Errorf("Result expected to contain 'total_amount', got: %s", res)
	}
}
