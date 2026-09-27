package memory

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

// Ensure mocks implement standard ai methods
func (m *MockPlaybookEmbedder) Dim() int                     { return 3 }
func (m *MockPlaybookEmbedder) Name() string                 { return "mock" }
func (m *MockPlaybookLLM) Name() string                      { return "mock" }
func (m *MockPlaybookLLM) EstimateCost(x int, y int) float64 { return 0 }
func (m *MockPlaybookLLM) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	return ai.GenOutput{}, nil
}
func (m *MockPlaybookLLM) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	return nil
}

func TestKnowledgeBase_API(t *testing.T) {
	ctx := context.Background()

	cats := inmemory.NewBtree[sop.UUID, *Category](true)
	vecs := inmemory.NewBtree[VectorKey, Vector](true)
	items := inmemory.NewBtree[ItemKey, Item[string]](true)

	s := NewStore[string]("test_kb", nil, cats.Btree, inmemory.NewBtree[string, sop.UUID](false).Btree, inmemory.NewBtree[DistanceKey, byte](false).Btree, vecs.Btree, items.Btree, inmemory.NewBtree[sop.UUID, Document](false).Btree).(*store[string])
	s.SetTextIndex(&MockTextIndex{})

	embedder := &MockPlaybookEmbedder{Rules: []PlaybookRule{
		{Keywords: []string{"test"}, CategoryName: "test_cat", Vector: []float32{1.0, 1.0, 1.0}},
	}}
	llm := &MockPlaybookLLM{Rules: []PlaybookRule{
		{Keywords: []string{"test"}, CategoryName: "test_cat", Vector: []float32{1.0, 1.0, 1.0}},
	}, Embedder: embedder}
	s.SetLLM(llm)

	manager := NewMemoryManager[string](s, llm, embedder)

	kb := &KnowledgeBase[string]{
		Store:   s,
		Manager: manager,
	}

	// We inject vectors during IngestThoughts for this test to bypass vectorize
	err := kb.IngestThoughts(ctx, []Thought[string]{{Summaries: []string{"test_cat"}, CategoryPath: "test_id", Vectors: [][]float32{{1.0, 1.0, 1.0}}, Data: "payload"}}, "test")
	if err != nil {
		t.Fatalf("IngestThought failed: %v", err)
	}

	batch, err := kb.Search(ctx, []SearchRequest[string]{{Vector: []float32{1.0, 1.0, 1.0}, Limit: 10, CategoryPath: "test_id"}})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(batch) != 1 || len(batch[0]) == 0 {
		t.Fatalf("expected semantic hits from Search, got %#v", batch)
	}

	kbatch, err := kb.Search(ctx, []SearchRequest[string]{{Text: "payload", Limit: 10}})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(kbatch) != 1 || len(kbatch[0]) == 0 {
		t.Fatalf("expected keyword hits from Search, got %#v", kbatch)
	}

	err = kb.TriggerSleepCycle(ctx)
	if err != nil {
		t.Fatalf("TriggerSleepCycle failed: %v", err)
	}
}

func TestKnowledgeBase_InitializeUsesConfiguredEmbedder(t *testing.T) {
	ctx := context.Background()

	cats := inmemory.NewBtree[sop.UUID, *Category](true)
	vecs := inmemory.NewBtree[VectorKey, Vector](false)
	items := inmemory.NewBtree[ItemKey, Item[string]](false)

	memStore := NewStore[string]("test_kb", nil, cats.Btree, inmemory.NewBtree[string, sop.UUID](false).Btree, inmemory.NewBtree[DistanceKey, byte](false).Btree, vecs.Btree, items.Btree, inmemory.NewBtree[sop.UUID, Document](false).Btree)
	ds := memStore.(*store[string])
	ds.SetTextIndex(&MockTextIndex{})

	kb := &KnowledgeBase[string]{
		Store:   ds,
		Manager: NewMemoryManager[string](ds, &MockLLM{}, nil),
	}

	cfg := &KnowledgeBaseConfig{Embedder: "simple", EmbedderDimension: 7}
	if err := kb.SetConfig(ctx, cfg); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}

	if err := kb.Initialize(ctx); err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}
	if kb.Manager == nil || kb.Manager.embedder == nil {
		t.Fatal("expected Initialize to assign an embedder to the manager")
	}
	if kb.Manager.embedder.Name() != "simple" {
		t.Fatalf("expected initialized embedder to use config name %q, got %q", "simple", kb.Manager.embedder.Name())
	}
	if kb.Manager.embedder.Dim() != 7 {
		t.Fatalf("expected initialized embedder to use config dimension %d, got %d", 7, kb.Manager.embedder.Dim())
	}
}

