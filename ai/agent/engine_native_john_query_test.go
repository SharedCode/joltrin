package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	sopdb "github.com/sharedcode/joltrin/database"
	"github.com/sharedcode/joltrin/jsondb"
)

type johnOrdersOver500Generator struct{ calls int }

func (m *johnOrdersOver500Generator) Name() string { return "john_orders_over_500_mock" }

func (m *johnOrdersOver500Generator) EstimateCost(inTokens, outTokens int) float64 { return 0 }

func (m *johnOrdersOver500Generator) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func (m *johnOrdersOver500Generator) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	m.calls++
	switch m.calls {
	case 1:
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{
					"op": "join",
					"args": map[string]any{
						"store": "users_orders",
						"on":    map[string]any{"users.key": true},
					},
				},
			}},
		}}}, nil
	case 2:
		checks := []string{
			"Ask-anchored MRU:",
			"- Last outcome: repair_required",
			"- Next delta: Research missing schema or relation facts with scoped list_stores calls before retrying execute_script.",
			"Repair strategy: research_first",
			"Repair directive: The last tool call to execute_script failed because grounded schema or relation facts are still missing.",
		}
		for _, check := range checks {
			if !strings.Contains(prompt, check) {
				return ai.GenOutput{Text: "missing John-query repair context: " + check}, nil
			}
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "list_stores",
			Args: map[string]any{"stores": []any{"users", "users_orders", "orders"}},
		}}}, nil
	case 3:
		checks := []string{
			"Ask-anchored MRU:",
			"- Last tool: list_stores",
			"- Last outcome: tool_completed",
			"- Suggested tool: execute_script",
			"list_stores confirmed users schema=",
			"list_stores confirmed orders schema=",
			"users_orders",
		}
		for _, check := range checks {
			if !strings.Contains(prompt, check) {
				return ai.GenOutput{Text: "missing John-query post-research context: " + check}, nil
			}
		}
		return ai.GenOutput{ToolCalls: []ai.ToolCall{{
			Name: "execute_script",
			Args: map[string]any{"script": []any{
				map[string]any{"op": "begin_tx", "args": map[string]any{"mode": "read"}, "result_var": "tx"},
				map[string]any{"op": "open_store", "args": map[string]any{"name": "users", "transaction": "tx"}, "result_var": "users"},
				map[string]any{"op": "open_store", "args": map[string]any{"name": "users_orders", "transaction": "tx"}, "result_var": "users_orders"},
				map[string]any{"op": "open_store", "args": map[string]any{"name": "orders", "transaction": "tx"}, "result_var": "orders"},
				map[string]any{"op": "scan", "args": map[string]any{"store": "users", "stream": true}, "result_var": "users_stream"},
				map[string]any{"op": "filter", "input_var": "users_stream", "args": map[string]any{"condition": map[string]any{"first_name": map[string]any{"$eq": "John"}}}, "result_var": "john_users"},
				map[string]any{"op": "join_right", "input_var": "john_users", "args": map[string]any{"store": "users_orders", "stream": true, "on": map[string]any{"key": "key"}}, "result_var": "user_orders_bridge"},
				map[string]any{"op": "join_right", "input_var": "user_orders_bridge", "args": map[string]any{"store": "orders", "stream": true, "on": map[string]any{"value": "key"}}, "result_var": "joined_orders"},
				map[string]any{"op": "filter", "input_var": "joined_orders", "args": map[string]any{"condition": map[string]any{"orders.total_amount": map[string]any{"$gt": 500}}}, "result_var": "expensive_orders"},
				map[string]any{"op": "project", "input_var": "expensive_orders", "args": map[string]any{"fields": []any{"users.first_name", "users.last_name", "orders.key AS order_id", "orders.total_amount", "orders.order_date"}}, "result_var": "result"},
				map[string]any{"op": "commit_tx", "args": map[string]any{"transaction": "tx"}},
			}},
		}}}, nil
	default:
		checks := []string{
			"Ask-anchored MRU:",
			"- Last tool: execute_script",
			"- Last outcome: tool_completed",
			"831",
			"John",
		}
		for _, check := range checks {
			if !strings.Contains(prompt, check) {
				return ai.GenOutput{Text: "missing John-query synthesis context: " + check}, nil
			}
		}
		return ai.GenOutput{Text: "Final answer: John Jones has order o1 with total_amount 831, which is greater than 500."}, nil
	}
}

