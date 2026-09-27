package memory

import (
	"context"
	"fmt"
	log "log/slog"
	"math"
	"sort"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/btree"
)

// store implements MemoryStore.
type store[T any] struct {
	db                   Database
	name                 string
	categories           btree.BtreeInterface[sop.UUID, *Category]
	categoriesByPath     btree.BtreeInterface[string, sop.UUID]
	categoriesByDistance btree.BtreeInterface[DistanceKey, byte]
	vectors              btree.BtreeInterface[VectorKey, Vector]
	items                btree.BtreeInterface[ItemKey, Item[T]]
	documents            btree.BtreeInterface[sop.UUID, Document]
	textIndex            ai.TextIndex
	llm                  LLM[T]
	domainReference      []float32
}

// NewStore creates a new instance of MemoryStore.
func NewStore[T any](
	name string,
	db Database,
	categories btree.BtreeInterface[sop.UUID, *Category],
	categoriesByPath btree.BtreeInterface[string, sop.UUID],
	categoriesByDistance btree.BtreeInterface[DistanceKey, byte],
	vectors btree.BtreeInterface[VectorKey, Vector],
	items btree.BtreeInterface[ItemKey, Item[T]],
	documents btree.BtreeInterface[sop.UUID, Document],
) MemoryStore[T] {
	return &store[T]{
		name:                 name,
		db:                   db,
		categories:           categories,
		categoriesByPath:     categoriesByPath,
		categoriesByDistance: categoriesByDistance,
		vectors:              vectors,
		items:                items,
		documents:            documents,
	}
}

func (s *store[T]) SetDomainReference(vec []float32) {
	s.domainReference = vec
}

func (s *store[T]) DomainReference() []float32 {
	return s.domainReference
}

func (s *store[T]) Name() string {
	return s.name
}

func (s *store[T]) SetTextIndex(idx ai.TextIndex) {
	s.textIndex = idx
}

func (s *store[T]) SetLLM(l LLM[T]) {
	s.llm = l
}

func extractSearchText(data any) string {
	if m, ok := data.(map[string]any); ok {
		var parts []string
		for _, field := range []string{"text", "description", "content", "page_content", "_raw_content"} {
			if text, ok := m[field].(string); ok && strings.TrimSpace(text) != "" {
				parts = append(parts, text)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " ")
		}
	}
	if st, ok := data.(interface{ SearchText() string }); ok {
		return st.SearchText()
	}
	return fmt.Sprintf("%v", data)
}

