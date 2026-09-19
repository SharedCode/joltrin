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
	"github.com/sharedcode/joltrin/jsondb"
)

func TestExecuteScript_RelationTargetJoinCompatibility(t *testing.T) {
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}

	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin setup tx: %v", err)
	}

	users, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users", tx, nil)
	users.Add(ctx, "u1", map[string]any{"first_name": "John", "last_name": "Jones", "age": 30})
	users.Add(ctx, "u2", map[string]any{"first_name": "Jane", "last_name": "Doe", "age": 25})

	orders, _ := core_database.NewBtree[any, any](ctx, dbOpts, "orders", tx, nil)
	orders.Add(ctx, "o1", map[string]any{"total_amount": 831, "order_date": "2026-01-01"})
	orders.Add(ctx, "o2", map[string]any{"total_amount": 100, "order_date": "2026-01-02"})

	usersOrders, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users_orders", tx, nil)
	usersOrders.Add(ctx, "u1", "o1")

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit setup tx: %v", err)
	}

	agent := &CopilotAgent{
		systemDB: sysDB,
		databases: map[string]sop.DatabaseOptions{
			"dev_db": dbOpts,
		},
		StoreOpener: func(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (jsondb.StoreAccessor, error) {
			s, _ := core_database.NewBtree[any, any](ctx, dbOpts, storeName, tx, nil)
			return &testStoreWrapper{s}, nil
		},
	}

	script := []map[string]any{
		{"args": map[string]any{"database": "dev_db", "mode": "read"}, "op": "begin_tx", "result_var": "tx"},
		{"args": map[string]any{"name": "users", "transaction": "tx"}, "op": "open_store", "result_var": "users_store"},
		{"args": map[string]any{"name": "orders", "transaction": "tx"}, "op": "open_store", "result_var": "orders_store"},
		{"args": map[string]any{"store": "users_store"}, "op": "scan", "result_var": "users_scan"},
		{"input_var": "users_scan", "args": map[string]any{"condition": map[string]any{"first_name": map[string]any{"$eq": "John"}}}, "op": "filter", "result_var": "filtered_users"},
		{"input_var": "filtered_users", "args": map[string]any{"relation": "users_orders", "target": "orders_store"}, "op": "join", "result_var": "joined_data"},
		{"input_var": "joined_data", "args": map[string]any{"condition": map[string]any{"orders.total_amount": map[string]any{"$gt": 500}}}, "op": "filter", "result_var": "final_result"},
		{"args": map[string]any{"transaction": "tx"}, "op": "commit_tx"},
		{"input_var": "final_result", "op": "return"},
	}

	scriptBytes, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("marshal script: %v", err)
	}

	ctxWithPayload := context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db"})
	resultRaw, err := agent.toolExecuteScript(ctxWithPayload, map[string]any{"script": string(scriptBytes)})
	if err != nil {
		t.Fatalf("toolExecuteScript failed: %v", err)
	}
	result, err := formatToolResult(ctxWithPayload, resultRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	if !strings.Contains(result, "John") {
		t.Fatalf("expected joined result to contain John, got %s", result)
	}
	if !strings.Contains(result, "831") {
		t.Fatalf("expected joined result to contain matching order total, got %s", result)
	}
	if strings.Contains(result, "100") {
		t.Fatalf("expected low-value order to be filtered out, got %s", result)
	}
}

