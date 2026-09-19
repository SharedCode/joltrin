package agent

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	core_database "github.com/sharedcode/joltrin/database"
)

func TestRepro_SelectMissingHeaders(t *testing.T) {
	// 1. Setup Temp DB
	tmpDir := t.TempDir()
	dbOpts := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	}
	sysDB := database.NewDatabase(dbOpts)

	// 2. Create Store with Data
	ctx := context.Background()
	tx, _ := core_database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	b3, _ := core_database.NewBtree[string, any](ctx, dbOpts, "orders", tx, nil)
	b3.Add(ctx, "ord1", map[string]any{"total_amount": 1500, "status": "Pending"})
	tx.Commit(ctx)

	// 3. Setup Agent
	dbs := map[string]sop.DatabaseOptions{filepath.Base(tmpDir): dbOpts}
	// mockGen := &MockScriptedGenerator{}

	// adminAgent definition
	adminAgent := &CopilotAgent{
		Config:    Config{ID: "copilot"},
		databases: dbs,
		systemDB:  sysDB,
	}

	// 4. Run Script Step directly (simulating PlayScript internals)
	var buf bytes.Buffer
	js := NewJSONStreamer(&buf)

	// Inject into Context
	payload := &ai.SessionPayload{
		CurrentDB: filepath.Base(tmpDir),
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, payload)
	ctx = context.WithValue(ctx, CtxKeyJSONStreamer, js)

	args := map[string]any{
		"store":    "orders",
		"database": filepath.Base(tmpDir),
		// No fields specified -> Implicit Select *
	}

	// Create StepStreamer manually
	ss := js.StartStreamingStep("command", "select", "", 1)
	ctx = context.WithValue(ctx, ai.CtxKeyResultStreamer, ss)

	// Call toolSelect directly
	_, err := adminAgent.toolSelect(ctx, args)
	if err != nil {
		t.Fatalf("toolSelect failed: %v", err)
	}

	ss.Close()

	output := buf.String()
	t.Logf("Output: %s", output)

	if strings.Contains(output, "columns") {
		t.Log("Headers found!")
	} else {
		t.Log("No headers found in output.")
		t.Fail()
	}
}

func TestRepro_SelectMissingHeaders_WithSuppression(t *testing.T) {
	// Setup simple streamer test without full database
	var buf bytes.Buffer
	js := NewJSONStreamer(&buf)
	js.SetSuppressStepStart(true)

	step := js.StartStreamingStep("record", "select *", "select", 1)

	// Write item, should auto-detect columns
	item := map[string]any{"id": 1, "name": "test"}
	step.WriteItem(item)
	step.EndArray()
	step.Close()

	output := buf.String()
	t.Logf("Output: %s", output)

	if !strings.Contains(output, `"metadata"`) {
		t.Errorf("Expected metadata in output, got: %s", output)
	}
	if !strings.Contains(output, `"columns"`) {
		t.Errorf("Expected columns in metadata, got: %s", output)
	}
}
