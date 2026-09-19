package agent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
)

func TestToolFetch_ReturnsTerminalEnvelopeForMissingKeyInNativeLoop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sop_fetch_terminal_hint_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbName := "test_db_fetch_terminal_hint"
	opts := sop.DatabaseOptions{StoresFolders: []string{tmpDir}}
	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{dbName: opts}, nil)
	ctx := context.Background()
	agent.Open(ctx)

	db := database.NewDatabase(opts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	store, err := jsondb.CreateObjectStore(ctx, opts, "users", tx)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("CreateObjectStore failed: %v", err)
	}
	if _, err := store.Add(ctx, "u1", map[string]any{"first_name": "John"}); err != nil {
		tx.Rollback(ctx)
		t.Fatalf("Add failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	readPayload := &ai.SessionPayload{CurrentDB: dbName}
	readTx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction read failed: %v", err)
	}
	defer readTx.Rollback(ctx)
	readPayload.Transaction = readTx
	ctx = context.WithValue(ctx, SessionPayloadKey, readPayload)
	ctx = context.WithValue(ctx, ai.CtxKeyNativeToolHints, true)

	res, err := agent.toolFetch(ctx, map[string]any{"store": "users", "key": "missing"})
	if err != nil {
		t.Fatalf("toolFetch failed: %v", err)
	}

	assertTerminalEnvelope(t, res, "terminal_error", "Key not found: missing")
}

func TestToolUpdate_ReturnsTerminalEnvelopeForMissingItemInNativeLoop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sop_update_terminal_hint_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbName := "test_db_update_terminal_hint"
	opts := sop.DatabaseOptions{StoresFolders: []string{tmpDir}}
	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{dbName: opts}, nil)
	ctx := context.Background()
	agent.Open(ctx)

	db := database.NewDatabase(opts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	_, err = jsondb.CreateObjectStore(ctx, opts, "users", tx)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("CreateObjectStore failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	writePayload := &ai.SessionPayload{CurrentDB: dbName}
	writeTx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction write failed: %v", err)
	}
	defer writeTx.Rollback(ctx)
	writePayload.Transaction = writeTx
	ctx = context.WithValue(ctx, SessionPayloadKey, writePayload)
	ctx = context.WithValue(ctx, ai.CtxKeyNativeToolHints, true)

	res, err := agent.toolUpdate(ctx, map[string]any{
		"store": "users",
		"key":   "missing",
		"value": map[string]any{"first_name": "Jane"},
	})
	if err != nil {
		t.Fatalf("toolUpdate failed: %v", err)
	}

	assertTerminalEnvelope(t, res, "terminal_error", "Item with key 'missing' not found in store 'users'")
}

func TestToolDelete_ReturnsTerminalEnvelopeForMissingItemInNativeLoop(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sop_delete_terminal_hint_test")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbName := "test_db_delete_terminal_hint"
	opts := sop.DatabaseOptions{StoresFolders: []string{tmpDir}}
	agent := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{dbName: opts}, nil)
	ctx := context.Background()
	agent.Open(ctx)

	db := database.NewDatabase(opts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	_, err = jsondb.CreateObjectStore(ctx, opts, "users", tx)
	if err != nil {
		tx.Rollback(ctx)
		t.Fatalf("CreateObjectStore failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	writePayload := &ai.SessionPayload{CurrentDB: dbName}
	writeTx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction write failed: %v", err)
	}
	defer writeTx.Rollback(ctx)
	writePayload.Transaction = writeTx
	ctx = context.WithValue(ctx, SessionPayloadKey, writePayload)
	ctx = context.WithValue(ctx, ai.CtxKeyNativeToolHints, true)

	res, err := agent.toolDelete(ctx, map[string]any{"store": "users", "key": "missing"})
	if err != nil {
		t.Fatalf("toolDelete failed: %v", err)
	}

	assertTerminalEnvelope(t, res, "terminal_error", "Item 'missing' not found in store 'users'")
}

func assertTerminalEnvelope(t *testing.T, raw string, wantStatus string, wantText string) {
	t.Helper()
	var envelope ai.ToolResultEnvelope
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		t.Fatalf("expected terminal envelope, got %q: %v", raw, err)
	}
	if envelope.ProgressHint == nil || envelope.ProgressHint.Status != wantStatus {
		t.Fatalf("expected terminal status %q, got %+v", wantStatus, envelope.ProgressHint)
	}
	if !strings.Contains(string(envelope.ToolResult), wantText) {
		t.Fatalf("expected tool_result to contain %q, got %s", wantText, string(envelope.ToolResult))
	}
	if len(envelope.ProgressHint.Tips) == 0 {
		t.Fatalf("expected terminal hint to include a retry-stop tip, got %+v", envelope.ProgressHint)
	}
}
