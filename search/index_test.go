package search

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
)

func TestIndex_AddAndSearch(t *testing.T) {
	ctx := context.Background()
	trans, err := infs.NewTransaction(ctx, sop.TransactionOptions{
		StoresFolders: []string{"test_search_trans"},
		Mode:          sop.ForWriting,
		CacheType:     sop.InMemory,
	})
	if err != nil {
		t.Fatalf("Failed to create transaction: %v", err)
	}
	if err := trans.Begin(ctx); err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}
	defer trans.Rollback(ctx)

	idx, err := NewIndex(ctx, sop.DatabaseOptions{StoresFolders: []string{"test_search_trans"}}, trans, "test_index")
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Add documents
	docs := map[string]string{
		"doc1": "the quick brown fox jumps over the lazy dog",
		"doc2": "the quick brown fox",
		"doc3": "jumps over the lazy dog",
		"doc4": "programming in go is fun",
	}

	for id, text := range docs {
		if err := idx.Add(ctx, id, text); err != nil {
			t.Fatalf("Failed to add document %s: %v", id, err)
		}
	}

	// Search for "fox"
	results, err := idx.Search(ctx, "fox")
	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}

	if len(results) == 0 {
		t.Errorf("Expected results for 'fox', got none")
	}

	// Check if doc1 and doc2 are in results
	foundDoc1 := false
	foundDoc2 := false
	for _, r := range results {
		if r.DocID == "doc1" {
			foundDoc1 = true
		}
		if r.DocID == "doc2" {
			foundDoc2 = true
		}
	}

	if !foundDoc1 {
		t.Errorf("Expected doc1 in results")
	}
	if !foundDoc2 {
		t.Errorf("Expected doc2 in results")
	}

	// Search for "go"
	results, err = idx.Search(ctx, "go")
	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}

	if len(results) == 0 {
		t.Errorf("Expected results for 'go', got none")
	}
	if results[0].DocID != "doc4" {
		t.Errorf("Expected doc4 to be top result for 'go', got %s", results[0].DocID)
	}
}
