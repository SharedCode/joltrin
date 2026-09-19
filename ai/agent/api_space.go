package agent

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
)

// getDatabase resolves database by explicit name (NO Context fallback)
func (a *CopilotAgent) getDatabase(dbName string) (*database.Database, error) {
	if dbName == "" {
		return nil, fmt.Errorf("database parameter required")
	}
	if dbName == "system" && a.systemDB != nil {
		return a.systemDB, nil
	}
	if opts, ok := a.databases[dbName]; ok {
		return database.NewDatabase(opts), nil
	}
	return nil, fmt.Errorf("database not found: %s", dbName)
}

// CreateSpace creates or opens a Space/KnowledgeBase (first-class API)
func (a *CopilotAgent) CreateSpace(ctx context.Context, args CreateSpaceArgs) (string, error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return "", err
	}
	if args.KBName == "" {
		return "", fmt.Errorf("kb_name required")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil && a.service.Domain().Embedder() != nil {
		embedder = a.service.Domain().Embedder()
	}

	_, err = db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return "", err
	}
	tx.Commit(ctx)

	emitSpaceMutationEvent(ctx, "create", args.Database, args.KBName)
	return fmt.Sprintf("Space/KnowledgeBase '%s' created/opened successfully.\n[[REFRESH_SPACES:%s]]", args.KBName, args.KBName), nil
}

// DeleteSpace deletes a Space/KnowledgeBase (first-class API)
func (a *CopilotAgent) DeleteSpace(ctx context.Context, args DeleteSpaceArgs) (string, error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return "", err
	}
	if args.KBName == "" {
		return "", fmt.Errorf("kb_name required")
	}

	err = db.RemoveKnowledgeBase(ctx, args.KBName)
	if err != nil {
		return "", err
	}

	emitSpaceMutationEvent(ctx, "delete", args.Database, args.KBName)
	return fmt.Sprintf("Space/KnowledgeBase %q deleted successfully.\n[[REFRESH_SPACES]]", args.KBName), nil
}

// UpdateSpaceConfig updates the configuration of a Space (first-class API)
func (a *CopilotAgent) UpdateSpaceConfig(ctx context.Context, args UpdateSpaceConfigArgs) (string, error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return "", err
	}
	if args.KBName == "" {
		return "", fmt.Errorf("kb_name required")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return "", err
	}

	err = kb.SetConfig(ctx, &args.Config)
	if err != nil {
		return "", err
	}
	tx.Commit(ctx)
	return fmt.Sprintf("Config updated for '%s'", args.KBName), nil
}

// ReadSpaceConfig reads the configuration of a Space (first-class API)
func (a *CopilotAgent) ReadSpaceConfig(ctx context.Context, args ReadSpaceConfigArgs) (*memory.KnowledgeBaseConfig, error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return nil, err
	}
	if args.KBName == "" {
		return nil, fmt.Errorf("kb_name required")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return nil, err
	}

	cfg, err := kb.GetConfig(ctx)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

// VectorizeSpace vectorizes an entire Space (first-class API)
func (a *CopilotAgent) VectorizeSpace(ctx context.Context, args VectorizeSpaceArgs) (string, error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return "", err
	}
	if args.KBName == "" {
		return "", fmt.Errorf("kb_name required")
	}

	batchSize := args.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}

	err = db.Vectorize(ctx, args.KBName, a.brain, embedder, batchSize)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Space '%s' vectorized.", args.KBName), nil
}

// UpsertCategory upserts a single category (first-class API)
func (a *CopilotAgent) UpsertCategory(ctx context.Context, args UpsertCategoryArgs) (string, error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return "", err
	}
	if args.KBName == "" {
		return "", fmt.Errorf("kb_name required")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return "", err
	}

	err = kb.UpsertCategories(ctx, []memory.UpsertCategoryParam{args.Parameter})
	if err != nil {
		return "", err
	}
	tx.Commit(ctx)
	return "Category upserted.", nil
}

// DeleteCategory deletes a single category (first-class API)
func (a *CopilotAgent) DeleteCategory(ctx context.Context, args DeleteCategoryArgs) (string, error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return "", err
	}
	if args.KBName == "" {
		return "", fmt.Errorf("kb_name required")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return "", err
	}

	err = kb.DeleteCategories(ctx, []sop.UUID{args.CategoryID})
	if err != nil {
		return "", err
	}
	tx.Commit(ctx)
	return "Category deleted.", nil
}

// ListCategories lists categories with pagination (first-class API)
func (a *CopilotAgent) ListCategories(ctx context.Context, args ListCategoriesArgs) (*CategoryListResult, error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return nil, err
	}
	if args.KBName == "" {
		return nil, fmt.Errorf("kb_name required")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return nil, err
	}

	var p memory.ListCategoriesParam
	p.Limit = args.Limit
	if p.Limit <= 0 {
		p.Limit = 100
	}
	p.Offset = args.Offset
	p.ParentPath = args.ParentPath

	cats, total, err := kb.ListCategories(ctx, p)
	if err != nil {
		return nil, err
	}
	return &CategoryListResult{Categories: cats, Total: total}, nil
}

// UpsertItem upserts a single item (first-class API)
func (a *CopilotAgent) UpsertItem(ctx context.Context, args UpsertItemArgs) (string, error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return "", err
	}
	if args.KBName == "" {
		return "", fmt.Errorf("kb_name required")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return "", err
	}

	err = kb.UpsertItems(ctx, []memory.UpsertItemParam[map[string]any]{args.Parameter})
	if err != nil {
		return "", err
	}
	tx.Commit(ctx)
	return "Item upserted.", nil
}

// DeleteItem deletes a single item (first-class API)
func (a *CopilotAgent) DeleteItem(ctx context.Context, args DeleteItemArgs) (string, error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return "", err
	}
	if args.KBName == "" {
		return "", fmt.Errorf("kb_name required")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return "", err
	}

	itemKeys := []memory.ItemKey{{CategoryID: args.CategoryID, ItemID: args.ItemID}}
	err = kb.DeleteItems(ctx, itemKeys)
	if err != nil {
		return "", err
	}
	tx.Commit(ctx)
	return "Item deleted.", nil
}

// ListItems lists items with pagination (first-class API)
func (a *CopilotAgent) ListItems(ctx context.Context, args ListItemsArgs) (*ItemListResult, error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return nil, err
	}
	if args.KBName == "" {
		return nil, fmt.Errorf("kb_name required")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return nil, err
	}

	var p memory.ListItemsParam
	if !args.CategoryID.IsNil() {
		p.CategoryID = args.CategoryID
	}
	p.Limit = args.Limit
	if p.Limit <= 0 {
		p.Limit = 100
	}
	p.Offset = args.Offset

	items, total, err := kb.ListItems(ctx, p)
	if err != nil {
		return nil, err
	}
	return &ItemListResult{Items: items, Total: total}, nil
}

// SearchItemsByPath searches items by path (first-class API)
func (a *CopilotAgent) SearchItemsByPath(ctx context.Context, args SearchItemsByPathArgs) ([]memory.Item[map[string]any], error) {
	db, err := a.getDatabase(args.Database)
	if err != nil {
		return nil, err
	}
	if args.KBName == "" {
		return nil, fmt.Errorf("kb_name required")
	}
	if len(args.Parameters) == 0 {
		return nil, fmt.Errorf("parameters required")
	}

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, args.KBName, tx, a.brain, embedder, false)
	if err != nil {
		return nil, err
	}

	items, err := kb.SearchByPath(ctx, args.Parameters)
	if err != nil {
		return nil, err
	}
	return items, nil
}