func (s *store[T]) Upsert(ctx context.Context, item Item[T], vec []float32) error {
	id := item.ID
	if id == sop.NilUUID {
	}

	// 1. Find nearest category
	var bestCategory sop.UUID
	var bestDist float32 = -1
	ok, err := s.categories.First(ctx)
	if err != nil {
		return err
	}
	if !ok {
		// Create a root category if none exists
		var c *Category
		if s.llm != nil {
			if genCat, gErr := s.llm.GenerateCategory(ctx, item.Data); gErr == nil && genCat != nil {
				c = genCat
				if c.ID == sop.NilUUID {
					c.ID = sop.NewUUID()
				}
				if len(c.CenterVector) == 0 {
					c.CenterVector = vec
				}
			}
		}
		if c == nil {
			c = &Category{
				ID:           sop.NewUUID(),
				CenterVector: vec, // Initial category math uses the first vector
				Name:         "Default Root",
			}
		}
		_, err = s.AddCategory(ctx, c)
		if err != nil {
			return err
		}
		bestCategory = c.ID
		bestDist = 0
	} else {
		for {
			c, err := s.categories.GetCurrentValue(ctx)
			if err == nil && c != nil {
				dist := Distance(vec, c.CenterVector, true)
				if bestDist == -1 || dist < bestDist {
					bestDist = dist
					bestCategory = c.ID
				}
			}
			nextOk, nextErr := s.categories.Next(ctx)
			if nextErr != nil || !nextOk {
				break
			}
		}

		// Active Memory Clustering: Re-evaluate category boundary via LLM if item's distance is too far from existing centers.
		if bestDist > 0.60 && s.llm != nil {
			if genCat, gErr := s.llm.GenerateCategory(ctx, item.Data); gErr == nil && genCat != nil {
				if genCat.ID == sop.NilUUID {
					genCat.ID = sop.NewUUID()
				}
				if len(genCat.CenterVector) == 0 {
					genCat.CenterVector = vec
				}
				if _, addErr := s.AddCategory(ctx, genCat); addErr == nil {
					bestCategory = genCat.ID
					// bestDist resets to perfect fit for the newly generated category
					bestDist = 0
				}
			}
		}
	}

	// 2. Insert into vectors tree
	vID := sop.NewUUID()
	v := Vector{
		ID:         vID,
		Data:       vec,
		ItemID:     id,
		CategoryID: bestCategory,
	}
	vk := VectorKey{
		CategoryID:         bestCategory,
		DistanceToCategory: bestDist,
		VectorID:           vID,
	}
	_, err = s.vectors.Add(ctx, vk, v)
	if err != nil {
		return err
	}

	// 3. Insert into items tree
	itemKey := ItemKey{CategoryID: bestCategory, ItemID: id}
	foundItem, err := s.items.Find(ctx, itemKey, false)
	if err != nil {
		return err
	}

	if foundItem {
		existingItem, err := s.items.GetCurrentValue(ctx)
		if err != nil {
			return err
		}
		existingItem.Positions = append(existingItem.Positions, vk)
		_, err = s.items.UpdateCurrentItem(ctx, itemKey, existingItem)
		if err != nil {
			return err
		}
	} else {
		itemObj := Item[T]{
			ID:         id,
			CategoryID: bestCategory,
			Data:       item.Data,
			Positions:  []VectorKey{vk},
		}
		_, err = s.items.Add(ctx, itemKey, itemObj)
		if err != nil {
			return err
		}
	}

	// 4. Update TextIndex if present
	if s.textIndex != nil {
		strData := extractSearchText(item.Data)
		err = s.textIndex.Add(ctx, fmt.Sprintf("%v,%v", bestCategory.String(), id.String()), strData)
		if err != nil {
			return err
		}
	}

	return nil
}

// UpsertByCategoryPath strictly inserts data assuming an explicit, fixed Category.
func (s *store[T]) UpsertByCategoryPath(ctx context.Context, categoryName string, item Item[T], vecs [][]float32) error {
	var c *Category

	ok, err := s.categoriesByPath.Find(ctx, categoryName, false)
	if err != nil {
		return err
	}
	if ok {
		catID, err := s.categoriesByPath.GetCurrentValue(ctx)
		if err != nil {
			return err
		}
		found, err := s.categories.Find(ctx, catID, false)
		if err != nil {
			return err
		}
		if found {
			c, err = s.categories.GetCurrentValue(ctx)
			if err != nil {
				return err
			}
		}
	}

	if c == nil {
		// Auto-vivify static category
		c = &Category{ID: sop.NewUUID(), Name: categoryName}
		_, err = s.categories.Add(ctx, c.ID, c)
		if err != nil {
			return err
		}
		_, err = s.categoriesByPath.Add(ctx, categoryName, c.ID)
		if err != nil {
			return err
		}
	}

	return s.UpsertByCategoryID(ctx, c.ID, c.CenterVector, item, vecs, nil)
}

