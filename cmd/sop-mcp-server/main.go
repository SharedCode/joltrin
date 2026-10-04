// Command sop-mcp-server runs tools/mcpserver over stdio, serving the
// example db-maintenance runbook (tools/runbookstore.DBMaintenanceWorkflow)
// so an MCP client has something real to call read_sop/validate_step/
// execute_step against out of the box.
//
//	go run ./cmd/sop-mcp-server
//
// Run "sop-mcp-server setup" to print the commands that register it with Claude
// Code, Codex, and the Gemini CLI by its full path, or "setup --apply" to run
// them for the agents installed on this machine.
//
// Set SOP_RUNBOOKS to a JSON file to serve your own runbooks instead of the
// example. See runbookstore.LoadFile for the format.
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
	"strings"

	"github.com/mark3labs/mcp-go/server"

	"github.com/sharedcode/joltrin/v5/tools/blocklog"
	"github.com/sharedcode/joltrin/v5/tools/mcpserver"
	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
)

// newStore builds the runbook store. With no file it holds the built-in
// db-maintenance example. With a file (SOP_RUNBOOKS) it holds exactly the
// runbooks in that file, so an agent is not offered the example as well.
func newStore(runbooksPath string) (*runbookstore.Store, []string, error) {
	store := runbookstore.New()
	if runbooksPath != "" {
		names, err := runbookstore.LoadFile(store, runbooksPath)
		if err != nil {
			return nil, nil, err
		}
		return store, names, nil
	}
	wf, err := runbookstore.DBMaintenanceWorkflow()
	if err != nil {
		return nil, nil, err
	}
	if err := store.RegisterWorkflow("db-maintenance", wf); err != nil {
		return nil, nil, err
	}
	return store, []string{"db-maintenance"}, nil
}

func main() {
	// "sop-mcp-server setup" registers this binary with your agent by its full
	// path. Anything else starts the server, which agents launch with no arguments.
	if len(os.Args) > 1 && os.Args[1] == "setup" {
		os.Exit(setupMain(os.Args[2:]))
	}

	store, names, err := newStore(os.Getenv("SOP_RUNBOOKS"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "sop-mcp-server:", err)
		os.Exit(1)
	}
	if os.Getenv("SOP_RUNBOOKS") != "" {
		fmt.Fprintf(os.Stderr, "sop-mcp-server: loaded runbooks: %s\n", strings.Join(names, ", "))
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
