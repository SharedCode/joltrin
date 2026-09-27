package memory

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/inmemory"
)

func prepareTestStore() *store[string] {
	cats := inmemory.NewBtree[sop.UUID, *Category](true)
	vecs := inmemory.NewBtree[VectorKey, Vector](true)
	items := inmemory.NewBtree[ItemKey, Item[string]](true)
	pathTree := inmemory.NewBtree[string, sop.UUID](false)
	distTree := inmemory.NewBtree[DistanceKey, byte](false)
	docsTree := inmemory.NewBtree[sop.UUID, Document](false)

	s := NewStore[string]("test_kb", nil, cats.Btree, pathTree.Btree, distTree.Btree, vecs.Btree, items.Btree, docsTree.Btree).(*store[string])
	s.SetTextIndex(&MockTextIndex{})
	s.SetLLM(&MockPlaybookLLM{})
	return s
}

type semanticFallbackStore struct {
	*store[string]
	fallbackCats []*Category
	called       bool
	captured     [][]float32
}

type modeAwareCategoryEmbedder struct {
	categoryCalls int
	queryCalls    int
	textCalls     int
	lastTexts     []string
}

func (m *modeAwareCategoryEmbedder) Name() string { return "mode-aware-category" }
func (m *modeAwareCategoryEmbedder) Dim() int     { return 3 }
func (m *modeAwareCategoryEmbedder) EmbedTexts(context.Context, []string) ([][]float32, error) {
	m.textCalls++
	return [][]float32{{1, 0, 0}}, nil
}
func (m *modeAwareCategoryEmbedder) EmbedCategoryTexts(_ context.Context, texts []string) ([][]float32, error) {
	m.categoryCalls++
	m.lastTexts = append([]string(nil), texts...)
	return [][]float32{{1, 0, 0}}, nil
}
func (m *modeAwareCategoryEmbedder) EmbedDocumentTexts(context.Context, []string) ([][]float32, error) {
	return [][]float32{{0, 0, 1}}, nil
}
func (m *modeAwareCategoryEmbedder) EmbedQueryTexts(context.Context, []string) ([][]float32, error) {
	m.queryCalls++
	return [][]float32{{0, 1, 0}}, nil
}

var _ ai.Embeddings = (*modeAwareCategoryEmbedder)(nil)
var _ ai.EmbeddingModeSupport = (*modeAwareCategoryEmbedder)(nil)

func (s *semanticFallbackStore) SemanticCategoryByPath(ctx context.Context, pathVectors [][]float32) ([]*Category, error) {
	s.called = true
	s.captured = append([][]float32(nil), pathVectors...)
	return s.fallbackCats, nil
}

func TestEmbedCategoryPath_NormalizesLanguageTokens(t *testing.T) {
	ctx := context.Background()
	embedder := &modeAwareCategoryEmbedder{}

	vecs, err := embedCategoryPath(ctx, "Language Bindings/C#", embedder)
	if err != nil {
		t.Fatalf("embedCategoryPath failed: %v", err)
	}
	if len(vecs) != 1 || len(vecs[0]) != 3 {
		t.Fatalf("embedCategoryPath returned unexpected vectors: %#v", vecs)
	}
	if got := embedder.lastTexts; !reflect.DeepEqual(got, []string{"language bindings", "csharp"}) {
		t.Fatalf("embedCategoryPath normalized tokens = %#v, want %#v", got, []string{"language bindings", "csharp"})
	}
}

