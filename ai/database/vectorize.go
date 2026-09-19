package database

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/sharedcode/joltrin/ai/memory"

	log "log/slog"
	"time"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/embed"
	"github.com/sharedcode/joltrin/ai/internal/logsafe"
)

// TODO: refactor Vectorize & VectorizeCategories so they can share common Category/Batched Items'
// vectorizer private function.

const maxDomainAnchorLength = 256

func chooseDomainAnchor(kb *memory.KnowledgeBase[map[string]any], cfg *memory.KnowledgeBaseConfig) string {
	if name := strings.TrimSpace(kb.Store.Name()); name != "" && len([]rune(name)) <= maxDomainAnchorLength {
		return name
	}
	if desc := strings.TrimSpace(cfg.Description); desc != "" && len([]rune(desc)) <= maxDomainAnchorLength {
		return desc
	}
	return strings.TrimSpace(cfg.SystemPrompt)
}

func ensureDomainReference(ctx context.Context, kb *memory.KnowledgeBase[map[string]any], cfg *memory.KnowledgeBaseConfig, embedder ai.Embeddings) error {
	if kb == nil || cfg == nil {
		return nil
	}

	if len(cfg.DomainReference) == 0 {
		anchor := chooseDomainAnchor(kb, cfg)
		if anchor == "" {
			return nil
		}

		vecs, err := embed.CategoryTexts(ctx, embedder, []string{anchor})
		if err != nil {
			return err
		}
		if len(vecs) == 0 || len(vecs[0]) == 0 {
			return nil
		}
		cfg.DomainReference = vecs[0]
	}

	kb.Store.SetDomainReference(cfg.DomainReference)
	return kb.SetConfig(ctx, cfg)
}

