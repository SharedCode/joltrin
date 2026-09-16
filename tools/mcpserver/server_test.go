package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sharedcode/joltrin/ai/verify"
	"github.com/sharedcode/joltrin/tools/runbookstore"
)

// dbMaintenanceWorkflow mirrors ai/verify's own test fixture: production
// database drop is forbidden without a validated backup, and rollback
// remains reachable even after the drop.
func dbMaintenanceWorkflow(t *testing.T) *verify.Workflow {
	t.Helper()
	steps := []verify.Step{
		{ID: "take_backup", Establishes: []verify.State{"backup_taken"}},
		{ID: "validate_backup", Requires: []verify.State{"backup_taken"}, Establishes: []verify.State{"backup_validated"}},
		{ID: "drop_prod_db", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"prod_db_dropped"}},
		{ID: "restore_from_backup", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"rollback_complete"}},
		{ID: "restore_from_backup_post_drop", Requires: []verify.State{"prod_db_dropped"}, Establishes: []verify.State{"rollback_complete"}},
	}
	safety := []verify.SafetyRule{
		{Name: "no-drop-without-validated-backup", Forbidden: "prod_db_dropped", Requires: "backup_validated"},
	}
	reachability := []verify.ReachabilityRule{
		{Name: "rollback-always-reachable", Target: "rollback_complete"},
	}
	wf, err := verify.NewWorkflow(steps, safety, reachability)
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}
	return wf
}

// newTestClient stands up a real MCP server with the mcp-go library's actual
// protocol implementation and connects an in-process client to it, so these
// tests exercise the real initialize/tools-call wire contract, not just Go
// function calls to the handlers directly.
func newTestClient(t *testing.T) *client.Client {
	t.Helper()
	store := runbookstore.New()
	if err := store.RegisterWorkflow("db-maintenance", dbMaintenanceWorkflow(t)); err != nil {
		t.Fatalf("RegisterWorkflow: %v", err)
	}

	srv := New(store)
	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "sop-test-client", Version: "0.0.1"},
		},
	}); err != nil {
		t.Fatalf("client.Initialize: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func callTool(t *testing.T, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := c.CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: name, Arguments: args},
	})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	return res
}

func resultText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// structuredAs decodes a real client-received result's structured content
// into T. The in-process client round-trips through JSON, so
// res.StructuredContent arrives as a generic map, not the original Go
// struct, re-marshal/unmarshal is how a real MCP client would decode it too.
func structuredAs[T any](t *testing.T, res *mcp.CallToolResult) T {
	t.Helper()
	raw := res.RawStructuredContent
	if raw == nil {
		var err error
		raw, err = json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal StructuredContent: %v", err)
		}
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal structured content into %T: %v (raw: %s)", out, err, raw)
	}
	return out
}

func Test_MCP_ReadSOP_UnknownWorkflow(t *testing.T) {
	c := newTestClient(t)
	res := callTool(t, c, "read_sop", map[string]any{"workflow": "does-not-exist"})
	if !res.IsError {
		t.Fatal("expected an error result for an unknown workflow")
	}
}

// Test_MCP_ExecuteStep_BlockedWithoutValidatedBackup confirms a barrier
// block is a normal (non-error) structured result with an actionable
// Blocked reason, not a bare error string: the request was well-formed and
// could still succeed once its precondition is met, so IsError must stay
// false, matching the same distinction tools/a2aagent already draws
// between TaskStateFailed and TaskStateInputRequired.
func Test_MCP_ExecuteStep_BlockedWithoutValidatedBackup(t *testing.T) {
	c := newTestClient(t)

	res := callTool(t, c, "execute_step", map[string]any{
		"workflow": "db-maintenance",
		"trace_id": "incident-1",
		"step":     "drop_prod_db",
	})
	if res.IsError {
		t.Fatalf("blocked-but-recoverable should not be IsError, got: %s", resultText(res))
	}
	got := structuredAs[ExecuteStepResult](t, res)
	if got.Executed {
		t.Fatal("expected executed=false")
	}
	if got.Blocked == nil {
		t.Fatal("expected a populated Blocked reason")
	}
	if got.Blocked.MissingState != "backup_validated" {
		t.Fatalf("expected missing_state %q, got %q", "backup_validated", got.Blocked.MissingState)
	}
	if len(got.Blocked.EstablishedBy) == 0 {
		t.Fatal("expected established_by_steps to name at least one step (validate_backup)")
	}
}

