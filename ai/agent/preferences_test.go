package agent

import (
	"context"
	"testing"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/v5"
)

func TestPersistAndLoadPreference_RoundTripsVerbosePreference(t *testing.T) {
	ctx := context.Background()
	db := database.NewDatabase(sop.DatabaseOptions{Type: sop.Standalone, StoresFolders: []string{t.TempDir()}})
	payload := &ai.SessionPayload{UserID: "user-42", AgentID: ai.AgentIDOmni}

	pref := memory.NewBoolPreference(memory.PreferenceKeyVerbose, true)
	pref.Source = "runtime_toggle"

	if err := persistPreference(ctx, db, payload, pref); err != nil {
		t.Fatalf("persistPreference() error = %v", err)
	}

	loaded, ok, err := loadPreference(ctx, db, payload, memory.PreferenceKeyVerbose)
	if err != nil {
		t.Fatalf("loadPreference() error = %v", err)
	}
	if !ok {
		t.Fatal("expected preference to be found")
	}

	value, found := loaded.Bool()
	if !found || !value {
		t.Fatalf("expected stored verbose preference to round-trip true, got value=%v found=%v", value, found)
	}
	if loaded.Source != "runtime_toggle" {
		t.Fatalf("expected source to round-trip, got %q", loaded.Source)
	}
}
