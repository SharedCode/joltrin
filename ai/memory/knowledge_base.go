package memory

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/embed"
	"github.com/sharedcode/joltrin/v5"
)

// KnowledgeBase provides a clean, unified API for developers.
// It orchestrates both the storage tables and the LLM memory management.
type KnowledgeBase[T any] struct {
	Store   MemoryStore[T]
	Manager *MemoryManager[T]
	// transaction is used by the convenience constructor to keep the underlying
	// filesystem-backed stores alive for the lifetime of the KnowledgeBase.
	transaction sop.Transaction
	// MaxMathCategoryDistance specifies the max Euclidean distance to cluster centroids
	// to avoid calling the LLM for category categorization. Set to 0.0 or less to disable
	// and always rely on "pristine" LLM categorization.
	MaxMathCategoryDistance float32

	configCache *KnowledgeBaseConfig
}

// Returns this KnowledgeBase's name.
func (kb *KnowledgeBase[T]) Name() string {
	return kb.Store.Name()
}

// SetTransaction attaches a transaction that should be committed or rolled back
// when the KnowledgeBase is closed. This is primarily used by the convenience
// constructor for the filesystem-backed default path.
func (kb *KnowledgeBase[T]) SetTransaction(tx sop.Transaction) {
	if kb != nil {
		kb.transaction = tx
	}
}

// Close commits any transaction owned by this KnowledgeBase.
func (kb *KnowledgeBase[T]) Close(ctx context.Context) error {
	if kb == nil || kb.transaction == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	err := kb.transaction.Commit(ctx)
	kb.transaction = nil
	return err
}

func uniqueCategories(cats []*Category) []*Category {
	seen := make(map[sop.UUID]struct{}, len(cats))
	unique := make([]*Category, 0, len(cats))
	for _, cat := range cats {
		if cat == nil || cat.ID.IsNil() {
			continue
		}
		if _, ok := seen[cat.ID]; ok {
			continue
		}
		seen[cat.ID] = struct{}{}
		unique = append(unique, cat)
	}
	return unique
}

// findCategoryByPath tries lexical lookup first, then semantic fallback via CategoryText embedding.
func (kb *KnowledgeBase[T]) findCategoryByPath(ctx context.Context, path string) ([]*Category, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}

	// Try lexical lookup
	catsByPath, err := kb.Store.CategoriesByPath(ctx)
	if err != nil {
		return nil, err
	}
	found, err := catsByPath.Find(ctx, path, false)
	if err != nil {
		return nil, err
	}
	if found {
		catID, err := catsByPath.GetCurrentValue(ctx)
		if err != nil {
			return nil, err
		}
		cats, err := kb.Store.Categories(ctx)
		if err != nil {
			return nil, err
		}
		foundCat, err := cats.Find(ctx, catID, false)
		if err != nil {
			return nil, err
		}
		if foundCat {
			cat, err := cats.GetCurrentValue(ctx)
			if err == nil && cat != nil && !cat.ID.IsNil() {
				return []*Category{cat}, nil
			}
		}
	}

	// Semantic fallback via CategoryByDistance.
	// If the KB has not been vectorized yet, skip the semantic path instead of failing the lookup.
	if kb.Manager != nil && kb.Manager.embedder != nil {
		partsVecs, err := embedCategoryPath(ctx, path, kb.Manager.embedder)
		if err != nil {
			return nil, err
		}
		cats, err := kb.Store.SemanticCategoryByPath(ctx, partsVecs)
		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "domain reference vector is not set") {
				return nil, nil
			}
			return nil, err
		}
		return cats, nil
	}

	return nil, nil
}

