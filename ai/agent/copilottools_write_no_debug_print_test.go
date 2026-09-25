package agent

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/jsondb"
)

// TestToolUpdate_ExternalTxPath_NoStdoutDebugPrint is the regression test for
// the live fmt.Printf that was left in toolUpdate's non-stub, external
// transaction path (localTx=false). Before the fix, calling toolUpdate with
// an existing caller-managed transaction wrote:
//
//	"DEBUG: toolAdd finishing. localTx=false. HasBegun=<bool>"
//
// to stdout on every update, leaking internal transaction state to process
// output in production. The test confirms that stdout is silent for a
// successful external-tx update so that the print cannot reappear as a
// regression.
func TestToolUpdate_ExternalTxPath_NoStdoutDebugPrint(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sop_update_no_debug_print")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbName := "test_db_no_debug_print"
	opts := sop.DatabaseOptions{StoresFolders: []string{tmpDir}}
	ag := NewCopilotAgent(Config{}, map[string]sop.DatabaseOptions{dbName: opts}, nil)
	ctx := context.Background()
	ag.Open(ctx)

	db := database.NewDatabase(opts)

	// Set up: create a store and insert the item we'll update.
	setupTx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction (setup): %v", err)
	}
	store, err := jsondb.CreateObjectStore(ctx, opts, "widgets", setupTx)
	if err != nil {
		setupTx.Rollback(ctx)
		t.Fatalf("CreateObjectStore: %v", err)
	}
	if _, err := store.Add(ctx, "w1", map[string]any{"name": "Widget One"}); err != nil {
		setupTx.Rollback(ctx)
		t.Fatalf("Add: %v", err)
	}
	if err := setupTx.Commit(ctx); err != nil {
		t.Fatalf("Commit (setup): %v", err)
	}

	// Begin an external transaction that toolUpdate will use (localTx=false).
	extTx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction (external): %v", err)
	}
	defer extTx.Rollback(ctx)

	payload := &ai.SessionPayload{
		CurrentDB:   dbName,
		Transaction: extTx,
	}
	callCtx := context.WithValue(ctx, SessionPayloadKey, payload)

	// Capture stdout for the duration of the toolUpdate call.
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w

	_, callErr := ag.toolUpdate(callCtx, map[string]any{
		"store": "widgets",
		"key":   "w1",
		"value": map[string]any{"name": "Widget One Updated"},
	})

	// Close the write end first so the read below terminates.
	w.Close()
	os.Stdout = origStdout

	var buf bytes.Buffer
	io.Copy(&buf, r) //nolint:errcheck
	r.Close()

	if callErr != nil {
		t.Fatalf("toolUpdate returned error: %v", callErr)
	}

	captured := buf.String()
	if captured != "" {
		t.Errorf("toolUpdate wrote %q to stdout, expected no output (debug print regression)", captured)
	}
}
