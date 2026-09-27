package search

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/infs"
	"github.com/sharedcode/joltrin/v5"
)

func TestIndex_Scoring_BM25(t *testing.T) {
	ctx := context.Background()
	trans, err := infs.NewTransaction(ctx, sop.TransactionOptions{
		StoresFolders: []string{"test_search_scoring"},
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

	idx, err := NewIndex(ctx, sop.DatabaseOptions{StoresFolders: []string{"test_search_trans"}}, trans, "test_scoring_index")
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Scenario:
	// Doc A: "apple" (Short, 1 match)
	// Doc B: "apple apple apple" (Short, 3 matches)
	// Doc C: "apple banana cherry date elderberry fig grape honeydew" (Long, 1 match)
	// Doc D: "banana cherry" (No match for apple)

	docs := map[string]string{
		"docA": "apple",
		"docB": "apple apple apple",
		"docC": "apple banana cherry date elderberry fig grape honeydew",
		"docD": "banana cherry",
	}

	for id, text := range docs {
		if err := idx.Add(ctx, id, text); err != nil {
			t.Fatalf("Failed to add document %s: %v", id, err)
		}
	}

	// Search for "apple"
	results, err := idx.Search(ctx, "apple")
	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}

	// Expectations:
	// 1. docD should NOT be in results.
	// 2. docB should be #1 (High frequency, short length).
	// 3. docA should be #2 (Low frequency, very short length).
	// 4. docC should be #3 (Low frequency, long length - penalized).

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	// Check for docD
	for _, r := range results {
		if r.DocID == "docD" {
			t.Errorf("docD should not match 'apple'")
		}
	}

	// Verify Order
	if len(results) >= 3 {
		if results[0].DocID != "docB" {
			t.Errorf("Expected docB to be first (highest score), got %s (Score: %f)", results[0].DocID, results[0].Score)
		}
		if results[1].DocID != "docA" {
			t.Errorf("Expected docA to be second, got %s (Score: %f)", results[1].DocID, results[1].Score)
		}
		if results[2].DocID != "docC" {
			t.Errorf("Expected docC to be third, got %s (Score: %f)", results[2].DocID, results[2].Score)
		}
	}
}

func TestIndex_MultiTerm_OR(t *testing.T) {
	ctx := context.Background()
	trans, err := infs.NewTransaction(ctx, sop.TransactionOptions{
		StoresFolders: []string{"test_search_multiterm"},
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

	idx, err := NewIndex(ctx, sop.DatabaseOptions{StoresFolders: []string{"test_search_trans"}}, trans, "test_multiterm_index")
	if err != nil {
		t.Fatalf("Failed to create index: %v", err)
	}

	// Doc 1: "cat"
	// Doc 2: "dog"
	// Doc 3: "cat dog"
	docs := map[string]string{
		"doc1": "cat",
		"doc2": "dog",
		"doc3": "cat dog",
	}

	for id, text := range docs {
		if err := idx.Add(ctx, id, text); err != nil {
			t.Fatalf("Failed to add document %s: %v", id, err)
		}
	}

	// Search for "cat dog"
	// Should match all 3.
	// doc3 should likely be top because it matches BOTH terms.
	results, err := idx.Search(ctx, "cat dog")
	if err != nil {
		t.Fatalf("Failed to search: %v", err)
	}

	if len(results) != 3 {
		t.Errorf("Expected 3 results, got %d", len(results))
	}

	if len(results) > 0 {
		if results[0].DocID != "doc3" {
			t.Errorf("Expected doc3 (both terms) to be top, got %s", results[0].DocID)
		}
	}
}
