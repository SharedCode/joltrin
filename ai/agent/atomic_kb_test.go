package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
)

func ensureHandler(path string, h http.HandlerFunc) {
	defer func() { recover() }()
	http.HandleFunc(path, h)
}

func setupTestDB(t *testing.T) (*database.Database, *ScriptEngine) {
	ctx := context.Background()
	dbOpt := sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{t.TempDir()},
		CacheType:     sop.InMemory,
	}
	db := database.NewDatabase(dbOpt)

	e := &ScriptEngine{
		ResolveDatabase: func(name string) (Database, error) {
			return db, nil
		},
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	kb, err := db.OpenKnowledgeBase(ctx, "sop", tx, nil, nil, false, false)
	if err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}

	cat1ID, _ := sop.ParseUUID("00000000-0000-0000-0000-000000000001")
	err = kb.UpsertCategories(ctx, []memory.UpsertCategoryParam{
		{
			Category: &memory.Category{
				ID:   cat1ID,
				Name: "MockCategory",
			},
		},
	})
	if err != nil {
		t.Fatalf("UpsertCategories failed: %v", err)
	}

	itemID := sop.NewUUID()
	err = kb.UpsertItems(ctx, []memory.UpsertItemParam[map[string]any]{
		{
			CategoryID: cat1ID,
			Item: &memory.Item[map[string]any]{
				ID:         itemID,
				CategoryID: cat1ID,
				Data:       map[string]any{"name": "MockItem1"},
			},
		},
	})
	if err != nil {
		t.Fatalf("UpsertItems failed: %v", err)
	}

	tx.Commit(ctx)

	return db, e
}

func TestAtomicKBOperations(t *testing.T) {
	ensureHandler("/api/spaces/vectorize", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"task_id": "test-task"})
	})
	ensureHandler("/api/spaces/item/add", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "added"})
	})
	ensureHandler("/api/store/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "total": 0})
	})
	ensureHandler("/api/spaces/categories", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"status": "deleted"})
	})

	_, engine := setupTestDB(t)

	// In atomic_kb.go, p = ai.GetSessionPayload(ctx), so we must inject it into context
	payload := &ai.SessionPayload{
		CurrentDB: "testdb",
		AgentID:   "omni", // to default to "sop" workspace
	}
	ctx := context.WithValue(context.Background(), SessionPayloadKey, payload)

	tests := []struct {
		op          string
		args        map[string]any
		expectError bool
	}{
		{"list_space_categories", map[string]any{}, false},
		{"list_space_items", map[string]any{"category": "MockCategory"}, false},
		{"search_space", map[string]any{"query": "test"}, false},
		{"vectorize_space", map[string]any{}, false},
		{"vectorize_space_categories", map[string]any{"categories": []any{"MockCategory"}}, false},
		{"vectorize_space_items", map[string]any{"category": "MockCategory", "item_names": []any{"MockItem1", "MockItem2"}}, false},
		{"delete_space_items", map[string]any{"items": []any{
			map[string]any{"category": "MockCategory", "item_name": "MockItem1"},
		}}, false},
		{"delete_space_categories", map[string]any{"categories": []any{"MockCategory"}}, false},
		{"upsert_space_items", map[string]any{"items": []any{
			map[string]any{"category": "MockCategory", "item_name": "MockItem2", "content": "Some desc"},
		}}, false},
	}

	for _, tc := range tests {
		t.Run(tc.op, func(t *testing.T) {
			_, err := engine.ExecuteKBManagement(ctx, tc.op, tc.args, nil)
			if tc.expectError && err == nil {
				t.Fatalf("expected error for %s but got nil", tc.op)
			}
			if !tc.expectError && err != nil {
				t.Fatalf("unexpected error for %s: %v", tc.op, err)
			}
		})
	}
}
