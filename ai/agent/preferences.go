package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

func preferenceCategory(payload *ai.SessionPayload) string {
	if payload == nil {
		return ""
	}

	agentID := payload.AgentID
	if agentID == "" {
		agentID = ai.AgentIDOmni
	}

	if strings.TrimSpace(payload.UserID) == "" {
		return agentID
	}

	return fmt.Sprintf("%s/%s", agentID, payload.UserID)
}

func persistPreference(ctx context.Context, systemDB *database.Database, payload *ai.SessionPayload, pref memory.Preference) error {
	if systemDB == nil || payload == nil || strings.TrimSpace(payload.UserID) == "" || pref.Key == "" {
		return nil
	}

	tx, err := systemDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return fmt.Errorf("begin preference tx: %w", err)
	}
	defer tx.Rollback(ctx)

	store, err := systemDB.OpenModelStore(ctx, "preferences", tx)
	if err != nil {
		return fmt.Errorf("open preference store: %w", err)
	}

	if err := store.Save(ctx, preferenceCategory(payload), pref.Key, pref); err != nil {
		return fmt.Errorf("save preference: %w", err)
	}

	return tx.Commit(ctx)
}

func loadPreference(ctx context.Context, systemDB *database.Database, payload *ai.SessionPayload, key string) (memory.Preference, bool, error) {
	if systemDB == nil || payload == nil || strings.TrimSpace(payload.UserID) == "" || strings.TrimSpace(key) == "" {
		return memory.Preference{}, false, nil
	}

	tx, err := systemDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return memory.Preference{}, false, fmt.Errorf("begin preference read tx: %w", err)
	}
	defer tx.Rollback(ctx)

	store, err := systemDB.OpenModelStore(ctx, "preferences", tx)
	if err != nil {
		return memory.Preference{}, false, fmt.Errorf("open preference store: %w", err)
	}

	var pref memory.Preference
	if err := store.Load(ctx, preferenceCategory(payload), key, &pref); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return memory.Preference{}, false, nil
		}
		return memory.Preference{}, false, fmt.Errorf("load preference: %w", err)
	}

	return pref, true, nil
}