// UpsertByCategoryID inserts data bypassing Category lookup.
// vecs are DocumentTexts (768 dim) used for item similarity search.
// classificationVecs are CategoryTexts (256 dim) used for calculating DistanceToCategory.
func (s *store[T]) UpsertByCategoryID(ctx context.Context, catID sop.UUID, catCenterVector []float32,
	item Item[T], vecs [][]float32, classificationVecs [][]float32) error {

	if catID == sop.NilUUID {
		return fmt.Errorf("catID can't be nil")
	}

	id := item.ID

	// Insert Vector links
	var keys []VectorKey
	for i, vec := range vecs {
		dist := float32(0)
		if len(catCenterVector) > 0 && len(classificationVecs) > i && len(classificationVecs[i]) > 0 {
			// Calculate DistanceToCategory in classification space (CategoryTexts)
			dist = Distance(classificationVecs[i], catCenterVector, true)
		}
		vk := VectorKey{CategoryID: catID, VectorID: sop.NewUUID(), DistanceToCategory: dist}

		log.Debug("s.vectors.Add call")
		s.vectors.Add(ctx, vk, Vector{ID: vk.VectorID, ItemID: id, CategoryID: catID, Data: vec})
		keys = append(keys, vk)
	}

	// Insert Item
	itemKey := ItemKey{CategoryID: catID, ItemID: id}
	if found, _ := s.items.Find(ctx, itemKey, false); found {
		oldItem, _ := s.items.GetCurrentValue(ctx)
		var newPositions []VectorKey
		for _, pos := range oldItem.Positions {
			if pos.CategoryID == catID {
				log.Debug("s.vectors.Remove call")
				s.vectors.Remove(ctx, pos)
			} else {
				newPositions = append(newPositions, pos)
			}
		}
		newPositions = append(newPositions, keys...)
		log.Debug("s.items.UpdateCurrentItem call")
		s.items.UpdateCurrentItem(ctx, itemKey, Item[T]{ID: id, DocID: item.DocID, CategoryID: catID, Summaries: item.Summaries, Data: item.Data, Positions: newPositions, VectorHash: item.VectorHash})
	} else {
		log.Debug("s.items.Add call")
		s.items.Add(ctx, itemKey, Item[T]{ID: id, DocID: item.DocID, CategoryID: catID, Summaries: item.Summaries, Data: item.Data, Positions: keys, VectorHash: item.VectorHash})
	}

	// Update global text index
	if s.textIndex != nil {
		strData := extractSearchText(item.Data)
		log.Debug("s.textIndex.Add call")
		s.textIndex.Add(ctx, fmt.Sprintf("%v,%v", catID.String(), id.String()), strData) // Assumes item.ID actually contained textual representation.
	}

	return nil
}

func (s *store[T]) DeleteItem(ctx context.Context, itemID ItemKey) error {
	found, err := s.items.Find(ctx, itemID, false)
	if err != nil {
		return err
	}
	if !found {
		return nil // already deleted
	}

	item, err := s.items.GetCurrentValue(ctx)
	if err != nil {
		return err
	}

	// Clean up all associated vectors in O(1) time
	for _, pos := range item.Positions {
		_, err := s.vectors.Remove(ctx, pos)
		if err != nil {
			return err
		}
	}

	// Finally, remove the item itself
	_, err = s.items.Remove(ctx, itemID)
	return err
}

func (s *store[T]) UpsertBatch(ctx context.Context, items []Item[T], vecs [][]float32) error {
	for i, item := range items {
		if err := s.Upsert(ctx, item, vecs[i]); err != nil {
			return err
		}
	}
	return nil
}

func (s *store[T]) Get(ctx context.Context, id ItemKey) (*Item[T], error) {
	found, err := s.items.Find(ctx, id, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("item not found")
	}

	item, err := s.items.GetCurrentValue(ctx)
	if err != nil {
		return nil, err
	}

	return &item, nil
}

func (s *store[T]) Delete(ctx context.Context, id ItemKey) error {
	return s.DeleteItem(ctx, id)
}

func (s *store[T]) QueryBatch(ctx context.Context, vectors [][]float32, opts *SearchOptions[T]) ([][]ai.Hit[T], error) {
	results := make([][]ai.Hit[T], len(vectors))
	for i, vec := range vectors {
		res, err := s.Query(ctx, vec, opts)
		if err != nil {
			return nil, err
		}
		results[i] = res
	}
	return results, nil
}

