package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	log "log/slog"
	"net/http"
	"strings"

	"github.com/sharedcode/joltrin/ai"
	"github.com/sharedcode/joltrin/ai/internal/logsafe"
)

// gemini implements the Generator interface for Google's Gemini models.
type gemini struct {
	apiKey string
	model  string
	apiURL string
}

func init() {
	Register("gemini", func(cfg map[string]any) (ai.Generator, error) {
		apiKey, _ := cfg["api_key"].(string)
		if strings.HasPrefix(apiKey, "sk-") {
			return nil, fmt.Errorf("detected OpenAI API key (starts with 'sk-') but generator type is 'gemini'. Please check your configuration")
		}
		model, _ := cfg["model"].(string)
		if model == "" {
			model = ai.DefaultModelGemini
		}
		apiURL, _ := cfg["api_url"].(string)
		return &gemini{apiKey: apiKey, model: model, apiURL: strings.TrimSpace(apiURL)}, nil
	})
}

// Name returns the name of the generator.
func (g *gemini) Name() string { return "gemini" }

func (g *gemini) CarryoverCapability() ai.CarryoverCapability {
	return ai.CarryoverCapability{
		Provider:        g.Name(),
		Model:           strings.TrimSpace(g.model),
		SupportsCompact: true,
		SupportsLive:    true,
	}
}

func (g *gemini) ReActLoop() ai.ReActLoop {
	return geminiOwnedReActLoop{
		generator:     g,
		maxIterations: 4,
	}
}

func (g *gemini) ReActTurnStrategy() ai.ReActTurnStrategy {
	return geminiReActTurnStrategy{}
}

type geminiReActTurnStrategy struct {
	Base ai.ReActTurnStrategy
}

func (geminiReActTurnStrategy) ShouldBypassPrompt(turn ai.ReActTurn) bool {
	return len(turn.Options.ToolCallContinuations) > 0
}

func (l geminiReActTurnStrategy) PrepareTurn(ctx context.Context, turn ai.ReActTurn) ai.ReActTurn {
	base := l.Base
	if base == nil {
		base = ai.DefaultReActTurnStrategy{}
	}
	turn = base.PrepareTurn(ctx, turn)
	return prepareGeminiContinuationTurn(turn, 0)
}

type geminiOwnedReActLoop struct {
	generator ai.Generator
	// turnStrategy is optional in the owned loop and is kept only for narrow
	// test hooks or explicit local overrides. The owned loop prepares its own
	// Gemini-native continuation turns instead of depending on shared strategy logic.
	turnStrategy  ai.ReActTurnStrategy
	maxIterations int
}

