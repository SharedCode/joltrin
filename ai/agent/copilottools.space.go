package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

const mintToSpaceArgsSchema = `{"type":"object","properties":{"kb_name":{"type":"string","description":"Exact target knowledge base name. Use the user-requested Space name even when the Space does not exist yet."},"content":{"type":"string","description":"Durable generated or discovered content to persist. Generate the content first, then store it with mint_to_space."},"category":{"type":"string","description":"Optional category label grouping related entries inside the target Space."}},"required":["kb_name","content"]}`

const deleteSpaceArgsSchema = `{"type":"object","properties":{"database":{"type":"string","description":"Optional database override. Defaults to the active session database."},"kb_name":{"type":"string","description":"Exact knowledge base name to delete. Use this only for full Space deletion, not normal content changes."}},"required":["kb_name"]}`

const enrichSpaceArgsSchema = `{"type":"object","properties":{"database":{"type":"string","description":"Optional database override. Defaults to the active session database."},"kb_name":{"type":"string","description":"Target knowledge base name whose derived knowledge should be refreshed or enriched."}},"required":["kb_name"]}`

const updateSpaceConfigArgsSchema = `{"type":"object","properties":{"database":{"type":"string","description":"Optional database override. Defaults to the active session database."},"kb_name":{"type":"string","description":"Exact target knowledge base name whose behavior should change."},"config":{"type":"object","description":"Grounded knowledge base configuration object to persist after inspecting the current config when needed."}},"required":["kb_name","config"]}`

const readSpaceConfigArgsSchema = `{"type":"object","properties":{"database":{"type":"string","description":"Optional database override. Defaults to the active session database."},"kb_name":{"type":"string","description":"Exact target knowledge base name whose routing rules, system prompts, persona settings, or enabled tool access should be inspected."}},"required":["kb_name"]}`

const vectorizeSpaceArgsSchema = `{"type":"object","properties":{"database":{"type":"string","description":"Optional database override. Defaults to the active session database."},"kb_name":{"type":"string","description":"Exact target knowledge base name to vectorize. Use only when the user explicitly asks for embeddings, reindexing, vectorization, or semantic refresh of the whole Space."},"batch_size":{"type":"integer","description":"Optional vectorization batch size for full-Space reconciliation."}},"required":["kb_name"]}`

const vectorizeSpaceCategoriesArgsSchema = `{"type":"object","properties":{"database":{"type":"string","description":"Optional database override. Defaults to the active session database."},"kb_name":{"type":"string","description":"Exact target knowledge base name whose selected categories should be vectorized. Prefer this over full-Space vectorization when the request is narrower."},"batch_size":{"type":"integer","description":"Optional vectorization batch size for the selected categories."},"category_ids":{"type":"array","description":"Category UUIDs to vectorize for a topic-wide refresh within the target Space.","items":{"type":"string"}}},"required":["kb_name"]}`

const vectorizeSpaceItemsArgsSchema = `{"type":"object","properties":{"database":{"type":"string","description":"Optional database override. Defaults to the active session database."},"kb_name":{"type":"string","description":"Exact target knowledge base name whose specific items should be vectorized. Prefer this over category-wide or full-Space vectorization when only a few changed items need refresh."},"batch_size":{"type":"integer","description":"Optional vectorization batch size for the selected items."},"category_id":{"type":"string","description":"Optional category UUID scoping the items to vectorize."},"item_ids":{"type":"array","description":"Item UUIDs to vectorize for a tightly scoped semantic refresh.","items":{"type":"string"}}},"required":["kb_name"]}`

