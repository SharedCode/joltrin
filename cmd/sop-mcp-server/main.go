// Command sop-mcp-server runs tools/mcpserver over stdio, serving the
// example db-maintenance runbook (tools/runbookstore.DBMaintenanceWorkflow)
// so an MCP client has something real to call read_sop/validate_step/
// execute_step against out of the box.
//
//	go run ./cmd/sop-mcp-server
package main

import (
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"

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

	if err := server.ServeStdio(mcpserver.New(store)); err != nil {
		fmt.Fprintln(os.Stderr, "sop-mcp-server:", err)
		os.Exit(1)
	}
}
