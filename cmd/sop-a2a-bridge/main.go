// Command sop-a2a-bridge runs tools/a2abridge over stdio: an MCP server
// that resolves a running sop-a2a-agent's card and translates Claude's
// execute_step tool calls into real A2A task delegation against it. Use
// this when the runbook you want an MCP client to drive is served by a
// remote A2A agent rather than sop-mcp-server itself.
//
//	go run ./cmd/sop-a2a-agent &
//	go run ./cmd/sop-a2a-bridge -agent-url http://localhost:8087
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/mark3labs/mcp-go/server"

	"github.com/sharedcode/joltrin/v5/tools/a2abridge"
)

func main() {
	agentURL := flag.String("agent-url", "http://localhost:8087", "Base URL of the A2A agent to bridge to (its agent card is fetched from <agent-url>/.well-known/agent-card.json).")
	flag.Parse()

	srv, err := a2abridge.New(context.Background(), *agentURL)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sop-a2a-bridge:", err)
		os.Exit(1)
	}

	if err := server.ServeStdio(srv); err != nil {
		fmt.Fprintln(os.Stderr, "sop-a2a-bridge:", err)
		os.Exit(1)
	}
}
