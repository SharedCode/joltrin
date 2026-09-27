package confighub

import (
	"context"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
	core "github.com/sharedcode/joltrin/v5/database"
)

func TestKB_BasicSearch(t *testing.T) {
	ctx := context.Background()
	storagePath := "../../tools/config.json"
	cfg := requireKnowledgeBaseFixture(t, storagePath)
	if len(cfg.Databases) == 0 {
		t.Fatal("LoadConfig returned no databases")
	}

	dbOpts, err := core.GetOptions(ctx, cfg.SystemDB.Path)
	if err != nil {
		t.Fatalf("GetOptions(%s) failed: %v", cfg.Databases[0].Path, err)
	}

	db := database.NewDatabase(dbOpts)

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	kb, err := db.OpenKnowledgeBase(ctx, "sop", tx, nil, nil, false)
	if err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}

	if err = kb.Initialize(ctx); err != nil {
		t.Fatalf("KnowledgeBase.Initialize failed: %v", err)
	}

	req := []memory.SearchRequest[map[string]any]{{CategoryPath: "Language Bindings/C#", Limit: 5}}
	hits, err := kb.Search(ctx, req)
	if err != nil {
		t.Fatalf("KnowledgeBase.Search failed: %v", err)
	}

	if len(hits) == 0 {
		t.Fatalf("KnowledgeBase.Search expected hits for category path %q", req[0].CategoryPath)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	fmt.Printf("hits: %v\n", hits)
}