func TestStaticKnowledgeBase(t *testing.T) {
	ctx := context.Background()

	categories := inmemory.NewBtree[sop.UUID, *Category](true)
	vectors := inmemory.NewBtree[VectorKey, Vector](false)
	items := inmemory.NewBtree[ItemKey, Item[string]](false)

	memStore := NewStore[string]("test_kb", nil, categories.Btree, inmemory.NewBtree[string, sop.UUID](false).Btree, inmemory.NewBtree[DistanceKey, byte](false).Btree, vectors.Btree, items.Btree, inmemory.NewBtree[sop.UUID, Document](false).Btree)
	ds := memStore.(*store[string])
	ds.SetTextIndex(&MockTextIndex{})
	ds.SetDomainReference([]float32{0.0, 0.0, 0.0})

	kb := KnowledgeBase[string]{
		Store:   ds,
		Manager: NewMemoryManager[string](ds, &MockLLM{}, &MockEmbedder{}),
	}

	err := kb.IngestThoughts(ctx, []Thought[string]{{Summaries: []string{"Apple is a fruit"}, CategoryPath: "Fruits", Vectors: [][]float32{{0.1, 0.2, 0.3}}, Data: "apple is a fruit"}}, "")
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	err = kb.IngestThoughts(ctx, []Thought[string]{{Summaries: []string{"Car is a vehicle"}, CategoryPath: "Vehicles", Vectors: [][]float32{{0.9, 0.8, 0.7}}, Data: "car"}}, "")
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	batchFruits, err := kb.Search(ctx, []SearchRequest[string]{{Vector: []float32{0.1, 0.2, 0.3}, CategoryPath: "Fruits", Limit: 10}})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(batchFruits) != 1 || len(batchFruits[0]) == 0 {
		t.Fatalf("expected semantic hits in Fruits category, got %#v", batchFruits)
	} else if batchFruits[0][0].Payload != "apple is a fruit" {
		t.Errorf("Expected apple is a fruit hit, got %v", batchFruits[0][0].Payload)
	}

	batchVehicles, err := kb.Search(ctx, []SearchRequest[string]{{Vector: []float32{0.1, 0.2, 0.3}, CategoryPath: "Vehicles", Limit: 10}})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(batchVehicles) != 1 || len(batchVehicles[0]) != 1 || batchVehicles[0][0].Payload != "car" {
		t.Errorf("Expected hits from Search in Vehicles category, got %#v", batchVehicles)
	}

	batchKeyword, err := kb.Search(ctx, []SearchRequest[string]{{Text: "fruit", CategoryPath: "Fruits", Limit: 10}})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(batchKeyword) != 1 || len(batchKeyword[0]) == 0 {
		t.Fatalf("expected keyword hits from Search, got %#v", batchKeyword)
	}

	// NonExistent category will semantically fall back to closest category (semantic fallback is intentional)
	batchEmpty, err := kb.Search(ctx, []SearchRequest[string]{{Text: "fruit", CategoryPath: "NonExistent", Limit: 10}})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	// With semantic fallback, this will match the closest category
	if len(batchEmpty) != 1 {
		t.Fatalf("expected search result with semantic fallback, got %#v", batchEmpty)
	}
}

func TestNormalize_NormalizesVectorizationText(t *testing.T) {
	got := normalize("  A & B / C  ")
	if got != "a and b c" {
		t.Fatalf("normalize() = %q, want %q", got, "a and b c")
	}
}

func TestNormalize_PreservesUnicodeTextAndNormalizesFullWidthPunctuation(t *testing.T) {
	got := normalize("  Ａ＆Ｂ 你好 / 世界  ")
	if got != "a and b 你好 世界" {
		t.Fatalf("normalize() = %q, want %q", got, "a and b 你好 世界")
	}
}