// Find Closest Category implements a beam search through the category tree, starting from the root and
// exploring closest categories at each level until it finds the best overall match for the query vector.
func (s *store[T]) FindClosestCategory(ctx context.Context, qVec []float32) (*Category, float32, error) {
	if s.categoriesByDistance == nil || s.categoriesByDistance.Count() == 0 {
		return s.findClosestCategoryLinear(ctx, qVec)
	}

	type candidate struct {
		ParentID sop.UUID
		Anchor   []float32
		Dist     float32
	}
	queue := []candidate{
		{ParentID: sop.NilUUID, Anchor: s.domainReference, Dist: 0},
	}
	var bestGlobalCat *Category
	var bestGlobalDist float32 = -1.0

	for {
		var nextQueue []candidate

		for _, cand := range queue {
			qDist := Distance(cand.Anchor, qVec, true)
			_, err := s.categoriesByDistance.Find(ctx, DistanceKey{ParentID: cand.ParentID, Distance: qDist, ID: sop.NilUUID}, false)
			if err != nil {
				continue
			}

			if s.categoriesByDistance.GetCurrentKey().Key.ID == sop.NilUUID {
				// No item selected but find succeeded...
			}

			backSteps := 0
			for i := 0; i < 5; i++ {
				ok, _ := s.categoriesByDistance.Previous(ctx)
				if !ok || s.categoriesByDistance.GetCurrentKey().Key.ParentID.Compare(cand.ParentID) != 0 {
					if ok {
						s.categoriesByDistance.Next(ctx)
					} else {
						s.categoriesByDistance.First(ctx)
					}
					break
				}
				backSteps++
			}

			maxForward := backSteps + 1 + 5
			for i := 0; i < maxForward; i++ {
				curr := s.categoriesByDistance.GetCurrentKey()
				if curr.Key.ParentID.Compare(cand.ParentID) != 0 {
					break
				}
				if curr.Key.ID != sop.NilUUID {
					f, err := s.categories.Find(ctx, curr.Key.ID, false)
					if f && err == nil {
						cat, _ := s.categories.GetCurrentValue(ctx)
						if cat != nil && len(cat.CenterVector) > 0 {
							mDist := Distance(qVec, cat.CenterVector, true)
							nextQueue = append(nextQueue, candidate{ParentID: cat.ID, Anchor: cat.CenterVector, Dist: mDist})
							if bestGlobalDist < 0 || mDist < bestGlobalDist {
								bestGlobalDist = mDist
								bestGlobalCat = cat
							}
						}
					}
				}
				ok, _ := s.categoriesByDistance.Next(ctx)
				if !ok {
					break
				}
			}
		}

		if len(nextQueue) == 0 {
			break
		}

		// Beam search: keep top 3 closest categories to explore next level
		for i := 0; i < len(nextQueue); i++ {
			for j := i + 1; j < len(nextQueue); j++ {
				if nextQueue[i].Dist > nextQueue[j].Dist {
					nextQueue[i], nextQueue[j] = nextQueue[j], nextQueue[i]
				}
			}
		}
		if len(nextQueue) > 3 {
			nextQueue = nextQueue[:3]
		}
		queue = nextQueue
	}
	return bestGlobalCat, bestGlobalDist, nil
}

func (s *store[T]) findClosestCategoryLinear(ctx context.Context, qVec []float32) (*Category, float32, error) {
	ok, err := s.categories.First(ctx)
	if err != nil {
		return nil, 0, err
	}
	if !ok {
		return nil, -1, nil
	}

	var bestCat *Category
	var bestDist float32 = -1
	for ok {
		cat, err := s.categories.GetCurrentValue(ctx)
		if err != nil {
			return nil, 0, err
		}
		if cat != nil && len(cat.CenterVector) > 0 {
			dist := Distance(qVec, cat.CenterVector, true)
			if bestCat == nil || dist < bestDist {
				bestCat = cat
				bestDist = dist
			}
		}
		ok, err = s.categories.Next(ctx)
		if err != nil {
			return nil, 0, err
		}
	}
	if bestCat == nil {
		return nil, -1, nil
	}
	return bestCat, bestDist, nil
}

func (s *store[T]) resolveTargetCategory(ctx context.Context, qVec []float32) (*Category, error) {
	cat, _, err := s.FindClosestCategory(ctx, qVec)
	return cat, err
}

// SemanticCategoryByPath resolves a hierarchical category path expressed as pre-embedded
// vectors into the closest matching Category at each level, using the CategoriesByDistance
// B-Tree for O(log N) lookup per level.
//
// Algorithm (per design doc Section 12):
//
//	Level 0: anchor = DomainReference, ParentID = NilUUID
//	Level i: anchor = prev.CenterVector,  ParentID = prev.ID
//
// At each level:
//  1. dist = EuclideanDistance(anchor, queryVec)
//  2. Find(DistanceKey{ParentID, dist, NilUUID}) positions the cursor at the nearest entry.
//  3. Scan a small neighbourhood on both sides, validate by EuclideanDistance(queryVec, cat.CenterVector).
//  4. The category with the smallest validated distance is the best match for this level.
func (s *store[T]) SemanticCategoryByPath(ctx context.Context, pathVectors [][]float32) ([]*Category, error) {
	log.Info("SemanticCategoryByPath start", "path_vectors", len(pathVectors), "domain_reference_len", len(s.domainReference))
	if len(pathVectors) == 0 {
		return nil, nil
	}
	if len(s.domainReference) == 0 {
		return nil, fmt.Errorf("domain reference vector is not set. Ensure this KB had been vectorized.")
	}

	results, _, err := s.semanticCategoryByPartRecursive(ctx, sop.NilUUID, s.domainReference, pathVectors, 0)
	if err != nil {
		return nil, err
	}
	return results, nil
}