func TestBatchCategories(t *testing.T) {
	ctx := context.Background()
	s := prepareTestStore()
	kb := &KnowledgeBase[string]{Store: s}

	rootID := sop.NewUUID()
	childID := sop.NewUUID()

	// 1. Upsert Categories
	err := kb.UpsertCategories(ctx, []UpsertCategoryParam{
		{
			Category: &Category{ID: rootID, Name: "Root"},
		},
		{
			ParentIDs: []sop.UUID{rootID},
			Category:  &Category{ID: childID, Name: "Child"},
		},
	})
	if err != nil {
		t.Fatalf("UpsertCategories failed: %v", err)
	}

	// 2. List Categories

	allCats, allCount, err := kb.ListCategories(ctx, ListCategoriesParam{Limit: 10})
	if err != nil {
		t.Fatalf("ListCategories failed: %v", err)
	}
	if allCount != 2 {
		t.Errorf("Expected 2 categories, got %d", allCount)
	}
	if len(allCats) != 2 {
		t.Errorf("Expected 2 categories in slice, got %d", len(allCats))
	}

	rootCats, rootCount, err := kb.ListCategories(ctx, ListCategoriesParam{Limit: 10, ParentPath: "/"})
	if err != nil {
		t.Fatalf("ListCategories failed: %v", err)
	}
	if rootCount != 1 {
		t.Errorf("Expected 1 root category, got %d", rootCount)
	}
	if len(rootCats) != 1 {
		t.Errorf("Expected 1 root category in slice, got %d", len(rootCats))
	}

	// 3. Delete Categories
	err = kb.DeleteCategories(ctx, []sop.UUID{childID})
	if err != nil {
		t.Fatalf("DeleteCategories failed: %v", err)
	}

	// 4. Verify Deletion
	_, countC, err := kb.ListCategories(ctx, ListCategoriesParam{Limit: 10})
	if err != nil {
		t.Fatalf("ListCategories failed: %v", err)
	}
	if countC != 1 {
		t.Errorf("Expected 1 category after deletion, got %d", countC)
	}
}

func TestBatchItemsAndSearch(t *testing.T) {
	ctx := context.Background()
	s := prepareTestStore()
	kb := &KnowledgeBase[string]{Store: s}

	catID := sop.NewUUID()
	err := kb.UpsertCategories(ctx, []UpsertCategoryParam{
		{Category: &Category{ID: catID, Name: "DataCategory"}},
	})
	if err != nil {
		t.Fatalf("UpsertCategories failed: %v", err)
	}

	// Also map the path explicitly since SearchByPath relies on CategoriesByPath
	catPathTree, _ := s.CategoriesByPath(ctx)
	catPathTree.Add(ctx, "DataCategory", catID)

	item1ID := sop.NewUUID()
	item2ID := sop.NewUUID()

	err = kb.UpsertItems(ctx, []UpsertItemParam[string]{
		{
			CategoryID: catID,
			Item:       &Item[string]{ID: item1ID, CategoryID: catID, Data: "item data 1", Summaries: []string{"Searchable Item 1"}},
		},
		{
			CategoryID: catID,
			Item:       &Item[string]{ID: item2ID, CategoryID: catID, Data: "item data 2", Summaries: []string{"Hidden Item 2"}},
		},
	})
	if err != nil {
		t.Fatalf("UpsertItems failed: %v", err)
	}

	// Test ListItems
	_, count, err := kb.ListItems(ctx, ListItemsParam{CategoryPath: "DataCategory", Limit: 10})
	if err != nil {
		t.Fatalf("ListItems failed: %v", err)
	}
	if count != 2 {
		t.Errorf("Expected 2 items, got %d", count)
	}

	// Test SearchByPath
	res, err := kb.SearchByPath(ctx, []PathSearchParam{
		{CategoryPath: "DataCategory", SearchText: "Searchable"},
	})
	if err != nil {
		t.Fatalf("SearchByPath failed: %v", err)
	}
	if len(res) != 1 {
		t.Errorf("Expected 1 search result, got %d", len(res))
	} else if !strings.HasPrefix(res[0].Summaries[0], "Searchable") {
		t.Errorf("Expected item with 'Searchable', got '%s'", res[0].Summaries[0])
	}

	// Test DeleteItems
	err = kb.DeleteItems(ctx, []ItemKey{{CategoryID: catID, ItemID: item1ID}})
	if err != nil {
		t.Fatalf("DeleteItems failed: %v", err)
	}

	_, count, err = kb.ListItems(ctx, ListItemsParam{CategoryPath: "DataCategory", Limit: 10})
	if err != nil {
		t.Fatalf("ListItems failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 item after deletion, got %d", count)
	}
}

