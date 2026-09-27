package fs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sharedcode/joltrin/v5"
	"github.com/sharedcode/joltrin/v5/common/mocks"
	"github.com/sharedcode/joltrin/v5/encoding"
)

func TestPriorityLog_ProcessNewer(t *testing.T) {
	ctx := context.Background()

	// Setup replication tracker with a temp directory
	tempDir := t.TempDir()
	rt, err := NewReplicationTracker(ctx, []string{tempDir}, false, mocks.NewMockClient())
	if err != nil {
		t.Fatalf("NewReplicationTracker failed: %v", err)
	}

	// Create TransactionLog and get PriorityLog
	tl := NewTransactionLog(mocks.NewMockClient(), rt)
	pl := tl.PriorityLog()

	// Define log folder path (internal constant logFolder is "translogs")
	logDir := filepath.Join(tempDir, "translogs")
	// Ensure directory exists (pl.Add will create it, but we need it for filepath.Join)

	// Helper to create a priority log file
	createLog := func(tid sop.UUID, age time.Duration) {
		payload := []sop.RegistryPayload[sop.Handle]{
			{RegistryTable: "table", IDs: []sop.Handle{{LogicalID: sop.NewUUID()}}},
		}
		ba, _ := encoding.DefaultMarshaler.Marshal(payload)

		// Use pl.Add to create the file
		if err := pl.Add(ctx, tid, ba); err != nil {
			t.Fatalf("pl.Add failed: %v", err)
		}

		// Modify the file timestamp
		// Note: priorityLogFileExtension is ".plg"
		filename := filepath.Join(logDir, tid.String()+".plg")
		newTime := time.Now().Add(-age)
		if err := os.Chtimes(filename, newTime, newTime); err != nil {
			t.Fatalf("Failed to change file time for %s: %v", filename, err)
		}
	}

	// Create an old file (older than 5 mins)
	oldTid := sop.NewUUID()
	createLog(oldTid, 10*time.Minute)

	// Create a new file (newer than 5 mins)
	newTid := sop.NewUUID()
	createLog(newTid, 1*time.Minute)

	// Track processed TIDs
	processed := make(map[sop.UUID]bool)
	processor := func(tid sop.UUID, payload []sop.RegistryPayload[sop.Handle]) error {
		processed[tid] = true
		return nil
	}

	// Run ProcessNewer
	if err := pl.ProcessNewer(ctx, processor); err != nil {
		t.Fatalf("ProcessNewer failed: %v", err)
	}

	// Verify results
	if processed[oldTid] {
		t.Errorf("Old log %v was processed, but should have been ignored", oldTid)
	}
	if !processed[newTid] {
		t.Errorf("New log %v was NOT processed, but should have been", newTid)
	}
}
