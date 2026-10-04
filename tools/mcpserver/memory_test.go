package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sharedcode/joltrin/v5/tools/blocklog"
	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
)

// connect starts a server with the given memory options and returns the client
// plus the instructions the server sent when the client connected.
func connect(t *testing.T, opts ...Option) (*client.Client, string) {
	t.Helper()
	store := runbookstore.New()
	if err := store.RegisterWorkflow("db-maintenance", dbMaintenanceWorkflow(t)); err != nil {
		t.Fatalf("RegisterWorkflow: %v", err)
	}
	c, err := client.NewInProcessClient(New(store, opts...))
	if err != nil {
		t.Fatalf("NewInProcessClient: %v", err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	init, err := c.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo:      mcp.Implementation{Name: "sop-test-client", Version: "0.0.1"},
		},
	})
	if err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c, init.Instructions
}

func execute(t *testing.T, c *client.Client, trace, step, key string) ExecuteStepResult {
	t.Helper()
	args := map[string]any{"workflow": "db-maintenance", "trace_id": trace, "step": step}
	if key != "" {
		args["idempotency_key"] = key
	}
	return structuredAs[ExecuteStepResult](t, callTool(t, c, "execute_step", args))
}

func TestMemoryOffByDefault(t *testing.T) {
	c, instr := connect(t)
	if instr != "" {
		t.Errorf("no memory configured, but instructions = %q", instr)
	}
	if execute(t, c, "r1", "drop_prod_db", "").Executed {
		t.Fatal("drop_prod_db must still be blocked")
	}
}

