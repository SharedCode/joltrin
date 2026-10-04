// Command sop-mcp-server runs tools/mcpserver over stdio, serving the
// example db-maintenance runbook (tools/runbookstore.DBMaintenanceWorkflow)
// so an MCP client has something real to call read_sop/validate_step/
// execute_step against out of the box.
//
//	go run ./cmd/sop-mcp-server
//
// Set SOP_LESSONS_DIR to a directory to turn on memory: the server records
// which steps were blocked in each run, tells later agents about them when
// they connect, and keeps a LESSONS.md in that directory that can be added to
// a CLAUDE.md or AGENTS.md. It is off by default.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mark3labs/mcp-go/server"

	"github.com/sharedcode/joltrin/v5/tools/blocklog"
	"github.com/sharedcode/joltrin/v5/tools/mcpserver"
	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
)

func main() {
	wf, err := runbookstore.DBMaintenanceWorkflow()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sop-mcp-server:", err)
		os.Exit(1)
	}

	store := runbookstore.New()
	if err := store.RegisterWorkflow("db-maintenance", wf); err != nil {
		fmt.Fprintln(os.Stderr, "sop-mcp-server:", err)
		os.Exit(1)
	}

	var opts []mcpserver.Option
	if dir := os.Getenv("SOP_LESSONS_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "sop-mcp-server:", err)
			os.Exit(1)
		}
		log, err := blocklog.Open(filepath.Join(dir, "blocks.jsonl"))
		if err != nil {
			fmt.Fprintln(os.Stderr, "sop-mcp-server:", err)
			os.Exit(1)
		}
		defer log.Close()
		opts = append(opts, mcpserver.WithMemory(log, filepath.Join(dir, "LESSONS.md")))
	}

	if err := server.ServeStdio(mcpserver.New(store, opts...)); err != nil {
		fmt.Fprintln(os.Stderr, "sop-mcp-server:", err)
		os.Exit(1)
	}
}
