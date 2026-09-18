package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	log "log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/agent"
	aidb "github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/embed"

	_ "github.com/sharedcode/joltrin/ai/generator"
	"github.com/sharedcode/joltrin/ai/memory"
	"github.com/sharedcode/joltrin/ai/obfuscation"
	"github.com/sharedcode/joltrin/btree"
	"github.com/sharedcode/joltrin/database"
	"github.com/sharedcode/joltrin/internal/logsafe"
)

// ObfuscationMode defines the global obfuscation policy.
type ObfuscationMode string

const (
	// ObfuscationDisabled means no obfuscation globally.
	ObfuscationDisabled ObfuscationMode = "disabled"
	// ObfuscationPerDatabase means we respect obfuscation flag per database.
	ObfuscationPerDatabase ObfuscationMode = "per_database"
	// ObfuscationAllDatabases means we enforce obfuscation globally.
	ObfuscationAllDatabases ObfuscationMode = "all_databases"
)

// DirectFlushingWriter writes directly to the http.ResponseWriter and flushes
type DirectFlushingWriter struct {
	w http.ResponseWriter
}

func (d *DirectFlushingWriter) Write(p []byte) (n int, err error) {
	d.w.Header().Set("Content-Type", "application/x-ndjson")
	n, err = d.w.Write(p)
	if f, ok := d.w.(http.Flusher); ok {
		f.Flush()
	}
	return
}

var seededKBs sync.Map

func seedMetaCognitionAsync(userID, kbName string, sysOpts sop.DatabaseOptions) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, loaded := seededKBs.LoadOrStore(kbName, true)
	if loaded {
		return // Already seeding or seeded this KB
	}

	sysDB := aidb.NewDatabase(sysOpts)

	trans, err := sysDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return
	}

	embedder := GetConfiguredEmbedder(nil)
	kb, err := sysDB.OpenKnowledgeBase(ctx, kbName, trans, nil, embedder, false, true)
	if err != nil {
		trans.Rollback(ctx)
		return
	}
	res, _ := kb.Search(ctx, []memory.SearchRequest[map[string]any]{{Text: "Meta_Cognition", Limit: 1}})
	trans.Rollback(ctx)

	if len(res) > 0 && len(res[0]) > 0 {
		return
	}

	wTrans, err := sysDB.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		return
	}
	defer wTrans.Rollback(ctx)
	wKb, err := sysDB.OpenKnowledgeBase(ctx, kbName, wTrans, nil, embedder, false, true)
	if err != nil {
		return
	}

	thoughtText := "Meta-Memory Rules: 1) Generalize bugs instead of memorizing stack traces. 2) Never duplicate information available in SOP. 3) Prioritize specific user preferences over generic defaults."
	vecs, err := embed.DocumentTexts(ctx, embedder, []string{thoughtText})
	if err == nil && len(vecs) > 0 {
		data := map[string]any{
			"type":        "meta_rule",
			"description": "Base rules for Long Term Memory consolidation",
		}
		_ = wKb.IngestThought(ctx, thoughtText, "Meta_Cognition", "system", vecs[0], data)
		if wTrans.Commit(ctx) == nil {
			log.Info("Successfully seeded lightweight Meta_Cognition to LTM", "user_id", logsafe.V(userID))
		}
	}
}

// handleAIChat manages the HTTP UI integration for the AI Agent.
//
// Step-by-Step Flow:
// 1. Request Initialization: Validates HTTP POST, reads raw body, and unmarshals JSON to extract Message, SessionID, UserID, Agent, etc.
// 2. Stream Setup: Initializes an NDJSON event writer (sendEvent) that flushes backend progress (logs, content, records) dynamically to the UI.
// 3. Context & Agent Resolution: Defaults to "copilot" if no agent specified, verifies agent exists, and handles zero-day session bootstrapping via UUID.
// 4. Session Mutex Locking: Isolates and locks the active session via activeSessions.GetOrCreate(), applying Agent.Clone() to ensure thread safety.
// 5. Payload Construction: Determines the 'AvatarScope' based on selected knowledge bases, bundles DB contexts, and anchors security via GlobalObfuscator.
// 6. Agent Execution Lifecycle: Uses `agentSvc.Open()` to bind physical memory contexts natively and asynchronously fires the `seedMetaCognitionAsync` task.
// 7. RAG Execution: Delegates the complex query processing to `agentSvc.Ask()`.
// 8. Output Interpretation (Heuristics): Scans response strings for required actions, valid JSON array/object structures, or NDJSON Script streams, rendering them gracefully as distinct UI records or standard MarkDown messages.
func handleAIChat(w http.ResponseWriter, r *http.Request) {
	req, err := initializeRequest(w, r)
	if err != nil {
		return
	}

	sendEvent, hasStreamedContent := setupStream(w)

	blueprint, err := resolveContextAndAgent(r.Context(), req, sendEvent)
	if err != nil {
		return
	}

	agentSvc, sessionMu := lockSession(req, blueprint)
	sessionMu.Lock()
	defer sessionMu.Unlock()
	log.Info("HTTP AI Chat Request",
		"session_id", logsafe.V(req.SessionID),
		"agent", logsafe.V(req.Agent),
		"agent_type", fmt.Sprintf("%T", agentSvc),
		"provider", logsafe.V(req.Provider),
		"database", logsafe.V(req.Database),
		"domain", logsafe.V(req.Domain),
		"selected_kb_count", len(req.SelectedKBs),
		"message_chars", len(req.Message),
	)

	sendEvent("session_id", req.SessionID)

	var rs *agent.RunnerSession
	if svc, ok := agentSvc.(*agent.Service); ok && svc != nil {
		rs = svc.RunnerSession()
	}

	llmSettings := resolveLLMSettings(r)
	ctx, payload, cfg, fullMessage := constructPayload(r.Context(), w, req, llmSettings, sendEvent, rs)
	log.Info("HTTP AI Chat Payload",
		"session_id", logsafe.V(req.SessionID),
		"current_db", logsafe.V(payload.CurrentDB),
		"active_domain", logsafe.V(payload.ActiveDomain),
		"avatar_scope", logsafe.V(payload.AvatarScope),
		"selected_kbs", len(payload.SelectedKBs),
		"full_message_chars", len(fullMessage),
	)

	// Inject streaming writer bypass
	ctx = context.WithValue(ctx, ai.CtxKeyWriter, &DirectFlushingWriter{w: w})
	ctx = context.WithValue(ctx, ai.CtxKeyAppBaseURL, buildAppBaseURL(r))

	if err := executeAgentLifecycle(ctx, r, agentSvc, req, sendEvent); err != nil {
		return
	}

	// Ensure Close is ALWAYS called, even on panic or context cancellation
	defer func() {
		if r := recover(); r != nil {
			log.Error("Panic during agent execution", "panic", r, "session_id", logsafe.V(req.SessionID))
			// Use background context since original may be canceled
			if err := agentSvc.Close(context.Background()); err != nil {
				log.Error(logsafe.V(fmt.Sprintf("Agent '%s' failed to close after panic: %v", req.Agent, err)))
			}
			sendEvent("error", fmt.Sprintf("Internal error: %v", r))
			panic(r) // Re-throw after cleanup
		}
		// Normal close - use original context to allow proper commit/rollback decisions
		if err := agentSvc.Close(ctx); err != nil {
			log.Error(logsafe.V(fmt.Sprintf("Agent '%s' failed to close session: %v", req.Agent, err)))
		}
	}()

	response, err := executeRAG(ctx, agentSvc, req, fullMessage, cfg, sendEvent)
	if err != nil {
		// Check if error is due to context cancellation
		if ctx.Err() != nil {
			log.Warn("Request canceled by client", "session_id", logsafe.V(req.SessionID), "error", ctx.Err())
			sendEvent("error", "Request canceled")
		}
		return
	}
	log.Info("HTTP AI Chat Response",
		"session_id", logsafe.V(req.SessionID),
		"response_chars", len(response),
	)

	// When the event stream already sent assistant content, calling interpretOutput
	// would duplicate the final answer. Only run it for the path where no content
	// has been flushed yet.
	if !hasStreamedContent() {
		interpretOutput(response, sendEvent)
	}
}

