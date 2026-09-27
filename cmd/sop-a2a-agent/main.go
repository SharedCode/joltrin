// Command sop-a2a-agent serves tools/a2aagent over HTTP, registering the
// example db-maintenance runbook (tools/runbookstore.DBMaintenanceWorkflow)
// so an A2A client has something real to delegate a task against out of the
// box. Serves the agent card at /.well-known/agent-card.json and the
// JSON-RPC transport at /a2a/invoke.
//
// It binds loopback by default. Executing runbook steps is a privileged
// operation and this agent has no identity model of its own, so exposing it
// beyond this machine is opt-in via -addr, and -token should be set
// whenever it is.
//
//	go run ./cmd/sop-a2a-agent
//	curl localhost:8087/.well-known/agent-card.json
package main

import (
	"crypto/subtle"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sharedcode/joltrin/v5/tools/a2aagent"
	"github.com/sharedcode/joltrin/v5/tools/runbookstore"
)

const invokePath = "/a2a/invoke"

const tokenHeader = "Authorization"

func main() {
	addr := flag.String("addr", "127.0.0.1:8087", "Address to listen on. Loopback by default; set an interface explicitly to expose this agent, and pair it with -token when you do.")
	baseURL := flag.String("base-url", "", "Public base URL advertised in the agent card. Defaults to http://<addr>.")
	token := flag.String("token", os.Getenv("SOP_A2A_TOKEN"), "Optional bearer token required on every request. Defaults to $SOP_A2A_TOKEN.")
	flag.Parse()

	wf, err := runbookstore.DBMaintenanceWorkflow()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sop-a2a-agent:", err)
		os.Exit(1)
	}

	store := runbookstore.New()
	if err := store.RegisterWorkflow("db-maintenance", wf); err != nil {
		fmt.Fprintln(os.Stderr, "sop-a2a-agent:", err)
		os.Exit(1)
	}

	public := *baseURL
	if public == "" {
		public = defaultBaseURL(*addr)
	}

	handler := requireToken(*token, a2aagent.NewMux(store, public, invokePath))

	srv := &http.Server{
		Addr:              *addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	fmt.Printf("sop-a2a-agent listening on %s (agent card: /.well-known/agent-card.json, invoke: %s)\n", *addr, invokePath)
	if *token == "" && !isLoopbackAddr(*addr) {
		fmt.Fprintf(os.Stderr, "sop-a2a-agent: warning: listening on %s with no -token; anyone who can reach this port can execute runbook steps\n", *addr)
	}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Fprintln(os.Stderr, "sop-a2a-agent:", err)
		os.Exit(1)
	}
}

// defaultBaseURL turns a listen address into the URL clients should use.
// A bare ":8087" or a wildcard bind is not dialable as written, so those
// become localhost.
func defaultBaseURL(addr string) string {
	host, port, found := strings.Cut(addr, ":")
	if !found {
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "[::]" {
		host = "localhost"
	}
	return "http://" + host + ":" + port
}

func isLoopbackAddr(addr string) bool {
	host, _, found := strings.Cut(addr, ":")
	if !found {
		return false
	}
	return host == "127.0.0.1" || host == "localhost" || host == "[::1]"
}

// requireToken gates every route, the agent card included, behind a bearer
// token when one is configured. With no token set it is a pass-through, so
// the default loopback experience is unchanged.
func requireToken(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := "Bearer " + token
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		given := r.Header.Get(tokenHeader)
		if subtle.ConstantTimeCompare([]byte(given), []byte(want)) != 1 {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
