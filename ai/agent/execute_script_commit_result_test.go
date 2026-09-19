package agent_test

import (
	"context"
	"encoding/json"
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

func TestExecuteScript_CommitDoesNotClobberSortedResult(t *testing.T) {
	dbPath := "/tmp/test_commit_result_sop"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{StoresFolders: []string{dbPath}}
	db := database.NewDatabase(dbOpts)
	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("begin write tx: %v", err)
	}

	store, err := jsondb.NewJsonBtreeMapKey(ctx, db.Options(), sop.StoreOptions{Name: "users"}, tx, "")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	items := []jsondb.Item[map[string]any, any]{
		{Key: map[string]any{"id": "u1"}, Value: ptrAny(map[string]any{"name": "Alice", "age": 20}), ID: uuid.New()},
		{Key: map[string]any{"id": "u2"}, Value: ptrAny(map[string]any{"name": "Bob", "age": 35}), ID: uuid.New()},
	}
	if _, err := store.Add(ctx, items); err != nil {
		t.Fatalf("seed store: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit seed tx: %v", err)
	}

	adminAgent := agent.NewCopilotAgent(agent.Config{Verbose: true}, map[string]sop.DatabaseOptions{"dev_db": dbOpts}, database.NewDatabase(sop.DatabaseOptions{StoresFolders: []string{"/tmp/sysdb_commit_result"}}))
	defer os.RemoveAll("/tmp/sysdb_commit_result")

	execCtx := context.WithValue(ctx, agent.SessionPayloadKey, &ai.SessionPayload{CurrentDB: "dev_db"})
	if err := adminAgent.Open(execCtx); err != nil {
		t.Fatalf("open agent: %v", err)
	}

	scriptSteps := []map[string]any{
		{"op": "begin_tx", "args": map[string]any{"database": "dev_db", "mode": "read"}, "result_var": "tx1"},
		{"op": "open_store", "args": map[string]any{"name": "users", "transaction": "tx1"}, "result_var": "users_store"},
		{"op": "scan", "args": map[string]any{"store": "users_store"}, "result_var": "scan_result"},
		{"op": "sort", "input_var": "scan_result", "args": map[string]any{"field": "age", "descending": true}, "result_var": "sorted_result"},
		{"op": "commit_tx", "args": map[string]any{"transaction": "tx1"}},
	}

	scriptJSON, err := json.Marshal(scriptSteps)
	if err != nil {
		t.Fatalf("marshal script: %v", err)
	}

	resStr, err := adminAgent.Execute(execCtx, "execute_script", map[string]any{"script": string(scriptJSON)})
	if err != nil {
		t.Fatalf("execute script: %v", err)
	}

	if len(resStr) == 0 || resStr[0] != '[' {
		t.Fatalf("expected list JSON result, got %q", resStr)
	}
	if !containsOrdered(resStr, "Bob", "Alice") {
		t.Fatalf("expected descending age order in result, got %s", resStr)
	}
	if containsOrdered(resStr, "users_store", "Bob") {
		t.Fatalf("unexpected store handle-like result, got %s", resStr)
	}
}

func containsOrdered(s, first, second string) bool {
	firstIdx := strings.Index(s, first)
	secondIdx := strings.Index(s, second)
	return firstIdx >= 0 && secondIdx >= 0 && firstIdx < secondIdx
}

func ptrAny(v any) *any {
	value := v
	return &value
}
