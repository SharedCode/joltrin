package confighub

import (
	"context"
	"fmt"
	"testing"

	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
	core "github.com/sharedcode/joltrin/v5/database"
)

// TestCompareSearchVsSearchKnowledgeBase compares direct Search() vs searchKnowledgeBase()
func TestCompareSearchVsSearchKnowledgeBase(t *testing.T) {
	ctx := context.Background()
	storagePath := "../../tools/config.json"
	cfg := requireKnowledgeBaseFixture(t, storagePath)

	dbOpts, err := core.GetOptions(ctx, cfg.SystemDB.Path)
	if err != nil {
		t.Fatalf("GetOptions failed: %v", err)
	}

	db := database.NewDatabase(dbOpts)

	// Test 1: Direct Search() with exact path
	t.Run("DirectSearch", func(t *testing.T) {
		tx, err := db.BeginTransaction(ctx, sop.ForReading)
		if err != nil {
			t.Fatalf("BeginTransaction failed: %v", err)
		}
		defer tx.Rollback(ctx)

		kb, err := db.OpenKnowledgeBase(ctx, "sop", tx, nil, nil, false)
		if err != nil {
			t.Fatalf("OpenKnowledgeBase failed: %v", err)
		}

		req := []memory.SearchRequest[map[string]any]{{CategoryPath: "Language Bindings/C#", Limit: 5}}
		hits, err := kb.Search(ctx, req)
		if err != nil {
			t.Fatalf("Search failed: %v", err)
		}

		fmt.Printf("Direct Search hits: %d\n", len(hits))
		if len(hits) > 0 && len(hits[0]) > 0 {
			fmt.Printf("First hit score: %.4f\n", hits[0][0].Score)
		} else {
			t.Error("Direct Search found no hits for 'Language Bindings/C#'")
		}
	})

	// Test 2: Simulate searchKnowledgeBase path extraction
	t.Run("PathExtraction", func(t *testing.T) {
		testCases := []struct {
			query    string
			catPath  string
			category string
		}{
			{query: "Language Bindings/C#", catPath: "", category: ""},
			{query: "C#", catPath: "Language Bindings", category: ""},
			{query: "C# tutorials", catPath: "Language Bindings/C#", category: ""},
			{query: "omni:sop:language bindings/c#", catPath: "", category: ""},
		}

		for _, tc := range testCases {
			t.Run(tc.query, func(t *testing.T) {
				// Simulate what searchKnowledgeBase does
				pathQuery, _ := agent.ExportSplitCategoryPathInstruction(tc.query)
				effectivePath := pathQuery
				if effectivePath == "" {
					effectivePath = tc.catPath
				}
				if effectivePath == "" {
					effectivePath = tc.category
				}

				fmt.Printf("Query: %q\n", tc.query)
				fmt.Printf("  pathQuery extracted: %q\n", pathQuery)
				fmt.Printf("  catPath param: %q\n", tc.catPath)
				fmt.Printf("  effectivePath: %q\n", effectivePath)
				fmt.Println()
			})
		}
	})
}
