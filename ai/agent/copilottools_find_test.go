package agent

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	sopdb "github.com/sharedcode/joltrin/database"
	"github.com/sharedcode/joltrin/jsondb"
)

func TestToolFind_ClosestItem(t *testing.T) {
	// 1. Setup
	ctx := context.Background()
	dbPath := "test_copilot_find"
	os.RemoveAll(dbPath)
	defer os.RemoveAll(dbPath)

	dbOpts := sop.DatabaseOptions{
		StoresFolders: []string{dbPath},
		CacheType:     sop.InMemory,
	}

	// Create DB and Store
	db := database.NewDatabase(dbOpts)
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	storeName := "fruits"
	// Create the store first
	storeOpts := sop.StoreOptions{
		Name:           storeName,
		SlotLength:     10,
		IsPrimitiveKey: true,
	}
	if _, err := sopdb.NewBtree[string, any](ctx, dbOpts, storeName, tx, nil, storeOpts); err != nil {
		t.Fatalf("NewBtree failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit creation failed: %v", err)
	}

	// Start new transaction for population
	tx, err = db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction population failed: %v", err)
	}

	store, err := jsondb.OpenStore(ctx, dbOpts, storeName, tx)
	if err != nil {
		t.Fatalf("OpenStore failed: %v", err)
	}

	// Add items
	items := map[string]string{
		"apple":  "red",
		"banana": "yellow",
		"cherry": "red",
	}
	for k, v := range items {
		if _, err := store.Add(ctx, k, v); err != nil {
			t.Fatalf("Add failed: %v", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// 2. Prepare Agent
	agent := &CopilotAgent{
		databases: map[string]sop.DatabaseOptions{
			"testdb": dbOpts,
		},
	}
	// We need to mock the session payload
	sessionPayload := &ai.SessionPayload{
		CurrentDB: "testdb",
	}
	ctx = context.WithValue(ctx, SessionPayloadKey, sessionPayload)

	// 3. Test Cases
	tests := []struct {
		name           string
		toolName       string
		searchKey      string
		expectedSubstr string
	}{
		{
			name:           "Exact Match (find)",
			toolName:       "find",
			searchKey:      "banana",
			expectedSubstr: `[{"key":"banana","value":"yellow"}]`,
		},
		{
			name:           "No Match (find)",
			toolName:       "find",
			searchKey:      "apricot",
			expectedSubstr: "[]",
		},
		{
			name:      "No Match - Middle (find_nearest)",
			toolName:  "find_nearest",
			searchKey: "apricot",
			// apricot < banana. Current: banana. Previous: apple.
			expectedSubstr: `[{"key":"banana","relation":"next_or_equal","value":"yellow"},{"key":"apple","relation":"previous","value":"red"}]`,
		},
		{
			name:      "No Match - End (find_nearest)",
			toolName:  "find_nearest",
			searchKey: "date",
			// date > cherry. Current: cherry (last item). Previous: banana.
			expectedSubstr: `[{"key":"cherry","relation":"next_or_equal","value":"red"},{"key":"banana","relation":"previous","value":"yellow"}]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// We need a new transaction for each read because runNavigation commits/rolls back?
			// Actually runNavigation starts a transaction if one isn't in payload.
			// But we want to reuse the DB.

			// Let's clear transaction in payload so runNavigation starts a new one
			// But we need to update the context with the modified payload?
			// context.WithValue returns a new context.
			// The payload pointer is shared if we modify the struct.
			sessionPayload.Transaction = nil

			args := map[string]any{
				"store": storeName,
				"key":   tt.searchKey,
			}

			var result string
			var err error
			if tt.toolName == "find_nearest" {
				result, err = agent.toolFindNearest(ctx, args)
			} else {
				result, err = agent.toolFind(ctx, args)
			}

			if err != nil {
				t.Fatalf("tool failed: %v", err)
			}

			if !strings.Contains(result, tt.expectedSubstr) {
				t.Errorf("expected result to contain %q, got %q", tt.expectedSubstr, result)
			}
		})
	}
}