func (a *CopilotAgent) registerSpaceTools(ctx context.Context) {
	a.registry.RegisterWithUI("mint_to_space", "Stores generated or durable knowledge in a target Space using the exact requested kb_name.", MintToSpaceInstruction, mintToSpaceArgsSchema, wrapStringTool(a.toolMintToSpace))
	a.registry.RegisterWithUI("delete_space", "Deletes an entire Space only after explicit user intent.", DeleteSpaceInstruction, deleteSpaceArgsSchema, wrapStringTool(a.toolDeleteSpace))
	a.registry.RegisterWithUI("enrich_space", "Reruns Space enrichment when derived knowledge needs an explicit refresh.", EnrichSpaceInstruction, enrichSpaceArgsSchema, wrapStringTool(a.toolEnrichSpace))
	a.registry.RegisterWithUI("update_space_config", "Changes routing or behavior settings for a Space after grounded config inspection.", UpdateSpaceConfigInstruction, updateSpaceConfigArgsSchema, wrapStringTool(a.toolUpdateSpaceConfig))
	a.registry.RegisterWithUI("read_space_config", "Reads the current config for a Space before behavior changes.", ReadSpaceConfigInstruction, readSpaceConfigArgsSchema, wrapStringTool(a.toolReadSpaceConfig))
	a.registry.RegisterWithUI("vectorize_space", "Refreshes embeddings for an entire Space only on explicit vectorization requests.", VectorizeSpaceInstruction, vectorizeSpaceArgsSchema, wrapStringTool(a.toolVectorizeSpace))
	a.registry.RegisterWithUI("vectorize_space_categories", "Refreshes embeddings for selected Space categories on explicit request.", VectorizeSpaceCategoriesInstruction, vectorizeSpaceCategoriesArgsSchema, wrapStringTool(a.toolVectorizeCategories))
	a.registry.RegisterWithUI("vectorize_space_items", "Refreshes embeddings for specific Space items on explicit request.", VectorizeSpaceItemsInstruction, vectorizeSpaceItemsArgsSchema, wrapStringTool(a.toolVectorizeItems))
}

func emitSpaceMutationEvent(ctx context.Context, action string, databaseName string, kbName string) {
	if strings.TrimSpace(kbName) == "" {
		return
	}
	if streamer, ok := ctx.Value(ai.CtxKeyEventStreamer).(func(string, any)); ok && streamer != nil {
		streamer("space_mutation", map[string]any{
			"action":   action,
			"database": strings.TrimSpace(databaseName),
			"kb_name":  strings.TrimSpace(kbName),
		})
	}
}

// Helper to get db
func (a *CopilotAgent) getDBForSpaceContext(ctx context.Context, args map[string]any) (*database.Database, error) {
	p := ai.GetSessionPayload(ctx)
	var dbName string
	if val, ok := args["database"].(string); ok && val != "" {
		dbName = val
	} else if p != nil {
		dbName = p.CurrentDB
	}

	if dbName != "" {
		if dbName == "system" && a.systemDB != nil {
			return a.systemDB, nil
		} else if opts, ok := a.databases[dbName]; ok {
			return database.NewDatabase(opts), nil
		}
	}
	return nil, fmt.Errorf("database not found: %s", dbName)
}

