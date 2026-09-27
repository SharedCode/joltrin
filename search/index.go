package search

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
	"github.com/sharedcode/joltrin/v5/database"
)

// TextSearchResult represents a scored document from text search.
type TextSearchResult struct {
	DocID string
	Score float64
}

// Index is the main entry point for the text search engine.
type Index struct {
	// B-Trees
	postings  btree.BtreeInterface[string, int] // Key: "term|docID", Value: Frequency
	termStats btree.BtreeInterface[string, int] // Key: term, Value: DocCount
	docStats  btree.BtreeInterface[string, int] // Key: docID, Value: DocLength
	global    btree.BtreeInterface[string, int] // Key: "total_docs", "total_len", Value: count

	tokenizer Tokenizer
}

// NewIndex creates or opens a text search index.
func NewIndex(ctx context.Context, config sop.DatabaseOptions, t sop.Transaction, name string) (*Index, error) {
	// We use a prefix for the store names to keep them grouped.
	// We use a larger SlotLength (btree.DefaultSlotLength) for better performance with many small items.
	postings, err := database.NewBtree[string, int](ctx, config, name+"/postings", t, nil, sop.ConfigureStore(
		name+"/postings",
		true,
		btree.DefaultSlotLength,
		"Inverted index postings (Term|DocID -> Freq)",
		sop.SmallData,
		"",
	))
	if err != nil {
		return nil, err
	}

	termStats, err := database.NewBtree[string, int](ctx, config, name+"/term_stats", t, nil, sop.ConfigureStore(
		name+"/term_stats",
		true,
		btree.DefaultSlotLength,
		"Term statistics (Term -> DocCount)",
		sop.SmallData,
		"",
	))
	if err != nil {
		return nil, err
	}

	docStats, err := database.NewBtree[string, int](ctx, config, name+"/doc_stats", t, nil, sop.ConfigureStore(
		name+"/doc_stats",
		true,
		btree.DefaultSlotLength,
		"Document statistics (DocID -> Length)",
		sop.SmallData,
		"",
	))
	if err != nil {
		return nil, err
	}

	global, err := database.NewBtree[string, int](ctx, config, name+"/global", t, nil, sop.ConfigureStore(
		name+"/global",
		true,
		btree.DefaultSlotLength,
		"Global statistics",
		sop.SmallData,
		"",
	))
	if err != nil {
		return nil, err
	}

	return &Index{
		postings:  postings,
		termStats: termStats,
		docStats:  docStats,
		global:    global,
		tokenizer: &SimpleTokenizer{},
	}, nil
}

// Add indexes a document.
func (idx *Index) Add(ctx context.Context, docID string, text string) error {
	tokens := idx.tokenizer.Tokenize(text)
	docLen := len(tokens)

	// 1. Update Document Stats
	if _, err := idx.docStats.Add(ctx, docID, docLen); err != nil {
		return err
	}

	// 2. Calculate Term Frequencies for this doc
	freqs := make(map[string]int)
	for _, token := range tokens {
		freqs[token]++
	}

	// 3. Update Postings and Term Stats
	for term, freq := range freqs {
		key := fmt.Sprintf("%s|%s", term, docID)
		if _, err := idx.postings.Add(ctx, key, freq); err != nil {
			return err
		}

		// Increment DocCount for this term
		if found, err := idx.termStats.Find(ctx, term, false); err != nil {
			return err
		} else if found {
			count, _ := idx.termStats.GetCurrentValue(ctx)
			if _, err := idx.termStats.UpdateCurrentValue(ctx, count+1); err != nil {
				return err
			}
		} else {
			if _, err := idx.termStats.Add(ctx, term, 1); err != nil {
				return err
			}
		}
	}

	// 4. Update Global Stats
	// Total Docs
	totalDocs := 0
	if found, _ := idx.global.Find(ctx, "total_docs", false); found {
		totalDocs, _ = idx.global.GetCurrentValue(ctx)
	}
	if _, err := idx.global.Upsert(ctx, "total_docs", totalDocs+1); err != nil {
		return err
	}

	// Total Length (for AvgDL)
	totalLen := 0
	if found, _ := idx.global.Find(ctx, "total_len", false); found {
		totalLen, _ = idx.global.GetCurrentValue(ctx)
	}
	if _, err := idx.global.Upsert(ctx, "total_len", totalLen+docLen); err != nil {
		return err
	}

	return nil
}

