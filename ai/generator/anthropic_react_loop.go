package generator

import (
	"context"
	"encoding/json"
	"fmt"
	log "log/slog"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/internal/logsafe"
)

// anthropicOwnedReActLoop implements Claude's native multi-turn conversation loop
type anthropicOwnedReActLoop struct {
	generator     *anthropic
	maxIterations int
}

func (g *anthropic) ReActLoop() ai.ReActLoop {
	return anthropicOwnedReActLoop{
		generator:     g,
		maxIterations: 4,
	}
}

func (l anthropicOwnedReActLoop) Run(ctx context.Context, req ai.ReasoningRequest) (ai.ReasoningResponse, error) {
	if l.generator == nil {
		return ai.ReasoningResponse{}, fmt.Errorf("anthropic owned loop requires a generator")
	}
	log.Debug("Anthropic owned loop started",
		"model", l.generator.model,
		"max_iterations", l.maxIterations,
		"query_preview", logsafe.V(anthropicPreview(req.UserQuery, 240)),
		"has_context", strings.TrimSpace(req.ContextText) != "",
		"has_history", strings.TrimSpace(req.HistoryText) != "",
	)

	var tools []ai.ToolDefinition
	var err error
	if req.Executor != nil {
		tools, err = req.Executor.ListTools(ctx)
		if err != nil {
			return ai.ReasoningResponse{}, fmt.Errorf("failed to list tools: %w", err)
		}
		log.Debug("Anthropic owned loop loaded tools",
			"tool_count", len(tools),
			"tools", anthropicToolNames(tools),
		)
	}

	continuations, err := restoreAnthropicContinuations(req.CarryoverState)
	if err != nil {
		log.Warn("Anthropic failed to restore continuations from carryover", "error", err)
	} else if len(continuations) > 0 {
		log.Info("Anthropic restored continuations from carryover",
			"count", len(continuations),
			"estimated_tokens", req.CarryoverState.EstimatedRawToolTokens,
		)
	}

	toolResults := make([]ai.ReActToolResult, 0)
	executedToolCalls := make([]ai.ToolCall, 0)

	for iteration := 1; iteration <= l.maxIterations; iteration++ {
		log.Debug("Anthropic owned loop iteration started",
			"iteration", iteration,
			"tool_results", len(toolResults),
			"continuations", len(continuations),
			"repair_directive", strings.TrimSpace(latestAnthropicRepairDirective(toolResults)) != "",
		)

		prompt := anthropicOwnedLoopPrompt(req)
		opts := ai.GenOptions{
			SystemPrompt:          req.SystemPrompt,
			Tools:                 tools,
			Temperature:           0.0,
			ForceTemperature:      true,
			ToolCallContinuations: append([]ai.ToolCallContinuation(nil), continuations...),
		}

		log.Debug("Anthropic owned loop calling generator",
			"iteration", iteration,
			"tool_count", len(opts.Tools),
			"continuations", len(opts.ToolCallContinuations),
			"temperature", opts.Temperature,
			"prompt_preview", logsafe.V(anthropicPreview(prompt, 320)),
		)

		output, err := l.generator.Generate(ctx, prompt, opts)
		if err != nil {
			log.Error("Anthropic owned loop generation failed",
				"iteration", iteration,
				"error", err,
			)
			return ai.ReasoningResponse{}, fmt.Errorf("generation failed: %w", err)
		}
		log.Debug("Anthropic owned loop received generator output",
			"iteration", iteration,
			"tool_calls", len(output.ToolCalls),
			"text_preview", anthropicPreview(output.Text, 320),
		)

		if req.Executor == nil || len(output.ToolCalls) == 0 {
			resp := anthropicOwnedLoopResponse(output.Text, executedToolCalls, toolResults, continuations)
			log.Debug("Anthropic owned loop completed without more tool calls",
				"iteration", iteration,
				"executed_tool_calls", len(executedToolCalls),
				"outcome_facts", len(resp.OutcomeFacts),
				"final_text_preview", anthropicPreview(resp.FinalText, 320),
			)
			emitAnthropicOwnedLoopHydration(req, resp)
			return resp, nil
		}

		for _, toolCall := range output.ToolCalls {
			emitAnthropicOwnedLoopEvent(req, ai.ReasoningEventToolCall, ai.BuildToolCallEvent(toolCall.Name, cloneAnthropicToolArgs(toolCall.Args), iteration))
			executedToolCalls = append(executedToolCalls, toolCall)
			toolResult, continuation := executeAnthropicOwnedLoopToolCall(ctx, req, iteration, l.maxIterations, toolCall)
			toolResults = append(toolResults, toolResult)
			continuations = append(continuations, continuation)
			emitAnthropicOwnedLoopHydration(req, anthropicOwnedLoopResponse("", executedToolCalls, toolResults, continuations))
			if shouldShortCircuitAnthropicOwnedLoopOnToolHint(toolResult.Hint) {
				resp := anthropicOwnedLoopResponse(toolResult.Result, executedToolCalls, toolResults, continuations)
				log.Warn("Anthropic owned loop short-circuited on terminal tool hint",
					"iteration", iteration,
					"tool", toolCall.Name,
					"hint_status", logsafe.V(anthropicHintStatus(toolResult.Hint)),
					"result_preview", logsafe.V(anthropicPreview(toolResult.Result, 320)),
				)
				emitAnthropicOwnedLoopHydration(req, resp)
				return resp, nil
			}
		}

		// Check for repeated validation errors and bail out early
		if shouldShortCircuitOnRepeatedValidationErrors(toolResults, iteration) {
			errorMsg := "I encountered repeated validation errors in the script. Please check the filter conditions and ensure they use proper operators like $eq, $gt, etc. instead of boolean placeholders."
			log.Warn("Anthropic owned loop short-circuited on repeated validation errors",
				"iteration", iteration,
				"tool_results_count", len(toolResults),
			)
			emitAnthropicOwnedLoopEvent(req, ai.ReasoningEventToolError, map[string]any{
				"tool":      "execute_script",
				"error":     errorMsg,
				"iteration": iteration,
				"reason":    "repeated_validation_errors",
			})
			resp := anthropicOwnedLoopResponse(errorMsg, executedToolCalls, toolResults, continuations)
			emitAnthropicOwnedLoopHydration(req, resp)
			return resp, nil
		}
	}

	// Final turn without tools
	finalPrompt := anthropicOwnedLoopPrompt(req)
	finalOpts := ai.GenOptions{
		SystemPrompt:          req.SystemPrompt,
		Temperature:           0.2,
		ToolCallContinuations: append([]ai.ToolCallContinuation(nil), continuations...),
	}
	log.Debug("Anthropic owned loop entering final turn",
		"iteration", l.maxIterations+1,
		"continuations", len(finalOpts.ToolCallContinuations),
		"tool_results", len(toolResults),
		"prompt_preview", logsafe.V(anthropicPreview(finalPrompt, 320)),
	)

	output, err := l.generator.Generate(ctx, finalPrompt, finalOpts)
	if err != nil {
		log.Error("Anthropic owned loop final generation failed",
			"iteration", l.maxIterations+1,
			"error", err,
		)
		return ai.ReasoningResponse{}, fmt.Errorf("final generation failed: %w", err)
	}
	resp := anthropicOwnedLoopResponse(output.Text, executedToolCalls, toolResults, continuations)
	log.Debug("Anthropic owned loop finished on final turn",
		"executed_tool_calls", len(executedToolCalls),
		"outcome_facts", len(resp.OutcomeFacts),
		"final_text_preview", anthropicPreview(resp.FinalText, 320),
	)
	emitAnthropicOwnedLoopHydration(req, resp)
	return resp, nil
}

