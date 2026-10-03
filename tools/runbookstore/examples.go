package runbookstore

import "github.com/sharedcode/joltrin/v5/verify"

// DBMaintenanceWorkflow is the same scenario used throughout this repo's
// tests and docs: production database drop is forbidden without a validated
// backup, and rollback stays reachable even after the drop happens. Used by
// cmd/sop-mcp-server and cmd/sop-a2a-agent so both have something real to
// serve out of the box.
func DBMaintenanceWorkflow() (*verify.Workflow, error) {
	return verify.NewWorkflow(
		[]verify.Step{
			{ID: "take_backup", Establishes: []verify.State{"backup_taken"}},
			{ID: "validate_backup", Requires: []verify.State{"backup_taken"}, Establishes: []verify.State{"backup_validated"}},
			{ID: "drop_prod_db", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"prod_db_dropped"}},
			{ID: "restore_from_backup", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"rollback_complete"}},
			{ID: "restore_from_backup_post_drop", Requires: []verify.State{"prod_db_dropped"}, Establishes: []verify.State{"rollback_complete"}},
		},
		[]verify.SafetyRule{
			{Name: "no-drop-without-validated-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"},
		},
		[]verify.ReachabilityRule{
			{Name: "rollback-always-reachable", Target: "rollback_complete"},
		},
	)
}

// ClusterTopologyWorkflow models resource/topology mutations: draining a
// node or failing over a cluster is forbidden without both a passing health
// check and verified replica parity, and reinstating the node or cluster
// stays reachable after either mutation, or after a worker is terminated
// post-drain.
func ClusterTopologyWorkflow() (*verify.Workflow, error) {
	return verify.NewWorkflow(
		[]verify.Step{
			{ID: "check_cluster_health", Establishes: []verify.State{"health_check_passed"}},
			{ID: "verify_replica_parity", Requires: []verify.State{"health_check_passed"}, Establishes: []verify.State{"replica_parity_verified"}},
			{ID: "drain_node", Requires: []verify.State{"replica_parity_verified"}, Establishes: []verify.State{"node_drained"}},
			{ID: "failover_cluster", Requires: []verify.State{"replica_parity_verified"}, Establishes: []verify.State{"failover_committed"}},
			{ID: "terminate_worker", Requires: []verify.State{"node_drained"}, Establishes: []verify.State{"worker_terminated"}},
			{ID: "reinstate_node", Requires: []verify.State{"replica_parity_verified"}, Establishes: []verify.State{"topology_rollback_complete"}},
			{ID: "reinstate_after_termination", Requires: []verify.State{"worker_terminated"}, Establishes: []verify.State{"topology_rollback_complete"}},
			{ID: "reinstate_after_failover", Requires: []verify.State{"failover_committed"}, Establishes: []verify.State{"topology_rollback_complete"}},
		},
		[]verify.SafetyRule{
			{Name: "no-drain-without-replica-parity", Forbidden: "node_drained", Requires: "replica_parity_verified"},
			{Name: "no-failover-without-replica-parity", Forbidden: "failover_committed", Requires: "replica_parity_verified"},
		},
		[]verify.ReachabilityRule{
			{Name: "topology-rollback-always-reachable", Target: "topology_rollback_complete"},
		},
	)
}

// LedgerTransferWorkflow models financial/ledger-mutating actions: a
// transfer can never commit without a serializable transaction, a
// pre-mutation balance snapshot, and a verified zero-sum invariant proof
// over the resulting balances, and reversing the transfer stays reachable
// both before and after it commits.
func LedgerTransferWorkflow() (*verify.Workflow, error) {
	return verify.NewWorkflow(
		[]verify.Step{
			{ID: "begin_serializable_transaction", Establishes: []verify.State{"transaction_serialized"}},
			{ID: "snapshot_balances", Requires: []verify.State{"transaction_serialized"}, Establishes: []verify.State{"balances_snapshotted"}},
			{ID: "apply_debit_credit", Requires: []verify.State{"balances_snapshotted"}, Establishes: []verify.State{"balances_mutated"}},
			{ID: "verify_zero_sum_invariant", Requires: []verify.State{"balances_mutated"}, Establishes: []verify.State{"zero_sum_verified"}},
			{ID: "commit_transfer", Requires: []verify.State{"zero_sum_verified"}, Establishes: []verify.State{"transfer_committed"}},
			{ID: "reverse_transfer", Requires: []verify.State{"zero_sum_verified"}, Establishes: []verify.State{"ledger_rollback_complete"}},
			{ID: "reverse_transfer_post_commit", Requires: []verify.State{"transfer_committed"}, Establishes: []verify.State{"ledger_rollback_complete"}},
		},
		[]verify.SafetyRule{
			{Name: "no-commit-without-zero-sum-proof", Forbidden: "transfer_committed", Requires: "zero_sum_verified"},
			{Name: "no-mutation-without-serialized-transaction", Forbidden: "balances_mutated", Requires: "transaction_serialized"},
		},
		[]verify.ReachabilityRule{
			{Name: "ledger-rollback-always-reachable", Target: "ledger_rollback_complete"},
		},
	)
}