func (s *store[T]) semanticCategoryByPartRecursive(ctx context.Context, parentID sop.UUID, anchor []float32, pathVectors [][]float32, level int) ([]*Category, int, error) {
	if level >= len(pathVectors) {
		return nil, level, nil
	}

	candidates, err := s.semanticCategoryByPart(ctx, parentID, anchor, pathVectors[level])
	if err != nil {
		return nil, level, err
	}
	if len(candidates) == 0 {
		return nil, level, nil
	}
	if level == len(pathVectors)-1 {
		return candidates, level, nil
	}

	var result []*Category
	deepestLevel := level
	for _, candidate := range candidates {
		if candidate == nil || candidate.ID.IsNil() {
			continue
		}
		subCandidates, subLevel, err := s.semanticCategoryByPartRecursive(ctx, candidate.ID, candidate.CenterVector, pathVectors, level+1)
		if err != nil {
			return nil, subLevel, err
		}
		if len(subCandidates) == 0 || subLevel < len(pathVectors)-1 {
			continue
		}
		if subLevel > deepestLevel {
			deepestLevel = subLevel
		}
		result = append(result, subCandidates...)
	}

	return result, deepestLevel, nil
}

func (s *store[T]) semanticCategoryByPart(ctx context.Context, parentID sop.UUID, anchor []float32, partVector []float32) ([]*Category, error) {
	log.Info("semanticCategoryByPart start", "parent_id", parentID, "anchor_len", len(anchor), "part_vector_len", len(partVector))
	if len(partVector) == 0 {
		log.Info("semanticCategoryByPart skip", "reason", "empty part vector", "parent_id", parentID)
		return nil, nil
	}
	if len(anchor) == 0 {
		return nil, fmt.Errorf("domain reference vector is not set. Ensure this KB had been vectorized.")
	}

	type candidateScore struct {
		cat  *Category
		dist float32
	}

	var result []*Category
	dist := Distance(anchor, partVector, true)
	log.Info("semanticCategoryByPart distance", "parent_id", parentID, "distance", dist, "anchor_len", len(anchor), "part_vector_len", len(partVector))

	// Position cursor at the nearest distance entry for this parent.
	if _, err := s.categoriesByDistance.Find(ctx, DistanceKey{ParentID: parentID, Distance: dist, ID: sop.NilUUID}, false); err != nil {
		return nil, err
	}

	// Scan a neighbourhood of up to 5 entries before and after the cursor position.
	// We peek backwards first then scan forward, collecting candidates.
	const scanWindow = 5
	backSteps := 0
	for i := 0; i < scanWindow; i++ {
		ok, err := s.categoriesByDistance.Previous(ctx)
		if err != nil {
			return nil, err
		}
		if !ok || s.categoriesByDistance.GetCurrentKey().Key.ParentID.Compare(parentID) != 0 {
			if ok {
				s.categoriesByDistance.Next(ctx)
			} else {
				s.categoriesByDistance.First(ctx)
			}
			break
		}
		backSteps++
	}

	maxForward := backSteps + 1 + scanWindow
	var scores []candidateScore
	seen := map[sop.UUID]struct{}{}
	for i := 0; i < maxForward; i++ {
		currKey := s.categoriesByDistance.GetCurrentKey()
		if currKey.Key.ParentID.Compare(parentID) != 0 {
			break
		}
		if currKey.Key.ID.IsNil() {
			if ok, err := s.categoriesByDistance.Next(ctx); !ok || err != nil {
				if err != nil {
					return nil, err
				}
				break
			}
			continue
		}
		if _, exists := seen[currKey.Key.ID]; exists {
			if ok, err := s.categoriesByDistance.Next(ctx); !ok || err != nil {
				if err != nil {
					return nil, err
				}
				break
			}
			continue
		}
		found, err := s.categories.Find(ctx, currKey.Key.ID, false)
		if err != nil {
			return nil, err
		}
		if found {
			cat, err := s.categories.GetCurrentValue(ctx)
			if err != nil {
				return nil, err
			}
			if cat != nil {
				actualDist := dist
				if len(cat.CenterVector) > 0 {
					actualDist = Distance(partVector, cat.CenterVector, true)
				}
				seen[currKey.Key.ID] = struct{}{}
				scores = append(scores, candidateScore{cat: cat, dist: actualDist})
			}
		}
		if ok, err := s.categoriesByDistance.Next(ctx); !ok || err != nil {
			if err != nil {
				return nil, err
			}
			break
		}
	}
	if len(scores) > 1 {
		sort.Slice(scores, func(i, j int) bool {
			if scores[i].dist == scores[j].dist {
				return scores[i].cat.ID.Compare(scores[j].cat.ID) < 0
			}
			return scores[i].dist < scores[j].dist
		})
	}
	// Increase limit from 3 to 10 to avoid missing relevant categories (e.g., C# when Java/Python/Rust exist)
	if len(scores) > 10 {
		scores = scores[:10]
	}
	for _, entry := range scores {
		result = append(result, entry.cat)
	}
	log.Info("semanticCategoryByPart result", "parent_id", parentID, "candidate_count", len(result), "candidate_ids", categoryIDs(result), "candidate_paths", categoryPaths(result), "best_distance", func() float32 {
		if len(scores) > 0 {
			return scores[0].dist
		}
		return 0
	}())
	if len(result) == 0 {
		log.Info("semanticCategoryByPart no candidates", "parent_id", parentID, "distance", dist)
	}
	return result, nil
}