type aiChatRequest struct {
	Message     string                 `json:"message"`
	Database    string                 `json:"database"`
	StoreName   string                 `json:"store"`
	Agent       string                 `json:"agent"`
	Provider    string                 `json:"provider"`
	Format      string                 `json:"format"`
	SessionID   string                 `json:"session_id"`
	UserID      string                 `json:"user_id"`
	ClientID    string                 `json:"client_id"`
	Domain      string                 `json:"domain"`
	SelectedKBs []ai.ArtifactReference `json:"selected_kbs"`
}

func buildAppBaseURL(r *http.Request) string {
	if r == nil {
		return ""
	}

	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = strings.TrimSpace(r.Host)
	}
	if host == "" {
		return ""
	}

	proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if proto == "" {
		if r.TLS != nil {
			proto = "https"
		} else {
			proto = "http"
		}
	}

	return strings.TrimRight(proto+"://"+host, "/")
}

func initializeRequest(w http.ResponseWriter, r *http.Request) (*aiChatRequest, error) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return nil, fmt.Errorf("method not allowed")
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusInternalServerError)
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	log.Debug("Received AIChat Request", "body", logsafe.V(string(bodyBytes)))

	var req aiChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Error("Invalid JSON body", "error", err)
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return nil, err
	}

	req.Database = strings.TrimSpace(req.Database)
	req.StoreName = strings.TrimSpace(req.StoreName)
	if authUserID := strings.TrimSpace(sop.GetAuthFromContext(r.Context()).UserID); authUserID != "" {
		req.UserID = authUserID
	}
	return &req, nil
}

func isStructuredToolResult(tool string) bool {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "execute_script":
		return true
	default:
		return false
	}
}

func renderVisibleText(s string) string {
	if strings.Contains(s, `\u`) {
		var decoded string
		if err := json.Unmarshal([]byte("\""+strings.ReplaceAll(s, `"`, `\"`)+"\""), &decoded); err == nil {
			s = decoded
		}
	}
	return html.UnescapeString(s)
}

func isReducerSamplePayload(s string) bool {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return false
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return false
	}

	switch v := decoded.(type) {
	case []any:
		for _, item := range v {
			if rec, ok := item.(map[string]any); ok {
				if _, hasNotice := rec["_result_reducer_notice"]; hasNotice {
					return true
				}
			}
		}
	case map[string]any:
		_, hasNotice := v["_result_reducer_notice"]
		return hasNotice
	}

	return false
}