func TestNextSessionIsToldWhatBlockedBefore(t *testing.T) {
	dir := t.TempDir()
	lessons := filepath.Join(dir, "LESSONS.md")
	open := func() *blocklog.Log {
		l, err := blocklog.Open(filepath.Join(dir, "blocks.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { l.Close() })
		return l
	}

	// First session: cold. Nothing recorded, no instructions.
	log1 := open()
	c1, instr1 := connect(t, WithMemory(log1, lessons))
	if instr1 != "" {
		t.Fatalf("a fresh server has nothing to say, got %q", instr1)
	}
	if got := execute(t, c1, "run-1", "drop_prod_db", ""); got.Executed || got.Blocked == nil {
		t.Fatalf("expected a block, got %+v", got)
	}
	log1.Close()

	// Second session: warm. The block from the first run is in the instructions.
	_, instr2 := connect(t, WithMemory(open(), lessons))
	for _, want := range []string{
		"advisory",
		"db-maintenance",
		"before drop_prod_db",
		"take_backup, then validate_backup",
		"blocked in 1 run",
		"backup_validated",
	} {
		if !strings.Contains(instr2, want) {
			t.Errorf("instructions missing %q:\n%s", want, instr2)
		}
	}

	b, err := os.ReadFile(lessons)
	if err != nil {
		t.Fatalf("lessons file not written: %v", err)
	}
	if !strings.Contains(string(b), "take_backup, then validate_backup") || !strings.Contains(string(b), "# Joltrin lessons") {
		t.Errorf("lessons file:\n%s", b)
	}
}

func TestRetriesInOneRunCountOnce(t *testing.T) {
	log := blocklog.New()
	c, _ := connect(t, WithMemory(log, ""))
	for i := 0; i < 4; i++ {
		execute(t, c, "run-1", "drop_prod_db", "")
	}
	// A retried call replayed from an idempotency key is not a new block either.
	execute(t, c, "run-2", "drop_prod_db", "k1")
	execute(t, c, "run-2", "drop_prod_db", "k1")

	_, instr := connect(t, WithMemory(log, ""))
	if !strings.Contains(instr, "blocked in 2 runs") {
		t.Errorf("want 2 runs (run-1 and run-2), got:\n%s", instr)
	}
}

func TestValidateStepDoesNotRecord(t *testing.T) {
	log := blocklog.New()
	c, _ := connect(t, WithMemory(log, ""))
	callTool(t, c, "validate_step", map[string]any{"workflow": "db-maintenance", "trace_id": "r1", "step": "drop_prod_db"})
	_, instr := connect(t, WithMemory(log, ""))
	if instr != "" {
		t.Errorf("a dry run is not a mistake, but got:\n%s", instr)
	}
}

func TestSuccessfulStepsAndUnknownStepsAreNotRecorded(t *testing.T) {
	log := blocklog.New()
	c, _ := connect(t, WithMemory(log, ""))
	execute(t, c, "r1", "take_backup", "")
	callTool(t, c, "execute_step", map[string]any{"workflow": "db-maintenance", "trace_id": "r1", "step": "no_such_step"})
	_, instr := connect(t, WithMemory(log, ""))
	if instr != "" {
		t.Errorf("only blocks are recorded, got:\n%s", instr)
	}
}

func TestMemoryNeverRelaxesTheBarrier(t *testing.T) {
	log := blocklog.New()
	c, _ := connect(t, WithMemory(log, ""))
	for i := 0; i < 3; i++ {
		execute(t, c, "earlier-run", "drop_prod_db", "")
	}
	// A new run with a lot of history behind it is still blocked until the
	// steps it depends on have committed.
	c2, _ := connect(t, WithMemory(log, ""))
	if execute(t, c2, "new-run", "drop_prod_db", "").Executed {
		t.Fatal("history must not unlock a step")
	}
	for _, s := range []string{"take_backup", "validate_backup", "drop_prod_db"} {
		if got := execute(t, c2, "new-run", s, ""); !got.Executed {
			t.Fatalf("%s should run once its dependencies committed: %+v", s, got)
		}
	}
}

func TestChangedRunbookDoesNotReuseOldLessons(t *testing.T) {
	log := blocklog.New()
	c, _ := connect(t, WithMemory(log, ""))
	execute(t, c, "run-1", "drop_prod_db", "")

	// Same workflow name, different steps: the old advice must not apply.
	store := runbookstore.New()
	wf := dbMaintenanceWorkflow(t)
	delete(wf.Steps, "restore_from_backup")
	if err := store.RegisterWorkflow("db-maintenance", wf); err != nil {
		t.Skipf("variant runbook not registrable: %v", err)
	}
	if got := instructions(store, log); got != "" {
		t.Errorf("a changed runbook must start clean, got:\n%s", got)
	}
}

func TestFixOrderFollowsRequirementsBackToTheStart(t *testing.T) {
	wf := dbMaintenanceWorkflow(t)
	got := fixOrder(wf, "backup_validated")
	if len(got) != 2 || got[0] != "take_backup" || got[1] != "validate_backup" {
		t.Errorf("fixOrder(backup_validated) = %v", got)
	}
	if got := fixOrder(wf, "no_such_state"); got != nil {
		t.Errorf("unknown state should give no order, got %v", got)
	}
	// rollback_complete is established by two steps, so the path is not clear.
	if got := fixOrder(wf, "rollback_complete"); got != nil {
		t.Errorf("ambiguous state should give no order, got %v", got)
	}
}

func TestLessonsAreCapped(t *testing.T) {
	log := blocklog.New()
	store := runbookstore.New()
	if err := store.RegisterWorkflow("db-maintenance", dbMaintenanceWorkflow(t)); err != nil {
		t.Fatal(err)
	}
	wf, _ := store.Workflow("db-maintenance")
	v := workflowVersion(wf)
	for i := 0; i < 40; i++ {
		log.Record(blocklog.Entry{Workflow: "db-maintenance", Version: v, TraceID: "t", Step: "drop_prod_db",
			BlockedBy: "rule-" + string(rune('a'+i%26)) + string(rune('a'+i/26)), MissingState: "backup_validated"})
	}
	if n := len(lessonLines(store, log)); n > maxLessonsPerWorkflow {
		t.Errorf("got %d lines for one workflow, cap is %d", n, maxLessonsPerWorkflow)
	}
}