func categoryIDs(cats []*Category) []string {
	ids := make([]string, 0, len(cats))
	for _, cat := range cats {
		if cat == nil {
			ids = append(ids, "<nil>")
			continue
		}
		ids = append(ids, cat.ID.String())
	}
	return ids
}

func categoryPaths(cats []*Category) []string {
	paths := make([]string, 0, len(cats))
	for _, cat := range cats {
		if cat == nil {
			paths = append(paths, "<nil>")
			continue
		}
		paths = append(paths, cat.Path)
	}
	return paths
}

func compare(vecDistance float32, A float32, B float32) int {
	x := math.Abs(float64(vecDistance) - float64(A))
	y := math.Abs(float64(vecDistance) - float64(B))
	if x > y {
		return 1
	}
	if x < y {
		return -1
	}
	return 0
}

func (s *store[T]) Query(ctx context.Context, vec []float32, opts *SearchOptions[T]) ([]ai.Hit[T], error) {
	if opts == nil {
		opts = &SearchOptions[T]{Limit: 10}
	}
	var targetCategory *Category

	if len(opts.CategoryVector) > 0 {
		qVec := opts.CategoryVector
		if len(qVec) == 0 {
			qVec = vec
		}
		var err error
		targetCategory, err = s.resolveTargetCategory(ctx, qVec)
		if err != nil {
			return nil, err
		}
		if targetCategory == nil {
			return nil, nil // Error or not found
		}
	} else if opts.CategoryPath != "" {
		ok, err := s.categoriesByPath.Find(ctx, opts.CategoryPath, false)
		if err != nil {
			return nil, err
		}
		if ok {
			catID, err := s.categoriesByPath.GetCurrentValue(ctx)
			if err != nil {
				return nil, err
			}
			found, err := s.categories.Find(ctx, catID, false)
			if err != nil {
				return nil, err
			}
			if found {
				targetCategory, _ = s.categories.GetCurrentValue(ctx)
			}
		}
		if targetCategory == nil {
			return nil, nil // Category strictly required but not found
		}
	} else {
		var err error
		targetCategory, err = s.resolveTargetCategory(ctx, vec)
		if err != nil {
			return nil, err
		}
	}

	if targetCategory == nil {
		return nil, nil
	}

	return s.QueryItems(ctx, vec, targetCategory, opts)
}

