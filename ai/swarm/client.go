package swarm

import (
	"context"
	"fmt"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

// Distribute fires a script into the swarm.
// It uses the provided transaction to enqueue the job.
func Distribute(ctx context.Context, t sop.Transaction, scriptName string, params map[string]string, targetFilter map[string]string) (string, error) {
	store, err := NewStore(ctx, t)
	if err != nil {
		return "", err
	}

	jobID := fmt.Sprintf("job-%d", time.Now().UnixNano())
	job := Job{
		ID:           jobID,
		ScriptName:   scriptName,
		Params:       params,
		TargetFilter: targetFilter,
		CreatedAt:    time.Now(),
		CreatedBy:    "user", // TODO: Get from context
	}

	if err := store.EnqueueJob(ctx, job); err != nil {
		return "", err
	}

	return jobID, nil
}

// TransactionFactory is a function that creates a new transaction.
type TransactionFactory func(mode sop.TransactionMode) (sop.Transaction, error)

// Await polls the swarm for results until completion or timeout.
func Await(ctx context.Context, tf TransactionFactory, jobID string, expectedResults int, timeout time.Duration) ([]JobResult, error) {
	start := time.Now()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			if time.Since(start) > timeout {
				return nil, fmt.Errorf("timeout waiting for job %s", jobID)
			}

			// Start a read-only transaction for polling
			t, err := tf(sop.ForReading)
			if err != nil {
				return nil, err
			}

			results, err := checkResults(ctx, t, jobID)
			// Always commit/rollback to close resources
			t.Commit(ctx)

			if err != nil {
				// Log error but continue polling? Or fail?
				// For now, fail fast on DB error
				return nil, err
			}

			if len(results) >= expectedResults {
				return results, nil
			}
		}
	}
}

func checkResults(ctx context.Context, t sop.Transaction, jobID string) ([]JobResult, error) {
	store, err := NewStore(ctx, t)
	if err != nil {
		return nil, err
	}
	return store.GetResults(ctx, jobID)
}