func anthropicOwnedLoopResponse(finalText string, toolCalls []ai.ToolCall, toolResults []ai.ReActToolResult, continuations []ai.ToolCallContinuation) ai.ReasoningResponse {
	resp := ai.ReasoningResponse{
		FinalText:      finalText,
		ToolCalls:      toolCalls,
		OutcomeFacts:   ai.SummarizeOutcomeFacts(toolResults),
		OutcomeRecipes: ai.SummarizeOutcomeRecipes(toolResults),
	}
	if carryState := anthropicOwnedLoopCarryoverState(continuations); carryState != nil {
		resp.CarryoverState = carryState
	}
	return resp
}

func restoreAnthropicContinuations(state *ai.CarryoverState) ([]ai.ToolCallContinuation, error) {
	if state == nil || state.Mode != ai.CarryoverModeLive {
		return nil, nil
	}

	payload := strings.TrimSpace(state.ConversationID)
	if payload == "" {
		payload = strings.TrimSpace(state.ConversationHandle)
	}
	if payload == "" {
		return nil, nil
	}

	continuations := make([]ai.ToolCallContinuation, 0)
	if err := json.Unmarshal([]byte(payload), &continuations); err != nil {
		return nil, err
	}
	return continuations, nil
}

func anthropicOwnedLoopCarryoverState(continuations []ai.ToolCallContinuation) *ai.CarryoverState {
	if len(continuations) == 0 {
		return nil
	}
	raw, err := json.Marshal(continuations)
	if err != nil {
		return &ai.CarryoverState{Mode: ai.CarryoverModeCompact}
	}

	// Anthropic's documented continuity model is based on replaying the accumulated
	// message/tool history and prompt caching prefixes. Keep the serialized fallback
	// payload in ConversationHandle only; do not synthesize a ConversationID that
	// implies a provider-native server thread that this implementation does not have.
	return &ai.CarryoverState{
		Mode:                   ai.CarryoverModeLive,
		ConversationHandle:     string(raw),
		EstimatedRawToolTokens: (len(raw) + 3) / 4,
	}
}