// setupStream initialises the NDJSON event writer for an AI chat response.
// It returns:
//   - sendEvent: the function to call for each event
//   - hasStreamedContent: returns true once any assistant text was sent via the
//     event stream (delta or non-streaming). Used by handleAIChat to decide
//     whether interpretOutput should re-emit the same text.
func setupStream(w http.ResponseWriter) (func(string, any), func() bool) {
	var contentStreamed bool

	writeEvent := func(eventType string, payload any) {
		switch eventType {
		case "record", "records", "preview", "result_stream":
			contentStreamed = true
		}

		if eventType == "records" || eventType == "record" || eventType == "preview" {
			b, _ := json.Marshal(payload)
			log.Debug("UI: Sending Payload (WIRE)", "type", eventType, "json_preview", string(b))
		} else if eventType == "content" {
			s := fmt.Sprintf("%v", payload)
			log.Debug("UI: Sending Content", "length", len(s))
		}

		w.Header().Set("Content-Type", "application/x-ndjson")

		log.Debug("UI: writing NDJSON event", "type", eventType, "payload_type", fmt.Sprintf("%T", payload), "payload_preview", fmt.Sprintf("%v", payload))
		if err := json.NewEncoder(w).Encode(map[string]any{
			"type":    eventType,
			"payload": payload,
		}); err != nil {
			log.Error("Failed to encode NDJSON event", "error", err)
			return
		}

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}

	sendEvent := func(eventType string, payload any) {
		switch eventType {
		case ai.ReasoningEventAssistantMessage:
			if message, ok := payload.(map[string]any); ok {
				if streaming, _ := message["streaming"].(bool); streaming {
					return
				}
				if source, _ := message["source"].(string); strings.EqualFold(strings.TrimSpace(source), "reasoning_summary") {
					return
				}
				if phase, _ := message["phase"].(string); strings.EqualFold(strings.TrimSpace(phase), "commentary") {
					return
				}
				if text, ok := message["text"].(string); ok && strings.TrimSpace(text) != "" {
					contentStreamed = true
					writeEvent("content", text)
					return
				}
			}
		case ai.ReasoningEventToolCall:
			if toolCall, ok := payload.(map[string]any); ok {
				if tool, _ := toolCall["tool"].(string); isStructuredToolResult(tool) {
					return
				}
			}
		case ai.ReasoningEventToolError:
			// Stream tool errors immediately so user can see validation failures in real-time
			if errorEvent, ok := payload.(map[string]any); ok {
				if errorMsg, _ := errorEvent["error"].(string); strings.TrimSpace(errorMsg) != "" {
					contentStreamed = true
					// Format error for display
					tool, _ := errorEvent["tool"].(string)
					iteration, _ := errorEvent["iteration"].(int)
					var msg string
					if iteration > 0 {
						msg = fmt.Sprintf("⚠️ Error (iteration %d, tool: %s): %s\n", iteration, tool, errorMsg)
					} else {
						msg = fmt.Sprintf("⚠️ Error (tool: %s): %s\n", tool, errorMsg)
					}
					writeEvent("content", msg)
					// Also send the raw error event for UI to handle if needed
					writeEvent("tool_error", errorEvent)
					return
				}
			}
		case ai.ReasoningEventToolResult:
			if result, ok := payload.(map[string]any); ok {
				tool, _ := result["tool"].(string)
				if text, ok := result["result"].(string); ok && strings.TrimSpace(text) != "" {
					trimmed := strings.TrimSpace(text)
					if isReducerSamplePayload(trimmed) {
						return
					}
					if isStructuredToolResult(tool) {
						contentStreamed = true
						return
					}

					contentStreamed = true
					formatted := fmt.Sprintf("🛠️ Result from %s:\n%s", tool, renderVisibleText(trimmed))
					writeEvent("content", formatted)
				}
			}
		}

		// Fallback event writer.
		writeEvent(eventType, payload)
	}

	return sendEvent, func() bool { return contentStreamed }
}

func resolveContextAndAgent(ctx context.Context, req *aiChatRequest, sendEvent func(string, any)) (ai.Agent[map[string]any], error) {
	if req.Database != "" {
		if _, err := getDBOptions(ctx, req.Database); err != nil {
			msg := fmt.Sprintf("Invalid database '%s': %v", req.Database, err)
			log.Info("Response: Invalid Database", "error", logsafe.V(msg))
			sendEvent("error", msg)
			return nil, err
		}
	}
	if req.Agent == "" {
		req.Agent = "copilot"
	}
	blueprint, exists := loadedAgents[req.Agent]
	if !exists {
		msg := fmt.Sprintf("Agent '%s' is not initialized or not found.", req.Agent)
		log.Info("Response: Agent Not Found", "error", logsafe.V(msg))
		sendEvent("error", msg)
		return nil, fmt.Errorf("agent not found")
	}
	if req.SessionID == "" {
		req.SessionID = uuid.New().String()
	}
	return blueprint, nil
}

func lockSession(req *aiChatRequest, blueprint ai.Agent[map[string]any]) (ai.Agent[map[string]any], *sync.Mutex) {
	agentSvc, sessionMu := activeSessions.GetOrCreate(req.SessionID, func() ai.Agent[map[string]any] {
		if cloneable, ok := blueprint.(interface {
			Clone() ai.Agent[map[string]any]
		}); ok {
			log.Debug("Cloned new pristine agent instance for session", "session_id", logsafe.V(req.SessionID))
			return cloneable.Clone()
		}
		return blueprint
	})
	return agentSvc, sessionMu
}