// Vectorize processes the store's KnowledgeBase in batches, managing its own transactions and cursor state.
func (db *Database) Vectorize(
	ctx context.Context,
	kbName string,
	llm ai.Generator,
	embedder ai.Embeddings,
	batchSize int,
) error {
	log.Info("Vectorize started", "kb_name", logsafe.V(kbName), "batchSize", batchSize)

	embedder = embed.NewResilientEmbedder(embedder)
	embedderDim := embedder.Dim()

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return err
	}

	kb, err := db.OpenKnowledgeBase(ctx, kbName, tx, llm, embedder, false)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	catsByDist, _ := kb.Store.CategoriesByDistance(ctx)

	cfg, err := kb.GetConfig(ctx)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	if cfg == nil {
		cfg = &memory.KnowledgeBaseConfig{
			EmbedderDimension: embedderDim,
		}
	}
	if err := ensureDomainReference(ctx, kb, cfg, embedder); err != nil {
		tx.Rollback(ctx)
		return err
	}

	catBtree, err := kb.Store.Categories(ctx)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}

	categoriesByPath, err := kb.Store.CategoriesByPath(ctx)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}

	itemsBtree, err := kb.Store.Items(ctx)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}

	ok, err := categoriesByPath.First(ctx)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}

	oldCategoryVectors := make(map[sop.UUID][]float32)
	newCategoryVectors := make(map[sop.UUID][]float32)

	type batchTask struct {
		item       *memory.Item[map[string]any]
		catID      sop.UUID
		centerVec  []float32
		summaryCnt int
	}

	batchTasks := make([]batchTask, 0, batchSize)
	var batchSummaries []string

	log.Debug("Before categories loop")
	for ok {
		catID, err := categoriesByPath.GetCurrentValue(ctx)
		if err != nil {
			tx.Rollback(ctx)
			break
		}

		found, err := catBtree.Find(ctx, catID, false)
		if err != nil || !found {
			tx.Rollback(ctx)
			break
		}

		cat, err := catBtree.GetCurrentValue(ctx)
		if err != nil || cat == nil {
			tx.Rollback(ctx)
			break
		}

		log.Debug("Processing category", "catID", catID, "catName", cat.Name)

		if len(cat.CenterVector) > 0 {
			oldCategoryVectors[cat.ID] = cat.CenterVector
		} else {
			oldCategoryVectors[cat.ID] = nil
		}

		catCenterVector := cat.CenterVector

		// Vectorize the category itself using its own canonicalized name as the semantic anchor.
		embedText := strings.TrimSpace(cat.Name)
		if embedText == "" {
			embedText = strings.TrimSpace(cat.Description)
		}
		embedText = memory.NormalizeCategoryToken(embedText)
		expectedCatHash := memory.ComputeVectorHash(embedderDim, embedText)
		if cat.VectorHash != expectedCatHash || len(cat.CenterVector) == 0 {
			log.Debug("Vectorizing category", "catID", catID)
			catVecs, err := embed.CategoryTexts(ctx, embedder, []string{embedText})
			if err != nil {
				tx.Rollback(ctx)
				return err
			}
			if len(catVecs) > 0 {
				if len(cat.CenterVector) > 0 {
					if len(cat.ParentIDs) > 0 {
						for _, p := range cat.ParentIDs {
							oldDist := memory.Distance(oldCategoryVectors[p.ParentID], cat.CenterVector, true)
							catsByDist.Remove(ctx, memory.DistanceKey{ParentID: p.ParentID, Distance: oldDist, ID: cat.ID})
						}
					} else {
						oldDist := memory.Distance(kb.Store.DomainReference(), cat.CenterVector, true)
						catsByDist.Remove(ctx, memory.DistanceKey{ParentID: sop.NilUUID, Distance: oldDist, ID: cat.ID})
					}
				}

				cat.CenterVector = catVecs[0]
				catCenterVector = cat.CenterVector
				cat.VectorHash = expectedCatHash
				newCategoryVectors[cat.ID] = cat.CenterVector

				if len(cat.ParentIDs) > 0 {
					for _, p := range cat.ParentIDs {
						refVec := newCategoryVectors[p.ParentID]
						if len(refVec) == 0 {
							refVec = oldCategoryVectors[p.ParentID]
						}
						newDist := memory.Distance(refVec, cat.CenterVector, true)
						catsByDist.Add(ctx, memory.DistanceKey{ParentID: p.ParentID, Distance: newDist, ID: cat.ID}, 0)
					}
				} else {
					newDist := memory.Distance(kb.Store.DomainReference(), cat.CenterVector, true)
					catsByDist.Add(ctx, memory.DistanceKey{ParentID: sop.NilUUID, Distance: newDist, ID: cat.ID}, 0)
				}

				catBtree.UpdateCurrentItem(ctx, cat.ID, cat)
			}
		}

		// Now process Items for this Category
		totalItemCount := 0
		iok, err := itemsBtree.Find(ctx, memory.ItemKey{CategoryID: catID, ItemID: sop.NilUUID}, false)
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		if !iok {
			// SOP Btree positions to closest item automatically on false. Evaluate it.
			iok = true
		}

		var lastItemInCat sop.UUID = sop.NilUUID

		log.Debug("Before items loop", "catID", catID)
		for iok {
			item, err := itemsBtree.GetCurrentValue(ctx)

			// Stop if cursor hits the end of BTree or leaves the category bounds
			if err != nil || item.CategoryID.Compare(catID) != 0 {
				break
			}

			if item.IsConfig() {
				iok, _ = itemsBtree.Next(ctx)
				continue
			}

			totalItemCount++
			lastItemInCat = item.ID

			dataStr := ""
			if len(item.Summaries) == 0 {
				if str, isStr := any(item.Data).(string); isStr {
					dataStr = str
				} else {
					b, _ := json.Marshal(item.Data)
					dataStr = string(b)
				}
			}

			hashTexts := item.Summaries
			if len(hashTexts) == 0 {
				hashTexts = []string{dataStr}
			}

			expectedHash := memory.ComputeVectorHash(embedderDim, hashTexts...)

			if item.VectorHash != expectedHash || len(item.Positions) == 0 {
				item.VectorHash = expectedHash
				if len(item.Summaries) == 0 {
					item.Summaries = []string{dataStr}
				}

				itemCopy := item
				batchTasks = append(batchTasks, batchTask{
					item:       &itemCopy,
					catID:      catID,
					centerVec:  catCenterVector,
					summaryCnt: len(itemCopy.Summaries),
				})
				batchSummaries = append(batchSummaries, itemCopy.Summaries...)
			}

			// If batch is full, flush and commit
			if len(batchTasks) >= batchSize {
				if len(batchSummaries) > 0 {
					log.Debug("Vectorize embedding items batch", "kb_name", logsafe.V(kbName), "count", len(batchSummaries))
					// Embed as DocumentTexts for item storage (768 dim)
					allVecs, err := embed.DocumentTexts(ctx, embedder, batchSummaries)
					if err != nil {
						tx.Rollback(ctx)
						return err
					}
					// Embed as CategoryTexts for classification (256 dim)
					allClassificationVecs, err := embed.CategoryTexts(ctx, embedder, batchSummaries)
					if err != nil {
						tx.Rollback(ctx)
						return err
					}

					log.Debug("before batch processing via kb.Store,UpsertByCategoryID")
					vecIdx := 0
					for _, task := range batchTasks {
						count := task.summaryCnt
						itemVecs := allVecs[vecIdx : vecIdx+count]
						classificationVecs := allClassificationVecs[vecIdx : vecIdx+count]
						vecIdx += count

						err = kb.Store.UpsertByCategoryID(ctx, task.catID, task.centerVec, *task.item, itemVecs, classificationVecs)
						if err != nil {
							tx.Rollback(ctx)
							return err
						}
					}
					log.Debug("after batch processing via kb.Store,UpsertByCategoryID")
				}

				// Reset batch
				batchTasks = batchTasks[:0]
				batchSummaries = batchSummaries[:0]

				if err := tx.Commit(ctx); err != nil {
					return err
				}

				// Start new transaction
				tx, err = db.BeginTransaction(ctx, sop.ForWriting)
				if err != nil {
					return err
				}
				kb, err = db.OpenKnowledgeBase(ctx, kbName, tx, llm, embedder, false)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
				catBtree, err = kb.Store.Categories(ctx)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
				categoriesByPath, err = kb.Store.CategoriesByPath(ctx)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
				itemsBtree, err = kb.Store.Items(ctx)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
				catsByDist, err = kb.Store.CategoriesByDistance(ctx)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}

				// Restore category cursors
				catPath := cat.Path
				if catPath == "" {
					catPath = cat.Name
				}
				_, err = categoriesByPath.Find(ctx, catPath, false)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
				_, err = catBtree.Find(ctx, catID, false)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
				// Restore item cursor
				fok, err := itemsBtree.Find(ctx, memory.ItemKey{CategoryID: catID, ItemID: lastItemInCat}, false)
				log.Debug("Restored item cursor", "fok", fok, "err", err, "lastItemInCat", lastItemInCat)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
			}

			iok, err = itemsBtree.Next(ctx)
			if err != nil {
				tx.Rollback(ctx)
				return err
			}
			log.Debug("After item cursor restore, Next", "iok", iok, "err", err)
		}
		log.Debug("After items loop", "catID", catID, "totalItemCount", totalItemCount)

		// Category finished processing. Update ItemCount if necessary
		if cat.ItemCount != totalItemCount {
			cat.ItemCount = totalItemCount
			catBtree.UpdateCurrentItem(ctx, cat.ID, cat)
		}

		ok, err = categoriesByPath.Next(ctx)
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
	}
	log.Debug("After categories loop")

	// Flush any remaining batch
	if len(batchTasks) > 0 {
		if len(batchSummaries) > 0 {
			log.Debug("Vectorize embedding items batch (final)", "kb_name", logsafe.V(kbName), "count", len(batchSummaries))
			// Embed as DocumentTexts for item storage (768 dim)
			allVecs, err := embed.DocumentTexts(ctx, embedder, batchSummaries)
			if err != nil {
				tx.Rollback(ctx)
				return err
			}
			// Embed as CategoryTexts for classification (256 dim)
			allClassificationVecs, err := embed.CategoryTexts(ctx, embedder, batchSummaries)
			if err != nil {
				tx.Rollback(ctx)
				return err
			}

			vecIdx := 0
			for _, task := range batchTasks {
				count := task.summaryCnt
				itemVecs := allVecs[vecIdx : vecIdx+count]
				classificationVecs := allClassificationVecs[vecIdx : vecIdx+count]
				vecIdx += count

				err = kb.Store.UpsertByCategoryID(ctx, task.catID, task.centerVec, *task.item, itemVecs, classificationVecs)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
			}
		}
	}

	cfg.LastVectorized = time.Now().Unix()
	log.Debug("Saving LastVectorized timestamp", "LastVectorized", cfg.LastVectorized)
	kb.SetConfig(ctx, cfg)
	return tx.Commit(ctx)
}

