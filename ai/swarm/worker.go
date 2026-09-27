package swarm

import (
	"context"
	"log/slog"
	"time"

	"github.com/sharedcode/joltrin/v5"
)

// Worker represents a node that listens for and executes jobs.
type Worker struct {
	ID               string
	PollInterval     time.Duration
	StopChan         chan struct{}
	tf               TransactionFactory
	supportedScripts map[string]func(context.Context, map[string]string) (string, error)
}

// NewWorker creates a new swarm worker.
func NewWorker(id string, tf TransactionFactory) *Worker {
	return &Worker{
		ID:               id,
		PollInterval:     2 * time.Second, // Default poll interval
		StopChan:         make(chan struct{}),
		tf:               tf,
		supportedScripts: make(map[string]func(context.Context, map[string]string) (string, error)),
	}
}

// RegisterScript registers a handler for a specific script name.
func (w *Worker) RegisterScript(name string, handler func(context.Context, map[string]string) (string, error)) {
	w.supportedScripts[name] = handler
}

// Start begins the worker loop.
func (w *Worker) Start(ctx context.Context) {
	slog.Info("Swarm Worker started", "worker_id", w.ID)
	ticker := time.NewTicker(w.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.StopChan:
			slog.Info("Swarm Worker stopping", "worker_id", w.ID)
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := w.processNextJob(ctx); err != nil {
				slog.Error("Error processing job", "error", err)
			}
		}
	}
}

// processNextJob checks for a pending job and executes it.
func (w *Worker) processNextJob(ctx context.Context) error {
	// 1. Start Transaction to find a job
	t, err := w.tf(sop.ForWriting)
	if err != nil {
		return err
	}
	defer t.Rollback(ctx)

	store, err := NewStore(ctx, t)
	if err != nil {
		return err
	}

	// 2. Scan for pending jobs
	// Ideally, we use a separate "Pending" B-Tree or index.
	// For this MVP, we iterate the jobs store.
	// Note: This is inefficient for large history; production needs a dedicated queue.
	iter, err := store.jobs.First(ctx)
	if err != nil {
		return err
	}
	if !iter {
		return nil // No jobs
	}

	for {
		_, err := store.jobs.GetCurrentItem(ctx)
		if err != nil {
			return err
		}

		// Check if job is pending and matches our filter
		// Note: We need to deserialize the Value to check status/filter.
		// Since 'Job' struct doesn't have Status (it's in Result?), wait.
		// The Job struct in types.go doesn't have Status! We need to add it.

		// ... logic to claim job ...

		if hasNext, _ := store.jobs.Next(ctx); !hasNext {
			break
		}
	}

	return nil
}
