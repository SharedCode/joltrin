package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"time"

	log "log/slog"

	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

type KnowledgeChunk struct {
	Category string `json:"category"`
	Title    string `json:"title"`
	Content  string `json:"content"`
}

func (s *Service) SeedSemanticBaseKnowledge(jsonFilePath string) {
	if s.domain == nil || !s.EnableShortTermMemory {
		log.Warn("SeedSemanticBaseKnowledge aborted: Domain not initialized or Active Memory disabled")
		return
	}

	go func() {
		embedCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()

		log.Info("SeedSemanticBaseKnowledge: Starting async RAG documentation ingestion", "file", jsonFilePath)

		data, err := os.ReadFile(jsonFilePath)
		if err != nil {
			log.Warn("SeedSemanticBaseKnowledge failed to read file", "error", err)
			return
		}

		var chunks []KnowledgeChunk
		if err := json.Unmarshal(data, &chunks); err != nil {
			log.Warn("SeedSemanticBaseKnowledge failed to unmarshal json", "error", err)
			return
		}

		txtx, err := s.domain.BeginTransaction(embedCtx, sop.ForReading)
		if err != nil {
			log.Warn("SeedSemanticBaseKnowledge: Failed to start transaction", "error", err)
			return
		}

		memAny, err := s.domain.Memory(embedCtx, txtx)
		if err != nil {
			txtx.Rollback(embedCtx)
			log.Warn("SeedSemanticBaseKnowledge: Failed to get MemoryBase", "error", err)
			return
		}

		_, ok := memAny.(*memory.KnowledgeBase[map[string]any])
		if !ok {
			txtx.Rollback(embedCtx)
			log.Warn("SeedSemanticBaseKnowledge: Invalid MemoryBase interface")
			return
		}
		txtx.Rollback(embedCtx)

		log.Info("SeedSemanticBaseKnowledge: Ingesting chunks...", "count", len(chunks))

		// We can do it thought by thought efficiently
		for i, chunk := range chunks {
			chunkID := fmt.Sprintf("core_knowledge_%d_%x", i, sha256.Sum256([]byte(chunk.Title+chunk.Content)))

			// A reading transaction to see if it's there? No, memory Upsert deduplicates.
			batchText := fmt.Sprintf("Base Knowledge -> %s -> %s\n%s", chunk.Category, chunk.Title, chunk.Content)

			payload := map[string]any{
				"id":       chunkID,
				"category": chunk.Category,
				"title":    chunk.Title,
				"ts":       time.Now().UnixMilli(),
				"type":     "semantic_base",
				"content":  chunk.Content,
			}

			tx, err := s.domain.BeginTransaction(embedCtx, sop.ForWriting)
			if err != nil {
				continue
			}
			kbTx, _ := s.domain.Memory(embedCtx, tx)
			kbW := kbTx.(*memory.KnowledgeBase[map[string]any])

			err = kbW.IngestThought(embedCtx, batchText, chunk.Category, "System", nil, payload)
			if err != nil {
				tx.Rollback(embedCtx)
				log.Warn("SeedSemanticBaseKnowledge: Failed to upsert thought", "error", err)
			} else {
				tx.Commit(embedCtx)
			}
		}

		log.Info("SeedSemanticBaseKnowledge: Documentation RAG ingestion complete!", "chunks", len(chunks))
	}()
}