func constructPayload(ctx context.Context, w http.ResponseWriter, req *aiChatRequest, llmSettings llmSettings, sendEvent func(string, any), rs *agent.RunnerSession) (context.Context, *ai.SessionPayload, *ai.ConfigMap, string) {
	if req.Provider != "" {
		ctx = context.WithValue(ctx, ai.CtxKeyProvider, req.Provider)
	}
	ctx = context.WithValue(ctx, agent.RunnerSessionKey, rs)
	ctx = context.WithValue(ctx, ai.CtxKeyProgressSink, func(msg string) {
		sendEvent("content", msg+"\n")
	})
	ctx = context.WithValue(ctx, ai.CtxKeyEventStreamer, sendEvent)
	ctx = context.WithValue(ctx, ai.CtxKeyResultStreamer, agent.NewEventResultStreamer(sendEvent))
	ctx = context.WithValue(ctx, agent.CtxKeyJSONStreamer, agent.NewNDJSONStreamer(&DirectFlushingWriter{w: w}))
	ctx = context.WithValue(ctx, ai.CtxKeyExecutor, &DefaultToolExecutor{Agents: loadedAgents})

	cfg := ai.NewConfigMap()

	// Add LLM settings as ProviderDetails struct for the agent
	if llmSettings.Provider != "" || llmSettings.APIKey != "" {
		providerDetails := &agent.ProviderDetails{
			Provider: llmSettings.Provider,
			Model:    llmSettings.Model,
			APIKey:   ai.Secret(llmSettings.APIKey),
			BaseURL:  llmSettings.URL,
		}
		cfg.Set("provider_details", providerDetails)
	}

	if req.Database != "" {
		cfg.Set("database", req.Database)
	}
	format := req.Format
	if format == "" {
		format = "csv"
	}
	cfg.Set("default_format", format)

	databases := make(map[string]any)
	for _, dbCfg := range config.Databases {
		if opts, err := getDBOptions(ctx, dbCfg.Name); err == nil {
			databases[dbCfg.Name] = opts
		}
	}
	if req.Database != "" {
		if _, exists := databases[req.Database]; !exists {
			if opts, err := getDBOptions(ctx, req.Database); err == nil {
				databases[req.Database] = opts
			}
		}
	}

	var avatarScope string
	for i, kbRef := range req.SelectedKBs {
		if req.SelectedKBs[i].DatabaseName == "" {
			// Fallback to request's main database if DBName is not provided in reference
			req.SelectedKBs[i].DatabaseName = req.Database
		}

		resolvedOpts, err := getDBOptions(ctx, req.SelectedKBs[i].DatabaseName)
		if err == nil {
			// Use the resolved options to check for Avatar exclusivity
			if resolvedOpts.Type >= 0 {
				db := aidb.NewDatabase(resolvedOpts)
				trans, _ := db.BeginTransaction(ctx, sop.ForReading)
				if trans != nil {
					kb, err := db.OpenKnowledgeBase(ctx, kbRef.Name, trans, nil, nil, false, true)
					if err == nil {
						cfg, err := kb.GetConfig(ctx)
						if err == nil && cfg != nil && cfg.IsExclusive {
							avatarScope = kbRef.Name
							log.Info(logsafe.V("[Copilot] Avatar Mode Active. LLM Sandboxed to: " + kbRef.Name))
						}
					}
					trans.Rollback(ctx)
					if avatarScope != "" {
						break
					}
				}
			}
		}
	}

	if strings.TrimSpace(req.UserID) == "" {
		req.UserID = strings.TrimSpace(sop.GetAuthFromContext(ctx).UserID)
	}

	payload := &ai.SessionPayload{
		CurrentDB:        req.Database,
		CurrentUserQuery: req.Message,
		ActiveDomain:     req.Domain,
		SelectedKBs:      req.SelectedKBs,
		UserID:           req.UserID,
		ClientID:         req.ClientID,
		SessionID:        req.SessionID,
		AvatarScope:      avatarScope,
	}
	cfg.Set("payload", payload)

	if req.Database != "" {
		obfuscation.GlobalObfuscator.RegisterResource(req.Database, "DB")
	}
	if req.StoreName != "" {
		obfuscation.GlobalObfuscator.RegisterResource(req.StoreName, "STORE")
	}

	fullMessage := strings.TrimSpace(req.Message)
	if fullMessage == "" {
		fullMessage = req.Message
	}

	ctx = context.WithValue(ctx, "session_payload", payload)

	return ctx, payload, cfg, fullMessage
}

func executeAgentLifecycle(ctx context.Context, r *http.Request, agentSvc ai.Agent[map[string]any], req *aiChatRequest, sendEvent func(string, any)) error {
	if sysOpts, err := getSystemDBOptions(ctx); err == nil {
		sysDB := aidb.NewDatabase(sysOpts)
		kbName := memory.BuildLTMStoreName(ai.AgentIDOmni, req.UserID)
		trans, _ := sysDB.BeginTransaction(ctx, sop.ForWriting)
		if trans != nil {
			dbEmbedder := GetConfiguredEmbedder(nil)
			dbLLM := GetConfiguredLLM(r)
			sysDB.OpenKnowledgeBase(ctx, kbName, trans, dbLLM, dbEmbedder, false, true)
			trans.Commit(ctx)
			go seedMetaCognitionAsync(req.UserID, kbName, sysOpts)
		}
	}

	if err := agentSvc.Open(ctx); err != nil {
		msg := fmt.Sprintf("Agent '%s' failed to open session: %v", req.Agent, err)
		log.Error("Response: Session Open Failed", "error", logsafe.V(msg))
		sendEvent("error", msg)
		return err
	}
	return nil
}

func executeRAG(ctx context.Context, agentSvc ai.Agent[map[string]any], req *aiChatRequest, fullMessage string, cfg *ai.ConfigMap, sendEvent func(string, any)) (string, error) {
	response, err := agentSvc.Ask(ctx, fullMessage, cfg)
	if err != nil {
		msg := fmt.Sprintf("Agent '%s' failed: %v", req.Agent, err)
		log.Error("Response: Agent Ask Failed", "error", logsafe.V(msg))
		sendEvent("error", msg)
		return "", err
	}
	return response, nil
}