func (s *store[T]) QueryItems(ctx context.Context, vec []float32, category *Category, opts *SearchOptions[T]) ([]ai.Hit[T], error) {
	if opts == nil {
		opts = &SearchOptions[T]{Limit: 10}
	}
	if category == nil || category.ID.IsNil() {
		return nil, nil
	}

	var hits []ai.Hit[T]

	// Find the start of the vectors for this Category ID in O(log N) time.
	searchKey := VectorKey{
		CategoryID:         category.ID,
		DistanceToCategory: -1.0,
	}

	found, err := s.vectors.Find(ctx, searchKey, false)
	if err != nil {
		return nil, err
	}

	var matchingVectors []Vector

	// If it didn't find an exact match (almost definitely false due to dummy -1 distance),
	// the cursor is likely left on the nearest neighbour (smaller node). We need to advance
	// until we are inside our target Category ID.
	if !found {
		currKey := s.vectors.GetCurrentKey()
		if currKey.Key.CategoryID.Compare(category.ID) < 0 {
			// Fast forward to the first node >= this category ID.
			for {
				ok, err := s.vectors.Next(ctx)
				if err != nil || !ok {
					break
				}
				if s.vectors.GetCurrentKey().Key.CategoryID.Compare(category.ID) >= 0 {
					break
				}
			}
		}
	}

	for {
		if len(matchingVectors) >= opts.Limit {
			break
		}

		vk := s.vectors.GetCurrentKey()

		// If we've passed our target category, we can stop evaluating entirely
		if vk.Key.CategoryID.Compare(category.ID) > 0 {
			break
		}

		if vk.Key.CategoryID.Compare(category.ID) == 0 {
			v, err := s.vectors.GetCurrentValue(ctx)
			if err == nil {
				matchingVectors = append(matchingVectors, v)
			}
		}

		nextOk, nextErr := s.vectors.Next(ctx)
		if nextErr != nil || !nextOk {
			break
		}
	}

	// Fetch items after we're done iterating vectors to avoid ANY cursor conflict
	for _, v := range matchingVectors {
		itemKeySearch := ItemKey{CategoryID: v.CategoryID, ItemID: v.ItemID}
		foundItem, err := s.items.Find(ctx, itemKeySearch, false)
		if foundItem && err == nil {
			item, err := s.items.GetCurrentValue(ctx)
			if err == nil {
				if opts.Filter == nil || opts.Filter(item.Data) {
					var d float32
					if len(vec) > 0 {
						d = Distance(vec, v.Data, true)
					}
					hits = append(hits, ai.Hit[T]{
						ID:      item.ID.String(),
						DocID:   item.DocID,
						Score:   d,
						Payload: item.Data,
					})
				}
			}
		}
	}

	if len(vec) > 0 {
		// Sort hits by score ascending (Euclidean distance: lower is better)
		for i := 0; i < len(hits); i++ {
			for j := i + 1; j < len(hits); j++ {
				if hits[i].Score > hits[j].Score {
					hits[i], hits[j] = hits[j], hits[i]
				}
			}
		}
	}
	if len(hits) > opts.Limit {
		hits = hits[:opts.Limit]
	}
	return hits, nil
}

func (s *store[T]) Count(ctx context.Context) (int64, error) {
	return s.items.Count(), nil
}

func (s *store[T]) Categories(ctx context.Context) (btree.BtreeInterface[sop.UUID, *Category], error) {
	return s.categories, nil
}

func (s *store[T]) Consolidate(ctx context.Context) error {
	return nil
}

func (s *store[T]) UpdateEmbedderInfo(ctx context.Context, provider string, model string, dimensions int) error {
	return nil
}

func (s *store[T]) Vectors(ctx context.Context) (btree.BtreeInterface[VectorKey, Vector], error) {
	return s.vectors, nil
}

func (s *store[T]) Items(ctx context.Context) (btree.BtreeInterface[ItemKey, Item[T]], error) {
	return s.items, nil
}

func (s *store[T]) Version(ctx context.Context) (int64, error) {
	return 0, nil
}

