package agent

import (
	"context"
	"time"

	log "log/slog"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

// StartSleepCycle launches background consolidators over the isolated Avatar STM
// migrating Short-Term episodic memories into Semantic Long-Term memory.
func (a *CopilotAgent) StartSleepCycle(ctx context.Context, hourlyInterval int, idleTimeoutMinutes int, nowFn func() time.Time) {
	if nowFn == nil {
		nowFn = time.Now
	}

	// Wrap the actual consolidating logic to avoid duplication
	runSleepCycle := func() {
		if a.systemDB == nil {
			return
		}

		tx, err := a.systemDB.BeginTransaction(ctx, sop.ForWriting)
		if err != nil {
			return
		}
		if a.Memory == nil {
			tx.Rollback(ctx)
			return
		}
		a.Memory.BindSession(ctx)
		if _, err := a.Memory.OpenShortTermMemory(ctx, a.systemDB, tx); err != nil {
			tx.Rollback(ctx)
			return
		}
		stmStore := a.Memory.STMStore()
		stm := stmStore.Primary()

		if removed, err := stmStore.PruneExpired(ctx, time.Now()); err != nil {
			log.Warn("CopilotAgent: Failed pruning stale STM episodes before sleep cycle", "agent_id", a.Memory.AgentID, "error", err)
			tx.Rollback(ctx)
			return
		} else if removed > 0 {
			log.Debug("CopilotAgent: Pruned stale STM episodes before sleep cycle", "agent_id", a.Memory.AgentID, "count", removed)
		}

		var embedder ai.Embeddings
		if a.service != nil && a.service.Domain() != nil && a.service.Domain().Embedder() != nil {
			embedder = a.service.Domain().Embedder()
		}

		ltm, err := a.systemDB.OpenKnowledgeBase(ctx, a.Memory.LongTermMemoryName(), tx, a.brain, embedder, false)
		if err != nil {
			tx.Rollback(ctx)
			return
		}

		var thoughts []memory.Thought[map[string]any]
		var itemIDs []string

		ok, _ := stm.First(ctx)
		for ok {
			key := stm.GetCurrentKey()
			if key.Key != "root_anchor" {
				val, _ := stm.GetCurrentValue(ctx)
				if payload, valid := val.(map[string]any); valid {
					thoughtText := ""
					if txt, has := payload["thought"].(string); has {
						thoughtText = txt
					}

					thoughts = append(thoughts, memory.Thought[map[string]any]{
						Summaries: []string{thoughtText},
						Data:      payload,
					})
					itemIDs = append(itemIDs, key.Key)
				}
			}
			ok, _ = stm.Next(ctx)
		}

		if len(thoughts) == 0 {
			tx.Rollback(ctx)
			return
		}

		err = ltm.IngestThoughts(ctx, thoughts, a.Memory.AgentID)
		if err != nil {
			tx.Rollback(ctx)
			return
		}

		err = ltm.TriggerSleepCycle(ctx)
		if err != nil {
			log.Warn("CopilotAgent: LTM TriggerSleepCycle encountered error", "agent_id", a.Memory.AgentID, "error", err)
		}

		for i, id := range itemIDs {
			var payload map[string]any
			if i < len(thoughts) {
				payload = thoughts[i].Data
			}
			err = stmStore.RemoveEpisode(ctx, id, payload)
			if err != nil {
				log.Warn("CopilotAgent: Failed to remove scrubbed item from STM", "id", id, "error", err)
			}
		}

		tx.Commit(ctx)
		a.Memory.CloseShortTermMemory()
		log.Debug("CopilotAgent: Sleep Cycle completed successfully.", "agent_id", a.Memory.AgentID)
	}

	// 1. "Quick Nap" Sensor (Idle Ticker)
	if idleTimeoutMinutes > 0 {
		go func() {
			log.Info("CopilotAgent: Initiating Quick Nap Idle Sensor", "agent_id", a.Memory.AgentID, "timeout_minutes", idleTimeoutMinutes)
			ticker := time.NewTicker(1 * time.Minute)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					lastLog := a.Memory.LastEpisodeTS.Load()
					if shouldTriggerQuickNap(lastLog, idleTimeoutMinutes, nowFn()) {
						// Perform a safe check: are there items in STM unvectorized?
						tx, err := a.systemDB.BeginTransaction(ctx, sop.ForReading)
						if err == nil {
							a.Memory.BindSession(ctx)
							stm, stmErr := a.systemDB.OpenBtree(ctx, a.Memory.ShortTermMemoryName(), tx)
							if stmErr == nil {
								// Root anchor check
								count := stm.Count()
								if count > 1 {
									log.Info("CopilotAgent: Avatar is IDLE. Triggering 'Quick Nap' memory consolidation.", "agent_id", a.Memory.AgentID)
									runSleepCycle()
									a.Memory.LastEpisodeTS.Store(0) // Reset after sweeping
								}
							}
							tx.Rollback(ctx)
						}
					}
				}
			}
		}()
	}

	// 2. "Deep Sleep" Scheduler (Hourly/Midnight Ticker)
	go func() {
		log.Info("CopilotAgent: Initiating Deep Sleep Scheduler", "agent_id", a.Memory.AgentID, "hourly_interval", hourlyInterval)

		// 1. Align to the precise top of the next hour
		now := nowFn()
		nextHour := now.Truncate(time.Hour).Add(time.Hour)
		time.Sleep(nextHour.Sub(now))

		// 2. Ticking perfectly on the hour
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				if shouldTriggerDeepSleep(t.Hour(), hourlyInterval) {
					log.Info("CopilotAgent: Running Scheduled Deep Sleep Cycle", "agent_id", a.Memory.AgentID, "hour", t.Hour())
					runSleepCycle()
					a.Memory.LastEpisodeTS.Store(0) // Reset idle sensor
				}
			}
		}
	}()
}
func shouldTriggerQuickNap(lastLogTS int64, idleTimeoutMinutes int, now time.Time) bool {
	if lastLogTS == 0 || idleTimeoutMinutes <= 0 {
		return false
	}
	idleTime := now.Sub(time.UnixMilli(lastLogTS))
	return idleTime >= time.Duration(idleTimeoutMinutes)*time.Minute
}

func shouldTriggerDeepSleep(hour int, hourlyInterval int) bool {
	if hour == 0 {
		return true // Always sweep at midnight
	}
	if hourlyInterval > 0 && hour%hourlyInterval == 0 {
		return true
	}
	return false
}
