package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"runtime"
	"strings"

	log "log/slog"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent"
	"github.com/sharedcode/joltrin/internal/logsafe"
)

// ExecuteScriptRequest defines the payload for executing a script.
type ExecuteScriptRequest struct {
	Name     string         `json:"name"`
	Category string         `json:"category"`
	Args     map[string]any `json:"args"`
	Agent    string         `json:"agent"` // Optional: specify which agent to use
}

// handleExecuteScript handles the POST /api/scripts/execute endpoint.
func handleExecuteScript(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ExecuteScriptRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid JSON body: %v", err), http.StatusBadRequest)
		return
	}

	if req.Name == "" {
		http.Error(w, "Script name is required", http.StatusBadRequest)
		return
	}

	if req.Category == "" {
		req.Category = "general"
	}

	// Determine which agent to use
	agentID := req.Agent
	if agentID == "" {
		agentID = "copilot" // Default to copilot as it's the main one loaded
	}

	agentIntf, exists := loadedAgents[agentID]
	if !exists {
		// Fallback: try to find any loaded agent if the default one isn't there
		if len(loadedAgents) > 0 {
			for k, v := range loadedAgents {
				agentID = k
				agentIntf = v
				break
			}
		} else {
			http.Error(w, fmt.Sprintf("Agent '%s' not found and no agents loaded", agentID), http.StatusInternalServerError)
			return
		}
	}

	// Type assert to *agent.Service to access PlayScript
	// Note: loadedAgents is map[string]ai.Agent[map[string]any]
	// We need to check if the underlying implementation is *agent.Service
	agentSvc, ok := agentIntf.(*agent.Service)
	if !ok {
		http.Error(w, fmt.Sprintf("Agent '%s' does not support script execution (not an *agent.Service)", agentID), http.StatusInternalServerError)
		return
	}

	// Create context with session payload
	ctx := r.Context()
	payload := &ai.SessionPayload{
		CurrentDB:   "system", // Default to system or make configurable
		AvatarScope: "",       // System scripts do not run in Avatar Mode
	}
	ctx = context.WithValue(ctx, agent.SessionPayloadKey, payload)
	if rs := agentSvc.RunnerSession(); rs != nil {
		ctx = context.WithValue(ctx, agent.RunnerSessionKey, rs)
	}

	// Inject ScriptRecorder into context so executed steps are recorded in session state
	// This enables /last-tool to work after running a script.
	ctx = context.WithValue(ctx, ai.CtxKeyScriptRecorder, agentSvc)

	// Set headers for streaming
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Transfer-Encoding", "chunked")
	// We don't strictly need to set status 200 here, the first Write will do it.
	// But it's good to be explicit if we haven't encountered an error yet.

	// Use PlayScript with the response writer
	// PlayScript writes JSON chunks to w
	if err := agentSvc.PlayScript(ctx, req.Name, req.Category, req.Args, w); err != nil {
		// If an error occurs during streaming, we can't easily change the HTTP status code
		// if headers have already been sent (which happens on the first Write).
		// However, PlayScript is expected to write error details to the stream if possible,
		// or we can log it here.
		// Since we are streaming JSON, appending a JSON error object might be invalid if the stream is mid-object.
		// For now, we just log it.
		fmt.Printf("Error executing script '%s': %v\n", logsafe.V(req.Name), logsafe.V(err))
	}
}

// withAuth is a middleware that optionally enforces Bearer token authentication.
func handlerName(next http.HandlerFunc) string {
	if next == nil {
		return "<nil>"
	}
	if fn := runtime.FuncForPC(reflect.ValueOf(next).Pointer()); fn != nil {
		return fn.Name()
	}
	return "unknown-handler"
}

// isLoopbackRequest reports whether the request arrived from this machine.
// It reads only RemoteAddr, deliberately ignoring X-Forwarded-For and
// friends: those are attacker-controlled unless a trusted proxy rewrites
// them, and treating them as authoritative here would hand back the exact
// bypass this check exists to close.
func isLoopbackRequest(r *http.Request) bool {
	host := r.RemoteAddr
	if h, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		host = h
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	return ip != nil && ip.IsLoopback()
}

func withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Debug("withAuth: handler invocation", "handler", handlerName(next), "method", r.Method, "path", logsafe.V(r.URL.Path))
		// First-run bootstrap: before a config file exists there are no
		// users to authenticate against, so the setup flow has to be
		// reachable unauthenticated. Restrict that window to loopback
		// callers. Without this check the same window left every /api/*
		// route, including store deletion and local command execution, open
		// to anyone who could reach an unconfigured server over the network.
		if !hasConfigFileOnDisk() {
			if !isLoopbackRequest(r) {
				log.Warn("withAuth: unauthenticated request denied", "path", logsafe.V(r.URL.Path), "method", r.Method,
					"reason", "server is unconfigured and the caller is not on loopback", "remote_addr", r.RemoteAddr)
				http.Error(w, "Unauthorized: server is not configured yet; complete first-run setup from the local machine", http.StatusUnauthorized)
				return
			}
			log.Debug("withAuth: skipping bearer-token validation", "path", logsafe.V(r.URL.Path), "method", r.Method, "reason", "first-run setup from loopback, no config file on disk")
			next(w, r)
			return
		}

		log.Debug("withAuth: enforcing bearer-token validation", "path", logsafe.V(r.URL.Path), "method", r.Method)

		token := authTokenFromRequest(r)
		hasHeader := r.Header.Get("Authorization") != ""
		cookie, errCookie := r.Cookie("sop_access_token")
		hasCookie := errCookie == nil && strings.TrimSpace(cookie.Value) != ""
		log.Debug("withAuth: checking bearer token", "path", logsafe.V(r.URL.Path), "has_header", hasHeader, "has_cookie", hasCookie)
		if token == "" {
			log.Warn("withAuth: unauthenticated request denied", "path", logsafe.V(r.URL.Path), "reason", "missing bearer token", "method", r.Method)
			http.Error(w, "Unauthorized: Missing bearer token", http.StatusUnauthorized)
			return
		}

		log.Debug("withAuth: executing token validation", "path", logsafe.V(r.URL.Path), "token_prefix", logsafe.V(tokenPrefix(token)), "header_present", hasHeader)
		ok, user, err := config.AuthenticateBearerToken(r.Context(), token)
		if err != nil || !ok {
			reason := "invalid or expired session token"
			if err != nil {
				reason = strings.ToLower(err.Error())
			}
			log.Warn("withAuth: unauthenticated request denied", "path", logsafe.V(r.URL.Path), "reason", reason, "error", logsafe.V(err), "ok", ok, "method", r.Method, "token_prefix", logsafe.V(tokenPrefix(token)))
			http.Error(w, "Unauthorized: Invalid token", http.StatusUnauthorized)
			return
		}
		log.Debug("withAuth: bearer token accepted", "path", logsafe.V(r.URL.Path), "user", logsafe.V(user.Username), "role", logsafe.V(user.Role))

		if ok, user, err := config.AuthenticateBearerToken(r.Context(), token); err == nil && ok {
			auth := sop.AuthContext{
				IsSystem: true,
				Roles:    []string{user.Role},
				UserID:   user.Username,
			}
			ctx := sop.ContextWithAuth(r.Context(), auth)
			r = r.WithContext(ctx)
		}
		// Let the script execution logic handle final auth decisions.
		next(w, r)
	}
}