func anthropicOwnedLoopPrompt(req ai.ReasoningRequest) string {
	parts := make([]string, 0, 6)

	if state := req.CarryoverState; state != nil {
		if previousAsk := strings.TrimSpace(state.LastUserQuery); previousAsk != "" {
			parts = append(parts, "Continuing from the original ask:\n"+previousAsk)
		}
		if summary := strings.TrimSpace(state.LastAssistantSummary); summary != "" {
			parts = append(parts, "Prior answer summary:\n"+summary)
		}
	}

	if contextText := strings.TrimSpace(req.ContextText); contextText != "" {
		parts = append(parts, "Context:\n"+contextText)
	}
	if historyText := strings.TrimSpace(req.HistoryText); historyText != "" {
		parts = append(parts, "History:\n"+historyText)
	}
	if query := strings.TrimSpace(req.UserQuery); query != "" {
		parts = append(parts, "User Query: "+query)
	}
	return strings.Join(parts, "\n\n")
}

func latestAnthropicRepairDirective(results []ai.ReActToolResult) string {
	if len(results) == 0 {
		return ""
	}
	return buildAnthropicRepairDirective(results[len(results)-1])
}

func buildAnthropicRepairDirective(last ai.ReActToolResult) string {
	result := strings.TrimSpace(last.Result)
	if result == "" {
		return ""
	}
	if strings.Contains(result, "Clarification required:") {
		return "Clarification directive: The last tool failure still remains unresolved after the first repair attempt. Do not call another tool yet. Ask the user one short, concrete clarification question that names the blocker and wait for the answer."
	}
	if !strings.Contains(result, "Retry instruction:") && !strings.Contains(result, "execute_script validation error [") {
		return ""
	}
	if strings.Contains(result, "Repair strategy: research_first") {
		return fmt.Sprintf("Repair directive: The last tool call to %s failed because grounded schema or relation facts are still missing. Your next step should be to call list_stores first, prefer scoped stores:[...] for likely targets, reuse its schema/relations output as the source of truth, and only then return to %s if needed. Do not summarize the error as a final answer unless correction is impossible.", last.Name, last.Name)
	}
	directive := fmt.Sprintf("Repair directive: The last tool call to %s failed because its arguments were invalid. Your next step should be to call the same tool again with corrected arguments using the repair guidance below. Preserve valid script slices and change only the malformed condition or join. Do not summarize the error as a final answer unless correction is impossible.", last.Name)
	return directive
}