func NormalizeCategoryToken(text string) string {
	text = strings.TrimSpace(text)
	text = strings.ToLower(text)
	text = strings.NewReplacer(
		"c#", "csharp",
		"c++", "cpp",
		".net", "dotnet",
		"&", " and ",
		"＆", " and ",
		"/", " ",
		"／", " ",
		"\\", " ",
		">", " ",
		"|", " ",
		"-", " ",
		"_", " ",
		":", " ",
		"：", " ",
		";", " ",
		"；", " ",
		"(", " ",
		")", " ",
		"（", " ",
		"）", " ",
		"[", " ",
		"]", " ",
		"【", " ",
		"】", " ",
		"{", " ",
		"}", " ",
		"+", " ",
		"=", " ",
		"@", " ",
		"#", " ",
		"%", " ",
		"$", " ",
		"!", " ",
		"?", " ",
		".", " ",
		",", " ",
		"'", " ",
		"\"", " ",
		"，", " ",
		"。", " ",
		"！", " ",
		"？", " ",
	).Replace(text)
	text = strings.Map(func(r rune) rune {
		switch {
		case r >= 'Ａ' && r <= 'Ｚ':
			return r - 'Ａ' + 'A'
		case r >= 'ａ' && r <= 'ｚ':
			return r - 'ａ' + 'a'
		case r >= '０' && r <= '９':
			return r - '０' + '0'
		case unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r):
			return r
		default:
			return ' '
		}
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	return text
}

func normalize(text string) string {
	return NormalizeCategoryToken(text)
}

func embedCategoryPath(ctx context.Context, catPath string, embedder ai.Embeddings) ([][]float32, error) {
	return CategoryPathVectors(ctx, catPath, embedder)
}

func CategoryPathVectors(ctx context.Context, catPath string, embedder ai.Embeddings) ([][]float32, error) {
	parts := strings.Split(catPath, "/")
	if len(parts) == 0 || (len(parts) == 1 && strings.TrimSpace(parts[0]) == "") {
		parts = strings.Split(catPath, "\\")
	}
	if len(parts) == 0 || (len(parts) == 1 && strings.TrimSpace(parts[0]) == "") {
		return nil, nil
	}

	cleanParts := make([]string, 0, len(parts))
	for _, part := range parts {
		cleanPart := normalize(part)
		if cleanPart != "" {
			cleanParts = append(cleanParts, cleanPart)
		}
	}
	if len(cleanParts) == 0 {
		return nil, nil
	}

	vecs, err := embed.CategoryTexts(ctx, embedder, cleanParts)
	if err != nil {
		return nil, err
	}
	return vecs, nil
}

// findCategoriesByTextSearch extracts categories from text search results.
func (kb *KnowledgeBase[T]) findCategoriesByTextSearch(ctx context.Context, text string, limit int) ([]*Category, error) {
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}

	// Delegate to store to extract categories from text index
	if s, ok := kb.Store.(interface {
		GetCategoriesFromTextSearch(context.Context, string) ([]*Category, error)
	}); ok {
		return s.GetCategoriesFromTextSearch(ctx, text)
	}

	return nil, nil
}

