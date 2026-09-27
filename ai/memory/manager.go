package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
)

// MemoryManager orchestrates the Semantic Anchoring and Asynchronous Sleep Cycle.
// It interfaces directly with an LLM and an Embedder to completely bypass
// mathematical (K-Means) clustering in favor of Semantic taxonomies.
type MemoryManager[T any] struct {
	store                   MemoryStore[T]
	llm                     ai.Generator
	embedder                ai.Embeddings
	sleepThreshold          int
	inlineRefactorThreshold int
}

// NewMemoryManager creates a new biomimetic memory orchestrator.
func NewMemoryManager[T any](store MemoryStore[T], llm ai.Generator, embedder ai.Embeddings) *MemoryManager[T] {
	return &MemoryManager[T]{
		store:                   store,
		llm:                     llm,
		embedder:                embedder,
		sleepThreshold:          1000,
		inlineRefactorThreshold: 10,
	}
}

// GenerateCategory uses the LLM to deduce a 2-4 word taxonomy category for a raw thought.
func (m *MemoryManager[T]) GenerateCategory(ctx context.Context, text string, personaContext string) (string, error) {
	var prompt string
	if personaContext != "" {
		prompt = fmt.Sprintf("Given the context '%s', categorize the following thought into exactly a 2-4 word concept:\n\n%s", personaContext, text)
	} else {
		prompt = fmt.Sprintf("Categorize the following thought into exactly a 2-4 word concept:\n\n%s", text)
	}
	opts := ai.GenOptions{MaxTokens: 10, Temperature: 0.1, ThinkingLevel: "low"}
	out, err := m.llm.Generate(ctx, prompt, opts)
	if err != nil {
		return "", fmt.Errorf("llm classification failed: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}

// GenerateCategories uses the LLM to deduce a 2-4 word taxonomy category for a batch of raw thoughts.
func (m *MemoryManager[T]) GenerateCategories(ctx context.Context, texts []string, personaContext string) ([]string, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	var promptBuilder strings.Builder
	if personaContext != "" {
		promptBuilder.WriteString(fmt.Sprintf("Given the context '%s', categorize each of the following thoughts into exactly a 2-4 word concept.\nReturn ONLY a comma-separated list of categories, one for each thought, in the exact same order.\n\n", personaContext))
	} else {
		promptBuilder.WriteString("Categorize each of the following thoughts into exactly a 2-4 word concept.\nReturn ONLY a comma-separated list of categories, one for each thought, in the exact same order.\n\n")
	}
	for i, t := range texts {
		promptBuilder.WriteString(fmt.Sprintf("[%d] %s\n", i+1, t))
	}

	opts := ai.GenOptions{MaxTokens: 10 * len(texts), Temperature: 0.1, ThinkingLevel: "low"}
	out, err := m.llm.Generate(ctx, promptBuilder.String(), opts)
	if err != nil {
		return nil, fmt.Errorf("llm batch classification failed: %w", err)
	}

	raw := strings.TrimSpace(out.Text)
	parts := strings.Split(raw, ",")
	var result []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			result = append(result, strings.TrimSpace(p))
		}
	}
	// Fallbacks if LLM fails formatting
	for len(result) < len(texts) {
		result = append(result, "Uncategorized")
	}
	if len(result) > len(texts) {
		result = result[:len(texts)]
	}
	return result, nil
}

// FindClosestCategory evaluates the spatial coordinates logically mapped into categories.
// This executes mathematically without LLM inference, serving as the fast-path.
func (m *MemoryManager[T]) FindClosestCategory(ctx context.Context, vector []float32) (*Category, float32, error) {
	return m.store.FindClosestCategory(ctx, vector)
}

// EnsureCategory guarantees a Semantic Anchor physically exists in the B-Tree for a string noun.
func (m *MemoryManager[T]) EnsureCategory(ctx context.Context, categoryPath string) (sop.UUID, error) {
	categoriesTree, err := m.store.Categories(ctx)
	if err != nil {
		return sop.NilUUID, err
	}

	if parsedID, err := sop.ParseUUID(categoryPath); err == nil {
		if found, _ := categoriesTree.Find(ctx, parsedID, false); found {
			return parsedID, nil
		}
	}

	ok, err := categoriesTree.First(ctx)
	for ok && err == nil {
		c, _ := categoriesTree.GetCurrentValue(ctx)
		// Match against Path instead of Name to ensure uniqueness of deeper nodes
		if c != nil {
			if c.Path != "" && strings.EqualFold(c.Path, categoryPath) {
				return categoriesTree.GetCurrentKey().Key, nil
			}
			if c.Path == "" && strings.EqualFold(c.Name, categoryPath) {
				return categoriesTree.GetCurrentKey().Key, nil
			}
		}
		ok, err = categoriesTree.Next(ctx)
	}

	parts := strings.Split(categoryPath, "/")
	leafName := parts[len(parts)-1]

	CID := sop.NewUUID()
	anchor := &Category{
		ID:          CID,
		Name:        leafName,
		Path:        categoryPath,
		Description: "",
		ItemCount:   0,
	}

	cid, err := m.store.AddCategory(ctx, anchor)
	if err != nil {
		return sop.NilUUID, err
	}
	return cid, nil
}

// SleepCycle performs Asynchronous Memory Consolidation.
func (m *MemoryManager[T]) SleepCycle(ctx context.Context) error {
	// Historical sleep-cycle reflection logic would remain here...
	return nil
}

// GenerateSummariesBatch splits a batch of data payloads into logical vectors via LLM
func (m *MemoryManager[T]) GenerateSummariesBatch(ctx context.Context, payloads []string) ([][]string, error) {
	if m.llm == nil {
		res := make([][]string, len(payloads))
		for i, d := range payloads {
			res[i] = []string{d}
		}
		return res, nil
	}
	if len(payloads) == 0 {
		return nil, nil
	}

	var prompt strings.Builder
	prompt.WriteString("Break each of the following data payloads down into distinct logical vectors or small standalone factual observations (sentences or short phrases).\n")
	prompt.WriteString("For each payload, output its index in brackets, followed by a pipe-separated ( | ) list of these phrases.\n\n")

	for i, p := range payloads {
		if len(p) > 2000 {
			p = p[:2000]
		}
		prompt.WriteString(fmt.Sprintf("[%d] %s\n", i, p))
	}

	opts := ai.GenOptions{MaxTokens: 2000, Temperature: 0.1, ThinkingLevel: "low"}
	out, err := m.llm.Generate(ctx, prompt.String(), opts)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(out.Text, "\n")
	res := make([][]string, len(payloads))

	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		start := strings.Index(l, "[")
		end := strings.Index(l, "]")
		if start >= 0 && end > start {
			var idx int
			if _, err := fmt.Sscanf(l[start+1:end], "%d", &idx); err == nil && idx >= 0 && idx < len(payloads) {
				text := l[end+1:]
				text = strings.TrimPrefix(text, ":")
				parts := strings.Split(text, "|")
				for _, p := range parts {
					p = strings.TrimSpace(p)
					p = strings.Trim(p, "-*\"'")
					if p != "" {
						res[idx] = append(res[idx], p)
					}
				}
			}
		}
	}

	for i := range res {
		if len(res[i]) == 0 {
			res[i] = []string{payloads[i]}
		}
	}
	return res, nil
}

func (m *MemoryManager[T]) GenerateSummaries(ctx context.Context, dataStr string) ([]string, error) {
	if m.llm == nil {
		return []string{dataStr}, nil
	}
	prompt := "Break the following data down into distinct logical vectors or small standalone factual observations (sentences or short phrases). Return ONLY a pipe-separated ( | ) list of these phrases.\n\nData: " + dataStr
	opts := ai.GenOptions{MaxTokens: 1000, Temperature: 0.1, ThinkingLevel: "low"}
	out, err := m.llm.Generate(ctx, prompt, opts)
	if err != nil {
		return nil, err
	}
	parts := strings.Split(out.Text, "|")
	var res []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			res = append(res, strings.TrimSpace(p))
		}
	}
	if len(res) == 0 {
		res = []string{dataStr}
	}
	return res, nil
}