// Search performs a BM25 search.
func (idx *Index) Search(ctx context.Context, query string) ([]TextSearchResult, error) {
	tokens := idx.tokenizer.Tokenize(query)
	if len(tokens) == 0 {
		return nil, nil
	}

	// Get Global Stats
	var N float64 // Total Docs
	if found, _ := idx.global.Find(ctx, "total_docs", false); found {
		val, _ := idx.global.GetCurrentValue(ctx)
		N = float64(val)
	}
	if N == 0 {
		return nil, nil
	}

	var totalLen float64
	if found, _ := idx.global.Find(ctx, "total_len", false); found {
		val, _ := idx.global.GetCurrentValue(ctx)
		totalLen = float64(val)
	}
	avgDL := totalLen / N

	// BM25 Constants
	k1 := 1.2
	b := 0.75

	// Accumulate scores: DocID -> Score
	scores := make(map[string]float64)

	for _, term := range tokens {
		// Get Term Stats (n_q)
		var nq float64
		if found, _ := idx.termStats.Find(ctx, term, false); found {
			val, _ := idx.termStats.GetCurrentValue(ctx)
			nq = float64(val)
		} else {
			continue // Term not found
		}

		// Calculate IDF
		idf := math.Log((N-nq+0.5)/(nq+0.5) + 1)

		// Find all postings for this term
		// Range scan: "term|" to "term|~"
		startKey := term + "|"

		// Position cursor
		if found, err := idx.postings.Find(ctx, startKey, true); err != nil {
			return nil, err
		} else if !found {
			// If not found, Find positions cursor at nearest item.
			// Check if we need to advance.
			currItem, err := idx.postings.GetCurrentItem(ctx)
			if err != nil {
				return nil, err
			}
			if currItem.Key < startKey {
				if hasNext, err := idx.postings.Next(ctx); err != nil {
					return nil, err
				} else if !hasNext {
					continue // End of index
				}
			}
		}

		// Iterate while prefix matches
		for {
			item, err := idx.postings.GetCurrentItem(ctx)
			if err != nil {
				return nil, err
			}

			// Check prefix
			if len(item.Key) < len(startKey) || item.Key[:len(startKey)] != startKey {
				break
			}

			// Extract DocID from Key "term|docID"
			docID := item.Key[len(startKey):]
			freq := *item.Value

			// Calculate BM25 Score for this term/doc
			// score = IDF * (freq * (k1 + 1)) / (freq + k1 * (1 - b + b * docLen / avgDL))

			// Need DocLen
			var docLen float64
			if found, _ := idx.docStats.Find(ctx, docID, false); found {
				val, _ := idx.docStats.GetCurrentValue(ctx)
				docLen = float64(val)
			} else {
				docLen = avgDL // Fallback
			}

			numerator := float64(freq) * (k1 + 1)
			denominator := float64(freq) + k1*(1-b+b*docLen/avgDL)
			score := idf * float64(numerator) / denominator

			scores[docID] += score

			// Move to next posting
			if hasNext, err := idx.postings.Next(ctx); err != nil {
				return nil, err
			} else if !hasNext {
				break
			}
		}
	}

	// Convert map to slice
	var results []TextSearchResult
	for docID, score := range scores {
		results = append(results, TextSearchResult{DocID: docID, Score: score})
	}

	// Sort by Score Descending
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	return results, nil
}