func mergeSearchHits[T any](semanticHits, textHits []ai.Hit[T]) []ai.Hit[T] {
	seen := make(map[string]struct{}, len(semanticHits)+len(textHits))
	merged := make([]ai.Hit[T], 0, len(semanticHits)+len(textHits))

	for _, hit := range append(append([]ai.Hit[T]{}, semanticHits...), textHits...) {
		key := fmt.Sprintf("%v", hit.ID)
		if hit.DocID != nil {
			key = fmt.Sprintf("%v:%v", hit.DocID, hit.ID)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		merged = append(merged, hit)
	}
	return merged
}

func searchOptionsSummary[T any](opts *SearchOptions[T]) []any {
	if opts == nil {
		return []any{"limit", 0, "category_path", "", "has_filter", false}
	}
	return []any{"limit", opts.Limit, "category_path", opts.CategoryPath, "has_filter", opts.Filter != nil}
}

// Search provides one reusable entry point for single or batch retrieval.
// Precedence:
// 1. If CategoryPath specified: try CategoryByPath, fallback to CategoryByDistance
// 2. If no category yet and Text present: use CategoryText embedding to get resolved in CategoryByDistance + TextSearch categories. (BOTH)
// 3. If no category found: short circuit and return empty
// 4. Use found categories to do vector search
// 5. Return matching items
func (kb *KnowledgeBase[T]) Search(ctx context.Context, requests []SearchRequest[T]) ([][]ai.Hit[T], error) {
	if len(requests) == 0 {
		return nil, nil
	}

	results := make([][]ai.Hit[T], 0, len(requests))
	for _, req := range requests {
		var candidates []*Category

		// Step 1: If CategoryPath specified, try CategoryByPath then CategoryByDistance
		if strings.TrimSpace(req.CategoryPath) != "" {
			cats, err := kb.findCategoryByPath(ctx, req.CategoryPath)
			if err != nil {
				return nil, err
			}
			if len(cats) > 0 {
				candidates = append(candidates, cats...)
			}
		}

		// Step 2: If no category yet and Text present, use CategoryText embedding to get resolved in CategoryByDistance + TextSearch categories. (BOTH)
		if len(candidates) == 0 && strings.TrimSpace(req.Text) != "" {
			// Use CategoryText embedding to get resolved in CategoryByDistance
			if kb.Manager != nil && kb.Manager.embedder != nil {
				vecs, err := embed.CategoryTexts(ctx, kb.Manager.embedder, []string{normalize(req.Text)})
				if err == nil && len(vecs) > 0 {
					cat, _, err := kb.Manager.FindClosestCategory(ctx, vecs[0])
					if err == nil && cat != nil {
						candidates = append(candidates, cat)
					}
				}
			}

			// ALSO get categories from TextSearch (BOTH paths, not fallback)
			textCats, err := kb.findCategoriesByTextSearch(ctx, req.Text, req.Limit)
			if err == nil && len(textCats) > 0 {
				candidates = append(candidates, textCats...)
			}
		}

		// Step 3: Short circuit if no category found
		if len(candidates) == 0 {
			continue
		}

		// Step 4: Use found categories to do vector search
		queryVector := req.Vector
		if len(queryVector) == 0 && strings.TrimSpace(req.Text) != "" && kb.Manager != nil && kb.Manager.embedder != nil {
			vecs, err := embed.QueryTexts(ctx, kb.Manager.embedder, []string{normalize(req.Text)})
			if err == nil && len(vecs) > 0 {
				queryVector = vecs[0]
			}
		}

		var hits []ai.Hit[T]

		candidates = uniqueCategories(candidates)
		for _, cat := range candidates {
			catOpts := &SearchOptions[T]{
				Limit:          req.Limit,
				CategoryVector: cat.CenterVector,
				Filter:         req.Filter,
			}
			catHits, err := kb.Store.QueryItems(ctx, queryVector, cat, catOpts)
			if err != nil {
				return nil, err
			}

			if len(catHits) == 0 {
				continue
			}

			// Boost items from better-matching categories by incorporating category distance
			if len(queryVector) > 0 && len(cat.CenterVector) > 0 {
				categoryDist := Distance(queryVector, cat.CenterVector, true)
				// Adjust scores: smaller category distance = better match = higher score
				// Category distance penalty scaled to keep item scores dominant
				for i := range catHits {
					catHits[i].Score = catHits[i].Score - (categoryDist * 0.1)
				}
			}

			hits = append(hits, catHits...)
		}

		// Step 5: Sort by adjusted scores (category distance + item score)
		if len(hits) > 1 {
			sort.Slice(hits, func(i, j int) bool {
				return hits[i].Score > hits[j].Score
			})
		}

		// Return matching items
		if len(hits) > 0 {
			results = append(results, hits)
		}
	}
	return results, nil
}

// Thought represents the individual entity of data in a batch categorization execution.
type Thought[T any] struct {
	Summaries    []string
	CategoryPath string
	DocID        DocIDs
	Data         T
	Vectors      [][]float32
	Positions    []VectorKey
	VectorHash   string
}

// IngestThoughts securely categorizes and stores an array of thoughts, optimizing latency
// by clustering queries and sending a batch request to the LLM generator.
func (kb *KnowledgeBase[T]) IngestThoughts(ctx context.Context, thoughts []Thought[T], persona string) error {
	if len(thoughts) == 0 {
		return nil
	}

	// 0. Resolve missing summaries via LLM LLM Enrichment
	for i, thought := range thoughts {
		if len(thought.Summaries) > 0 {
			continue
		}

		dataStr := ""
		if str, ok := any(thought.Data).(string); ok {
			dataStr = str
		} else {
			b, _ := json.Marshal(thought.Data)
			dataStr = string(b)
		}

		gen, err := kb.Manager.GenerateSummaries(ctx, dataStr)
		if err == nil && len(gen) > 0 {
			thoughts[i].Summaries = gen
		} else {
			thoughts[i].Summaries = []string{dataStr}
		}
	}

	var uncategorizedIdx []int
	var uncategorizedTexts []string

	// 2. Evaluate if we need to categorize
	for i, thought := range thoughts {
		if thought.CategoryPath != "" {
			continue // Already formally categorized
		}

		categorizedByMath := false
		if kb.MaxMathCategoryDistance > 0 && len(thought.Vectors) > 0 {
			closest, dist, err := kb.Manager.FindClosestCategory(ctx, thought.Vectors[0])
			if err == nil && closest != nil && dist <= kb.MaxMathCategoryDistance {
				thoughts[i].CategoryPath = closest.Name
				categorizedByMath = true
			}
		}

		if !categorizedByMath {
			uncategorizedIdx = append(uncategorizedIdx, i)
			// Aggregate summaries for LLM to deduce category
			agg := ""
			for _, s := range thought.Summaries {
				agg += s + " "
			}
			uncategorizedTexts = append(uncategorizedTexts, strings.TrimSpace(agg))
		}
	}

	// 3. Batch Fallback to LLM Pristine Cataloging
	if len(uncategorizedTexts) > 0 {
		generated, err := kb.Manager.GenerateCategories(ctx, uncategorizedTexts, persona)
		if err != nil {
			return err
		}

		// Re-align the generated outputs with the original array
		for idx, origIdx := range uncategorizedIdx {
			thoughts[origIdx].CategoryPath = generated[idx]
		}
	}

	// 4. Ensure Categories and Store
	catCache := make(map[string]*Category)

	// Batch collect all summaries for classification embedding
	type thoughtWithIndex struct {
		thought Thought[T]
		index   int
	}
	var needsClassificationVecs []thoughtWithIndex
	var allClassificationTexts []string

	for i, thought := range thoughts {
		if len(thought.Summaries) > 0 {
			needsClassificationVecs = append(needsClassificationVecs, thoughtWithIndex{thought, i})
			allClassificationTexts = append(allClassificationTexts, thought.Summaries...)
		}
	}

	// Batch embed all classification vectors at once
	var allClassificationVecs [][]float32
	if len(allClassificationTexts) > 0 && kb.Manager != nil && kb.Manager.embedder != nil {
		vecs, err := embed.CategoryTexts(ctx, kb.Manager.embedder, allClassificationTexts)
		if err == nil {
			allClassificationVecs = vecs
		}
	}

	// Map classification vectors back to thoughts
	thoughtClassificationVecs := make(map[int][][]float32)
	vecIdx := 0
	for _, twi := range needsClassificationVecs {
		summaryCount := len(twi.thought.Summaries)
		if vecIdx+summaryCount <= len(allClassificationVecs) {
			thoughtClassificationVecs[twi.index] = allClassificationVecs[vecIdx : vecIdx+summaryCount]
			vecIdx += summaryCount
		}
	}

	for i, thought := range thoughts {
		cat, exists := catCache[thought.CategoryPath]
		if !exists {
			catID, err := kb.Manager.EnsureCategory(ctx, thought.CategoryPath)
			if err != nil {
				return err
			}
			// Retrieve the full category object
			cats, err := kb.Store.Categories(ctx)
			if err != nil {
				return err
			}
			found, err := cats.Find(ctx, catID, false)
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("category not found after ensuring: %v", catID)
			}
			cat, err = cats.GetCurrentValue(ctx)
			if err != nil {
				return err
			}

			// Initialize CenterVector if empty - must use CategoryTexts (classification space)
			if len(cat.CenterVector) == 0 && kb.Manager != nil && kb.Manager.embedder != nil {
				catVecs, err := embed.CategoryTexts(ctx, kb.Manager.embedder, []string{normalize(cat.Name)})
				if err == nil && len(catVecs) > 0 && len(catVecs[0]) > 0 {
					cat.CenterVector = catVecs[0]
					// Update the category in the store
					cats.UpdateCurrentItem(ctx, cat.ID, cat)
				}
			}
			catCache[thought.CategoryPath] = cat
		}

		item := Item[T]{
			ID:         sop.NewUUID(),
			DocID:      thought.DocID,
			Summaries:  thought.Summaries,
			Data:       thought.Data,
			Positions:  thought.Positions,
			VectorHash: thought.VectorHash,
		}

		// Use pre-batched classification vectors
		classificationVecs := thoughtClassificationVecs[i]

		err := kb.Store.UpsertByCategoryID(ctx, cat.ID, cat.CenterVector, item, thought.Vectors, classificationVecs)
		if err != nil {
			return err
		}
	}
	return nil
}