func TestNativeReActEngine_EndToEndFindJohnOrdersOver500(t *testing.T) {
	ctx := context.Background()
	dbDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{dbDir}, CacheType: sop.InMemory}
	sysDB := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}, CacheType: sop.InMemory})

	tx, err := sopdb.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin setup tx: %v", err)
	}

	usersOpts := sop.StoreOptions{
		Name:           "users",
		SlotLength:     10,
		IsPrimitiveKey: true,
		Relations: []sop.Relation{{
			SourceFields: []string{"key"},
			TargetStore:  "users_orders",
			TargetFields: []string{"key"},
		}},
	}
	users, err := sopdb.NewBtree[string, any](ctx, dbOpts, "users", tx, nil, usersOpts)
	if err != nil {
		t.Fatalf("create users store: %v", err)
	}
	if ok, err := users.Add(ctx, "u1", map[string]any{"first_name": "John", "last_name": "Jones", "age": 30}); err != nil || !ok {
		t.Fatalf("add John user: %v", err)
	}
	if ok, err := users.Add(ctx, "u2", map[string]any{"first_name": "Jane", "last_name": "Doe", "age": 25}); err != nil || !ok {
		t.Fatalf("add Jane user: %v", err)
	}

	usersOrdersOpts := sop.StoreOptions{
		Name:           "users_orders",
		SlotLength:     10,
		IsPrimitiveKey: true,
		Relations: []sop.Relation{{
			SourceFields: []string{"value"},
			TargetStore:  "orders",
			TargetFields: []string{"key"},
		}},
	}
	usersOrders, err := sopdb.NewBtree[string, any](ctx, dbOpts, "users_orders", tx, nil, usersOrdersOpts)
	if err != nil {
		t.Fatalf("create users_orders store: %v", err)
	}
	if ok, err := usersOrders.Add(ctx, "u1", "o1"); err != nil || !ok {
		t.Fatalf("add user-order bridge for John: %v", err)
	}
	if ok, err := usersOrders.Add(ctx, "u2", "o2"); err != nil || !ok {
		t.Fatalf("add user-order bridge for Jane: %v", err)
	}

	ordersOpts := sop.StoreOptions{Name: "orders", SlotLength: 10, IsPrimitiveKey: true}
	orders, err := sopdb.NewBtree[string, any](ctx, dbOpts, "orders", tx, nil, ordersOpts)
	if err != nil {
		t.Fatalf("create orders store: %v", err)
	}
	if ok, err := orders.Add(ctx, "o1", map[string]any{"total_amount": 831, "order_date": "2026-01-01"}); err != nil || !ok {
		t.Fatalf("add expensive order: %v", err)
	}
	if ok, err := orders.Add(ctx, "o2", map[string]any{"total_amount": 100, "order_date": "2026-01-02"}); err != nil || !ok {
		t.Fatalf("add cheap order: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit setup tx: %v", err)
	}

	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{"dev_db": dbOpts}, sysDB)
	agent.StoreOpener = func(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (jsondb.StoreAccessor, error) {
		return jsondb.OpenStore(ctx, dbOpts, storeName, tx)
	}
	agent.Open(ctx)

	query := "Find orders for users with first_name 'John' with total amount > 500"
	runCtx := context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db", CurrentUserQuery: query})
	runCtx = context.WithValue(runCtx, RunnerSessionKey, &RunnerSession{Verbose: true})
	engine := &NativeReActEngine{}
	gen := &johnOrdersOver500Generator{}
	var progress []string
	runCtx = context.WithValue(runCtx, ai.CtxKeyProgressSink, func(msg string) { progress = append(progress, msg) })

	resp, err := engine.Run(runCtx, ai.ReasoningRequest{
		SystemPrompt: "You are a test assistant.",
		UserQuery:    query,
		Executor:     agent,
		Generator:    gen,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.FinalText != "Final answer: John Jones has order o1 with total_amount 831, which is greater than 500." {
		t.Fatalf("unexpected final answer: %q", resp.FinalText)
	}
	if gen.calls != 4 {
		t.Fatalf("expected four generation calls for repair, research, corrected execution, and synthesis; got %d", gen.calls)
	}
	if len(resp.ToolCalls) != 2 || resp.ToolCalls[0].Name != "list_stores" || resp.ToolCalls[1].Name != "execute_script" {
		t.Fatalf("expected list_stores followed by execute_script, got %#v", resp.ToolCalls)
	}
	factsText := strings.Join(resp.OutcomeFacts, "\n")
	if !strings.Contains(factsText, "execute_script confirmed filter field=first_name op=$eq") || !strings.Contains(factsText, "execute_script confirmed filter field=orders.total_amount op=$gt") {
		t.Fatalf("expected final execute_script grounding facts, got %#v", resp.OutcomeFacts)
	}
	seenRecipe := false
	for _, recipe := range resp.OutcomeRecipes {
		if recipe.ID == "implicit.execute_script.research_then_retry" {
			seenRecipe = true
			break
		}
	}
	if !seenRecipe {
		t.Fatalf("expected research-then-retry recipe to be learned, got %#v", resp.OutcomeRecipes)
	}
	if !containsProgressMessage(progress, "Tool `execute_script` needs corrected arguments; retrying.") {
		t.Fatalf("expected repair progress message, got %#v", progress)
	}
}
