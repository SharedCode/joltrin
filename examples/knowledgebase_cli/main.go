package main

import (
	"context"
	"fmt"
	"os"

	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/embed"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

func main() {
	ctx := context.Background()
	if err := os.RemoveAll("./data/demo-cli-kb"); err != nil {
		panic(err)
	}

	embedder, err := embed.NewKelindarEmbedder("kelindar", 0)
	if err != nil {
		panic(err)
	}

	db := database.NewDatabase(sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./data/demo-cli-kb"},
	})

	kb, err := database.NewKnowledgeBase(ctx, "demo-cli-kb", sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./data/demo-cli-kb"},
	}, nil, embedder, false)
	if err != nil {
		panic(err)
	}
	if err := kb.UpsertCategories(ctx, []memory.UpsertCategoryParam{{
		Category: &memory.Category{ID: sop.NewUUID(), Name: "Root", Path: "Root"},
	}}); err != nil {
		panic(err)
	}
	if err := kb.UpsertCategories(ctx, []memory.UpsertCategoryParam{{
		Category:    &memory.Category{ID: sop.NewUUID(), Name: "Engineering", Path: "Root/Engineering"},
		ParentPaths: []string{"Root"},
	}}); err != nil {
		panic(err)
	}

	if err := kb.UpsertItems(ctx, []memory.UpsertItemParam[map[string]any]{{
		CategoryPath: "Root/Engineering",
		Item: &memory.Item[map[string]any]{
			Data:      map[string]any{"text": "Use KnowledgeBase for semantic memory"},
			Summaries: []string{"semantic memory"},
		},
	}}); err != nil {
		panic(err)
	}

	if err := kb.Close(ctx); err != nil {
		panic(err)
	}

	fmt.Println("2) Vectorize categories and items with the real embedder")
	if err := db.Vectorize(ctx, "demo-cli-kb", nil, embedder, 10); err != nil {
		panic(err)
	}

	kb, err = database.NewKnowledgeBase(ctx, "demo-cli-kb", sop.DatabaseOptions{
		Type:          sop.Standalone,
		StoresFolders: []string{"./data/demo-cli-kb"},
	}, nil, embedder, false)
	if err != nil {
		panic(err)
	}

	fmt.Println("3) Query a simpler, semantically related path")
	results, err := kb.SearchByPath(ctx, []memory.PathSearchParam{{CategoryPath: "Root/Knowledge", SearchText: "semantic"}})
	if err != nil {
		panic(err)
	}
	if len(results) == 0 {
		fmt.Println("No semantic match")
		return
	}
	fmt.Printf("Semantic match: %v\n", results[0].Data)
}
