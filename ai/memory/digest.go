package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/embed"
)

type KBDigestRequest struct {
	Queries            []string
	PerQueryLimit      int
	MaxResults         int
	MinScore           float32
	UseClosestCategory bool
	KeywordFallback    bool
}

type KBDigestHit struct {
	DocID      []string
	Score      float32
	Category   string
	Text       string
	Query      string
	SearchType string
}

func DigestKnowledgeBase(ctx context.Context, kb *KnowledgeBase[map[string]any], embedder ai.Embeddings, req KBDigestRequest) ([]KBDigestHit, error) {
	if kb == nil {
		return nil, nil
	}

	queries := normalizeDigestQueries(req.Queries)
	if len(queries) == 0 {
		return nil, nil
	}

	// Bounded on both ends: an unbounded caller-supplied limit would make
	// the map-size hint below (and the search calls that use perQueryLimit)
	// an easy way to force a huge allocation.
	const maxDigestLimit = 1000

	perQueryLimit := req.PerQueryLimit
	if perQueryLimit <= 0 {
		perQueryLimit = 5
	} else if perQueryLimit > maxDigestLimit {
		perQueryLimit = maxDigestLimit
	}
	maxResults := req.MaxResults
	if maxResults <= 0 {
		maxResults = perQueryLimit
	} else if maxResults > maxDigestLimit {
		maxResults = maxDigestLimit
	}
	minScore := req.MinScore
	if minScore <= 0 {
		minScore = 0.6
	}

	merged := make(map[string]KBDigestHit, len(queries)*perQueryLimit)
	for _, query := range queries {
		categoryFilter := ""
		if embedder != nil {
			vecs, err := embed.CategoryTexts(ctx, embedder, []string{normalize(query)})
			if err == nil && len(vecs) > 0 {
				if req.UseClosestCategory {
					closestCat, _, err := kb.Manager.FindClosestCategory(ctx, vecs[0])
					if err == nil && closestCat != nil {
						categoryFilter = closestCat.Name
					}
				}

				semanticOpts := []*SearchOptions[map[string]any]{{Limit: perQueryLimit}}
				if categoryFilter != "" {
					semanticOpts = append([]*SearchOptions[map[string]any]{{
						Limit:        perQueryLimit,
						CategoryPath: categoryFilter,
					}}, semanticOpts...)
				}

				for _, opts := range semanticOpts {
					vecs, err = embed.QueryTexts(ctx, embedder, []string{normalize(query)})
					if err != nil {
						return nil, err
					}
					batch, err := kb.Search(ctx, []SearchRequest[map[string]any]{{
						Vector:       vecs[0],
						Limit:        opts.Limit,
						CategoryPath: opts.CategoryPath,
					}})
					if err != nil {
						return nil, err
					}
					if len(batch) == 0 {
						continue
					}
					for _, hit := range batch[0] {
						relevance := digestSemanticRelevance(hit.Score)
						if relevance < minScore {
							continue
						}
						mergeDigestHit(merged, KBDigestHit{
							DocID:      hit.DocID,
							Score:      relevance,
							Category:   extractDigestCategory(hit.Payload),
							Text:       extractDigestText(hit.Payload),
							Query:      query,
							SearchType: "semantic",
						})
					}
				}
			}
		}

		if req.KeywordFallback {
			batch, err := kb.Search(ctx, []SearchRequest[map[string]any]{{
				Text:  query,
				Limit: perQueryLimit,
			}})
			if err != nil {
				return nil, err
			}
			if len(batch) == 0 {
				continue
			}
			for _, hit := range batch[0] {
				mergeDigestHit(merged, KBDigestHit{
					DocID:      hit.DocID,
					Score:      hit.Score,
					Category:   extractDigestCategory(hit.Payload),
					Text:       extractDigestText(hit.Payload),
					Query:      query,
					SearchType: "keyword",
				})
			}
		}
	}

	results := make([]KBDigestHit, 0, len(merged))
	for _, hit := range merged {
		if strings.TrimSpace(hit.Text) == "" {
			continue
		}
		results = append(results, hit)
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Category != results[j].Category {
			return results[i].Category < results[j].Category
		}
		return results[i].Text < results[j].Text
	})

	if len(results) > maxResults {
		results = results[:maxResults]
	}

	return results, nil
}

func normalizeDigestQueries(queries []string) []string {
	if len(queries) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(queries))
	normalized := make([]string, 0, len(queries))
	for _, query := range queries {
		query = strings.TrimSpace(query)
		if query == "" {
			continue
		}
		key := strings.ToLower(query)
		if seen[key] {
			continue
		}
		seen[key] = true
		normalized = append(normalized, query)
	}
	return normalized
}

func mergeDigestHit(merged map[string]KBDigestHit, hit KBDigestHit) {
	text := strings.TrimSpace(hit.Text)
	if text == "" {
		return
	}
	key := strings.Join(hit.DocID, "|")
	if key == "" {
		key = hit.Category + "|" + text
	}
	if existing, ok := merged[key]; !ok || hit.Score > existing.Score {
		hit.Text = text
		merged[key] = hit
	}
}

func digestSemanticRelevance(distance float32) float32 {
	if distance < 0 {
		distance = 0
	}
	return 1 / (1 + distance)
}

func extractDigestText(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	for _, field := range []string{"_raw_content", "content", "description", "text", "page_content"} {
		if text, ok := payload[field].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return fmt.Sprintf("%v", payload)
}

func extractDigestCategory(payload map[string]any) string {
	if payload == nil {
		return ""
	}
	for _, field := range []string{"category", "category_path", "path"} {
		if text, ok := payload[field].(string); ok {
			return text
		}
	}
	return ""
}