// IngestThought securely categorizes and stores a thought.
// If category is omitted (""), the LLM dynamically categorizes the text, unless it is close
// enough to an existing category centroid and MaxMathCategoryDistance > 0.
func (kb *KnowledgeBase[T]) IngestThought(
	ctx context.Context,
	text string,
	category string,
	persona string,
	vector []float32,
	data T,
) error {
	var vectors [][]float32
	if vector != nil {
		vectors = [][]float32{vector}
	}
	return kb.IngestThoughts(ctx, []Thought[T]{{Summaries: []string{text}, CategoryPath: category, Data: data, Vectors: vectors}}, persona)
}

// TriggerSleepCycle forces the LLM to scan, reflect, and re-organize dense categories.
func (kb *KnowledgeBase[T]) TriggerSleepCycle(ctx context.Context) error {
	if err := kb.Manager.SleepCycle(ctx); err != nil {
		return err
	}
	return nil
}

func encodeConfigValue[T any](config *KnowledgeBaseConfig) (T, error) {
	var zero T
	if config == nil {
		return zero, nil
	}

	configBytes, err := json.Marshal(config)
	if err != nil {
		return zero, err
	}

	valueType := reflect.TypeOf(zero)
	if valueType == nil {
		return zero, nil
	}
	if valueType.Kind() == reflect.String {
		value := reflect.ValueOf(string(configBytes)).Convert(valueType)
		return value.Interface().(T), nil
	}

	var v T
	if err := json.Unmarshal(configBytes, &v); err != nil {
		return zero, err
	}
	return v, nil
}