func interpretOutput(response string, sendEvent func(string, any)) {
	text := strings.TrimSpace(response)

	if idxStart := strings.Index(text, "<<<ACTION_REQUIRED:"); idxStart >= 0 {
		if idxEnd := strings.LastIndex(text, ">>>"); idxEnd > idxStart {
			candidate := text[idxStart+len("<<<ACTION_REQUIRED:") : idxEnd]
			var actionPayload map[string]any
			if err := json.Unmarshal([]byte(candidate), &actionPayload); err == nil {
				log.Info("Response: Action Required payload detected")
				sendEvent("action_required", actionPayload)
				return
			}
		}
	}

	var toolCall struct {
		Tool string         `json:"tool"`
		Args map[string]any `json:"args"`
	}
	cleanText := strings.TrimPrefix(text, "```json")
	cleanText = strings.TrimPrefix(cleanText, "```")
	cleanText = strings.TrimSuffix(cleanText, "```")
	cleanText = strings.TrimSpace(cleanText)

	if err := json.Unmarshal([]byte(cleanText), &toolCall); err == nil && toolCall.Tool != "" {
		log.Debug(logsafe.V(fmt.Sprintf("Agent returned raw tool call (unexpected): %s", toolCall.Tool)))
		msg := fmt.Sprintf("Agent attempted to call tool '%s' but failed to execute it internally.", toolCall.Tool)
		log.Info("Response: Raw Tool Call", "response", logsafe.V(msg))
		sendEvent("content", msg)
		return
	}

	var rawRecords []json.RawMessage
	var mapRecords []map[string]any
	if err := json.Unmarshal([]byte(cleanText), &mapRecords); err == nil && len(mapRecords) > 0 {
		if err := json.Unmarshal([]byte(cleanText), &rawRecords); err == nil {
			log.Debug("Response: Detected JSON Records", "count", len(rawRecords))
			for _, rec := range rawRecords {
				sendEvent("record", rec)
			}
			return
		}
	}

	if idxStart := strings.Index(cleanText, "["); idxStart >= 0 {
		if idxEnd := strings.LastIndex(cleanText, "]"); idxEnd > idxStart {
			candidate := cleanText[idxStart : idxEnd+1]
			var embeddedMapRecords []map[string]any
			if err := json.Unmarshal([]byte(candidate), &embeddedMapRecords); err == nil && len(embeddedMapRecords) > 0 {
				if err := json.Unmarshal([]byte(candidate), &rawRecords); err == nil {
					log.Debug("Response: Detected Embedded JSON Records", "count", len(rawRecords))
					for _, rec := range rawRecords {
						sendEvent("record", rec)
					}
					return
				}
			}
		}
	}

	var singleRecord map[string]any
	if strings.HasPrefix(cleanText, "{") {
		if err := json.Unmarshal([]byte(cleanText), &singleRecord); err == nil {
			log.Debug("Response: Detected Single JSON Record")
			sendEvent("record", singleRecord)
			return
		}
	}

	lines := strings.Split(cleanText, "\n")
	var ndjsonEvents []map[string]any
	isNDJSON := false

	if len(lines) > 0 {
		validCount := 0
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			var obj map[string]any
			if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &obj) == nil {
				ndjsonEvents = append(ndjsonEvents, obj)
				validCount++
			} else {
				isNDJSON = false
				break
			}
		}
		if validCount > 0 && len(ndjsonEvents) == validCount {
			isNDJSON = true
		}
	}

	if isNDJSON {
		log.Debug("Response: Detected NDJSON Stream", "count", len(ndjsonEvents))
		for _, evt := range ndjsonEvents {
			rawType, _ := evt["type"].(string)

			switch rawType {
			case "record":
				if rec, ok := evt["record"]; ok {
					sendEvent("record", rec)
				} else if rec, ok := evt["data"]; ok {
					sendEvent("record", rec)
				}
			case "log":
				msg, _ := evt["message"].(string)
				sendEvent("content", fmt.Sprintf("> *Log:* %s\n", msg))
			case "error":
				msg, _ := evt["error"].(string)
				sendEvent("error", msg)
			case "step_start":
				name, _ := evt["name"].(string)
				if name == "" {
					name, _ = evt["command"].(string)
				}
				stepIndexVal := evt["step_index"]

				var stepIndex int
				if f, ok := stepIndexVal.(float64); ok {
					stepIndex = int(f)
				} else if i, ok := stepIndexVal.(int); ok {
					stepIndex = i
				}

				if stepIndex > 0 {
					sendEvent("content", fmt.Sprintf("**Step %d:** `%s`\n", stepIndex, name))
				} else {
					sendEvent("content", fmt.Sprintf("**Step:** `%s`\n", name))
				}
			case "outputs":
				if list, ok := evt["outputs"].([]any); ok {
					for _, item := range list {
						sendEvent("record", item)
					}
				} else {
					b, _ := json.MarshalIndent(evt["outputs"], "", "  ")
					sendEvent("content", fmt.Sprintf("```json\n%s\n```", string(b)))
				}
			default:
				sendEvent("record", evt)
			}
		}
		return
	}

	if response != "" {
		sendEvent("content", response)
	}
}

func initAgents(ctx context.Context) error {
	return loadAgent(ctx, "copilot", "ai/data/copilot_pipeline.json")
}

func seedDefaultScripts(ctx context.Context, db *aidb.Database) {
	tx, err := db.BeginTransaction(ctx, sop.ForWriting)
	if err != nil {
		log.Error(fmt.Sprintf("Failed to begin transaction for seeding scripts: %v", err))
		return
	}
	store, err := db.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		log.Error(fmt.Sprintf("Failed to open scripts store: %v", err))
		return
	}

	// Check if demo_loop exists
	var existing ai.Script
	if err := store.Load(ctx, "general", "demo_loop", &existing); err == nil {
		tx.Rollback(ctx)
		return // Already exists
	}

	// Create demo_loop script
	demoLoop := ai.Script{
		Description: "Demonstrates loops and variables",
		Steps: []ai.ScriptStep{
			{
				Type:     "set",
				Variable: "items",
				Value:    "apple\nbanana\ncherry",
			},
			{
				Type:     "loop",
				List:     "items",
				Iterator: "fruit",
				Steps: []ai.ScriptStep{
					{
						Type:    "say",
						Message: "Processing {{.fruit}}...",
					},
					{
						Type:   "ask",
						Prompt: "What color is {{.fruit}}? (Answer in 1 word)",
					},
				},
			},
		},
	}

	if err := store.Save(ctx, "general", "demo_loop", demoLoop); err != nil {
		log.Error(fmt.Sprintf("Failed to save demo_loop script: %v", err))
		tx.Rollback(ctx)
		return
	}

	tx.Commit(ctx)
	log.Info("Seeded 'demo_loop' script.")
}