// Test_MCP_ExecuteStep_UnknownWorkflowIsErrorWithInventory confirms a
// malformed request (a workflow that was never registered) is reported as
// an error carrying the server's actual inventory, distinct from a barrier
// block: no precondition could ever make this one succeed.
func Test_MCP_ExecuteStep_UnknownWorkflowIsErrorWithInventory(t *testing.T) {
	c := newTestClient(t)

	res := callTool(t, c, "execute_step", map[string]any{
		"workflow": "does-not-exist",
		"trace_id": "incident-1",
		"step":     "drop_prod_db",
	})
	if !res.IsError {
		t.Fatal("expected an error result for an unknown workflow")
	}
	got := structuredAs[UnknownWorkflowResult](t, res)
	if len(got.Available) != 1 || got.Available[0] != "db-maintenance" {
		t.Fatalf("expected available_workflows to list db-maintenance, got %v", got.Available)
	}
}

// Test_MCP_ExecuteStep_UnknownStepIsErrorWithInventory confirms the same
// for a step ID that does not exist on an otherwise-known workflow: also
// malformed, not a barrier block, and reported with the workflow's real
// step inventory rather than being folded into safe/executed=false.
func Test_MCP_ExecuteStep_UnknownStepIsErrorWithInventory(t *testing.T) {
	c := newTestClient(t)

	res := callTool(t, c, "execute_step", map[string]any{
		"workflow": "db-maintenance",
		"trace_id": "incident-1",
		"step":     "nonexistent_step",
	})
	if !res.IsError {
		t.Fatal("expected an error result for an unknown step")
	}
	got := structuredAs[UnknownStepResult](t, res)
	if len(got.Steps) == 0 {
		t.Fatal("expected available_steps to list this workflow's real steps")
	}
}

// Test_MCP_ExecuteStep_IdempotencyKey_RetryDoesNotDuplicate is the real
// end-to-end proof (over the actual MCP wire protocol, not a direct Go
// call) that a client retrying execute_step with the same idempotency_key,
// simulating a lost or timed-out response, gets back the original result
// and marked replayed, instead of the step executing a second time.
func Test_MCP_ExecuteStep_IdempotencyKey_RetryDoesNotDuplicate(t *testing.T) {
	c := newTestClient(t)
	args := map[string]any{
		"workflow":        "db-maintenance",
		"trace_id":        "incident-retry",
		"step":            "take_backup",
		"idempotency_key": "req-1",
	}

	first := callTool(t, c, "execute_step", args)
	if first.IsError {
		t.Fatalf("first call unexpectedly errored: %s", resultText(first))
	}
	firstResult := structuredAs[ExecuteStepResult](t, first)
	if !firstResult.Executed || firstResult.Replayed {
		t.Fatalf("expected a fresh execution, got %+v", firstResult)
	}

	retry := callTool(t, c, "execute_step", args)
	if retry.IsError {
		t.Fatalf("retry unexpectedly errored: %s", resultText(retry))
	}
	retryResult := structuredAs[ExecuteStepResult](t, retry)
	if !retryResult.Executed || !retryResult.Replayed {
		t.Fatalf("expected the retry to report executed=true replayed=true, got %+v", retryResult)
	}
	if len(retryResult.Trace) != 1 {
		t.Fatalf("expected the retry to leave the trace at one entry, got %v", retryResult.Trace)
	}
}

// Test_MCP_ExecuteStep_NoIdempotencyKey_RetryDuplicates is the explicit
// backward-compatibility check: a caller that omits idempotency_key must
// see the pre-existing behavior (each call executes independently), not a
// silently different default once this feature exists.
func Test_MCP_ExecuteStep_NoIdempotencyKey_RetryDuplicates(t *testing.T) {
	c := newTestClient(t)
	args := map[string]any{
		"workflow": "db-maintenance",
		"trace_id": "incident-no-key",
		"step":     "take_backup",
	}

	callTool(t, c, "execute_step", args)
	res := callTool(t, c, "execute_step", args)
	result := structuredAs[ExecuteStepResult](t, res)
	if result.Replayed {
		t.Fatal("a call with no idempotency_key must never report replayed=true")
	}
	if len(result.Trace) != 2 {
		t.Fatalf("expected two independent executions without a key, got %v", result.Trace)
	}
}