func TestExecuteScript_JoinResolvesStoreByUnderlyingNameWhenResultVarDiffers(t *testing.T) {
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}

	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin setup tx: %v", err)
	}

	usersByAge, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users_by_age", tx, nil)
	usersByAge.Add(ctx, 30, "u1")
	usersByAge.Add(ctx, 25, "u2")

	users, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users", tx, nil)
	users.Add(ctx, "u1", map[string]any{"first_name": "John", "age": 30})
	users.Add(ctx, "u2", map[string]any{"first_name": "Jane", "age": 25})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit setup tx: %v", err)
	}

	agent := &CopilotAgent{
		systemDB: sysDB,
		databases: map[string]sop.DatabaseOptions{
			"dev_db": dbOpts,
		},
		StoreOpener: func(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (jsondb.StoreAccessor, error) {
			s, _ := core_database.NewBtree[any, any](ctx, dbOpts, storeName, tx, nil)
			return &testStoreWrapper{s}, nil
		},
	}

	script := []map[string]any{
		{"args": map[string]any{"database": "dev_db", "mode": "read"}, "op": "begin_tx", "result_var": "tx"},
		{"args": map[string]any{"name": "users_by_age", "transaction": "tx"}, "op": "open_store", "result_var": "idx_store"},
		{"args": map[string]any{"name": "users", "transaction": "tx"}, "op": "open_store", "result_var": "users_store"},
		{"args": map[string]any{"direction": "desc", "store": "idx_store"}, "op": "scan", "result_var": "idx_records"},
		{"args": map[string]any{"on": map[string]any{"value": "key"}, "store": "users"}, "input_var": "idx_records", "op": "join", "result_var": "users_list"},
		{"args": map[string]any{"transaction": "tx"}, "op": "commit_tx"},
		{"input_var": "users_list", "op": "return"},
	}

	scriptBytes, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("marshal script: %v", err)
	}

	ctxWithPayload := context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db"})
	resultRaw, err := agent.toolExecuteScript(ctxWithPayload, map[string]any{"script": string(scriptBytes)})
	if err != nil {
		t.Fatalf("toolExecuteScript failed: %v", err)
	}
	result, err := formatToolResult(ctxWithPayload, resultRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	if !strings.Contains(result, "John") {
		t.Fatalf("expected joined result to contain John, got %s", result)
	}
	if !strings.Contains(result, "Jane") {
		t.Fatalf("expected joined result to contain Jane, got %s", result)
	}
}

