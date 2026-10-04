package a2aagent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2aclient"

	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
	"github.com/sharedcode/joltrin/v5/verify"
)

const invokePath = "/a2a/invoke"

// dbMaintenanceWorkflow mirrors verify's and tools/mcpserver's own test
// fixture, so all three packages are demonstrably verifying the same
// property against the same shape of workflow.
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

// newTestServer stands up a real HTTP server running this package's actual
// a2asrv wiring (NewMux), so these tests exercise the real JSON-RPC wire
// protocol via httptest, not direct Go function calls to Execute.
func newTestServer(t *testing.T) (*httptest.Server, *runbookstore.Store) {
	t.Helper()
	store := runbookstore.New()
	if err := store.RegisterWorkflow("db-maintenance", dbMaintenanceWorkflow(t)); err != nil {
		t.Fatalf("RegisterWorkflow: %v", err)
	}
	srv := httptest.NewServer(NewMux(store, "", invokePath))
	t.Cleanup(srv.Close)
	return srv, store
}

func newTestClient(t *testing.T, srv *httptest.Server) *a2aclient.Client {
	t.Helper()
	c, err := a2aclient.NewFromEndpoints(context.Background(), []a2a.AgentInterface{
		{URL: srv.URL + invokePath, Transport: a2a.TransportProtocolJSONRPC},
	})
	if err != nil {
		t.Fatalf("a2aclient.NewFromEndpoints: %v", err)
	}
	t.Cleanup(func() { c.Destroy() })
	return c
}

func sendStep(t *testing.T, c *a2aclient.Client, workflow, traceID, step string) *a2a.Task {
	t.Helper()
	return sendStepWithKey(t, c, workflow, traceID, step, "")
}

// sendStepWithKey is sendStep plus an optional idempotency_key, split out
// rather than changing sendStep's signature so every existing call site
// (which never needs a key) is unaffected.
func sendStepWithKey(t *testing.T, c *a2aclient.Client, workflow, traceID, step, idempotencyKey string) *a2a.Task {
	t.Helper()
	data := map[string]any{
		"workflow": workflow,
		"trace_id": traceID,
		"step":     step,
	}
	if idempotencyKey != "" {
		data["idempotency_key"] = idempotencyKey
	}
	result, err := c.SendMessage(context.Background(), &a2a.MessageSendParams{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.DataPart{Data: data}),
	})
	if err != nil {
		t.Fatalf("SendMessage(%s): %v", step, err)
	}
	task, ok := result.(*a2a.Task)
	if !ok {
		t.Fatalf("SendMessage(%s): expected a *a2a.Task result, got %T", step, result)
	}
	return task
}

func Test_A2A_ExecuteStep_BlockedGoesInputRequired(t *testing.T) {
	srv, _ := newTestServer(t)
	c := newTestClient(t, srv)

	task := sendStep(t, c, "db-maintenance", "incident-1", "drop_prod_db")
	if task.Status.State != a2a.TaskStateInputRequired {
		t.Fatalf("expected TaskStateInputRequired for a blocked step, got %q", task.Status.State)
	}
}

// Test_A2A_ExecuteStep_BlockedCarriesStructuredData confirms an A2A caller
// gets the same structured block reason an MCP caller gets from
// tools/mcpserver's execute_step (blocked_by, missing_state,
// established_by_steps), as a DataPart alongside the human-readable
// TextPart, not just text to parse.
func Test_A2A_ExecuteStep_BlockedCarriesStructuredData(t *testing.T) {
	srv, _ := newTestServer(t)
	c := newTestClient(t, srv)

	task := sendStep(t, c, "db-maintenance", "incident-1", "drop_prod_db")
	if task.Status.Message == nil {
		t.Fatal("expected a status message on the input-required task")
	}

	var data map[string]any
	for _, part := range task.Status.Message.Parts {
		if dp, ok := part.(a2a.DataPart); ok {
			data = dp.Data
		}
	}
	if data == nil {
		t.Fatal("expected a DataPart on the input-required message")
	}
	if data["missing_state"] != "backup_validated" {
		t.Fatalf("expected missing_state %q, got %v", "backup_validated", data["missing_state"])
	}
	established, ok := data["established_by_steps"].([]any)
	if !ok || len(established) == 0 {
		t.Fatalf("expected established_by_steps to name at least one step, got %v", data["established_by_steps"])
	}
}