// Test_MCP_ExecuteStep_FullSequence is the real end-to-end path: an MCP
// client calls validate_step then execute_step for each step in order,
// exactly how an agent would drive this server, and confirms the barrier
// certificate allows the sequence once (and only once) preconditions are
// actually met.
func Test_MCP_ExecuteStep_FullSequence(t *testing.T) {
	c := newTestClient(t)
	const traceID = "incident-2"

	for _, step := range []string{"take_backup", "validate_backup"} {
		validated := callTool(t, c, "validate_step", map[string]any{
			"workflow": "db-maintenance",
			"trace_id": traceID,
			"step":     step,
		})
		if validated.IsError {
			t.Fatalf("validate_step(%s) unexpectedly errored: %s", step, resultText(validated))
		}

		executed := callTool(t, c, "execute_step", map[string]any{
			"workflow": "db-maintenance",
			"trace_id": traceID,
			"step":     step,
		})
		if executed.IsError {
			t.Fatalf("execute_step(%s) unexpectedly blocked: %s", step, resultText(executed))
		}
	}

	// Now the drop should be allowed, since backup_validated holds in this trace.
	res := callTool(t, c, "execute_step", map[string]any{
		"workflow": "db-maintenance",
		"trace_id": traceID,
		"step":     "drop_prod_db",
	})
	if res.IsError {
		t.Fatalf("expected drop_prod_db to be allowed after take_backup + validate_backup, got: %s", resultText(res))
	}
}

// Test_MCP_ExecuteStep_TraceIsolation confirms two different trace_ids are
// independent: progress in one incident's trace must not leak into another,
// an agent working incident B should not be able to drop prod just because
// incident A already validated a backup.
func Test_MCP_ExecuteStep_TraceIsolation(t *testing.T) {
	c := newTestClient(t)

	for _, step := range []string{"take_backup", "validate_backup"} {
		if res := callTool(t, c, "execute_step", map[string]any{
			"workflow": "db-maintenance",
			"trace_id": "incident-a",
			"step":     step,
		}); res.IsError {
			t.Fatalf("setup step %s failed: %s", step, resultText(res))
		}
	}

	// A completely separate trace_id should not inherit incident-a's progress.
	res := callTool(t, c, "execute_step", map[string]any{
		"workflow": "db-maintenance",
		"trace_id": "incident-b",
		"step":     "drop_prod_db",
	})
	if res.IsError {
		t.Fatalf("blocked-but-recoverable should not be IsError, got: %s", resultText(res))
	}
	got := structuredAs[ExecuteStepResult](t, res)
	if got.Executed {
		t.Fatal("expected incident-b's drop_prod_db to be blocked: it has no validated backup of its own")
	}
}

func Test_RegisterWorkflow_RejectsUnreachableRollback(t *testing.T) {
	steps := []verify.Step{
		{ID: "take_backup", Establishes: []verify.State{"backup_taken"}},
		{ID: "validate_backup", Requires: []verify.State{"backup_taken"}, Establishes: []verify.State{"backup_validated"}},
		{ID: "drop_prod_db", Requires: []verify.State{"backup_validated"}, Establishes: []verify.State{"prod_db_dropped"}},
		// No restore step reachable from prod_db_dropped: this workflow is
		// unsafe to register.
	}
	wf, err := verify.NewWorkflow(steps, nil, []verify.ReachabilityRule{
		{Name: "rollback-always-reachable", Target: "backup_validated"},
	})
	if err != nil {
		t.Fatalf("NewWorkflow: %v", err)
	}

	store := runbookstore.New()
	if err := store.RegisterWorkflow("broken", wf); err == nil {
		t.Fatal("expected RegisterWorkflow to refuse a workflow with an unreachable rollback state")
	}
}