func loadAgent(ctx context.Context, key, configPath string) error {
	// ctx is passed in

	// Try to find the file in common locations, relative to where the
	// server was started and to the binary itself. Nothing here may be tied
	// to one developer's home directory.
	pathsToTry := []string{
		configPath,
		filepath.Join("..", "..", configPath), // From tools/httpserver
		filepath.Join("..", configPath),       // From tools
	}
	if exePath, err := os.Executable(); err == nil {
		pathsToTry = append(pathsToTry, filepath.Join(filepath.Dir(exePath), configPath))
	}
	if root := os.Getenv("SOP_ROOT"); root != "" {
		pathsToTry = append(pathsToTry, filepath.Join(root, configPath))
	}

	var foundPath string
	for _, p := range pathsToTry {
		if _, err := os.Stat(p); err == nil {
			foundPath = p
			break
		}
	}

	if foundPath == "" {
		log.Debug(fmt.Sprintf("Agent config not found at %s (searched parents), skipping.", configPath))
		return nil
	}

	cfg, err := agent.LoadConfigFromFile(foundPath)
	if err != nil {
		log.Debug(fmt.Sprintf("Failed to load agent config %s: %v", foundPath, err))
		return err
	}

	// Apply Global config (LLM API Key, text embedders, etc)
	injectGlobalConfig(cfg, &config)

	// Apply Global Obfuscation Mode if specified in HTTP Config
	// We do NOT update the agent config anymore, instead we calculate the per-database flag below
	globalObfMode := ObfuscationDisabled
	if config.ObfuscationMode != "" {
		log.Info("Applying Global Obfuscation Mode from HTTP config", "mode", config.ObfuscationMode)
		globalObfMode = ObfuscationMode(config.ObfuscationMode)
	}

	// Ensure absolute path for storage
	if cfg.StoragePath != "" {
		if !filepath.IsAbs(cfg.StoragePath) {
			configDir := filepath.Dir(foundPath)
			cfg.StoragePath = filepath.Join(configDir, cfg.StoragePath)
		}
		if absPath, err := filepath.Abs(cfg.StoragePath); err == nil {
			cfg.StoragePath = absPath
		}
	}

	log.Debug(fmt.Sprintf("Initializing AI Agent: %s (%s)...", cfg.Name, cfg.ID))

	// Initialize System DB
	sysOpts, err := getSystemDBOptions(ctx)
	var sysDB *aidb.Database
	if err == nil {
		pathExists := true
		if len(sysOpts.StoresFolders) > 0 {
			if _, statErr := os.Stat(sysOpts.StoresFolders[0]); os.IsNotExist(statErr) {
				pathExists = false
			}
		}
		if pathExists {
			sysDB = aidb.NewDatabase(sysOpts)
			// Seed default scripts for testing
			seedDefaultScripts(ctx, sysDB)
		} else {
			log.Warn("System DB path does not exist. Skipping AI Agent seeding to prevent auto-creation.")
		}
	} else {
		log.Debug(fmt.Sprintf("System DB not available for agent %s: %v", cfg.ID, err))
	}

	// Prepare Databases map
	databases := make(map[string]sop.DatabaseOptions)

	// Add System DB if available
	if sysDB != nil {
		databases[agent.SystemDBName] = sysOpts
	}

	for _, dbCfg := range config.Databases {
		// We need to use a copy of dbCfg because getDBOptionsFromConfig takes a pointer
		// and loop variable reuse might be an issue in older Go versions, though fixed in 1.22.
		// Safe to just pass address.
		d := dbCfg
		opts, err := getDBOptionsFromConfig(ctx, &d)
		if err == nil {
			// Calculate Obfuscation Flag based on Global Mode and Per-DB Config
			switch globalObfMode {
			case ObfuscationAllDatabases:
				opts.EnableObfuscation = true
			case ObfuscationDisabled:
				opts.EnableObfuscation = false
			case ObfuscationPerDatabase:
				opts.EnableObfuscation = dbCfg.EnableObfuscation
			default:
				// Fallback to Disabled if unknown
				opts.EnableObfuscation = false
			}

			databases[d.Name] = opts
		}
	}

	registry := make(map[string]ai.Agent[map[string]any])

	// Helper to initialize an agent from a config
	initAgent := func(agentCfg agent.Config) (ai.Agent[map[string]any], error) {
		if agentCfg.StoragePath != "" {
			if !filepath.IsAbs(agentCfg.StoragePath) {
				configDir := filepath.Dir(foundPath)
				agentCfg.StoragePath = filepath.Join(configDir, agentCfg.StoragePath)
			}
			if absPath, err := filepath.Abs(agentCfg.StoragePath); err == nil {
				agentCfg.StoragePath = absPath
			}
		}
		svc, err := agent.NewFromConfig(ctx, agentCfg, agent.Dependencies{
			AgentRegistry: registry,
			SystemDB:      sysDB,
			Databases:     databases,
		})
		return svc, err
	}

	// Pre-register internal policy agents
	for _, pCfg := range cfg.Policies {
		if pCfg.ID != "" {
			registry[pCfg.ID] = agent.NewPolicyAgent(pCfg.ID, nil, nil)
		}
	}

	// Register locally defined agents
	for _, localAgentCfg := range cfg.Agents {
		if localAgentCfg.ID == "" {
			continue
		}
		if _, exists := registry[localAgentCfg.ID]; exists {
			continue
		}

		// Dynamic Generator Override based on Environment Variables
		// This allows the "sql_core" agent to switch between Ollama/Gemini/GPT without changing the JSON file.
		if localAgentCfg.Generator.Type != "" {
			// Check for explicit override from UI (passed via context or global config?)
			// For now, we stick to Env Vars as the "Configuration" source.
			// But to allow runtime switching, we might need to re-initialize the agent or pass the provider in the Ask() call.
			// Since Ask() is generic, we can't easily pass it there without changing the interface.
			// However, we can make the Generator itself memory!

			provider := os.Getenv("AI_PROVIDER")
			if provider != "" {
				log.Debug(fmt.Sprintf("Overriding generator for agent %s to %s", localAgentCfg.ID, provider))
				localAgentCfg.Generator.Type = provider
				localAgentCfg.Generator.Options = make(map[string]any) // Clear options to rely on env vars
			}
		}

		log.Debug(fmt.Sprintf("Initializing local agent: %s...", localAgentCfg.ID))
		svc, err := initAgent(localAgentCfg)
		if err != nil {
			log.Debug(fmt.Sprintf("Failed to initialize local agent %s: %v", localAgentCfg.ID, err))
			continue
		}
		registry[localAgentCfg.ID] = svc
		loadedAgents[localAgentCfg.ID] = svc
	}

	// Initialize the main agent
	mainAgent, err := agent.NewFromConfig(ctx, *cfg, agent.Dependencies{
		AgentRegistry: registry,
		SystemDB:      sysDB,
		Databases:     databases,
	})
	if err != nil {
		log.Debug(fmt.Sprintf("Failed to initialize main agent %s: %v", key, err))
		return err
	}

	// Enable History Injection (Manual Override as requested)
	if svc, ok := mainAgent.(*agent.Service); ok {
		log.Info("Enabling History Injection for agent", "agent", key)
		svc.SetFeature("history_injection", true)
	}

	loadedAgents[key] = mainAgent
	log.Debug(fmt.Sprintf("Agent '%s' initialized successfully.", key))
	return nil
}