func TestExecuteScript_OpenStoreWithoutResultVarCompatibility(t *testing.T) {
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}

	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin setup tx: %v", err)
	}

	users, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users", tx, nil)
	users.Add(ctx, "u1", map[string]any{"first_name": "John", "last_name": "Jones", "age": 30})

	usersOrders, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users_orders", tx, nil)
	usersOrders.Add(ctx, "u1", "o1")

	orders, _ := core_database.NewBtree[any, any](ctx, dbOpts, "orders", tx, nil)
	orders.Add(ctx, "o1", map[string]any{"total_amount": 831, "order_date": "2026-01-01"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit setup tx: %v", err)
	}

	agent := &CopilotAgent{
		systemDB: sysDB,
		databases: map[string]sop.DatabaseOptions{
			"dev_db": dbOpts,
		},
		StoreOpener: func(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (jsondb.StoreAccessor, error) {
			s, _ := core_database.NewBtree[any, any](ctx, dbOpts, storeName, tx, nil)
			return &testStoreWrapper{s}, nil
		},
	}

	script := []map[string]any{
		{"args": map[string]any{"mode": "read"}, "op": "begin_tx", "result_var": "tx"},
		{"args": map[string]any{"name": "users", "transaction": "tx"}, "op": "open_store"},
		{"args": map[string]any{"name": "users_orders", "transaction": "tx"}, "op": "open_store"},
		{"args": map[string]any{"name": "orders", "transaction": "tx"}, "op": "open_store"},
		{"args": map[string]any{"store": "users"}, "op": "scan", "result_var": "users_stream"},
		{"args": map[string]any{"condition": map[string]any{"first_name": map[string]any{"$eq": "John"}}}, "input_var": "users_stream", "op": "filter", "result_var": "john_users"},
		{"args": map[string]any{"on": map[string]any{"key": "key"}, "store": "users_orders"}, "input_var": "john_users", "op": "join_right", "result_var": "user_orders_bridge"},
		{"args": map[string]any{"on": map[string]any{"value": "key"}, "store": "orders"}, "input_var": "user_orders_bridge", "op": "join_right", "result_var": "orders_stream"},
		{"args": map[string]any{"condition": map[string]any{"total_amount": map[string]any{"$gt": 500}}}, "input_var": "orders_stream", "op": "filter", "result_var": "final_orders"},
		{"args": map[string]any{"transaction": "tx"}, "op": "commit_tx"},
		{"args": map[string]any{"value": map[string]any{"$var": "final_orders"}}, "op": "return"},
	}

	scriptBytes, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("marshal script: %v", err)
	}

	ctxWithPayload := context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db"})
	resultRaw, err := agent.toolExecuteScript(ctxWithPayload, map[string]any{"script": string(scriptBytes)})
	if err != nil {
		t.Fatalf("toolExecuteScript failed: %v", err)
	}
	result, err := formatToolResult(ctxWithPayload, resultRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	if !strings.Contains(result, "John") {
		t.Fatalf("expected joined result to contain John, got %s", result)
	}
	if !strings.Contains(result, "831") {
		t.Fatalf("expected joined result to contain matching order total, got %s", result)
	}
}

func TestExecuteScript_JoinAutoOpensRightStoresByUnderlyingName(t *testing.T) {
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}

	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin setup tx: %v", err)
	}

	users, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users", tx, nil)
	users.Add(ctx, "u1", map[string]any{"first_name": "John", "last_name": "Jones"})
	users.Add(ctx, "u2", map[string]any{"first_name": "Jane", "last_name": "Doe"})

	usersOrders, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users_orders", tx, nil)
	usersOrders.Add(ctx, "u1", "o1")
	usersOrders.Add(ctx, "u2", "o2")

	orders, _ := core_database.NewBtree[any, any](ctx, dbOpts, "orders", tx, nil)
	orders.Add(ctx, "o1", map[string]any{"total_amount": 1500, "order_date": "2026-01-01"})
	orders.Add(ctx, "o2", map[string]any{"total_amount": 100, "order_date": "2026-01-02"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit setup tx: %v", err)
	}

	agent := &CopilotAgent{
		systemDB: sysDB,
		databases: map[string]sop.DatabaseOptions{
			"dev_db": dbOpts,
		},
		StoreOpener: func(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (jsondb.StoreAccessor, error) {
			s, _ := core_database.NewBtree[any, any](ctx, dbOpts, storeName, tx, nil)
			return &testStoreWrapper{s}, nil
		},
	}

	script := []map[string]any{
		{"args": map[string]any{"database": "dev_db", "mode": "read"}, "op": "begin_tx", "result_var": "tx"},
		{"args": map[string]any{"name": "users", "transaction": "tx"}, "op": "open_store", "result_var": "users_store"},
		{"args": map[string]any{"store": "users_store"}, "op": "scan", "result_var": "s1"},
		{"args": map[string]any{"condition": map[string]any{"first_name": map[string]any{"$eq": "John"}}}, "input_var": "s1", "op": "filter", "result_var": "f1"},
		{"args": map[string]any{"on": map[string]any{"users.key": "key"}, "store": "users_orders"}, "input_var": "f1", "op": "join", "result_var": "j1"},
		{"args": map[string]any{"on": map[string]any{"users_orders.value": "key"}, "store": "orders"}, "input_var": "j1", "op": "join", "result_var": "j2"},
		{"args": map[string]any{"condition": map[string]any{"orders.total_amount": map[string]any{"$gt": 1000}}}, "input_var": "j2", "op": "filter", "result_var": "f2"},
		{"args": map[string]any{"transaction": "tx"}, "op": "commit_tx"},
		{"args": map[string]any{"value": map[string]any{"$var": "f2"}}, "op": "return"},
	}

	scriptBytes, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("marshal script: %v", err)
	}

	ctxWithPayload := context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db"})
	resultRaw, err := agent.toolExecuteScript(ctxWithPayload, map[string]any{"script": string(scriptBytes)})
	if err != nil {
		t.Fatalf("toolExecuteScript failed: %v", err)
	}
	result, err := formatToolResult(ctxWithPayload, resultRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	if !strings.Contains(result, "John") {
		t.Fatalf("expected joined result to contain John, got %s", result)
	}
	if !strings.Contains(result, "1500") {
		t.Fatalf("expected joined result to contain matching order total, got %s", result)
	}
	if strings.Contains(result, "100") {
		t.Fatalf("expected low-value order to be filtered out, got %s", result)
	}
}

func TestExecuteScript_ExplicitCommitMaterializesJoinChainWithoutReturn(t *testing.T) {
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}

	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin setup tx: %v", err)
	}

	users, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users", tx, nil)
	users.Add(ctx, "u1", map[string]any{"first_name": "John", "last_name": "Jones"})
	users.Add(ctx, "u2", map[string]any{"first_name": "Jane", "last_name": "Doe"})

	usersOrders, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users_orders", tx, nil)
	usersOrders.Add(ctx, "u1", "o1")
	usersOrders.Add(ctx, "u2", "o2")

	orders, _ := core_database.NewBtree[any, any](ctx, dbOpts, "orders", tx, nil)
	orders.Add(ctx, "o1", map[string]any{"total_amount": 831, "order_date": "2026-01-01"})
	orders.Add(ctx, "o2", map[string]any{"total_amount": 100, "order_date": "2026-01-02"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit setup tx: %v", err)
	}

	agent := &CopilotAgent{
		systemDB: sysDB,
		databases: map[string]sop.DatabaseOptions{
			"dev_db": dbOpts,
		},
		StoreOpener: func(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (jsondb.StoreAccessor, error) {
			s, _ := core_database.NewBtree[any, any](ctx, dbOpts, storeName, tx, nil)
			return &testStoreWrapper{s}, nil
		},
	}

	script := []map[string]any{
		{"args": map[string]any{"database": "dev_db", "mode": "read"}, "op": "begin_tx", "result_var": "tx"},
		{"args": map[string]any{"name": "users", "transaction": "tx"}, "op": "open_store", "result_var": "users_store"},
		{"args": map[string]any{"name": "users_orders", "transaction": "tx"}, "op": "open_store", "result_var": "users_orders_store"},
		{"args": map[string]any{"name": "orders", "transaction": "tx"}, "op": "open_store", "result_var": "orders_store"},
		{"args": map[string]any{"store": "users_store", "stream": true}, "op": "scan", "result_var": "users_stream"},
		{"args": map[string]any{"condition": map[string]any{"first_name": map[string]any{"$eq": "John"}}}, "input_var": "users_stream", "op": "filter", "result_var": "matched_users"},
		{"args": map[string]any{"on": map[string]any{"users.key": "key"}, "store": "users_orders_store", "type": "inner"}, "input_var": "matched_users", "op": "join", "result_var": "user_order_links"},
		{"args": map[string]any{"on": map[string]any{"users_orders.value": "key"}, "store": "orders_store", "type": "inner"}, "input_var": "user_order_links", "op": "join", "result_var": "joined_orders"},
		{"args": map[string]any{"condition": map[string]any{"orders.total_amount": map[string]any{"$gt": 500}}}, "input_var": "joined_orders", "op": "filter", "result_var": "filtered_orders"},
		{"args": map[string]any{"transaction": "tx"}, "op": "commit_tx"},
	}

	scriptBytes, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("marshal script: %v", err)
	}

	ctxWithPayload := context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db"})
	resultRaw, err := agent.toolExecuteScript(ctxWithPayload, map[string]any{"script": string(scriptBytes)})
	if err != nil {
		t.Fatalf("toolExecuteScript failed: %v", err)
	}
	result, err := formatToolResult(ctxWithPayload, resultRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	if !strings.Contains(result, "John") {
		t.Fatalf("expected explicit commit result to contain John, got %s", result)
	}
	if !strings.Contains(result, "831") {
		t.Fatalf("expected explicit commit result to contain matching order total, got %s", result)
	}
	if strings.Contains(result, "100") {
		t.Fatalf("expected low-value order to be filtered out, got %s", result)
	}
}

func TestExecuteScript_ImplicitFilteredCursorSurvivesExplicitCommit(t *testing.T) {
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}

	sysDB := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, err := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin setup tx: %v", err)
	}

	users, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users", tx, nil)
	users.Add(ctx, "u1", map[string]any{"first_name": "John", "last_name": "Jones", "age": 30})
	users.Add(ctx, "u2", map[string]any{"first_name": "Jane", "last_name": "Doe", "age": 25})

	usersOrders, _ := core_database.NewBtree[any, any](ctx, dbOpts, "users_orders", tx, nil)
	usersOrders.Add(ctx, "u1", "o1")

	orders, _ := core_database.NewBtree[any, any](ctx, dbOpts, "orders", tx, nil)
	orders.Add(ctx, "o1", map[string]any{"total_amount": 831, "order_date": "2026-01-01"})
	orders.Add(ctx, "o2", map[string]any{"total_amount": 100, "order_date": "2026-01-02"})

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit setup tx: %v", err)
	}

	agent := &CopilotAgent{
		systemDB: sysDB,
		databases: map[string]sop.DatabaseOptions{
			"dev_db": dbOpts,
		},
		StoreOpener: func(ctx context.Context, dbOpts sop.DatabaseOptions, storeName string, tx sop.Transaction) (jsondb.StoreAccessor, error) {
			s, _ := core_database.NewBtree[any, any](ctx, dbOpts, storeName, tx, nil)
			return &testStoreWrapper{s}, nil
		},
	}

	script := []map[string]any{
		{"args": map[string]any{"mode": "read"}, "op": "begin_tx", "result_var": "tx"},
		{"args": map[string]any{"name": "users", "transaction": "tx"}, "op": "open_store", "result_var": "users"},
		{"args": map[string]any{"name": "users_orders", "transaction": "tx"}, "op": "open_store", "result_var": "users_orders"},
		{"args": map[string]any{"name": "orders", "transaction": "tx"}, "op": "open_store", "result_var": "orders"},
		{"args": map[string]any{"store": "users"}, "op": "scan", "result_var": "s1"},
		{"args": map[string]any{"condition": map[string]any{"first_name": map[string]any{"$eq": "John"}}}, "input_var": "s1", "op": "filter", "result_var": "f1"},
		{"args": map[string]any{"on": map[string]any{"users.key": "key"}, "store": "users_orders", "type": "inner", "left_alias": "users"}, "input_var": "f1", "op": "join", "result_var": "j1"},
		{"args": map[string]any{"on": map[string]any{"users_orders.value": "key"}, "store": "orders", "type": "inner"}, "input_var": "j1", "op": "join", "result_var": "j2"},
		{"args": map[string]any{"condition": map[string]any{"orders.total_amount": map[string]any{"$gt": 500}}}, "input_var": "j2", "op": "filter", "result_var": "f2"},
		{"args": map[string]any{"transaction": "tx"}, "op": "commit_tx"},
	}

	scriptBytes, err := json.Marshal(script)
	if err != nil {
		t.Fatalf("marshal script: %v", err)
	}

	ctxWithPayload := context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db"})
	resultRaw, err := agent.toolExecuteScript(ctxWithPayload, map[string]any{"script": string(scriptBytes)})
	if err != nil {
		t.Fatalf("toolExecuteScript failed: %v", err)
	}
	result, err := formatToolResult(ctxWithPayload, resultRaw)
	if err != nil {
		t.Fatalf("formatToolResult failed: %v", err)
	}

	if !strings.Contains(result, "John") {
		t.Fatalf("expected explicit commit result to contain John, got %s", result)
	}
	if !strings.Contains(result, "831") {
		t.Fatalf("expected explicit commit result to contain matching order total, got %s", result)
	}
	if strings.Contains(result, "100") {
		t.Fatalf("expected low-value order to stay filtered out, got %s", result)
	}
}