// Test_A2A_ExecuteStep_IdempotencyKey_RetryDoesNotDuplicate is the A2A
// counterpart of the MCP proof: a client retrying with the same
// idempotency_key over the real A2A HTTP+JSON-RPC wire protocol gets back
// the original outcome, marked replayed, instead of a second commit.
func Test_A2A_ExecuteStep_IdempotencyKey_RetryDoesNotDuplicate(t *testing.T) {
	srv, store := newTestServer(t)
	c := newTestClient(t, srv)

	first := sendStepWithKey(t, c, "db-maintenance", "incident-retry", "take_backup", "req-1")
	if first.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("expected the first call to complete, got %q", first.Status.State)
	}
	if replayed := artifactBool(t, first, "replayed"); replayed {
		t.Fatal("expected the first call to report replayed=false")
	}

	retry := sendStepWithKey(t, c, "db-maintenance", "incident-retry", "take_backup", "req-1")
	if retry.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("expected the retry to also complete, got %q", retry.Status.State)
	}
	if replayed := artifactBool(t, retry, "replayed"); !replayed {
		t.Fatal("expected the retry to report replayed=true")
	}

	if got := store.TraceFor("incident-retry").ExecutedSteps(); len(got) != 1 {
		t.Fatalf("expected exactly one commit despite the retry, got %v", got)
	}
}

// artifactBool extracts a bool field from the first DataPart artifact on
// task, failing the test if none is found.
func artifactBool(t *testing.T, task *a2a.Task, key string) bool {
	t.Helper()
	for _, artifact := range task.Artifacts {
		for _, part := range artifact.Parts {
			if dp, ok := part.(a2a.DataPart); ok {
				if v, ok := dp.Data[key].(bool); ok {
					return v
				}
			}
		}
	}
	t.Fatalf("expected an artifact DataPart with key %q", key)
	return false
}

func Test_A2A_ExecuteStep_UnknownWorkflowFails(t *testing.T) {
	srv, _ := newTestServer(t)
	c := newTestClient(t, srv)

	task := sendStep(t, c, "does-not-exist", "incident-1", "drop_prod_db")
	if task.Status.State != a2a.TaskStateFailed {
		t.Fatalf("expected TaskStateFailed for an unknown workflow, got %q", task.Status.State)
	}
	if task.Status.Message == nil {
		t.Fatal("expected a status message explaining the failure")
	}
	var text string
	for _, part := range task.Status.Message.Parts {
		if tp, ok := part.(a2a.TextPart); ok {
			text += tp.Text
		}
	}
	if !strings.Contains(text, "db-maintenance") {
		t.Fatalf("expected the failure message to list the registered workflow as a correction, got: %s", text)
	}
}

func Test_A2A_ExecuteStep_UnknownStepFails(t *testing.T) {
	srv, _ := newTestServer(t)
	c := newTestClient(t, srv)

	task := sendStep(t, c, "db-maintenance", "incident-1", "nonexistent_step")
	if task.Status.State != a2a.TaskStateFailed {
		t.Fatalf("expected TaskStateFailed for an unknown step, got %q", task.Status.State)
	}
}

// Test_A2A_ExecuteStep_FullSequence drives the exact lifecycle the design
// brief named: submitted -> working -> (input-required | completed),
// delegating each step over the real A2A wire protocol.
func Test_A2A_ExecuteStep_FullSequence(t *testing.T) {
	srv, _ := newTestServer(t)
	c := newTestClient(t, srv)
	const traceID = "incident-2"

	for _, step := range []string{"take_backup", "validate_backup"} {
		task := sendStep(t, c, "db-maintenance", traceID, step)
		if task.Status.State != a2a.TaskStateCompleted {
			t.Fatalf("step %q: expected TaskStateCompleted, got %q", step, task.Status.State)
		}
	}

	task := sendStep(t, c, "db-maintenance", traceID, "drop_prod_db")
	if task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("expected drop_prod_db to complete after backup+validate, got %q", task.Status.State)
	}
	if len(task.Artifacts) == 0 {
		t.Fatal("expected a completed task to carry at least one artifact with the execution result")
	}
}

