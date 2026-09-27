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

// TestCaseSensitivityInSearch tests if Search() is case-sensitive for CategoryPath
func TestCaseSensitivityInSearch(t *testing.T) {
	ctx := context.Background()
	storagePath := "../../tools/config.json"
	cfg := requireKnowledgeBaseFixture(t, storagePath)

	dbOpts, err := core.GetOptions(ctx, cfg.SystemDB.Path)
	if err != nil {
		t.Fatalf("GetOptions failed: %v", err)
	}

	db := database.NewDatabase(dbOpts)

	testCases := []struct {
		name string
		path string
	}{
		{"Exact case", "Language Bindings/C#"},
		{"Lowercase", "language bindings/c#"},
		{"UPPERCASE", "LANGUAGE BINDINGS/C#"},
		{"Mixed", "language Bindings/c#"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tx, err := db.BeginTransaction(ctx, sop.ForReading)
			if err != nil {
				t.Fatalf("BeginTransaction failed: %v", err)
			}
			defer tx.Rollback(ctx)

			kb, err := db.OpenKnowledgeBase(ctx, "sop", tx, nil, nil, false)
			if err != nil {
				t.Fatalf("OpenKnowledgeBase failed: %v", err)
			}

			if err = kb.Initialize(ctx); err != nil {
				t.Fatalf("Initialize failed: %v", err)
			}

			req := []memory.SearchRequest[map[string]any]{{CategoryPath: tc.path, Limit: 5}}
			hits, err := kb.Search(ctx, req)
			if err != nil {
				t.Fatalf("Search failed: %v", err)
			}

			if len(hits) > 0 && len(hits[0]) > 0 {
				fmt.Printf("%s: Found %d hits, first score: %.4f\n", tc.name, len(hits[0]), hits[0][0].Score)
				// Show first 3 hit categories
				for i, hit := range hits[0] {
					if i >= 3 {
						break
					}
					catPath := ""
					if cat, ok := hit.Payload["category"].(string); ok {
						catPath = cat
					}
					fmt.Printf("  Hit %d: category=%q score=%.4f\n", i+1, catPath, hit.Score)
				}
			} else {
				fmt.Printf("%s: NO HITS (path: %q)\n", tc.name, tc.path)
			}
		})
	}
}
