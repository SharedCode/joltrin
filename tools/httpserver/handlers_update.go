package main

import (
	"encoding/json"
	log "log/slog"
	"net/http"
	"time"

	aidb "github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/database"
)

// chunkSentences limits to a max number of sentences.
// We use a constant to ensure high semantic density without bloat.
const MAX_ITEM_SUMMARIES = 5

func handleUpdateSpaceItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req UpdateSpaceItemRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	newText, hasChunk := req.Data["chunk"].(string)
	if !hasChunk {
		newText, hasChunk = req.Data["Text"].(string)
	}

	if req.ID == "" {
		http.Error(w, "Missing ID", http.StatusBadRequest)
		return
	}
	if !hasChunk && len(req.Summaries) == 0 {
		http.Error(w, "Missing fields: must provide Data 'chunk' or 'Text', or 'summaries'", http.StatusBadRequest)
		return
	}

	itemID, err := sop.ParseUUID(req.ID)
	if err != nil {
		http.Error(w, "Invalid ID", http.StatusBadRequest)
		return
	}

	ctx := r.Context()
	storeName := r.URL.Query().Get("name")
	dbName := r.URL.Query().Get("database")
	if storeName == "" {
		http.Error(w, "Knowledge Base name is required", http.StatusBadRequest)
		return
	}

	dbOpts, err := getDBOptions(ctx, dbName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	trans, err := database.BeginTransaction(ctx, dbOpts, sop.ForWriting)
	if err != nil {
		http.Error(w, "Failed to begin transaction", http.StatusInternalServerError)
		return
	}
	defer trans.Rollback(ctx)

	db := aidb.NewDatabase(dbOpts)
	dbEmbedder := GetConfiguredEmbedderForSpace(r, storeName, "")
	dbLLM := GetConfiguredLLM(r)

	kb, err := db.OpenKnowledgeBase(ctx, storeName, trans, dbLLM, dbEmbedder, false)
	if err != nil {
		http.Error(w, "Failed to open knowledge base", http.StatusInternalServerError)
		return
	}

	itemsTree, err := kb.Store.Items(ctx)
	if err != nil {
		http.Error(w, "Failed to access items", http.StatusInternalServerError)
		return
	}

	// Fetch existing
	var foundKey memory.ItemKey
	var found bool

	if req.CategoryID == "" {
		http.Error(w, "Missing CategoryID", http.StatusBadRequest)
		return
	}

	parsedCatID, catErr := sop.ParseUUID(req.CategoryID)
	if catErr != nil {
		log.Error("Error encountered while parsing Category ID", "err", catErr)
		http.Error(w, "Invalid CategoryID", http.StatusBadRequest)
		return
	}

	foundKey = memory.ItemKey{CategoryID: parsedCatID, ItemID: itemID}
	if found, err = itemsTree.Find(ctx, foundKey, false); err != nil {
		log.Error("Error encountered while reading Item", "err", err)
		http.Error(w, "Error encountered while reading Item", http.StatusInternalServerError)
		return
	}

	if !found {
		http.Error(w, "Item not found", http.StatusNotFound)
		return
	}
	existingItem, _ := itemsTree.GetCurrentValue(ctx)

	// Merge data (twist for UI integration)
	mergedData := existingItem.Data
	if mergedData == nil {
		mergedData = make(map[string]any)
	}

	// Apply all incoming data props to our existing data to retain other fields
	for k, v := range req.Data {
		mergedData[k] = v
	}

	// Fetch category to get SummaryMaxCount and Name
	maxSummaries := MAX_ITEM_SUMMARIES
	var categoryName string
	categoriesTree, err := kb.Store.Categories(ctx)
	if err == nil {
		catFound, _ := categoriesTree.Find(ctx, existingItem.CategoryID, false)
		if catFound {
			if cat, _ := categoriesTree.GetCurrentValue(ctx); cat != nil {
				categoryName = cat.Name
				if cat.SummaryMaxCount > 0 {
					maxSummaries = cat.SummaryMaxCount
				}
			}
		}
	}

	if categoryName == "" {
		http.Error(w, "Category not found for existing item", http.StatusInternalServerError)
		return
	}

	mergedDataBytes, _ := json.Marshal(mergedData)
	mergedDataStr := string(mergedDataBytes)

	textToSummarize := ""
	if hasChunk {
		textToSummarize = newText
	}

	summarizer := GetSummarizer(config.ProductionMode, GetConfiguredLLMClient(r)) // In future, inject active LLM here
	summaries := DetermineSummaries(ctx, summarizer, req.Summaries, textToSummarize, mergedDataStr, maxSummaries)

	// Create multiple vectors with Differential Sync/Merge
	var vecs [][]float32

	if len(req.Positions) > 0 && len(req.Positions) == len(summaries) {
		vecs = req.Positions
	} else {
		vectorsTree, err := kb.Store.Vectors(ctx)
		if err != nil {
			http.Error(w, "Failed to access vectors tree", http.StatusInternalServerError)
			return
		}

		// 1. O(N) Match existing summaries to their previous vector math
		existingSummariesMap := make(map[string][]float32)
		for i, summary := range existingItem.Summaries {
			if i < len(existingItem.Positions) {
				vKey := existingItem.Positions[i]
				found, err := vectorsTree.Find(ctx, vKey, false)
				if err == nil && found {
					vecObj, _ := vectorsTree.GetCurrentValue(ctx)
					existingSummariesMap[summary] = vecObj.Data
				}
			}
		}

		vecs = make([][]float32, len(summaries))

		// 2. Identify unchanged vectors to recycle vs changes to embed
		for i, summary := range summaries {
			if existingVec, ok := existingSummariesMap[summary]; ok {
				vecs[i] = existingVec
			}
		}
	}

	newItem := memory.Item[map[string]any]{
		ID:         itemID,
		CategoryID: existingItem.CategoryID,
		Summaries:  summaries,
		Data:       mergedData,
	}

	err = kb.Store.UpsertByCategoryPath(ctx, categoryName, newItem, vecs)
	if err != nil {
		http.Error(w, "Upsert failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if cfg, err := kb.GetConfig(ctx); err == nil {
		if cfg == nil {
			cfg = &memory.KnowledgeBaseConfig{}
		}
		cfg.LastModified = time.Now().Unix()
		kb.SetConfig(ctx, cfg)
	}

	if err := trans.Commit(ctx); err != nil {
		http.Error(w, "Commit failed", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