func TestKnowledgeBase_SearchByPath_UsesSemanticFallbackPath(t *testing.T) {
	ctx := context.Background()
	base := prepareTestStore()
	catID := sop.NewUUID()
	itemID := sop.NewUUID()

	fallback := &semanticFallbackStore{
		store:        base,
		fallbackCats: []*Category{{ID: catID, Name: "Semantic Fallback"}},
	}
	kb := &KnowledgeBase[string]{
		Store:   fallback,
		Manager: NewMemoryManager[string](fallback, &MockLLM{}, &MockPlaybookEmbedder{Rules: []PlaybookRule{{Keywords: []string{"missing"}, Vector: []float32{1, 0, 0}}}}),
	}

	if _, err := base.AddCategory(ctx, &Category{ID: catID, Name: "Semantic Fallback"}); err != nil {
		t.Fatalf("AddCategory failed: %v", err)
	}

	itemsTree, err := base.Items(ctx)
	if err != nil {
		t.Fatalf("Items failed: %v", err)
	}
	if _, err := itemsTree.Add(ctx, ItemKey{CategoryID: catID, ItemID: itemID}, Item[string]{ID: itemID, CategoryID: catID, Summaries: []string{"Semantic fallback result"}}); err != nil {
		t.Fatalf("Add item failed: %v", err)
	}

	res, err := kb.SearchByPath(ctx, []PathSearchParam{{CategoryPath: "Missing/Path", SearchText: "Semantic"}})
	if err != nil {
		t.Fatalf("SearchByPath failed: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 semantic fallback result, got %d", len(res))
	}
	if !fallback.called {
		t.Fatal("expected SemanticCategoryByPath to be invoked")
	}
	if len(fallback.captured) != 2 {
		t.Fatalf("expected two embedded path parts, got %d", len(fallback.captured))
	}
	if !strings.HasPrefix(res[0].Summaries[0], "Semantic") {
		t.Fatalf("expected semantic fallback item, got %q", res[0].Summaries[0])
	}
}

func TestKnowledgeBase_SearchByPath_SkipsWhenSemanticFallbackFindsNothing(t *testing.T) {
	ctx := context.Background()
	base := prepareTestStore()

	fallback := &semanticFallbackStore{store: base}
	kb := &KnowledgeBase[string]{
		Store:   fallback,
		Manager: NewMemoryManager[string](fallback, &MockLLM{}, &MockPlaybookEmbedder{Rules: []PlaybookRule{{Keywords: []string{"missing"}, Vector: []float32{1, 0, 0}}}}),
	}

	res, err := kb.SearchByPath(ctx, []PathSearchParam{{CategoryPath: "Missing/Path", SearchText: "Semantic"}})
	if err != nil {
		t.Fatalf("SearchByPath failed: %v", err)
	}
	if len(res) != 0 {
		t.Fatalf("expected no fallback results, got %d", len(res))
	}
	if !fallback.called {
		t.Fatal("expected SemanticCategoryByPath to be invoked")
	}
}

func TestChooseCategoryAnchor_NormalizesProgrammingLanguageTokens(t *testing.T) {
	if got := chooseCategoryAnchor("Language Bindings/C#", "", ""); got != "language bindings csharp" {
		t.Fatalf("chooseCategoryAnchor() = %q, want %q", got, "language bindings csharp")
	}
}

