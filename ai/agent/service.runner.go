package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"text/template"

	log "log/slog"

	"golang.org/x/sync/errgroup"

	"github.com/sharedcode/joltrin"
	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/database"
	"github.com/sharedcode/joltrin/ai/internal/logsafe"
)

type contextKey string

const CtxKeyJSONStreamer contextKey = "json_streamer"
const CtxKeyUseNDJSON contextKey = "use_ndjson"
const CtxKeyCurrentScriptCategory contextKey = "current_script_category"
const CtxKeySuppressInternalStepStart contextKey = "suppress_internal_step_start"

type StepExecutionResult struct {
	Type    string `json:"type"`
	Command string `json:"command,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
	// Use Payload for generic message/label if needed, but Command/Prompt cover most.
	// We might want to add "Name" explicitly if strictly needed,
	// but currently the code equates Command field to the label.
	StepIndex int    `json:"step_index"`
	Result    any    `json:"result,omitempty"`
	Record    any    `json:"record,omitempty"`
	Error     string `json:"error,omitempty"`
}

type Flusher interface {
	Flush()
}

// JSONStreamer handles streaming JSON array elements.
// TODO: add a buffered batching wrapper in the future to reduce transport overhead for large result sets.
type JSONStreamer struct {
	w                 io.Writer
	mu                *sync.Mutex
	first             bool
	isNDJSON          bool
	shouldFlush       bool
	suppressStepStart bool
}

func (s *JSONStreamer) SetFlush(flush bool) {
	s.shouldFlush = flush
}

func (s *JSONStreamer) SetSuppressStepStart(suppress bool) {
	s.suppressStepStart = suppress
}

func NewJSONStreamer(w io.Writer) *JSONStreamer {
	return &JSONStreamer{
		w:     w,
		mu:    &sync.Mutex{},
		first: true,
	}
}

func NewNDJSONStreamer(w io.Writer) *JSONStreamer {
	return &JSONStreamer{
		w:        w,
		mu:       &sync.Mutex{},
		first:    true,
		isNDJSON: true,
	}
}

func (s *JSONStreamer) Write(step StepExecutionResult) {
	if s.suppressStepStart && step.Type == "step_start" {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isNDJSON {
		bytes, _ := json.Marshal(step)
		fmt.Fprintln(s.w, string(bytes))
		if s.shouldFlush {
			if f, ok := s.w.(Flusher); ok {
				f.Flush()
			}
		}
		return
	}

	if !s.first {
		fmt.Fprint(s.w, ",\n")
	}
	s.first = false

	bytes, _ := json.MarshalIndent(step, "  ", "  ")
	fmt.Fprint(s.w, string(bytes))
	if s.shouldFlush {
		if f, ok := s.w.(Flusher); ok {
			f.Flush()
		}
	}
}

// StartStreamingStep starts a new step in the JSON stream and returns a StepStreamer.
// Note: The header is written lazily when the first item is written.
func (s *JSONStreamer) StartStreamingStep(stepType, command, prompt string, stepIndex int) *StepStreamer {
	return &StepStreamer{
		parent:    s,
		first:     true,
		stepType:  stepType,
		command:   command,
		prompt:    prompt,
		stepIndex: stepIndex,
	}
}

// StepStreamer implements ai.ResultStreamer to stream result items.
type StepStreamer struct {
	parent    *JSONStreamer
	first     bool
	closed    bool
	used      bool
	stepType  string
	command   string
	prompt    string
	stepIndex int
	metadata  map[string]any
}

func (ss *StepStreamer) SetMetadata(meta map[string]any) {
	ss.metadata = meta
}

func (ss *StepStreamer) writeHeader() {
	ss.parent.mu.Lock()
	defer ss.parent.mu.Unlock()

	// Check suppression flag on parent
	if ss.parent.suppressStepStart && ss.parent.isNDJSON {
		// Suppress the step header for cleaner stream
		return
	}

	if ss.parent.isNDJSON {
		res := StepExecutionResult{
			Type:      "step_start",
			Command:   ss.command,
			Prompt:    ss.prompt,
			StepIndex: ss.stepIndex,
		}
		b, _ := json.Marshal(res)
		fmt.Fprintln(ss.parent.w, string(b))

		if f, ok := ss.parent.w.(Flusher); ok {
			f.Flush()
		}
		return
	}

	if !ss.parent.first {
		fmt.Fprint(ss.parent.w, ",\n")
	}
	ss.parent.first = false

	fmt.Fprintf(ss.parent.w, `{"type":%q`, ss.stepType)
	if ss.command != "" {
		fmt.Fprintf(ss.parent.w, `,"command":%q`, ss.command)
	}
	if ss.prompt != "" {
		fmt.Fprintf(ss.parent.w, `,"prompt":%q`, ss.prompt)
	}
	if len(ss.metadata) > 0 {
		b, _ := json.Marshal(ss.metadata)
		fmt.Fprintf(ss.parent.w, `,"metadata":%s`, string(b))
	}
	fmt.Fprint(ss.parent.w, `,"result":`)
}

func (ss *StepStreamer) BeginArray() {
	// If it's a new result set (Step 2 vs Step 1), we must ensure we are in a state to write a new array.
	// The problem is "writeHeader()" assumes we are writing "result": [ ... ].
	// If Step 1 already wrote a result, Step 2 should be a separate "step_start" event.
	// But StepStreamer is created PER step.
}

func (ss *StepStreamer) WriteItem(item any) {
	if !ss.used {
		// Auto-detect columns if not set
		if ss.metadata == nil {
			ss.metadata = make(map[string]any)
		}
		if _, ok := ss.metadata["columns"]; !ok {
			var cols []string
			if m, ok := item.(map[string]any); ok {
				for k := range m {
					cols = append(cols, k)
				}
				sort.Strings(cols)
			} else if om, ok := item.(OrderedMap); ok {
				cols = om.keys
			} else if om, ok := item.(*OrderedMap); ok {
				cols = om.keys
			}

			if len(cols) > 0 {
				ss.metadata["columns"] = cols
			}
		}

		ss.writeHeader()
		ss.used = true

		if !ss.parent.isNDJSON {
			// Start Array (Only for JSON Array mode)
			func() {
				ss.parent.mu.Lock()
				defer ss.parent.mu.Unlock()
				fmt.Fprint(ss.parent.w, "[")
			}()
		}
	}

	ss.parent.mu.Lock()
	defer ss.parent.mu.Unlock()

	if ss.parent.isNDJSON {
		res := StepExecutionResult{
			Type:   "record",
			Record: item,
		}
		b, _ := json.Marshal(res)
		fmt.Fprintln(ss.parent.w, string(b))
		if ss.parent.shouldFlush {
			if f, ok := ss.parent.w.(Flusher); ok {
				f.Flush()
			}
		}
		return
	}

	if !ss.first {
		fmt.Fprint(ss.parent.w, ",")
	}
	ss.first = false

	b, _ := json.Marshal(item)
	ss.parent.w.Write(b)
	if ss.parent.shouldFlush {
		if f, ok := ss.parent.w.(Flusher); ok {
			f.Flush()
		}
	}
}

func (ss *StepStreamer) EndArray() {
	if ss.parent.isNDJSON {
		return
	}

	if !ss.used {
		// Empty results case
		// Write Header
		ss.writeHeader()
		ss.used = true
		// Write Empty Array
		ss.parent.mu.Lock()
		defer ss.parent.mu.Unlock()
		fmt.Fprint(ss.parent.w, "[]")
		return
	}

	ss.parent.mu.Lock()
	defer ss.parent.mu.Unlock()
	fmt.Fprint(ss.parent.w, "]")
}

func (ss *StepStreamer) Close() {
	if ss.closed {
		log.Debug("StepStreamer.Close: Already closed, skipping")
		return
	}
	ss.parent.mu.Lock()
	defer ss.parent.mu.Unlock()

	if ss.parent.isNDJSON {
		ss.closed = true
		log.Debug("StepStreamer.Close: NDJSON mode, flushing")
		// Flush any buffered data before closing
		if f, ok := ss.parent.w.(Flusher); ok {
			f.Flush()
			log.Debug("StepStreamer.Close: NDJSON flushed")
		}
		return
	}

	// If the streamer wasn't used, we haven't written the header (lazy).
	// Therefore, we shouldn't write the footer '}' either, effectively skipping the step output.
	if !ss.used {
		ss.closed = true
		log.Debug("StepStreamer.Close: JSON array mode but not used, skipping")
		return
	}

	fmt.Fprint(ss.parent.w, "}")
	ss.closed = true
	log.Debug("StepStreamer.Close: JSON array mode, flushing after closing bracket")
	// Flush any buffered data before closing
	if f, ok := ss.parent.w.(Flusher); ok {
		f.Flush()
		log.Debug("StepStreamer.Close: JSON array flushed")
	}
}

func (s *Service) runStep(ctx context.Context, step ai.ScriptStep, scope map[string]any, scopeMu *sync.RWMutex, sb *strings.Builder, db *database.Database, cfg *ai.ConfigMap) error {
	switch step.Type {
	case "ask":
		return s.runStepAsk(ctx, step, scope, scopeMu, sb, db, cfg)
	case "command":
		return s.runStepCommand(ctx, step, scope, scopeMu, sb)
	case "set", "assignment":
		return s.runStepSet(ctx, step, scope, scopeMu, sb)
	case "if", "condition":
		return s.runStepIf(ctx, step, scope, scopeMu, sb, db, cfg)
	case "loop":
		return s.runStepLoop(ctx, step, scope, scopeMu, sb, db, cfg)
	// case "fetch":
	// 	return s.runStepFetch(ctx, step, scope, scopeMu, sb, db)
	case "say", "print":
		return s.runStepSay(ctx, step, scope, scopeMu, sb)
	case "call_script", "script":
		return s.runStepScript(ctx, step, scope, scopeMu, sb, db, cfg)
	case "block":
		return s.runStepBlock(ctx, step, scope, scopeMu, sb, db, cfg)
	}
	return nil
}

func (s *Service) runStepBlock(ctx context.Context, step ai.ScriptStep, scope map[string]any, scopeMu *sync.RWMutex, sb *strings.Builder, db *database.Database, cfg *ai.ConfigMap) error {
	return s.runSteps(ctx, step.Steps, scope, scopeMu, sb, db, cfg)
}

func (s *Service) runStepAsk(ctx context.Context, step ai.ScriptStep, scope map[string]any, scopeMu *sync.RWMutex, sb *strings.Builder, db *database.Database, cfg *ai.ConfigMap) error {
	w, _ := ctx.Value(ai.CtxKeyWriter).(io.Writer)

	prompt := step.Prompt
	prompt = s.resolveTemplate(prompt, scope, scopeMu)

	// Only print prompt if NOT using streamer (standard output mode)
	if ctx.Value(CtxKeyJSONStreamer) == nil {
		msg := fmt.Sprintf("> %s\n", prompt)
		sb.WriteString(msg)
		if w != nil {
			fmt.Fprint(w, msg)
		}
	}

	// SOP Design Principle: Explicit Control
	// An 'ask' step in a script is an imperative instruction to query the LLM.
	// It is never skipped unless explicitly overridden by an external "replay" context
	// (e.g. recovering session history).
	// This ensures the script engine remains "dumb and obedient," allowing complex
	// agentic workflows (Setup -> Ask -> Act) to function reliably.

	// Use provided ConfigMap, or create empty if nil
	if cfg == nil {
		cfg = ai.NewConfigMap()
	}
	if db != nil {
		cfg.Set("database", db)
	}

	// Call Ask with config (not context-based parameters!)
	resp, err := s.Ask(ctx, prompt, cfg)
	if err != nil {
		msg := fmt.Sprintf("Error: %v\n", err)
		sb.WriteString(msg)
		if w != nil {
			fmt.Fprint(w, msg)
		}
		// Continue on error, but log it.
	} else {
		// Check if resp is a tool call
		var toolCall struct {
			Tool string         `json:"tool"`
			Args map[string]any `json:"args"`
		}
		if json.Unmarshal([]byte(resp), &toolCall) == nil && toolCall.Tool != "" {
			// It's a tool call! Execute it if we have an executor.
			if executor, ok := ctx.Value(ai.CtxKeyExecutor).(ai.ToolExecutor); ok && executor != nil {
				toolResp, err := executor.Execute(ctx, toolCall.Tool, toolCall.Args)
				if err != nil {
					resp = fmt.Sprintf("Error executing tool '%s': %v", toolCall.Tool, err)
				} else {
					resp = toolResp
				}
			}
		}

		if streamer, ok := ctx.Value(CtxKeyJSONStreamer).(*JSONStreamer); ok {
			var resultAny any = resp
			// Try to unmarshal if it looks like JSON
			trimmed := strings.TrimSpace(resp)
			if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
				// Optimization: Use RawMessage
				resultAny = json.RawMessage(resp)
			}

			stepIndex, _ := ctx.Value("step_index").(int)
			streamer.Write(StepExecutionResult{
				Type:      "ask",
				Prompt:    prompt,
				Result:    resultAny,
				StepIndex: stepIndex,
			})
		} else {
			msg := fmt.Sprintf("%s\n", resp)
			sb.WriteString(msg)
			if w != nil {
				fmt.Fprint(w, msg)
			}
		}

		// Support "Variable" field for backward compatibility
		outVar := step.OutputVariable
		if outVar == "" {
			outVar = step.Variable
		}

		if outVar != "" {
			if scopeMu != nil {
				scopeMu.Lock()
				defer scopeMu.Unlock()
			}
			scope[outVar] = strings.TrimSpace(resp)
		}
	}
	return nil
}

func (s *Service) runStepCommand(ctx context.Context, step ai.ScriptStep, scope map[string]any, scopeMu *sync.RWMutex, sb *strings.Builder) error {
	w, _ := ctx.Value(ai.CtxKeyWriter).(io.Writer)

	// Create a safe writer function
	sbMu := ctx.Value("sb_mutex")
	var writeMsg func(msg string)
	if mu, ok := sbMu.(*sync.Mutex); ok && mu != nil {
		writeMsg = func(msg string) {
			mu.Lock()
			defer mu.Unlock()
			sb.WriteString(msg)
			if w != nil {
				fmt.Fprint(w, msg)
			}
		}
	} else {
		writeMsg = func(msg string) {
			sb.WriteString(msg)
			if w != nil {
				fmt.Fprint(w, msg)
			}
		}
	}

	// Resolve templates in Args (Recursive)
	resolvedArgs := make(map[string]any)
	for k, v := range step.Args {
		resolvedArgs[k] = s.resolveDeep(v, scope, scopeMu)
	}

	commandName, commandArgs := normalizeScriptCommandInvocation(step.Command, resolvedArgs)

	// Capture step for /last-tool support
	if s.session != nil {
		func() {
			s.session.mu.Lock()
			defer s.session.mu.Unlock()
			recordedStep := step
			recordedStep.Args = resolvedArgs
			s.session.LastInteractionToolCalls = append(s.session.LastInteractionToolCalls, recordedStep)
			// Also update LastStep (used by downstream step inspectors)
			s.session.LastStep = &recordedStep
		}()
	}

	// Execute Tool
	val := ctx.Value(ai.CtxKeyExecutor)
	if val == nil {
		// If no executor, but command is empty, we can skip it.
		if step.Command == "" {
			return nil
		}
		return fmt.Errorf("no tool executor available (ctx value is nil)")
	}

	executor, ok := val.(ai.ToolExecutor)
	if !ok {
		return fmt.Errorf("no tool executor available (type assertion failed, type: %T)", val)
	}

	if executor != nil {
		// Skip empty commands
		if commandName == "" {
			return nil
		}

		// Prepare streamer if available
		var stepStreamer *StepStreamer
		preAnnouncedStep := false
		if streamer, ok := ctx.Value(CtxKeyJSONStreamer).(*JSONStreamer); ok {
			stepIndex, _ := ctx.Value("step_index").(int)
			// Use Name if provided, otherwise Command
			displayName := commandName
			if step.Name != "" {
				displayName = step.Name
			}

			//&& isVerboseEnabled(ctx)
			if commandName == "execute_script" {
				streamer.Write(StepExecutionResult{
					Type:      "step_start",
					Command:   displayName,
					StepIndex: stepIndex,
				})
				ctx = context.WithValue(ctx, CtxKeySuppressInternalStepStart, true)
				preAnnouncedStep = true
			} else {
				stepStreamer = streamer.StartStreamingStep("command", displayName, "", stepIndex)
				ctx = context.WithValue(ctx, ai.CtxKeyResultStreamer, stepStreamer)
			}
		}

		resp, err := executor.Execute(ctx, commandName, commandArgs)

		// Check if tool streamed the result.
		if stepStreamer != nil && stepStreamer.used {
			stepStreamer.Close()
			if err != nil {
				return fmt.Errorf("command execution failed (streamed): %w", err)
			}
			// If streamed, we don't capture output to variable currently.
			return nil
		}

		if err != nil {
			return fmt.Errorf("command execution failed: %w", err)
		}

		if preAnnouncedStep {
			// execute_script already streams its own inner results. Re-emitting the
			// outer returned string as a synthetic record is redundant and expensive,
			// especially for large result sets, so suppress it for the wrapper path.
			if commandName != "execute_script" {
				if streamer, ok := ctx.Value(CtxKeyJSONStreamer).(*JSONStreamer); ok {
					trimmed := strings.TrimSpace(resp)
					stepIndex, _ := ctx.Value("step_index").(int)
					if trimmed != "" {
						if strings.HasPrefix(trimmed, "[") {
							var list []json.RawMessage
							if err := json.Unmarshal([]byte(trimmed), &list); err == nil {
								for _, item := range list {
									streamer.Write(StepExecutionResult{
										Type:      "record",
										Record:    item,
										StepIndex: stepIndex,
									})
								}
							} else {
								streamer.Write(StepExecutionResult{
									Type:      "record",
									Record:    resp,
									StepIndex: stepIndex,
								})
							}
						} else {
							var resultAny any = resp
							if strings.HasPrefix(trimmed, "{") {
								resultAny = json.RawMessage(resp)
							}
							streamer.Write(StepExecutionResult{
								Type:      "record",
								Record:    resultAny,
								StepIndex: stepIndex,
							})
						}
					}
				}
			}
			if step.OutputVariable != "" {
				if scopeMu != nil {
					scopeMu.Lock()
					defer scopeMu.Unlock()
				}
				scope[step.OutputVariable] = strings.TrimSpace(resp)
			}
			return nil
		}

		// Stream result if streamer is present (and wasn't used by tool)
		if streamer, ok := ctx.Value(CtxKeyJSONStreamer).(*JSONStreamer); ok {
			isVerbose := isVerboseEnabled(ctx)

			// Low-level commands that should NOT be announced as steps (system details)
			isSystemStep := false
			switch step.Command {
			case "open_db", "begin_tx", "commit_tx", "rollback_tx", "open_store":
				isSystemStep = true
			}

			if !isSystemStep {
				var resultAny any = resp

				stepIndex, _ := ctx.Value("step_index").(int)

				// Determine Display Name (reused logic)
				displayName := step.Command
				if step.Name != "" {
					displayName = step.Name
				} else if step.Command == "execute_script" {
					if n, ok := resolvedArgs["name"].(string); ok && n != "" {
						displayName = n
					}
				}

				// Optimization/UX: If the result is a JSON array (list of records),
				// we want to stream them individually or at least separate the command metadata from data.
				// This matches the "Pattern 3" request: Header -> Row -> Row ...
				trimmed := strings.TrimSpace(resp)
				if strings.HasPrefix(trimmed, "[") {
					// Parse array
					var list []json.RawMessage
					if err := json.Unmarshal([]byte(trimmed), &list); err == nil {
						// 1. Emit Step Header (only if verbose)
						if isVerbose {
							streamer.Write(StepExecutionResult{
								Type:      "step_start",
								Command:   displayName,
								StepIndex: stepIndex,
							})
						}

						// 2. Emit Records regardless of verbose; verbose only controls step narration.
						for _, item := range list {
							streamer.Write(StepExecutionResult{
								Type:      "record",
								Record:    item,
								StepIndex: stepIndex,
							})
						}
						// Return nil here strictly for the List path which is a special display-optimized path
						// (NOTE: This implies list results aren't captured in variables currently, preserving existing behavior)
						return nil
					}
				}

				// Fallback: Single Object or Primitive or Failed Array Parse
				// Try to unmarshal if it looks like JSON object to avoid escaping
				if strings.HasPrefix(trimmed, "{") {
					resultAny = json.RawMessage(resp)
				}

				// Emit as single result (default behavior for non-list outputs)
				// But maybe we should still wrap in step_start?
				// Let's emit header + 1 record
				log.Debug("runStepCommand: Emitting fallback step_start", "index", stepIndex)
				if isVerbose {
					streamer.Write(StepExecutionResult{
						Type:      "step_start",
						Command:   displayName,
						StepIndex: stepIndex,
					})
				}
				streamer.Write(StepExecutionResult{
					Type:      "record",
					Record:    resultAny,
					StepIndex: stepIndex,
				})
			}
		} else {
			// Fallback to string builder if no streamer (standard output mode)
			msg := fmt.Sprintf("%s\n", resp)
			writeMsg(msg)
		}

		// Output Variable
		if step.OutputVariable != "" {
			if scopeMu != nil {
				scopeMu.Lock()
				defer scopeMu.Unlock()
			}
			scope[step.OutputVariable] = strings.TrimSpace(resp)
		}
	} else {
		// Should be unreachable due to checks above
		return fmt.Errorf("no tool executor available")
	}
	return nil
}

func normalizeScriptCommandInvocation(command string, args map[string]any) (string, map[string]any) {
	command = strings.TrimSpace(command)
	if !strings.EqualFold(command, "call_function") {
		return command, args
	}
	if len(args) == 0 {
		return command, args
	}

	target := ""
	for _, key := range []string{"function", "tool", "name", "command"} {
		if value, ok := args[key].(string); ok && strings.TrimSpace(value) != "" {
			target = strings.TrimSpace(value)
			break
		}
	}
	if target == "" || strings.EqualFold(target, "call_function") {
		return command, args
	}

	for _, nestedKey := range []string{"args", "arguments", "params", "input"} {
		if nested, ok := args[nestedKey].(map[string]any); ok {
			cloned := make(map[string]any, len(nested))
			for k, v := range nested {
				cloned[k] = v
			}
			return target, cloned
		}
	}

	normalized := make(map[string]any)
	for k, v := range args {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "type", "command", "function", "tool", "name", "result_var":
			continue
		}
		normalized[k] = v
	}
	return target, normalized
}

func (s *Service) runStepSet(ctx context.Context, step ai.ScriptStep, scope map[string]any, scopeMu *sync.RWMutex, sb *strings.Builder) error {
	val := step.Value
	val = s.resolveTemplate(val, scope, scopeMu)

	if step.Variable != "" {
		if scopeMu != nil {
			scopeMu.Lock()
			defer scopeMu.Unlock()
		}
		scope[step.Variable] = val
	}
	return nil
}

func (s *Service) runStepIf(ctx context.Context, step ai.ScriptStep, scope map[string]any, scopeMu *sync.RWMutex, sb *strings.Builder, db *database.Database, cfg *ai.ConfigMap) error {
	w, _ := ctx.Value(ai.CtxKeyWriter).(io.Writer)

	cond := step.Condition

	// Evaluate Expression using template: {{ if .Expression }}true{{end}}
	condTmpl := fmt.Sprintf("{{if %s}}true{{end}}", cond)

	if scopeMu != nil {
		scopeMu.RLock()
		defer scopeMu.RUnlock()
	}

	if tmpl, err := template.New("cond").Parse(condTmpl); err == nil {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, scope); err == nil {
			if buf.String() == "true" {
				thenSteps := step.Then
				return s.runSteps(ctx, thenSteps, scope, scopeMu, sb, db, cfg)
			} else {
				if len(step.Else) > 0 {
					return s.runSteps(ctx, step.Else, scope, scopeMu, sb, db, cfg)
				}
			}
		} else {
			if ctx.Value(CtxKeyJSONStreamer) == nil {
				msg := fmt.Sprintf("Error evaluating condition '%s': %v\n", cond, err)
				sb.WriteString(msg)
				if w != nil {
					fmt.Fprint(w, msg)
				}
			}
		}
	}
	return nil
}

func (s *Service) runStepLoop(ctx context.Context, step ai.ScriptStep, scope map[string]any, scopeMu *sync.RWMutex, sb *strings.Builder, db *database.Database, cfg *ai.ConfigMap) error {
	listExpr := step.List
	iterator := step.Iterator
	body := step.Steps

	var val any
	var ok bool
	if scopeMu != nil {
		func() {
			scopeMu.RLock()
			defer scopeMu.RUnlock()
			val, ok = scope[listExpr]
		}()
	} else {
		val, ok = scope[listExpr]
	}

	if ok {
		// Attempt to parse string as JSON array if it looks like one
		if strVal, ok := val.(string); ok {
			trimmed := strings.TrimSpace(strVal)
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				var list []any
				if err := json.Unmarshal([]byte(strVal), &list); err == nil {
					// It's a valid JSON array, switch to slice iteration
					val = list
				}
			}
		}

		if strVal, ok := val.(string); ok {
			lines := strings.Split(strVal, "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				if scopeMu != nil {
					func() {
						scopeMu.Lock()
						defer scopeMu.Unlock()
						scope[iterator] = line
					}()
				} else {
					scope[iterator] = line
				}
				if err := s.runSteps(ctx, body, scope, scopeMu, sb, db, cfg); err != nil {
					return err
				}
			}
		} else if sliceVal, ok := val.([]any); ok {
			for _, item := range sliceVal {
				if scopeMu != nil {
					func() {
						scopeMu.Lock()
						defer scopeMu.Unlock()
						scope[iterator] = item
					}()
				} else {
					scope[iterator] = item
				}
				if err := s.runSteps(ctx, body, scope, scopeMu, sb, db, cfg); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *Service) runStepSay(ctx context.Context, step ai.ScriptStep, scope map[string]any, scopeMu *sync.RWMutex, sb *strings.Builder) error {
	w, _ := ctx.Value(ai.CtxKeyWriter).(io.Writer)

	msgText := step.Message
	msgText = s.resolveTemplate(msgText, scope, scopeMu)

	if streamer, ok := ctx.Value(CtxKeyJSONStreamer).(*JSONStreamer); ok {
		streamer.Write(StepExecutionResult{
			Type:   "say",
			Result: msgText,
		})
	} else {
		msg := fmt.Sprintf("%s\n", msgText)
		sb.WriteString(msg)
		if w != nil {
			fmt.Fprint(w, msg)
		}
	}
	return nil
}

func (s *Service) runStepScript(ctx context.Context, step ai.ScriptStep, scope map[string]any, scopeMu *sync.RWMutex, sb *strings.Builder, db *database.Database, cfg *ai.ConfigMap) error {
	name := step.ScriptName
	if name == "" {
		return fmt.Errorf("script name required")
	}

	// Load the script
	scriptDB := s.getScriptDB()
	if scriptDB == nil {
		return fmt.Errorf("no database configured")
	}

	tx, err := scriptDB.BeginTransaction(ctx, sop.ForReading)
	if err != nil {
		return fmt.Errorf("error starting transaction: %v", err)
	}

	store, err := scriptDB.OpenModelStore(ctx, "scripts", tx)
	if err != nil {
		tx.Rollback(ctx)
		return fmt.Errorf("error opening store: %v", err)
	}

	var script ai.Script
	// Parse category from script name (e.g. "finance.compute_monthly")
	targetCategory := ai.DefaultScriptCategory
	scriptName := name
	if idx := strings.Index(name, "."); idx > 0 {
		targetCategory = name[:idx]
		scriptName = name[idx+1:]
	} else {
		// No category specified, try to inherit from current context
		if curr, ok := ctx.Value(CtxKeyCurrentScriptCategory).(string); ok && curr != "" {
			targetCategory = curr
		}
	}

	// Try loading from target category
	if err := store.Load(ctx, targetCategory, scriptName, &script); err != nil {
		// If not found and we were looking in a specific category (not general), try general
		if targetCategory != ai.DefaultScriptCategory {
			if errFallback := store.Load(ctx, ai.DefaultScriptCategory, scriptName, &script); errFallback == nil {
				targetCategory = ai.DefaultScriptCategory // Found in general
				err = nil
			}
		}

		if err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("error loading script '%s': %v", name, err)
		}
	}
	tx.Commit(ctx)

	// Prepare scope for the nested script
	// We inherit the current scope, but we might want to override with ScriptArgs
	nestedScope := make(map[string]any)
	if scopeMu != nil {
		func() {
			scopeMu.RLock()
			defer scopeMu.RUnlock()
			for k, v := range scope {
				nestedScope[k] = v
			}
		}()
	} else {
		for k, v := range scope {
			nestedScope[k] = v
		}
	}

	for k, v := range step.ScriptArgs {
		// Resolve template in args
		val := s.resolveTemplate(v, scope, scopeMu)
		nestedScope[k] = val
	}

	sb.WriteString(fmt.Sprintf("Running nested script '%s'...\n", name))

	// Prepared Streamer for Nested Script (Output Handling)
	// We want to suppress 'step_start' events from the nested script so they don't clutter the UI.
	// We emit the PARENT step start (Script Step) here, then create a suppressive streamer for children.
	if streamer, ok := ctx.Value(CtxKeyJSONStreamer).(*JSONStreamer); ok {
		stepIndex, _ := ctx.Value("step_index").(int)
		isVerbose := isVerboseEnabled(ctx)

		// 1. Emit Parent Step Start (The "Run Script" step)
		// We use the script name as the command label
		// ONLY emit if verbose
		if isVerbose {
			ss := streamer.StartStreamingStep("script", name, "", stepIndex)
			ss.writeHeader()
			// We do not close ss because the step persists while children run
		}

		// 2. Create Suppressed Streamer for Child
		// We share the mutex and writer to ensure thread safety
		childStreamer := &JSONStreamer{
			w:  streamer.w,
			mu: streamer.mu, // Share the mutex pointer
			// first:             streamer.first,
			// isNDJSON:          streamer.isNDJSON, // Always NDJSON
			shouldFlush:       streamer.shouldFlush,
			suppressStepStart: true, // Suppress child step events
		}

		// Override streamer in context
		ctx = context.WithValue(ctx, CtxKeyJSONStreamer, childStreamer)
	}

	// Nested script gets its own mutex because it has its own scope map
	var nestedMu sync.RWMutex

	// Handle Database Switching for Nested Script
	// Priority: step.Database > script.Database > inherited db

	// Handle Database Override from Step
	if step.Database != "" {
		script.Database = step.Database
		script.Portable = false // Enforce the step's DB
	}

	// Update context with the category where the script was found/resolved
	ctx = context.WithValue(ctx, CtxKeyCurrentScriptCategory, targetCategory)

	return s.executeScript(ctx, &script, nestedScope, &nestedMu, sb, db, cfg)
}

// ToolProvider interface for agents that can execute tools
type ToolProvider interface {
	Execute(ctx context.Context, toolName string, args map[string]any) (string, error)
}

// ServiceToolExecutor delegates tool execution to registered agents.
// It carries explicit ToolExecutionContext to make dependencies visible.
type ServiceToolExecutor struct {
	s       *Service
	toolCtx *ToolExecutionContext
}

type chainedToolExecutor struct {
	primary  ai.ToolExecutor
	fallback ai.ToolExecutor
}

func (e *chainedToolExecutor) Execute(ctx context.Context, toolName string, args map[string]any) (string, error) {
	if e.primary != nil {
		resp, err := e.primary.Execute(ctx, toolName, args)
		if err == nil {
			return resp, nil
		}
		if !isToolUnavailableError(err) || e.fallback == nil || e.fallback == e.primary {
			return "", err
		}
	}
	if e.fallback == nil {
		return "", fmt.Errorf("tool '%s' not found in any registered agent", toolName)
	}
	return e.fallback.Execute(ctx, toolName, args)
}

func (e *chainedToolExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	if e.primary != nil {
		tools, err := e.primary.ListTools(ctx)
		if err == nil && len(tools) > 0 {
			return tools, nil
		}
	}
	if e.fallback != nil {
		return e.fallback.ListTools(ctx)
	}
	return nil, nil
}

func isToolUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unknown tool") ||
		strings.Contains(msg, "not found") ||
		strings.Contains(msg, "no executor available")
}

func (e *ServiceToolExecutor) Execute(ctx context.Context, toolName string, args map[string]any) (string, error) {
	log.Debug("Executing Tool call", "tool", logsafe.V(toolName), "args", logsafe.V(args))

	// Get or build tool execution context
	toolCtx := e.toolCtx
	if toolCtx == nil {
		// Backward compatibility: extract from context
		toolCtx = &ToolExecutionContext{
			Executor:        ctx.Value(ai.CtxKeyExecutor).(ai.ToolExecutor),
			Recorder:        nil, // will be extracted per-use
			Writer:          nil, // will be extracted per-use
			ResultStreamer:  nil, // will be extracted per-use
			NativeToolHints: ctx.Value(ai.CtxKeyNativeToolHints) == true,
			Database:        nil, // will be extracted per-use
			EventStreamer:   nil,
			ProgressSink:    nil,
		}
		if recorder, ok := ctx.Value(ai.CtxKeyScriptRecorder).(ai.ScriptRecorder); ok {
			toolCtx.Recorder = recorder
		}
		if writer, ok := ctx.Value(ai.CtxKeyWriter).(io.Writer); ok {
			toolCtx.Writer = writer
		}
		if streamer, ok := ctx.Value(ai.CtxKeyResultStreamer).(ai.ResultStreamer); ok {
			toolCtx.ResultStreamer = streamer
		}
		if streamer, ok := ctx.Value(ai.CtxKeyEventStreamer).(func(string, any)); ok {
			toolCtx.EventStreamer = streamer
		}
		if sink, ok := ctx.Value(ai.CtxKeyProgressSink).(func(string)); ok {
			toolCtx.ProgressSink = sink
		}
		if db, ok := ctx.Value(ai.CtxKeyDatabase).(*database.Database); ok {
			toolCtx.Database = db
		}
		if session, ok := ctx.Value("session_payload").(*ai.SessionPayload); ok {
			toolCtx.Session = session
		}
	}

	// Inject tool context into context for tools that haven't been refactored yet
	ctx = e.injectToolContextToLegacyContext(ctx, toolCtx)

	// Handle "gettoolinfo" explicitly as it's a meta-tool served by Service from LLM Knowledge
	if toolName == "gettoolinfo" {
		targetTool, _ := args["tool"].(string)
		if targetTool == "" {
			// Try "tool_name" or just looking for the first string arg if not named standardly
			targetTool, _ = args["tool_name"].(string)
		}
		if targetTool == "" {
			return "", fmt.Errorf("tool argument required for gettoolinfo")
		}
		return e.s.getToolInfo(ctx, targetTool)
	}

	for _, agent := range e.s.registry {
		if provider, ok := agent.(ToolProvider); ok {
			resp, err := provider.Execute(ctx, toolName, args)
			if err == nil {
				return resp, nil
			}
			// If error is "unknown tool", continue to next agent.
			// Otherwise return error.
			// Since we don't have a standard error type for "unknown tool", we check string.
			// CopilotAgent returns "unknown tool: %s".
			if strings.Contains(err.Error(), "unknown tool") {
				continue
			}
			return "", err
		}
	}
	return "", fmt.Errorf("tool '%s' not found in any registered agent", toolName)
}

// injectToolContextToLegacyContext puts ToolExecutionContext fields back into context
// for backward compatibility with tools that haven't been refactored yet.
// This is a transitional helper that will be removed once all tools are migrated.
func (e *ServiceToolExecutor) injectToolContextToLegacyContext(ctx context.Context, toolCtx *ToolExecutionContext) context.Context {
	if toolCtx == nil {
		return ctx
	}

	if toolCtx.Executor != nil {
		ctx = context.WithValue(ctx, ai.CtxKeyExecutor, toolCtx.Executor)
	}
	if toolCtx.Recorder != nil {
		ctx = context.WithValue(ctx, ai.CtxKeyScriptRecorder, toolCtx.Recorder)
	}
	if toolCtx.Writer != nil {
		ctx = context.WithValue(ctx, ai.CtxKeyWriter, toolCtx.Writer)
	}
	if toolCtx.ResultStreamer != nil {
		ctx = context.WithValue(ctx, ai.CtxKeyResultStreamer, toolCtx.ResultStreamer)
	}
	if toolCtx.NativeToolHints {
		ctx = context.WithValue(ctx, ai.CtxKeyNativeToolHints, true)
	}
	if toolCtx.Database != nil {
		ctx = context.WithValue(ctx, ai.CtxKeyDatabase, toolCtx.Database)
	}
	if toolCtx.Session != nil {
		ctx = context.WithValue(ctx, "session_payload", toolCtx.Session)
	}
	if toolCtx.EventStreamer != nil {
		ctx = context.WithValue(ctx, ai.CtxKeyEventStreamer, toolCtx.EventStreamer)
	}
	if toolCtx.ProgressSink != nil {
		ctx = context.WithValue(ctx, ai.CtxKeyProgressSink, toolCtx.ProgressSink)
	}

	return ctx
}

func (e *ServiceToolExecutor) ListTools(ctx context.Context) ([]ai.ToolDefinition, error) {
	if e == nil || e.s == nil {
		return nil, nil
	}

	var tools []ai.ToolDefinition
	for _, agent := range e.s.registry {
		if executor, ok := agent.(ai.ToolExecutor); ok {
			list, err := executor.ListTools(ctx)
			if err != nil {
				return nil, err
			}
			tools = append(tools, list...)
		}
	}

	return tools, nil
}

// injectScriptContextToLegacyContext puts ScriptRunContext fields back into context
// for backward compatibility with code that hasn't been refactored yet.
// This is a transitional helper that will be removed once all script execution is migrated.
func injectScriptContextToLegacyContext(ctx context.Context, scriptCtx *ScriptRunContext) context.Context {
	if scriptCtx == nil {
		return ctx
	}

	if scriptCtx.JSONStreamer != nil {
		ctx = context.WithValue(ctx, CtxKeyJSONStreamer, scriptCtx.JSONStreamer)
	}
	if scriptCtx.SuppressInternalStepStart {
		ctx = context.WithValue(ctx, CtxKeySuppressInternalStepStart, true)
	}
	if scriptCtx.StepIndex > 0 {
		ctx = context.WithValue(ctx, "step_index", scriptCtx.StepIndex)
	}
	if scriptCtx.UseNDJSON {
		ctx = context.WithValue(ctx, CtxKeyUseNDJSON, true)
	}
	if scriptCtx.CurrentScriptCategory != "" {
		ctx = context.WithValue(ctx, CtxKeyCurrentScriptCategory, scriptCtx.CurrentScriptCategory)
	}
	if scriptCtx.StringBuilderMutex != nil {
		ctx = context.WithValue(ctx, "sb_mutex", scriptCtx.StringBuilderMutex)
	}

	return ctx
}

// extractScriptContextFromLegacyContext extracts ScriptRunContext from context for backward compatibility
func extractScriptContextFromLegacyContext(ctx context.Context) *ScriptRunContext {
	scriptCtx := &ScriptRunContext{}

	if streamer, ok := ctx.Value(CtxKeyJSONStreamer).(*JSONStreamer); ok {
		scriptCtx.JSONStreamer = streamer
	}
	if suppress, ok := ctx.Value(CtxKeySuppressInternalStepStart).(bool); ok {
		scriptCtx.SuppressInternalStepStart = suppress
	}
	if stepIndex, ok := ctx.Value("step_index").(int); ok {
		scriptCtx.StepIndex = stepIndex
	}
	if verbose := effectiveVerbose(ctx); verbose {
		scriptCtx.Verbose = true
	}
	if useNDJSON, ok := ctx.Value(CtxKeyUseNDJSON).(bool); ok {
		scriptCtx.UseNDJSON = useNDJSON
	}
	if category, ok := ctx.Value(CtxKeyCurrentScriptCategory).(string); ok {
		scriptCtx.CurrentScriptCategory = category
	}
	if sbMu, ok := ctx.Value("sb_mutex").(*sync.Mutex); ok {
		scriptCtx.StringBuilderMutex = sbMu
	}

	return scriptCtx
}

func (s *Service) executeScript(ctx context.Context, script *ai.Script, scope map[string]any, scopeMu *sync.RWMutex, sb *strings.Builder, db *database.Database, cfg *ai.ConfigMap) error {
	// Build ScriptRunContext from existing context values (for backward compatibility)
	// or create new defaults
	scriptCtx := extractScriptContextFromLegacyContext(ctx)

	// Initialize StringBuilderMutex if not present
	if scriptCtx.StringBuilderMutex == nil {
		var sbMu sync.Mutex
		scriptCtx.StringBuilderMutex = &sbMu
	}

	// Inject ScriptRunContext back into context for legacy code
	ctx = injectScriptContextToLegacyContext(ctx, scriptCtx)

	// ScriptRecorder must be preserved in context so that tools executed by the script
	// are recorded in the session state (Session.LastStep / Session.LastInteractionToolCalls).
	// This ensures commands like /last-tool work correctly after script execution.
	// ctx = context.WithValue(ctx, ai.CtxKeyScriptRecorder, nil)

	// Ensure we have a tool executor
	serviceExecutor := &ServiceToolExecutor{s: s}
	if existing, ok := ctx.Value(ai.CtxKeyExecutor).(ai.ToolExecutor); ok && existing != nil {
		ctx = context.WithValue(ctx, ai.CtxKeyExecutor, &chainedToolExecutor{primary: existing, fallback: serviceExecutor})
	} else {
		ctx = context.WithValue(ctx, ai.CtxKeyExecutor, serviceExecutor)
	}

	// Explicit Execution Mode:
	// In SOP Architecture, scripts are explicit execution plans.
	// There is no implicit "inference" or "compilation" based on content.
	// If a script contains mixing of 'ask' (LLM) and 'command' (Deterministic) steps,
	// they are executed exactly as defined. This allows for "Hybrid Scripts" that
	// can reason, act, and reason again within a single run-loop.
	//
	// The 'isCompiled' flag is only used for explicit external replay mechanisms
	// (like restoring session state), never guessed from script content.
	isCompiled := false
	if v, ok := ctx.Value("force_compile_mode").(bool); ok {
		isCompiled = v
	}
	ctx = context.WithValue(ctx, "is_compiled", isCompiled)

	// Set Playback flag
	// wasPlayback := s.session.Playback
	s.session.Playback = true
	// defer func() { s.session.Playback = wasPlayback }()

	return s.runSteps(ctx, script.Steps, scope, scopeMu, sb, db, cfg)
}

// runSteps runs a sequence of script steps with support for control flow and variables.
func (s *Service) runSteps(ctx context.Context, steps []ai.ScriptStep, scope map[string]any, scopeMu *sync.RWMutex, sb *strings.Builder, db *database.Database, cfg *ai.ConfigMap) error {
	// Create cancellable context for this script execution scope to support stopping on error
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Ensure sb_mutex exists in context for concurrent string builder writing
	if ctx.Value("sb_mutex") == nil {
		var sbMu sync.Mutex
		ctx = context.WithValue(ctx, "sb_mutex", &sbMu)
	}

	// Use errgroup for managing concurrency and error propagation
	g, groupCtx := errgroup.WithContext(ctx)

	var syncErr error

	// Initialize currentDBName from payload
	var currentDBName string
	if p := ai.GetSessionPayload(ctx); p != nil {
		currentDBName = p.CurrentDB
	}

	for i, step := range steps {
		// Check if context is already cancelled (by errgroup or sync error)
		if groupCtx.Err() != nil {
			break
		}

		// Determine effective database for this step
		stepDBName := step.Database
		if stepDBName != "" {
			// Step specifies DB -> Update currentDBName (sticky)
			currentDBName = stepDBName
		} else {
			// Step does not specify -> Fallback to currentDBName
			stepDBName = currentDBName
		}

		log.Debug("Determined effective database for step", "step_type", step.Type, "db", logsafe.V(stepDBName))

		// Prepare Context and DB for this step
		stepCtx := groupCtx
		log.Debug("runSteps: Setting step_index", "index", i+1, "command", step.Command)
		stepCtx = context.WithValue(stepCtx, "step_index", i+1)
		stepDB := db

		// Check for dead transaction (committed or rolled back)
		// This prevents "running cmd on a dead transaction" errors if a previous step manually closed it.
		p := ai.GetSessionPayload(groupCtx)
		if p != nil && p.Transaction != nil {
			if t, ok := p.Transaction.(sop.Transaction); ok && !t.HasBegun() {
				// Transaction is dead. Clear it from the payload for this step.
				// This forces the step to start a new local transaction (or session one if we could, but local is safer fallback).
				newPayload := *p
				newPayload.Transaction = nil
				stepCtx = context.WithValue(stepCtx, "session_payload", &newPayload)
				// Update p to point to newPayload so subsequent logic uses the clean state
				p = &newPayload
			}
		}

		// Check if we need to update context/DB based on stepDBName
		if p != nil {
			// If the desired DB differs from the context, or if we just want to ensure we have the right DB object
			if p.CurrentDB != stepDBName {
				// Clone payload
				newPayload := *p
				newPayload.CurrentDB = stepDBName
				// Clear transaction if switching DB
				newPayload.Transaction = nil

				stepCtx = context.WithValue(groupCtx, "session_payload", &newPayload)
				// ...
			}
			// ...
		}

		// If async, run in errgroup
		if step.IsAsync {
			// Async Step Logic:
			// We capture the 'currentDBName' at the moment of dispatch (via stepCtx/stepDB).
			// The async step runs with a CLONED context containing that DB.
			// It does NOT affect the 'currentDBName' for subsequent steps in the loop,
			// nor does it affect other async steps. This ensures isolation.

			// Check if we are in a transaction.
			// If so, we generally force sync execution to maintain integrity for simple steps.
			// HOWEVER, if the step is a nested "script", we allow it to run async (Swarm pattern),
			// assuming it will manage its own transaction (e.g. on a different DB).
			p := ai.GetSessionPayload(stepCtx) // Use stepCtx
			if p != nil && p.Transaction != nil && step.Type != "script" {
				if ctx.Value(CtxKeyJSONStreamer) == nil {
					msg := fmt.Sprintf("Info: Step '%s' marked async but active transaction exists. Running synchronously.\n", step.Type)
					sb.WriteString(msg)
					if w, ok := ctx.Value(ai.CtxKeyWriter).(io.Writer); ok && w != nil {
						fmt.Fprint(w, msg)
					}
				}
				// Fall through to sync execution
			} else {
				// Detach transaction to enforce isolation (Swarm Computing model)
				asyncCtx := stepCtx
				asyncDB := stepDB

				if p != nil {
					// Clone payload and remove transaction
					newPayload := *p
					newPayload.Transaction = nil
					asyncCtx = context.WithValue(stepCtx, "session_payload", &newPayload)
				}

				// Capture step for closure
				st := step
				g.Go(func() error {
					// Handle panic in async step
					defer func() {
						if r := recover(); r != nil {
							// We can't easily return the panic as error here without named return,
							// but errgroup expects an error return.
							// We'll just log it (if we had a logger) and return a generic error.
							// For now, let's assume runStep handles its own panics or we let it crash?
							// Better to recover and return error to stop the group.
						}
					}()

					if err := s.runStep(asyncCtx, st, scope, scopeMu, sb, asyncDB, cfg); err != nil {
						if st.ContinueOnError && !shouldShortCircuitScriptOnError(st.Command, st.Args, err) {
							// Log error but don't stop the group
							log.Error("Async step failed (continuing)", "step_type", st.Type, "error", logsafe.V(err))
							return nil
						}
						return err // This cancels groupCtx
					}
					return nil
				})
				continue
			}
		}

		// Sync step
		if err := s.runStep(stepCtx, step, scope, scopeMu, sb, stepDB, cfg); err != nil {
			if step.ContinueOnError && !shouldShortCircuitScriptOnError(step.Command, step.Args, err) {
				// Log error and continue
				log.Error("Step failed (continuing)", "step_type", step.Type, "error", logsafe.V(err))
				continue
			}
			// Stop everything
			syncErr = err
			cancel() // Cancel context for async tasks
			break
		}
		// Synchronize state back from stepCtx cloned payload if mutated
		if stepPayload := ai.GetSessionPayload(stepCtx); stepPayload != nil && p != nil && stepPayload != p {
			//p.ExplicitTransaction = stepPayload.ExplicitTransaction
			p.Transaction = stepPayload.Transaction
			p.CurrentDB = stepPayload.CurrentDB
		}
	}

	// Wait for all async tasks
	asyncErr := g.Wait()

	if syncErr != nil {
		return syncErr
	}
	return asyncErr
}

func (s *Service) resolveTemplate(tmplStr string, scope map[string]any, scopeMu *sync.RWMutex) string {
	if tmplStr == "" {
		return ""
	}

	if scopeMu != nil {
		scopeMu.RLock()
		defer scopeMu.RUnlock()
	}

	if tmpl, err := template.New("tmpl").Parse(tmplStr); err == nil {
		var buf bytes.Buffer
		if err := tmpl.Execute(&buf, scope); err == nil {
			return buf.String()
		}
	}
	return tmplStr
}

func (s *Service) resolveDeep(v any, scope map[string]any, scopeMu *sync.RWMutex) any {
	switch val := v.(type) {
	case string:
		return s.resolveTemplate(val, scope, scopeMu)
	case map[string]any:
		newMap := make(map[string]any)
		for k, subVal := range val {
			newMap[k] = s.resolveDeep(subVal, scope, scopeMu)
		}
		return newMap
	case []any:
		newSlice := make([]any, len(val))
		for i, subVal := range val {
			newSlice[i] = s.resolveDeep(subVal, scope, scopeMu)
		}
		return newSlice
	default:
		return v
	}
}