// DefaultToolExecutor implements ai.ToolExecutor by delegating to registered agents.
type DefaultToolExecutor struct {
	Agents map[string]ai.Agent[map[string]any]
}

func (e *DefaultToolExecutor) Execute(ctx context.Context, tool string, args map[string]any) (string, error) {
	// For now, we assume tools are handled by the "copilot" agent (CopilotAgent)
	// In a real system, we might look up the tool in a global registry or iterate agents.

	// Try copilot first
	if agentSvc, ok := e.Agents["copilot"]; ok {
		if da, ok := agentSvc.(*agent.CopilotAgent); ok {
			return da.Execute(ctx, tool, args)
		}
	}

	return "", fmt.Errorf("tool '%s' not found or no executor available", tool)
}

func injectGlobalConfig(cfg *agent.Config, globalCfg *Config) {
	if cfg == nil || globalCfg == nil {
		return
	}

	// The global config no longer owns AI secret/provider fields. Keep runtime
	// configuration in the agent config itself and only recurse into nested agents.
	// Recursive injection
	for i := range cfg.Agents {
		// We need to take address of slice element to modify it
		// But cfg.Agents is []Config.
		// wait, Config struct has []Config.
		// We can't easily recurse efficiently on value type slices in Go without pointers or rewriting the slice.
		// cfg.Agents is []Config (values).
		subCfg := &cfg.Agents[i]
		injectGlobalConfig(subCfg, globalCfg)
	}
}

func (e *DefaultToolExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	if e == nil {
		return nil, nil
	}

	if agentSvc, ok := e.Agents["copilot"]; ok {
		if te, ok := agentSvc.(interface {
			ListTools(context.Context) ([]ai.ToolDefinition, error)
		}); ok {
			tools, err := te.ListTools(ctx)
			if err != nil {
				return nil, err
			}
			if len(tools) > 0 {
				return tools, nil
			}
		}
	}

	for _, agentSvc := range e.Agents {
		if te, ok := agentSvc.(interface {
			ListTools(context.Context) ([]ai.ToolDefinition, error)
		}); ok {
			tools, err := te.ListTools(ctx)
			if err != nil {
				return nil, err
			}
			if len(tools) > 0 {
				return tools, nil
			}
		}
	}

	return nil, nil
}

func handleAIFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		MsgID       string `json:"msgId"`
		Type        string `json:"type"` // positive, negative
		AIContent   string `json:"ai_content"`
		UserContent string `json:"user_content"`
		Database    string `json:"database"`
		Agent       string `json:"agent"`
		UserID      string `json:"user_id"`
		SessionID   string `json:"session_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}

	log.Info("AI Feedback Received", "msgId", logsafe.V(req.MsgID), "type", logsafe.V(req.Type))

	ctx := r.Context()

	// 1. Get System DB Options
	// We store feedback in the 'system' database to be global across user sessions/databases
	opts, err := getDBOptions(ctx, "system")
	if err != nil {
		log.Error("Failed to get system db options", "error", err)
		http.Error(w, "System DB error", http.StatusInternalServerError)
		return
	}

	// 2. Begin Transaction
	trans, err := database.BeginTransaction(ctx, opts, sop.ForWriting)
	if err != nil {
		log.Error("Failed to begin transaction", "error", err)
		http.Error(w, "DB trans error", http.StatusInternalServerError)
		return
	}
	// Ensure rollback if not committed
	defer trans.Rollback(ctx)

	kbName := memory.BuildLTMStoreName(ai.AgentIDOmni, req.UserID)
	if req.UserID == "" {
		kbName = "llm_feedback"
	}

	// 3. Open Store "llm_feedback"
	storeName := "llm_feedback"
	// Simple string comparer
	comparer := func(a, b string) int { return strings.Compare(a, b) }

	// Configure store (auto-create if missing)
	so := sop.ConfigureStore(storeName, true, btree.DefaultSlotLength, "LLM Feedback Store", sop.SmallData, "")

	store, err := database.NewBtree[string, string](ctx, opts, storeName, trans, comparer, so)
	if err != nil {
		log.Error("Failed to open feedback store", "error", logsafe.V(err))
		http.Error(w, "Store open error", http.StatusInternalServerError)
		return
	}

	// 4. Create Payload
	id := uuid.New().String()
	payload := map[string]any{
		// "id" removed - it is the Key
		"msg_id":       req.MsgID,
		"type":         req.Type,
		"ai_content":   req.AIContent,
		"user_content": req.UserContent,
		"database":     req.Database,
		"agent":        req.Agent,
		"timestamp":    time.Now().Format(time.RFC3339),
	}

	valBytes, _ := json.Marshal(payload)
	val := string(valBytes)

	// 5. Save (Add new record) to B-Tree
	if ok, err := store.Add(ctx, id, val); err != nil {
		log.Error("Failed to save feedback", "error", err)
		http.Error(w, "Save error", http.StatusInternalServerError)
		return
	} else if !ok {
		// Collision? Retry or just fail (UUID collision unlikely)
		http.Error(w, "Save failed (collision)", http.StatusInternalServerError)
		return
	}

	// 6. Vectorize & Save to Memory KnowledgeBase (if user content is present)
	if req.UserContent != "" {
		// Create Embedder (Simple Hash for now, ideally use Agent's Embedder)
		// 384 dimensions is good balance
		embedder := GetConfiguredEmbedder(nil)

		vecs, err := embed.DocumentTexts(ctx, embedder, []string{req.UserContent})
		if err != nil {
			log.Warn("Failed to embed user content", "error", err)
			// Don't fail the request, just skip vectorization
		} else if len(vecs) > 0 {
			sysDB := aidb.NewDatabase(opts)
			kb, err := sysDB.OpenKnowledgeBase(ctx, kbName, trans, nil, embedder, false, true)
			if err != nil {
				log.Error("Failed to open physical knowledge base", "error", logsafe.V(err))
				http.Error(w, "Knowledge base open error", http.StatusInternalServerError)
				return
			}

			if err := kb.IngestThought(ctx, req.UserContent, "Feedback", "System", vecs[0], map[string]any{"data": val}); err != nil {
				log.Error("Failed to ingest memory feedback", "error", err)
				http.Error(w, "Memory ingest error", http.StatusInternalServerError)
				return
			}
			log.Info("Feedback ingested to active memory", "id", id)
		}
	}

	// 7. Commit
	if err := trans.Commit(ctx); err != nil {
		log.Error("Failed to commit feedback", "error", err)
		http.Error(w, "Commit error", http.StatusInternalServerError)
		return
	}

	log.Info("Feedback Saved", "id", id)

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "id": id})
}

// handleCloseSession allows explicitly closing an active session.
func handleCloseSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete && r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("session_id")
	if sessionID == "" {
		http.Error(w, "query param session_id is required", http.StatusBadRequest)
		return
	}

	activeSessions.Close(sessionID)
	log.Info("Closed session explicitly", "session_id", logsafe.V(sessionID))

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"success", "message":"Session closed successfully"}`))
}

func handleToolExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Tool    string `json:"tool"`
		Text    string `json:"text"`
		Intent  string `json:"intent"`
		Context string `json:"context"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		log.Error("Invalid JSON body", "error", err)
		http.Error(w, "Invalid JSON body", http.StatusBadRequest)
		return
	}

	agentSvc, exists := loadedAgents["copilot"]
	if !exists {
		http.Error(w, "Default AI agent not configured", http.StatusInternalServerError)
		return
	}

	switch req.Tool {
	case "enhance_text":
		prompt := fmt.Sprintf("Can you help me creatively enhance this text/prompt config for a %s?\n\n", strings.ReplaceAll(req.Intent, "_", " "))
		if req.Context != "" {
			prompt += fmt.Sprintf("Context / Details about the project:\n%s\n\n", req.Context)
		}
		if req.Intent == "system_prompt" {
			prompt += "IMPORTANT INSTRUCTION: The user is writing a System Prompt for an AI that interacts with a specific Knowledge Base.\n"
			prompt += "Please expand their text into a robust set of instructions that tells the AI exactly HOW to interact with, search, and synthesize the data in this knowledge base, ensuring it grounds its answers in the data.\n\n"
		}
		prompt += fmt.Sprintf("Here is what the user has drafted so far:\n%s\n\nPlease improve it into a professional, clear, and comprehensive format. Return ONLY the improved text, no conversational filler or markdown code block formatting.", req.Text)

		ctx := context.Background()

		if err := agentSvc.Open(ctx); err != nil {
			log.Error("Failed to open agent session", "error", logsafe.V(err))
			http.Error(w, "Failed to initialize AI session", http.StatusInternalServerError)
			return
		}
		defer agentSvc.Close(ctx)

		cfg := ai.NewConfigMap()
		response, err := agentSvc.Ask(ctx, prompt, cfg)
		if err != nil {
			log.Error("Failed to ask agent", "error", logsafe.V(err))
			http.Error(w, "Failed to generate text", http.StatusInternalServerError)
			return
		}

		response = strings.TrimSpace(response)
		response = strings.TrimPrefix(response, "```xml")
		response = strings.TrimPrefix(response, "```markdown")
		response = strings.TrimPrefix(response, "```json")
		response = strings.TrimPrefix(response, "```")
		response = strings.TrimSuffix(response, "```")
		response = strings.TrimSpace(response)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"enhanced_text": response,
		})
	default:
		http.Error(w, "Unknown tool", http.StatusBadRequest)
	}
}
