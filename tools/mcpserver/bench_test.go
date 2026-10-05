package mcpserver

import (
	"context"
	"fmt"
	"testing"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"

	"github.com/sharedcode/joltrin/v5/tools/blocklog"
	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
)

func benchClient(b *testing.B, opts ...Option) *client.Client {
	b.Helper()
	store := runbookstore.New()
	wf, err := runbookstore.DBMaintenanceWorkflow()
	if err != nil {
		b.Fatal(err)
	}
	if err := store.RegisterWorkflow("db-maintenance", wf); err != nil {
		b.Fatal(err)
	}
	c, err := client.NewInProcessClient(New(store, opts...))
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		b.Fatal(err)
	}
	if _, err := c.Initialize(ctx, mcp.InitializeRequest{Params: mcp.InitializeParams{
		ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
		ClientInfo:      mcp.Implementation{Name: "bench", Version: "0"},
	}}); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { c.Close() })
	return c
}

func benchExecute(b *testing.B, c *client.Client, trace, step string) {
	b.Helper()
	if _, err := c.CallTool(context.Background(), mcp.CallToolRequest{Params: mcp.CallToolParams{
		Name:      "execute_step",
		Arguments: map[string]any{"workflow": "db-maintenance", "trace_id": trace, "step": step},
	}}); err != nil {
		b.Fatal(err)
	}
}

// BenchmarkExecuteStepBlocked is a blocked execute_step through the whole MCP
// request path with memory off, so it is the cost of the barrier and the
// protocol alone.
func BenchmarkExecuteStepBlocked(b *testing.B) {
	c := benchClient(b)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchExecute(b, c, "run", "drop_prod_db")
	}
}

// BenchmarkExecuteStepBlockedWithMemory is the same call with memory on, which
// also fingerprints the runbook, records the run and the block, and builds the
// lesson. Each iteration is a new run, as it is when agents come and go.
func BenchmarkExecuteStepBlockedWithMemory(b *testing.B) {
	c := benchClient(b, WithMemory(blocklog.New(), ""))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchExecute(b, c, fmt.Sprintf("run-%d", i), "drop_prod_db")
	}
}

// BenchmarkExecuteStepAllowedWithMemory is a step that passes with memory on.
func BenchmarkExecuteStepAllowedWithMemory(b *testing.B) {
	c := benchClient(b, WithMemory(blocklog.New(), ""))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchExecute(b, c, fmt.Sprintf("run-%d", i), "take_backup")
	}
}

// BenchmarkWorkflowVersion is the runbook fingerprint on its own.
func BenchmarkWorkflowVersion(b *testing.B) {
	wf, err := runbookstore.DBMaintenanceWorkflow()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = workflowVersion(wf)
	}
}
