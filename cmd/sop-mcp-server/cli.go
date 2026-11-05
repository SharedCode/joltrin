package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
)

const usageText = `sop-mcp-server runs the Joltrin verification barrier as an MCP server.

Usage:
  sop-mcp-server                       serve over stdio, which is how an agent launches it
  sop-mcp-server http [--addr ADDR]    serve over MCP streamable HTTP for one team (default 127.0.0.1:8088, path /mcp)
  sop-mcp-server setup [--apply]       register it with Claude Code, Codex and the Gemini CLI
                       [--lessons DIR] [--runbooks FILE]
  sop-mcp-server check [--json] [--diagnose] FILE
                                       show rules; --diagnose finds runnable order and blocked steps
  sop-mcp-server try [--json] FILE WORKFLOW STEP...
                                       replay steps against a runbook and see which the barrier blocks
  sop-mcp-server gate [--json] [--state-dir DIR] [--reset] FILE WORKFLOW SESSION [STEP]
                                       ask about one step and remember it between calls, for hooks and scripts
  sop-mcp-server demo [--json]         watch the barrier block a database drop until a backup is validated
  sop-mcp-server version               print the version
  sop-mcp-server help                  print this text

Environment:
  SOP_RUNBOOKS     JSON file with your own runbooks (default: the built-in db-maintenance example)
  SOP_LESSONS_DIR  folder that turns on memory of earlier blocks
  SOP_METRICS_ADDR address such as 127.0.0.1:9464 that serves Prometheus metrics on /metrics
  SOP_DECISION_LOG "stderr" or a file path: one JSON line per execute_step decision
  SOP_MCP_TOKEN    bearer token for the http command, at least 16 characters if --addr is not loopback
  SOP_RETRY_LIMIT  after this many blocks of one step in a trace, tell the agent to stop and ask

Exit codes: 0 success, 1 failure, 2 usage error.
`

// version reports the module version the binary was built from, which is the
// release tag for a published binary or for go install, and "dev" for a build
// from a working copy.
func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}

// servingHint tells someone who ran the bare command in a terminal that the
// server is waiting for a client, not hung. An agent launches it with a pipe on
// stdin, so it never sees this line, and stdout stays clear for the protocol.
func servingHint(stdin *os.File, errw io.Writer) {
	fi, err := stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}
	fmt.Fprintln(errw, "sop-mcp-server: waiting for an MCP client on stdin. Press Ctrl-C to stop, or run \"sop-mcp-server help\" for the commands.")
}

// dispatch runs a command and reports the exit code. serve is true when the
// arguments ask for the server itself, in which case the caller starts it. An
// unknown word is a usage error and never starts a server, because a typo that
// silently starts one just sits waiting on stdin.
func dispatch(args []string, out, errw io.Writer) (code int, serve bool) {
	if len(args) == 0 {
		return 0, true
	}
	switch args[0] {
	case "stdio", "serve":
		return 0, true
	case "http":
		return 0, true
	case "help", "-h", "--help":
		fmt.Fprint(out, usageText)
		return 0, false
	case "version", "-v", "--version":
		fmt.Fprintf(out, "sop-mcp-server %s\n", version())
		return 0, false
	case "setup":
		return setupMain(args[1:], out, errw), false
	case "check":
		return runCheck(args[1:], out, errw), false
	case "try":
		return runTry(args[1:], out, errw), false
	case "gate":
		return runGate(args[1:], out, errw), false
	case "demo":
		return runDemoArgs(args[1:], out, errw), false
	}
	fmt.Fprintf(errw, "sop-mcp-server: unknown command %q\nRun \"sop-mcp-server help\" for the commands.\n", args[0])
	return 2, false
}

// popFlag removes every occurrence of flag from args and reports whether it was
// there, so a flag works before or after the file name.
func popFlag(args []string, flag string) (rest []string, found bool) {
	for _, a := range args {
		if a == flag {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return rest, found
}

func isHelp(args []string) bool {
	for _, a := range args {
		if a == "-h" || a == "--help" || a == "help" {
			return true
		}
	}
	return false
}
