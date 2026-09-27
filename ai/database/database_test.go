package database_test

import (
	"context"
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/ai/vector"
	"github.com/sharedcode/joltrin/v5"
	core "github.com/sharedcode/joltrin/v5/database"
)

func TestAIDatabase_RemoveKnowledgeBase_RemovesAllDomainStores(t *testing.T) {
	storagePath := t.TempDir()

	db := database.NewDatabase(core.DatabaseOptions{
		StoresFolders: []string{storagePath},
	})

	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	if _, err := db.OpenKnowledgeBase(ctx, "tasks3", tx, nil, nil, false); err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}

	if _, err := db.OpenVectorStore(ctx, "tasks3", tx, vector.Config{UsageMode: ai.Dynamic}); err != nil {
		t.Fatalf("OpenVectorStore failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	before, err := db.GetDomains(ctx)
	if err != nil {
		t.Fatalf("GetDomains before delete failed: %v", err)
	}
	if len(before) == 0 {
		t.Fatal("expected tasks3 domain to exist before deletion")
	}

	if err := db.RemoveKnowledgeBase(ctx, "tasks3"); err != nil {
		t.Fatalf("RemoveKnowledgeBase failed: %v", err)
	}

	after, err := db.GetDomains(ctx)
	if err != nil {
		t.Fatalf("GetDomains after delete failed: %v", err)
	}
	for _, name := range after {
		if name == "tasks3" {
			t.Fatalf("expected tasks3 to be removed from domains, got %v", after)
		}
	}

	verifyTx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		t.Fatalf("BeginTransaction verify failed: %v", err)
	}
	stores, err := verifyTx.GetStores(ctx)
	_ = verifyTx.Rollback(ctx)
	if err != nil {
		t.Fatalf("GetStores verify failed: %v", err)
	}
	for _, store := range stores {
		if len(store) >= len("tasks3/") && store[:len("tasks3/")] == "tasks3/" {
			t.Fatalf("expected no remaining tasks3 stores, found %q", store)
		}
	}
}

func TestAIDatabase_RemoveKnowledgeBase_AcceptsDataSuffix(t *testing.T) {
	storagePath := t.TempDir()

	db := database.NewDatabase(core.DatabaseOptions{
		StoresFolders: []string{storagePath},
	})

	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	if _, err := db.OpenKnowledgeBase(ctx, "tasks4", tx, nil, nil, false); err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	if err := db.RemoveKnowledgeBase(ctx, "tasks4/data"); err != nil {
		t.Fatalf("RemoveKnowledgeBase with /data suffix failed: %v", err)
	}
}

func TestAIDatabase_RemoveKnowledgeBase_SuggestsCorrectCase(t *testing.T) {
	storagePath := t.TempDir()

	db := database.NewDatabase(core.DatabaseOptions{
		StoresFolders: []string{storagePath},
	})

	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	if _, err := db.OpenKnowledgeBase(ctx, "Tasks5", tx, nil, nil, false); err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	err = db.RemoveKnowledgeBase(ctx, "tasks5")
	if err == nil {
		t.Fatal("expected case-mismatched delete to fail")
	}
	if !strings.Contains(err.Error(), "did you mean 'Tasks5'") {
		t.Fatalf("expected case suggestion, got %v", err)
	}

	after, err := db.GetDomains(ctx)
	if err != nil {
		t.Fatalf("GetDomains after failed delete failed: %v", err)
	}
	found := false
	for _, name := range after {
		if name == "Tasks5" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected Tasks5 to remain after failed delete, got %v", after)
	}
}

func TestAIDatabase_RemoveKnowledgeBase_ReturnsNotFoundWhenMissing(t *testing.T) {
	storagePath := t.TempDir()

	db := database.NewDatabase(core.DatabaseOptions{
		StoresFolders: []string{storagePath},
	})

	ctx := context.Background()
	err := db.RemoveKnowledgeBase(ctx, "missing_space")
	if err == nil {
		t.Fatal("expected missing knowledge base delete to fail")
	}
}

func TestAIDatabase_OpenKnowledgeBase_ReopensLegacyStoreConfiguration(t *testing.T) {
	storagePath := t.TempDir()

	db := database.NewDatabase(core.DatabaseOptions{
		StoresFolders: []string{storagePath},
	})

	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	legacyStore := sop.ConfigureStore("kb_legacy/categories", true, 2, "legacy categories store", sop.BigData, "")
	if _, err := core.NewBtree[sop.UUID, *memory.Category](ctx, db.Config(), legacyStore.Name, tx, nil, legacyStore); err != nil {
		t.Fatalf("pre-creating legacy categories store failed: %v", err)
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	reopenTx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction reopen failed: %v", err)
	}
	if _, err := db.OpenKnowledgeBase(ctx, "kb_legacy", reopenTx, nil, nil, false); err != nil {
		t.Fatalf("expected OpenKnowledgeBase to reopen a legacy store without failing, got: %v", err)
	}
	if err := reopenTx.Rollback(ctx); err != nil {
		t.Fatalf("Rollback failed: %v", err)
	}
}

