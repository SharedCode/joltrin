package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
	"github.com/sharedcode/joltrin/v5/verify"
)

func clientWith(t *testing.T, opts ...Option) *client.Client {
	t.Helper()
	store := runbookstore.New()
	if err := store.RegisterWorkflow("db-maintenance", dbMaintenanceWorkflow(t)); err != nil {
		t.Fatal(err)
	}
	c, err := client.NewInProcessClient(New(store, opts...))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "t", Version: "0"},
	}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func exec(t *testing.T, c *client.Client, workflow, trace, step string) ExecuteStepResult {
	t.Helper()
	res := callTool(t, c, "execute_step", map[string]any{"workflow": workflow, "trace_id": trace, "step": step})
	if res.IsError {
		return ExecuteStepResult{}
	}
	return structuredAs[ExecuteStepResult](t, res)
}

func TestMetricsAndDecisionLogCountEachOutcome(t *testing.T) {
	m := NewMetrics()
	var logBuf bytes.Buffer
	c := clientWith(t, WithMetrics(m), WithDecisionLog(&logBuf))

	exec(t, c, "db-maintenance", "t1", "drop_prod_db")  // blocked
	exec(t, c, "db-maintenance", "t1", "take_backup")   // allowed
	exec(t, c, "no-such-workflow", "t1", "take_backup") // invalid

	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()
	for _, want := range []string{
		`joltrin_steps_total{workflow="db-maintenance",outcome="blocked"} 1`,
		`joltrin_steps_total{workflow="db-maintenance",outcome="allowed"} 1`,
		`joltrin_steps_total{workflow="",outcome="invalid"} 1`,
		`joltrin_blocks_total{workflow="db-maintenance",rule="precondition"} 1`,
		`joltrin_step_duration_seconds_count 3`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q\n%s", want, body)
		}
	}
	if strings.Contains(body, "no-such-workflow") {
		t.Error("an unknown workflow name must not become a label")
	}

	lines := strings.Split(strings.TrimSpace(logBuf.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d log lines, want 3:\n%s", len(lines), logBuf.String())
	}
	var first struct {
		Event, Workflow, Step, Outcome string
		BlockedBy                      string `json:"blocked_by"`
		Missing                        string `json:"missing_state"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if first.Event != "execute_step" || first.Step != "drop_prod_db" || first.Outcome != "blocked" ||
		first.BlockedBy != "precondition" || first.Missing != "backup_validated" {
		t.Errorf("first log line: %+v", first)
	}
	var second, third struct {
		Workflow, Step, Outcome string
		Attempts                int
	}
	json.Unmarshal([]byte(lines[1]), &second)
	json.Unmarshal([]byte(lines[2]), &third)
	if second.Step != "take_backup" || second.Outcome != "allowed" || second.Workflow != "db-maintenance" {
		t.Errorf("second log line: %+v", second)
	}
	if third.Outcome != "invalid" || third.Workflow != "" {
		t.Errorf("third log line: %+v", third)
	}
}

func TestRetryLimitTellsTheAgentToStopAndAsk(t *testing.T) {
	m := NewMetrics()
	c := clientWith(t, WithRetryLimit(2), WithMetrics(m))
	for i := 1; i <= 2; i++ {
		r := exec(t, c, "db-maintenance", "t1", "drop_prod_db")
		if r.Blocked == nil || r.Blocked.Next != verify.NextRunEstablishingSteps {
			t.Fatalf("attempt %d: %+v", i, r.Blocked)
		}
	}
	r := exec(t, c, "db-maintenance", "t1", "drop_prod_db")
	if r.Executed || r.Blocked == nil {
		t.Fatalf("the step must still be blocked: %+v", r)
	}
	if r.Blocked.Next != verify.NextStopAndAsk || !strings.Contains(r.Hint, "Stop and ask a person") {
		t.Errorf("third attempt: next=%q hint=%q", r.Blocked.Next, r.Hint)
	}
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `joltrin_retry_limit_stops_total{workflow="db-maintenance"} 1`) {
		t.Errorf("stop not counted:\n%s", rec.Body.String())
	}
}

func TestNoRetryLimitByDefault(t *testing.T) {
	c := clientWith(t)
	var r ExecuteStepResult
	for i := 0; i < 6; i++ {
		r = exec(t, c, "db-maintenance", "t1", "drop_prod_db")
	}
	if r.Blocked == nil || r.Blocked.Next != verify.NextRunEstablishingSteps || r.Hint != "" {
		t.Errorf("without a limit the advice must not change: %+v hint=%q", r.Blocked, r.Hint)
	}
}

func TestObserveOnlyLetsABlockedStepThroughAndSaysSo(t *testing.T) {
	m := NewMetrics()
	var logBuf bytes.Buffer
	c := clientWith(t, WithObserveOnly(), WithMetrics(m), WithDecisionLog(&logBuf))

	got := exec(t, c, "db-maintenance", "t1", "drop_prod_db")
	if !got.Executed || got.Blocked != nil {
		t.Fatalf("observe mode should run the step: %+v", got)
	}
	if got.WouldHaveBlocked == nil || got.WouldHaveBlocked.MissingState != "backup_validated" {
		t.Fatalf("result should say what would have blocked it: %+v", got.WouldHaveBlocked)
	}
	if got.Hint == "" {
		t.Error("result should carry the observe-mode hint")
	}

	// The step is recorded, so the trace moves on like a real run.
	got = exec(t, c, "db-maintenance", "t1", "take_backup")
	if !got.Executed || got.WouldHaveBlocked != nil {
		t.Fatalf("a step that passes should not be flagged: %+v", got)
	}

	if !strings.Contains(logBuf.String(), `"outcome":"observed"`) || !strings.Contains(logBuf.String(), `"blocked_by":"precondition"`) {
		t.Errorf("decision log should record the observed block, got %s", logBuf.String())
	}
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(rec.Body.String(), `joltrin_steps_total{workflow="db-maintenance",outcome="observed"} 1`) {
		t.Errorf("metrics should count the observed step, got %s", rec.Body.String())
	}
}

func TestObserveOnlyReplayDoesNotCommitTwice(t *testing.T) {
	c := clientWith(t, WithObserveOnly())
	call := func() ExecuteStepResult {
		res := callTool(t, c, "execute_step", map[string]any{"workflow": "db-maintenance", "trace_id": "t1", "step": "drop_prod_db", "idempotency_key": "k1"})
		return structuredAs[ExecuteStepResult](t, res)
	}
	first, second := call(), call()
	if !second.Replayed || len(second.Trace) != len(first.Trace) {
		t.Fatalf("a replay must not add a second commit: first=%v second=%v", first.Trace, second.Trace)
	}
}

func TestEnforcingModeIsUnchanged(t *testing.T) {
	c := clientWith(t)
	got := exec(t, c, "db-maintenance", "t1", "drop_prod_db")
	if got.Executed || got.Blocked == nil || got.WouldHaveBlocked != nil {
		t.Fatalf("default mode must still block: %+v", got)
	}
}
