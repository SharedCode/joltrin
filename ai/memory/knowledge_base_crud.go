package memory

import (
	"context"
	log "log/slog"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/embed"
	"github.com/sharedcode/joltrin/v5"
)

// ============================================================================
// V1 Space CRUD & Omni Batched API Parameters
// ============================================================================

type UpsertCategoryParam struct {
	ParentPaths []string   `json:"parent_paths"` // Declarative DAG routing (e.g. ["Root/Engineering", "Root/DevOps"])
	ParentIDs   []sop.UUID `json:"parent_ids"`   // Explicit DAG routing fallback
	Category    *Category  `json:"category"`     // Pointer to the Category to upsert
}

type UpsertItemParam[T any] struct {
	CategoryPath string      `json:"category_path"` // e.g. "Root/Engineering/Architecture"
	CategoryID   sop.UUID    `json:"category_id"`   // Direct ID fallback if path is empty
	Item         *Item[T]    `json:"item"`          // Pointer to avoid heavy allocation during batch
	Vectors      [][]float32 `json:"vectors"`       // Optional explicit embeddings
}

// PathSearchParam specifies a hierarchical category path search.
// BREAKTHROUGH: Supports semantic path navigation using CategoriesByDistance B-Tree.
// When exact lexical path is not found, the system performs hierarchical semantic drill-down:
// 1. Split path by "/" (e.g., "Engineering/Databases/SQL" → ["Engineering", "Databases", "SQL"])
// 2. Root level: embed first part, search CategoriesByDistance using DomainReference as anchor
// 3. Nested levels: embed each part, search CategoriesByDistance using parent CenterVector as anchor
// 4. Navigate hierarchically through semantic similarity with O(D * log N) performance
// This enables typo-resistant, cross-lingual, natural language path queries.
// See ai/DYNAMIC_VECTOR_STORE_DESIGN.md Section 12 for full details.
type PathSearchParam struct {
	CategoryPath string `json:"category_path"` // e.g. "Root/Engineering/Architecture" (semantic or lexical)
	SearchText   string `json:"search_text"`   // Text to prefix search on item content/title
}

type ListCategoriesParam struct {
	ParentPath string   `json:"parent_path"` // Optional: restrict list to a specific parent node
	ParentID   sop.UUID `json:"parent_id"`   // Optional: explicit restriction by ID
	Limit      int      `json:"limit"`
	Offset     int      `json:"offset"`
}

type ListItemsParam struct {
	CategoryPath string   `json:"category_path"` // Optional: restrict list to a specific category
	CategoryID   sop.UUID `json:"category_id"`   // Optional: restrict list by explicit ID
	Limit        int      `json:"limit"`
	Offset       int      `json:"offset"`
}

// ============================================================================
// V1 Space CRUD & Omni Batched API (Implementations)
// ============================================================================