func TestAIDatabase_OpenKnowledgeBase_RespectsPersistedTextSearchSetting(t *testing.T) {
	storagePath := t.TempDir()

	db := database.NewDatabase(core.DatabaseOptions{
		StoresFolders: []string{storagePath},
	})

	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}
	kb, err := db.OpenKnowledgeBase(ctx, "kb_text_optional", tx, nil, nil, false, false)
	if err != nil {
		t.Fatalf("OpenKnowledgeBase failed: %v", err)
	}
	if err := kb.SetConfig(ctx, &memory.KnowledgeBaseConfig{TextSearchEnabled: false}); err != nil {
		t.Fatalf("SetConfig failed: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	tx2, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction reopen failed: %v", err)
	}
	if _, err := db.OpenKnowledgeBase(ctx, "kb_text_optional", tx2, nil, nil, false, true); err != nil {
		t.Fatalf("OpenKnowledgeBase reopen failed: %v", err)
	}
	stores, err := tx2.GetStores(ctx)
	_ = tx2.Rollback(ctx)
	if err != nil {
		t.Fatalf("GetStores failed: %v", err)
	}
	for _, store := range stores {
		if store == "kb_text_optional/text" {
			t.Fatalf("expected persisted KB text-search setting to prevent creating text index store, found %q", store)
		}
	}
}

func TestAIDatabase_NewKnowledgeBase_ProvidesSimpleCRUDFlow(t *testing.T) {
	storagePath := t.TempDir()

	ctx := context.Background()
	kb, err := database.NewKnowledgeBase(ctx, "simple_kb", sop.DatabaseOptions{StoresFolders: []string{storagePath}}, nil, nil, false)
	if err != nil {
		t.Fatalf("NewKnowledgeBase failed: %v", err)
	}

	catID := sop.NewUUID()
	if err := kb.UpsertCategories(ctx, []memory.UpsertCategoryParam{{
		Category: &memory.Category{ID: catID, Name: "Root", Path: "Root"},
	}}); err != nil {
		t.Fatalf("UpsertCategories failed: %v", err)
	}

	if err := kb.UpsertItems(ctx, []memory.UpsertItemParam[map[string]any]{{
		CategoryPath: "Root",
		Item: &memory.Item[map[string]any]{
			ID:   sop.NewUUID(),
			Data: map[string]any{"text": "hello world"},
		},
	}}); err != nil {
		t.Fatalf("UpsertItems failed: %v", err)
	}

	cats, total, err := kb.ListCategories(ctx, memory.ListCategoriesParam{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("ListCategories failed: %v", err)
	}
	if total != 1 {
		t.Fatalf("expected 1 category, got %d", total)
	}
	if len(cats) != 1 || cats[0].Name != "Root" {
		t.Fatalf("unexpected categories: %+v", cats)
	}

	results, err := kb.Search(ctx, []memory.SearchRequest[map[string]any]{{CategoryPath: "Root", Text: "hello", Limit: 5}})
	if err != nil {
		t.Fatalf("Search failed: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected no semantic hits without query vectors, got %d batches", len(results))
	}

	if err := kb.Close(ctx); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestAIDatabase_Standalone_ModelStore(t *testing.T) {
	storagePath := t.TempDir()

	db := database.NewDatabase(core.DatabaseOptions{
		StoresFolders: []string{storagePath},
	})

	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	ms, err := db.OpenModelStore(ctx, "models", tx)
	if err != nil {
		t.Fatalf("OpenModelStore failed: %v", err)
	}

	if ms == nil {
		t.Error("ModelStore is nil")
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}

func TestAIDatabase_Standalone_VectorStore(t *testing.T) {
	storagePath := t.TempDir()

	db := database.NewDatabase(core.DatabaseOptions{
		StoresFolders: []string{storagePath},
	})

	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	cfg := vector.Config{
		UsageMode: ai.Dynamic,
	}
	vs, err := db.OpenVectorStore(ctx, "vectors", tx, cfg)
	if err != nil {
		t.Fatalf("OpenVectorStore failed: %v", err)
	}

	if vs == nil {
		t.Error("VectorStore is nil")
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}

func TestAIDatabase_Standalone_Search(t *testing.T) {
	storagePath := t.TempDir()

	db := database.NewDatabase(core.DatabaseOptions{
		StoresFolders: []string{storagePath},
	})

	ctx := context.Background()
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		t.Fatalf("BeginTransaction failed: %v", err)
	}

	idx, err := db.OpenSearch(ctx, "search_index", tx)
	if err != nil {
		t.Fatalf("OpenSearch failed: %v", err)
	}

	if idx == nil {
		t.Error("Search Index is nil")
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}
}