func TestRefreshSemanticVectors_UsesCategoryEmbeddingMode(t *testing.T) {
	ctx := context.Background()
	s := prepareTestStore()
	s.SetDomainReference([]float32{1, 0, 0})

	embedder := &modeAwareCategoryEmbedder{}
	kb := &KnowledgeBase[string]{
		Store:   s,
		Manager: NewMemoryManager[string](s, &MockLLM{}, embedder),
	}

	rootID := sop.NewUUID()
	if _, err := s.AddCategory(ctx, &Category{
		ID:         rootID,
		Name:       "Root",
		VectorHash: "stale",
	}); err != nil {
		t.Fatalf("AddCategory(root) failed: %v", err)
	}

	if err := refreshSemanticVectors(ctx, kb); err != nil {
		t.Fatalf("refreshSemanticVectors failed: %v", err)
	}
	if embedder.categoryCalls == 0 {
		t.Fatal("expected category embedding mode to be used for category vectors")
	}
	if embedder.textCalls != 0 {
		t.Fatalf("expected generic EmbedTexts not to be used for category vectors, got %d calls", embedder.textCalls)
	}
}

func TestRefreshSemanticVectors_RebuildsEmptyDistanceIndex(t *testing.T) {
	ctx := context.Background()
	s := prepareTestStore()
	s.SetDomainReference([]float32{1, 0, 0})

	embedder := &MockPlaybookEmbedder{Rules: []PlaybookRule{
		{Keywords: []string{"root"}, Vector: []float32{1, 0, 0}},
		{Keywords: []string{"child"}, Vector: []float32{0, 1, 0}},
	}}
	kb := &KnowledgeBase[string]{
		Store:   s,
		Manager: NewMemoryManager[string](s, &MockLLM{}, embedder),
	}

	rootID := sop.NewUUID()
	childID := sop.NewUUID()
	if _, err := s.AddCategory(ctx, &Category{
		ID:           rootID,
		Name:         "Root",
		CenterVector: []float32{1, 0, 0},
		VectorHash:   ComputeVectorHash(3, "Root"),
	}); err != nil {
		t.Fatalf("AddCategory(root) failed: %v", err)
	}
	if _, err := s.AddCategory(ctx, &Category{
		ID:           childID,
		Name:         "Child",
		CenterVector: []float32{0, 1, 0},
		VectorHash:   ComputeVectorHash(3, "Child"),
		ParentIDs:    []CategoryParent{{ParentID: rootID}},
	}); err != nil {
		t.Fatalf("AddCategory(child) failed: %v", err)
	}

	catsByDist, err := s.CategoriesByDistance(ctx)
	if err != nil {
		t.Fatalf("CategoriesByDistance failed: %v", err)
	}
	if catsByDist.Count() != 0 {
		t.Fatalf("expected empty distance index before refresh, got %d", catsByDist.Count())
	}

	if err := refreshSemanticVectors(ctx, kb); err != nil {
		t.Fatalf("refreshSemanticVectors failed: %v", err)
	}
	if catsByDist.Count() == 0 {
		t.Fatal("expected refreshSemanticVectors to rebuild an empty CategoriesByDistance index")
	}
}