func (l geminiOwnedReActLoop) Run(ctx context.Context, req ai.ReasoningRequest) (ai.ReasoningResponse, error) {
	if l.generator == nil {
		return ai.ReasoningResponse{}, fmt.Errorf("gemini owned loop requires a generator")
	}
	log.Debug("Gemini owned loop started",
		"model", geminiGeneratorModel(l.generator),
		"max_iterations", l.maxIterations,
		"query_preview", logsafe.V(geminiPreview(req.UserQuery, 240)),
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
		log.Debug("Gemini owned loop loaded tools",
			"tool_count", len(tools),
			"tools", geminiToolNames(tools),
		)
	}

	continuations := make([]ai.ToolCallContinuation, 0)

	// Restore continuations from previous Ask carryover state
	if req.CarryoverState != nil && req.CarryoverState.Mode == ai.CarryoverModeLive && req.CarryoverState.ConversationHandle != "" {
		if err := json.Unmarshal([]byte(req.CarryoverState.ConversationHandle), &continuations); err == nil {
			log.Info("Gemini restored continuations from carryover",
				"count", len(continuations),
				"estimated_tokens", req.CarryoverState.EstimatedRawToolTokens,
			)
		} else {
			log.Warn("Gemini failed to restore continuations from carryover", "error", err)
		}
	}

	toolResults := make([]ai.ReActToolResult, 0)
	executedToolCalls := make([]ai.ToolCall, 0)

	for iteration := 1; iteration <= l.maxIterations; iteration++ {
		log.Debug("Gemini owned loop iteration started",
			"iteration", iteration,
			"tool_results", len(toolResults),
			"continuations", len(continuations),
			"repair_directive", strings.TrimSpace(latestGeminiRepairDirective(toolResults)) != "",
		)
		turn := ai.ReActTurn{
			Iteration:       iteration,
			UserQuery:       req.UserQuery,
			RepairDirective: latestGeminiRepairDirective(toolResults),
			Prompt:          geminiOwnedLoopPrompt(req),
			Options: ai.GenOptions{
				SystemPrompt:          req.SystemPrompt,
				Tools:                 tools,
				Temperature:           0.0,
				ForceTemperature:      true,
				ToolCallContinuations: append([]ai.ToolCallContinuation(nil), continuations...),
			},
			ToolResults: cloneGeminiReActToolResults(toolResults),
		}
		turn = prepareGeminiContinuationTurn(turn, l.maxIterations)
		if l.turnStrategy != nil {
			turn = l.turnStrategy.PrepareTurn(ctx, turn)
		}
		if strings.TrimSpace(turn.Prompt) == "" {
			turn.Prompt = geminiOwnedLoopPrompt(req)
		}
		log.Debug("Gemini owned loop calling generator",
			"iteration", iteration,
			"tool_count", len(turn.Options.Tools),
			"continuations", len(turn.Options.ToolCallContinuations),
			"temperature", turn.Options.Temperature,
			"final_turn", turn.FinalTurn,
			"prompt_preview", logsafe.V(geminiPreview(turn.Prompt, 320)),
		)

		output, err := l.generator.Generate(ctx, turn.Prompt, turn.Options)
		if err != nil {
			log.Error("Gemini owned loop generation failed",
				"iteration", iteration,
				"error", err,
			)
			return ai.ReasoningResponse{}, fmt.Errorf("generation failed: %w", err)
		}
		log.Debug("Gemini owned loop received generator output",
			"iteration", iteration,
			"tool_calls", len(output.ToolCalls),
			"text_preview", geminiPreview(output.Text, 320),
		)
		if req.Executor == nil || len(output.ToolCalls) == 0 {
			resp := geminiOwnedLoopResponse(output.Text, executedToolCalls, toolResults, continuations, l.generator.Name(), geminiGeneratorModel(l.generator))
			log.Debug("Gemini owned loop completed without more tool calls",
				"iteration", iteration,
				"executed_tool_calls", len(executedToolCalls),
				"outcome_facts", len(resp.OutcomeFacts),
				"final_text_preview", geminiPreview(resp.FinalText, 320),
			)
			emitGeminiOwnedLoopHydration(req, resp)
			return resp, nil
		}

		for _, toolCall := range output.ToolCalls {
			emitGeminiOwnedLoopEvent(req, ai.ReasoningEventToolCall, ai.BuildToolCallEvent(toolCall.Name, cloneGeminiToolArgs(toolCall.Args), iteration))
			executedToolCalls = append(executedToolCalls, toolCall)
			toolResult, continuation := executeGeminiOwnedLoopToolCall(ctx, req, iteration, l.maxIterations, toolCall)
			toolResults = append(toolResults, toolResult)
			continuations = append(continuations, continuation)
			emitGeminiOwnedLoopHydration(req, geminiOwnedLoopResponse("", executedToolCalls, toolResults, continuations, l.generator.Name(), geminiGeneratorModel(l.generator)))
			if shouldShortCircuitGeminiOwnedLoopOnToolHint(toolResult.Hint) {
				resp := geminiOwnedLoopResponse(toolResult.Result, executedToolCalls, toolResults, continuations, l.generator.Name(), geminiGeneratorModel(l.generator))
				log.Warn("Gemini owned loop short-circuited on terminal tool hint",
					"iteration", iteration,
					"tool", toolCall.Name,
					"hint_status", logsafe.V(geminiHintStatus(toolResult.Hint)),
					"result_preview", logsafe.V(geminiPreview(toolResult.Result, 320)),
				)
				emitGeminiOwnedLoopHydration(req, resp)
				return resp, nil
			}
		}

		// Check for repeated validation errors and bail out early
		if shouldShortCircuitOnRepeatedValidationErrors(toolResults, iteration) {
			errorMsg := "I encountered repeated validation errors in the script. Please check the filter conditions and ensure they use proper operators like $eq, $gt, etc. instead of boolean placeholders."
			log.Warn("Gemini owned loop short-circuited on repeated validation errors",
				"iteration", iteration,
				"tool_results_count", len(toolResults),
			)
			// Emit error event so UI displays it immediately
			emitGeminiOwnedLoopEvent(req, ai.ReasoningEventToolError, map[string]any{
				"tool":      "execute_script",
				"error":     errorMsg,
				"iteration": iteration,
				"reason":    "repeated_validation_errors",
			})
			resp := geminiOwnedLoopResponse(errorMsg, executedToolCalls, toolResults, continuations, l.generator.Name(), geminiGeneratorModel(l.generator))
			emitGeminiOwnedLoopHydration(req, resp)
			return resp, nil
		}
	}

	finalTurn := ai.ReActTurn{
		Iteration:       l.maxIterations + 1,
		UserQuery:       req.UserQuery,
		RepairDirective: latestGeminiRepairDirective(toolResults),
		Options: ai.GenOptions{
			SystemPrompt:          req.SystemPrompt,
			Temperature:           0.2,
			ToolCallContinuations: append([]ai.ToolCallContinuation(nil), continuations...),
		},
		ToolResults: cloneGeminiReActToolResults(toolResults),
		FinalTurn:   true,
	}
	finalTurn = prepareGeminiContinuationTurn(finalTurn, l.maxIterations)
	if l.turnStrategy != nil {
		finalTurn = l.turnStrategy.PrepareTurn(ctx, finalTurn)
	}
	finalTurn.Options.Tools = nil
	if strings.TrimSpace(finalTurn.Prompt) == "" {
		finalTurn.Prompt = geminiFinalTurnPrompt(finalTurn)
	}
	log.Debug("Gemini owned loop entering final turn",
		"iteration", finalTurn.Iteration,
		"continuations", len(finalTurn.Options.ToolCallContinuations),
		"tool_results", len(finalTurn.ToolResults),
		"prompt_preview", geminiPreview(finalTurn.Prompt, 320),
	)
	output, err := l.generator.Generate(ctx, finalTurn.Prompt, finalTurn.Options)
	if err != nil {
		log.Error("Gemini owned loop final generation failed",
			"iteration", finalTurn.Iteration,
			"error", err,
		)
		return ai.ReasoningResponse{}, fmt.Errorf("final generation failed: %w", err)
	}
	resp := geminiOwnedLoopResponse(output.Text, executedToolCalls, toolResults, continuations, l.generator.Name(), geminiGeneratorModel(l.generator))
	log.Debug("Gemini owned loop finished on final turn",
		"executed_tool_calls", len(executedToolCalls),
		"outcome_facts", len(resp.OutcomeFacts),
		"final_text_preview", geminiPreview(resp.FinalText, 320),
	)
	emitGeminiOwnedLoopHydration(req, resp)
	return resp, nil
}

