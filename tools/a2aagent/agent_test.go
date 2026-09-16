package a2aagent

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/a2aproject/a2a-go/a2aclient"

	"github.com/sharedcode/joltrin/ai/verify"
	"github.com/sharedcode/joltrin/tools/runbookstore"
)

const invokePath = "/a2a/invoke"

// dbMaintenanceWorkflow mirrors ai/verify's and tools/mcpserver's own test
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
	result, err := c.SendMessage(context.Background(), &a2a.MessageSendParams{
		Message: a2a.NewMessage(a2a.MessageRoleUser, a2a.DataPart{
			Data: map[string]any{
				"workflow": workflow,
				"trace_id": traceID,
				"step":     step,
			},
		}),
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
