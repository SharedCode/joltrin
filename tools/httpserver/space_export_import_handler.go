package main

import (
	"context"
	log "log/slog"
	"mime"
	"net/http"
	"strings"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai/database"
)

func handleExportSpace(w http.ResponseWriter, r *http.Request) {
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
		http.Error(w, "Failed to begin transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer trans.Rollback(ctx)

	kb, err := db.OpenKnowledgeBase(ctx, spaceName, trans, nil, nil, false)
	if err != nil {
		http.Error(w, "Failed to load Knowledge Base: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", exportContentDisposition(spaceName))
	if err := kb.ExportJSON(ctx, w); err != nil {
		// Cannot change HTTP status code after headers are written, just log
		log.Error("handleExportSpace ExportJSON failed", "error", err)
		return
	}
}

func exportContentDisposition(spaceName string) string {
	filename := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return '_'
		}
		return r
	}, spaceName+"_export.json")
	if filename == "_export.json" {
		filename = "export.json"
	}

	disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": filename,
	})
	if disposition == "" {
		return `attachment; filename="export.json"`
	}
	return disposition
}

func handleImportSpace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	dbName := r.FormValue("database")
	spaceName := r.FormValue("name")

	if dbName == "" || spaceName == "" {
		http.Error(w, "database and name are required", http.StatusBadRequest)
		return
	}

	file, fileHeader, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Failed to read uploaded file: "+err.Error(), http.StatusBadRequest)
		return
	}
	defer file.Close()

	dbOpt, err := getDBOptions(context.Background(), dbName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := context.Background()
	db := database.NewDatabase(dbOpt)

	trans, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		http.Error(w, "Failed to begin transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer trans.Rollback(ctx)

	kb, err := db.OpenKnowledgeBase(ctx, spaceName, trans, nil, nil, false)
	if err != nil {
		http.Error(w, "Failed to load Knowledge Base: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := kb.ImportJSON(ctx, file, ""); err != nil {
		http.Error(w, "Failed to import JSON: "+err.Error(), http.StatusInternalServerError)
		return
	}

	emb := GetConfiguredEmbedderForSpace(r, spaceName, fileHeader.Filename)
	if err := syncKnowledgeBaseEmbedderConfig(ctx, kb, emb); err != nil {
		http.Error(w, "Failed to update KnowledgeBase config: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if err := trans.Commit(ctx); err != nil {
		http.Error(w, "Failed to commit transaction: "+err.Error(), http.StatusInternalServerError)
		return
	}

	llm := GetConfiguredLLM(r)
	if err := autoVectorizeBuiltinSpace(ctx, db, spaceName, fileHeader.Filename, emb, llm, nil); err != nil {
		http.Error(w, "Failed to vectorize built-in space: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