func executeAnthropicOwnedLoopToolCall(ctx context.Context, req ai.ReasoningRequest, iteration, maxIterations int, toolCall ai.ToolCall) (ai.ReActToolResult, ai.ToolCallContinuation) {
	log.Debug("Anthropic owned loop executing tool",
		"iteration", iteration,
		"tool", toolCall.Name,
		"args_preview", anthropicPreviewJSON(toolCall.Args, 320),
	)
	execCtx := context.WithValue(ctx, ai.CtxKeyNativeToolHints, true)
	if req.Streamer != nil {
		execCtx = context.WithValue(execCtx, ai.CtxKeyEventStreamer, req.Streamer)
	}

	rawResult, execErr := req.Executor.Execute(execCtx, toolCall.Name, toolCall.Args)
	resultText, hint := unwrapAnthropicToolResultEnvelope(rawResult)
	continuationResponse := coerceAnthropicToolContinuationResponse(rawResult)
	if execErr != nil {
		log.Warn("Anthropic owned loop tool execution failed",
			"iteration", iteration,
			"tool", toolCall.Name,
			"error", logsafe.V(execErr),
			"raw_result_preview", logsafe.V(anthropicPreview(rawResult, 320)),
		)
		resultText = execErr.Error()
		if shouldStreamAnthropicToolResult(req, iteration >= maxIterations) {
			emitAnthropicOwnedLoopEvent(req, ai.ReasoningEventToolResult, ai.BuildToolResultEvent(toolCall.Name, cloneAnthropicToolArgs(toolCall.Args), resultText, cloneAnthropicToolProgressHint(hint), iteration))
		}
		emitAnthropicOwnedLoopEvent(req, ai.ReasoningEventToolError, ai.BuildToolErrorEvent(toolCall.Name, cloneAnthropicToolArgs(toolCall.Args), execErr, iteration))
		continuationResponse = map[string]any{
			"tool_error": map[string]any{
				"message": execErr.Error(),
			},
		}
	} else {
		log.Debug("Anthropic owned loop tool execution completed",
			"iteration", iteration,
			"tool", toolCall.Name,
			"hint_status", logsafe.V(anthropicHintStatus(hint)),
			"result_preview", logsafe.V(anthropicPreview(resultText, 320)),
		)
		if shouldStreamAnthropicToolResult(req, iteration >= maxIterations) {
			emitAnthropicOwnedLoopEvent(req, ai.ReasoningEventToolResult, ai.BuildToolResultEvent(toolCall.Name, cloneAnthropicToolArgs(toolCall.Args), resultText, cloneAnthropicToolProgressHint(hint), iteration))
		}
	}

	return ai.ReActToolResult{
			Name:   toolCall.Name,
			Args:   cloneAnthropicToolArgs(toolCall.Args),
			Result: resultText,
			Hint:   cloneAnthropicToolProgressHint(hint),
		}, ai.ToolCallContinuation{
			ToolCall: toolCall,
			Response: continuationResponse,
		}
}

func unwrapAnthropicToolResultEnvelope(rawResult string) (string, *ai.ToolProgressHint) {
	trimmed := strings.TrimSpace(rawResult)
	if trimmed == "" {
		return rawResult, nil
	}

	var envelope ai.ToolResultEnvelope
	if err := json.Unmarshal([]byte(trimmed), &envelope); err != nil {
		return rawResult, nil
	}
	if len(envelope.ToolResult) == 0 && envelope.ProgressHint == nil {
		return rawResult, nil
	}

	result := formatAnthropicEnvelopeToolResult(envelope.ToolResult)
	if strings.TrimSpace(result) == "" {
		result = rawResult
	}
	return result, cloneAnthropicToolProgressHint(envelope.ProgressHint)
}

