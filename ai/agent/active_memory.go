package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	log "log/slog"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

// InitializeShortTermMemory buffers the DDL creation of the active scratchpad B-Tree store.
// This MUST be called sequentially during Service setup before DML (logEpisode) operations begin.
func (s *Service) InitializeShortTermMemory(ctx context.Context) error {
	if s.systemDB == nil {
		return fmt.Errorf("systemDB is not configured")
	}

	agentID := ai.AgentIDOmni
	userID := ""
	if p := ai.GetSessionPayload(ctx); p != nil {
		if p.AgentID != "" {
			agentID = p.AgentID
		}
		userID = strings.TrimSpace(p.UserID)
	}

	storeName := memory.BuildSTMStoreName(agentID, userID)
	exists, err := s.systemDB.StoreExists(ctx, storeName)
	if err != nil {
		return fmt.Errorf("failed to check STM store existence: %w", err)
	}

	if !exists {
		tx, err := s.systemDB.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return fmt.Errorf("failed to begin transaction for STM initialization: %w", err)
		}

		store, err := s.systemDB.NewBtree(ctx, storeName, tx)
		if err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("failed to initialize STM buffer store: %w", err)
		}

		// Insert a root anchor item to give identity to the btree immediately upon DDL.
		rootAnchor := map[string]any{
			"id":         "root_anchor",
			"user_id":    userID,
			"session_id": "system",
			"intent":     "Initialization",
			"ts":         time.Now().UnixMilli(),
			"type":       "system_anchor",
			"thought":    "Active memory root anchor initialized.",
		}
		_, err = store.Add(ctx, "root_anchor", rootAnchor)
		if err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("failed to insert root anchor into STM buffer store: %w", err)
		}

		err = tx.Commit(ctx)
		if err != nil {
			return fmt.Errorf("failed to commit STM initialization transaction: %w", err)
		}
		log.Info("ShortTermMemory: Successfully initialized DDL and root anchor", "store", storeName, "user_id", userID)
	}

	return nil
}

// ==========================================
// PHASE 5: Auto-Enrichment for Standard Spaces (KBs)
// ==========================================

// triggerSpaceAutoEnrichment iterates over all standard domain KBs and invokes their Sleep Cycle if AllowAutoEnrichment is enabled.
func (s *Service) triggerSpaceAutoEnrichment(ctx context.Context) {
	if len(s.databases) == 0 {
		return
	}

	// Iterate over all tenant databases
	for dbName, opts := range s.databases {
		db := database.NewDatabase(opts)

		// Get all stores to identify active knowledge bases
		stores, err := db.GetStores(ctx)
		if err != nil {
			log.Warn("SpaceAutoEnrichment: Failed to get stores", "db", dbName, "error", err)
			continue
		}

		// Find unique KB names by looking for the /categories or /items suffix
		uniqueKBs := make(map[string]bool)
		for _, storeName := range stores {
			if strings.HasSuffix(storeName, "/categories") {
				kbName := strings.TrimSuffix(storeName, "/categories")
				uniqueKBs[kbName] = true
			}
		}

		for kbName := range uniqueKBs {
			// Do not auto-enrich agent LTM stores here. They are maintained by the STM/LTM memory pipeline.
			if strings.HasPrefix(kbName, "ltm_") {
				continue
			}

			err := s.enrichSingleKB(ctx, db, kbName)
			if err != nil {
				log.Warn("SpaceAutoEnrichment: Failed enrichment", "db", dbName, "kb", kbName, "error", err)
			}
		}
	}
}

func (s *Service) enrichSingleKB(ctx context.Context, db *database.Database, kbName string) error {
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	// Fallback to domain embedder if needed, but wait: does db have an embedder?
	// The domain holds the embedder. We can use s.domain.Embedder() since it's the global one.
	var embedder ai.Embeddings
	if s.domain != nil {
		embedder = s.domain.Embedder()
	}

	kb, err := db.OpenKnowledgeBase(ctx, kbName, tx, s.generator, embedder, false)
	if err != nil {
		return err
	}

	cfg, err := kb.GetConfig(ctx)
	if err != nil || cfg == nil {
		return err
	}

	if !cfg.AllowAutoEnrichment {
		return nil
	}

	log.Info("SpaceAutoEnrichment: Triggering sleep cycle for", "kb", kbName)
	err = kb.TriggerSleepCycle(ctx)
	if err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	// Always trigger Vectorize on autonomous agent spaces so internal knowledge syncs immediately.
	// Vectorize maintains its own batch transactions internally.
	return db.Vectorize(ctx, kb.Name(), s.generator, embedder, 100)
}
