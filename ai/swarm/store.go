package swarm

import (
	"context"
	"fmt"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/btree"
	"github.com/sharedcode/joltrin/infs"
)

// Store manages the persistence of Jobs and Results in the swarm.
type Store struct {
	jobs    btree.BtreeInterface[string, Job]       // Key: JobID
	results btree.BtreeInterface[string, JobResult] // Key: JobID|NodeID
}

// NewStore creates or opens the swarm stores.
// It follows the "Thin Wrapper" pattern, requiring an external transaction.
func NewStore(ctx context.Context, t sop.Transaction) (*Store, error) {
	jobs, err := infs.NewBtree[string, Job](ctx, sop.ConfigureStore(
		"sys_jobs",
		true,
		1000, // Optimized for high throughput
		"Swarm Job Queue",
		sop.SmallData,
		"",
	), t, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open jobs store: %w", err)
	}

	results, err := infs.NewBtree[string, JobResult](ctx, sop.ConfigureStore(
		"sys_results",
		true,
		1000, // Optimized for high throughput
		"Swarm Job Results",
		sop.SmallData,
		"",
	), t, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to open results store: %w", err)
	}

	return &Store{
		jobs:    jobs,
		results: results,
	}, nil
}

// EnqueueJob adds a new job to the swarm.
func (s *Store) EnqueueJob(ctx context.Context, job Job) error {
	if job.ID == "" {
		return fmt.Errorf("job ID is required")
	}
	found, err := s.jobs.Find(ctx, job.ID, false)
	if err != nil {
		return err
	}
	if found {
		return fmt.Errorf("job with ID %s already exists", job.ID)
	}
	_, err = s.jobs.Add(ctx, job.ID, job)
	return err
}

// GetJob retrieves a job by ID.
func (s *Store) GetJob(ctx context.Context, jobID string) (*Job, error) {
	found, err := s.jobs.Find(ctx, jobID, false)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	val, err := s.jobs.GetCurrentValue(ctx)
	if err != nil {
		return nil, err
	}
	return &val, nil
}

// SubmitResult saves the result of a job execution.
func (s *Store) SubmitResult(ctx context.Context, result JobResult) error {
	key := fmt.Sprintf("%s|%s", result.JobID, result.NodeID)
	_, err := s.results.Add(ctx, key, result)
	return err
}

// GetResults retrieves all results for a given job.
func (s *Store) GetResults(ctx context.Context, jobID string) ([]JobResult, error) {
	// Range scan: "jobID|" to "jobID|~"
	startKey := jobID + "|"

	var results []JobResult

	// The found flag isn't checked directly: whether or not Find landed
	// exactly on startKey, the loop below verifies every candidate item
	// against the prefix itself and breaks the moment it doesn't match,
	// which is a correct and sufficient guard on its own (verified in
	// store_test.go against the real storage engine, including the case
	// of a jobID that sorts past every key already in the tree).
	if _, err := s.results.Find(ctx, startKey, true); err != nil {
		return nil, err
	}

	// Iterate
	for {
		item, err := s.results.GetCurrentItem(ctx)
		if err != nil {
			return nil, err
		}

		// Check prefix
		if len(item.Key) < len(startKey) || item.Key[:len(startKey)] != startKey {
			break
		}

		results = append(results, *item.Value)

		hasNext, err := s.results.Next(ctx)
		if err != nil {
			return nil, err
		}
		if !hasNext {
			break
		}
	}

	return results, nil
}