func formatAnthropicEnvelopeToolResult(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}

	var value any
	if err := json.Unmarshal(raw, &value); err == nil {
		if bytes, marshalErr := json.Marshal(value); marshalErr == nil {
			return string(bytes)
		}
	}

	return string(raw)
}

func coerceAnthropicToolContinuationResponse(rawResult string) any {
	trimmed := strings.TrimSpace(rawResult)
	if trimmed == "" {
		return map[string]any{"result": ""}
	}

	var envelope ai.ToolResultEnvelope
	if json.Unmarshal([]byte(trimmed), &envelope) == nil && len(envelope.ToolResult) > 0 {
		var decoded any
		if json.Unmarshal(envelope.ToolResult, &decoded) == nil {
			return anthropicFunctionResponseObject(decoded)
		}
	}

	var decoded any
	if json.Unmarshal([]byte(trimmed), &decoded) == nil {
		return anthropicFunctionResponseObject(decoded)
	}

	return map[string]any{"result": rawResult}
}

func anthropicFunctionResponseObject(value any) map[string]any {
	switch typed := value.(type) {
	case nil:
		return map[string]any{"result": nil}
	case map[string]any:
		return typed
	case string:
		return map[string]any{"result": typed}
	default:
		return map[string]any{"result": typed}
	}
}

func shouldStreamAnthropicToolResult(req ai.ReasoningRequest, finalToolResult bool) bool {
	if req.Streamer == nil {
		return false
	}
	return req.Verbose || finalToolResult
}

func emitAnthropicOwnedLoopEvent(req ai.ReasoningRequest, eventType string, data any) {
	if req.Streamer == nil {
		return
	}
	req.Streamer(eventType, data)
}

func emitAnthropicOwnedLoopHydration(req ai.ReasoningRequest, resp ai.ReasoningResponse) {
	if req.HydrationSink == nil {
		return
	}
	req.HydrationSink(ai.BuildMemoryHydrationUpdateFromParts(ai.MemoryHydrationUpdate{
		FinalText:      resp.FinalText,
		ToolCalls:      resp.ToolCalls,
		OutcomeFacts:   resp.OutcomeFacts,
		OutcomeRecipes: resp.OutcomeRecipes,
		CarryoverState: resp.CarryoverState,
	}))
}

func shouldShortCircuitAnthropicOwnedLoopOnToolHint(hint *ai.ToolProgressHint) bool {
	if hint == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(hint.Status)) {
	case "anti_success", "blocked", "error", "failed", "fatal", "hard_error", "terminal_error":
		return true
	default:
		return false
	}
}

func cloneAnthropicToolArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(args))
	for key, value := range args {
		cloned[key] = value
	}
	return cloned
}

func cloneAnthropicToolProgressHint(hint *ai.ToolProgressHint) *ai.ToolProgressHint {
	if hint == nil {
		return nil
	}

	cloned := *hint
	cloned.Missing = append([]string(nil), hint.Missing...)
	cloned.Tips = append([]string(nil), hint.Tips...)
	cloned.Clues = append([]string(nil), hint.Clues...)
	cloned.SuggestedNextTools = append([]string(nil), hint.SuggestedNextTools...)
	return &cloned
}

func anthropicToolNames(tools []ai.ToolDefinition) []string {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func anthropicHintStatus(hint *ai.ToolProgressHint) string {
	if hint == nil {
		return ""
	}
	return strings.TrimSpace(hint.Status)
}

func anthropicPreviewJSON(value any, maxLen int) string {
	if value == nil {
		return ""
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<unmarshalable:%T>", value)
	}
	return anthropicPreview(string(bytes), maxLen)
}

func anthropicPreview(text string, maxLen int) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if maxLen <= 0 || len(trimmed) <= maxLen {
		return trimmed
	}
	if maxLen <= 3 {
		return trimmed[:maxLen]
	}
	return trimmed[:maxLen-3] + "..."
}
