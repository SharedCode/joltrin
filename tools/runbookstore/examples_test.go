package runbookstore

import (
	"strings"
	"testing"

	"github.com/sharedcode/joltrin/v5/verify"
)

func Test_ClusterTopologyWorkflow_ReachabilityHolds(t *testing.T) {
	wf, err := ClusterTopologyWorkflow()
	if err != nil {
		t.Fatalf("ClusterTopologyWorkflow: %v", err)
	}
	if err := wf.VerifyReachability(); err != nil {
		t.Fatalf("VerifyReachability: %v", err)
	}
}

func Test_ClusterTopologyWorkflow_BlocksDrainWithoutReplicaParity(t *testing.T) {
	wf, err := ClusterTopologyWorkflow()
	if err != nil {
		t.Fatalf("ClusterTopologyWorkflow: %v", err)
	}
	trace := verify.NewTrace()

	if err := wf.CheckSafety(trace, "drain_node"); err == nil {
		t.Fatal("expected drain_node to be blocked with no prior trace")
	} else if !strings.Contains(err.Error(), "requires state") {
		t.Fatalf("expected a precondition error, got: %v", err)
	}
}

func Test_ClusterTopologyWorkflow_AllowsDrainAfterReplicaParityVerified(t *testing.T) {
	wf, err := ClusterTopologyWorkflow()
	if err != nil {
		t.Fatalf("ClusterTopologyWorkflow: %v", err)
	}
	trace := verify.NewTrace()

	for _, step := range []verify.StepID{"check_cluster_health", "verify_replica_parity"} {
		if err := wf.CheckSafety(trace, step); err != nil {
			t.Fatalf("CheckSafety(%q): unexpected block: %v", step, err)
		}
		if err := wf.Commit(trace, step); err != nil {
			t.Fatalf("Commit(%q): %v", step, err)
		}
	}

	if err := wf.CheckSafety(trace, "drain_node"); err != nil {
		t.Fatalf("expected drain_node to be allowed after replica parity verified, got: %v", err)
	}
}

func Test_LedgerTransferWorkflow_ReachabilityHolds(t *testing.T) {
	wf, err := LedgerTransferWorkflow()
	if err != nil {
		t.Fatalf("LedgerTransferWorkflow: %v", err)
	}
	if err := wf.VerifyReachability(); err != nil {
		t.Fatalf("VerifyReachability: %v", err)
	}
}

func Test_LedgerTransferWorkflow_BlocksCommitWithoutZeroSumProof(t *testing.T) {
	wf, err := LedgerTransferWorkflow()
	if err != nil {
		t.Fatalf("LedgerTransferWorkflow: %v", err)
	}
	trace := verify.NewTrace()

	if err := wf.CheckSafety(trace, "commit_transfer"); err == nil {
		t.Fatal("expected commit_transfer to be blocked with no prior trace")
	} else if !strings.Contains(err.Error(), "requires state") {
		t.Fatalf("expected a precondition error, got: %v", err)
	}
}

func Test_LedgerTransferWorkflow_AllowsCommitAfterZeroSumVerified(t *testing.T) {
	wf, err := LedgerTransferWorkflow()
	if err != nil {
		t.Fatalf("LedgerTransferWorkflow: %v", err)
	}
	trace := verify.NewTrace()

	steps := []verify.StepID{
		"begin_serializable_transaction",
		"snapshot_balances",
		"apply_debit_credit",
		"verify_zero_sum_invariant",
	}
	for _, step := range steps {
		if err := wf.CheckSafety(trace, step); err != nil {
			t.Fatalf("CheckSafety(%q): unexpected block: %v", step, err)
		}
		if err := wf.Commit(trace, step); err != nil {
			t.Fatalf("Commit(%q): %v", step, err)
		}
	}

	if err := wf.CheckSafety(trace, "commit_transfer"); err != nil {
		t.Fatalf("expected commit_transfer to be allowed after zero-sum invariant verified, got: %v", err)
	}
}
