package a2abridge

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/a2a"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sharedcode/joltrin/ai/verify"
	"github.com/sharedcode/joltrin/tools/a2aagent"
	"github.com/sharedcode/joltrin/tools/runbookstore"
)

const invokePath = "/a2a/invoke"

// dbMaintenanceWorkflow mirrors the fixture used throughout ai/verify,
// tools/mcpserver, and tools/a2aagent's own tests, so all four packages
// demonstrably verify the same property against the same workflow shape.
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

// newTestAgent stands up a real tools/a2aagent HTTP server (the actual
// production wiring, NewMux) via httptest, exactly what sop-a2a-bridge
// would be pointed at over the network. The mux needs its own base URL to
// build the agent card, which needs the listener address; httptest.Server
// exposes the listener before Start binds the handler, so the address is
// known first and the handler (and its embedded card) is only wired in
// once, before the server ever accepts a request.
func newTestAgent(t *testing.T) *httptest.Server {
	t.Helper()
	store := runbookstore.New()
	if err := store.RegisterWorkflow("db-maintenance", dbMaintenanceWorkflow(t)); err != nil {
		t.Fatalf("RegisterWorkflow: %v", err)
	}
	srv := httptest.NewUnstartedServer(nil)
	baseURL := "http://" + srv.Listener.Addr().String()
	srv.Config.Handler = a2aagent.NewMux(store, baseURL, invokePath)
	srv.Start()
	return srv
}