func decodeConfigValue[T any](data T) (*KnowledgeBaseConfig, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}

	trimmed := bytes.TrimSpace(b)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var stored string
		if err := json.Unmarshal(trimmed, &stored); err == nil {
			trimmed = bytes.TrimSpace([]byte(stored))
		}
	}

	var cfg KnowledgeBaseConfig
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return nil, nil
	}
	if err := json.Unmarshal(trimmed, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// GetConfig retrieves the metadata configuration for this KnowledgeBase.
func (kb *KnowledgeBase[T]) GetConfig(ctx context.Context) (*KnowledgeBaseConfig, error) {
	if kb.configCache != nil {
		return kb.configCache, nil
	}

	itemsBtree, err := kb.Store.Items(ctx)
	if err != nil {
		return nil, err
	}

	nilKey := ItemKey{CategoryID: sop.NilUUID, ItemID: sop.NilUUID}
	found, err := itemsBtree.Find(ctx, nilKey, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil // No config found
	}

	item, err := itemsBtree.GetCurrentValue(ctx)
	if err != nil {
		return nil, err
	}

	cfg, err := decodeConfigValue(item.Data)
	if err != nil {
		return nil, err
	}

	kb.configCache = cfg
	return cfg, nil
}

// SetConfig saves the metadata configuration for this KnowledgeBase.
func (kb *KnowledgeBase[T]) SetConfig(ctx context.Context, config *KnowledgeBaseConfig) error {
	itemsBtree, err := kb.Store.Items(ctx)
	if err != nil {
		return err
	}

	nilKey := ItemKey{CategoryID: sop.NilUUID, ItemID: sop.NilUUID}

	storedData, err := encodeConfigValue[T](config)
	if err != nil {
		return err
	}

	configItem := Item[T]{
		ID:         sop.NilUUID,
		CategoryID: sop.NilUUID,
		Data:       storedData,
	}

	_, err = itemsBtree.Upsert(ctx, nilKey, configItem)
	if err == nil {
		kb.configCache = config
	}
	return err
}

// Initialize ensures the knowledge base has an embedder attached based on its persisted config.
// It uses the configured embedder name and dimension when available, falling back to a simple embedder.
func (kb *KnowledgeBase[T]) Initialize(ctx context.Context) error {
	if kb == nil || kb.Manager == nil {
		return nil
	}
	if kb.Manager.embedder != nil {
		return nil
	}

	cfg, err := kb.GetConfig(ctx)
	if err != nil {
		return err
	}
	if cfg == nil {
		return nil
	}

	dim := cfg.EmbedderDimension
	name := strings.TrimSpace(cfg.Embedder)
	if name == "" {
		name = kb.Store.Name()
	}
	if name == "" {
		name = "simple"
	}

	embedder, err := embed.NewFromName(name, dim)
	if err != nil {
		return err
	}
	if embedder != nil {
		kb.Manager.embedder = embedder
	}
	return nil
}

// ComputeVectorHash computes a predictable string hash representing the text content.
// We optionally include dimensions, but explicitly exclude embedderName so that compatible models (same dims) don't trigger re-vectorization unless content actually changes.
func ComputeVectorHash(embedderDim int, texts ...string) string {
	hasher := sha256.New()

	dimStr := "dim:" + strconv.Itoa(embedderDim) + "::"
	hasher.Write([]byte(dimStr))

	for _, t := range texts {
		hasher.Write([]byte(t + "::"))
	}
	return hex.EncodeToString(hasher.Sum(nil))
}