// QueryTextBatch performs a BM25 or keyword batch search on the stored text representation.
func (s *store[T]) QueryTextBatch(ctx context.Context, texts []string, opts *SearchOptions[T]) ([][]ai.Hit[T], error) {
	results := make([][]ai.Hit[T], len(texts))
	for i, txt := range texts {
		res, err := s.QueryText(ctx, txt, opts)
		if err != nil {
			return nil, err
		}
		results[i] = res
	}
	return results, nil
}

// QueryText performs a BM25 or keyword text search on the stored text representation of the thoughts.
// When a use-case already has a reliable category taxonomy, CategoryPath-based retrieval can be
// a cheaper SearchByPath-style alternative to enabling and maintaining the optional text index.
func (s *store[T]) QueryText(ctx context.Context, text string, opts *SearchOptions[T]) ([]ai.Hit[T], error) {
	if opts == nil {
		opts = &SearchOptions[T]{Limit: 10}
	}

	var targetCategoryID sop.UUID

	if len(opts.CategoryVector) > 0 {
		targetCat, err := s.resolveTargetCategory(ctx, opts.CategoryVector)
		if err != nil {
			return nil, err
		}
		if targetCat != nil {
			targetCategoryID = targetCat.ID
		}
		if targetCategoryID == sop.NilUUID {
			return nil, nil
		}
	} else if opts.CategoryPath != "" {
		ok, err := s.categoriesByPath.Find(ctx, opts.CategoryPath, false)
		if err == nil && ok {
			catID, err := s.categoriesByPath.GetCurrentValue(ctx)
			if err == nil {
				targetCategoryID = catID
			}
		}
		if targetCategoryID == sop.NilUUID {
			return nil, nil // Category strictly requested but not found
		}
	}

	if s.textIndex == nil {
		return nil, nil
	}

	searchResults, err := s.textIndex.Search(ctx, text)
	if err != nil {
		return nil, fmt.Errorf("text search failed: %w", err)
	}

	var results []ai.Hit[T]
	for _, res := range searchResults {
		if len(results) >= opts.Limit {
			break
		}

		parts := strings.Split(res.DocID, ",")
		if len(parts) != 2 {
			continue
		}
		catID, err := sop.ParseUUID(parts[0])
		if err != nil {
			continue
		}
		id, err := sop.ParseUUID(parts[1])
		if err != nil {
			continue
		}
		itemKeySearch := ItemKey{CategoryID: catID, ItemID: id}
		foundItem, err := s.items.Find(ctx, itemKeySearch, false)
		var payload T
		var docID DocIDs
		if foundItem && err == nil {
			item, err := s.items.GetCurrentValue(ctx)
			if err == nil {
				// Category strictly requested, filtering...
				if opts.CategoryPath != "" && item.CategoryID.Compare(targetCategoryID) != 0 {
					continue
				}

				payload = item.Data
				docID = item.DocID
				if opts.Filter != nil && !opts.Filter(payload) {
					continue
				}
			}
		}

		results = append(results, ai.Hit[T]{
			ID:      parts[1], // We use item ID here since res.DocID is formatted as "catID:itemID"
			DocID:   docID,
			Score:   float32(res.Score),
			Payload: payload,
		})
	}
	return results, nil
}

// GetCategoriesFromTextSearch extracts unique category IDs from text search results.
func (s *store[T]) GetCategoriesFromTextSearch(ctx context.Context, text string) ([]*Category, error) {
	if s.textIndex == nil {
		return nil, nil
	}

	searchResults, err := s.textIndex.Search(ctx, text)
	if err != nil {
		return nil, err
	}

	seenCatIDs := make(map[sop.UUID]struct{})
	var categories []*Category

	for _, res := range searchResults {
		parts := strings.Split(res.DocID, ",")
		if len(parts) != 2 {
			continue
		}
		catID, err := sop.ParseUUID(parts[0])
		if err != nil {
			continue
		}
		if _, seen := seenCatIDs[catID]; seen {
			continue
		}
		seenCatIDs[catID] = struct{}{}

		// Look up the category
		found, err := s.categories.Find(ctx, catID, false)
		if err == nil && found {
			cat, err := s.categories.GetCurrentValue(ctx)
			if err == nil && cat != nil && !cat.ID.IsNil() {
				categories = append(categories, cat)
			}
		}
	}

	return categories, nil
}