func geminiOwnedLoopResponse(finalText string, toolCalls []ai.ToolCall, toolResults []ai.ReActToolResult, continuations []ai.ToolCallContinuation, provider, model string) ai.ReasoningResponse {
	resp := ai.ReasoningResponse{
		FinalText:      finalText,
		ToolCalls:      toolCalls,
		OutcomeFacts:   ai.SummarizeOutcomeFacts(toolResults),
		OutcomeRecipes: ai.SummarizeOutcomeRecipes(toolResults),
	}
	if carryState := geminiOwnedLoopCarryoverState(continuations, provider, model, finalText, toolCalls, toolResults); carryState != nil {
		resp.CarryoverState = carryState
	}
	return resp
}

func latestGeminiRepairDirective(results []ai.ReActToolResult) string {
	if len(results) == 0 {
		return ""
	}
	return buildGeminiRepairDirective(results[len(results)-1])
}

func buildGeminiRepairDirective(last ai.ReActToolResult) string {
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
	if guidance := geminiValidationRepairGuidance(result); guidance != "" {
		directive += " " + guidance
	}
	if strings.Contains(directive, "Example fix:") {
		directive += " When an Example fix matches the malformed slice, copy that exact field path, predicate operator, and literal value shape into the retry instead of inventing aliases, placeholders, or paraphrased predicate objects."
	}
	return directive
}

func geminiValidationRepairGuidance(result string) string {
	lines := strings.Split(result, "\n")
	guidance := make([]string, 0, 2)
	seen := make(map[string]bool, 2)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "execute_script validation error [") {
			continue
		}
		category, message, example := geminiValidationErrorParts(line)
		if category == "" {
			continue
		}
		key := category + "|" + message + "|" + example
		if seen[key] {
			continue
		}
		seen[key] = true
		piece := fmt.Sprintf("Validation guidance (%s): %s", category, message)
		if example != "" {
			piece += fmt.Sprintf(" Example fix: %s", example)
		}
		guidance = append(guidance, piece)
		if len(guidance) >= 2 {
			break
		}
	}
	return strings.Join(guidance, " ")
}

func geminiValidationErrorParts(line string) (string, string, string) {
	const prefix = "execute_script validation error ["
	if !strings.HasPrefix(line, prefix) {
		return "", "", ""
	}
	remainder := strings.TrimPrefix(line, prefix)
	closeIdx := strings.Index(remainder, "]:")
	if closeIdx == -1 {
		return "", "", ""
	}
	category := strings.TrimSpace(remainder[:closeIdx])
	remainder = strings.TrimSpace(remainder[closeIdx+2:])
	example := ""
	if exampleIdx := strings.Index(remainder, " Example fix: "); exampleIdx >= 0 {
		example = strings.TrimSpace(remainder[exampleIdx+len(" Example fix: "):])
		remainder = strings.TrimSpace(remainder[:exampleIdx])
	}
	return category, remainder, example
}

func summarizeGeminiContinuationToolOutput(toolName string, response any, finalToolResult bool) any {
	if !strings.EqualFold(strings.TrimSpace(toolName), "execute_script") {
		return response
	}
	return map[string]any{"result": SummarizeToolResultForLLM(toolName, ExtractToolResultText(response), finalToolResult)}
}

func geminiOwnedLoopCarryoverState(continuations []ai.ToolCallContinuation, provider, model, finalText string, toolCalls []ai.ToolCall, toolResults []ai.ReActToolResult) *ai.CarryoverState {
	if len(continuations) == 0 && len(toolCalls) == 0 && strings.TrimSpace(finalText) == "" {
		return nil
	}

	state := &ai.CarryoverState{
		Mode: ai.CarryoverModeCompact,
	}
	if strings.TrimSpace(provider) != "" {
		state.Provider = strings.TrimSpace(provider)
	}
	if strings.TrimSpace(model) != "" {
		state.Model = strings.TrimSpace(model)
	}
	if summary := strings.TrimSpace(finalText); summary != "" {
		state.LastAssistantSummary = summary
	}
	if len(toolCalls) > 0 {
		state.LastToolNames = toolCallNames(toolCalls)
	}
	if len(toolResults) > 0 {
		state.LastOutcomeFacts = append([]string(nil), ai.SummarizeOutcomeFacts(toolResults)...)
		state.LastRecipeIDs = recipeIDs(ai.SummarizeOutcomeRecipes(toolResults))
	}

	if len(continuations) == 0 {
		return state
	}

	raw, err := json.Marshal(continuations)
	if err != nil {
		return state
	}
	state.Mode = ai.CarryoverModeLive
	state.ConversationHandle = string(raw)
	state.EstimatedRawToolTokens = (len(raw) + 3) / 4
	return state
}

