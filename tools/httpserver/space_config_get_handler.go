package main

import (
	"github.com/sharedcode/joltrin/ai/memory"

	"context"
	"encoding/json"
	"net/http"

	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/v5"
)

func handleGetSpaceConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	dbName := r.URL.Query().Get("database")
	spaceName := r.URL.Query().Get("name")

	if dbName == "" || spaceName == "" {
		http.Error(w, "database and name are required", http.StatusBadRequest)
		return
	}

	dbOpt, err := getDBOptions(context.Background(), dbName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	db := database.NewDatabase(dbOpt)
	trans, err := db.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		http.Error(w, "Failed to connect to database: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer trans.Rollback(ctx)

	kb, err := db.OpenKnowledgeBase(ctx, spaceName, trans, nil, nil, false)
	if err != nil {
		http.Error(w, "Failed to open Space: "+err.Error(), http.StatusInternalServerError)
		return
	}

	cfg, err := kb.GetConfig(ctx)

	// Apply Authorization/RBAC visibility layer
	if err != nil {
		http.Error(w, "Failed to get config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	systemEmbedder := GetConfiguredEmbedder(nil)
	sysDim := systemEmbedder.Dim()

	spaceDim := sysDim
	if cfg != nil && cfg.EmbedderDimension != 0 {
		spaceDim = cfg.EmbedderDimension
	}

	pinnedEmbedder, pinnedReadOnly := pinnedInternalKBSettings(spaceName)
	if pinnedEmbedder != "" {
		pinnedInstance := GetConfiguredEmbedderForSpace(nil, spaceName, "")
		if pinnedInstance != nil {
			sysDim = pinnedInstance.Dim()
			spaceDim = pinnedInstance.Dim()
		}
	}

	response := struct {
		memory.KnowledgeBaseConfig `json:",inline"`
		IsReadOnly                 bool `json:"is_read_only"`
		SystemEmbedderDimension    int  `json:"system_embedder_dimension"`
		SpaceEmbedderDimension     int  `json:"space_embedder_dimension"`
	}{
		IsReadOnly:              pinnedReadOnly || !sop.CanPerformAction(ctx, spaceName, sop.ResourceAccess{}, sop.ActionWrite),
		SystemEmbedderDimension: sysDim,
		SpaceEmbedderDimension:  spaceDim,
	}

	if cfg != nil {
		response.KnowledgeBaseConfig = *cfg
		if pinnedEmbedder != "" {
			response.Embedder = pinnedEmbedder
		}
	}

	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		http.Error(w, "Failed to encode config: "+err.Error(), http.StatusInternalServerError)
	}
}
