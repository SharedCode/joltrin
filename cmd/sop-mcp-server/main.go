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
// Run "sop-mcp-server demo" to watch the barrier block a database drop until a
// backup is validated, with no agent and no Go needed.
//
// Set SOP_RUNBOOKS to a JSON file to serve your own runbooks instead of the
// example. See runbookstore.LoadFile for the format. Run
// "sop-mcp-server check runbooks.json" to see what a file enforces, or why it
// does not load, before an agent uses it.
//
// Set SOP_LESSONS_DIR to a directory to turn on memory: the server records
// which steps were blocked in each run, tells later agents about them when
// they connect, and keeps a LESSONS.md in that directory that can be added to
// a CLAUDE.md or AGENTS.md. It is off by default.
package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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
	// Anything but the bare command or "stdio" is handled before a server
	// starts. An unknown word is a usage error, not a silent server.
	if code, serve := dispatch(os.Args[1:], os.Stdout, os.Stderr); !serve {
		os.Exit(code)
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
		if err := log.CompactionError(); err != nil {
			fmt.Fprintln(os.Stderr, "sop-mcp-server: the block log could not be compacted and will keep growing until it can:", err)
		}
		opts = append(opts, mcpserver.WithMemory(log, filepath.Join(dir, "LESSONS.md")))
	}

	obsOpts, closeObs, err := observability(os.Getenv, os.Stderr)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sop-mcp-server:", err)
		os.Exit(1)
	}
	defer closeObs()
	opts = append(opts, obsOpts...)

	mcp := mcpserver.New(store, opts...)
	if len(os.Args) > 1 && os.Args[1] == "http" {
		if code := runHTTP(mcp, os.Args[2:]); code != 0 {
			closeObs()
			os.Exit(code)
		}
		return
	}

	servingHint(os.Stdin, os.Stderr)
	if err := server.ServeStdio(mcp); err != nil {
		fmt.Fprintln(os.Stderr, "sop-mcp-server:", err)
		os.Exit(1)
	}
}

// observability turns SOP_MODE, SOP_METRICS_ADDR, SOP_DECISION_LOG and SOP_RETRY_LIMIT
// into server options. The decision log never goes to stdout, which carries
// the MCP protocol.
func observability(getenv func(string) string, errw io.Writer) ([]mcpserver.Option, func(), error) {
	var opts []mcpserver.Option
	var closers []func()
	closeAll := func() {
		for _, c := range closers {
			c()
		}
	}

	switch v := getenv("SOP_MODE"); v {
	case "", "enforce":
	case "observe":
		fmt.Fprintln(errw, "sop-mcp-server: SOP_MODE=observe, NOTHING IS BLOCKED. Steps that would be blocked run anyway and are logged as observed. Unset SOP_MODE to enforce.")
		opts = append(opts, mcpserver.WithObserveOnly())
	default:
		return nil, closeAll, fmt.Errorf("SOP_MODE must be observe or enforce, got %q", v)
	}

	if v := getenv("SOP_RETRY_LIMIT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, closeAll, fmt.Errorf("SOP_RETRY_LIMIT must be a whole number of 1 or more, got %q", v)
		}
		opts = append(opts, mcpserver.WithRetryLimit(n))
	}

	if v := getenv("SOP_DECISION_LOG"); v != "" {
		w := errw
		if v != "stderr" {
			f, err := os.OpenFile(v, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err != nil {
				return nil, closeAll, fmt.Errorf("SOP_DECISION_LOG: %w", err)
			}
			closers = append(closers, func() { f.Close() })
			w = f
		}
		opts = append(opts, mcpserver.WithDecisionLog(w))
	}

	if addr := getenv("SOP_METRICS_ADDR"); addr != "" {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			closeAll()
			return nil, func() {}, fmt.Errorf("SOP_METRICS_ADDR: %w", err)
		}
		m := mcpserver.NewMetrics()
		mux := http.NewServeMux()
		mux.Handle("/metrics", m.Handler())
		srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				fmt.Fprintln(errw, "sop-mcp-server: the metrics server stopped:", err)
			}
		}()
		closers = append(closers, func() { srv.Close() })
		fmt.Fprintf(errw, "sop-mcp-server: metrics on http://%s/metrics\n", ln.Addr())
		if tcp, ok := ln.Addr().(*net.TCPAddr); ok && !tcp.IP.IsLoopback() {
			fmt.Fprintln(errw, "sop-mcp-server: warning: the metrics address is not loopback, so anything that can reach it can read the metrics")
		}
		opts = append(opts, mcpserver.WithMetrics(m))
	}
	return opts, closeAll, nil
}