func (a *CopilotAgent) toolCreateSpace(ctx context.Context, args map[string]any) (string, error) {
	db, err := a.getDBForSpaceContext(ctx, args)
	if err != nil {
		return "", err
	}
	kbName, _ := args["kb_name"].(string)
	if kbName == "" {
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

	_, err = db.OpenKnowledgeBase(ctx, kbName, tx, a.brain, embedder, false)
	if err != nil {
		return "", err
	}
	tx.Commit(ctx)
	databaseName := ""
	if p := ai.GetSessionPayload(ctx); p != nil {
		databaseName = p.CurrentDB
	}
	emitSpaceMutationEvent(ctx, "create", databaseName, kbName)
	return fmt.Sprintf("Space/KnowledgeBase '%s' created/opened successfully.\n[[REFRESH_SPACES:%s]]", kbName, kbName), nil
}

func (a *CopilotAgent) toolDeleteSpace(ctx context.Context, args map[string]any) (string, error) {
	// Extract parameters from args and Context
	p := ai.GetSessionPayload(ctx)
	databaseName, _ := args["database"].(string)
	if databaseName == "" && p != nil {
		databaseName = p.CurrentDB
	}

	kbName, _ := args["kb_name"].(string)
	if kbName == "" {
		if fallback, ok := args["name"].(string); ok && fallback != "" {
			kbName = fallback
		} else if fallback, ok := args["space_name"].(string); ok && fallback != "" {
			kbName = fallback
		} else if fallback, ok := args["space"].(string); ok && fallback != "" {
			kbName = fallback
		} else if p != nil {
			if inferred, ok := parseDeleteSpaceRequest(p.CurrentUserQuery); ok {
				kbName = inferred
			}
		}
	}

	// Delegate to typed API with explicit parameters
	result, err := a.DeleteSpace(ctx, DeleteSpaceArgs{
		Database: databaseName,
		KBName:   kbName,
	})
	if err != nil {
		return "", err
	}
	return result + "\n[[REFRESH_SPACES]]", nil
}

func (a *CopilotAgent) toolUpdateSpaceConfig(ctx context.Context, args map[string]any) (string, error) {
	// Extract parameters from args and Context
	p := ai.GetSessionPayload(ctx)
	databaseName, _ := args["database"].(string)
	if databaseName == "" && p != nil {
		databaseName = p.CurrentDB
	}

	kbName, _ := args["kb_name"].(string)
	configMap, _ := args["config"].(map[string]any)

	// Convert config map to struct
	configBytes, _ := json.Marshal(configMap)
	var config memory.KnowledgeBaseConfig
	json.Unmarshal(configBytes, &config)

	// Delegate to typed API with explicit parameters
	return a.UpdateSpaceConfig(ctx, UpdateSpaceConfigArgs{
		Database: databaseName,
		KBName:   kbName,
		Config:   config,
	})
}

func (a *CopilotAgent) toolReadSpaceConfig(ctx context.Context, args map[string]any) (string, error) {
	// Extract parameters from args and Context
	p := ai.GetSessionPayload(ctx)
	databaseName, _ := args["database"].(string)
	if databaseName == "" && p != nil {
		databaseName = p.CurrentDB
	}

	kbName, _ := args["kb_name"].(string)

	// Delegate to typed API with explicit parameters
	cfg, err := a.ReadSpaceConfig(ctx, ReadSpaceConfigArgs{
		Database: databaseName,
		KBName:   kbName,
	})
	if err != nil {
		return "", err
	}
	b, _ := json.Marshal(cfg)
	return string(b), nil
}

func (a *CopilotAgent) toolVectorizeSpace(ctx context.Context, args map[string]any) (string, error) {
	// Extract parameters from args and Context
	p := ai.GetSessionPayload(ctx)
	databaseName, _ := args["database"].(string)
	if databaseName == "" && p != nil {
		databaseName = p.CurrentDB
	}

	kbName, _ := args["kb_name"].(string)
	batchSize := 100
	if val, ok := args["batch_size"].(float64); ok && val > 0 {
		batchSize = int(val)
	}

	// Delegate to typed API with explicit parameters
	return a.VectorizeSpace(ctx, VectorizeSpaceArgs{
		Database:  databaseName,
		KBName:    kbName,
		BatchSize: batchSize,
	})
}

func (a *CopilotAgent) toolVectorizeCategories(ctx context.Context, args map[string]any) (string, error) {
	// Extract parameters from args and Context
	p := ai.GetSessionPayload(ctx)
	databaseName, _ := args["database"].(string)
	if databaseName == "" && p != nil {
		databaseName = p.CurrentDB
	}

	kbName, _ := args["kb_name"].(string)
	batchSize := 100
	if val, ok := args["batch_size"].(float64); ok && val > 0 {
		batchSize = int(val)
	}

	idsRaw, _ := args["category_ids"].([]any)
	var ids []sop.UUID
	for _, v := range idsRaw {
		if s, ok := v.(string); ok {
			u, e := sop.ParseUUID(s)
			if e == nil {
				ids = append(ids, u)
			}
		}
	}

	// Delegate to typed API with explicit parameters
	result, err := a.BulkVectorizeCategories(ctx, BulkVectorizeCategoriesArgs{
		Database:    databaseName,
		KBName:      kbName,
		BatchSize:   batchSize,
		CategoryIDs: ids,
	})
	if err != nil {
		return "", err
	}
	if result.Success {
		return "Categories vectorized.", nil
	}
	return fmt.Sprintf("Categories vectorized with errors: %d processed, %d failed", result.Processed, result.Failed), nil
}

func (a *CopilotAgent) toolVectorizeItems(ctx context.Context, args map[string]any) (string, error) {
	// Extract parameters from args and Context
	p := ai.GetSessionPayload(ctx)
	databaseName, _ := args["database"].(string)
	if databaseName == "" && p != nil {
		databaseName = p.CurrentDB
	}

	kbName, _ := args["kb_name"].(string)
	batchSize := 100
	if val, ok := args["batch_size"].(float64); ok {
		batchSize = int(val)
	}

	var catID *sop.UUID
	if val, ok := args["category_id"].(string); ok && val != "" {
		u, e := sop.ParseUUID(val)
		if e == nil {
			catID = &u
		}
	}

	idsRaw, _ := args["item_ids"].([]any)
	var itemIDs []sop.UUID
	for _, v := range idsRaw {
		if s, ok := v.(string); ok {
			u, e := sop.ParseUUID(s)
			if e == nil {
				itemIDs = append(itemIDs, u)
			}
		}
	}

	// Delegate to typed API with explicit parameters
	result, err := a.BulkVectorizeItems(ctx, BulkVectorizeItemsArgs{
		Database:   databaseName,
		KBName:     kbName,
		BatchSize:  batchSize,
		CategoryID: catID,
		ItemIDs:    itemIDs,
	})
	if err != nil {
		return "", err
	}
	if result.Success {
		return "Items vectorized.", nil
	}
	return fmt.Sprintf("Items vectorized with errors: %d processed, %d failed", result.Processed, result.Failed), nil
}

func (a *CopilotAgent) toolUpsertCategories(ctx context.Context, args map[string]any) (string, error) {
	// Extract parameters from args and Context
	p := ai.GetSessionPayload(ctx)
	databaseName, _ := args["database"].(string)
	if databaseName == "" && p != nil {
		databaseName = p.CurrentDB
	}

	kbName, _ := args["kb_name"].(string)

	paramsRaw, _ := args["parameters"].([]any)
	b, _ := json.Marshal(paramsRaw)
	var params []memory.UpsertCategoryParam
	json.Unmarshal(b, &params)

	// Delegate to typed API with explicit parameters
	result, err := a.BulkUpsertCategories(ctx, BulkUpsertCategoriesArgs{
		Database:   databaseName,
		KBName:     kbName,
		Parameters: params,
	})
	if err != nil {
		return "", err
	}
	if result.Success {
		return "Categories upserted.", nil
	}
	return fmt.Sprintf("Categories upserted with errors: %d processed, %d failed", result.Processed, result.Failed), nil
}

func (a *CopilotAgent) toolDeleteCategories(ctx context.Context, args map[string]any) (string, error) {
	// Extract parameters from args and Context
	p := ai.GetSessionPayload(ctx)
	databaseName, _ := args["database"].(string)
	if databaseName == "" && p != nil {
		databaseName = p.CurrentDB
	}

	kbName, _ := args["kb_name"].(string)

	idsRaw, _ := args["category_ids"].([]any)
	var ids []sop.UUID
	for _, v := range idsRaw {
		if s, ok := v.(string); ok {
			u, _ := sop.ParseUUID(s)
			ids = append(ids, u)
		}
	}

	// Delegate to typed API with explicit parameters
	result, err := a.BulkDeleteCategories(ctx, BulkDeleteCategoriesArgs{
		Database:    databaseName,
		KBName:      kbName,
		CategoryIDs: ids,
	})
	if err != nil {
		return "", err
	}
	if result.Success {
		return "Categories deleted.", nil
	}
	return fmt.Sprintf("Categories deleted with errors: %d processed, %d failed", result.Processed, result.Failed), nil
}

func (a *CopilotAgent) toolListCategories(ctx context.Context, args map[string]any) (string, error) {
	// Extract parameters from args and Context
	p := ai.GetSessionPayload(ctx)
	databaseName, _ := args["database"].(string)
	if databaseName == "" && p != nil {
		databaseName = p.CurrentDB
	}

	kbName, _ := args["kb_name"].(string)

	limit := 100
	if val, ok := args["limit"].(float64); ok && val > 0 {
		limit = int(val)
	}
	offset := 0
	if val, ok := args["offset"].(float64); ok && val > 0 {
		offset = int(val)
	}
	parentPath, _ := args["parent_path"].(string)

	// Delegate to typed API with explicit parameters
	result, err := a.ListCategories(ctx, ListCategoriesArgs{
		Database:   databaseName,
		KBName:     kbName,
		Limit:      limit,
		Offset:     offset,
		ParentPath: parentPath,
	})
	if err != nil {
		return "", err
	}
	res := map[string]any{"categories": result.Categories, "total": result.Total}
	b, _ := json.Marshal(res)
	return string(b), nil
}

func (a *CopilotAgent) toolUpsertItems(ctx context.Context, args map[string]any) (string, error) {
	// Extract parameters from args and Context
	p := ai.GetSessionPayload(ctx)
	databaseName, _ := args["database"].(string)
	if databaseName == "" && p != nil {
		databaseName = p.CurrentDB
	}

	kbName, _ := args["kb_name"].(string)

	paramsRaw, _ := args["parameters"].([]any)
	b, _ := json.Marshal(paramsRaw)
	var params []memory.UpsertItemParam[map[string]any]
	json.Unmarshal(b, &params)

	// Delegate to typed API with explicit parameters
	result, err := a.BulkUpsertItems(ctx, BulkUpsertItemsArgs{
		Database:   databaseName,
		KBName:     kbName,
		Parameters: params,
	})
	if err != nil {
		return "", err
	}
	if result.Success {
		return "Items upserted.", nil
	}
	return fmt.Sprintf("Items upserted with errors: %d processed, %d failed", result.Processed, result.Failed), nil
}

func (a *CopilotAgent) toolDeleteItems(ctx context.Context, args map[string]any) (string, error) {
	db, err := a.getDBForSpaceContext(ctx, args)
	if err != nil {
		return "", err
	}
	kbName, _ := args["kb_name"].(string)

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, kbName, tx, a.brain, embedder, false)
	if err != nil {
		return "", err
	}

	idsRaw, _ := args["item_keys"].([]any)
	var ids []memory.ItemKey
	for _, v := range idsRaw {
		if m, ok := v.(map[string]any); ok {
			var k memory.ItemKey
			if catStr, ok := m["category_id"].(string); ok {
				k.CategoryID, _ = sop.ParseUUID(catStr)
			}
			if itmStr, ok := m["item_id"].(string); ok {
				k.ItemID, _ = sop.ParseUUID(itmStr)
			}
			ids = append(ids, k)
		}
	}

	err = kb.DeleteItems(ctx, ids)
	if err != nil {
		return "", err
	}
	tx.Commit(ctx)
	return "Items deleted.", nil
}