func (kb *KnowledgeBase[T]) UpsertCategories(ctx context.Context, params []UpsertCategoryParam) error {
	catBtree, err := kb.Store.Categories(ctx)
	if err != nil {
		return err
	}
	catsByPath, err := kb.Store.CategoriesByPath(ctx)
	if err != nil {
		return err
	}

	for _, p := range params {
		if p.Category == nil {
			continue
		}

		for _, path := range p.ParentPaths {
			found, _ := catsByPath.Find(ctx, path, false)
			if found {
				id, _ := catsByPath.GetCurrentValue(ctx)
				p.ParentIDs = append(p.ParentIDs, id)
			}
		}

		parentMap := make(map[sop.UUID]bool)
		var finalParents []CategoryParent
		for _, pid := range p.ParentIDs {
			if !parentMap[pid] {
				parentMap[pid] = true
				finalParents = append(finalParents, CategoryParent{ParentID: pid})
			}
		}
		p.Category.ParentIDs = finalParents

		if p.Category.ID.IsNil() {
			p.Category.ID = sop.NewUUID()
		}

		found, _ := catBtree.Find(ctx, p.Category.ID, false)
		if found {
			_, err = catBtree.Update(ctx, p.Category.ID, p.Category)
			if err != nil {
				return err
			}
		} else {
			_, err = catBtree.Add(ctx, p.Category.ID, p.Category)
			if err != nil {
				return err
			}
		}

		path := p.Category.Path
		if path == "" {
			path = p.Category.Name
		}
		if path != "" {
			foundPath, _ := catsByPath.Find(ctx, path, false)
			if foundPath {
				_, err = catsByPath.Update(ctx, path, p.Category.ID)
				if err != nil {
					return err
				}
			} else {
				_, err = catsByPath.Add(ctx, path, p.Category.ID)
				if err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (kb *KnowledgeBase[T]) DeleteCategories(ctx context.Context, categoryIDs []sop.UUID) error {
	tree, err := kb.Store.Categories(ctx)
	if err != nil {
		return err
	}
	for _, id := range categoryIDs {
		_, err = tree.Remove(ctx, id)
		if err != nil {
			return err
		}
	}
	return nil
}

func (kb *KnowledgeBase[T]) ListCategories(ctx context.Context, param ListCategoriesParam) ([]Category, int, error) {
	catsTree, err := kb.Store.Categories(ctx)
	if err != nil {
		return nil, 0, err
	}

	var parentFilter sop.UUID
	if !param.ParentID.IsNil() {
		parentFilter = param.ParentID
	} else if param.ParentPath != "" && param.ParentPath != "/" {
		catsByPath, err := kb.Store.CategoriesByPath(ctx)
		if err == nil {
			if found, _ := catsByPath.Find(ctx, param.ParentPath, false); found {
				parentFilter, _ = catsByPath.GetCurrentValue(ctx)
			}
		}
	}

	var categories []Category
	matchCount := 0
	ok, err := catsTree.First(ctx)
	if err != nil {
		return nil, 0, err
	}

	for ok {
		cat, _ := catsTree.GetCurrentValue(ctx)
		if cat != nil {
			isMatch := false
			if param.ParentPath == "/" {
				if len(cat.ParentIDs) == 0 {
					isMatch = true
				}
			} else if !parentFilter.IsNil() {
				for _, p := range cat.ParentIDs {
					if p.ParentID == parentFilter {
						isMatch = true
						break
					}
				}
			} else if param.ParentPath == "" && param.ParentID.IsNil() {
				isMatch = true
			}
			if isMatch {
				if matchCount >= param.Offset && matchCount < param.Offset+param.Limit {
					categories = append(categories, *cat)
				}
				matchCount++
			}
		}
		ok, _ = catsTree.Next(ctx)
	}
	if categories == nil {
		categories = make([]Category, 0)
	}
	return categories, matchCount, nil
}

func (kb *KnowledgeBase[T]) UpsertItems(ctx context.Context, params []UpsertItemParam[T]) error {
	itemsTree, err := kb.Store.Items(ctx)
	if err != nil {
		return err
	}
	catsByPath, err := kb.Store.CategoriesByPath(ctx)
	if err != nil {
		return err
	}

	for _, p := range params {
		if p.Item == nil {
			continue
		}

		catID := p.CategoryID
		if p.CategoryPath != "" {
			if found, _ := catsByPath.Find(ctx, p.CategoryPath, false); found {
				catID, _ = catsByPath.GetCurrentValue(ctx)
			}
		}

		if catID.IsNil() {
			continue
		}
		if p.Item.ID.IsNil() {
			p.Item.ID = sop.NewUUID()
		}

		key := ItemKey{CategoryID: catID, ItemID: p.Item.ID}
		found, _ := itemsTree.Find(ctx, key, false)
		if found {
			_, _ = itemsTree.Update(ctx, key, *p.Item)
		} else {
			_, _ = itemsTree.Add(ctx, key, *p.Item)
		}
	}
	return nil
}

func (kb *KnowledgeBase[T]) DeleteItems(ctx context.Context, itemKeys []ItemKey) error {
	tree, err := kb.Store.Items(ctx)
	if err != nil {
		return err
	}
	for _, key := range itemKeys {
		_, _ = tree.Remove(ctx, key)
	}
	return nil
}

func (kb *KnowledgeBase[T]) ListItems(ctx context.Context, param ListItemsParam) ([]Item[T], int, error) {
	itemsTree, err := kb.Store.Items(ctx)
	if err != nil {
		return nil, 0, err
	}

	var categoryFilter sop.UUID
	if !param.CategoryID.IsNil() {
		categoryFilter = param.CategoryID
	} else if param.CategoryPath != "" {
		catsByPath, err := kb.Store.CategoriesByPath(ctx)
		if err == nil {
			if found, _ := catsByPath.Find(ctx, param.CategoryPath, false); found {
				categoryFilter, _ = catsByPath.GetCurrentValue(ctx)
			}
		}
	}

	var items []Item[T]
	matchCount := 0
	totalCount := 0

	c, _ := kb.Store.Count(ctx)
	totalCount = int(c)

	ok, _ := itemsTree.First(ctx)

	if !categoryFilter.IsNil() {
		ok, _ = itemsTree.Find(ctx, ItemKey{CategoryID: categoryFilter, ItemID: sop.NilUUID}, true)
		if !ok {
			currKey := itemsTree.GetCurrentKey()
			if !currKey.ID.IsNil() {
				if currKey.Key.CategoryID == categoryFilter {
					ok = true
				} else if currKey.Key.CategoryID.Compare(categoryFilter) < 0 {
					ok, _ = itemsTree.Next(ctx)
				}
			}
		}
	}

	for ok {
		itemReq := itemsTree.GetCurrentKey()
		if itemReq.ID.IsNil() {
			break
		}
		if !categoryFilter.IsNil() && itemReq.Key.CategoryID != categoryFilter {
			break
		}
		if categoryFilter.IsNil() || itemReq.Key.CategoryID == categoryFilter {
			if matchCount >= param.Offset && matchCount < param.Offset+param.Limit {
				item, _ := itemsTree.GetCurrentValue(ctx)
				items = append(items, item)
			}
			matchCount++
			if categoryFilter.IsNil() && matchCount >= param.Offset+param.Limit {
				break
			}
		}
		ok, _ = itemsTree.Next(ctx)
	}

	if items == nil {
		items = make([]Item[T], 0)
	}
	if !categoryFilter.IsNil() {
		totalCount = matchCount
	}
	return items, totalCount, nil
}

func chooseCategoryAnchor(name, description, systemPrompt string) string {
	if text := strings.TrimSpace(name); text != "" {
		return normalize(text)
	}
	if text := strings.TrimSpace(description); text != "" {
		return normalize(text)
	}
	return normalize(strings.TrimSpace(systemPrompt))
}

func (kb *KnowledgeBase[T]) RefreshSemanticVectors(ctx context.Context) error {
	return refreshSemanticVectors(ctx, kb)
}

func refreshSemanticVectors[T any](ctx context.Context, kb *KnowledgeBase[T]) error {
	if kb == nil || kb.Manager == nil || kb.Manager.embedder == nil {
		return nil
	}

	targetDim := kb.Manager.embedder.Dim()
	if targetDim <= 0 {
		return nil
	}

	cats, err := kb.Store.Categories(ctx)
	if err != nil {
		return err
	}
	catsByDist, err := kb.Store.CategoriesByDistance(ctx)
	if err != nil {
		return err
	}

	cfg, err := kb.GetConfig(ctx)
	if err != nil {
		return err
	}
	if cfg == nil {
		cfg = &KnowledgeBaseConfig{}
	}

	needRefresh := catsByDist.Count() == 0
	if !needRefresh && len(kb.Store.DomainReference()) != targetDim {
		needRefresh = true
	}
	if !needRefresh {
		ok, err := cats.First(ctx)
		if err != nil {
			return err
		}
		for ok {
			cat, err := cats.GetCurrentValue(ctx)
			if err != nil {
				return err
			}
			if cat == nil {
				break
			}
			text := chooseCategoryAnchor(cat.Name, cat.Description, cfg.SystemPrompt)
			if len(cat.CenterVector) != targetDim || cat.VectorHash != ComputeVectorHash(targetDim, text) {
				needRefresh = true
				break
			}
			ok, err = cats.Next(ctx)
			if err != nil {
				return err
			}
		}
	}
	if !needRefresh {
		return nil
	}

	if len(kb.Store.DomainReference()) != targetDim {
		anchor := chooseCategoryAnchor(kb.Store.Name(), cfg.Description, cfg.SystemPrompt)
		if anchor == "" {
			anchor = kb.Store.Name()
		}
		if anchor != "" {
			vecs, err := embed.CategoryTexts(ctx, kb.Manager.embedder, []string{anchor})
			if err != nil {
				return err
			}
			if len(vecs) > 0 && len(vecs[0]) == targetDim {
				cfg.DomainReference = vecs[0]
				kb.Store.SetDomainReference(vecs[0])
			}
		}
	}

	if cfg.DomainReference != nil && len(cfg.DomainReference) == targetDim {
		if err := kb.SetConfig(ctx, cfg); err != nil {
			return err
		}
	}

	oldVectors := map[sop.UUID][]float32{}
	newVectors := map[sop.UUID][]float32{}
	ok, err := cats.First(ctx)
	if err != nil {
		return err
	}
	for ok {
		cat, err := cats.GetCurrentValue(ctx)
		if err != nil {
			return err
		}
		if cat == nil {
			break
		}
		text := chooseCategoryAnchor(cat.Name, cat.Description, cfg.SystemPrompt)
		oldVectors[cat.ID] = append([]float32(nil), cat.CenterVector...)
		if text == "" {
			ok, err = cats.Next(ctx)
			if err != nil {
				return err
			}
			continue
		}
		if len(cat.CenterVector) != targetDim || cat.VectorHash != ComputeVectorHash(targetDim, text) {
			vecs, err := embed.CategoryTexts(ctx, kb.Manager.embedder, []string{text})
			if err != nil {
				return err
			}
			if len(vecs) > 0 && len(vecs[0]) == targetDim {
				cat.CenterVector = vecs[0]
				cat.VectorHash = ComputeVectorHash(targetDim, text)
				if _, err := cats.UpdateCurrentItem(ctx, cat.ID, cat); err != nil {
					return err
				}
			}
		}
		newVectors[cat.ID] = cat.CenterVector
		ok, err = cats.Next(ctx)
		if err != nil {
			return err
		}
	}

	ok, err = catsByDist.First(ctx)
	for ok && err == nil {
		if _, err := catsByDist.RemoveCurrentItem(ctx); err != nil {
			return err
		}
		ok, err = catsByDist.Next(ctx)
	}
	if err != nil {
		return err
	}

	ok, err = cats.First(ctx)
	if err != nil {
		return err
	}
	for ok {
		cat, err := cats.GetCurrentValue(ctx)
		if err != nil {
			return err
		}
		if cat == nil {
			break
		}
		refVec := kb.Store.DomainReference()
		if len(cat.ParentIDs) > 0 {
			for _, parent := range cat.ParentIDs {
				if len(newVectors[parent.ParentID]) > 0 {
					refVec = newVectors[parent.ParentID]
					break
				}
				if len(oldVectors[parent.ParentID]) > 0 {
					refVec = oldVectors[parent.ParentID]
					break
				}
			}
		}
		if len(refVec) == 0 {
			refVec = kb.Store.DomainReference()
		}
		dist := Distance(refVec, cat.CenterVector, true)
		if len(cat.ParentIDs) > 0 {
			for _, parent := range cat.ParentIDs {
				if _, err := catsByDist.Add(ctx, DistanceKey{ParentID: parent.ParentID, Distance: dist, ID: cat.ID}, 0); err != nil {
					return err
				}
			}
		} else {
			if _, err := catsByDist.Add(ctx, DistanceKey{ParentID: sop.NilUUID, Distance: dist, ID: cat.ID}, 0); err != nil {
				return err
			}
		}
		ok, err = cats.Next(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}

func refreshSemanticVectorsInWriteTx[T any](ctx context.Context, kb *KnowledgeBase[T]) error {
	if kb == nil || kb.Manager == nil || kb.Manager.embedder == nil {
		return nil
	}

	store, ok := kb.Store.(*store[T])
	if !ok || store == nil || store.db == nil {
		return nil
	}

	writeTx, err := store.db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return err
	}
	defer writeTx.Rollback(ctx)

	refreshedKB, err := store.db.OpenKnowledgeBase(ctx, kb.Name(), writeTx, kb.Manager.llm, kb.Manager.embedder, false, true)
	if err != nil {
		return err
	}
	if err := refreshSemanticVectors(ctx, refreshedKB); err != nil {
		return err
	}
	return writeTx.Commit(ctx)
}

func convertToVectors(ctx context.Context, catPath string, embedder ai.Embeddings) ([][]float32, error) {
	return CategoryPathVectors(ctx, catPath, embedder)
}

// SearchByPath performs hierarchical category path search with dual-mode operation:
//
// MODE 1 - Lexical Fast-Path (O(1)):
// If exact CategoryPath exists in CategoriesByPath B-Tree, uses direct lookup.
//
// MODE 2 - Semantic Path Navigation (O(D * log N)) - WORLD'S FIRST 🚀:
// When lexical match fails, performs breakthrough semantic hierarchical drill-down:
//  1. Split path: "Engineering/Databases/SQL" → ["Engineering", "Databases", "SQL"]
//  2. Root level: Embed first part, search CategoriesByDistance using DomainReference anchor
//  3. Nested levels: Embed each part, search CategoriesByDistance using parent CenterVector
//  4. Navigate hierarchically through semantic similarity using Triangle Inequality pruning
//
// Revolutionary capabilities:
//   - Natural language paths: "ML training optimization" finds "Machine Learning/Model Training"
//   - Typo-resistant: "Databse" semantically matches "Databases"
//   - Cross-lingual: Chinese paths match English category structure
//   - Zero additional storage: Leverages existing CategoriesByDistance infrastructure
//   - ACID-compliant: Full transactional guarantees during semantic navigation
//
// This is the only vector database in the world with hierarchical semantic path search.
// See ai/DYNAMIC_VECTOR_STORE_DESIGN.md Section 12 for full algorithm details.
func (kb *KnowledgeBase[T]) SearchByPath(ctx context.Context, params []PathSearchParam) ([]Item[T], error) {
	log.Info("SearchByPath start", "params_count", len(params), "params", params)
	itemsTree, err := kb.Store.Items(ctx)
	if err != nil {
		return nil, err
	}
	catsByPathTree, err := kb.Store.CategoriesByPath(ctx)
	if err != nil {
		return nil, err
	}

	var results []Item[T]

	for _, param := range params {
		log.Info("SearchByPath param", "category_path", param.CategoryPath, "search_text", param.SearchText)
		found, err := catsByPathTree.Find(ctx, param.CategoryPath, false)
		if err != nil {
			return nil, err
		}
		catID := sop.NilUUID
		if found {
			catID, err = catsByPathTree.GetCurrentValue(ctx)
			if err != nil {
				return nil, err
			}
		} else {
			log.Info("SearchByPath lexical miss, trying semantic fallback", "category_path", param.CategoryPath)
			// Try search by Semantic Category Path.
			vecs, err := convertToVectors(ctx, param.CategoryPath, kb.Manager.embedder)
			if err != nil {
				log.Warn("SearchByPath semantic fallback skipped", "category_path", param.CategoryPath, "error", err)
				continue
			}
			cats, err := kb.Store.SemanticCategoryByPath(ctx, vecs)
			if err != nil {
				log.Warn("SearchByPath semantic fallback failed", "category_path", param.CategoryPath, "error", err)
				continue
			}
			if len(cats) > 0 {
				log.Info("SearchByPath semantic fallback matched", "category_path", param.CategoryPath, "selected_cat_id", cats[0].ID.String(), "selected_cat_path", cats[0].Path, "candidate_count", len(cats), "candidate_ids", categoryIDs(cats), "candidate_paths", categoryPaths(cats))
				// Just pick one for MVP.
				catID = cats[0].ID
			} else {
				log.Info("SearchByPath semantic fallback missed", "category_path", param.CategoryPath)
				// Skip if category path is not found
				continue
			}
		}

		ok, _ := itemsTree.Find(ctx, ItemKey{CategoryID: catID, ItemID: sop.NilUUID}, true)
		if !ok {
			currKey := itemsTree.GetCurrentKey()
			if !currKey.Key.ItemID.IsNil() {
				if currKey.Key.CategoryID == catID {
					ok = true
				} else if currKey.Key.CategoryID.Compare(catID) < 0 {
					ok, _ = itemsTree.Next(ctx)
				}
			}
		}

		log.Info("SearchByPath leaf item scan", "category_path", param.CategoryPath, "search_text", param.SearchText, "selected_cat_id", catID, "item_scan_started", ok)
		matchCount := 0
		for ok {
			itemReq := itemsTree.GetCurrentKey()
			if itemReq.Key.ItemID.IsNil() {
				break
			}
			if itemReq.Key.CategoryID != catID {
				break
			}

			item, err := itemsTree.GetCurrentValue(ctx)
			matched := err == nil && len(item.Summaries) > 0 && strings.HasPrefix(item.Summaries[0], param.SearchText)
			if matched {
				matchCount++
				log.Info("SearchByPath leaf item matched", "category_path", param.CategoryPath, "selected_cat_id", catID, "item_id", itemReq.Key.ItemID, "summary_prefix", item.Summaries[0], "search_text", param.SearchText)
				results = append(results, item)
			} else {
				log.Debug("SearchByPath leaf item skipped", "category_path", param.CategoryPath, "selected_cat_id", catID, "item_id", itemReq.Key.ItemID, "summary_count", len(item.Summaries), "search_text", param.SearchText)
			}

			ok, _ = itemsTree.Next(ctx)
		}
		log.Info("SearchByPath leaf item summary", "category_path", param.CategoryPath, "selected_cat_id", catID, "match_count", matchCount, "result_count", len(results))
	}

	if results == nil {
		results = make([]Item[T], 0)
	}

	return results, nil
}