func TestNormalize_StabilizesProgrammingLanguageNames(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "csharp", in: "C#", want: "csharp"},
		{name: "cpp", in: "C++", want: "cpp"},
		{name: "dotnet", in: ".NET", want: "dotnet"},
		{name: "path tokens", in: "Language Bindings/C#", want: "language bindings csharp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalize(tt.in); got != tt.want {
				t.Fatalf("normalize(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSearchOptionsSummary_SafelyExcludesFilterFunction(t *testing.T) {
	opts := &SearchOptions[map[string]any]{
		Limit:          7,
		CategoryPath:   "docs",
		CategoryVector: []float32{1, 2},
		Filter:         func(map[string]any) bool { return true },
	}

	summary := searchOptionsSummary(opts)
	if len(summary) != 6 {
		t.Fatalf("expected 6 summary fields, got %d", len(summary))
	}
	if got := summary[0].(string); got != "limit" {
		t.Fatalf("expected limit key, got %v", got)
	}
	if got := summary[1].(int); got != 7 {
		t.Fatalf("expected limit value 7, got %v", got)
	}
	if got := summary[2].(string); got != "category_path" {
		t.Fatalf("expected category_path key, got %v", got)
	}
	if got := summary[3].(string); got != "docs" {
		t.Fatalf("expected docs category path, got %v", got)
	}
	if got := summary[4].(string); got != "has_filter" {
		t.Fatalf("expected has_filter key, got %v", got)
	}
	if got := summary[5].(bool); !got {
		t.Fatalf("expected has_filter to be true, got %v", got)
	}
}

func TestKnowledgeBase_Search_UsesSingleEntryPoint(t *testing.T) {
	ctx := context.Background()

	cats := inmemory.NewBtree[sop.UUID, *Category](true)
	vecs := inmemory.NewBtree[VectorKey, Vector](true)
	items := inmemory.NewBtree[ItemKey, Item[string]](true)

	memStore := NewStore[string]("test_kb", nil, cats.Btree, inmemory.NewBtree[string, sop.UUID](false).Btree, inmemory.NewBtree[DistanceKey, byte](false).Btree, vecs.Btree, items.Btree, inmemory.NewBtree[sop.UUID, Document](false).Btree)
	memStore.(*store[string]).SetTextIndex(&MockTextIndex{})
	kb := KnowledgeBase[string]{
		Store:   memStore,
		Manager: NewMemoryManager[string](memStore, &MockLLM{}, &MockEmbedder{}),
	}

	if err := kb.IngestThoughts(ctx, []Thought[string]{{Summaries: []string{"fruit"}, CategoryPath: "Fruits", Vectors: [][]float32{{0.1, 0.2, 0.3}}, Data: "apple is a fruit"}}, ""); err != nil {
		t.Fatalf("IngestThoughts failed: %v", err)
	}

	hits, err := kb.Search(ctx, []SearchRequest[string]{{Text: "fruit", Limit: 5}})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(hits) != 1 || len(hits[0]) == 0 {
		t.Fatalf("expected one batch result with hits, got %#v", hits)
	}
}

func TestKnowledgeBase_Search_AllowsTextOnlySearchWithoutVectorization(t *testing.T) {
	ctx := context.Background()

	categories := inmemory.NewBtree[sop.UUID, *Category](true)
	vectors := inmemory.NewBtree[VectorKey, Vector](true)
	items := inmemory.NewBtree[ItemKey, Item[string]](true)

	memStore := NewStore[string]("test_kb", nil, categories.Btree, inmemory.NewBtree[string, sop.UUID](false).Btree, inmemory.NewBtree[DistanceKey, byte](false).Btree, vectors.Btree, items.Btree, inmemory.NewBtree[sop.UUID, Document](false).Btree)
	ds := memStore.(*store[string])
	ds.SetTextIndex(&MockTextIndex{})

	kb := KnowledgeBase[string]{
		Store:   ds,
		Manager: NewMemoryManager[string](ds, &MockLLM{}, &MockEmbedder{}),
	}
	kb.configCache = &KnowledgeBaseConfig{LastVectorized: 0}

	if err := kb.IngestThoughts(ctx, []Thought[string]{{Summaries: []string{"fruit"}, CategoryPath: "Fruits", Vectors: [][]float32{{0.1, 0.2, 0.3}}, Data: "apple is a fruit"}}, ""); err != nil {
		t.Fatalf("IngestThoughts failed: %v", err)
	}

	batch, err := kb.Search(ctx, []SearchRequest[string]{{Text: "fruit", Limit: 10}})
	if err != nil {
		t.Fatalf("Search should allow text-only retrieval even when not vectorized: %v", err)
	}
	if len(batch) != 1 || len(batch[0]) == 0 {
		t.Fatalf("expected text-only hits on non-vectorized KB, got %#v", batch)
	}
}

func TestKnowledgeBase_Search_TextQueryCombinesVectorAndTextResults(t *testing.T) {
	ctx := context.Background()

	categories := inmemory.NewBtree[sop.UUID, *Category](true)
	vectors := inmemory.NewBtree[VectorKey, Vector](true)
	items := inmemory.NewBtree[ItemKey, Item[string]](true)

	memStore := NewStore[string]("test_kb", nil, categories.Btree, inmemory.NewBtree[string, sop.UUID](false).Btree, inmemory.NewBtree[DistanceKey, byte](false).Btree, vectors.Btree, items.Btree, inmemory.NewBtree[sop.UUID, Document](false).Btree)
	ds := memStore.(*store[string])
	ds.SetTextIndex(&MockTextIndex{})

	embedder := &MockPlaybookEmbedder{Rules: []PlaybookRule{{Keywords: []string{"fruit"}, CategoryName: "Fruit", Vector: []float32{0.9, 0.8, 0.7}}}}
	kb := KnowledgeBase[string]{
		Store:   ds,
		Manager: NewMemoryManager[string](ds, &MockLLM{}, embedder),
	}

	cat := &Category{ID: sop.NewUUID(), Name: "Fruit", CenterVector: []float32{0.9, 0.8, 0.7}}
	if _, err := ds.categories.Add(ctx, cat.ID, cat); err != nil {
		t.Fatalf("add category failed: %v", err)
	}

	itemA := Item[string]{ID: sop.NewUUID(), CategoryID: cat.ID, Data: "fruit note", Summaries: []string{"fruit"}}
	if err := ds.UpsertByCategoryID(ctx, cat.ID, cat.CenterVector, itemA, [][]float32{{0.1, 0.2, 0.3}}, nil); err != nil {
		t.Fatalf("upsert item A failed: %v", err)
	}

	itemB := Item[string]{ID: sop.NewUUID(), CategoryID: cat.ID, Data: "banana note", Summaries: []string{"banana"}}
	if err := ds.UpsertByCategoryID(ctx, cat.ID, cat.CenterVector, itemB, [][]float32{{0.9, 0.8, 0.7}}, nil); err != nil {
		t.Fatalf("upsert item B failed: %v", err)
	}

	batch, err := kb.Search(ctx, []SearchRequest[string]{{Text: "fruit", Vector: []float32{0.9, 0.8, 0.7}, Limit: 10}})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(batch) != 1 || len(batch[0]) != 2 {
		t.Fatalf("expected text query to combine semantic and text hits, got %#v", batch)
	}

	got := map[string]bool{}
	for _, hit := range batch[0] {
		got[hit.Payload] = true
	}
	if !got["fruit note"] || !got["banana note"] {
		t.Fatalf("expected combined hits for both fruit and banana notes, got %#v", batch[0])
	}
}

func TestKnowledgeBase_SearchKeywords_NoTextSearchEnabledReturnsNoHits(t *testing.T) {
	ctx := context.Background()

	categories := inmemory.NewBtree[sop.UUID, *Category](true)
	vectors := inmemory.NewBtree[VectorKey, Vector](true)
	items := inmemory.NewBtree[ItemKey, Item[string]](true)

	store := NewStore[string]("test_kb", nil, categories.Btree, inmemory.NewBtree[string, sop.UUID](false).Btree, inmemory.NewBtree[DistanceKey, byte](false).Btree, vectors.Btree, items.Btree, inmemory.NewBtree[sop.UUID, Document](false).Btree)
	kb := KnowledgeBase[string]{
		Store:   store,
		Manager: NewMemoryManager[string](store, &MockLLM{}, &MockEmbedder{}),
	}

	batch, err := kb.Search(ctx, []SearchRequest[string]{{Text: "fruit", Limit: 10}})
	if err != nil {
		t.Fatalf("Search should not fail when text search is disabled: %v", err)
	}
	if len(batch) != 0 {
		t.Fatalf("expected no search batches when text search is disabled, got %#v", batch)
	}
}