func emitGeminiOwnedLoopHydration(req ai.ReasoningRequest, resp ai.ReasoningResponse) {
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

func geminiOwnedLoopPrompt(req ai.ReasoningRequest) string {
	parts := make([]string, 0, 3)
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

func prepareGeminiContinuationTurn(turn ai.ReActTurn, maxIterations int) ai.ReActTurn {
	if len(turn.Options.ToolCallContinuations) == 0 {
		return turn
	}
	turn.Options.ToolCallContinuations = geminiToolCallContinuations(turn, maxIterations)
	if turn.FinalTurn {
		turn.Prompt = geminiFinalTurnPrompt(turn)
		turn.Options.Tools = nil
		return turn
	}
	if strings.TrimSpace(turn.RepairDirective) != "" {
		turn.Prompt = "Continue from the supplied tool-call state. The latest tool call requires repair. Use react_state.repair_directive and the structured function response as the source of truth, emit exactly one corrected tool call next, preserve valid script slices, and change only the malformed condition or join. If the repair directive includes an Example fix for the malformed slice, apply that exact field, operator, and literal value shape directly instead of paraphrasing it. Do not answer the user, do not switch tools unless the repair directive explicitly requires it, and do not repeat placeholder-only or previously rejected argument shapes."
		return turn
	}
	turn.Prompt = "Continue from the supplied tool-call state. Use the structured function response as the source of truth, avoid replaying prior history, emit the next tool call if more work is needed, and otherwise answer the user directly."
	return turn
}

func geminiFinalTurnPrompt(turn ai.ReActTurn) string {
	if strings.TrimSpace(turn.RepairDirective) != "" {
		return "Using the supplied tool-call state, do not call more tools. If the remaining issue is a recoverable retry with no missing user fact, do not ask for permission and do not ask a clarification question. Briefly explain what is still malformed and state the corrected filter or join slice the engine would need next, using the grounded schema, joins, and predicate operators already confirmed by the tool state. If react_state.repair_directive includes an Example fix for the malformed slice, reuse that exact field, operator, and literal value shape in the correction you describe. Do not dump a full tool call, do not dump a full script, do not describe steps, and do not include placeholder or dummy fields. Ask one short, concrete clarification question only if a required user fact is genuinely missing."
	}
	return "Using the supplied tool-call state, do not call more tools. Briefly explain what is still blocking progress and ask one short, concrete clarification question."
}

func geminiToolCallContinuations(turn ai.ReActTurn, maxIterations int) []ai.ToolCallContinuation {
	continuations := turn.Options.ToolCallContinuations
	if len(continuations) == 0 {
		return nil
	}

	wrapped := make([]ai.ToolCallContinuation, 0, len(continuations))
	for idx, continuation := range continuations {
		response := continuation.Response
		if idx == len(continuations)-1 {
			phase, actions, remainingToolCalls := geminiLoopMetadata(turn, maxIterations)
			response = map[string]any{
				"tool_result": continuation.Response,
				"react_state": map[string]any{
					"user_query":           strings.TrimSpace(turn.UserQuery),
					"latest_tool":          strings.TrimSpace(continuation.ToolCall.Name),
					"repair_directive":     strings.TrimSpace(turn.RepairDirective),
					"iteration":            turn.Iteration,
					"task_status":          geminiTaskStatus(turn),
					"has_more_tool_work":   !turn.FinalTurn,
					"phase":                string(phase),
					"allowed_next_actions": geminiAllowedActions(actions),
					"remaining_tool_calls": remainingToolCalls,
				},
			}
		}
		wrapped = append(wrapped, ai.ToolCallContinuation{
			ToolCall: continuation.ToolCall,
			Response: response,
		})
	}
	return wrapped
}

func geminiTaskStatus(turn ai.ReActTurn) string {
	if turn.FinalTurn && strings.TrimSpace(turn.RepairDirective) != "" {
		return "repair_budget_exhausted"
	}
	if turn.FinalTurn {
		return "clarification_required"
	}
	if strings.TrimSpace(turn.RepairDirective) != "" {
		return "repair_required"
	}
	if len(turn.ToolResults) > 0 {
		return "tool_progressed"
	}
	return "active"
}

func geminiLoopMetadata(turn ai.ReActTurn, maxIterations int) (ai.ReActLoopPhase, []ai.ReActNextAction, int) {
	if turn.FinalTurn && strings.TrimSpace(turn.RepairDirective) != "" {
		return ai.ReActLoopPhaseRepair, []ai.ReActNextAction{ai.ReActNextActionAnswerUser, ai.ReActNextActionAskClarification}, 0
	}
	if turn.FinalTurn {
		return ai.ReActLoopPhaseClarification, []ai.ReActNextAction{ai.ReActNextActionAskClarification, ai.ReActNextActionAnswerUser}, 0
	}
	if strings.TrimSpace(turn.RepairDirective) != "" {
		return ai.ReActLoopPhaseRepair, []ai.ReActNextAction{ai.ReActNextActionRetrySameTool, ai.ReActNextActionAskClarification}, 0
	}
	remainingToolCalls := 0
	if maxIterations <= 0 {
		maxIterations = turn.LoopState.MaxIterations
		if maxIterations <= 0 {
			maxIterations = turn.LoopState.AllowedIterations
		}
	}
	if maxIterations > 0 {
		remainingToolCalls = max(0, maxIterations-turn.Iteration+1)
	}
	return ai.ReActLoopPhaseActive, []ai.ReActNextAction{ai.ReActNextActionCallTool, ai.ReActNextActionAnswerUser}, remainingToolCalls
}

func geminiAllowedActions(actions []ai.ReActNextAction) []string {
	if len(actions) == 0 {
		return nil
	}
	allowed := make([]string, 0, len(actions))
	for _, action := range actions {
		allowed = append(allowed, string(action))
	}
	return allowed
}

func shouldShortCircuitGeminiOwnedLoopOnToolHint(hint *ai.ToolProgressHint) bool {
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

func shouldShortCircuitOnRepeatedValidationErrors(toolResults []ai.ReActToolResult, currentIteration int) bool {
	// Need at least 2 consecutive failures to short-circuit
	if len(toolResults) < 2 || currentIteration < 2 {
		return false
	}

	// Check the last 2-3 tool results for validation errors
	checkCount := 3
	if len(toolResults) < checkCount {
		checkCount = len(toolResults)
	}

	consecutiveValidationErrors := 0
	for i := len(toolResults) - 1; i >= len(toolResults)-checkCount && i >= 0; i-- {
		result := toolResults[i]
		// Check if the result contains validation error keywords
		if strings.Contains(result.Result, "validation error") &&
			(strings.Contains(result.Result, "invalid_filter_placeholder") ||
				strings.Contains(result.Result, "invalid_filter_operator_placeholder") ||
				strings.Contains(result.Result, "invalid_filter_field_placeholder")) {
			consecutiveValidationErrors++
		} else {
			break // Stop if we hit a non-validation-error
		}
	}

	// Short-circuit after 2 consecutive validation errors
	return consecutiveValidationErrors >= 2
}

func cloneGeminiReActToolResults(results []ai.ReActToolResult) []ai.ReActToolResult {
	if len(results) == 0 {
		return nil
	}
	cloned := make([]ai.ReActToolResult, 0, len(results))
	for _, result := range results {
		cloned = append(cloned, ai.ReActToolResult{
			Name:   result.Name,
			Args:   cloneGeminiToolArgs(result.Args),
			Result: result.Result,
			Hint:   result.Hint,
		})
	}
	return cloned
}

func cloneGeminiToolArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(args))
	for key, value := range args {
		cloned[key] = value
	}
	return cloned
}

func executeGeminiOwnedLoopToolCall(ctx context.Context, req ai.ReasoningRequest, iteration, maxIterations int, toolCall ai.ToolCall) (ai.ReActToolResult, ai.ToolCallContinuation) {
	log.Debug("Gemini owned loop executing tool",
		"iteration", iteration,
		"tool", toolCall.Name,
		"args_preview", geminiPreviewJSON(toolCall.Args, 320),
	)
	execCtx := context.WithValue(ctx, ai.CtxKeyNativeToolHints, true)
	if req.Streamer != nil {
		execCtx = context.WithValue(execCtx, ai.CtxKeyEventStreamer, req.Streamer)
	}

	rawResult, execErr := req.Executor.Execute(execCtx, toolCall.Name, toolCall.Args)
	resultText, hint := unwrapGeminiToolResultEnvelope(rawResult)
	continuationResponse := coerceGeminiToolContinuationResponse(rawResult)
	if execErr != nil {
		log.Warn("Gemini owned loop tool execution failed",
			"iteration", iteration,
			"tool", toolCall.Name,
			"error", logsafe.V(execErr),
			"raw_result_preview", logsafe.V(geminiPreview(rawResult, 320)),
		)
		resultText = execErr.Error()

		if shouldStreamGeminiToolResult(req, iteration >= maxIterations) {
			emitGeminiOwnedLoopEvent(req, ai.ReasoningEventToolResult, ai.BuildToolResultEvent(toolCall.Name, cloneGeminiToolArgs(toolCall.Args), resultText, cloneGeminiToolProgressHint(hint), iteration))
		}
		emitGeminiOwnedLoopEvent(req, ai.ReasoningEventToolError, ai.BuildToolErrorEvent(toolCall.Name, cloneGeminiToolArgs(toolCall.Args), execErr, iteration))
		continuationResponse = map[string]any{
			"tool_error": map[string]any{
				"message": execErr.Error(),
			},
		}
	} else {
		log.Debug("Gemini owned loop tool execution completed",
			"iteration", iteration,
			"tool", toolCall.Name,
			"hint_status", logsafe.V(geminiHintStatus(hint)),
			"result_preview", logsafe.V(geminiPreview(resultText, 320)),
		)
		if shouldStreamGeminiToolResult(req, iteration >= maxIterations) {
			emitGeminiOwnedLoopEvent(req, ai.ReasoningEventToolResult, ai.BuildToolResultEvent(toolCall.Name, cloneGeminiToolArgs(toolCall.Args), resultText, cloneGeminiToolProgressHint(hint), iteration))
		}
	}

	return ai.ReActToolResult{
			Name:   toolCall.Name,
			Args:   cloneGeminiToolArgs(toolCall.Args),
			Result: resultText,
			Hint:   cloneGeminiToolProgressHint(hint),
		}, ai.ToolCallContinuation{
			ToolCall: toolCall,
			Response: continuationResponse,
		}
}

func shouldStreamGeminiToolResult(req ai.ReasoningRequest, finalToolResult bool) bool {
	if req.Streamer == nil {
		return false
	}
	return req.Verbose || finalToolResult
}

func emitGeminiOwnedLoopEvent(req ai.ReasoningRequest, eventType string, data any) {
	if req.Streamer == nil {
		return
	}
	req.Streamer(eventType, data)
}

func unwrapGeminiToolResultEnvelope(rawResult string) (string, *ai.ToolProgressHint) {
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

	result := formatGeminiEnvelopeToolResult(envelope.ToolResult)
	if strings.TrimSpace(result) == "" {
		result = rawResult
	}
	return result, cloneGeminiToolProgressHint(envelope.ProgressHint)
}

func formatGeminiEnvelopeToolResult(raw json.RawMessage) string {
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

func cloneGeminiToolProgressHint(hint *ai.ToolProgressHint) *ai.ToolProgressHint {
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

func coerceGeminiToolContinuationResponse(rawResult string) any {
	trimmed := strings.TrimSpace(rawResult)
	if trimmed == "" {
		return map[string]any{"result": ""}
	}

	var envelope ai.ToolResultEnvelope
	if json.Unmarshal([]byte(trimmed), &envelope) == nil && len(envelope.ToolResult) > 0 {
		var decoded any
		if json.Unmarshal(envelope.ToolResult, &decoded) == nil {
			return geminiFunctionResponseObject(decoded)
		}
	}

	var decoded any
	if json.Unmarshal([]byte(trimmed), &decoded) == nil {
		return geminiFunctionResponseObject(decoded)
	}

	return map[string]any{"result": rawResult}
}

func geminiFunctionResponseObject(value any) map[string]any {
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

type geminiRequest struct {
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	Contents          []geminiContent         `json:"contents"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

type geminiToolConfig struct {
	FunctionCallingConfig *geminiFunctionCallingConfig `json:"functionCallingConfig,omitempty"`
}

type geminiFunctionCallingConfig struct {
	Mode                 string   `json:"mode,omitempty"`
	AllowedFunctionNames []string `json:"allowedFunctionNames,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     float32               `json:"temperature,omitempty"`
	TopP            float32               `json:"topP,omitempty"`
	MaxOutputTokens int                   `json:"maxOutputTokens,omitempty"`
	ThinkingConfig  *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
	ResponseSchema  map[string]any        `json:"responseSchema,omitempty"`
}

type geminiThinkingConfig struct {
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunction `json:"functionDeclarations,omitempty"`
}

type geminiFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type geminiFunctionCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
	ID   string         `json:"id,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string `json:"name"`
	Response any    `json:"response,omitempty"`
	ID       string `json:"id,omitempty"`
}

type geminiResponse struct {
	Candidates []struct {
		FinishReason string `json:"finishReason,omitempty"`
		Content      struct {
			Parts []geminiPart `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	PromptFeedback *struct {
		BlockReason string `json:"blockReason,omitempty"`
	} `json:"promptFeedback,omitempty"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

var geminiAllowedSchemaKeys = map[string]struct{}{
	"description": {},
	"enum":        {},
	"items":       {},
	"properties":  {},
	"required":    {},
	"type":        {},
}

func describeGeminiEmptyResponse(resp geminiResponse) string {
	parts := []string{"no candidates returned from gemini"}
	if resp.PromptFeedback != nil && strings.TrimSpace(resp.PromptFeedback.BlockReason) != "" {
		parts = append(parts, fmt.Sprintf("block_reason=%s", resp.PromptFeedback.BlockReason))
	}
	if len(resp.Candidates) > 0 && strings.TrimSpace(resp.Candidates[0].FinishReason) != "" {
		parts = append(parts, fmt.Sprintf("finish_reason=%s", resp.Candidates[0].FinishReason))
	}
	return strings.Join(parts, "; ")
}

func extractGeminiOutput(resp geminiResponse) (ai.GenOutput, error) {
	if resp.Error != nil {
		return ai.GenOutput{}, fmt.Errorf("gemini api returned error: %s", resp.Error.Message)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return ai.GenOutput{}, fmt.Errorf("%s", describeGeminiEmptyResponse(resp))
	}

	var out ai.GenOutput
	for _, p := range resp.Candidates[0].Content.Parts {
		if p.FunctionCall != nil {
			toolCall := ai.ToolCall{
				Name:     p.FunctionCall.Name,
				Args:     p.FunctionCall.Args,
				NativeID: strings.TrimSpace(p.FunctionCall.ID),
			}
			if toolCall.NativeID != "" || strings.TrimSpace(p.ThoughtSignature) != "" {
				toolCall.TransportMeta = map[string]any{
					"provider": "gemini",
				}
				if toolCall.NativeID != "" {
					toolCall.TransportMeta["function_call_id"] = toolCall.NativeID
				}
				if signature := strings.TrimSpace(p.ThoughtSignature); signature != "" {
					toolCall.TransportMeta["thought_signature"] = signature
				}
			}
			out.ToolCalls = append(out.ToolCalls, toolCall)
		} else if p.Text != "" {
			out.Text += p.Text
		}
	}

	return out, nil
}

func buildGeminiRequest(prompt string, opts ai.GenOptions) geminiRequest {
	reqBody := geminiRequest{}

	if strings.TrimSpace(prompt) != "" {
		reqBody.Contents = append(reqBody.Contents,
			geminiContent{Role: "user", Parts: []geminiPart{{Text: prompt}}},
		)
	}

	for i, continuation := range opts.ToolCallContinuations {
		if strings.TrimSpace(continuation.ToolCall.Name) == "" {
			continue
		}

		summarizedResponse := summarizeGeminiContinuationToolOutput(continuation.ToolCall.Name, continuation.Response, i == len(opts.ToolCallContinuations)-1)
		reqBody.Contents = append(reqBody.Contents,
			geminiContent{
				Role: "model",
				Parts: []geminiPart{{FunctionCall: &geminiFunctionCall{
					Name: continuation.ToolCall.Name,
					Args: continuation.ToolCall.Args,
					ID:   strings.TrimSpace(continuation.ToolCall.NativeID),
				}, ThoughtSignature: geminiThoughtSignature(continuation.ToolCall)}},
			},
			geminiContent{
				Role: "user",
				Parts: []geminiPart{{FunctionResponse: &geminiFunctionResponse{
					Name:     continuation.ToolCall.Name,
					Response: summarizedResponse,
					ID:       strings.TrimSpace(continuation.ToolCall.NativeID),
				}}},
			},
		)
	}

	if opts.SystemPrompt != "" {
		reqBody.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: opts.SystemPrompt}},
		}
	}

	if len(opts.Tools) > 0 {
		var funcs []geminiFunction
		for _, t := range opts.Tools {
			var params map[string]any
			if t.Schema != "" && strings.HasPrefix(strings.TrimSpace(t.Schema), "{") {
				json.Unmarshal([]byte(t.Schema), &params)
			} else if t.Schema != "" {
				params = map[string]any{"type": "object"}
			}
			params = sanitizeGeminiSchema(params)
			funcs = append(funcs, geminiFunction{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  params,
			})
		}
		reqBody.Tools = []geminiTool{{FunctionDeclarations: funcs}}
		reqBody.ToolConfig = &geminiToolConfig{
			FunctionCallingConfig: &geminiFunctionCallingConfig{
				Mode:                 "ANY",
				AllowedFunctionNames: geminiAllowedFunctionNames(opts.Tools),
			},
		}
	}

	if opts.ForceTemperature || opts.Temperature > 0 || opts.TopP > 0 || opts.MaxTokens > 0 || opts.ThinkingLevel != "" || opts.ResponseSchema != nil || len(opts.Tools) > 0 {
		generatorConfig := &geminiGenerationConfig{
			Temperature:     opts.Temperature,
			TopP:            opts.TopP,
			MaxOutputTokens: opts.MaxTokens,
		}

		// Configure thinking level - auto-detect or use explicit setting
		// Only enable for models that support it (Gemini 3.x+, Gemma 4+)
		thinkingLevel := strings.ToLower(strings.TrimSpace(opts.ThinkingLevel))
		if thinkingLevel == "" && len(opts.Tools) > 0 {
			// Auto-detect: Use medium thinking level for tool-based structured tasks
			// This prevents over-analysis and creative syntax variations in tool schemas
			thinkingLevel = "medium"
		}
		if thinkingLevel != "" {
			generatorConfig.ThinkingConfig = &geminiThinkingConfig{
				ThinkingLevel: thinkingLevel,
			}
		}

		// Hard-constrain output structure if response schema is provided
		if opts.ResponseSchema != nil {
			generatorConfig.ResponseSchema = sanitizeGeminiSchema(opts.ResponseSchema)
		}

		reqBody.GenerationConfig = generatorConfig
	}

	return reqBody
}

func geminiAllowedFunctionNames(tools []ai.ToolDefinition) []string {
	if len(tools) == 0 {
		return nil
	}

	names := make([]string, 0, len(tools))
	seen := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func geminiThoughtSignature(toolCall ai.ToolCall) string {
	if toolCall.TransportMeta == nil {
		return ""
	}
	if signature, ok := toolCall.TransportMeta["thought_signature"].(string); ok {
		return strings.TrimSpace(signature)
	}
	return ""
}

func sanitizeGeminiSchema(schema map[string]any) map[string]any {
	sanitized, ok := sanitizeGeminiSchemaNode(schema)
	if !ok {
		return map[string]any{"type": "object"}
	}
	if _, ok := sanitized["type"].(string); !ok || strings.TrimSpace(sanitized["type"].(string)) == "" {
		sanitized["type"] = inferGeminiSchemaType(sanitized, "object")
	}
	return sanitized
}

func sanitizeGeminiSchemaNode(schema map[string]any) (map[string]any, bool) {
	if len(schema) == 0 {
		return nil, false
	}

	sanitized := make(map[string]any)
	for key := range geminiAllowedSchemaKeys {
		value, ok := schema[key]
		if !ok {
			continue
		}
		switch key {
		case "type":
			typeName, ok := value.(string)
			if ok && strings.TrimSpace(typeName) != "" {
				sanitized[key] = strings.ToLower(strings.TrimSpace(typeName))
			}
		case "description":
			description, ok := value.(string)
			if ok && strings.TrimSpace(description) != "" {
				sanitized[key] = description
			}
		case "enum":
			if enumValues := sanitizeGeminiEnum(value); len(enumValues) > 0 {
				sanitized[key] = enumValues
			}
		case "required":
			if required := sanitizeGeminiRequired(value); len(required) > 0 {
				sanitized[key] = required
			}
		case "properties":
			if props := sanitizeGeminiProperties(value); len(props) > 0 {
				sanitized[key] = props
			}
		case "items":
			if itemSchema, ok := value.(map[string]any); ok {
				child, childOK := sanitizeGeminiSchemaNode(itemSchema)
				if childOK {
					if _, hasType := child["type"]; !hasType {
						child["type"] = inferGeminiSchemaType(child, "string")
					}
					sanitized[key] = child
				}
			}
		}
	}

	if len(sanitized) == 0 {
		return nil, false
	}
	return sanitized, true
}

func sanitizeGeminiProperties(value any) map[string]any {
	propMap, ok := value.(map[string]any)
	if !ok || len(propMap) == 0 {
		return nil
	}
	props := make(map[string]any)
	for name, rawChild := range propMap {
		childMap, ok := rawChild.(map[string]any)
		if !ok {
			continue
		}
		child, childOK := sanitizeGeminiSchemaNode(childMap)
		if !childOK {
			continue
		}
		if _, hasType := child["type"]; !hasType {
			child["type"] = inferGeminiSchemaType(child, "string")
		}
		props[name] = child
	}
	if len(props) == 0 {
		return nil
	}
	return props
}

func sanitizeGeminiRequired(value any) []string {
	switch required := value.(type) {
	case []string:
		out := make([]string, 0, len(required))
		for _, name := range required {
			if strings.TrimSpace(name) != "" {
				out = append(out, name)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(required))
		for _, item := range required {
			name, ok := item.(string)
			if ok && strings.TrimSpace(name) != "" {
				out = append(out, name)
			}
		}
		return out
	default:
		return nil
	}
}

func sanitizeGeminiEnum(value any) []any {
	enumValues, ok := value.([]any)
	if ok && len(enumValues) > 0 {
		return enumValues
	}
	if stringValues, ok := value.([]string); ok && len(stringValues) > 0 {
		out := make([]any, 0, len(stringValues))
		for _, item := range stringValues {
			out = append(out, item)
		}
		return out
	}
	return nil
}

func inferGeminiSchemaType(schema map[string]any, fallback string) string {
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	if _, ok := schema["items"]; ok {
		return "array"
	}
	return fallback
}

// Generate sends a prompt to the Gemini API and returns the generated text.
func (g *gemini) Generate(ctx context.Context, prompt string, opts ai.GenOptions) (ai.GenOutput, error) {
	if g.apiKey == "" || g.apiKey == "YOUR_API_KEY" {
		log.Warn("Gemini generate running in stub mode",
			"model", strings.TrimSpace(g.model),
			"prompt_preview", logsafe.V(geminiPreview(prompt, 240)),
		)
		return ai.GenOutput{
			Text: fmt.Sprintf("[Gemini Stub] Missing API Key. Please provide api_key in generator configuration. Would send: %q", prompt),
		}, nil
	}

	apiURL := g.apiEndpoint()

	reqBody := buildGeminiRequest(prompt, opts)
	log.Debug("Gemini generate building request",
		"model", strings.TrimSpace(g.model),
		"prompt_preview", logsafe.V(geminiPreview(prompt, 320)),
		"tool_count", len(opts.Tools),
		"continuations", len(opts.ToolCallContinuations),
		"temperature", opts.Temperature,
	)

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		log.Error("Gemini generate request marshal failed", "error", err)
		return ai.GenOutput{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewBuffer(jsonBody))
	if err != nil {
		log.Error("Gemini generate request creation failed", "error", err)
		return ai.GenOutput{}, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", g.apiKey)

	log.Debug("Gemini generate sending request",
		"model", strings.TrimSpace(g.model),
		"request_bytes", len(jsonBody),
	)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.Error("Gemini API request failed",
			"model", strings.TrimSpace(g.model),
			"error", err,
		)
		return ai.GenOutput{}, fmt.Errorf("gemini api request failed: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	log.Debug("Gemini generate received response",
		"model", strings.TrimSpace(g.model),
		"status_code", resp.StatusCode,
		"response_bytes", len(body),
	)

	if resp.StatusCode != http.StatusOK {
		log.Error("Gemini API returned non-OK status",
			"model", strings.TrimSpace(g.model),
			"status_code", resp.StatusCode,
			"body_preview", geminiPreview(string(body), 320),
		)
		return ai.GenOutput{}, fmt.Errorf("gemini api error (status %d): %s", resp.StatusCode, string(body))
	}

	var geminiResp geminiResponse
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		log.Error("Gemini response unmarshal failed",
			"model", strings.TrimSpace(g.model),
			"error", err,
			"body_preview", geminiPreview(string(body), 320),
		)
		return ai.GenOutput{}, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	out, err := extractGeminiOutput(geminiResp)
	if err != nil {
		log.Error("Gemini response extraction failed",
			"model", strings.TrimSpace(g.model),
			"error", err,
		)
		return ai.GenOutput{}, err
	}
	log.Debug("Gemini generate parsed response",
		"model", strings.TrimSpace(g.model),
		"tool_calls", len(out.ToolCalls),
		"text_preview", geminiPreview(out.Text, 320),
	)

	// Default rough estimate
	out.TokensUsed = len(prompt) / 4
	return out, nil
}

func (g *gemini) apiEndpoint() string {
	if strings.TrimSpace(g.apiURL) == "" {
		return fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", g.model)
	}
	if strings.Contains(g.apiURL, "%s") {
		return fmt.Sprintf(g.apiURL, g.model)
	}
	return g.apiURL
}

func geminiGeneratorModel(generator ai.Generator) string {
	if provider, ok := generator.(*gemini); ok {
		return strings.TrimSpace(provider.model)
	}
	if generator == nil {
		return ""
	}
	return strings.TrimSpace(generator.Name())
}

func geminiToolNames(tools []ai.ToolDefinition) []string {
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

func geminiHintStatus(hint *ai.ToolProgressHint) string {
	if hint == nil {
		return ""
	}
	return strings.TrimSpace(hint.Status)
}

func geminiPreviewJSON(value any, maxLen int) string {
	if value == nil {
		return ""
	}
	bytes, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<unmarshalable:%T>", value)
	}
	return geminiPreview(string(bytes), maxLen)
}

func geminiPreview(text string, maxLen int) string {
	trimmed := strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if maxLen <= 0 || len(trimmed) <= maxLen {
		return trimmed
	}
	if maxLen <= 3 {
		return trimmed[:maxLen]
	}
	return trimmed[:maxLen-3] + "..."
}

// EstimateCost estimates the cost of the generation based on token usage.
func (g *gemini) EstimateCost(inTokens, outTokens int) float64 {
	// Placeholder pricing
	return float64(inTokens)*0.0001 + float64(outTokens)*0.0002
}

func (g *gemini) PrewarmCache(ctx context.Context, opts ai.GenOptions) error {
	// Gemini does not support cache pre-warming.
	return nil
}
