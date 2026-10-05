package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
	"github.com/sharedcode/joltrin/v5/verify"
)

// dbMaintenanceWorkflow mirrors verify's own test fixture: production
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

// Test_MCP_ExecuteStep_BlockedWireFormat pins the JSON a client receives for a
// blocked step. Agents read these field names directly, so renaming one is a
// breaking change even though the Go struct would still compile.
func Test_MCP_ExecuteStep_BlockedWireFormat(t *testing.T) {
	c := newTestClient(t)

	res := callTool(t, c, "execute_step", map[string]any{
		"workflow": "db-maintenance",
		"trace_id": "wire-1",
		"step":     "drop_prod_db",
	})
	raw := res.RawStructuredContent
	if raw == nil {
		var err error
		if raw, err = json.Marshal(res.StructuredContent); err != nil {
			t.Fatalf("marshal StructuredContent: %v", err)
		}
	}
	var top map[string]any
	if err := json.Unmarshal(raw, &top); err != nil {
		t.Fatalf("unmarshal: %v (raw: %s)", err, raw)
	}
	if top["executed"] != false {
		t.Errorf("executed = %v, want false (raw: %s)", top["executed"], raw)
	}
	blocked, ok := top["blocked"].(map[string]any)
	if !ok {
		t.Fatalf("missing blocked object (raw: %s)", raw)
	}
	if blocked["blocked_by"] != "precondition" {
		t.Errorf("blocked_by = %v, want precondition", blocked["blocked_by"])
	}
	if blocked["missing_state"] != "backup_validated" {
		t.Errorf("missing_state = %v, want backup_validated", blocked["missing_state"])
	}
	steps, _ := blocked["established_by_steps"].([]any)
	if len(steps) != 1 || steps[0] != "validate_backup" {
		t.Errorf("established_by_steps = %v, want [validate_backup]", blocked["established_by_steps"])
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

// Test_MCP_ExecuteStep_ReplayedBlockSaysToUseANewKey covers an agent that
// reuses its idempotency_key after fixing what was missing: it gets the
// original blocked answer back, marked replayed, with a hint to use a new key,
// and a new key then runs the step.
func Test_MCP_ExecuteStep_ReplayedBlockSaysToUseANewKey(t *testing.T) {
	c := newTestClient(t)
	call := func(step, key string) ExecuteStepResult {
		return structuredAs[ExecuteStepResult](t, callTool(t, c, "execute_step", map[string]any{
			"workflow": "db-maintenance", "trace_id": "hint-1", "step": step, "idempotency_key": key,
		}))
	}

	first := call("drop_prod_db", "drop-1")
	if first.Executed || first.Replayed || first.Hint != "" {
		t.Fatalf("a fresh block should not be replayed or carry a hint: %+v", first)
	}
	call("take_backup", "take-1")
	call("validate_backup", "validate-1")

	again := call("drop_prod_db", "drop-1")
	if again.Executed || !again.Replayed {
		t.Fatalf("same key should replay the original block: %+v", again)
	}
	if !strings.Contains(again.Hint, "new idempotency_key") {
		t.Errorf("a replayed block should say to use a new key, got %q", again.Hint)
	}

	fresh := call("drop_prod_db", "drop-2")
	if !fresh.Executed || fresh.Replayed || fresh.Hint != "" {
		t.Fatalf("a new key should run the step with no hint: %+v", fresh)
	}
}

func Test_MCP_ExecuteStep_ReplayedSuccessHasNoHint(t *testing.T) {
	c := newTestClient(t)
	args := map[string]any{"workflow": "db-maintenance", "trace_id": "hint-2", "step": "take_backup", "idempotency_key": "k"}
	callTool(t, c, "execute_step", args)
	again := structuredAs[ExecuteStepResult](t, callTool(t, c, "execute_step", args))
	if !again.Replayed || again.Hint != "" {
		t.Fatalf("a replayed success needs no hint: %+v", again)
	}
}

// A call with no trace_id used to land in one shared empty-id trace, so a run
// that forgot it could use the steps another run had finished. The schema marks
// trace_id required; the server now enforces it.
func Test_MCP_MissingTraceIDIsRefused(t *testing.T) {
	c := newTestClient(t)
	for _, tool := range []string{"execute_step", "validate_step"} {
		for name, args := range map[string]map[string]any{
			"omitted": {"workflow": "db-maintenance", "step": "take_backup"},
			"empty":   {"workflow": "db-maintenance", "step": "take_backup", "trace_id": ""},
		} {
			res := callTool(t, c, tool, args)
			if !res.IsError {
				t.Errorf("%s with trace_id %s should be refused, got: %s", tool, name, resultText(res))
				continue
			}
			if !strings.Contains(resultText(res), "trace_id is required") {
				t.Errorf("%s with trace_id %s: message should say trace_id is required, got %q", tool, name, resultText(res))
			}
		}
	}

	// Two callers that both leave it out can no longer build on each other's steps.
	for _, step := range []string{"take_backup", "validate_backup", "drop_prod_db"} {
		res := callTool(t, c, "execute_step", map[string]any{"workflow": "db-maintenance", "step": step})
		if !res.IsError || strings.Contains(resultText(res), `"executed":true`) {
			t.Errorf("%s without a trace_id must not execute: %s", step, resultText(res))
		}
	}
	// A real trace is unaffected: the drop is still blocked there.
	got := structuredAs[ExecuteStepResult](t, callTool(t, c, "execute_step", map[string]any{
		"workflow": "db-maintenance", "trace_id": "real-1", "step": "drop_prod_db",
	}))
	if got.Executed || got.Blocked == nil {
		t.Errorf("drop_prod_db on a fresh trace must be blocked: %+v", got)
	}
}

// An agent chooses its own idempotency_key. A key already used in the trace for
// one step must never produce an answer for a different step: reporting a step
// as executed without checking it would hand out an approval the barrier never
// gave.
func Test_MCP_ExecuteStep_KeyReusedForADifferentStepIsRefused(t *testing.T) {
	c := newTestClient(t)
	call := func(trace, step, key string) *mcp.CallToolResult {
		return callTool(t, c, "execute_step", map[string]any{
			"workflow": "db-maintenance", "trace_id": trace, "step": step, "idempotency_key": key,
		})
	}

	if got := structuredAs[ExecuteStepResult](t, call("reuse-1", "take_backup", "k")); !got.Executed {
		t.Fatalf("take_backup should run: %+v", got)
	}

	// No validated backup exists, so drop_prod_db is blocked. The key from the
	// successful call must not turn that into an approval.
	res := call("reuse-1", "drop_prod_db", "k")
	if !res.IsError {
		t.Fatalf("a reused key should be an error result, got: %s", resultText(res))
	}
	got := structuredAs[KeyReusedResult](t, res)
	if got.UsedForStep != "take_backup" || got.RequestedStep != "drop_prod_db" || got.IdempotencyKey != "k" {
		t.Errorf("unexpected result: %+v", got)
	}
	if !strings.Contains(got.Error, "new idempotency_key") {
		t.Errorf("the error should say to use a new key, got %q", got.Error)
	}
	if strings.Contains(resultText(res), `"executed":true`) {
		t.Errorf("a refused call must not say executed: %s", resultText(res))
	}

	// The barrier still decides drop_prod_db on its own, and the trace is unchanged.
	v := structuredAs[ValidateStepResult](t, callTool(t, c, "validate_step", map[string]any{
		"workflow": "db-maintenance", "trace_id": "reuse-1", "step": "drop_prod_db",
	}))
	if v.Safe {
		t.Error("drop_prod_db must still be unsafe")
	}
	real := structuredAs[ExecuteStepResult](t, call("reuse-1", "drop_prod_db", "another"))
	if real.Executed || real.Blocked == nil {
		t.Errorf("drop_prod_db with a fresh key must be blocked: %+v", real)
	}

	// A key first used on a blocked call must not hide an allowed step either.
	call("reuse-2", "drop_prod_db", "j")
	res2 := call("reuse-2", "take_backup", "j")
	if !res2.IsError {
		t.Errorf("reusing a blocked call's key for take_backup should be refused, got: %s", resultText(res2))
	}
	if got := structuredAs[ExecuteStepResult](t, call("reuse-2", "take_backup", "j2")); !got.Executed {
		t.Errorf("take_backup with its own key should run: %+v", got)
	}
}

func Test_MCP_ExecuteStep_RepeatedBlockCountsAttemptsAndPointsAtTheFix(t *testing.T) {
	c := newTestClient(t)
	call := func(key string) ExecuteStepResult {
		return structuredAs[ExecuteStepResult](t, callTool(t, c, "execute_step", map[string]any{
			"workflow": "db-maintenance", "trace_id": "repeat-1", "step": "drop_prod_db", "idempotency_key": key,
		}))
	}

	for i, key := range []string{"k1", "k2", "k3"} {
		res := call(key)
		if res.Executed || res.Blocked == nil {
			t.Fatalf("call %d should be blocked: %+v", i+1, res)
		}
		if res.Blocked.Attempts != i+1 {
			t.Errorf("call %d: attempts = %d, want %d", i+1, res.Blocked.Attempts, i+1)
		}
		if res.Blocked.Next != verify.NextRunEstablishingSteps {
			t.Errorf("call %d: next = %q, want %q", i+1, res.Blocked.Next, verify.NextRunEstablishingSteps)
		}
	}

	// The same key replays the original block and does not count again.
	replay := call("k2")
	if !replay.Replayed || replay.Blocked.Attempts != 2 {
		t.Errorf("a replay should return the original count, got replayed=%v attempts=%d", replay.Replayed, replay.Blocked.Attempts)
	}
	if next := call("k4"); next.Blocked.Attempts != 4 {
		t.Errorf("replay must not count: next real block has attempts %d, want 4", next.Blocked.Attempts)
	}
}

func Test_MCP_ValidateStep_TellsTheNextActionButDoesNotCount(t *testing.T) {
	c := newTestClient(t)
	args := map[string]any{"workflow": "db-maintenance", "trace_id": "dry-1", "step": "drop_prod_db"}
	for i := 0; i < 2; i++ {
		res := structuredAs[ValidateStepResult](t, callTool(t, c, "validate_step", args))
		if res.Safe || res.Reason == nil {
			t.Fatalf("should be blocked: %+v", res)
		}
		if res.Reason.Attempts != 0 || res.Reason.Next != verify.NextRunEstablishingSteps {
			t.Errorf("a dry run has no attempts and still names the next action, got attempts=%d next=%q", res.Reason.Attempts, res.Reason.Next)
		}
	}
	res := structuredAs[ExecuteStepResult](t, callTool(t, c, "execute_step", map[string]any{
		"workflow": "db-maintenance", "trace_id": "dry-1", "step": "drop_prod_db",
	}))
	if res.Blocked.Attempts != 1 {
		t.Errorf("dry runs must not count toward attempts, got %d", res.Blocked.Attempts)
	}
}

// An id is kept in memory for every trace and idempotency key, so a caller
// must not be able to make the server keep an id of any size.
func Test_MCP_OverlongIDsAreRefusedAndNothingIsStored(t *testing.T) {
	long := strings.Repeat("x", runbookstore.MaxIDLength+1)
	store := runbookstore.New()
	wf := dbMaintenanceWorkflow(t)
	if err := store.RegisterWorkflow("db-maintenance", wf); err != nil {
		t.Fatal(err)
	}
	c, err := client.NewInProcessClient(New(store))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION, ClientInfo: mcp.Implementation{Name: "t", Version: "0"},
	}}); err != nil {
		t.Fatal(err)
	}

	for name, args := range map[string]map[string]any{
		"trace_id":        {"workflow": "db-maintenance", "step": "take_backup", "trace_id": long},
		"idempotency_key": {"workflow": "db-maintenance", "step": "take_backup", "trace_id": "ok", "idempotency_key": long},
	} {
		res := callTool(t, c, "execute_step", args)
		if !res.IsError || !strings.Contains(resultText(res), name) {
			t.Errorf("an overlong %s should be refused, naming it: %s", name, resultText(res))
		}
	}
	if res := callTool(t, c, "validate_step", map[string]any{"workflow": "db-maintenance", "step": "take_backup", "trace_id": long}); !res.IsError {
		t.Errorf("validate_step should refuse an overlong trace_id: %s", resultText(res))
	}
	if got := store.TraceCount(); got != 0 {
		t.Errorf("a refused call must not create a trace, the store holds %d", got)
	}
	// An id at the limit still works.
	ok := strings.Repeat("y", runbookstore.MaxIDLength)
	if res := callTool(t, c, "execute_step", map[string]any{"workflow": "db-maintenance", "step": "take_backup", "trace_id": ok}); res.IsError {
		t.Errorf("an id at the limit should work: %s", resultText(res))
	}
}
