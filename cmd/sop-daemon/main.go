// Command sop-daemon runs a loopback-only helper that executes shell
// commands on behalf of the SOP web UI (tools/httpserver serves a page that
// posts here after a human approves a proposed command).
//
// Security model, since this endpoint runs shell commands: the only
// meaningful remote threat is a web page in the user's own browser posting
// here cross-origin, because any local process running as this user could
// already execute commands directly without the daemon's help. That threat
// is closed by validating the Origin header against an allowlist and never
// answering with a wildcard CORS header; a browser always attaches Origin
// to a cross-origin POST, so a page on evil.example cannot reach this
// endpoint even though it is reachable at 127.0.0.1. Requests with no
// Origin at all (curl, tests, other local tooling) are allowed through on
// the reasoning above, and can additionally be gated with -token.
package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode"
)

const (
	maxRequestBody = 64 << 10             // 64 KiB: a command line, not a file upload.
	tokenHeader    = "X-SOP-Daemon-Token" //nolint:gosec // header name, not a credential
)

type ExecuteRequest struct {
	// Command is the legacy field: a single string split into argv by
	// splitCommand and exec'd directly, with no shell involved. Pipes,
	// &&/||, redirects, globs, and env expansion are no longer
	// interpreted; they land as literal argument text. Callers that need
	// those should prefer Executable+Args, or pipe/chain client-side.
	Command    string   `json:"command"`
	Executable string   `json:"executable"`
	Args       []string `json:"args"`
}

type ExecuteResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// config holds the resolved security settings for one daemon run.
type config struct {
	allowedOrigins map[string]bool
	token          string
	commandTimeout time.Duration
}

func main() {
	port := flag.Int("port", 9090, "Port to run the local SOP daemon on")
	origins := flag.String("allowed-origins", "http://localhost:8080,http://127.0.0.1:8080",
		"Comma-separated origins allowed to call this daemon from a browser. A request carrying any other Origin is rejected.")
	token := flag.String("token", os.Getenv("SOP_DAEMON_TOKEN"),
		"Optional shared secret; when set, callers must send it in the "+tokenHeader+" header. Defaults to $SOP_DAEMON_TOKEN.")
	timeout := flag.Duration("command-timeout", 60*time.Second, "Maximum wall-clock time a single command may run before it is killed.")
	flag.Parse()

	cfg := &config{
		allowedOrigins: parseOrigins(*origins),
		token:          *token,
		commandTimeout: *timeout,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/execute", cfg.guard(executeHandler(cfg)))

	// Loopback only: this must never be reachable from the network.
	addr := fmt.Sprintf("127.0.0.1:%d", *port)
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}

	log.Printf("Starting SOP Desktop Daemon on %s", addr)
	log.Printf("This daemon executes shell commands. Allowed browser origins: %s", strings.Join(sortedKeys(cfg.allowedOrigins), ", "))
	if cfg.token == "" {
		log.Printf("No -token set: any local process can post commands here (it could also just run them directly). Set -token to require a shared secret.")
	}

	if err := srv.ListenAndServe(); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func parseOrigins(csv string) map[string]bool {
	out := make(map[string]bool)
	for _, o := range strings.Split(csv, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out[o] = true
		}
	}
	return out
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// guard applies the Origin allowlist, the optional token check, and the
// CORS response headers. It deliberately never emits
// Access-Control-Allow-Origin: *, since that wildcard is what would let any page
// on the internet drive this endpoint.
func (c *config) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")

		// Vary matters for any cache sitting in front of this: the response
		// differs per Origin.
		w.Header().Set("Vary", "Origin")

		if origin != "" {
			if !c.allowedOrigins[origin] {
				log.Printf("rejected request from disallowed origin %q", origin)
				http.Error(w, "Forbidden: origin not allowed", http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, "+tokenHeader)
		}

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		if c.token != "" {
			given := r.Header.Get(tokenHeader)
			if subtle.ConstantTimeCompare([]byte(given), []byte(c.token)) != 1 {
				http.Error(w, "Unauthorized: bad or missing "+tokenHeader, http.StatusUnauthorized)
				return
			}
		}

		next(w, r)
	}
}

func executeHandler(cfg *config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var req ExecuteRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBody)).Decode(&req); err != nil {
			respondJSON(w, http.StatusBadRequest, ExecuteResponse{Error: "Invalid JSON body: " + err.Error()})
			return
		}

		if req.Executable == "" && req.Command == "" {
			respondJSON(w, http.StatusBadRequest, ExecuteResponse{Error: "Either 'executable' or 'command' must be provided"})
			return
		}

		var program string
		var args []string

		if req.Executable != "" {
			program = req.Executable
			args = req.Args
		} else {
			// Legacy path: split the command string into program + args
			// to avoid passing raw input through a shell interpreter.
			parts, err := splitCommand(req.Command)
			if err != nil || len(parts) == 0 {
				respondJSON(w, http.StatusBadRequest, ExecuteResponse{Error: "Failed to parse command"})
				return
			}
			program = parts[0]
			args = parts[1:]
		}

		log.Printf("Executing: %s %v", sanitizeLog(program), sanitizeLogArgs(args))

		// Bound the run: a command that never exits would otherwise pin this
		// handler (and its goroutine) forever.
		ctx, cancel := context.WithTimeout(r.Context(), cfg.commandTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, program, args...)

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()

		resp := ExecuteResponse{
			Stdout: stdout.String(),
			Stderr: stderr.String(),
		}

		if err != nil {
			resp.Error = err.Error()
			if exitError, ok := err.(*exec.ExitError); ok {
				resp.ExitCode = exitError.ExitCode()
			} else {
				resp.ExitCode = -1
			}
			if ctx.Err() != nil {
				resp.Error = fmt.Sprintf("command exceeded %s and was killed", cfg.commandTimeout)
			}
			log.Printf("Command finished with error: %v", err)
		} else {
			resp.ExitCode = 0
			log.Printf("Command executed successfully")
		}

		respondJSON(w, http.StatusOK, resp)
	}
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("Error encoding JSON response: %v", err)
	}
}

// sanitizeLog strips characters a caller could use to forge extra log lines
// (newlines, carriage returns) out of a value before it's logged.
func sanitizeLog(s string) string {
	r := strings.NewReplacer("\n", "\\n", "\r", "\\r")
	return r.Replace(s)
}

func sanitizeLogArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = sanitizeLog(a)
	}
	return out
}

// splitCommand splits a shell command string into words, respecting single
// quotes, double quotes, and backslash escapes. This lets callers pass a
// single "command" string without routing it through sh -c.
func splitCommand(s string) ([]string, error) {
	var args []string
	var current []rune
	var quote rune // 0 = unquoted, '\'' or '"' = inside that quote type
	escaped := false

	for _, r := range s {
		if escaped {
			current = append(current, r)
			escaped = false
			continue
		}
		if r == '\\' && quote != '\'' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				current = append(current, r)
			}
			continue
		}
		switch {
		case r == '\'' || r == '"':
			quote = r
		case unicode.IsSpace(r):
			if len(current) > 0 {
				args = append(args, string(current))
				current = current[:0]
			}
		default:
			current = append(current, r)
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unclosed quote")
	}
	if len(current) > 0 {
		args = append(args, string(current))
	}
	return args, nil
}
