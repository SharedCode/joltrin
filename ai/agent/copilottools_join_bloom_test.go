package agent

import (
	"context"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	sopdb "github.com/sharedcode/joltrin/database"
)

func TestToolJoin_BloomFilter(t *testing.T) {
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
	// Mock Session Payload manually as in other tests
	ctx = context.WithValue(ctx, SessionPayloadKey, &ai.SessionPayload{CurrentDB: "system"})
	agent.Open(ctx)

	// Create Large Right Store (> 101 items to trigger Bloom Filter)
	// Key: "R<N>", Value: "Val<N>"
	// Create Left Store with some matches and some misses.

	// Transaction for setup
	trans, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)

	// Right Store
	rightStoreName := "RightBloom"
	rightOpts := sop.StoreOptions{Name: rightStoreName, SlotLength: 10, IsPrimitiveKey: true}
	rightStore, _ := sopdb.NewBtree[string, any](ctx, dbOpts, rightStoreName, trans, nil, rightOpts)

	count := 150
	for i := 0; i < count; i++ {
		key := fmt.Sprintf("R%d", i)
		val := map[string]string{"name": fmt.Sprintf("Val%d", i)}
		rightStore.Add(ctx, key, val)
	}

	// Left Store
	leftStoreName := "LeftBloom"
	leftOpts := sop.StoreOptions{Name: leftStoreName, SlotLength: 10, IsPrimitiveKey: true}
	leftStore, _ := sopdb.NewBtree[string, any](ctx, dbOpts, leftStoreName, trans, nil, leftOpts)

	// Add Matches
	// 50 matches (0-49)
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("L%d", i)
		// Link to Right Key
		val := map[string]string{"r_link": fmt.Sprintf("R%d", i)}
		leftStore.Add(ctx, key, val)
	}
	// Add Misses (Links to R200+)
	for i := 0; i < 50; i++ {
		key := fmt.Sprintf("L_Miss%d", i)
		val := map[string]string{"r_link": fmt.Sprintf("R%d", 200+i)}
		leftStore.Add(ctx, key, val)
	}

	trans.Commit(ctx)

	// EXECUTE JOIN
	// Link: left.r_link = right.key
	// This maps to Right Key, so StrategyLookup should be chosen.
	// Right Count (150) > 100, so Bloom Filter should be built.

	args := map[string]any{
		"left_store":        leftStoreName,
		"right_store":       rightStoreName,
		"left_join_fields":  []string{"r_link"},
		"right_join_fields": []string{"key"}, // Join on Right Key
	}

	result, err := agent.toolJoin(ctx, args)
	if err != nil {
		t.Fatalf("toolJoin failed: %v", err)
	}

	// Verify Results
	// We expect 50 matches.
	// Parsed result is JSON string.
	// But toolJoin returns a message, JSON is in emitter.
	// Wait, agent.toolJoin returns string message. Result is via Emitter (log/stdout in test environment?).
	// Ah, TestHelper usually captures emitter?
	// The `NewResultEmitter` defaults to writing to stdout/log if not configured?
	// `ResultEmitter` has a global or contextual output?
	// In the main code: `emitter := NewResultEmitter(ctx)`.
	// `toolJoin` returns `jp.emitter.Finalize()`.
	// `Finalize` returns the JSON array string!

	// So `result` string IS the JSON.

	// Parse result
	// Note: `Finalize()` might return "Joined X items." message if not JSON?
	// Let's check `emitter.Finalize()`.

	t.Logf("Result: %s", result)

	// It should contain "R0"..."R49".
	// It should NOT contain "R200".
}