func TestKnowledgeBase_SearchByPath_PrefixMatchesSummaries(t *testing.T) {
	ctx := context.Background()
	cats := inmemory.NewBtree[sop.UUID, *Category](true)
	vecs := inmemory.NewBtree[VectorKey, Vector](true)
	items := inmemory.NewBtree[ItemKey, Item[string]](true)
	pathTree := inmemory.NewBtree[string, sop.UUID](false)
	distTree := inmemory.NewBtree[DistanceKey, byte](false)
	docsTree := inmemory.NewBtree[sop.UUID, Document](false)

	s := NewStore[string]("semantic_kb", nil, cats.Btree, pathTree.Btree, distTree.Btree, vecs.Btree, items.Btree, docsTree.Btree).(*store[string])
	embedder := &MockPlaybookEmbedder{Rules: []PlaybookRule{
		{Keywords: []string{"root"}, Vector: []float32{1, 0, 0}},
		{Keywords: []string{"engineering"}, Vector: []float32{0, 1, 0}},
		{Keywords: []string{"architecture"}, Vector: []float32{0, 0, 1}},
	}}
	kb := &KnowledgeBase[string]{
		Store:   s,
		Manager: NewMemoryManager[string](s, &MockLLM{}, embedder),
	}

	rootID := sop.NewUUID()
	engID := sop.NewUUID()
	archID := sop.NewUUID()
	itemID := sop.NewUUID()

	cats.Btree.Add(ctx, rootID, &Category{ID: rootID, Name: "Root", Path: "Root", CenterVector: []float32{1, 0, 0}})
	cats.Btree.Add(ctx, engID, &Category{ID: engID, Name: "Engineering", Path: "Root/Engineering", CenterVector: []float32{0, 1, 0}, ParentIDs: []CategoryParent{{ParentID: rootID}}})
	cats.Btree.Add(ctx, archID, &Category{ID: archID, Name: "Architecture", Path: "Root/Engineering/Architecture", CenterVector: []float32{0, 0, 1}, ParentIDs: []CategoryParent{{ParentID: engID}}})
	pathTree.Btree.Add(ctx, "Root/Engineering/Architecture", archID)
	items.Btree.Add(ctx, ItemKey{CategoryID: archID, ItemID: itemID}, Item[string]{ID: itemID, CategoryID: archID, Summaries: []string{"Architecture guidance for new B-tree"}})

	res, err := kb.SearchByPath(ctx, []PathSearchParam{{CategoryPath: "Root/Engineering/Architecture", SearchText: "Architecture"}})
	if err != nil {
		t.Fatalf("SearchByPath failed: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("expected 1 prefix-match result, got %d", len(res))
	}
	if !strings.Contains(strings.Join(res[0].Summaries, " "), "Architecture") {
		t.Fatalf("expected prefix search to surface the Architecture item, got %+v", res[0].Summaries)
	}
}

func TestKnowledgeBaseEdgeCases(t *testing.T) {
	ctx := context.Background()
	s := prepareTestStore()
	s.SetDomainReference([]float32{0.0, 0.0, 0.0})
	kb := &KnowledgeBase[string]{
		Store:   s,
		Manager: NewMemoryManager[string](s, &MockLLM{}, &MockPlaybookEmbedder{}),
	}

	// Create 5 categories
	upserts := make([]UpsertCategoryParam, 5)
	for i := 0; i < 5; i++ {
		upserts[i] = UpsertCategoryParam{
			Category: &Category{ID: sop.NewUUID(), Name: "Cat"},
		}
	}
	_ = kb.UpsertCategories(ctx, upserts)

	// Test category pagination
	cats, _, _ := kb.ListCategories(ctx, ListCategoriesParam{Limit: 2, Offset: 1})
	if len(cats) != 2 {
		t.Errorf("Expected 2 categories from pagination, got %d", len(cats))
	}

	// Create 5 items in the first category
	catID := upserts[0].Category.ID
	itemUpserts := make([]UpsertItemParam[string], 5)
	for i := 0; i < 5; i++ {
		itemUpserts[i] = UpsertItemParam[string]{
			CategoryID: catID,
			Item:       &Item[string]{ID: sop.NewUUID(), CategoryID: catID, Data: "item"},
		}
	}
	_ = kb.UpsertItems(ctx, itemUpserts)

	// Test Item Pagination directly with offset
	items, count, _ := kb.ListItems(ctx, ListItemsParam{Limit: 2, Offset: 2})
	if len(items) != 2 {
		t.Errorf("Expected 2 items from pagination, got %d", len(items))
	}
	if count != 5 {
		t.Errorf("Expected total count 5, got %d", count)
	}

	// Edge Case: Empty ID or missing path Delete
	_ = kb.DeleteCategories(ctx, []sop.UUID{sop.NilUUID})
	_ = kb.DeleteItems(ctx, []ItemKey{{}})

	// Edge Case: SearchByPath with unmapped or empty category
	res, err := kb.SearchByPath(ctx, []PathSearchParam{
		{CategoryPath: "NonExistentPath", SearchText: "Hello"},
	})
	if err != nil {
		t.Errorf("Expected nil error for non existent path, got %v", err)
	}
	if len(res) != 0 {
		t.Errorf("Expected 0 results, got %d", len(res))
	}
}