// Test_A2A_MCP_ShareTrace is the concrete interoperability proof: a step
// executed via the MCP tool path (exercised directly against the shared
// store here, mirroring what tools/mcpserver's own tests do over the real
// MCP wire protocol) is visible to, and enforced against, the A2A path
// against the identical trace_id.
func Test_A2A_MCP_ShareTrace(t *testing.T) {
	srv, store := newTestServer(t)
	c := newTestClient(t, srv)
	const traceID = "incident-shared"

	wf, ok := store.Workflow("db-maintenance")
	if !ok {
		t.Fatal("expected db-maintenance to be registered")
	}
	trace := store.TraceFor(traceID)
	if err := wf.CheckSafety(trace, "take_backup"); err != nil {
		t.Fatalf("take_backup unexpectedly blocked: %v", err)
	}
	if err := wf.Commit(trace, "take_backup"); err != nil {
		t.Fatalf("Commit(take_backup): %v", err)
	}
	if err := wf.CheckSafety(trace, "validate_backup"); err != nil {
		t.Fatalf("validate_backup unexpectedly blocked: %v", err)
	}
	if err := wf.Commit(trace, "validate_backup"); err != nil {
		t.Fatalf("Commit(validate_backup): %v", err)
	}

	// The A2A path, over the real wire protocol, should now see
	// backup_validated already established by the "MCP-side" commits above
	// and allow the drop.
	task := sendStep(t, c, "db-maintenance", traceID, "drop_prod_db")
	if task.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("expected drop_prod_db to complete: the shared trace already has a validated backup, got %q", task.Status.State)
	}
}

// Test_AgentCard_SetsProtocolVersion guards against ProtocolVersion silently
// going unset again: it is a plain struct field with no compiler-enforced
// requirement, and every other field on this card is populated deliberately.
// An empty protocolVersion in the served agent card is exactly the kind of
// thing a first-time evaluator would screenshot next to MCP's populated one
// and ask whether this project is actually finished.
func Test_AgentCard_SetsProtocolVersion(t *testing.T) {
	card := AgentCard("http://127.0.0.1:8099/a2a/invoke")
	if card.ProtocolVersion == "" {
		t.Fatal("AgentCard().ProtocolVersion is empty, want the SDK's a2a.Version")
	}
	if card.ProtocolVersion != string(a2a.Version) {
		t.Fatalf("AgentCard().ProtocolVersion = %q, want %q (a2a.Version)", card.ProtocolVersion, a2a.Version)
	}
}

// A2A has always required a trace_id. Pin it so the two protocols stay the same.
func Test_A2A_ExecuteStep_MissingTraceIDFails(t *testing.T) {
	srv, _ := newTestServer(t)
	c := newTestClient(t, srv)
	task := sendStep(t, c, "db-maintenance", "", "take_backup")
	if task.Status.State != a2a.TaskStateFailed {
		t.Errorf("want the task to fail without a trace_id, got %q", task.Status.State)
	}
}

// A key already used in the trace for one step must not answer for a different
// step. Over A2A the second call fails instead of reporting the destructive step
// as completed.
func Test_A2A_ExecuteStep_KeyReusedForADifferentStepFails(t *testing.T) {
	srv, store := newTestServer(t)
	c := newTestClient(t, srv)

	first := sendStepWithKey(t, c, "db-maintenance", "reuse-1", "take_backup", "k")
	if first.Status.State != a2a.TaskStateCompleted {
		t.Fatalf("take_backup should complete, got %q", first.Status.State)
	}
	// No validated backup exists, so drop_prod_db must not complete.
	second := sendStepWithKey(t, c, "db-maintenance", "reuse-1", "drop_prod_db", "k")
	if second.Status.State == a2a.TaskStateCompleted {
		t.Fatalf("a reused key must not complete drop_prod_db")
	}
	if second.Status.State != a2a.TaskStateFailed {
		t.Errorf("want the task to fail, got %q", second.Status.State)
	}
	if got := store.TraceFor("reuse-1").ExecutedSteps(); len(got) != 1 || got[0] != "take_backup" {
		t.Errorf("trace = %v, want only take_backup", got)
	}
}
