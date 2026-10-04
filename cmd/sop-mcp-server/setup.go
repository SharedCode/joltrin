package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// setup registers this binary with an agent. It uses the binary's own full
// path, so it keeps working when Go's bin folder is not on the PATH the agent
// starts with, which is the usual reason a bare "sop-mcp-server" fails to
// launch with "Executable not found".

type agentCLI struct {
	name string // shown to the user
	cli  string // the command that registers a server
	// args builds the registration command. env is a list of KEY=VALUE pairs for
	// the server and may be empty.
	args func(exe string, env []string) []string
}

var agentCLIs = []agentCLI{
	{"Claude Code", "claude", func(exe string, env []string) []string {
		a := []string{"mcp", "add", "--scope", "user", "joltrin"}
		for _, kv := range env {
			a = append(a, "-e", kv)
		}
		return append(a, "--", exe)
	}},
	{"Codex", "codex", func(exe string, env []string) []string {
		a := []string{"mcp", "add", "joltrin"}
		for _, kv := range env {
			a = append(a, "--env", kv)
		}
		return append(a, "--", exe)
	}},
	// The Gemini CLI takes its environment as a repeated option that can swallow
	// the arguments after it, so the server's settings go in its settings file.
	{"Gemini CLI", "gemini", func(exe string, _ []string) []string {
		return []string{"mcp", "add", "--scope", "user", "joltrin", exe}
	}},
}

// shellQuote leaves a plain path alone and single-quotes anything else.
func shellQuote(s string) string {
	safe := s != ""
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_/.:=@%+-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func commandLine(cli string, args []string) string {
	parts := []string{cli}
	for _, a := range args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

// runSetup implements "sop-mcp-server setup". It returns the process exit code.
func runSetup(args []string, out, errw io.Writer, exe string,
	lookPath func(string) (string, error), run func(name string, args ...string) error) int {

	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(errw)
	apply := fs.Bool("apply", false, "register with the agent CLIs found on this machine")
	lessons := fs.String("lessons", "", "folder for the memory of earlier blocks (sets SOP_LESSONS_DIR)")
	runbooks := fs.String("runbooks", "", "JSON file with your own runbooks (sets SOP_RUNBOOKS)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// "go run" builds a throwaway binary that is deleted afterwards, so a path to
	// it would break the moment the run ends.
	if strings.Contains(filepath.ToSlash(exe), "/go-build") {
		fmt.Fprintln(errw, "setup: this is a temporary build from go run, so its path will not last.")
		fmt.Fprintln(errw, "Install it first, then run setup from the installed binary:")
		fmt.Fprintln(errw, `  go install github.com/sharedcode/joltrin/v5/cmd/sop-mcp-server@latest`)
		fmt.Fprintln(errw, `  "$(go env GOPATH)/bin/sop-mcp-server" setup --apply`)
		return 1
	}

	var env []string
	if *lessons != "" {
		env = append(env, "SOP_LESSONS_DIR="+*lessons)
	}
	if *runbooks != "" {
		path := *runbooks
		if abs, err := filepath.Abs(path); err == nil {
			path = abs // an agent starts the server from any folder
		}
		env = append(env, "SOP_RUNBOOKS="+path)
	}

	fmt.Fprintf(out, "Joltrin MCP server: %s\n\n", exe)

	if !*apply {
		fmt.Fprintln(out, "Register it with your agent. These use the full path, so they work even when")
		fmt.Fprintln(out, "Go's bin folder is not on your PATH:")
		fmt.Fprintln(out)
		for _, a := range agentCLIs {
			fmt.Fprintf(out, "  %-12s %s\n", a.name, commandLine(a.cli, a.args(exe, env)))
		}
		fmt.Fprintf(out, "\nOr register it with the ones installed here: %s setup --apply\n", shellQuote(exe))
		return 0
	}

	failed, registered := false, 0
	for _, a := range agentCLIs {
		if _, err := lookPath(a.cli); err != nil {
			fmt.Fprintf(out, "  %-12s %s is not installed here, skipped\n", a.name, a.cli)
			continue
		}
		if err := run(a.cli, a.args(exe, env)...); err != nil {
			fmt.Fprintf(out, "  %-12s failed: %v\n", a.name, err)
			failed = true
			continue
		}
		fmt.Fprintf(out, "  %-12s registered\n", a.name)
		registered++
	}
	switch {
	case failed:
		fmt.Fprintln(out, "\nSome registrations failed. Run without --apply to see the commands and run them yourself.")
		return 1
	case registered == 0:
		fmt.Fprintln(out, "\nNo agent CLI found. Run without --apply to see the commands.")
		return 1
	}
	fmt.Fprintln(out, "\nRestart your agent, or reconnect the server from its MCP menu.")
	return 0
}

// setupMain wires runSetup to the real environment.
func setupMain(args []string) int {
	exe, err := os.Executable()
	if err == nil {
		if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
			exe = resolved
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "setup: cannot find this binary's path:", err)
		return 1
	}
	run := func(name string, args ...string) error {
		cmd := exec.Command(name, args...)
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		return cmd.Run()
	}
	return runSetup(args, os.Stdout, os.Stderr, exe, exec.LookPath, run)
}