// newBridgeClient builds the real bridge (agent-card resolution included,
// exercising the tools/a2aagent/http.go fix that makes the card's "url" the
// actual JSON-RPC endpoint) and connects an in-process MCP client to it,
// the same pattern tools/mcpserver's own tests use to exercise the real MCP
// wire contract rather than calling Go functions directly.
func newBridgeClient(t *testing.T, agentURL string) *client.Client {
	t.Helper()
	ctx := context.Background()

	srv, err := New(ctx, agentURL)
	if err != nil {
		t.Fatalf("a2abridge.New: %v", err)
	}

	c, err := client.NewInProcessClient(srv)
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "sop-bridge-test-client", Version: "0.0.1"},
		},
	}); err != nil {
		t.Fatalf("client.Initialize: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func callExecuteStep(t *testing.T, c *client.Client, traceID, step string) *mcp.CallToolResult {
	t.Helper()
	return callExecuteStepWithKey(t, c, traceID, step, "")
}

// callExecuteStepWithKey is callExecuteStep plus an optional
// idempotency_key, split out rather than changing callExecuteStep's
// signature so every existing call site is unaffected.
func callExecuteStepWithKey(t *testing.T, c *client.Client, traceID, step, idempotencyKey string) *mcp.CallToolResult {
	t.Helper()
	args := map[string]any{
		"workflow": "db-maintenance",
		"trace_id": traceID,
		"step":     step,
	}
	if idempotencyKey != "" {
		args["idempotency_key"] = idempotencyKey
	}
	res, err := c.CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{Name: "execute_step", Arguments: args},
	})
	if err != nil {
		t.Fatalf("CallTool(execute_step, %s): %v", step, err)
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

// Test_Bridge_BlockedGoesToMCPError is the full round trip: an MCP client
// calls execute_step on the bridge, the bridge resolves the remote agent's
// card and delegates over the real A2A wire protocol, and the remote
// agent's TaskStateInputRequired comes back as an MCP tool error naming the
// safety barrier, not a crash and not a silent success.
func Test_Bridge_BlockedGoesToMCPError(t *testing.T) {
	agent := newTestAgent(t)
	defer agent.Close()

	c := newBridgeClient(t, agent.URL)
	res := callExecuteStep(t, c, "incident-1", "drop_prod_db")

	if !res.IsError {
		t.Fatal("expected drop_prod_db to be blocked by the remote safety barrier")
	}
	if !strings.Contains(resultText(res), "blocked by safety barrier") {
		t.Fatalf("expected a safety-barrier error message, got: %s", resultText(res))
	}
}

// Test_Bridge_FullSequence drives take_backup -> validate_backup ->
// drop_prod_db through the bridge, confirming a real, allowed execution
// round-trips correctly end to end (MCP -> bridge -> A2A wire -> executor
// -> shared runbookstore.Store -> back).
func Test_Bridge_FullSequence(t *testing.T) {
	agent := newTestAgent(t)
	defer agent.Close()

	c := newBridgeClient(t, agent.URL)
	const traceID = "incident-2"

	for _, step := range []string{"take_backup", "validate_backup"} {
		res := callExecuteStep(t, c, traceID, step)
		if res.IsError {
			t.Fatalf("step %q unexpectedly blocked/failed: %s", step, resultText(res))
		}
	}

	res := callExecuteStep(t, c, traceID, "drop_prod_db")
	if res.IsError {
		t.Fatalf("expected drop_prod_db to succeed after backup+validate, got: %s", resultText(res))
	}
}

// Test_Bridge_IdempotencyKey_ForwardedToRemoteAgent is the full round trip
// for the idempotency feature: an MCP client calls execute_step on the
// bridge twice with the same idempotency_key, the bridge forwards it to
// the remote A2A agent unchanged, and the retry comes back marked
// replayed, with the remote trace left at one entry, proving the key
// actually reached ai/verify.Trace.CheckAndCommitIdempotent through both
// protocol hops, not just locally on the bridge.
func Test_Bridge_IdempotencyKey_ForwardedToRemoteAgent(t *testing.T) {
	agent := newTestAgent(t)
	defer agent.Close()

	c := newBridgeClient(t, agent.URL)
	const traceID = "incident-retry"

	first := callExecuteStepWithKey(t, c, traceID, "take_backup", "req-1")
	if first.IsError {
		t.Fatalf("first call unexpectedly errored: %s", resultText(first))
	}
	if replayed := bridgeArtifactBool(t, first, "replayed"); replayed {
		t.Fatal("expected the first call to report replayed=false")
	}

	retry := callExecuteStepWithKey(t, c, traceID, "take_backup", "req-1")
	if retry.IsError {
		t.Fatalf("retry unexpectedly errored: %s", resultText(retry))
	}
	if replayed := bridgeArtifactBool(t, retry, "replayed"); !replayed {
		t.Fatal("expected the retry to report replayed=true")
	}
}

// bridgeArtifactBool decodes a real client-received execute_step result's
// structuredContent (which nests the remote agent's artifact data under
// "artifacts", see artifactData) and returns the named bool field from the
// first artifact, failing the test if it isn't found.
func bridgeArtifactBool(t *testing.T, res *mcp.CallToolResult, key string) bool {
	t.Helper()
	raw := res.RawStructuredContent
	if raw == nil {
		var err error
		raw, err = json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal StructuredContent: %v", err)
		}
	}
	var decoded struct {
		Artifacts []map[string]any `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal structured content: %v (raw: %s)", err, raw)
	}
	for _, artifact := range decoded.Artifacts {
		if v, ok := artifact[key].(bool); ok {
			return v
		}
	}
	t.Fatalf("expected an artifact with key %q, got: %s", key, raw)
	return false
}

// Test_Bridge_UnknownWorkflowGoesToMCPError confirms a remote
// TaskStateFailed (as opposed to input-required) also surfaces as an MCP
// tool error, carrying the remote agent's own reason.
func Test_Bridge_UnknownWorkflowGoesToMCPError(t *testing.T) {
	agent := newTestAgent(t)
	defer agent.Close()

	c := newBridgeClient(t, agent.URL)
	res, err := c.CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "execute_step",
			Arguments: map[string]any{
				"workflow": "does-not-exist",
				"trace_id": "incident-4",
				"step":     "drop_prod_db",
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected an unknown workflow to surface as an MCP tool error")
	}
	if !strings.Contains(resultText(res), "remote agent reported failure") {
		t.Fatalf("expected a remote-failure error message, got: %s", resultText(res))
	}
}

// Test_Bridge_MissingArgumentsRejectedLocally confirms the bridge validates
// its own tool arguments before ever making a network call to the remote
// agent, an empty trace_id or step is a local, immediate tool error, not a
// request sent to the wire.
func Test_Bridge_MissingArgumentsRejectedLocally(t *testing.T) {
	agent := newTestAgent(t)
	defer agent.Close()

	c := newBridgeClient(t, agent.URL)
	res, err := c.CallTool(context.Background(), mcp.CallToolRequest{
		Params: mcp.CallToolParams{
			Name: "execute_step",
			Arguments: map[string]any{
				"workflow": "db-maintenance",
				"trace_id": "",
				"step":     "drop_prod_db",
			},
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected a missing trace_id to be rejected")
	}
}

// Test_ArtifactData_SkipsNilArtifacts covers a crash vector a peer
// controls: Task.Artifacts is a slice of pointers, so a remote agent
// answering "artifacts":[null] decodes to a nil element, and dereferencing
// it would panic inside an MCP server the user is depending on.
func Test_ArtifactData_SkipsNilArtifacts(t *testing.T) {
	task := &a2a.Task{
		Artifacts: []*a2a.Artifact{
			nil,
			{Parts: []a2a.Part{a2a.DataPart{Data: map[string]any{"executed": "take_backup"}}}},
			nil,
		},
	}

	got := artifactData(task)
	if len(got) != 1 {
		t.Fatalf("expected the one real artifact, got %d entries: %v", len(got), got)
	}
	if got[0]["executed"] != "take_backup" {
		t.Fatalf("unexpected artifact payload: %v", got[0])
	}
}