func (a *CopilotAgent) toolListItems(ctx context.Context, args map[string]any) (string, error) {
	db, err := a.getDBForSpaceContext(ctx, args)
	if err != nil {
		return "", err
	}
	kbName, _ := args["kb_name"].(string)

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, kbName, tx, a.brain, embedder, false)
	if err != nil {
		return "", err
	}

	var p memory.ListItemsParam
	p.Limit = 100
	if val, ok := args["limit"].(float64); ok && val > 0 {
		p.Limit = int(val)
	}
	if val, ok := args["offset"].(float64); ok && val > 0 {
		p.Offset = int(val)
	}
	if val, ok := args["category_path"].(string); ok {
		p.CategoryPath = val
	}

	items, total, err := kb.ListItems(ctx, p)
	if err != nil {
		return "", err
	}
	res := map[string]any{"items": items, "total": total}
	b, _ := json.Marshal(res)
	return string(b), nil
}

func (a *CopilotAgent) toolSearchItemsByPath(ctx context.Context, args map[string]any) (string, error) {
	db, err := a.getDBForSpaceContext(ctx, args)
	if err != nil {
		return "", err
	}
	kbName, _ := args["kb_name"].(string)

	tx, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var embedder ai.Embeddings
	if a.service != nil && a.service.Domain() != nil {
		embedder = a.service.Domain().Embedder()
	}
	kb, err := db.OpenKnowledgeBase(ctx, kbName, tx, a.brain, embedder, false)
	if err != nil {
		return "", err
	}

	b, _ := json.Marshal(args["parameters"])
	var params []memory.PathSearchParam
	json.Unmarshal(b, &params)

	items, err := kb.SearchByPath(ctx, params)
	if err != nil {
		return "", err
	}
	res := map[string]any{"items": items}
	resB, _ := json.Marshal(res)
	return string(resB), nil
}
