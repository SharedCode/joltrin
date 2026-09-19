package swarm

import (
	"context"
	"os"
	"testing"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/infs"
)

func newTestStore(t *testing.T) (*Store, context.Context) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "swarm-store-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	ctx := context.Background()
	trans, err := infs.NewTransaction(ctx, sop.TransactionOptions{
		Mode:          sop.ForWriting,
		StoresFolders: []string{tmpDir},
		CacheType:     sop.InMemory,
	})
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	if err := trans.Begin(ctx); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	store, err := NewStore(ctx, trans)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store, ctx
}

// GetResults positions the results cursor with Find(startKey, true) but
// never checks the returned found flag - staticcheck flags it as a dead
// store (SA4006). The loop right after relies purely on a prefix check
// to decide whether the current item belongs to the requested job, so
// the found value being unused isn't itself a bug as long as that
// prefix check is a safe net on its own. These three cases verify it
// actually is, on the real storage engine, not a mock.

func TestGetResults_EmptyStoreReturnsNoResults(t *testing.T) {
	store, ctx := newTestStore(t)

	results, err := store.GetResults(ctx, "does-not-exist")
	if err != nil {
		t.Fatalf("GetResults on empty store: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results on an empty store, got %d: %+v", len(results), results)
	}
}

func TestGetResults_QueryPastEndOfTreeReturnsNoResults(t *testing.T) {
	store, ctx := newTestStore(t)

	if err := store.SubmitResult(ctx, JobResult{JobID: "job-aaa", NodeID: "n1"}); err != nil {
		t.Fatalf("SubmitResult: %v", err)
	}

	// "job-zzz" sorts after every key in the tree, so Find(startKey, true)
	// cannot land on or after it - this is the case most likely to expose
	// a stale-cursor bug if the found flag actually mattered.
	results, err := store.GetResults(ctx, "job-zzz")
	if err != nil {
		t.Fatalf("GetResults past end of tree: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results querying a jobID past the end of the tree, got %d: %+v", len(results), results)
	}
}

func TestGetResults_ReturnsOnlyMatchingJobResults(t *testing.T) {
	store, ctx := newTestStore(t)

	if err := store.SubmitResult(ctx, JobResult{JobID: "job-a", NodeID: "n1", Output: "a1"}); err != nil {
		t.Fatalf("SubmitResult: %v", err)
	}
	if err := store.SubmitResult(ctx, JobResult{JobID: "job-a", NodeID: "n2", Output: "a2"}); err != nil {
		t.Fatalf("SubmitResult: %v", err)
	}
	if err := store.SubmitResult(ctx, JobResult{JobID: "job-b", NodeID: "n1", Output: "b1"}); err != nil {
		t.Fatalf("SubmitResult: %v", err)
	}

	results, err := store.GetResults(ctx, "job-a")
	if err != nil {
		t.Fatalf("GetResults: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results for job-a, got %d: %+v", len(results), results)
	}
	for _, r := range results {
		if r.JobID != "job-a" {
			t.Errorf("GetResults(\"job-a\") returned a result for a different job: %+v", r)
		}
	}
}