// Vectorize processes the store's KnowledgeBase in batches, managing its own transactions and cursor state.
func (db *Database) VectorizeCategories(
	ctx context.Context,
	kbName string,
	llm ai.Generator,
	embedder ai.Embeddings,
	batchSize int,
	categoryIDs []sop.UUID,
) error {
	log.Info("vectorizeCategories started", "kb_name", logsafe.V(kbName), "batchSize", batchSize)

	embedder = embed.NewResilientEmbedder(embedder)
	embedderDim := embedder.Dim()

	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return err
	}

	kb, err := db.OpenKnowledgeBase(ctx, kbName, tx, llm, embedder, false)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	catsByDist, _ := kb.Store.CategoriesByDistance(ctx)

	cfg, err := kb.GetConfig(ctx)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}
	if cfg == nil {
		cfg = &memory.KnowledgeBaseConfig{
			EmbedderDimension: embedderDim,
		}
	}
	if err := ensureDomainReference(ctx, kb, cfg, embedder); err != nil {
		tx.Rollback(ctx)
		return err
	}

	catBtree, err := kb.Store.Categories(ctx)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}

	itemsBtree, err := kb.Store.Items(ctx)
	if err != nil {
		tx.Rollback(ctx)
		return err
	}

	oldCategoryVectors := make(map[sop.UUID][]float32)
	newCategoryVectors := make(map[sop.UUID][]float32)

	type batchTask struct {
		item       *memory.Item[map[string]any]
		catID      sop.UUID
		centerVec  []float32
		summaryCnt int
	}

	batchTasks := make([]batchTask, 0, batchSize)
	var batchSummaries []string

	log.Debug("Before categories loop")
	for _, catID := range categoryIDs {
		found, err := catBtree.Find(ctx, catID, false)
		if err != nil || !found {
			tx.Rollback(ctx)
			break
		}

		cat, err := catBtree.GetCurrentValue(ctx)
		if err != nil || cat == nil {
			tx.Rollback(ctx)
			break
		}

		log.Debug("Processing category", "catID", catID, "catName", cat.Name)

		if len(cat.CenterVector) > 0 {
			oldCategoryVectors[cat.ID] = cat.CenterVector
		} else {
			oldCategoryVectors[cat.ID] = nil
		}

		catCenterVector := cat.CenterVector

		// Vectorize the category itself using its own canonicalized name as the semantic anchor.
		embedText := strings.TrimSpace(cat.Name)
		if embedText == "" {
			embedText = strings.TrimSpace(cat.Description)
		}
		embedText = memory.NormalizeCategoryToken(embedText)
		expectedCatHash := memory.ComputeVectorHash(embedderDim, embedText)
		if cat.VectorHash != expectedCatHash || len(cat.CenterVector) == 0 {
			log.Debug("Vectorizing category", "catID", catID)
			catVecs, err := embed.CategoryTexts(ctx, embedder, []string{embedText})
			if err != nil {
				tx.Rollback(ctx)
				return err
			}
			if len(catVecs) > 0 {
				if len(cat.CenterVector) > 0 {
					if len(cat.ParentIDs) > 0 {
						for _, p := range cat.ParentIDs {
							oldDist := memory.Distance(oldCategoryVectors[p.ParentID], cat.CenterVector, true)
							catsByDist.Remove(ctx, memory.DistanceKey{ParentID: p.ParentID, Distance: oldDist, ID: cat.ID})
						}
					} else {
						oldDist := memory.Distance(kb.Store.DomainReference(), cat.CenterVector, true)
						catsByDist.Remove(ctx, memory.DistanceKey{ParentID: sop.NilUUID, Distance: oldDist, ID: cat.ID})
					}
				}

				cat.CenterVector = catVecs[0]
				catCenterVector = cat.CenterVector
				cat.VectorHash = expectedCatHash
				newCategoryVectors[cat.ID] = cat.CenterVector

				if len(cat.ParentIDs) > 0 {
					for _, p := range cat.ParentIDs {
						refVec := newCategoryVectors[p.ParentID]
						if len(refVec) == 0 {
							refVec = oldCategoryVectors[p.ParentID]
						}
						newDist := memory.Distance(refVec, cat.CenterVector, true)
						catsByDist.Add(ctx, memory.DistanceKey{ParentID: p.ParentID, Distance: newDist, ID: cat.ID}, 0)
					}
				} else {
					newDist := memory.Distance(kb.Store.DomainReference(), cat.CenterVector, true)
					catsByDist.Add(ctx, memory.DistanceKey{ParentID: sop.NilUUID, Distance: newDist, ID: cat.ID}, 0)
				}

				catBtree.UpdateCurrentItem(ctx, cat.ID, cat)
			}
		}

		// Now process Items for this Category
		totalItemCount := 0
		iok, err := itemsBtree.Find(ctx, memory.ItemKey{CategoryID: catID, ItemID: sop.NilUUID}, false)
		if err != nil {
			tx.Rollback(ctx)
			return err
		}
		if !iok {
			// SOP Btree positions to closest item automatically on false. Evaluate it.
			iok = true
		}

		var lastItemInCat sop.UUID = sop.NilUUID

		log.Debug("Before items loop", "catID", catID)
		for iok {
			item, err := itemsBtree.GetCurrentValue(ctx)

			// Stop if cursor hits the end of BTree or leaves the category bounds
			if err != nil || item.CategoryID.Compare(catID) != 0 {
				break
			}

			if item.IsConfig() {
				iok, _ = itemsBtree.Next(ctx)
				continue
			}

			totalItemCount++
			lastItemInCat = item.ID

			dataStr := ""
			if len(item.Summaries) == 0 {
				if str, isStr := any(item.Data).(string); isStr {
					dataStr = str
				} else {
					b, _ := json.Marshal(item.Data)
					dataStr = string(b)
				}
			}

			hashTexts := item.Summaries
			if len(hashTexts) == 0 {
				hashTexts = []string{dataStr}
			}

			expectedHash := memory.ComputeVectorHash(embedderDim, hashTexts...)

			if item.VectorHash != expectedHash || len(item.Positions) == 0 {
				item.VectorHash = expectedHash
				if len(item.Summaries) == 0 {
					item.Summaries = []string{dataStr}
				}

				itemCopy := item
				batchTasks = append(batchTasks, batchTask{
					item:       &itemCopy,
					catID:      catID,
					centerVec:  catCenterVector,
					summaryCnt: len(itemCopy.Summaries),
				})
				batchSummaries = append(batchSummaries, itemCopy.Summaries...)
			}

			// If batch is full, flush and commit
			if len(batchTasks) >= batchSize {
				if len(batchSummaries) > 0 {
					log.Debug("Vectorize embedding items batch", "kb_name", logsafe.V(kbName), "count", len(batchSummaries))
					// Embed as DocumentTexts for item storage (768 dim)
					allVecs, err := embed.DocumentTexts(ctx, embedder, batchSummaries)
					if err != nil {
						tx.Rollback(ctx)
						return err
					}
					// Embed as CategoryTexts for classification (256 dim)
					allClassificationVecs, err := embed.CategoryTexts(ctx, embedder, batchSummaries)
					if err != nil {
						tx.Rollback(ctx)
						return err
					}

					log.Debug("before batch processing via kb.Store,UpsertByCategoryID")
					vecIdx := 0
					for _, task := range batchTasks {
						count := task.summaryCnt
						itemVecs := allVecs[vecIdx : vecIdx+count]
						classificationVecs := allClassificationVecs[vecIdx : vecIdx+count]
						vecIdx += count

						err = kb.Store.UpsertByCategoryID(ctx, task.catID, task.centerVec, *task.item, itemVecs, classificationVecs)
						if err != nil {
							tx.Rollback(ctx)
							return err
						}
					}
					log.Debug("after batch processing via kb.Store,UpsertByCategoryID")
				}

				// Reset batch
				batchTasks = batchTasks[:0]
				batchSummaries = batchSummaries[:0]

				if err := tx.Commit(ctx); err != nil {
					return err
				}

				// Start new transaction
				tx, err = db.BeginTransaction(ctx, sop.ForWriting)
				if err != nil {
					return err
				}
				kb, err = db.OpenKnowledgeBase(ctx, kbName, tx, llm, embedder, false)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
				catBtree, err = kb.Store.Categories(ctx)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
				itemsBtree, err = kb.Store.Items(ctx)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
				catsByDist, err = kb.Store.CategoriesByDistance(ctx)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}

				// Restore category cursors
				_, err = catBtree.Find(ctx, catID, false)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
				// Restore item cursor
				fok, err := itemsBtree.Find(ctx, memory.ItemKey{CategoryID: catID, ItemID: lastItemInCat}, false)
				log.Debug("Restored item cursor", "fok", fok, "err", err, "lastItemInCat", lastItemInCat)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
			}

			iok, err = itemsBtree.Next(ctx)
			log.Debug("After item cursor restore, Next", "iok", iok, "err", err)
		}
		log.Debug("After items loop", "catID", catID, "totalItemCount", totalItemCount)

		// Category finished processing. Update ItemCount if necessary
		if cat.ItemCount != totalItemCount {
			cat.ItemCount = totalItemCount
			catBtree.UpdateCurrentItem(ctx, cat.ID, cat)
		}
	}
	log.Debug("After categories loop")

	// Flush any remaining batch
	if len(batchTasks) > 0 {
		if len(batchSummaries) > 0 {
			log.Debug("Vectorize embedding items batch (final)", "kb_name", logsafe.V(kbName), "count", len(batchSummaries))
			// Embed as DocumentTexts for item storage (768 dim)
			allVecs, err := embed.DocumentTexts(ctx, embedder, batchSummaries)
			if err != nil {
				tx.Rollback(ctx)
				return err
			}
			// Embed as CategoryTexts for classification (256 dim)
			allClassificationVecs, err := embed.CategoryTexts(ctx, embedder, batchSummaries)
			if err != nil {
				tx.Rollback(ctx)
				return err
			}

			vecIdx := 0
			for _, task := range batchTasks {
				count := task.summaryCnt
				itemVecs := allVecs[vecIdx : vecIdx+count]
				classificationVecs := allClassificationVecs[vecIdx : vecIdx+count]
				vecIdx += count

				err = kb.Store.UpsertByCategoryID(ctx, task.catID, task.centerVec, *task.item, itemVecs, classificationVecs)
				if err != nil {
					tx.Rollback(ctx)
					return err
				}
			}
		}
	}

	return tx.Commit(ctx)
}
